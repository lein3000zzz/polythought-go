package engine_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	metricsmocks "github.com/lein3000zzz/polythought-go/internal/mocks/metrics"
)

var errBoom = errors.New("boom")

const waitFor = 3 * time.Second

type fakeNode struct {
	typ engine.NodeType
	run func(ctx context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error)
}

func (f fakeNode) Type() engine.NodeType { return f.typ }

func (f fakeNode) Run(ctx context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
	return f.run(ctx, rc, in)
}

type fakeTrigger struct {
	typ engine.NodeType
}

func (f fakeTrigger) Type() engine.NodeType { return f.typ }

func (f fakeTrigger) Start(ctx context.Context, _ engine.TriggerContext, _ engine.Emit) error {
	<-ctx.Done()
	return nil
}

func mainPorts(names ...string) []string {
	if len(names) == 0 {
		return []string{engine.DefaultPort}
	}

	return names
}

// eachItem applies fn to every item on the main input, tagging outputs with their source.
func eachItem(fn func(rc engine.RunContext, params engine.Params, item engine.Item, out engine.Ports, src engine.ItemRef) error) func(context.Context, engine.RunContext, engine.Ports) (engine.Ports, error) {
	return func(_ context.Context, rc engine.RunContext, in engine.Ports) (engine.Ports, error) {
		out := engine.Ports{}

		for i, item := range in[engine.DefaultPort] {
			src := engine.ItemRef{Port: engine.DefaultPort, Index: i}

			params, err := rc.Params.Resolve(src)
			if err != nil {
				return nil, err
			}

			if err := fn(rc, params, item, out, src); err != nil {
				return nil, err
			}
		}

		return out, nil
	}
}

func item(kv ...any) engine.Item {
	j := make(map[string]any, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		j[kv[i].(string)] = kv[i+1]
	}

	return engine.Item{JSON: j}
}

