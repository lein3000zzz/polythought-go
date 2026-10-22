// Movers is Polythought's example plugin (5f): it watches Polymarket markets and tells
// their workflows' owners on Telegram when one moves, a coin's price beside it from
// CoinGecko when asked. "Write a plugin", in the docs, walks through it.
//
// Pack it as admins install it, from the repository's root:
//
//	go run ./tools/pluginpack -o movers-1.0.0.tar.gz -platforms linux_amd64,linux_arm64 ./examples/plugins/movers
package main

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/lein3000zzz/polythought-go/pkg/plugin"
)

var manifest = plugin.Manifest{
	ID:          "movers",
	Version:     "1.0.0",
	Name:        "Market movers",
	Description: "Watches Polymarket markets, and tells you on Telegram when one moves.",

	// Telegram, to tell the owner; CoinGecko, for a coin's price. Polymarket's data
	// needs no permission.
	Permissions: []string{plugin.PermTelegram, plugin.PermFetch + coinGecko},

	Settings: []plugin.Setting{
		{
			Key: "move", Label: "Default move (points)", Kind: "int", Default: 5,
			Description: "How far a market's price must move, in percentage points, for Market moved to say so, unless its own setting says otherwise.",
		},
		{
			Key: "every", Label: "Look every", Kind: "duration", Default: "5m",
			Description: "How often Market moved looks at its markets.",
		},
	},

	Metrics: []plugin.Metric{
		{Name: "moves_total", Kind: "counter", Help: "Markets seen moving past their threshold."},
		{Name: "alerts_total", Kind: "counter", Help: "Alerts sent on Telegram."},
	},

	Triggers: []plugin.NodeType{{
		Name: "moved", Label: "Market moved", Outputs: []string{plugin.MainPort},
		Description: "Starts a run when a market its search finds moves, its first outcome's price, past a threshold since it last said so.",
		// The price each market last said so at, in its store: once per move.
		Remembers: true,
		Params: []plugin.Param{
			{Name: "query", Label: "Markets", Kind: "string", Required: true, Description: "Words of the markets' event titles, as Polymarket's search takes them."},
			{Name: "move", Label: "Move (points)", Kind: "number", Description: "How far, in percentage points; empty for the plugin's setting."},
			{Name: "markets", Label: "Markets watched", Kind: "number", Default: 10, Description: "The most of those it finds that it watches, up to 50."},
		},
		Items: &plugin.Items{Fields: []plugin.Field{
			{Name: "market", Kind: "text", Description: "The market's condition ID."},
			{Name: "slug", Kind: "text"},
			{Name: "question", Kind: "text"},
			{Name: "outcome", Kind: "text", Description: "The outcome whose price moved, the market's first."},
			{Name: "from", Kind: "decimal", Description: "Its price when it last said so."},
			{Name: "to", Kind: "decimal", Description: "Its price now."},
		}},
	}},

	Nodes: []plugin.NodeType{{
		Name: "alert", Label: "Alert me", Outputs: []string{plugin.MainPort},
		Description: "Tells the workflow's owner on Telegram how each market that comes in moved.",
		Params: []plugin.Param{
			{Name: "coin", Label: "Coin, on CoinGecko", Kind: "string", Description: "A CoinGecko coin ID, like bitcoin, whose price in dollars the alert gives too; empty for none."},
			{Name: "key", Label: "CoinGecko key", Kind: "credential", CredentialTypes: []string{"coingecko"}, Description: "Optional: CoinGecko answers without one, more slowly when busy."},
			{Name: "send", Label: "Send on Telegram", Kind: "bool", Default: true, Description: "Off, it only writes the alert, as a preview."},
		},
		Items: &plugin.Items{Passed: true, Fields: []plugin.Field{
			{Name: "message", Kind: "text", Description: "The alert, as sent."},
			{Name: "sent", Kind: "bool"},
		}},
	}},

	CredentialTypes: []plugin.CredentialType{{
		Name: "coingecko", Label: "CoinGecko key",
		Description: "A CoinGecko demo API key, from coingecko.com/en/developers/dashboard.",
		Fields:      []plugin.CredentialField{{Name: "key", Label: "Key", Required: true, Secret: true}},
	}},

	Scenarios: []plugin.Scenario{{
		ID: "alert", Name: "Alert me when a market moves",
		Summary:     "Tells you on Telegram when a Bitcoin market moves 5 points, with Bitcoin's price.",
		Nodes:       json.RawMessage(`[{"id":"moved","type":"plugin.movers.moved","params":{"query":"bitcoin"},"position":{"x":0,"y":0}},{"id":"alert","type":"plugin.movers.alert","params":{"coin":"bitcoin"},"position":{"x":300,"y":0}}]`),
		Connections: json.RawMessage(`[{"from":"moved","fromPort":"main","to":"alert","toPort":"main"}]`),
	}},
}

func main() {
	plugin.Serve(plugin.Plugin{
		Manifest: manifest,
		Triggers: map[string]plugin.Trigger{"moved": moved{}},
		Nodes:    map[string]plugin.Node{"alert": plugin.PerItem(alert)},
		Credentials: map[string]plugin.CredentialChecks{"coingecko": {
			Check: func(values map[string]string) error {
				if !strings.HasPrefix(values["key"], "CG-") {
					return errors.New("a CoinGecko key starts with CG-")
				}

				return nil
			},
		}},
	})
}
