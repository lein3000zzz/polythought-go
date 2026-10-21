// Package llm calls the language model providers (phase 4) with their official SDKs:
// Anthropic, OpenAI and any OpenAI-compatible API, and Google's Gemini, each with the
// key of the user whose workflow asks (D243). The SDKs are kept from the server's
// environment: no ANTHROPIC_*, OPENAI_* or GOOGLE_* variable adds a key, a header or a
// base URL to a user's request.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics/redkit"
	"github.com/lein3000zzz/the-watchers/pkg/observability/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/lein3000zzz/polythought-go/internal/adapters/webhook"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ ports.LLM = (*Client)(nil)

const (
	tracerName = "github.com/lein3000zzz/polythought-go/internal/adapters/llm"

	opComplete = "complete"
	opModels   = "models"

	// timeout bounds one call: a long answer takes minutes.
	timeout    = 10 * time.Minute
	maxRetries = 2
)

// The providers' own addresses.
const (
	anthropicURL = "https://api.anthropic.com/"
	openAIURL    = "https://api.openai.com/v1/"
	geminiURL    = "https://generativelanguage.googleapis.com/"
)

// ErrInsecureURL is a base URL in plain http to a public address: the key would cross
// the internet in clear.
var ErrInsecureURL = errors.New("the base URL must be https, unless private addresses are allowed for a server on your network")

type Config struct {
	// AllowPrivate lets an OpenAI-compatible base URL be an internal address, for a model
	// served on the same machine or network: Private addresses, the webhooks' setting. Asked on every connection; nil means never.
	AllowPrivate func() bool
}

// Client is the providers, behind one port.
type Client struct {
	public  *http.Client // to the providers' own addresses
	guarded *http.Client // to an address a user chose, in https: public ones, unless private are allowed
	local   *http.Client // to one in plain http: inside the network only, and only while allowed
	private func() bool

	// The providers' addresses: theirs, but for tests.
	anthropicURL, openAIURL, geminiURL string
	kit                                *redkit.Kit
	scope                              tracing.Scope
	log                                logging.Logger
}

func New(util *observability.Utility, cfg Config) (*Client, error) {
	log := util.Logger.With("component", "llm")

	kit, err := redkit.Register(redkit.Params{
		Manager:   util.MetricsManager,
		Logger:    log,
		Subsystem: "llm",
		Buckets:   redkit.MediumLongBuckets,
		Component: "llm",
	})
	if err != nil {
		return nil, fmt.Errorf("llm: register metrics: %w", err)
	}

	private := cfg.AllowPrivate
	if private == nil {
		private = func() bool { return false }
	}

	// No otelhttp: the kit's spans name the call, and the URLs stay out of traces.
	return &Client{
		public:       &http.Client{Timeout: timeout},
		guarded:      dialing(webhook.GuardedDialer(private)),
		local:        dialing(webhook.InternalDialer(private)),
		private:      private,
		anthropicURL: anthropicURL, openAIURL: openAIURL, geminiURL: geminiURL,
		kit:   kit,
		scope: tracing.Scope{Manager: util.TracerManager, TracerName: tracerName},
		log:   log,
	}, nil
}

// errRedirected is a redirect from an address a user chose: not followed, as Go would
// send the key on to the same host in plain http.
var errRedirected = errors.New("the API answered with a redirect: give the address it redirects to as the base URL")

// dialing is a client to addresses users chose, through dialer, following no redirect;
// no proxy, which would dial instead and hide the address.
func dialing(dialer *net.Dialer) *http.Client {
	return &http.Client{
		Timeout:       timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return errRedirected },
		Transport: &http.Transport{
			Proxy: nil, DialContext: dialer.DialContext, ForceAttemptHTTP2: true,
			TLSHandshakeTimeout: 10 * time.Second, IdleConnTimeout: 90 * time.Second,
		},
	}
}

func (c *Client) Complete(ctx context.Context, key ports.LLMKey, req ports.LLMRequest) (res ports.LLMResponse, err error) {
	done := c.kit.Start(ctx, opComplete)
	defer func() { done(err == nil) }()

	return tracing.InScope(ctx, c.scope, opComplete, func(ctx context.Context, span trace.Span) (ports.LLMResponse, error) {
		span.SetAttributes(attribute.String("llm.provider", string(key.Provider)), attribute.String("llm.model", req.Model))

		cl, err := prepare(req)
		if err != nil {
			return ports.LLMResponse{}, err
		}

		var res ports.LLMResponse

		switch key.Provider {
		case ports.LLMAnthropic:
			res, err = c.anthropicComplete(ctx, key, cl)
			err = anthropicError(err)
		case ports.LLMOpenAI:
			var base string
			if base, err = c.openAIBase(key); err == nil {
				res, err = c.openAIComplete(ctx, key, base, cl)
				err = c.openAIError(err, base)
			}
		case ports.LLMGemini:
			res, err = c.geminiComplete(ctx, key, cl)
			err = geminiError(err)
		default:
			err = fmt.Errorf("llm: no provider %q", key.Provider)
		}

		if err != nil {
			return ports.LLMResponse{}, scrub(err, key)
		}

		span.SetAttributes(attribute.Int64("llm.tokens.input", res.Usage.InputTokens), attribute.Int64("llm.tokens.output", res.Usage.OutputTokens))

		return res, nil
	})
}

