// Bare is a test plugin that asks for no permission at all, so the host refuses what
// takes one.
package main

import (
	"context"
	"errors"

	"github.com/lein3000zzz/polythought-go/pkg/plugin"
)

func main() {
	plugin.Serve(plugin.Plugin{
		Manifest: plugin.Manifest{
			ID: "bare", Version: "0.1.0", Name: "Bare",
			Nodes: []plugin.NodeType{{Name: "try", Label: "Try"}},
		},
		Nodes: map[string]plugin.Node{"try": plugin.PerItem(try)},
	})
}

// try asks for all that takes a permission: the owner's wallets, a fetch, a message, a
// question and an order.
func try(ctx context.Context, run plugin.Run, _ plugin.Item) (string, map[string]any, error) {
	_, wallets := run.Host.Wallets(ctx)
	_, fetch := run.Host.Fetch(ctx, plugin.Request{URL: "https://example.com/"})
	message := run.Host.Message(ctx, plugin.Message{Text: "hi"})
	_, ask := run.Host.Ask(ctx, plugin.Question{Text: "?", Options: []string{"Yes"}})
	_, orders := run.Host.PlaceOrders(ctx, []plugin.Order{{TokenID: "t", Side: "buy", Type: "FOK", Price: "0.5", Shares: "1"}})

	out := map[string]any{}
	for name, err := range map[string]error{"wallets": wallets, "fetch": fetch, "message": message, "ask": ask, "orders": orders} {
		out[name] = err.Error()
		out[name+"Refused"] = errors.Is(err, plugin.ErrNotAllowed)
	}

	return "", out, nil
}
