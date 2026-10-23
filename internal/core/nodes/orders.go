package nodes

import (
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/trading"
)

// PlacesOrders says a workflow has a node that places or cancels orders (D399): one of the
// trading nodes, or a plugin's that says it trades (D309).
func PlacesOrders(r *engine.Registry, w domain.Workflow) bool {
	for _, n := range w.Nodes {
		switch n.Type {
		case trading.PlaceOrderType, trading.PlaceOrdersType, trading.CancelOrdersType:
			return true
		}

		if t, _, ok := r.Type(n.Type); ok && t.Trades {
			return true
		}
	}

	return false
}
