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

// try asks for the owner's wallets, and to fetch.
func try(ctx context.Context, run plugin.Run, _ plugin.Item) (string, map[string]any, error) {
	_, wallets := run.Host.Wallets(ctx)
	_, fetch := run.Host.Fetch(ctx, plugin.Request{URL: "https://example.com/"})

	return "", map[string]any{
		"wallets": wallets.Error(), "walletsRefused": errors.Is(wallets, plugin.ErrNotAllowed),
		"fetch": fetch.Error(), "fetchRefused": errors.Is(fetch, plugin.ErrNotAllowed),
	}, nil
}
