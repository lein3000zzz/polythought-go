package pluginhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"maps"
	"slices"
	"time"

	"google.golang.org/grpc/status"

	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	pluginv1 "github.com/lein3000zzz/polythought-go/pkg/plugin/v1"
)

// proxyNode is a plugin's node, run by the plugin: the host sends it the run's items with
// their settings worked out, expressions and all (D286), and takes back what it sends on.
type proxyNode struct {
	p      *Plugin
	name   string
	typ    engine.NodeType
	trades bool // its manifest marks it trades (D309)
}

var (
	_ engine.Resumer = (*proxyNode)(nil)
	_ engine.Trigger = (*proxyTrigger)(nil)
)

// notWaited closes a question its run did not wait on: it failed, or went on without
// the answer.
const notWaited = "⏹ <i>No longer waiting for an answer.</i>"

func (n *proxyNode) Type() engine.NodeType { return n.typ }

func (n *proxyNode) Run(ctx context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	return n.run(ctx, rc, in, nil)
}

// Resume finishes a run that waited on the question it asked (D291): with a quarter of
// the time left, the owner is reminded once and the run waits on; then the question
// closes, saying how it ended, and the plugin's Resume gets the answer.
func (n *proxyNode) Resume(ctx context.Context, rc engine.RunContext, in engine.Ports, r engine.Resumption) (engine.Ports, error) {
	w, err := decodeWaiting(r.State)
	if err != nil {
		return nil, err
	}

	q, api := w.Question, n.p.api

	if r.TimedOut && q.Remind {
		// A reminder that does not arrive is no reason to give up on the answer.
		if remind := api.deps.Telegram.Reminder; remind != nil {
			_ = remind.RemindQuestion(ctx, rc.OwnerID, rc.WorkflowID, q.Message, time.Until(q.Deadline))
		}

		q.Remind = false

		return nil, &engine.Wait{Until: q.Deadline, Key: q.Key, State: waiting{Question: q, State: w.State}.encode()}
	}

	resume := &pluginv1.Resume{TimedOut: r.TimedOut, State: []byte(w.State)}
	closing := "⌛ <i>No answer in time.</i>"

	if !r.TimedOut {
		i, err := engine.Params(r.Payload).Int("option")
		if err != nil || i < 0 || i >= len(q.Options) {
			return nil, fmt.Errorf("an answer for button %v, of %d", r.Payload["option"], len(q.Options))
		}

		resume.Option, resume.Label = int32(i), q.Options[i] //nolint:gosec // one of 10
		closing = "✅ <b>" + html.EscapeString(q.Options[i]) + "</b>"
	}

	api.closeQuestion(ctx, rc.OwnerID, q, closing)

	return n.run(ctx, rc, in, resume)
}

// run sends the run's items to the plugin, its Run's or, with resume, its Resume's, a
// session open for its calls to the host while it runs. A run that asked the host past
// its limit fails, whatever the plugin made of the refusal; one that asked a question
// and did not wait on it closes it.
func (n *proxyNode) run(ctx context.Context, rc engine.RunContext, in engine.Ports, resume *pluginv1.Resume) (engine.Ports, error) {
	req := &pluginv1.RunNodeRequest{
		Node: n.name, Run: runContext(string(rc.ExecutionID), string(rc.WorkflowID), string(rc.OwnerID), string(rc.NodeID), rc.StartedAt),
		Resume: resume,
	}

	s := &session{ctx: ctx, rc: rc, typ: n.typ, trades: n.trades, params: map[engine.ItemRef]engine.Params{}}

	for _, port := range slices.Sorted(maps.Keys(in)) {
		input := &pluginv1.Input{Port: port}

		for i, it := range in[port] {
			ref := engine.ItemRef{Port: port, Index: i}

			params, err := rc.Params.Resolve(ref)
			if err != nil {
				return nil, err
			}

			s.params[ref] = params

			j, err := json.Marshal(orEmpty(it.JSON))
			if err != nil {
				return nil, fmt.Errorf("an item: %w", err)
			}

			p, err := json.Marshal(map[string]any(params))
			if err != nil {
				return nil, fmt.Errorf("an item's settings: %w", err)
			}

			input.Items = append(input.Items, &pluginv1.InputItem{Json: j, Params: p})
		}

		req.Inputs = append(req.Inputs, input)
	}

	token, closed, err := n.p.api.open(s)
	if err != nil {
		return nil, err
	}
	defer closed()

	req.Run.Session = token

	rpc, err := n.p.rpc()
	if err != nil {
		return nil, err
	}

	res, err := rpc.RunNode(ctx, req)
	q := s.asked()

	out, wait, err := n.answered(ctx, s, in, res, err, q)
	if q != nil && wait == nil {
		n.p.api.closeQuestion(ctx, rc.OwnerID, *q, notWaited)
	}

	if wait != nil {
		return nil, wait
	}

	return out, err
}

// answered is what the plugin's answer to a run comes to: its items, or the wait on the
// question it asked.
func (n *proxyNode) answered(ctx context.Context, s *session, in engine.Ports, res *pluginv1.RunNodeResponse, err error, q *question) (engine.Ports, *engine.Wait, error) {
	id := n.p.manifest.ID

	switch {
	case s.overLimit():
		return nil, nil, fmt.Errorf("plugin %s asked the host more than the %d times a run may", id, s.limits.HostCalls)
	case err != nil && ctx.Err() != nil:
		return nil, nil, ctx.Err()
	case err != nil:
		return nil, nil, fmt.Errorf("plugin %s: %w", id, callError(err))
	case res.GetWait() == nil:
		out, err := n.p.ports(n.typ, in, res.GetOutputs())
		return out, nil, err
	case q == nil:
		return nil, nil, fmt.Errorf("plugin %s waited, but asked no question", id)
	case len(res.GetOutputs()) > 0:
		return nil, nil, fmt.Errorf("plugin %s sent items and waited too", id)
	}

	wait, err := waitOn(*q, res.GetWait().GetState())
	if err != nil {
		return nil, nil, fmt.Errorf("plugin %s waited: %w", id, err)
	}

	return nil, wait, nil
}

