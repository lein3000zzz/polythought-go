package tscompiler_test

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/services/code"
	"github.com/lein3000zzz/polythought-go/internal/core/services/library"
)

// The built-in scenarios' messages, worked out by the real compiler and sandbox (here as
// these tests have them) on items like those the nodes get.

var expression = regexp.MustCompile(`\{\{\s*(.+?)\s*\}\}`)

// rendered fills in each {{ }} of message for each item, every one in the sandbox.
func rendered(t *testing.T, message string, items ...map[string]any) []string {
	t.Helper()

	var exprs []string
	for _, m := range expression.FindAllStringSubmatch(message, -1) {
		exprs = append(exprs, m[1])
	}

	results, err := code.New(compiler(t), withSDKRunner(t), code.Data{}).
		Evaluate(context.Background(), engine.Evaluation{Expressions: exprs, Items: items})
	require.NoError(t, err)

	out := make([]string, len(items))

	for i, row := range results {
		j := 0
		out[i] = expression.ReplaceAllStringFunc(message, func(string) string {
			r := row[j]
			j++

			require.Empty(t, r.Err, "expression %q", exprs[j-1])

			return fmt.Sprint(r.Value)
		})
	}

	return out
}

func scenarioMessage(t *testing.T, id domain.TemplateID, node domain.NodeID) string {
	t.Helper()

	builtins, err := library.Builtins()
	require.NoError(t, err)

	for _, tpl := range builtins {
		for _, n := range tpl.Nodes {
			if tpl.ID == id && n.ID == node {
				return n.Params["message"].(string)
			}
		}
	}

	t.Fatalf("no node %s in scenario %s", node, id)

	return ""
}

func TestCopyTradersWithApproval(t *testing.T) {
	t.Parallel()

	trade := func(side string, copy map[string]any, more map[string]any) map[string]any {
		item := map[string]any{
			"trader": map[string]any{"label": "Alice", "address": "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"},
			"side":   side, "question": "Will it rain?", "outcome": "Yes",
			"shares": "100", "price": "0.523", "dollars": "52.3",
			"copy": copy,
		}

		for k, v := range more {
			item[k] = v
		}

		return item
	}
	buy := map[string]any{"side": "buy", "price": "0.54", "dollars": "10", "shares": nil}
	sell := map[string]any{"side": "sell", "price": "0.5", "dollars": nil, "shares": "19.2307"}

	t.Run("the question says what they did and what the copy would", func(t *testing.T) {
		got := rendered(t, scenarioMessage(t, "copy-traders-approval", "ask"), trade("buy", buy, nil), trade("sell", sell, nil))

		assert.Equal(t, []string{
			"Alice bought $52.30 of Yes in “Will it rain?” at 0.523.\nCopy: buy $10.00 at 0.54?",
			"Alice sold $52.30 of Yes in “Will it rain?” at 0.523.\nCopy: sell 19.23 shares at 0.5?",
		}, got)
	})

	t.Run("then how it went", func(t *testing.T) {
		filled := map[string]any{"status": "filled", "shares": "18.518518", "dollars": "10", "fees": "0.1", "price": "0.54"}

		assert.Equal(t, []string{"Copied Alice: bought 18.52 shares of Yes in “Will it rain?” for $10.00."},
			rendered(t, scenarioMessage(t, "copy-traders-approval", "copied"), trade("buy", buy, map[string]any{"order": filled})))

		missed := map[string]any{"status": "unfilled", "reason": "nothing in the book within 0.54", "shares": "0", "dollars": "0"}
		blocked := map[string]any{"reason": "the $200 a day is spent"}

		assert.Equal(t, []string{
			"Not copied, Alice's trade in “Will it rain?”: nothing in the book within 0.54",
			"Not copied, Alice's trade in “Will it rain?”: the $200 a day is spent",
		}, rendered(t, scenarioMessage(t, "copy-traders-approval", "notCopied"),
			trade("buy", buy, map[string]any{"order": missed}), trade("buy", buy, map[string]any{"blocked": blocked})))
	})
}
