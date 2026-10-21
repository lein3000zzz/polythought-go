package ai

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
)

// The kinds a field of the answer can be.
const (
	kindText    = "text"
	kindNumber  = "number"
	kindWhole   = "whole number"
	kindBool    = "true or false"
	kindList    = "list of text"
	maxSchema   = 64 << 10 // the largest schema, in bytes
	maxFieldLen = 64
)

var fieldColumns = []engine.ParamSpec{
	{Name: "name", Label: "Name", Kind: engine.ParamString, Required: true, Description: "Like watch or reason: letters, digits and _."},
	{
		Name: "type", Label: "Type", Kind: engine.ParamString, Default: kindText,
		Options: []string{kindText, kindNumber, kindWhole, kindBool, kindList},
	},
	{Name: "description", Label: "Description", Kind: engine.ParamString, Description: "What it is, for the model: it reads this."},
	{Name: "oneOf", Label: "One of", Kind: engine.ParamString, Description: "For text: the answers allowed, separated by commas, like buy, sell, wait."},
	{Name: "optional", Label: "Optional", Kind: engine.ParamBool, Default: false, Description: "The model may leave it out, as null."},
}

var fieldName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// fieldsSchema is the schema the Fields table describes, in the shape every provider
// holds to (D255): an object closed to other fields and requiring all of its own, those
// it may leave out taking null, so OpenAI's strict mode applies and Claude's format too.
func fieldsSchema(rows []engine.Params) (json.RawMessage, error) {
	if len(rows) == 0 {
		return nil, errors.New("add a row for each field of the answer")
	}

	props := map[string]any{}
	required := make([]string, 0, len(rows))

	for i, row := range rows {
		name, _ := row.String("name")
		typ, _ := row.String("type")
		description, _ := row.String("description")
		oneOf, _ := row.String("oneOf")
		optional, _ := row.Bool("optional")

		name = strings.TrimSpace(name)

		switch {
		case !fieldName.MatchString(name) || len(name) > maxFieldLen:
			return nil, fmt.Errorf("row %d: %q is no name: letters, digits and _, up to %d", i+1, name, maxFieldLen)
		case props[name] != nil:
			return nil, fmt.Errorf("row %d: %s twice", i+1, name)
		}

		var s map[string]any

		switch typ {
		case kindText, "":
			s = map[string]any{"type": "string"}

			if choices := split(oneOf); len(choices) > 0 {
				s["enum"] = choices
			}
		case kindNumber:
			s = map[string]any{"type": "number"}
		case kindWhole:
			s = map[string]any{"type": "integer"}
		case kindBool:
			s = map[string]any{"type": "boolean"}
		case kindList:
			s = map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
		default:
			return nil, fmt.Errorf("row %d: %q is not a type a field can be", i+1, typ)
		}

		if optional {
			s = map[string]any{"anyOf": []any{s, map[string]any{"type": "null"}}}
		}

		if description = strings.TrimSpace(description); description != "" {
			s["description"] = description
		}

		props[name] = s
		required = append(required, name)
	}

	return json.Marshal(map[string]any{
		"type": "object", "additionalProperties": false, "properties": props, "required": required,
	})
}

func split(list string) []string {
	var out []string

	for v := range strings.SplitSeq(list, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}

	return out
}

// validator holds an answer to a schema.
type validator struct {
	raw      json.RawMessage
	resolved *jsonschema.Resolved
}

// compile reads a schema: a JSON object, with no reference outside it, which no
// loader is given to fetch.
func compile(raw json.RawMessage) (*validator, error) {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))

	switch {
	case len(raw) == 0:
		return nil, errors.New("give one, or answer as text or fields")
	case len(raw) > maxSchema:
		return nil, fmt.Errorf("%d KiB at the most", maxSchema>>10)
	}

	var s jsonschema.Schema
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("not a JSON schema: %w", err)
	}

	resolved, err := s.Resolve(nil)
	if err != nil {
		return nil, err
	}

	return &validator{raw: raw, resolved: resolved}, nil
}

// check reads an answer as JSON and holds it to the schema. A fence the model put round
// it, as models not held to the schema do, is taken off first.
func (v *validator) check(text string) (any, error) {
	text = strings.TrimSpace(text)

	if body, ok := strings.CutPrefix(text, "```"); ok {
		_, body, _ = strings.Cut(body, "\n") // the fence's language, if any
		text = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(body), "```"))
	}

	var answer any
	if err := json.Unmarshal([]byte(text), &answer); err != nil {
		return nil, fmt.Errorf("it is not JSON: %w", err)
	}

	if err := v.resolved.Validate(answer); err != nil {
		return nil, err
	}

	return answer, nil
}
