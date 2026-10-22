package main

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/lein3000zzz/polythought-go/pkg/plugin"
)

// coinGecko is the outside API the plugin fetches from, as its permission names it.
const coinGecko = "api.coingecko.com"

// alert is the Alert me node, for each market that comes in: it writes how the market
// moved, a coin's price beside it when the node names one, and sends it to the workflow's
// owner on Telegram, unless the node only previews it.
func alert(ctx context.Context, run plugin.Run, item plugin.Item) (string, map[string]any, error) {
	from, to := text(item.JSON["from"]), text(item.JSON["to"])

	msg := fmt.Sprintf("%s moved %s points, from %s to %s (%s).",
		or(text(item.JSON["question"]), "A market"), points(from, to), percent(from), percent(to),
		or(text(item.JSON["outcome"]), "its first outcome"))

	if coin := text(item.Params["coin"]); coin != "" {
		// Without the price, the alert still says what matters.
		if price, err := coinPrice(ctx, run.Host, coin, item); err != nil {
			run.Log.Warn("Coin's price not had", "coin", coin, "error", err)
		} else {
			msg += fmt.Sprintf(" %s is $%s on CoinGecko.", titled(coin), dollars(price))
		}
	}

	send := item.Params["send"] != false

	if send {
		if err := run.Host.Message(ctx, plugin.Message{Text: msg}); err != nil {
			return "", nil, err // the item fails, saying why: Telegram not linked, say
		}

		_ = run.Host.Metric(ctx, "alerts_total", 1, nil)
	}

	out := maps.Clone(item.JSON)
	out["message"], out["sent"] = msg, send

	return "", out, nil
}

// coinPrice is a coin's price in dollars, as CoinGecko answers, with the key the node's
// setting picks when it picks one.
func coinPrice(ctx context.Context, host plugin.Host, coin string, item plugin.Item) (float64, error) {
	header := http.Header{"Accept": {"application/json"}}

	if item.Params["key"] != nil {
		c, err := host.Credential(ctx, "key", item)
		if err != nil {
			return 0, err
		}

		header.Set("x-cg-demo-api-key", c.Values["key"])
	}

	res, err := host.Fetch(ctx, plugin.Request{
		URL:    "https://" + coinGecko + "/api/v3/simple/price?vs_currencies=usd&ids=" + url.QueryEscape(coin),
		Header: header,
	})
	if err != nil {
		return 0, err
	}

	if res.Status != http.StatusOK {
		return 0, fmt.Errorf("CoinGecko answered %d", res.Status)
	}

	var prices map[string]struct {
		USD *float64 `json:"usd"`
	}

	if err := json.Unmarshal(res.Body, &prices); err != nil {
		return 0, fmt.Errorf("CoinGecko's answer: %w", err)
	}

	p, ok := prices[coin]
	if !ok || p.USD == nil {
		return 0, fmt.Errorf("CoinGecko knows no coin %q", coin)
	}

	return *p.USD, nil
}

// percent is a price as people read it: 0.47 is 47%.
func percent(price string) string {
	p, err := strconv.ParseFloat(price, 64)
	if err != nil {
		return price
	}

	return fmt.Sprintf("%.0f%%", p*100)
}

// dollars is an amount with its thousands apart: 97120.5 is 97,120.50.
func dollars(n float64) string {
	whole, cents, _ := strings.Cut(strconv.FormatFloat(n, 'f', 2, 64), ".")

	var b strings.Builder

	for i, d := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}

		b.WriteRune(d)
	}

	return b.String() + "." + cents
}

func titled(s string) string {
	if s == "" {
		return s
	}

	return strings.ToUpper(s[:1]) + s[1:]
}

func or(s, otherwise string) string {
	if s == "" {
		return otherwise
	}

	return s
}
