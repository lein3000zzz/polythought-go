package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lein3000zzz/polythought-go/pkg/plugin"
)

// host is the part of the host the plugin asks of, as a test stands in for it: its other
// methods, the embedded interface's, are never called.
type host struct {
	plugin.Host

	markets  []plugin.Market
	store    map[string]json.RawMessage
	fetched  []plugin.Request
	answer   plugin.Response
	messages []string
	noChat   bool
}

func newHost(markets ...plugin.Market) *host {
	return &host{markets: markets, store: map[string]json.RawMessage{}}
}

func (h *host) SearchMarkets(context.Context, string, bool, int) ([]plugin.Market, error) {
	return h.markets, nil
}

func (h *host) Get(_ context.Context, key string, v any) (bool, error) {
	raw, ok := h.store[key]
	if !ok {
		return false, nil
	}

	return true, json.Unmarshal(raw, v)
}

func (h *host) Set(_ context.Context, key string, v any) error {
	raw, err := json.Marshal(v)
	h.store[key] = raw

	return err
}

func (h *host) Fetch(_ context.Context, req plugin.Request) (plugin.Response, error) {
	h.fetched = append(h.fetched, req)
	return h.answer, nil
}

func (h *host) Credential(context.Context, string, plugin.Item) (plugin.Credential, error) {
	return plugin.Credential{Type: "coingecko", Values: map[string]string{"key": "CG-test"}}, nil
}

func (h *host) Message(_ context.Context, m plugin.Message) error {
	if h.noChat {
		return errors.New("the owner has not linked Telegram")
	}

	h.messages = append(h.messages, m.Text)

	return nil
}

func (h *host) Metric(context.Context, string, float64, map[string]string) error { return nil }

func market(id, price string) plugin.Market {
	return plugin.Market{
		ID: id, Slug: id, Question: "Will " + id + "?", Outcomes: []plugin.Outcome{{TokenID: "t-" + id, Label: "Yes", Price: plugin.Decimal(price)}},
	}
}

func run(h *host) plugin.Run {
	return plugin.Run{Host: h, Log: slog.New(slog.DiscardHandler), Settings: map[string]any{"move": json.Number("5"), "every": "5m"}}
}

// A market seen first is kept, not said; one that moves past the threshold since it was
// last said is, once, from what it was then.
func TestWatch(t *testing.T) {
	ctx := context.Background()
	h := newHost(market("rain", "0.40"), market("snow", "0.10"))
	w := watch{host: h, query: "weather", move: 0.05, markets: 10}

	movers, err := w.look(ctx)
	if err != nil || len(movers) != 0 {
		t.Fatalf("first look: %v, %v", movers, err)
	}

	h.markets = []plugin.Market{market("rain", "0.47"), market("snow", "0.12")}

	movers, err = w.look(ctx)
	if err != nil || len(movers) != 1 {
		t.Fatalf("second look: %v, %v", movers, err)
	}

	got := movers[0].JSON
	if got["market"] != "rain" || got["from"] != "0.40" || got["to"] != "0.47" || got["outcome"] != "Yes" {
		t.Errorf("rain moved as %v", got)
	}

	movers, _ = w.look(ctx)
	if len(movers) != 0 {
		t.Errorf("said again: %v", movers)
	}

	h.markets = []plugin.Market{market("rain", "0.47"), market("snow", "0.16")}

	movers, _ = w.look(ctx)
	if len(movers) != 1 || movers[0].JSON["market"] != "snow" || movers[0].JSON["from"] != "0.10" {
		t.Errorf("snow, from where it was last said, not at each look: %v", movers)
	}
}

// The trigger looks at once as it starts, and stops as its context ends.
func TestMovedStarts(t *testing.T) {
	h := newHost(market("rain", "0.40"))
	h.store["said:rain"] = json.RawMessage(`"0.30"`)

	ctx, cancel := context.WithCancel(context.Background())
	emitted := make(chan plugin.Ports, 1)

	done := make(chan error)

	go func() {
		done <- moved{}.Start(ctx, run(h), map[string]any{"query": "rain"}, false, func(_ context.Context, out plugin.Ports) error {
			emitted <- out
			return nil
		})
	}()

	select {
	case out := <-emitted:
		if len(out[plugin.MainPort]) != 1 {
			t.Errorf("emitted %v", out)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nothing emitted")
	}

	cancel()

	if err := <-done; err != nil {
		t.Errorf("stopped as %v", err)
	}
}

func item(params map[string]any) plugin.Item {
	return plugin.Item{
		JSON:   map[string]any{"question": "Will it rain?", "outcome": "Yes", "from": "0.40", "to": "0.47"},
		Params: params,
	}
}

func TestAlert(t *testing.T) {
	ctx := context.Background()

	t.Run("sent, how it moved", func(t *testing.T) {
		h := newHost()

		_, out, err := alert(ctx, run(h), item(map[string]any{}))
		if err != nil {
			t.Fatal(err)
		}

		want := "Will it rain? moved +7 points, from 40% to 47% (Yes)."
		if !slices.Equal(h.messages, []string{want}) || out["message"] != want || out["sent"] != true {
			t.Errorf("sent %q, out %v", h.messages, out)
		}
	})

	t.Run("a coin's price beside it, with the key picked", func(t *testing.T) {
		h := newHost()
		h.answer = plugin.Response{Status: http.StatusOK, Body: []byte(`{"bitcoin":{"usd":97120.5}}`)}

		_, out, err := alert(ctx, run(h), item(map[string]any{"coin": "bitcoin", "key": "cred-1"}))
		if err != nil {
			t.Fatal(err)
		}

		if !strings.HasSuffix(text(out["message"]), " Bitcoin is $97,120.50 on CoinGecko.") {
			t.Errorf("said %q", out["message"])
		}

		if len(h.fetched) != 1 || h.fetched[0].URL != "https://api.coingecko.com/api/v3/simple/price?vs_currencies=usd&ids=bitcoin" ||
			h.fetched[0].Header.Get("x-cg-demo-api-key") != "CG-test" {
			t.Errorf("fetched %+v", h.fetched)
		}
	})

	t.Run("without the price when CoinGecko does not answer", func(t *testing.T) {
		h := newHost()
		h.answer = plugin.Response{Status: http.StatusTooManyRequests}

		_, out, err := alert(ctx, run(h), item(map[string]any{"coin": "bitcoin"}))
		if err != nil || strings.Contains(text(out["message"]), "CoinGecko") {
			t.Errorf("said %q, %v", out["message"], err)
		}
	})

	t.Run("previewed, not sent", func(t *testing.T) {
		h := newHost()

		_, out, err := alert(ctx, run(h), item(map[string]any{"send": false}))
		if err != nil || len(h.messages) != 0 || out["sent"] != false {
			t.Errorf("sent %q, out %v, %v", h.messages, out, err)
		}
	})

	t.Run("failed, saying why, where it cannot be sent", func(t *testing.T) {
		h := newHost()
		h.noChat = true

		if _, _, err := alert(ctx, run(h), item(map[string]any{})); err == nil || !strings.Contains(err.Error(), "Telegram") {
			t.Errorf("failed as %v", err)
		}
	})
}

func TestDollars(t *testing.T) {
	for n, want := range map[float64]string{0.5: "0.50", 999: "999.00", 1234.5: "1,234.50", 97120.5: "97,120.50", 1234567: "1,234,567.00"} {
		if got := dollars(n); got != want {
			t.Errorf("%v is %s, not %s", n, got, want)
		}
	}
}
