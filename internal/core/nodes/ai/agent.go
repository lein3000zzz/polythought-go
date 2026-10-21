// Package ai has the nodes that ask a language model, with their owner's own key
// (D243): the agent (phase 4).
package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/shopspring/decimal"
	"golang.org/x/sync/errgroup"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/credtypes"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/shape"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const AgentType = "ai.agent"

// The ways it calls the model (D253).
const (
	CallEach     = "each"
	CallTogether = "together"
)

// The ways it answers (D255).
const (
	AnswerText   = "text"
	AnswerFields = "fields"
	AnswerSchema = "schema"
)

const (
	// parallel is how many items' calls run at once.
	parallel = 4
	// maxRetries bounds Retries: each one is a call, and billed.
	maxRetries = 3
)

var _ engine.Node = (*Agent)(nil)

// Agent asks a model for each item, or once for all of them together (D253), with any
// provider's key (D254): a prompt with expressions, an answer as text or as JSON keeping
// to a schema (D255), put under the answer field with the tokens it took and what it
// cost when the provider says (D256). An answer that does not fit is asked for again,
// as many times as Retries says (D258).
type Agent struct {
	credentials ports.CredentialReader
	llm         ports.LLM
}

func NewAgent(credentials ports.CredentialReader, llm ports.LLM) *Agent {
	return &Agent{credentials: credentials, llm: llm}
}

func (*Agent) Type() engine.NodeType {
	return engine.NodeType{
		Name:  AgentType,
		Label: "AI agent",
		Description: "Asks a language model, with your own key, for each item or once for all of them: a prompt " +
			"with expressions, the answer as text or as JSON in the shape you give, added to the item with the " +
			"tokens it took. Calls are billed to the key's account.",
		Inputs:    []string{engine.DefaultPort},
		Outputs:   []string{engine.DefaultPort},
		SeesItems: true,
		Items: &engine.Items{
			Passed: true, Named: "field", Alone: engine.Choice{Param: "call", Value: CallTogether},
			Fields: []engine.Field{answerField},
		},
		Params: []engine.ParamSpec{
			{
				Name: "credential", Label: "Key", Kind: engine.ParamCredential, CredentialTypes: credtypes.LLMKeyTypes, Required: true,
				Description: "Your Anthropic, OpenAI-compatible or Gemini key, from the Credentials page.",
			},
			{
				Name: "model", Label: "Model", Kind: engine.ParamString, Required: true, ModelsOf: "credential",
				Description: "As the provider names it, like claude-opus-5-5: one the key can use, or any other.",
			},
			{
				Name: "call", Label: "Call", Kind: engine.ParamString, Default: CallEach, Options: []string{CallEach, CallTogether},
				Description: "each: a call per item, the answer added to it. together: one call for all of them, " +
					"and one item out, the answer alone.",
			},
			{
				Name: "system", Label: "System prompt", Kind: engine.ParamString, Multiline: true,
				Description: "How the model should go about it, like You are a careful prediction market analyst.",
			},
			{
				Name: "prompt", Label: "Prompt", Kind: engine.ParamString, Required: true, Multiline: true,
				Description: "What to ask, like Is {{ question }} worth watching? Taking them together, " +
					"{{ items }} are all of them.",
			},
			{
				Name: "include", Label: "Include the items", Kind: engine.ParamBool, Default: true,
				Description: "Taking them together: the items go after the prompt, as JSON.",
			},
			{
				Name: "answerAs", Label: "Answer as", Kind: engine.ParamString, Default: AnswerText,
				Options:     []string{AnswerText, AnswerFields, AnswerSchema},
				Description: "text as it is written; fields, the Fields below; schema, the JSON schema below.",
			},
			{
				Name: "fields", Label: "Fields", Kind: engine.ParamTable, Columns: fieldColumns,
				Description: "Answering in fields: a row each, like watch, true or false.",
			},
			{
				Name: "schema", Label: "JSON schema", Kind: engine.ParamString, Multiline: true,
				Description: "Answering to a schema: the answer's, for nested shapes. Claude wants additionalProperties " +
					"false on every object.",
			},
			{
				Name: "field", Label: "Answer field", Kind: engine.ParamString, Default: "answer",
				Description: "Where the answer goes on the item.",
			},
			{
				Name: "maxTokens", Label: "Max tokens", Kind: engine.ParamNumber, Default: 16000,
				Description: "The most an answer may take, thinking included: one cut off fails.",
			},
			{
				Name: "temperature", Label: "Temperature", Kind: engine.ParamDecimal,
				Description: "Empty for the model's own; the newest Claude models take none.",
			},
			{
				Name: "effort", Label: "Effort", Kind: engine.ParamString, Options: []string{"low", "medium", "high"},
				Description: "How hard the model thinks first; empty for its own.",
			},
			{
				Name: "retries", Label: "Retries", Kind: engine.ParamNumber, Default: 1,
				Description: "How many more times to ask when an answer does not fit, up to 3; each is billed.",
			},
		},
	}
}

