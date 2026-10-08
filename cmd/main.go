package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/lein3000zzz/the-watchers/pkg/observability"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/logging/slogging"
	"github.com/lein3000zzz/the-watchers/pkg/observability/metrics/prommetrics"
	"github.com/lein3000zzz/the-watchers/pkg/observability/tracing/oteltracing"

	"github.com/lein3000zzz/polythought-go/pkg/clients/polymarket"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
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

	if err := run(ctx, util); err != nil {
		log.Error(ctx, "Run failed", logging.Err(err))
	}
}

func run(ctx context.Context, util *observability.Utility) error {
	gamma, err := polymarket.NewGammaClient(util, polymarket.HTTPConfig{})
	if err != nil {
		return err
	}

	closed := false
	page, err := gamma.ListMarkets(ctx, polymarket.MarketsQuery{Limit: 10, Closed: &closed})
	if err != nil {
		return err
	}

	fmt.Println("Open markets:")
	printMarkets(page.Items)

	if len(page.Items) == 0 {
		return nil
	}

	market, err := gamma.GetMarketBySlug(ctx, page.Items[0].Slug)
	if err != nil {
		return err
	}

	fmt.Printf("\nMarket %q\n  condition: %s\n  version:   %s\n  tokens:    %s\n",
		market.Question, market.ConditionID, market.Version, strings.Join(market.TokenIDs(), "\n             "))

	clob, err := polymarket.NewClobClient(util, polymarket.HTTPConfig{})
	if err != nil {
		return err
	}

	tokenIDs := market.TokenIDs()

	mids, err := clob.GetMidpoints(ctx, tokenIDs...)
	if err != nil {
		return err
	}

	books, err := clob.GetOrderBooks(ctx, tokenIDs...)
	if err != nil {
		return err
	}

	for i, id := range tokenIDs {
		outcome := id
		if i < len(market.Outcomes) {
			outcome = market.Outcomes[i]
		}

		fmt.Printf("\n%s  mid %s\n", outcome, mids[id])

		for _, book := range books {
			if book.AssetID == id {
				printBook(book, 5)
			}
		}
	}

	data, err := polymarket.NewDataClient(util, polymarket.HTTPConfig{})
	if err != nil {
		return err
	}

	return printActivity(ctx, data, market.ConditionID)
}

func printActivity(ctx context.Context, data polymarket.DataClient, conditionID string) error {
	oi, err := data.OpenInterest(ctx, conditionID)
	if err != nil {
		return err
	}

	for _, row := range oi {
		fmt.Printf("\nOpen interest: %s USD\n", row.Value)
	}

	trades, err := data.Trades(ctx, polymarket.TradesQuery{Conditions: []string{conditionID}, Limit: 5})
	if err != nil {
		return err
	}

	fmt.Println("\nLatest trades:")

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "TIME\tSIDE\tOUTCOME\tPRICE\tSIZE")

	for _, t := range trades.Items {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			time.Unix(t.Timestamp, 0).Format(time.TimeOnly), t.Side, t.Outcome, t.Price, t.Size)
	}

	_ = w.Flush()

	holders, err := data.Holders(ctx, polymarket.HoldersQuery{Conditions: []string{conditionID}, Limit: 3})
	if err != nil {
		return err
	}

	fmt.Println("\nTop holders:")

	w = tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "OUTCOME\tNAME\tWALLET\tSHARES")

	for _, token := range holders {
		for _, h := range token.Holders {
			name := h.Name
			if name == "" {
				name = h.Pseudonym
			}

			_, _ = fmt.Fprintf(w, "%d\t%s\t%s\t%s\n", h.OutcomeIndex, name, h.ProxyWallet, h.Amount)
		}
	}

	return w.Flush()
}

// printBook prints the best levels first. The API sorts each side worst to best.
func printBook(book polymarket.OrderBook, depth int) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
	defer func() { _ = w.Flush() }()

	_, _ = fmt.Fprintln(w, "BID SIZE\tBID\tASK\tASK SIZE\t")

	for i := range depth {
		var bid, ask polymarket.PriceLevel
		if i < len(book.Bids) {
			bid = book.Bids[len(book.Bids)-1-i]
		}

		if i < len(book.Asks) {
			ask = book.Asks[len(book.Asks)-1-i]
		}

		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t\n", bid.Size, bid.Price, ask.Price, ask.Size)
	}
}

func printMarkets(markets []polymarket.Market) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	defer func() { _ = w.Flush() }()

	_, _ = fmt.Fprintln(w, "QUESTION\tOUTCOMES\tBID\tASK\tVOL 24H")

	for _, m := range markets {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			truncate(m.Question, 60), formatOutcomes(m), m.BestBid, m.BestAsk, m.Volume24hr)
	}
}

func formatOutcomes(m polymarket.Market) string {
	parts := make([]string, 0, len(m.Outcomes))
	for i, outcome := range m.Outcomes {
		price := "?"
		if i < len(m.OutcomePrices) {
			price = m.OutcomePrices[i].String()
		}

		parts = append(parts, outcome+" "+price)
	}

	return strings.Join(parts, " / ")
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}

	return string(r[:n-1]) + "…"
}
