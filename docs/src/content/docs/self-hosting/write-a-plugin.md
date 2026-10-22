---
title: Write a plugin
description: Build a plugin in Go with Polythought's SDK, test it, pack it, sign it and install it.
---

A plugin is a Go program built with Polythought's SDK, the package
`github.com/lein3000zzz/polythought-go/pkg/plugin`. It brings nodes, triggers, credential types
and scenarios, which an admin installs from an archive on the
[Plugins page](/docs/self-hosting/plugins/).

This page walks through the example in `examples/plugins/movers`, **Market movers**. Its trigger
watches the Polymarket markets a search finds and starts a run when one moves past a threshold.
Its node tells the workflow's owner on Telegram, with a coin's price from CoinGecko beside the
move when the node names a coin.

## The module

A plugin is a Go module of its own, which requires the SDK:

```go title="go.mod"
module github.com/lein3000zzz/polythought-go/examples/plugins/movers

go 1.27.0

require github.com/lein3000zzz/polythought-go v0.0.0

// The SDK from this repository; an author's plugin requires a released version instead.
replace github.com/lein3000zzz/polythought-go => ../../..
```

The SDK's major version has to be the server's: the server refuses a plugin built with
another.

## The manifest

The manifest says everything about the plugin that the server needs before running it: what it
is, what it may ask for, its settings and metrics, and its nodes, triggers, credential types and
scenarios. Its nodes and triggers are `plugin.<id>.<name>` to the rest, so Market movers' are
`plugin.movers.moved` and `plugin.movers.alert`.

```go title="main.go"
var manifest = plugin.Manifest{
	ID:          "movers",
	Version:     "1.0.0",
	Name:        "Market movers",
	Description: "Watches Polymarket markets, and tells you on Telegram when one moves.",

	Permissions: []string{plugin.PermTelegram, plugin.PermFetch + "api.coingecko.com"},

	Settings: []plugin.Setting{
		{Key: "move", Label: "Default move (points)", Kind: "int", Default: 5},
		{Key: "every", Label: "Look every", Kind: "duration", Default: "5m"},
	},

	Triggers: []plugin.NodeType{{
		Name: "moved", Label: "Market moved", Outputs: []string{plugin.MainPort},
		Remembers: true,
		Params: []plugin.Param{
			{Name: "query", Label: "Markets", Kind: "string", Required: true},
			{Name: "move", Label: "Move (points)", Kind: "number"},
		},
	}},
	// Nodes, CredentialTypes, Metrics and Scenarios, as in examples/plugins/movers.
}
```

