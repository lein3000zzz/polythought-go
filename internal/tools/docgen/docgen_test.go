package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
)

// The reference pages on disk must be what the code generates now.
func TestReferenceIsCurrent(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "docs", "src", "content", "docs", "reference")

	pages, err := build()
	require.NoError(t, err)

	for path, want := range pages {
		got, err := os.ReadFile(filepath.Join(dir, path))
		require.NoError(t, err, "%s is missing: run make docs-gen", path)
		assert.Equal(t, want, string(got), "%s is stale: run make docs-gen", path)
	}

	onDisk, err := generatedFiles(dir)
	require.NoError(t, err)

	for path := range onDisk {
		rel, err := filepath.Rel(dir, path)
		require.NoError(t, err)
		assert.Contains(t, pages, filepath.ToSlash(rel), "%s is no longer generated: run make docs-gen", rel)
	}
}

func TestEveryNodeHasAPage(t *testing.T) {
	pages, err := build()
	require.NoError(t, err)

	assert.Contains(t, pages, "nodes/triggers/market.md", "the market trigger is documented though it needs a feed")
	assert.Contains(t, pages, "nodes/polymarket/price-change.md")
	assert.Contains(t, pages, "nodes.md")
	assert.Contains(t, pages, "settings.md")
	assert.Contains(t, pages["credential-types.md"], "Used by: [Webhook](/docs/reference/nodes/output/webhook/).")

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