func (c *Client) Models(ctx context.Context, key ports.LLMKey) (models []ports.LLMModel, err error) {
	done := c.kit.Start(ctx, opModels)
	defer func() { done(err == nil) }()

	return tracing.InScope(ctx, c.scope, opModels, func(ctx context.Context, span trace.Span) ([]ports.LLMModel, error) {
		span.SetAttributes(attribute.String("llm.provider", string(key.Provider)))

		var (
			models []ports.LLMModel
			err    error
		)

		switch key.Provider {
		case ports.LLMAnthropic:
			models, err = c.anthropicModels(ctx, key)
			err = anthropicError(err)
		case ports.LLMOpenAI:
			var base string
			if base, err = c.openAIBase(key); err == nil {
				models, err = c.openAIModels(ctx, key, base)
				err = c.openAIError(err, base)
			}
		case ports.LLMGemini:
			models, err = c.geminiModels(ctx, key)
			err = geminiError(err)
		default:
			err = fmt.Errorf("llm: no provider %q", key.Provider)
		}

		if err != nil {
			return nil, scrub(err, key)
		}

		return models, nil
	})
}

// openAIBase is where an OpenAI-compatible key's API is: OpenAI's own unless it says,
// and in https unless private addresses are allowed, for a server on the network; plain
// http then reaches no public address (the local client).
func (c *Client) openAIBase(key ports.LLMKey) (string, error) {
	if key.BaseURL == "" {
		return c.openAIURL, nil
	}

	u, err := ports.ParseLLMBaseURL(key.BaseURL)
	if err != nil {
		return "", err
	}

	if u.Scheme != "https" && !c.private() {
		return "", ErrInsecureURL
	}

	return strings.TrimRight(u.String(), "/") + "/", nil
}

// call is a request, its schemas read as the SDKs take them.
type call struct {
	req    ports.LLMRequest
	schema map[string]any // nil for none
	tools  []tool
}

type tool struct {
	ports.LLMTool
	schema map[string]any
}

func prepare(req ports.LLMRequest) (call, error) {
	schema, err := decodeSchema(req.Schema)
	if err != nil {
		return call{}, err
	}

	cl := call{req: req, schema: schema}

	for _, t := range req.Tools {
		s, err := decodeSchema(t.Schema)
		if err != nil || s == nil {
			return call{}, fmt.Errorf("llm: tool %q: its schema must be a JSON object", t.Name)
		}

		cl.tools = append(cl.tools, tool{LLMTool: t, schema: s})
	}

	return cl, nil
}

// strict is whether a schema fits the providers' strict modes, which then hold the
// model to it: every object closed, with additionalProperties false, and requiring all
// its properties. One that does not is sent as a guide only, rather than refused.
func strict(schema any) bool {
	switch s := schema.(type) {
	case map[string]any:
		if s["type"] == "object" || s["properties"] != nil {
			if s["additionalProperties"] != false {
				return false
			}

			props, _ := s["properties"].(map[string]any)
			required, _ := s["required"].([]any)

			for name := range props {
				if !slices.Contains(required, any(name)) {
					return false
				}
			}
		}

		for _, v := range s {
			if !strict(v) {
				return false
			}
		}
	case []any:
		for _, v := range s {
			if !strict(v) {
				return false
			}
		}
	}

	return true
}

// decodeSchema is a request's JSON schema as the SDKs take it: an object, or nil for
// none.
func decodeSchema(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil || schema == nil {
		return nil, errors.New("llm: the schema must be a JSON object")
	}

	return schema, nil
}

// maxMessage bounds a provider's message: a proxy in the way may answer with a page.
const maxMessage = 300

// answered is a provider's refusal in its own words: who, the status and its message,
// without the URL or the request's IDs.
func answered(who string, status int, message string) error {
	message = strings.Join(strings.Fields(message), " ")
	if message == "" {
		message = http.StatusText(status)
	}

	if r := []rune(message); len(r) > maxMessage {
		message = string(r[:maxMessage]) + "…"
	}

	return fmt.Errorf("%s answered %d: %s", who, status, message)
}

// scrub keeps a key out of an error, whatever quoted it.
func scrub(err error, key ports.LLMKey) error {
	if key.APIKey == "" || !strings.Contains(err.Error(), key.APIKey) {
		return err
	}

	return errors.New(strings.ReplaceAll(err.Error(), key.APIKey, "<key>"))
}
