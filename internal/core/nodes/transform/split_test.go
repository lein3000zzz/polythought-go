package transform_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/transform"
)

func TestSplitOut(t *testing.T) {
	market := map[string]any{
		"question": "Rain?", "price": "0.5",
		"outcomes": []any{
			map[string]any{"label": "Yes", "price": "0.6", "tokenId": "t-yes"},
			map[string]any{"label": "No", "price": "0.4", "tokenId": "t-no"},
		},
	}

	t.Run("an object entry's fields at the top, with the item's others (D132)", func(t *testing.T) {
		out, err := run(t, transform.SplitOut{}, map[string]any{"field": "outcomes"}, market)
		require.NoError(t, err)

		require.Len(t, out[engine.DefaultPort], 2)
		assert.Equal(t, map[string]any{"question": "Rain?", "label": "Yes", "price": "0.6", "tokenId": "t-yes"},
			out[engine.DefaultPort][0].JSON, "the outcome's price wins, and the list is gone")
		assert.Equal(t, "t-no", out[engine.DefaultPort][1].JSON["tokenId"])
		assert.Equal(t, []engine.ItemRef{{Port: engine.DefaultPort, Index: 0}}, out[engine.DefaultPort][1].Source)
		assert.Len(t, market["outcomes"], 2, "the item it came from is left as it was")
	})

	t.Run("anything else takes the list's place", func(t *testing.T) {
		out, err := run(t, transform.SplitOut{}, map[string]any{"field": "tokens"},
			map[string]any{"q": "Rain?", "tokens": []string{"a", "b"}})
		require.NoError(t, err)

		assert.Equal(t, []map[string]any{{"q": "Rain?", "tokens": "a"}, {"q": "Rain?", "tokens": "b"}}, jsonOf(out))
	})

	t.Run("a list inside an object", func(t *testing.T) {
		out, err := run(t, transform.SplitOut{}, map[string]any{"field": "event.markets"},
			map[string]any{"event": map[string]any{"slug": "rain", "markets": []any{map[string]any{"id": "m1"}}}})
		require.NoError(t, err)

		assert.Equal(t, []map[string]any{{"event": map[string]any{"slug": "rain"}, "id": "m1"}}, jsonOf(out))
	})

	t.Run("an empty list, or none, gives no items; anything but a list fails the item", func(t *testing.T) {
		out, err := run(t, transform.SplitOut{}, map[string]any{"field": "outcomes"},
			map[string]any{"outcomes": []any{}}, map[string]any{"question": "?"}, map[string]any{"outcomes": nil},
			map[string]any{"outcomes": "Yes"})
		require.NoError(t, err)

		assert.Empty(t, out[engine.DefaultPort])
		require.Len(t, out[engine.ErrorPort], 1)
		assert.EqualError(t, out[engine.ErrorPort][0].Err, `field "outcomes" is "Yes", not a list`)
	})
}

func jsonOf(out engine.Ports) []map[string]any {
	var got []map[string]any
	for _, it := range out[engine.DefaultPort] {
		got = append(got, it.JSON)
	}

	return got
}
