package chance_test

import (
	"fmt"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"

	"github.com/lein3000zzz/polythought-go/internal/core/nodes/chance"
)

func TestTaken(t *testing.T) {
	thirty := decimal.NewFromInt(30)
	taken := func(node string) []bool {
		out := make([]bool, 10_000)
		for i := range out {
			out[i] = chance.Taken("wf-1", "sample", fmt.Sprintf("0xtx%d", i), thirty)
			if node != "sample" {
				out[i] = chance.Taken("wf-1", "sample2", fmt.Sprintf("0xtx%d", i), thirty)
			}
		}

		return out
	}

	first, again, other := taken("sample"), taken("sample"), taken("sample2")
	assert.Equal(t, first, again, "the same answer every time")
	assert.NotEqual(t, first, other, "another node picks others")

	n := 0
	for _, ok := range first {
		if ok {
			n++
		}
	}

	assert.InDelta(t, 3000, n, 150, "about 3 in 10")

	assert.False(t, chance.Taken("wf-1", "sample", "0xtx", decimal.Zero))
	assert.True(t, chance.Taken("wf-1", "sample", "0xtx", decimal.NewFromInt(100)))
	assert.True(t, chance.Taken("wf-1", "sample", "0xtx", decimal.NewFromInt(250)), "past all is all")
}
