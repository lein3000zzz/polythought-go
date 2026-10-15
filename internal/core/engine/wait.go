package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/ports"
)

// Executions can wait: a node pauses one, say until a time or until someone answers, and
// it carries on from there later, even after a restart. The executor keeps a snapshot of
// the paused execution in a WaitRepository: the workflow as it ran, the waiting node and
// its input, and the input already gathered for every node not run yet. Items go through
// JSON in it, so nodes after a wait must take JSON-decoded values: decimals as strings or
// json.Number, times as RFC 3339 text.

var (
	// ErrCannotWait means a node asked to wait where it cannot: it does not implement
	// Resumer, or the executor keeps no waits.
	ErrCannotWait = errors.New("cannot wait here")

	// ErrWaitRefused means a wait is beyond the limits.
	ErrWaitRefused = errors.New("wait refused")
)

// maxSnapshot bounds what a paused execution keeps: waiting with a whole market list in
// flight is a mistake worth reporting.
const maxSnapshot = 4 << 20

// Wait is what a node returns from Run, as its error, to pause the execution: until Until
// at the latest, or until something resumes it sooner. Only nodes that implement Resumer
// may wait.
type Wait struct {
	Until time.Time

	// Key, when set, lets the wait be answered: an answer must bring it. Make it random
	// for each question, so an answer to an earlier one cannot answer this.
	Key string

	// State is kept for the node's Resume. It goes through JSON.
	State map[string]any
}

func (w *Wait) Error() string {
	return "waiting until " + w.Until.UTC().Format(time.RFC3339)
}

// Resumer is a node that can wait. Resume finishes the run that waited, with the input it
// had, once the wait ends.
type Resumer interface {
	Node
	Resume(ctx context.Context, rc RunContext, in Ports, r Resumption) (Ports, error)
}

// Resumption is how a wait ended.
type Resumption struct {
	TimedOut bool           // its time ran out; otherwise something answered it
	Payload  map[string]any // the answer, when there was one
	State    map[string]any // what the node kept when it waited
}

// WaitLimits keep waiting executions in check. Zero means no limit.
type WaitLimits struct {
	MaxWaiting int           // executions waiting at once, per owner
	MaxWait    time.Duration // how far ahead a wait may end
}

// WithWaits lets nodes pause executions, keeping them in waits within limits. Without
// it, a node that asks to wait fails.
func WithWaits(waits ports.WaitRepository, limits func() WaitLimits) ExecutorOption {
	return func(e *Executor) { e.waits, e.waitLimits = waits, limits }
}

// pause is what stops the walk when a node waits.
type pause struct {
	node      domain.NodeID
	in        Ports
	wait      *Wait
	remaining []domain.NodeID // nodes after it, in order
}

func (p *pause) Error() string {
	return fmt.Sprintf("node %q is %s", p.node, p.wait.Error())
}

// snapshot is a paused execution, as kept in its wait record.
type snapshot struct {
	Execution domain.Execution           `json:"execution"`
	Workflow  domain.Workflow            `json:"workflow"`
	Node      domain.NodeID              `json:"node"`
	Input     portsDoc                   `json:"input"`
	Pending   map[domain.NodeID]portsDoc `json:"pending,omitempty"`
	Remaining []domain.NodeID            `json:"remaining"`
	Samples   bool                       `json:"samples,omitempty"`
	State     map[string]any             `json:"state,omitempty"`
}

type portsDoc map[string][]itemDoc

type itemDoc struct {
	JSON   map[string]any `json:"json"`
	Source []ItemRef      `json:"source,omitempty"`
	Error  string         `json:"error,omitempty"`
}

func toPortsDoc(p Ports) portsDoc {
	out := make(portsDoc, len(p))

	for port, items := range p {
		docs := make([]itemDoc, len(items))
		for i, it := range items {
			docs[i] = itemDoc{JSON: it.JSON, Source: it.Source}
			if it.Err != nil {
				docs[i].Error = it.Err.Error()
			}
		}

		out[port] = docs
	}

	return out
}

func (d portsDoc) ports() Ports {
	out := make(Ports, len(d))

	for port, docs := range d {
		items := make([]Item, len(docs))
		for i, doc := range docs {
			items[i] = Item{JSON: doc.JSON, Source: doc.Source}
			if doc.Error != "" {
				items[i].Err = errors.New(doc.Error)
			}
		}

		out[port] = items
	}

	return out
}

func encodeSnapshot(x *execution, p *pause) (json.RawMessage, error) {
	snap := snapshot{
		Execution: *x.exec, Workflow: x.workflow, Node: p.node, Input: toPortsDoc(p.in),
		Pending: make(map[domain.NodeID]portsDoc, len(x.inputs)), Remaining: p.remaining,
		Samples: x.samples, State: p.wait.State,
	}

	for id, in := range x.inputs {
		snap.Pending[id] = toPortsDoc(in)
	}

	raw, err := json.Marshal(snap)
	if err != nil {
		return nil, fmt.Errorf("%w: its items cannot be kept: %w", ErrCannotWait, err)
	}

	if len(raw) > maxSnapshot {
		return nil, fmt.Errorf("%w: %d KiB of items are in flight, more than the %d KiB a wait keeps",
			ErrWaitRefused, len(raw)>>10, maxSnapshot>>10)
	}

	return raw, nil
}

// decodeSnapshot reads numbers as json.Number, so decimals stay exact.
func decodeSnapshot(raw json.RawMessage) (snapshot, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var snap snapshot
	if err := dec.Decode(&snap); err != nil {
		return snapshot{}, fmt.Errorf("engine: decode a waiting execution: %w", err)
	}

	return snap, nil
}