// testRegistry registers every fake node type the engine tests use.
func testRegistry(t *testing.T) *engine.Registry {
	t.Helper()

	r := engine.NewRegistry()

	require.NoError(t, r.RegisterTrigger(fakeTrigger{typ: engine.NodeType{Name: "test.trigger", Outputs: mainPorts()}}))

	nodes := []fakeNode{
		{
			// pass copies items and stamps them with its "tag" param.
			typ: engine.NodeType{
				Name: "test.pass", Inputs: mainPorts(), Outputs: mainPorts(),
				Params: []engine.ParamSpec{{Name: "tag", Kind: engine.ParamString, Default: "default"}},
			},
			run: eachItem(func(_ engine.RunContext, p engine.Params, it engine.Item, out engine.Ports, src engine.ItemRef) error {
				tag, err := p.String("tag")
				if err != nil {
					return err
				}

				j := map[string]any{"tag": tag}
				for k, v := range it.JSON {
					if k != "tag" {
						j[k] = v
					}
				}

				out.Add(engine.DefaultPort, engine.Item{JSON: j, Source: []engine.ItemRef{src}})

				return nil
			}),
		},
		{
			// if routes items by v > threshold.
			typ: engine.NodeType{
				Name: "test.if", Inputs: mainPorts(), Outputs: mainPorts("true", "false"),
				Params: []engine.ParamSpec{{Name: "threshold", Kind: engine.ParamDecimal, Required: true}},
			},
			run: eachItem(func(_ engine.RunContext, p engine.Params, it engine.Item, out engine.Ports, src engine.ItemRef) error {
				threshold, err := p.Decimal("threshold")
				if err != nil {
					return err
				}

				v, err := engine.Params(it.JSON).Decimal("v")
				if err != nil {
					return err
				}

				port := "false"
				if v.GreaterThan(threshold) {
					port = "true"
				}

				out.Add(port, engine.Item{JSON: it.JSON, Source: []engine.ItemRef{src}})

				return nil
			}),
		},
		{
			// fail sends items marked bad to the error port.
			typ: engine.NodeType{Name: "test.fail", Inputs: mainPorts(), Outputs: mainPorts()},
			run: eachItem(func(_ engine.RunContext, _ engine.Params, it engine.Item, out engine.Ports, src engine.ItemRef) error {
				if it.JSON["bad"] == true {
					out.Fail(it, src, errBoom)
					return nil
				}

				out.Add(engine.DefaultPort, engine.Item{JSON: it.JSON, Source: []engine.ItemRef{src}})

				return nil
			}),
		},
		{
			// merge concatenates its a and b inputs.
			typ: engine.NodeType{Name: "test.merge", Inputs: mainPorts("a", "b"), Outputs: mainPorts()},
			run: func(_ context.Context, _ engine.RunContext, in engine.Ports) (engine.Ports, error) {
				out := engine.Ports{}
				out.Add(engine.DefaultPort, in["a"]...)
				out.Add(engine.DefaultPort, in["b"]...)

				return out, nil
			},
		},
		{
			typ: engine.NodeType{Name: "test.error", Inputs: mainPorts(), Outputs: mainPorts()},
			run: func(context.Context, engine.RunContext, engine.Ports) (engine.Ports, error) {
				return nil, errBoom
			},
		},
		{
			typ: engine.NodeType{Name: "test.panic", Inputs: mainPorts(), Outputs: mainPorts()},
			run: func(context.Context, engine.RunContext, engine.Ports) (engine.Ports, error) {
				panic("kaboom")
			},
		},
		{
			// wait blocks until the execution is cancelled.
			typ: engine.NodeType{Name: "test.wait", Inputs: mainPorts(), Outputs: mainPorts()},
			run: func(ctx context.Context, _ engine.RunContext, _ engine.Ports) (engine.Ports, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
		},
	}

	for _, n := range nodes {
		require.NoError(t, r.RegisterNode(n))
	}

	return r
}

func spec(id domain.NodeID, typ string, params map[string]any) domain.NodeSpec {
	return domain.NodeSpec{ID: id, Type: typ, Params: params}
}

func link(from domain.NodeID, fromPort string, to domain.NodeID, toPort string) domain.Connection {
	return domain.Connection{From: from, FromPort: fromPort, To: to, ToPort: toPort}
}

func mainLink(from, to domain.NodeID) domain.Connection {
	return link(from, engine.DefaultPort, to, engine.DefaultPort)
}

// newMockedUtil hands out the given counters by "subsystem/name"; every other metric accepts any call.
func newMockedUtil(t *testing.T, counters map[string]*metricsmocks.MockCounter) *observability.Utility {
	t.Helper()

	manager := metricsmocks.NewMockManager(t)

	manager.EXPECT().RegisterCounter(mock.Anything).
		RunAndReturn(func(d metrics.CounterDefinition) (metrics.Counter, error) {
			if c, ok := counters[d.Subsystem+"/"+d.Name]; ok {
				return c, nil
			}

			c := metricsmocks.NewMockCounter(t)
			c.EXPECT().Inc(mock.Anything).Return(nil).Maybe()

			return c, nil
		}).Maybe()

	manager.EXPECT().RegisterGauge(mock.Anything).
		RunAndReturn(func(metrics.GaugeDefinition) (metrics.Gauge, error) {
			g := metricsmocks.NewMockGauge(t)
			g.EXPECT().Inc(mock.Anything).Return(nil).Maybe()
			g.EXPECT().Dec(mock.Anything).Return(nil).Maybe()

			return g, nil
		}).Maybe()

	manager.EXPECT().RegisterHistogram(mock.Anything).
		RunAndReturn(func(metrics.HistogramDefinition) (metrics.Histogram, error) {
			h := metricsmocks.NewMockHistogram(t)
			h.EXPECT().Observe(mock.Anything, mock.Anything).Return(nil).Maybe()

			return h, nil
		}).Maybe()

	return observability.NewUtility(logging.Noop(), manager, observability.NoopUtility().TracerManager)
}
