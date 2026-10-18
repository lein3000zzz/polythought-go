---
title: Copy traders
description: Follow Polymarket traders and copy their trades, on paper, as they make them.
---

A copytrading workflow is a few nodes in a row: **Wallet activity** follows the traders,
**Copy sizing** works out a copy of each of their trades, **Risk guard** holds the copies
to your limits, and **Place order** places them. Orders are on paper for now: they fill
against the live order book, out of a paper account of the workflow's own, and spend no
real money.

## Follow the traders

The [Wallet activity](/docs/reference/nodes/triggers/wallet-activity/) trigger follows
traders on Polymarket. Each trade one of them makes starts the workflow, about two seconds
after it, with the trade as its item.

1. Place **Wallet activity**, from the **Triggers** group of the node list.
2. Under **Traders**, press **Edit traders**, then **Add a row** for each trader:
   - **Label**: who they are to you, like `Alice`.
   - **Address**: their wallet, the `0x…` address in the link of their profile,
     `polymarket.com/profile/0x…`. Saving checks it, a mistyped letter included.
   - **On**: switch a trader off to stop following them and keep their row.
   - **Size ×** scales the copies of their buys, `0.5` for half; **Max $** is the most a
     copy of one of their buys spends. Empty, they leave the copy as Copy sizing works it
     out.
3. Save, and switch the workflow **Active**.

### What a trade is

A trade is one trader's fills in one transaction, outcome and side, added up: the shares,
the dollars, and the average price. An order that filled against many others is one trade,
and starts the workflow once.

Both kinds of trade count: orders they place against the book, whose `role` is `taker`,
and their resting orders that fill, `maker`. The item also has the market, the outcome
and the transaction; see the [list of fields](/docs/reference/nodes/triggers/wallet-activity/#items).

**Sides** and **At least $** choose which trades go on: only buys, say, or only trades of
$100 or more. For anything finer, put an [If](/docs/reference/nodes/logic/if/) after it,
like `role` is `taker`.

### When some are missed

The trades come from Polymarket's live feed of activity. When the feed drops, or the server
restarts, Wallet activity looks up what it missed and starts the workflow for the trades
younger than **Catch up within**, 2 minutes by default and up to an hour. Older ones are
skipped: a trade is never passed on late. Those found this way have no `role`, as the
lookup does not say.

Switching a workflow on follows from then: trades from before are not caught up. Saving an
active workflow catches up on what came in while it restarted.

The lookup also runs every 30 seconds, and every 5 seconds while the feed is down, so a
trade the feed lost still comes through.

## Size the copies

[Copy sizing](/docs/reference/nodes/trading/copy-size/) turns each trade into the order
that copies it, as `copy` on the item.

**Buys** are sized by **Buy size**:

| Buy size | A copy spends |
|---|---|
| `fixed` | **Dollars**, $10 by default |
| `multiple` | **Multiple** times what they spent: `0.1` is a tenth |
| `proportional` | the share of their portfolio they spent, of what the workflow's paper account is worth |

Their portfolio is what their open positions are worth, as Polymarket's data says; the
paper account is worth its cash and what its holdings cost. Then the trader's **Size ×**
scales the copy and their **Max $** caps it.

**Sells** sell the same share: when they sell 40% of their shares in an outcome, the copy
sells 40% of what it holds copying them, and when they sell out, all of it. What is bought
copying one trader is theirs: a sell copied from Alice sells what was bought copying Alice,
never Bob's copies of the same outcome.

**The price** is theirs, worse by at most **Slippage**, 2 cents by default, on the market's
tick: a copy of a buy at 0.503 is placed at up to 0.52. It fills what the book has within
that price at once, and the rest is cancelled.

A trade it cannot copy goes out of `skipped`, with why in `skipped.reason`: nothing held
copying them to sell, or a copy under the market's least order, which is skipped rather than
made bigger than its sizing says.

## Guard them

[Risk guard](/docs/reference/nodes/trading/risk-guard/), between Copy sizing and Place
order, holds copies of buys to the limits you set; a limit left empty does not apply.

| Setting | Holds a copy to |
|---|---|
| **Max $ a copy** | the most one copy spends |
| **Max $ a market** | the most the workflow holds in one market, at cost, all traders' copies together: selling makes room again |
| **Max $ a day** | the most its buys spend in any 24 hours |
| **Least price**, **Most price** | buys at prices within them |
| **Only categories**, **Not categories** | markets with one of these tags, or none of them, like `Politics` or `Sports` |

A copy over a cap goes ahead smaller, as much as the cap leaves: `guard` on the item says
from what and why. A copy a limit stops, or one a cap would leave under the market's least
order, goes out of `blocked`, with why in `blocked.reason`. Copies in one run count against
the ones after them.

Sells always go ahead: they lower what is at risk, so a copy is never stuck in a position
the guard let it into.

## Place them

[Place order](/docs/reference/nodes/trading/place-order/) places each item's copy. An order
that filled, wholly or in part, goes out of `main`; one that did not, out of `missed`. Both
carry `order`: its status (`filled`, `partial`, `unfilled` or `refused`), the shares,
dollars and fees it came to, the average price, and why when it did not fill.

Set **Order** to `settings` and Place order places an order of its own instead, from its
settings, which take [expressions](/docs/guides/expressions/): a **Token**, a **Side**, a
**Price** that is the limit, **Shares** or for a buy **Dollars**, and FAK or FOK. After
[Markets](/docs/reference/nodes/polymarket/markets/), say, `{{ outcomes[0].tokenId }}` as
the token buys the first outcome of each market.

## Paper trading

Each workflow trading on paper has a paper account, opened by its first order with $1000;
an admin can change **Paper balance** on the Config page. A paper order fills at once
against the live order book, as Polymarket would fill it: the best price first, within its
limit, a FAK as far as the book goes, a FOK wholly or not at all. Each fill pays the
market's taker fee, as Polymarket charges it: on top of a buy, out of a sell.

As on Polymarket, an order must keep to its market: a price on the tick, at least its least
order size. A buy needs the cash for all it may spend, a sell the shares. An order that
does not is refused, saying why, and is kept with the others.

When a market resolves, what the workflow holds in it settles on its own within a few
minutes, at what a share pays: $1 for the outcome that won, nothing for the others. The
cash goes to the paper account, and the order list shows it as settled.

## See how it does

A workflow with trading nodes has a third view beside **Canvas** and **Code**: **Trading**.
It shows, refreshed every half minute:

- **The paper account**: what it is worth, its cash and what it holds now, its cash alone,
  and its profit or loss since it started.
- **Traders**: a row per trader copied, and one for orders copied from no one: how many
  copies filled, the dollars they traded, the profit or loss made on what was sold
  (realized) and what what is held would make sold now (unrealized), and the win rate, the
  closed copies, sold out of or settled, that made money.
- **Holdings**: what is held, per trader and outcome, with what it cost and what it is
  worth now, at the midpoint of the order book; when the book has only one side, at the
  market's own price.
- **Orders**: every order, the newest first, with how it went.

**Reset**, after asking, starts the paper account over: its cash goes back to the Paper
balance and its holdings are emptied. The orders stay in the list, under the reset, and
the results count from it.

The same is in the API: `GET /api/v1/workflows/{id}/trading`, `/orders`, `/holdings` and
`/paper-account`, and `POST /api/v1/workflows/{id}/paper-account/reset`.
