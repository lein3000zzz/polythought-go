package output

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/credtypes"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/fields"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const WebhookType = "output.webhook"

const (
	// maxRequests bounds the requests one run sends, so a long list cannot flood a chat.
	maxRequests = 20

	// maxMessage keeps a combined message under chat apps' limits (Discord's is 2000).
	maxMessage = 1900
)

var _ engine.Node = (*Webhook)(nil)

// Webhook posts items to a URL: as a chat message, which Discord, Slack and many other
// services accept, or as the items' JSON. It passes on the items it delivered.
type Webhook struct {
	sender      ports.WebhookSender
	credentials ports.CredentialReader // nil where there is no vault, e.g. the demo
}

func NewWebhook(sender ports.WebhookSender, credentials ports.CredentialReader) *Webhook {
	return &Webhook{sender: sender, credentials: credentials}
}

func (*Webhook) Type() engine.NodeType {
	return engine.NodeType{
		Name:  WebhookType,
		Label: "Webhook",
		Description: "Sends items to a web address: a message to a Discord or Slack channel, or the " +
			"items as JSON to any service that takes them.",
		Inputs:  []string{engine.DefaultPort},
		Outputs: []string{engine.DefaultPort},
		Items:   shape.Passed(),
		Params: []engine.ParamSpec{
			{
				Name: "url", Label: "Webhook URL", Kind: engine.ParamString, Required: true, Sensitive: true,
				Description: "From the channel's integration settings in Discord or Slack.",
			},
			{
				Name: "message", Label: "Message", Kind: engine.ParamString, Default: "Update from your Polythought workflow",
				Description: "{{field}} puts in an item's field, e.g. {{question}} or {{spread}}.",
			},
			{
				Name: "send", Label: "Send", Kind: engine.ParamString, Default: "all", Options: []string{"all", "each"},
				Description: "all: one request listing every item. each: one request per item, up to 20 a run.",
			},
			{
				Name: "format", Label: "Format", Kind: engine.ParamString, Default: "message", Options: []string{"message", "json"},
				Description: "message: the text above, for chat apps. json: the items themselves.",
			},
			{
				Name: "credential", Label: "Credential", Kind: engine.ParamCredential, CredentialType: credtypes.HTTPHeader,
				Description: "Optional. A header sent with each request, such as Authorization, from your Credentials page.",
			},
		},
	}
}

type webhookConfig struct {
	url, message, send, format string
	header                     map[string]string
}

func (n *Webhook) Run(ctx context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	out := engine.Ports{}

	items := in[engine.DefaultPort]
	if len(items) == 0 {
		return out, nil
	}

	cfg, err := webhookSettings(rc)
	if err != nil {
		return nil, err
	}

	if cfg.header, err = n.header(ctx, rc); err != nil {
		return nil, err
	}

	if cfg.send == "each" {
		for i, item := range items {
			src := engine.ItemRef{Port: engine.DefaultPort, Index: i}

			if i >= maxRequests {
				out.Fail(item, src, fmt.Errorf("not sent: at most %d requests a run; send them all in one instead", maxRequests))
				continue
			}

			n.deliver(ctx, cfg, []engine.Item{item}, []engine.ItemRef{src}, out)
		}

		return out, nil
	}

	srcs := make([]engine.ItemRef, len(items))
	for i := range items {
		srcs[i] = engine.ItemRef{Port: engine.DefaultPort, Index: i}
	}

	n.deliver(ctx, cfg, items, srcs, out)

	return out, nil
}

// deliver sends items in one request and passes them on, or fails them all.
func (n *Webhook) deliver(ctx context.Context, cfg webhookConfig, items []engine.Item, srcs []engine.ItemRef, out engine.Ports) {
	body, err := webhookBody(cfg, items)
	if err == nil {
		err = n.sender.Post(ctx, cfg.url, body, cfg.header)
	}

	for i, item := range items {
		if err != nil {
			out.Fail(item, srcs[i], err)
			continue
		}

		out.Add(engine.DefaultPort, engine.Item{JSON: item.JSON, Source: []engine.ItemRef{srcs[i]}})
	}
}

// webhookSettings reads the settings once: they apply to the whole run.
func webhookSettings(rc engine.RunContext) (webhookConfig, error) {
	p, err := rc.Params.Resolve(engine.ItemRef{Port: engine.DefaultPort})
	if err != nil {
		return webhookConfig{}, err
	}

	var cfg webhookConfig

	for name, dst := range map[string]*string{"url": &cfg.url, "message": &cfg.message, "send": &cfg.send, "format": &cfg.format} {
		if *dst, err = p.String(name); err != nil {
			return webhookConfig{}, err
		}
	}

	return cfg, nil
}

// header reads the credential, if the node has one, as the header it adds.
func (n *Webhook) header(ctx context.Context, rc engine.RunContext) (map[string]string, error) {
	p, err := rc.Params.Resolve(engine.ItemRef{Port: engine.DefaultPort})
	if err != nil {
		return nil, err
	}

	if p["credential"] == nil {
		return nil, nil
	}

	id, err := p.String("credential")
	if err != nil {
		return nil, err
	}

	if n.credentials == nil {
		return nil, errors.New("credentials are not available here")
	}

	values, err := n.credentials.Values(ctx, rc.OwnerID, domain.CredentialID(id), credtypes.HTTPHeader)
	if err != nil {
		return nil, fmt.Errorf("credential: %w", err)
	}

	return map[string]string{values["header"]: values["value"]}, nil
}

// webhookBody is {"content", "text"} for messages, read by Discord and Slack
// respectively, or the item, or {"items": [...]}, for JSON.
func webhookBody(cfg webhookConfig, items []engine.Item) ([]byte, error) {
	if cfg.format == "json" {
		if len(items) == 1 && cfg.send == "each" {
			return json.Marshal(items[0].JSON)
		}

		all := make([]map[string]any, len(items))
		for i, it := range items {
			all[i] = it.JSON
		}

		return json.Marshal(map[string]any{"items": all})
	}

	lines := make([]string, len(items))
	for i, it := range items {
		lines[i] = fields.Render(cfg.message, it.JSON)
	}

	text := truncate(strings.Join(lines, "\n"), maxMessage)

	return json.Marshal(map[string]string{"content": text, "text": text})
}

func truncate(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}

	return string([]rune(s)[:max-1]) + "…"
}
