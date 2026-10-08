package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lein3000zzz/polythought-go/internal/core/nodes"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/logic"
	markets2 "github.com/lein3000zzz/polythought-go/internal/core/nodes/markets"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/output"
	"github.com/lein3000zzz/polythought-go/internal/core/nodes/triggers"
	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging/slogging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics/prommetrics"
	"github.com/lein3000zzz/the-watchers/pkg/observability/tracing/oteltracing"

	pmadapter "github.com/lein3000zzz/polythought-go/internal/adapters/polymarket"
	"github.com/lein3000zzz/polythought-go/internal/core/domain"
	"github.com/lein3000zzz/polythought-go/internal/core/engine"
	pm "github.com/lein3000zzz/polythought-go/pkg/clients/polymarket"
)

func main() {
	interval := flag.Duration("interval", 30*time.Second, "how often the demo workflow runs")
	once := flag.Bool("once", false, "run the demo workflow once from its manual trigger and exit")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log, err := slogging.NewSlogLoggerWithConfig(slogging.Config{
		Level:    logging.LevelInfo,
		Format:   logging.FormatText,
		ToStdout: true,
	}, oteltracing.CorrelateCore)
	if err != nil {
		panic(err)
	}

	util := observability.NewUtility(
		log,
		prommetrics.DefaultManager(),
		oteltracing.DefaultOtelManager(),
		func(context.Context) error { return log.Close() },
	)

	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_ = util.Shutdown(shutdownCtx)
	}()

	if err := run(ctx, util, demoWorkflow(*interval), *once); err != nil {
		log.Error(ctx, "Run failed", logging.Err(err))
	}
}

func run(ctx context.Context, util *observability.Utility, wf domain.Workflow, once bool) error {
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
	if err := nodes.Register(registry, nodes.Deps{Markets: reader, OrderBooks: reader, Logger: util.Logger}); err != nil {
		return err
	}

	executor, err := engine.NewExecutor(util, registry)
	if err != nil {
		return err
	}

	if once {
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
			{ID: "markets", Type: markets2.MarketsType, Params: map[string]any{"limit": 5}},
			{ID: "books", Type: markets2.OrderBooksType, Params: map[string]any{"depth": 5}},
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
