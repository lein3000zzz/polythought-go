package main

import (
	"context"
	"flag"
	"time"

	pmadapter "github.com/lein3000zzz/polythought-go/internal/adapters/polymarket"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/logic"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/markets"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/output"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/triggers"
	pm "github.com/lein3000zzz/polythought-go/pkg/clients/polymarket"
)

// demo runs the spread-watch workflow against live Polymarket. It needs no database.
func demo(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("demo", flag.ContinueOnError)
	interval := fs.Duration("interval", 30*time.Second, "how often the demo workflow runs")
	once := fs.Bool("once", false, "run the demo workflow once from its manual trigger and exit")

	if err := fs.Parse(args); err != nil {
		return err
	}

	util, err := newUtility("info", "text")
	if err != nil {
		return err
	}
	defer shutdown(util)

	gamma, err := pm.NewGammaClient(util, pm.HTTPConfig{})
	if err != nil {
		return err
	}

	clob, err := pm.NewClobClient(util, pm.HTTPConfig{})
	if err != nil {
		return err
	}

	data, err := pm.NewDataClient(util, pm.HTTPConfig{})
	if err != nil {
		return err
	}

	reader := pmadapter.NewReader(gamma, clob, data)

	registry := engine.NewRegistry()
	// The demo has no database, so no wallets: account nodes are registered but unused.
	if err := nodes.Register(registry, nodes.Deps{Markets: reader, OrderBooks: reader, Positions: reader, Logger: util.Logger}); err != nil {
		return err
	}

	executor, err := engine.NewExecutor(util, registry)
	if err != nil {
		return err
	}

	wf := demoWorkflow(*interval)

	if *once {
		_, err := executor.Execute(ctx, wf, "manual", triggers.ManualOutput())
		return err
	}

	return engine.NewRunner(util, registry, executor).Run(ctx, wf)
}

// demoWorkflow fetches open markets, summarises their order books and logs tight and
// wide spreads separately. It has a schedule for live runs and a manual trigger for -once.
func demoWorkflow(interval time.Duration) domain.Workflow {
	main := engine.DefaultPort

	return domain.Workflow{
		ID:     "demo",
		Name:   "Spread watch",
		Active: true,
		Nodes: []domain.NodeSpec{
			{ID: "every", Type: triggers.ScheduleType, Params: map[string]any{"interval": interval.String()}},
			{ID: "manual", Type: triggers.ManualType},
			{ID: "markets", Type: markets.MarketsType, Params: map[string]any{"limit": 5}},
			{ID: "books", Type: markets.OrderBooksType, Params: map[string]any{"depth": 5}},
			{ID: "tight", Type: logic.IfType, Params: map[string]any{"field": "spread", "operator": "lte", "value": "0.02"}},
			{ID: "logTight", Type: output.LogType, Params: map[string]any{"message": "Tight spread"}},
			{ID: "logWide", Type: output.LogType, Params: map[string]any{"message": "Wide spread"}},
		},
		Connections: []domain.Connection{
			{From: "every", FromPort: main, To: "markets", ToPort: main},
			{From: "manual", FromPort: main, To: "markets", ToPort: main},
			{From: "markets", FromPort: main, To: "books", ToPort: main},
			{From: "books", FromPort: main, To: "tight", ToPort: main},
			{From: "tight", FromPort: logic.PortTrue, To: "logTight", ToPort: main},
			{From: "tight", FromPort: logic.PortFalse, To: "logWide", ToPort: main},
		},
	}
}
