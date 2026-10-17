package tscompiler_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/lein3000zzz/polythought-go/internal/adapters/tscompiler"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
)

func TestTypeName(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]string{
		"polymarket.priceChange": "PolymarketPriceChange",
		"trigger.market":         "TriggerMarket",
		"logic.if":               "LogicIf",
		"my-plugin.do_it":        "MyPluginDoIt",
	} {
		assert.Equal(t, want, tscompiler.TypeName(name), name)
	}
}

func TestNodeDeclarations(t *testing.T) {
	t.Parallel()

	types := []engine.TypeInfo{
		{NodeType: engine.NodeType{
			Name: "test.quote", Label: "Quote", Outputs: []string{"main"},
			Params: []engine.ParamSpec{
				{Name: "tokens", Kind: engine.ParamTokens, Required: true},
				{Name: "on", Kind: engine.ParamList, Default: []any{"trade"}, Options: []string{"trade", "book"}},
				{Name: "least", Kind: engine.ParamDecimal, Description: "Ignore smaller */ ones."},
				{Name: "field", Kind: engine.ParamString, Field: true, Required: true},
			},
			Items: &engine.Items{Fields: []engine.Field{
				{Name: "side", Kind: engine.FieldText, Options: []string{"buy", "sell"}, Description: "Who took."},
				{Name: "bid", Kind: engine.FieldDecimal, Nullable: true},
				{Name: "at", Kind: engine.FieldTime, Optional: true},
				{Name: "levels", Kind: engine.FieldList, Of: &engine.Field{Kind: engine.FieldObject, Fields: []engine.Field{
					{Name: "price", Kind: engine.FieldDecimal},
				}}},
				{Name: "tags", Kind: engine.FieldList, Of: &engine.Field{Kind: engine.FieldText, Nullable: true}},
				{Name: "odd-name", Kind: engine.FieldAny},
			}},
		}},
		{NodeType: engine.NodeType{Name: "test.pass", Inputs: []string{"main"}, Items: &engine.Items{Passed: true}}},
		{NodeType: engine.NodeType{Name: "test.empty", Items: &engine.Items{}}},
		{NodeType: engine.NodeType{Name: "test.unknown"}},
		{NodeType: engine.NodeType{Name: "test.start", Outputs: []string{"main"}}, Trigger: true},
		{NodeType: engine.NodeType{
			Name: "test.ask", Inputs: []string{"main", "b"}, Outputs: []string{"timeout"}, OutputsParam: "buttons",
			Params: []engine.ParamSpec{{Name: "buttons", Kind: engine.ParamList}},
		}},
	}

	got := tscompiler.NodeDeclarations(types)

	for _, want := range []string{
		`import type { Json } from "./sdk";`,
		"export interface TestQuoteSettings {\n    /** Outcome token IDs. */\n    tokens: string[] | Expression;\n",
		"    /**\n     * @default [\"trade\"]\n     */\n    on?: (\"trade\" | \"book\")[] | Expression;\n",
		"    /** Ignore smaller *\\/ ones. */\n    least?: DecimalText | number | Expression;\n",
		"    /** A field of the items it receives. */\n    field: string;\n",
		"/** An item Quote (test.quote) sends on. */\nexport interface TestQuoteItem {\n" +
			"    /** Who took. */\n    side: \"buy\" | \"sell\";\n" +
			"    bid: DecimalText | null;\n" +
			"    at?: TimeText;\n" +
			"    levels: {\n        price: DecimalText;\n    }[];\n" +
			"    tags: (string | null)[];\n" +
			"    \"odd-name\": Json;\n}\n",
		"export type TestEmptySettings = Record<string, never>;",
		"export type TestEmptyItem = Record<string, never>;",
		`    "test.pass": {
        settings: TestPassSettings;
        items: "received";
        trigger: false;
        inputs: "main";
        outputs: never;
    };`,
		`    "test.quote": {
        settings: TestQuoteSettings;
        items: TestQuoteItem;
        trigger: false;
        inputs: never;
        outputs: "main";
    };`,
		`    "test.unknown": {
        settings: TestUnknownSettings;
        items: Json;`,
		`    "test.start": {
        settings: TestStartSettings;
        items: Json;
        trigger: true;`,
		// Ports a setting makes take any name; the known ones still complete.
		`        inputs: "main" | "b";
        outputs: "timeout" | (string & {});`,
	} {
		assert.Contains(t, got, want)
	}

	assert.NotContains(t, got, "TestPassItem", "a node passing items on has no item type of its own")
	assert.Less(t, strings.Index(got, "TestEmpty"), strings.Index(got, "TestPass"), "sorted by name")
}

func TestDeclarations(t *testing.T) {
	t.Parallel()

	files := tscompiler.Declarations(nil)

	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.Path
	}

	assert.Equal(t, []string{"index.d.ts", "sdk.d.ts", "nodes.d.ts"}, paths)
	assert.Contains(t, files[0].Content, `export * from "./nodes";`)
	assert.Contains(t, files[1].Content, "export declare function dec(n: DecimalLike): Decimal;", "the SDK's, as tsc made them")
}
