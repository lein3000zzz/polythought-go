package code

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

var _ engine.Evaluator = (*Service)(nil)

// Evaluate works expressions out for items, in one run of the sandbox (D38 to D40): each
// sees the item's fields by name, and item, params and execution, and dec from the SDK;
// it makes no data requests. An expression that does not compile, or throws, is that
// one's error; the run failing, out of time or memory, fails them all.
func (s *Service) Evaluate(ctx context.Context, e engine.Evaluation) ([][]engine.EvalResult, error) {
	js := make([]string, len(e.Expressions))
	failed := make([]string, len(e.Expressions))

	for i, expr := range e.Expressions {
		code, err := s.compiler.CompileExpression(ctx, expr)

		var ce *domain.CodeError

		switch {
		case errors.As(err, &ce):
			failed[i] = ce.Message
		case err != nil:
			return nil, err
		default:
			js[i] = code
		}
	}

	input, err := json.Marshal(map[string]any{"items": e.Items, "params": e.Params, "execution": e.Execution})
	if err != nil {
		return nil, fmt.Errorf("code: the items: %w", err)
	}

	res, err := s.runner.Run(ctx, ports.CodeJob{Source: evaluator(js), Entry: "__evaluate", Input: input})
	if err != nil {
		return nil, err
	}

	var rows [][]*struct {
		Value any     `json:"value"`
		Error *string `json:"error"`
	}

	dec := json.NewDecoder(bytes.NewReader(res.Output))
	dec.UseNumber() // decimals stay exact

	if err := dec.Decode(&rows); err != nil {
		return nil, fmt.Errorf("code: read what the expressions gave: %w", err)
	}

	out := make([][]engine.EvalResult, len(rows))

	for i, row := range rows {
		out[i] = make([]engine.EvalResult, len(e.Expressions))

		for j := range e.Expressions {
			switch {
			case failed[j] != "":
				out[i][j] = engine.EvalResult{Err: failed[j]}
			case j >= len(row) || row[j] == nil:
			case row[j].Error != nil:
				out[i][j] = engine.EvalResult{Err: *row[j].Error}
			default:
				out[i][j] = engine.EvalResult{Value: row[j].Value}
			}
		}
	}

	return out, nil
}

// evaluator is the script that works the expressions out, a function each, null for one
// that did not compile. It runs as a plain script, where with works: the item's fields are
// in scope by name, though item, params and execution always mean those.
func evaluator(js []string) string {
	var b strings.Builder

	b.WriteString(evaluatorHead)

	for _, e := range js {
		if e == "" {
			b.WriteString("  null,\n")
			continue
		}

		fmt.Fprintf(&b, "  function (item, params, execution) { with (__scope(item)) { return (\n%s\n); } },\n", e)
	}

	b.WriteString(evaluatorTail)

	return b.String()
}

const evaluatorHead = `const dec = globalThis.__polythought_sdk.dec;
// Over an empty object, not the item: the item is frozen, and a proxy may not hide a
// frozen object's fields, as it does those named item, params or execution.
const __scope = (item) => new Proxy(Object.create(null), {
  has: (_, k) => typeof k === "string" && k !== "item" && k !== "params" && k !== "execution" && Object.hasOwn(item, k),
  get: (_, k) => (k === Symbol.unscopables ? undefined : item[k]),
});
const __expressions = [
`

const evaluatorTail = `];
function __evaluate(input) {
  return input.items.map((item) => {
    Object.freeze(item);
    return __expressions.map((f) => {
      if (f === null) return null;
      try {
        const value = f(item, input.params, input.execution);
        return { value: value === undefined ? null : value };
      } catch (e) {
        return { error: String(e) };
      }
    });
  });
}
`