// answerField is what the node adds, under the answer field's name.
var answerField = engine.Field{Name: "answer", Kind: engine.FieldObject, Description: "What the model answered.", Fields: []engine.Field{
	shape.Optional(shape.Text("text", "The answer as the model wrote it, answering as text.")),
	{Name: "json", Kind: engine.FieldAny, Optional: true, Description: "The answer as JSON, answering in fields or to a schema."},
	shape.Text("model", "The model that answered, as the provider names it."),
	shape.Text("stopReason", "Why the model stopped, as the provider says: end_turn, stop, STOP and the like."),
	{Name: "usage", Kind: engine.FieldObject, Description: "The tokens it took, every try counted.", Fields: []engine.Field{
		shape.Number("input", "Tokens in."),
		shape.Number("output", "Tokens out, thinking included."),
	}},
	shape.Nullable(shape.Decimal("cost", "What the provider says the calls cost, in dollars, when it says (OpenRouter).")),
	shape.Number("tries", "How many calls it took: 1 unless an answer did not fit."),
}}

// settings are a call's, read from an item's params.
type settings struct {
	req     ports.LLMRequest
	schema  *validator
	field   string
	retries int
	include bool
}

func (n *Agent) Run(ctx context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	items := in[engine.DefaultPort]
	out := engine.Ports{}

	if len(items) == 0 {
		return out, nil
	}

	first := engine.ItemRef{Port: engine.DefaultPort, Index: 0}

	p, err := rc.Params.Resolve(first)
	if err != nil {
		return nil, err
	}

	key, err := n.key(ctx, rc, p)
	if err != nil {
		return nil, err
	}

	if call, _ := p.String("call"); call == CallTogether {
		return n.together(ctx, key, p, items)
	}

	// Each item's settings first, then the calls, a few at a time.
	type job struct {
		s   settings
		err error
	}

	jobs := make([]job, len(items))

	for i := range items {
		src := engine.ItemRef{Port: engine.DefaultPort, Index: i}

		p, err := rc.Params.Resolve(src)
		if err != nil {
			return nil, err
		}

		jobs[i].s, jobs[i].err = read(p)
	}

	answers := make([]map[string]any, len(items))
	failures := make([]error, len(items))

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(parallel)

	for i := range items {
		if jobs[i].err != nil {
			failures[i] = jobs[i].err
			continue
		}

		g.Go(func() error {
			answers[i], failures[i] = n.ask(gctx, key, jobs[i].s)
			return ctx.Err()
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	for i, item := range items {
		src := engine.ItemRef{Port: engine.DefaultPort, Index: i}

		if failures[i] != nil {
			out.Fail(item, src, failures[i])
			continue
		}

		j := maps.Clone(item.JSON)
		if j == nil {
			j = map[string]any{}
		}

		j[jobs[i].s.field] = answers[i]
		out.Add(engine.DefaultPort, engine.Item{JSON: j, Source: []engine.ItemRef{src}})
	}

	return out, nil
}

// together is one call for all the items, and one item out, the answer alone.
func (n *Agent) together(ctx context.Context, key ports.LLMKey, p engine.Params, items []engine.Item) (engine.Ports, error) {
	out := engine.Ports{}

	srcs := make([]engine.ItemRef, len(items))
	for i := range items {
		srcs[i] = engine.ItemRef{Port: engine.DefaultPort, Index: i}
	}

	s, err := read(p)
	if err == nil && s.include {
		err = s.attach(items)
	}

	var answer map[string]any
	if err == nil {
		answer, err = n.ask(ctx, key, s)
	}

	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		out.Add(engine.ErrorPort, engine.Item{JSON: map[string]any{}, Source: srcs, Err: err})

		return out, nil
	}

	out.Add(engine.DefaultPort, engine.Item{JSON: map[string]any{s.field: answer}, Source: srcs})

	return out, nil
}

// key is the run's key, from its owner's vault: a credential takes no expressions, so
// one for all the items.
func (n *Agent) key(ctx context.Context, rc engine.RunContext, p engine.Params) (ports.LLMKey, error) {
	id, err := p.String("credential")
	if err != nil || id == "" {
		return ports.LLMKey{}, errors.New("credential: pick your key")
	}

	if n.credentials == nil {
		return ports.LLMKey{}, errors.New("credentials are not available here")
	}

	credType, values, err := n.credentials.Values(ctx, rc.OwnerID, domain.CredentialID(id), credtypes.LLMKeyTypes)
	if err != nil {
		return ports.LLMKey{}, fmt.Errorf("credential: %w", err)
	}

	key, _ := credtypes.LLMKey(credType, values)

	return key, nil
}

// read is a call's settings, as an item's params give them.
func read(p engine.Params) (settings, error) {
	var s settings

	text := func(name string) string {
		v, _ := p.String(name)
		return v
	}

	s.req.Model, s.req.System = strings.TrimSpace(text("model")), text("system")
	prompt := text("prompt")

	switch {
	case s.req.Model == "":
		return s, errors.New("model: name one, like claude-opus-5-5")
	case strings.TrimSpace(prompt) == "":
		return s, errors.New("prompt: the question to ask is empty")
	}

	s.req.Messages = []ports.LLMMessage{{Role: ports.LLMUser, Text: prompt}}

	if s.field = strings.TrimSpace(text("field")); s.field == "" {
		return s, errors.New("answer field: name where the answer goes")
	}

	maxTokens, err := p.Int("maxTokens")
	if err != nil || maxTokens <= 0 {
		return s, errors.New("max tokens: a whole number above 0")
	}

	s.req.MaxTokens = int64(maxTokens)

	if p["temperature"] != nil {
		t, err := p.Decimal("temperature")
		if err != nil || t.IsNegative() || t.GreaterThan(decimal.NewFromInt(2)) {
			return s, errors.New("temperature: from 0 to 2, or empty")
		}

		f, _ := t.Float64()
		s.req.Temperature = &f
	}

	s.req.Effort = ports.LLMEffort(text("effort"))

	if s.retries, err = p.Int("retries"); err != nil || s.retries < 0 || s.retries > maxRetries {
		return s, fmt.Errorf("retries: from 0 to %d", maxRetries)
	}

	s.include, _ = p.Bool("include")

	switch as := text("answerAs"); as {
	case AnswerText, "":
	case AnswerFields:
		rows, err := p.Table("fields", fieldColumns)
		if err != nil {
			return s, err
		}

		raw, err := fieldsSchema(rows)
		if err != nil {
			return s, fmt.Errorf("fields: %w", err)
		}

		if s.schema, err = compile(raw); err != nil {
			return s, fmt.Errorf("fields: %w", err)
		}
	case AnswerSchema:
		if s.schema, err = compile(json.RawMessage(text("schema"))); err != nil {
			return s, fmt.Errorf("JSON schema: %w", err)
		}
	default:
		return s, fmt.Errorf("answer as: %q is not text, fields or schema", as)
	}

	if s.schema != nil {
		s.req.Schema = s.schema.raw
	}

	return s, nil
}

// attach puts the items after the prompt, as JSON (D257).
func (s *settings) attach(items []engine.Item) error {
	all := make([]map[string]any, len(items))
	for i, it := range items {
		all[i] = it.JSON
	}

	raw, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return fmt.Errorf("the items: %w", err)
	}

	m := &s.req.Messages[0]
	m.Text += "\n\nThe items, as JSON:\n" + string(raw)

	return nil
}

// ask calls the model, again while an answer does not fit, as many more times as the
// settings say. A refusal or a provider's failure is not asked again: the SDKs have
// retried what a retry can fix.
func (n *Agent) ask(ctx context.Context, key ports.LLMKey, s settings) (map[string]any, error) {
	req := s.req
	req.Messages = append([]ports.LLMMessage(nil), s.req.Messages...)

	var (
		used    ports.LLMUsage
		costs   = decimal.Zero
		costed  = true
		problem string
	)

	for try := 1; try <= 1+s.retries; try++ {
		res, err := n.llm.Complete(ctx, key, req)
		if err != nil {
			return nil, err
		}

		used.InputTokens += res.Usage.InputTokens
		used.OutputTokens += res.Usage.OutputTokens

		if res.Usage.Cost != nil {
			costs = costs.Add(*res.Usage.Cost)
		} else {
			costed = false
		}

		if res.StopReason == "refusal" {
			return nil, errors.New("the model refused to answer")
		}

		answer := map[string]any{
			"model": res.Model, "stopReason": res.StopReason, "tries": try,
			"usage": map[string]any{"input": used.InputTokens, "output": used.OutputTokens},
			"cost":  nil,
		}

		if costed {
			answer["cost"] = costs.String()
		}

		if s.schema == nil {
			answer["text"] = res.Text
			return answer, nil
		}

		v, err := s.schema.check(res.Text)
		if err == nil {
			answer["json"] = v
			return answer, nil
		}

		problem = err.Error()
		if cutOff(res.StopReason) {
			problem = "cut off at Max tokens: " + problem
		}

		req.Messages = append(req.Messages,
			ports.LLMMessage{Role: ports.LLMAssistant, Text: res.Text},
			ports.LLMMessage{Role: ports.LLMUser, Text: "That answer does not fit the schema: " + problem +
				". Answer again, with only the JSON."},
		)
	}

	return nil, fmt.Errorf("the answer does not fit the schema, after %d tries: %s", 1+s.retries, problem)
}

// cutOff reports whether a stop reason is the answer running out of tokens, as each
// provider says it.
func cutOff(reason string) bool {
	switch reason {
	case "max_tokens", "length", "MAX_TOKENS":
		return true
	}

	return false
}
