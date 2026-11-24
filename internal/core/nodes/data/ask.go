package data

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

const AskType = "data.ask"

const (
	// DefaultWait is how long Ask a model waits for the answers, unless its setting says.
	DefaultWait = 30 * time.Second
	// longestWait bounds the setting; the admin's most bounds each run.
	longestWait = time.Hour
	maxSchema   = 64 << 10
)

var _ engine.Node = (*Ask)(nil)

// Ask asks one of the owner's models about the items it receives, all of them together, a
// batch a run, and puts each item's answer on it, with the model's name, the version that
// answered and the worker.
type Ask struct {
	models ports.ModelAsker
}

func NewAsk(models ports.ModelAsker) *Ask {
	return &Ask{models: models}
}

var answerField = engine.Field{Name: "prediction", Kind: engine.FieldObject, Description: "The model's answer for the item.", Fields: []engine.Field{
	{Name: "answer", Kind: engine.FieldAny, Description: "Its answer: any JSON, as the model gave it."},
	{Name: "model", Kind: engine.FieldText, Description: "The model's name."},
	{Name: "version", Kind: engine.FieldText, Optional: true, Description: "The version its worker or URL said it was."},
	{Name: "worker", Kind: engine.FieldText, Optional: true, Description: "The worker that answered, for a worker model."},
}}

func (*Ask) Type() engine.NodeType {
	return engine.NodeType{
		Name:  AskType,
		Label: "Ask a model",
		Description: "Asks one of your models about the items it receives, all together, and puts each item's answer " +
			"on it, with the model's name, its version and the worker. A model with no worker connected fails at once.",
		Inputs:  []string{engine.DefaultPort},
		Outputs: []string{engine.DefaultPort},
		Items:   &engine.Items{Passed: true, Named: "field", Fields: []engine.Field{answerField}},
		Params: []engine.ParamSpec{
			{
				Name: "model", Label: "Model", Kind: engine.ParamModel, Required: true,
				Description: "The model asked: one of yours, on Data → Models.",
			},
			{
				Name: "fields", Label: "Fields", Kind: engine.ParamList, Field: true,
				Description: "The fields each item sends, like city or outcomes.0.price, each under its name as written; " +
					"none for the whole item.",
			},
			{
				Name: "schema", Label: "Answer schema", Kind: engine.ParamString, Multiline: true, Check: checkSchema,
				Description: "A JSON Schema each answer keeps to, which the model is told: an answer that does not fails " +
					"its item. Empty for any JSON.",
			},
			{Name: "field", Label: "Answer field", Kind: engine.ParamString, Default: "prediction", Description: "Where the answer goes on the item."},
			{
				Name: "wait", Label: "Wait", Kind: engine.ParamDuration, Default: "30s", Check: checkWait,
				Description: "How long to wait for the answers, up to the instance's most: 5 minutes unless an admin says.",
			},
		},
	}
}

func checkSchema(v any) error {
	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return nil
	}

	_, err := parseSchema(s)

	return err
}

func parseSchema(s string) (json.RawMessage, error) {
	raw := json.RawMessage(strings.TrimSpace(s))
	if len(raw) > maxSchema {
		return nil, fmt.Errorf("%d KiB at the most", maxSchema>>10)
	}

	var schema jsonschema.Schema
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, fmt.Errorf("not a JSON Schema: %w", err)
	}

	if _, err := schema.Resolve(nil); err != nil {
		return nil, fmt.Errorf("not a JSON Schema: %w", err)
	}

	return raw, nil
}

// checkWait bounds the wait: one that is no positive duration its kind refuses first.
func checkWait(v any) error {
	if d, err := (engine.Params{"wait": v}).Duration("wait"); err == nil && d > longestWait {
		return fmt.Errorf("up to %s", longestWait)
	}

	return nil
}

func (n *Ask) Run(ctx context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	out := engine.Ports{}

	items := in[engine.DefaultPort]
	if len(items) == 0 {
		return out, nil
	}

	// One batch a run, its settings the first item's.
	p, err := rc.Params.Resolve(engine.ItemRef{Port: engine.DefaultPort})
	if err != nil {
		return nil, err
	}

	ask, err := askOf(p)
	if err != nil {
		return nil, err
	}

	asked := make([]json.RawMessage, 0, len(items))
	from := make([]int, 0, len(items)) // each asked item's index

	for i, item := range items {
		raw, err := json.Marshal(rowOf(item.JSON, ask.fields, nil))
		if err != nil {
			out.Fail(item, engine.ItemRef{Port: engine.DefaultPort, Index: i}, fmt.Errorf("not asked: %w", err))
			continue
		}

		asked = append(asked, raw)
		from = append(from, i)
	}

	if len(asked) == 0 {
		return out, nil
	}

	a, err := n.models.Ask(ctx, rc.OwnerID, ask.model, asked, ask.schema, ask.wait)

	switch {
	case err != nil && ctx.Err() != nil:
		return nil, ctx.Err()
	case errors.Is(err, domain.ErrNotFound), errors.Is(err, domain.ErrDataOff), errors.Is(err, domain.ErrInvalidInput):
		return nil, err // gone, kept from its owner, or past the limits: the run fails at once
	case err != nil:
		// No worker, no answer in time, a worker's failure: each item fails saying so, for
		// an error branch to take.
		for _, i := range from {
			out.Fail(items[i], engine.ItemRef{Port: engine.DefaultPort, Index: i}, err)
		}

		return out, nil
	}

	for j, i := range from {
		src := engine.ItemRef{Port: engine.DefaultPort, Index: i}

		if why := a.Refused[j]; why != nil {
			out.Fail(items[i], src, why)
			continue
		}

		answer, err := decodeAny(a.Answers[j])
		if err != nil {
			out.Fail(items[i], src, err)
			continue
		}

		prediction := map[string]any{"answer": answer, "model": ask.model}
		if a.Version != "" {
			prediction["version"] = a.Version
		}

		if a.Worker != "" {
			prediction["worker"] = a.Worker
		}

		j := maps.Clone(items[i].JSON)
		if j == nil {
			j = map[string]any{}
		}

		j[ask.field] = prediction
		out.Add(engine.DefaultPort, engine.Item{JSON: j, Source: []engine.ItemRef{src}})
	}

	return out, nil
}

// askSettings are what one run asks.
type askSettings struct {
	model  string
	fields []string
	schema json.RawMessage
	field  string
	wait   time.Duration
}

func askOf(p engine.Params) (askSettings, error) {
	var (
		a   askSettings
		err error
	)

	if a.model, err = p.String("model"); err != nil {
		return a, err
	}

	if p["fields"] != nil {
		if a.fields, err = p.Strings("fields"); err != nil {
			return a, err
		}
	}

	if p["schema"] != nil {
		s, err := p.String("schema")
		if err != nil {
			return a, err
		}

		if strings.TrimSpace(s) != "" {
			if a.schema, err = parseSchema(s); err != nil {
				return a, fmt.Errorf("%w %q: %w", engine.ErrInvalidParam, "schema", err)
			}
		}
	}

	if a.field, err = p.String("field"); err != nil {
		return a, err
	}

	if a.wait, err = p.Duration("wait"); err != nil {
		return a, err
	}

	return a, nil
}

// decodeAny is a JSON answer as an item holds it, its numbers exact.
func decodeAny(raw json.RawMessage) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("the answer is not JSON: %w", err)
	}

	return v, nil
}
