// Package plugin is the SDK for Polythought plugins (phase 5): programs the server starts,
// whose nodes, triggers, credential types and scenarios join the built-in ones. A plugin
// describes itself in a Manifest, implements what it declares, and calls Serve from main:
//
//	func main() {
//		plugin.Serve(plugin.Plugin{
//			Manifest: manifest,
//			Nodes:    map[string]plugin.Node{"forecast": plugin.PerItem(forecast)},
//		})
//	}
//
// It speaks the contract in pkg/plugin/v1 over gRPC, through hashicorp/go-plugin.
package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// SDKVersion is this SDK's version, semver: a plugin built with another major version is
// refused.
const SDKVersion = "1.0.0"

// Plugin is a plugin: its manifest, and what implements it, each by the name its manifest
// gives it.
type Plugin struct {
	Manifest    Manifest
	Nodes       map[string]Node
	Triggers    map[string]Trigger
	Credentials map[string]CredentialChecks
}

// Run is the run a node or trigger works in.
type Run struct {
	ExecutionID string // empty for a trigger
	WorkflowID  string
	OwnerID     string // whose workflow it is: the node acts for them
	NodeID      string
	StartedAt   time.Time

	// Host is what the run may ask of Polythought (5b). Log logs through it, with the
	// plugin's name and the run's.
	Host Host
	Log  *slog.Logger
}

// Item is one item: its fields, as JSON decodes them (numbers json.Number, decimals text),
// and, coming in, the node's settings for it, expressions worked out.
type Item struct {
	JSON   map[string]any
	Params map[string]any
	// Sources are the input items an item going out came from; Err fails it, sending it
	// out of the error port with the reason.
	Sources []ItemRef
	Err     string

	// ref is where an input item came in, for the host to find its settings.
	ref *ItemRef
}

// ItemRef names an input item: its port and place there.
type ItemRef struct {
	Port  string
	Index int
}

// Ports are items by port.
type Ports map[string][]Item

// MainPort is the port nodes have unless they say: the one items usually come in and go
// out of.
const MainPort = "main"

func (p Ports) Add(port string, it Item) {
	p[port] = append(p[port], it)
}

// Fail sends the input item at src out as failed, with the reason.
func (p Ports) Fail(in Item, src ItemRef, err error) {
	p.Add(MainPort, Item{JSON: in.JSON, Sources: []ItemRef{src}, Err: err.Error()})
}

// Node is one of the plugin's nodes: it gets a run's items, by port, and returns those it
// sends on, by port. An error fails the node, as bad settings would.
type Node interface {
	Run(ctx context.Context, run Run, in Ports) (Ports, error)
}

// NodeFunc is a Node of a function.
type NodeFunc func(ctx context.Context, run Run, in Ports) (Ports, error)

func (f NodeFunc) Run(ctx context.Context, run Run, in Ports) (Ports, error) { return f(ctx, run, in) }

// PerItem is a node of a function of one item, for each item on main: it gives the port
// the item goes out of, empty for main, and its fields, nil to send nothing on. An error
// fails that item alone.
func PerItem(fn func(ctx context.Context, run Run, item Item) (port string, out map[string]any, err error)) Node {
	return NodeFunc(func(ctx context.Context, run Run, in Ports) (Ports, error) {
		out := Ports{}

		for i, item := range in[MainPort] {
			src := ItemRef{Port: MainPort, Index: i}

			port, j, err := fn(ctx, run, item)
			switch {
			case err != nil:
				out.Fail(item, src, err)
			case j != nil:
				if port == "" {
					port = MainPort
				}

				out.Add(port, Item{JSON: j, Sources: []ItemRef{src}})
			}
		}

		return out, nil
	})
}

// Emit starts an execution with what a trigger emits, by port, and returns once it is
// done, with its failure if it failed.
type Emit func(ctx context.Context, out Ports) error

// Trigger is one of the plugin's triggers: it starts executions with emit until ctx is
// done, its settings worked out. Resumed says the workflow was running before this start,
// so a trigger that can look back may catch up.
type Trigger interface {
	Start(ctx context.Context, run Run, params map[string]any, resumed bool, emit Emit) error
}

// CredentialChecks check one of the plugin's credential types' values: Check their form,
// as they are saved; Test them against the service they are for, with the type Testable.
type CredentialChecks struct {
	Check func(values map[string]string) error
	Test  func(ctx context.Context, values map[string]string) error
}

// check makes sure the plugin implements what its manifest declares, and nothing else.
func (p Plugin) check() error {
	m := p.Manifest

	var errs []error

	for _, n := range m.Nodes {
		if p.Nodes[n.Name] == nil {
			errs = append(errs, fmt.Errorf("node %q has no implementation", n.Name))
		}
	}

	for _, t := range m.Triggers {
		if p.Triggers[t.Name] == nil {
			errs = append(errs, fmt.Errorf("trigger %q has no implementation", t.Name))
		}
	}

	for _, c := range m.CredentialTypes {
		if checks, ok := p.Credentials[c.Name]; c.Testable && (!ok || checks.Test == nil) {
			errs = append(errs, fmt.Errorf("credential type %q is testable but has no test", c.Name))
		}
	}

	declared := func(kind string, names map[string]bool, have []string) {
		for _, n := range have {
			if !names[n] {
				errs = append(errs, fmt.Errorf("%s %q is not in the manifest", kind, n))
			}
		}
	}

	declared("node", namesOf(m.Nodes), keys(p.Nodes))
	declared("trigger", namesOf(m.Triggers), keys(p.Triggers))
	declared("credential type", credentialNames(m.CredentialTypes), keys(p.Credentials))

	return errors.Join(errs...)
}

func namesOf(types []NodeType) map[string]bool {
	out := make(map[string]bool, len(types))
	for _, t := range types {
		out[t.Name] = true
	}

	return out
}

func credentialNames(types []CredentialType) map[string]bool {
	out := make(map[string]bool, len(types))
	for _, t := range types {
		out[t.Name] = true
	}

	return out
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	return out
}

// decodeObject reads a JSON object as items keep it: numbers as json.Number, exact.
func decodeObject(raw []byte) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}

	var out map[string]any

	dec := json.NewDecoder(bytesReader(raw))
	dec.UseNumber()

	if err := dec.Decode(&out); err != nil {
		return nil, err
	}

	if out == nil {
		out = map[string]any{}
	}

	return out, nil
}
