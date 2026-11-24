package mlmodels

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
)

// MaxSchema is the largest schema, in bytes.
const MaxSchema = 64 << 10

// Schema is a JSON Schema a model's answers keep to.
type Schema struct {
	raw      json.RawMessage
	resolved *jsonschema.Resolved
}

// ParseSchema reads a JSON Schema; nil for none, when raw is empty.
func ParseSchema(raw json.RawMessage) (*Schema, error) {
	raw = bytes.TrimSpace(raw)

	switch {
	case len(raw) == 0:
		return nil, nil
	case len(raw) > MaxSchema:
		return nil, fmt.Errorf("the schema is %d bytes, more than the %d KiB one may be", len(raw), MaxSchema>>10)
	}

	var s jsonschema.Schema
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("not a JSON Schema: %w", err)
	}

	resolved, err := s.Resolve(nil)
	if err != nil {
		return nil, fmt.Errorf("not a JSON Schema: %w", err)
	}

	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return nil, fmt.Errorf("not JSON: %w", err)
	}

	return &Schema{raw: compact.Bytes(), resolved: resolved}, nil
}

// Raw is the schema as the model is told it.
func (s *Schema) Raw() json.RawMessage {
	if s == nil {
		return nil
	}

	return s.raw
}

// Check holds an answer to the schema.
func (s *Schema) Check(answer json.RawMessage) error {
	if s == nil {
		return nil
	}

	var v any
	if err := json.Unmarshal(answer, &v); err != nil {
		return fmt.Errorf("the answer is not JSON: %w", err)
	}

	if err := s.resolved.Validate(v); err != nil {
		return fmt.Errorf("the answer does not keep to the schema: %w", err)
	}

	return nil
}
