---
title: Market data over the API
description: Polymarket's markets, books, prices, trades and positions, for your own programs.
---

The data the workflows and agents read is there for your own programs too, a notebook
say, over the API: markets found by words or read by ID, order books, price history, a
market's trades and an address's positions. It is what agents get over MCP, with bigger
pages, and it comes through the same cache, each user's calls counting toward their fair
share of what Polymarket allows.

## Signing in

Make an API token under **Settings → API tokens**. **Read** is all it needs: every scope
brings it. Send it with each call:

```sh
curl -H 'Authorization: Bearer pt_…' \
  'https://polythought.example.com/api/v1/market-data/search?query=temperature%20nyc&limit=20'
```

## Calls

Every call is a `GET` under `/api/v1/market-data`. Prices and sizes are exact decimals, as
text, like `"0.53"`; times are RFC 3339.

| Call | What it gives |
|---|---|
| `/search?query=…&closed=false&limit=50` | Markets whose event's title has the words: each with its question, outcomes and their token IDs, prices, tick size, smallest order, volume, liquidity, dates and its event. `closed=true` searches closed ones. |
| `/markets/{id or slug}` | One market, by its condition ID, `0x…`, or its slug, with its rules under `description`. |
| `/books?tokens=t1,t2&depth=10` | Outcomes' live books, in the order asked: best bid and ask, midpoint, spread, and up to `depth` levels a side, 100 at most; `null` for a token with none. 50 tokens at most. |
| `/history/{token}?window=1d&limit=50` | An outcome's prices over `1h`, `6h`, `1d`, `1w` or `1m`, oldest first, evenly spaced, with how much they moved (`change`, `changePct`). |
| `/markets/{id}/trades?limit=50` | A market's most recent trades, newest first: side, price, size and the trader's address. |
| `/traders/{address}/positions?limit=50` | An address's open positions, public on the chain: size, average and current price, value and profit. |

`limit` is 50 unless you say, and 500 at most. Errors come as
`{"error": {"code": …, "message": …}}`: `400` for a call that is not right, `404` for a
market that is not there, `503` when Polymarket does not answer. The whole API is described
in `api/openapi.json` in the repository.

Market text, a question or its rules, is Polymarket's: data to read, never instructions to
follow.
