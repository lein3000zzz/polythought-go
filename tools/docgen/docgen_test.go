package main

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"

	"github.com/lein3000zzz/polythought-go/internal/adapters/tscompiler"
	"github.com/lein3000zzz/polythought-go/internal/config"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes"
)

// The reference pages on disk must be what the code generates now.
func TestReferenceIsCurrent(t *testing.T) {
	dir := filepath.Join("..", "..", "docs", "src", "content", "docs", "reference")

	pages, types, err := build()
	require.NoError(t, err)

	for path, want := range pages {
		got, err := os.ReadFile(filepath.Join(dir, path))
		require.NoError(t, err, "%s is missing: run make docs-gen", path)
		assert.Equal(t, want, string(got), "%s is stale: run make docs-gen", path)
	}

	for name, want := range types {
		got, err := os.ReadFile(filepath.Join("..", "..", typesDir, name))
		require.NoError(t, err, "%s is missing: run make docs-gen", name)
		assert.Equal(t, want, string(got), "%s is stale: run make docs-gen", name)
	}

	onDisk, err := generatedFiles(dir)
	require.NoError(t, err)

	for path := range onDisk {
		rel, err := filepath.Rel(dir, path)
		require.NoError(t, err)
		assert.Contains(t, pages, filepath.ToSlash(rel), "%s is no longer generated: run make docs-gen", rel)
	}
}

// So must the example config file, which must also be one the server reads.
func TestExampleConfigIsCurrent(t *testing.T) {
	want, err := exampleFile()
	require.NoError(t, err)

	path := filepath.Join("..", "..", exampleConfigPath)

	got, err := os.ReadFile(path)
	require.NoError(t, err, "%s is missing: run make docs-gen", exampleConfigPath)
	assert.Equal(t, want, string(got), "%s is stale: run make docs-gen", exampleConfigPath)

	reg, err := config.Settings()
	require.NoError(t, err)

	file, err := config.ReadFile(path, true, reg)
	require.NoError(t, err)
	assert.Empty(t, file, "everything in it is commented out")

	// With every value uncommented, it is still one the server takes.
	uncommented := filepath.Join(t.TempDir(), "polythought.yml")
	require.NoError(t, os.WriteFile(uncommented, []byte(uncomment(want)), 0o600))

	file, err = config.ReadFile(uncommented, true, reg)
	require.NoError(t, err)
	assert.Len(t, file, len(config.Bootstrap)+len(reg.Specs()))
}

// uncomment turns the example's commented values back on: its "  # name: value" lines.
func uncomment(example string) string {
	return regexp.MustCompile(`(?m)^  # ([a-z_]+: )`).ReplaceAllString(example, "  $1")
}

func TestEveryNodeHasAPage(t *testing.T) {
	pages, _, err := build()
	require.NoError(t, err)

	assert.Contains(t, pages, "nodes/triggers/market.md", "the market trigger is documented though it needs a feed")
	assert.Contains(t, pages, "nodes/polymarket/price-change.md")
	assert.Contains(t, pages, "nodes.md")
	assert.Contains(t, pages, "settings.md")
	assert.Contains(t, pages["credential-types.md"], "Used by: [Webhook](/docs/reference/nodes/output/webhook/).")

	assert.Contains(t, pages, "code-types.md")
	assert.Contains(t, pages["code-types.md"], "export interface PolymarketMarketsItem {")

	_, err = Generate([]engine.TypeInfo{{NodeType: engine.NodeType{Name: "mystery.box"}}}, nil, nil)
	require.ErrorContains(t, err, `node type "mystery.box" fits no group`)
}

func TestText(t *testing.T) {
	for in, want := range map[string]string{
		"Finds markets, e.g. bitcoin. More here.":  "Finds markets, e.g. bitcoin.",
		"Keeps the first, i.e. Oldest. Then more.": "Keeps the first, i.e. Oldest.",
		"One sentence only.":                       "One sentence only.",
		"Ends at 0.5 percent. Next.":               "Ends at 0.5 percent.",
	} {
		assert.Equal(t, want, firstSentence(in), in)
	}

	assert.Equal(t, "price-change", slug("polymarket.priceChange"))
	assert.Equal(t, "orderbook", slug("polymarket.orderbook"))
	assert.Equal(t, `Bearer &lt;token&gt; \| more`, cell("Bearer <token> | more"))
	assert.Equal(t, "http-header", anchor("HTTP header"))
}

func TestTypeNamesAreUnique(t *testing.T) {
	registry := engine.NewRegistry()
	require.NoError(t, nodes.Register(registry, nodes.Deps{
		Feed: noFeed{}, Traders: noTraders{}, Messenger: noTelegram{}, TelegramInbox: noTelegram{}, Asker: noTelegram{}, Code: noCode{},
		Orders: noOrders{}, Books: noBooks{}, Memory: noMemory{},
		Logger: logging.Noop(),
	}))

	seen := map[string]string{}
	for _, typ := range registry.Types() {
		name := tscompiler.TypeName(typ.Name)
		assert.Empty(t, seen[name], "%s and %s are both %s in TypeScript", seen[name], typ.Name, name)
		seen[name] = typ.Name
	}
}

func TestTablePage(t *testing.T) {
	page := nodePage(engine.TypeInfo{NodeType: engine.NodeType{
		Name: "test.copy", Label: "Copy", Description: "Copies traders.", Inputs: []string{"main"}, Outputs: []string{"main"},
		Params: []engine.ParamSpec{{Name: "traders", Label: "Traders", Kind: engine.ParamTable, Columns: []engine.ParamSpec{
			{Name: "address", Label: "Address", Kind: engine.ParamAddress, Required: true, Description: "Their wallet."},
			{Name: "side", Label: "Side", Kind: engine.ParamString, Options: []string{"buy", "sell"}},
			{Name: "on", Label: "On", Kind: engine.ParamBool, Default: true},
		}}},
	}}, 1, nil)

	assert.Contains(t, page, "| Traders (`traders`) | table, its columns below | no |  |  |")
	assert.Contains(t, page, "### Traders columns\n\nEach row of **Traders** has these cells.")
	assert.Contains(t, page, "| Address (`address`) | address: `0x` and 40 hex digits | yes |  | Their wallet. |")
	assert.Contains(t, page, "| Side (`side`) | text | no |  | One of `buy`, `sell`. |")
	assert.Contains(t, page, "| On (`on`) | true or false | no | `true` |  |")
}
