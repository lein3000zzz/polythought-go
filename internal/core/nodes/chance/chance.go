// Package chance decides which items a percentage takes (D140): about that share of
// them, each the same way every time for the same workflow node, so a re-run or a
// restart agrees, and other nodes pick others.
package chance

import (
	"crypto/sha256"
	"encoding/binary"

	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
)

var hundred = decimal.NewFromInt(100)

// Taken reports whether the item keyed key is among the percent, 0 to 100, that a node
// takes: its hash, with the workflow and the node's, falls within that share.
func Taken(wf domain.WorkflowID, node domain.NodeID, key string, percent decimal.Decimal) bool {
	switch {
	case !percent.IsPositive():
		return false
	case percent.GreaterThanOrEqual(hundred):
		return true
	}

	sum := sha256.Sum256([]byte(string(wf) + "\x1f" + string(node) + "\x1f" + key))
	at := float64(binary.BigEndian.Uint64(sum[:8])>>11) / (1 << 53) // in [0, 1), evenly
	share, _ := percent.Div(hundred).Float64()

	return at < share
}
