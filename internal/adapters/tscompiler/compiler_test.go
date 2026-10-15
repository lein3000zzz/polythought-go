package tscompiler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/adapters/jsruntime"
	"github.com/lein3000zzz/polythought-go/internal/adapters/tscompiler"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

func compiler(t *testing.T) *tscompiler.Compiler {
	t.Helper()

	c, err := tscompiler.New(observability.NoopUtility())
	require.NoError(t, err)

	return c
}

var (
	runnerOnce sync.Once
	runner     *jsruntime.Runner
)

// run compiles source and runs its export run with input, the SDK being sdk.
func run(t *testing.T, source, sdk, input string) (json.RawMessage, error) {
	t.Helper()

	runnerOnce.Do(func() {
		var err error
		runner, err = jsruntime.New(context.Background(), observability.NoopUtility(), jsruntime.Config{})
		require.NoError(t, err)
	})

	code, err := compiler(t).Compile(context.Background(), source)
	if err != nil {
		return nil, err
	}

	script := code.Script
	if sdk != "" {
		script = fmt.Sprintf("globalThis.%s = %s;\n%s", tscompiler.SDKGlobal, sdk, script)
	}

	res, err := runner.Run(context.Background(), ports.CodeJob{Source: script, Entry: code.Entry("run"), Input: json.RawMessage(input)})

	return res.Output, err
}

func TestCompileAndRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		sdk    string
		input  string
		want   string
	}{
		{
			name: "types go, code stays",
			source: `
				interface Item { question: string; spread: string }
				export function run(items: Item[]): string[] {
					return items.filter((i) => Number(i.spread) < 0.02).map((i) => i.question)
				}`,
			input: `[{"question":"A","spread":"0.01"},{"question":"B","spread":"0.05"}]`,
			want:  `["A"]`,
		},
		{
			name: "enums, satisfies, optional chaining",
			source: `
				enum Side { Buy = "BUY", Sell = "SELL" }
				const sides = { buy: Side.Buy } satisfies Record<string, Side>
				export const run = (x?: { n?: number }) => [sides.buy, x?.n ?? 0]`,
			want: `["BUY",0]`,
		},
		{
			name: "the SDK is the runtime's global",
			source: `
				import { answer, double } from "@polythought/sdk"
				import * as sdk from "@polythought/sdk"
				export async function run() { return [answer, double(answer), typeof sdk.double] }`,
			sdk:  `{answer: 21, double: (n) => n * 2}`,
			want: `[21,42,"function"]`,
		},
		{
			name: "only what is exported is reachable",
			source: `
				function helper() { return "help" }
				export function run() { return [helper(), typeof globalThis.helper] }`,
			want: `["help","undefined"]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out, err := run(t, tt.source, tt.sdk, tt.input)
			require.NoError(t, err)
			assert.JSONEq(t, tt.want, string(out))
		})
	}
}

func TestCompileRefuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   []domain.CodeProblem
	}{
		{
			name:   "a package",
			source: "import { z } from \"zod\"\nexport const run = () => z",
			want:   []domain.CodeProblem{{Line: 1, Column: 19, Message: `only @polythought/sdk can be imported, not "zod"`}},
		},
		{
			name:   "files and node built-ins, in any form",
			source: "import fs from \"fs\"\nconst p = require(\"./secret.json\")\nexport async function run() { return [fs, p, await import(\"node:child_process\")] }",
			want: []domain.CodeProblem{
				{Line: 1, Column: 16, Message: `only @polythought/sdk can be imported, not "fs"`},
				{Line: 2, Column: 19, Message: `only @polythought/sdk can be imported, not "./secret.json"`},
				{Line: 3, Column: 59, Message: `only @polythought/sdk can be imported, not "node:child_process"`},
			},
		},
		{
			name:   "a URL",
			source: `import x from "https://example.com/x.js"; export const run = () => x`,
			want:   []domain.CodeProblem{{Line: 1, Column: 15, Message: `only @polythought/sdk can be imported, not "https://example.com/x.js"`}},
		},
		{
			name:   "a syntax error, its column in characters",
			source: "// é\nexport const run = (x: number => «x»",
			want:   []domain.CodeProblem{{Line: 2, Column: 31, Message: `Expected ")" but found "=>"`}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := compiler(t).Compile(context.Background(), tt.source)

			var ce *domain.CodeError
			require.ErrorAs(t, err, &ce)
			assert.Equal(t, domain.CodeDidNotCompile, ce.Kind)
			assert.Equal(t, tt.want, ce.Problems)
		})
	}

	t.Run("the message points at the first problem", func(t *testing.T) {
		t.Parallel()

		// Imports nothing uses are dropped, as types would be, so these are used.
		_, err := compiler(t).Compile(context.Background(), "import a from \"a\"\nimport b from \"b\"\nexport const run = () => [a, b]")
		require.EqualError(t, err, `line 1, column 15: only @polythought/sdk can be imported, not "a" (and 1 more)`)
	})

	t.Run("too big", func(t *testing.T) {
		t.Parallel()

		_, err := compiler(t).Compile(context.Background(), "//"+strings.Repeat("x", tscompiler.MaxSource))
		require.EqualError(t, err, "the code is 256 KiB, more than the 256 KiB allowed")
	})
}

func TestCompileCaches(t *testing.T) {
	t.Parallel()

	c := compiler(t)
	source := `export const run = () => 1`

	first, err := c.Compile(context.Background(), source)
	require.NoError(t, err)
	assert.Len(t, first.Hash, 64)
	assert.Equal(t, tscompiler.ExportsGlobal+".run", first.Entry("run"))

	again, err := c.Compile(context.Background(), source)
	require.NoError(t, err)
	assert.Equal(t, first, again)

	other, err := c.Compile(context.Background(), source+" ")
	require.NoError(t, err)
	assert.NotEqual(t, first.Hash, other.Hash)
}

func TestCompileExpression(t *testing.T) {
	t.Parallel()

	c := compiler(t)
	ctx := context.Background()

	for expr, want := range map[string]string{
		"spread * 100":                 "spread * 100",
		"(item.price as number) * 2":   "item.price * 2",
		"outcomes[0]!.label ?? 'none'": `outcomes[0].label ?? "none"`,
		"{ a: 1 }.a":                   "({ a: 1 }).a",
		"x // a comment at the end":    "x",
		"`${question} (${spread})`":    "`${question} (${spread})`",
	} {
		got, err := c.CompileExpression(ctx, expr)
		require.NoError(t, err, expr)
		assert.Equal(t, want, got, expr)
	}

	for expr, want := range map[string]domain.CodeProblem{
		"a; b":         {Line: 1, Column: 2, Message: `Expected ")" but found ";"`},
		"price *":      {Line: 1, Column: 8, Message: "Unexpected end of the expression"},
		"x\n  + * 2":   {Line: 2, Column: 5, Message: `Unexpected "*"`},
		"import('fs')": {}, // compiles: imports cannot run in the sandbox
	} {
		_, err := c.CompileExpression(ctx, expr)
		if want.Message == "" {
			require.NoError(t, err, expr)
			continue
		}

		var ce *domain.CodeError
		require.ErrorAs(t, err, &ce, expr)
		assert.Equal(t, domain.CodeDidNotCompile, ce.Kind)
		assert.Equal(t, want, ce.Problems[0], expr)
	}

	_, err := c.CompileExpression(ctx, strings.Repeat("x", tscompiler.MaxExpression+1))
	require.ErrorContains(t, err, "more than the 16 KiB allowed")
}
