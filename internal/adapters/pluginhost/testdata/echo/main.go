// Echo is the host's test plugin: a node of each shape, a trigger, a credential type and
// a scenario, and a node that misbehaves.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/lein3000zzz/polythought-go/pkg/plugin"
)

var manifest = plugin.Manifest{
	ID: "echo", Version: "1.2.0", Name: "Echo", Description: "Says it back.",
	Nodes: []plugin.NodeType{
		{
			Name: "shout", Label: "Shout", Outputs: []string{"main"},
			Params: []plugin.Param{{Name: "field", Kind: "string", Default: "text"}},
			Items:  &plugin.Items{Passed: true, Fields: []plugin.Field{{Name: "shouted", Kind: "text"}}},
		},
		{Name: "count", Label: "Count", Items: &plugin.Items{Fields: []plugin.Field{{Name: "count", Kind: "number"}}}},
		{Name: "env", Label: "Environment"},
		{Name: "misbehave", Label: "Misbehave", Params: []plugin.Param{{Name: "how", Kind: "string"}}},
		{
			Name: "keyed", Label: "Keyed",
			Params: []plugin.Param{{Name: "key", Kind: "credential", CredentialTypes: []string{"token", "http.header"}}},
		},
	},
	Triggers: []plugin.NodeType{
		{Name: "tick", Label: "Tick", Params: []plugin.Param{{Name: "times", Kind: "number", Default: 3}}},
	},
	CredentialTypes: []plugin.CredentialType{{
		Name: "token", Label: "Echo token", Testable: true,
		Fields: []plugin.CredentialField{{Name: "token", Label: "Token", Required: true, Secret: true}},
	}},
	Scenarios: []plugin.Scenario{{
		ID: "demo", Name: "Echo demo", Summary: "Shouts what comes in.",
		Nodes:       json.RawMessage(`[{"id":"manual","type":"trigger.manual","position":{"x":0,"y":0}},{"id":"shout","type":"plugin.echo.shout","params":{"field":"question"},"position":{"x":300,"y":0}}]`),
		Connections: json.RawMessage(`[{"from":"manual","fromPort":"main","to":"shout","toPort":"main"}]`),
	}},
}

func main() {
	plugin.Serve(plugin.Plugin{
		Manifest: manifest,
		Nodes: map[string]plugin.Node{
			"shout":     plugin.PerItem(shout),
			"count":     plugin.NodeFunc(count),
			"env":       plugin.PerItem(env),
			"misbehave": plugin.NodeFunc(misbehave),
			"keyed": plugin.PerItem(func(context.Context, plugin.Run, plugin.Item) (string, map[string]any, error) {
				return "", map[string]any{}, nil
			}),
		},
		Triggers: map[string]plugin.Trigger{"tick": tick{}},
		Credentials: map[string]plugin.CredentialChecks{"token": {
			Check: func(values map[string]string) error {
				if !strings.HasPrefix(values["token"], "tok_") {
					return errors.New("a token starts with tok_")
				}

				return nil
			},
			Test: func(_ context.Context, values map[string]string) error {
				if values["token"] != "tok_good" {
					return errors.New("the echo service refused it")
				}

				return nil
			},
		}},
	})
}

func shout(_ context.Context, run plugin.Run, item plugin.Item) (string, map[string]any, error) {
	field, _ := item.Params["field"].(string)

	text, ok := item.JSON[field].(string)
	if !ok {
		return "", nil, fmt.Errorf("no text in %s", field)
	}

	out := map[string]any{"shouted": strings.ToUpper(text), "owner": run.OwnerID}
	for k, v := range item.JSON {
		out[k] = v
	}

	return "", out, nil
}

func count(_ context.Context, _ plugin.Run, in plugin.Ports) (plugin.Ports, error) {
	var sources []plugin.ItemRef
	for i := range in[plugin.MainPort] {
		sources = append(sources, plugin.ItemRef{Port: plugin.MainPort, Index: i})
	}

	out := plugin.Ports{}
	out.Add(plugin.MainPort, plugin.Item{JSON: map[string]any{"count": len(in[plugin.MainPort])}, Sources: sources})

	return out, nil
}

// env is what the plugin's process can see of the server's environment: nothing.
func env(context.Context, plugin.Run, plugin.Item) (string, map[string]any, error) {
	var names []string
	for _, kv := range os.Environ() {
		names = append(names, strings.SplitN(kv, "=", 2)[0])
	}

	return "", map[string]any{"names": strings.Join(names, ","), "secret": os.Getenv("POLYTHOUGHT_SECRET_KEYS")}, nil
}

func misbehave(_ context.Context, _ plugin.Run, in plugin.Ports) (plugin.Ports, error) {
	how := ""
	if items := in[plugin.MainPort]; len(items) > 0 {
		how, _ = items[0].Params["how"].(string)
	}

	out := plugin.Ports{}

	switch how {
	case "fail":
		return nil, errors.New("told to fail")
	case "port":
		out.Add("nowhere", plugin.Item{JSON: map[string]any{}})
	case "source":
		out.Add(plugin.MainPort, plugin.Item{JSON: map[string]any{}, Sources: []plugin.ItemRef{{Port: plugin.MainPort, Index: 9}}})
	case "crash":
		os.Exit(3)
	}

	return out, nil
}

// tick emits times events, 0 for until it is stopped, each saying its turn and whether the
// last one's execution failed.
type tick struct{}

func (tick) Start(ctx context.Context, _ plugin.Run, params map[string]any, resumed bool, emit plugin.Emit) error {
	times := 3
	if n, ok := params["times"].(json.Number); ok {
		v, _ := n.Int64()
		times = int(v)
	}

	last := ""

	for i := 0; times == 0 || i < times; i++ {
		err := emit(ctx, plugin.Ports{plugin.MainPort: {{JSON: map[string]any{"n": i, "resumed": resumed, "lastFailed": last}}}})
		if ctx.Err() != nil {
			return nil
		}

		last = ""
		if err != nil {
			last = err.Error()
		}
	}

	return nil
}