// proxyTrigger is a plugin's trigger, run by the plugin: each event it sends starts an
// execution, and is answered once the execution is done, as a built-in trigger's emit
// returns then.
type proxyTrigger struct {
	p    *Plugin
	name string
	typ  engine.NodeType
}

func (t *proxyTrigger) Type() engine.NodeType { return t.typ }

func (t *proxyTrigger) Start(ctx context.Context, tc engine.TriggerContext, emit engine.Emit) error {
	params, err := json.Marshal(map[string]any(tc.Params))
	if err != nil {
		return fmt.Errorf("its settings: %w", err)
	}

	// Its calls to the host are counted by the minute, for as long as it runs.
	token, closed, err := t.p.api.open(&session{
		ctx: ctx, trigger: true, typ: t.typ, params: map[engine.ItemRef]engine.Params{{}: tc.Params},
		rc: engine.RunContext{WorkflowID: tc.WorkflowID, OwnerID: tc.OwnerID, NodeID: tc.NodeID},
	})
	if err != nil {
		return err
	}
	defer closed()

	rpc, err := t.p.rpc()
	if err != nil {
		return err
	}

	stream, err := rpc.RunTrigger(ctx)
	if err != nil {
		return fmt.Errorf("plugin %s: %w", t.p.manifest.ID, callError(err))
	}

	start := &pluginv1.TriggerStart{
		Trigger: t.name, Run: runContext("", string(tc.WorkflowID), string(tc.OwnerID), string(tc.NodeID), time.Time{}),
		Params: params, Resumed: tc.Resumed,
	}
	start.Run.Session = token

	if err := stream.Send(&pluginv1.RunTriggerRequest{Msg: &pluginv1.RunTriggerRequest_Start{Start: start}}); err != nil {
		return t.ended(ctx, err)
	}

	for {
		event, err := stream.Recv()
		if err != nil {
			return t.ended(ctx, err)
		}

		out, err := t.p.ports(t.typ, nil, event.GetOutputs())
		if err == nil {
			err = emit(ctx, out)
		}

		ack := &pluginv1.TriggerAck{}
		if err != nil {
			ack.Error = err.Error()
		}

		if err := stream.Send(&pluginv1.RunTriggerRequest{Msg: &pluginv1.RunTriggerRequest_Ack{Ack: ack}}); err != nil {
			return t.ended(ctx, err)
		}
	}
}

// ended is how a trigger's stream ending ends it: quietly when the host stopped it or the
// plugin was done, with why otherwise.
func (t *proxyTrigger) ended(ctx context.Context, err error) error {
	if ctx.Err() != nil || errors.Is(err, io.EOF) {
		return nil
	}

	return fmt.Errorf("plugin %s: trigger %s: %w", t.p.manifest.ID, t.name, callError(err))
}

// ports are what a plugin sends out as the engine's, held to the node's ports: an item on
// a port the node has not, or naming an item it was not given, fails the node.
func (p *Plugin) ports(typ engine.NodeType, in engine.Ports, outputs []*pluginv1.Output) (engine.Ports, error) {
	out := engine.Ports{}

	for _, o := range outputs {
		port := o.GetPort()

		for _, it := range o.GetItems() {
			j, err := decodeObject(it.GetJson())
			if err != nil {
				return nil, fmt.Errorf("plugin %s sent an item that is no JSON object: %w", p.manifest.ID, err)
			}

			var sources []engine.ItemRef

			for _, src := range it.GetSources() {
				ref := engine.ItemRef{Port: src.GetPort(), Index: int(src.GetIndex())}
				if ref.Index < 0 || ref.Index >= len(in[ref.Port]) {
					return nil, fmt.Errorf("plugin %s sent an item from %s %d, which it was not given", p.manifest.ID, ref.Port, ref.Index)
				}

				sources = append(sources, ref)
			}

			if msg := it.GetError(); msg != "" {
				out.Add(engine.ErrorPort, engine.Item{JSON: j, Source: sources, Err: errors.New(msg)})
				continue
			}

			if !slices.Contains(typ.Outputs, port) && typ.OutputsParam == "" {
				return nil, fmt.Errorf("plugin %s sent an item out of %q, which the node has not", p.manifest.ID, port)
			}

			out.Add(port, engine.Item{JSON: j, Source: sources})
		}
	}

	return out, nil
}

func runContext(execution, workflow, owner, node string, startedAt time.Time) *pluginv1.RunContext {
	rc := &pluginv1.RunContext{ExecutionId: execution, WorkflowId: workflow, OwnerId: owner, NodeId: node}

	if !startedAt.IsZero() {
		rc.StartedAt = startedAt.UTC().Format(time.RFC3339Nano)
	}

	return rc
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}

	return m
}

// decodeObject reads a JSON object as items keep it: numbers exact.
func decodeObject(raw []byte) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}

	var out map[string]any

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	if err := dec.Decode(&out); err != nil {
		return nil, err
	}

	if out == nil {
		out = map[string]any{}
	}

	return out, nil
}

// callError is a failed call's reason, as the plugin gave it.
func callError(err error) error {
	if s, ok := status.FromError(err); ok {
		return errors.New(s.Message())
	}

	return err
}

func errorOf(msg string) error {
	if msg == "" {
		return nil
	}

	return errors.New(msg)
}