- **Permissions** are what an admin approves, one by one, before the plugin runs. The
  [Plugins page](/docs/self-hosting/plugins/#approving) lists each. Market movers asks for
  `telegram`, to message owners, and `fetch:api.coingecko.com`, for prices. Reading
  Polymarket's public data needs none.
- **Settings** are the plugin's own, which an admin sets on its card: `string`, `int`,
  `bool`, `duration`, `enum` or `list`, `Secret` ones sealed.
- **Params** are a node's settings in the editor: `string`, `number`, `decimal`, `bool`,
  `list`, `duration`, `wallet`, `tokens`, `address`, `table` or `credential`. The host works out
  expressions in them before the node gets them.
- **Remembers** gives a node or trigger a store of its own in each workflow. **Trades** lets a
  node place orders, with the `orders` permission.
- **Items** describe what a node sends on, for the editor and for code written against it.
- **Scenarios** are workflows for the library, written as the built-in ones are.

## main

`plugin.Serve` runs the plugin. It refuses a plugin whose manifest declares a node, trigger or
credential type it does not implement, or that implements one it does not declare.

```go title="main.go"
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
```

Run as `plugin manifest`, the program prints its manifest instead, which is how it is packed.

## A node

A node gets a run's items by port and returns those it sends on. `plugin.PerItem` calls a
function for each item on the main port instead. The function returns the port the item goes
out of (empty for main), its fields, and an error, which fails that item alone and sends it out
of the error port with the reason.

```go title="alert.go"
func alert(ctx context.Context, run plugin.Run, item plugin.Item) (string, map[string]any, error) {
	msg := fmt.Sprintf("%s moved %s points.", text(item.JSON["question"]),
		points(text(item.JSON["from"]), text(item.JSON["to"])))

	if item.Params["send"] != false {
		if err := run.Host.Message(ctx, plugin.Message{Text: msg}); err != nil {
			return "", nil, err
		}
	}

	out := maps.Clone(item.JSON)
	out["message"] = msg

	return "", out, nil
}
```

`run.Host` is the server, for this run only. `run.Log` is a `slog.Logger` that writes to the
server's log with the plugin's name and the run's. `run.Settings` are the plugin's own settings.
Numbers in items, params and settings come as `json.Number`, decimals and durations as text.

## A trigger

A trigger's `Start` runs as long as its workflow is active, and returns once `ctx` is done. Each
call to `emit` starts an execution and waits for it to finish. Market movers looks at its markets,
emits those that moved, and waits for its next look:

```go title="moved.go"
func (moved) Start(ctx context.Context, run plugin.Run, params map[string]any, _ bool, emit plugin.Emit) error {
	w := watch{host: run.Host, query: text(params["query"]), move: 0.05, markets: 10}

	tick := time.NewTicker(5 * time.Minute)
	defer tick.Stop()

	for {
		if movers, err := w.look(ctx); err == nil && len(movers) > 0 {
			_ = emit(ctx, plugin.Ports{plugin.MainPort: movers})
		}

		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}
```

It keeps the price each market last moved at in its store, `run.Host.Set(ctx, "said:"+id, price)`,
so it says each move once, and still knows the markets after a restart of its own or of the
server. `resumed` says the workflow was running before this start, for a trigger that can catch
up on what it missed.

## What the host gives

| Call | Needs |
| --- | --- |
| `Fetch`, an HTTP request | `fetch:<host>` or `fetch:*` |
| `Credential`, the credential a node's setting picks | nothing |
| `Get`, `Set`, `Delete` and `Keys`, the node's store | Remembers |
| `SearchMarkets`, `OrderBook`, `PriceHistory`, `Trades`, `Positions` | nothing |
| `Wallets`, the owner's | `wallets` |
| `Message`, and `Ask`, a question its run waits on | `telegram` |
| `PlaceOrders` and `CancelOrders` | `orders`, and Trades |
| `Metric` and `Span`, the server's metrics and traces | the metric in the manifest |

Each call but `Metric` and `Span` counts against **Data requests per run** on the Config page;
a trigger's, against as many each minute. Logs, metrics and spans are capped apart, at 1,000 a
run. A fetch is held to **Plugin fetch time** and **Plugin fetch size (KiB)**, a
store to **Plugin store per node (KiB)**. A call the permissions do not allow fails with
`plugin.ErrNotAllowed`, one past a limit with `plugin.ErrLimit`.

## Where it runs

On Linux, the server runs a plugin in [its sandbox](/docs/self-hosting/plugins/#the-sandbox),
and a plugin is written for it:

- **No network of its own.** Every request goes through `Host.Fetch`, held to the permissions.
- **Two directories.** `plugin.DataDir()` is its own, kept across restarts and upgrades;
  `os.TempDir()` is emptied when it stops. Nothing else on disk, its own files read only.
- **No other process.** It starts no process, and runs no program but its own.
- **Caps.** Its data and its memory are capped, 1 GiB and 256 MiB unless an admin says. A Go
  plugin gets a soft memory limit under its cap, which its garbage collector keeps to.
- **A static program.** The sandbox gives it no C library, so it is built with CGO off, as
  pluginpack builds it.

One process serves every workflow and every owner using the plugin. Keep what belongs to a run
in that run, its `Run` and its items, never in a global another run could see.

## Testing it

`plugin.Host` is an interface, so a test stands in for the host. Embedding the interface in a
struct leaves out the calls the plugin never makes:

```go title="movers_test.go"
type host struct {
	plugin.Host

	markets  []plugin.Market
	messages []string
}

func (h *host) SearchMarkets(context.Context, string, bool, int) ([]plugin.Market, error) {
	return h.markets, nil
}

func (h *host) Message(_ context.Context, m plugin.Message) error {
	h.messages = append(h.messages, m.Text)
	return nil
}
```

A test then calls the node's function, or the trigger's `Start`, with
`plugin.Run{Host: h, Log: slog.New(slog.DiscardHandler)}`. The example's tests check a market's
first look, a move past the threshold, said once, and an alert sent, previewed, priced, or
failing where Telegram is not linked. Nothing calls CoinGecko.

## Packing it

`tools/pluginpack`, from a checkout of Polythought, builds the program for each platform and
writes the archive admins install:

```sh
go run ./tools/pluginpack -o movers-1.0.0.tar.gz -platforms linux_amd64,linux_arm64 ./examples/plugins/movers
```

The archive holds `manifest.json`, as the program describes itself, `bin/<os>_<arch>/plugin`
for each platform, and `checksums.txt`, the SHA-256 of each. The server checks all of them as
it installs the archive, and again each time it starts the plugin.

To sign it, make a key pair with [minisign](https://jedisct1.github.io/minisign/), `minisign
-G`, and pack with `-sign minisign.key`. Give the key's password, if it has one, in
`MINISIGN_PASSWORD`. Admins add the public key's line, from `minisign.pub`, to **Trusted signing
keys** on the Config page, and the Plugins page then says who signed the plugin.

## Installing it

On **Admin → Plugins**, install the archive, approve what it asks for, and restart the server.
[Plugins](/docs/self-hosting/plugins/) has the rest: who may use it, its settings and caps, and
what happens when it crashes.
