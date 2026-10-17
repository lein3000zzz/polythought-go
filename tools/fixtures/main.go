// Command fixtures refreshes the Polymarket client test fixtures from the live APIs.
// Responses are trimmed but otherwise stored exactly as received. Run with `make fixtures`.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

const (
	gammaURL = "https://gamma-api.polymarket.com"
	clobURL  = "https://clob.polymarket.com"
	dataURL  = "https://data-api.polymarket.com"
)

type object = map[string]any

var client = &http.Client{Timeout: 30 * time.Second}

func main() {
	dir := flag.String("dir", "pkg/clients/polymarket/testdata", "fixture directory")
	pages := flag.Int("pages", 20, "Gamma pages to scan for a v2 market")
	flag.Parse()

	if err := run(*dir, *pages); err != nil {
		log.Fatal(err)
	}
}

func run(dir string, pages int) error {
	first, v1, v2, err := scanMarkets(pages)
	if err != nil {
		return err
	}

	if len(v1) < 2 {
		return errors.New("fewer than 2 open v1 order-book markets on the first page")
	}

	if err := write(dir, "gamma_markets_keyset.json", object{"markets": v1[:2], "next_cursor": first["next_cursor"]}); err != nil {
		return err
	}

	if v2 != nil {
		if err := write(dir, "gamma_market_v2.json", v2); err != nil {
			return err
		}
	} else {
		log.Printf("no v2 market in %d pages, keeping the existing v2 fixture if any", pages)
	}

	if err := writeEvents(dir); err != nil {
		return err
	}

	if err := writeBooks(dir, first); err != nil {
		return err
	}

	conditionID, _ := v1[0]["conditionId"].(string)

	return writeData(dir, conditionID)
}

// scanMarkets returns the first page, the first page's v1 order-book markets and the first v2 market seen.
func scanMarkets(pages int) (first object, v1 []object, v2 object, err error) {
	cursor := ""

	for i := range pages {
		query := url.Values{"limit": {"100"}, "closed": {"false"}}
		if cursor != "" {
			query.Set("after_cursor", cursor)
		}

		var page object
		if err := getJSON(gammaURL+"/markets/keyset?"+query.Encode(), &page); err != nil {
			return nil, nil, nil, err
		}

		if i == 0 {
			first = page
		}

		markets, _ := page["markets"].([]any)
		for _, raw := range markets {
			m, _ := raw.(object)

			switch {
			case m["version"] == "v2" && v2 == nil:
				v2 = m
			case i == 0 && m["version"] == "v1" && m["enableOrderBook"] == true && m["clobTokenIds"] != nil:
				v1 = append(v1, m)
			}
		}

		cursor, _ = page["next_cursor"].(string)
		if cursor == "" || v2 != nil {
			break
		}
	}

	return first, v1, v2, nil
}

func writeEvents(dir string) error {
	var page object
	if err := getJSON(gammaURL+"/events/keyset?limit=1", &page); err != nil {
		return err
	}

	events, _ := page["events"].([]any)
	for _, raw := range events {
		e, _ := raw.(object)
		e["markets"] = head(e["markets"], 2)
	}

	return write(dir, "gamma_events_keyset.json", page)
}

// writeBooks keeps one traded book and one never-traded book (last_trade_price "").
func writeBooks(dir string, first object) error {
	var tokenReqs []any

	markets, _ := first["markets"].([]any)
	for _, raw := range markets {
		m, _ := raw.(object)
		if m["enableOrderBook"] != true {
			continue
		}

		encoded, _ := m["clobTokenIds"].(string)

		var ids []string
		if err := json.Unmarshal([]byte(encoded), &ids); err != nil {
			continue
		}

		for _, id := range ids {
			tokenReqs = append(tokenReqs, object{"token_id": id})
		}
	}

	var books []object
	if err := postJSON(clobURL+"/books", head(tokenReqs, 100), &books); err != nil {
		return err
	}

	var traded, fresh object
	for _, b := range books {
		bids, _ := b["bids"].([]any)
		asks, _ := b["asks"].([]any)

		switch {
		case b["last_trade_price"] == "" && fresh == nil:
			fresh = b
		case b["last_trade_price"] != "" && len(bids) > 0 && len(asks) > 0 && traded == nil:
			traded = b
		}
	}

	if traded == nil || fresh == nil {
		return errors.New("could not find both a traded and a never-traded book")
	}

	for _, b := range []object{traded, fresh} {
		b["bids"] = tail(b["bids"], 3)
		b["asks"] = tail(b["asks"], 3)
	}

	return write(dir, "clob_books.json", []object{traded, fresh})
}

func writeData(dir, conditionID string) error {
	var info object
	if err := getJSON(clobURL+"/clob-markets/"+url.PathEscape(conditionID), &info); err != nil {
		return err
	}

	if err := write(dir, "clob_market_info.json", info); err != nil {
		return err
	}

	var trades object
	if err := getJSON(dataURL+"/v2/trades?limit=2&condition="+conditionID, &trades); err != nil {
		return err
	}

	if err := write(dir, "data_trades.json", trades); err != nil {
		return err
	}

	var holders struct {
		Data []struct {
			Holders []struct {
				ProxyWallet string `json:"proxy_wallet"`
			} `json:"holders"`
		} `json:"data"`
	}
	if err := getJSON(dataURL+"/v2/holders?limit=1&condition="+conditionID, &holders); err != nil {
		return err
	}

	if len(holders.Data) == 0 || len(holders.Data[0].Holders) == 0 {
		return errors.New("market has no holders to take positions from")
	}

	var positions object
	if err := getJSON(dataURL+"/v2/positions?limit=2&user="+holders.Data[0].Holders[0].ProxyWallet, &positions); err != nil {
		return err
	}

	return write(dir, "data_positions.json", positions)
}

func getJSON(target string, out any) error {
	return do(http.MethodGet, target, nil, out)
}

func postJSON(target string, body, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}

	return do(http.MethodPost, target, payload, out)
}

func do(method, target string, payload []byte, out any) error {
	req, err := http.NewRequest(method, target, bytes.NewReader(payload))
	if err != nil {
		return err
	}

	req.Header.Set("User-Agent", "polythought-go-fixtures")
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s %s: http %d: %s", method, target, resp.StatusCode, body)
	}

	// UseNumber keeps numbers exactly as sent, so fixtures stay byte-faithful.
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()

	return dec.Decode(out)
}

func write(dir, name string, v any) error {
	b, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return err
	}

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return err
	}

	log.Printf("wrote %s", path)

	return nil
}

func head(v any, n int) any {
	s, _ := v.([]any)

	return s[:min(n, len(s))]
}

func tail(v any, n int) any {
	s, _ := v.([]any)

	return s[max(0, len(s)-n):]
}
