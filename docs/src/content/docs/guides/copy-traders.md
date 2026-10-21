---
title: Copy traders
description: Follow Polymarket traders and copy their trades, on paper or live, as they make them.
---

A copytrading workflow is a few nodes in a row: **Wallet activity** follows the traders,
**Copy sizing** works out a copy of each of their trades, **Risk guard** holds the copies
to your limits, and **Place order** places them. A workflow trades on paper until you
switch it to live: on paper its orders fill against the live order book, out of a paper
account of its own, and spend no real money; live, they go to Polymarket from one of your
wallets and spend its pUSD.

The [library](/docs/guides/library/) has it ready to use, three ways. **Copy traders**
copies $10 of each buy, at most $50 in a market and $200 a day, and asks only for the
traders to follow. **Copy traders with approval** does the same, but first asks you on
[Telegram](/docs/guides/telegram/), with **Copy** and **Skip**, and tells you how each copy
you approved went. Answer within 2 minutes; after that the copy is dropped, as the price
may have moved. **Copy when traders agree** copies a buy only once 2 of your traders bought
the same outcome within 6 hours.

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
   - **Take %** copies only a share of their buys: `30` copies about 3 in 10. Each trade
     is decided by its transaction, so a re-run decides it the same way. Their sells are
     always copied, so a copy never outstays their position.
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
skipped: a trade is never passed on late, nor twice, a restart included. Those found this
way have no `role`, as the lookup does not say.

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
| `proportional` | the share of their portfolio they spent, of what the workflow is worth |

Their portfolio is what their open positions are worth, as Polymarket's data says; the
workflow is worth its cash and what its holdings cost: on paper, the paper account's cash,
live, its wallet's pUSD. Then the trader's **Size ×** scales the copy and their **Max $**
caps it.

**Sells** sell the same share: when they sell 40% of their shares in an outcome, the copy
sells 40% of what it holds copying them, and when they sell out, all of it. What is bought
copying one trader is theirs: a sell copied from Alice sells what was bought copying Alice,
never Bob's copies of the same outcome.

**The price** is theirs, worse by at most **Slippage**, 2 cents by default, on the market's
tick: a copy of a buy at 0.503 is placed at up to 0.52. It fills what the book has within
that price at once, and the rest is cancelled.

A trade it cannot copy goes out of `skipped`, with why in `skipped.reason`: nothing held
copying them to sell; a copy under the market's least order, which is skipped rather than
made bigger than its sizing says; or a buy their **Take %** leaves out.

To copy only what several of your traders buy, put a
[Consensus](/docs/reference/nodes/logic/consensus/) before Copy sizing, for buys only: an
If on `side` `is` `sell` sends sells straight on, so a copy is sold when its trader sells.
The library's **Copy when traders agree** is built so.

To copy at most so many of a trader's trades, put a
[Throttle](/docs/reference/nodes/logic/throttle/) after Copy sizing: **At most** `3` **In**
`24h`, **Count per** `trader.address`. Copies over it leave from `limited`. It remembers
across restarts, so a restart does not make room.

## Guard them

[Risk guard](/docs/reference/nodes/trading/risk-guard/), between Copy sizing and Place
order, holds copies of buys to the limits you set; a limit left empty does not apply.

| Setting | Holds a copy to |
|---|---|
| **Max $ a copy** | the most one copy spends |
| **Max $ a market** | the most the workflow holds in one market, at cost, all traders' copies together, with what its resting buys there may still spend: selling makes room again |
| **Max $ a day** | the most its buys spend in any 24 hours, with what its resting buys may still spend |
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
that filled, wholly or in part, goes out of `main`; one left resting on the book, out of
`resting` (below); one that did not fill, out of `missed`. Each carries `order`: its mode,
its status (`filled`, `partial`, `open`, `unfilled` or `refused`, and live also `pending`
or `failed`, below), the shares, dollars and fees it came to, the average price, and why
when it did not fill.

Set **Order** to `settings` and Place order places an order of its own instead, from its
settings, which take [expressions](/docs/guides/expressions/): a **Token**, a **Side**, a
**Price** that is the limit, **Shares** or for a buy **Dollars**, and a **Type**. After
[Markets](/docs/reference/nodes/polymarket/markets/), say, `{{ outcomes[0].tokenId }}` as
the token buys the first outcome of each market.

### Several at once

[Place orders](/docs/reference/nodes/trading/place-orders/) places a list of orders for
each item, together: live, they reach Polymarket in one request, up to 15 at a time, so
they reach the book at the same prices and go easy on Polymarket's limits. The list is its
**Orders** table, a row per order typed in, or the whole table an expression giving a
list, like `{{ outcomes.map(o => ({ tokenId: o.tokenId, side: 'buy', price: 0.4, shares:
5, type: 'GTC' })) }}` to buy every outcome of a market. Each order is checked on its own,
the caps counting those before it: a refused one goes out of `missed` with why, and the
others go all the same. Polymarket's batch is not all or nothing, and neither is this. An
item comes out per order, as from Place order, with `order.index`, its place in the list.
On paper, the orders are placed one after another.

### Resting orders

A FAK takes what the book has within its price and cancels the rest; a FOK fills wholly or
not at all. A **GTC** takes what it can at once too, and then **rests** on the book at its
price, waiting for others to trade with it, until it fills or you cancel it; a **GTD** the
same, until **Expires after**, 3 minutes at the least. With **Post-only**, a GTC or GTD
that would match at once is refused instead, so it only ever rests. A buy of dollars rests
as the shares those dollars buy at its price.

A copy can rest too: **Copy as** `GTC` or `GTD` places the copy at its price instead of as
the FAK Copy sizing worked out.

A resting order goes out of `resting`, with what matched at once. What fills later is in
the books and the order list; each resting order has **Cancel** there, which cancels what
is left of it and keeps what it filled.

While it rests, a buy holds back the cash it may spend, and a sell its shares: other orders
cannot spend them. When its market resolves, what is left of it is cancelled.

### Cancel orders

[Cancel orders](/docs/reference/nodes/trading/cancel-orders/) cancels the workflow's own
resting orders for each item, paper and live: with **Cancel** `order`, the item's order, as
Place order or Order activity put it on the item; `all`, every one of the workflow's;
`market` or `outcome`, those in a **Market** or of a **Token**. Orders placed by hand, or by
other workflows on the same wallet, are never touched. What an order filled before stays.
Items with something cancelled go out of `main`, those with nothing, or what Polymarket
would not cancel (matched already, say), out of `missed`, with why in `cancel`.

### Order activity

[Order activity](/docs/reference/nodes/triggers/order-activity/) starts the workflow on what
happens to its own orders after the runs that placed them, an item each: `filled`, a
resting order filled, in part or wholly, with the fills; `canceled`, what was left of one
was cancelled, by Cancel orders, the Trading view, Polymarket or its market resolving;
`expired`, a GTD's time came; `failed`, a live trade failed on chain and the books undid it.
Paper and live alike, `mode` saying which. Put an [If](/docs/reference/nodes/logic/if/) on
`event` after it to do different things for each, or tick only some under **Events**. What
happens while the workflow is switched off, or the server is down, does not start it.

A workflow can have both: Wallet activity placing copies as resting orders, and Order
activity telling you on Telegram as they fill.

## Paper trading

Each workflow trading on paper has a paper account, opened by its first order with $1000;
an admin can change **Paper balance** on the Config page. A paper order fills at once
against the live order book, as Polymarket would fill it: the best price first, within its
limit, a FAK as far as the book goes, a FOK wholly or not at all. Each fill pays the
market's taker fee, as Polymarket charges it: on top of a buy, out of a sell.

As on Polymarket, an order must keep to its market: a price on the tick, at least its least
order size. A buy needs the cash for all it may spend, a sell the shares. An order that
does not is refused, saying why, and is kept with the others.

A paper order resting on the book fills as the live market trades **through** its price:
a trade below a resting buy's price, or above a resting sell's, would have met it first,
so it fills by as many shares as traded, at its own price, as a maker, paying no fee. A
trade at exactly its price leaves it waiting, as where it would be in the queue is not
known. This is an approximation: trades while the server was down are not seen, and
others' orders ahead of it at the same price are guessed at, not known. A reset cancels
what rests, with the cash it held.

When a market resolves, what the workflow holds in it settles on its own within a few
minutes, at what a share pays: $1 for the outcome that won, nothing for the others. The
cash goes to the paper account, and the order list shows it as settled.

## Trading live

Live, a workflow's orders are real: Polymarket fills them from one of your wallets, with
its pUSD. Three things come first:

- An admin turns on **Live trading** on the Config page, under Trading. Off, which it is
  to start with, every workflow trades on paper; a workflow already switched to live keeps
  its switch, but its orders are refused until it is on again.
- A wallet that can trade, on the [Wallets](/docs/guides/concepts/#wallets) page: its
  private key imported, or a session key authorised; its trading approvals granted; and
  pUSD in it.
- The server somewhere Polymarket takes orders from: the Config page warns when it does
  not (see [Where the server runs](/docs/self-hosting/install/#where-the-server-runs)).

### Switching

In the **Trading** view, **Trade live…** asks first: from which of your wallets that can
trade, how much pUSD it holds, and that its orders now spend real money. Ticking **I
understand** unlocks **Switch to live**. **Back to paper** goes back at once, and the
next order is on paper.

A workflow's live holdings are its own books: going back to paper keeps them, and live
again finds them. While it holds live shares, or has live orders resting or pending, its
wallet stays, so a copied sell never goes to a wallet without the shares, and no order is
followed from one wallet while its sells go from another.

### Stopping it

**Stop live trading**, in the top bar once you have a wallet that can trade, is your kill
switch. It works at once, without asking: every live order of your workflows is refused
from then on, out of Place order's `missed` saying so, and those resting on the book are
cancelled; your own orders on polymarket.com are left alone. A red bar on every page says
it is on. Your workflows stay live and keep running, paper ones trading on, so **Resume
live trading** in that bar, after asking, has them trade live again as before. On
Telegram, it is the 🛑 button under [Status](/docs/guides/telegram/#the-menu). An order on
its way to Polymarket as you press it, and coming back to rest, is cancelled within half
a minute. It stops your [AI agent nodes](/docs/guides/agents/#caps) too only if you chose so,
under Settings → AI agents.

An admin caps what each user's live buys may spend, on the Config page under Trading:
**Max $ a live order**, $100 to start with, and **Max $ a day of live buys**, $1,000, for
what your live buys spent in the last 24 hours, by when they filled, and what your
resting ones may still spend, all your workflows together. A buy over either is refused before it is sent,
saying which cap and what is left; sells are never capped. Unlike the Risk guard's caps,
which make a copy smaller, these are a hard stop the Risk guard cannot raise. An admin can
give you caps of your own on the Users page.

When an admin disables an account, its kill switch goes on, and stays on if it is enabled
again, for its owner to turn off. When an admin turns **Live trading** off, every live
order resting on the book is cancelled, everyone's.

### What happens to an order

Each order is checked as on paper: on the market's tick, at least its least order, and a
sell only of shares the workflow holds live copying that trader, not held back by its
resting sells. Polythought then signs it with the wallet's key, as Polymarket's own client
does, puts it in the books as `pending`, *sending to Polymarket*, and only then sends it.
Should the server stop, or its database fail, just after, the order is there to be looked
up on Polymarket and followed; one that never reached Polymarket shows as refused after
10 minutes. If it cannot be put in the books first, it is not sent.

- **It counts at match.** As soon as Polymarket matches it, its fills are in the books and
  the item goes on. The trades then settle on chain, and the order list says *confirming
  on chain* until they have. A trade that fails on chain is taken out of the books, the
  order shows as `failed`, and you are told: the notification **A live trade fails**, on by
  default, in [Settings](/docs/guides/telegram/#notifications).
- **Some markets make takers wait** a few seconds, sports ones among them; Place order
  waits for such an order, up to 30 seconds, and so for one whose sending got no answer.
  Still undecided after that, it goes out of `missed` as `pending`, and is followed: the
  books show what it came to once Polymarket says. An order's ID is known before it is
  sent, so one sent again after a lost answer is found, never placed twice.
- **Fees** are Polymarket's taker fee, worked out per fill as Polymarket charges it: on top
  of a buy, out of a sell. With a builder code set (below), the builder's fee is on top.
- **Resting orders** rest on Polymarket's book. Polythought follows them through
  Polymarket's live channel for the wallet, reading each again as it changes, and every
  half minute anyway; a fill of one, as a maker, pays no fee. Polymarket ends a GTD at its
  time; **Cancel** asks Polymarket to cancel what is left.
- **They are cancelled on their own** when the workflow goes back to paper, when it is
  deleted, and when its wallet is unlinked: unlinking a wallet that workflows trade live
  from names them first, and once you confirm, cancels their resting orders and puts them
  back on paper. Switching a workflow off asks: its resting orders keep resting unless you
  tick **Also cancel**. From Telegram, switching off keeps them, and says so; the API's
  `POST /workflows/{id}/deactivate?cancelOrders=true` cancels them too.
- **Heartbeats**, a switch on each wallet that can trade, off to start with: on, the
  server tells Polymarket every 5 seconds that it is there while the wallet has orders
  resting, and if it goes quiet for 10 seconds, a restart or a network blip included,
  Polymarket cancels every one of the wallet's resting orders, of every workflow. Turned
  off while orders rest, the heartbeats go on until none do, as stopping them would cancel
  the orders at once.
- **The cash** is the wallet's pUSD, which every workflow trading from it shares; the
  Risk guard's caps count each workflow's own live buys and holdings, an admin's caps all
  of yours.

When a market resolves, the live holdings in it settle in the books as on paper, at what a
share pays. Redeeming the shares themselves is Polymarket's, which redeems resolved
positions on its own, or yours, on polymarket.com.

Deleting a workflow keeps its live orders and holdings, as the record of real money spent;
its paper books go with it.

## See how it does

A workflow with trading nodes has a third view beside **Canvas** and **Code**: **Trading**.
It shows, refreshed every half minute:

- **The paper account**: what it is worth, its cash and what it holds now, its cash alone,
  what of it resting buys hold back, and its profit or loss since it started.
- **Traders**: a row per trader copied, and one for orders copied from no one: how many
  copies filled, the dollars they traded, the profit or loss made on what was sold
  (realized) and what what is held would make sold now (unrealized), and the win rate, the
  closed copies, sold out of or settled, that made money.
- **Holdings**: what is held, per trader and outcome, with what it cost and what it is
  worth now, at the midpoint of the order book; when the book has only one side, at the
  market's own price.
- **Orders**: every order, the newest first, with how it went; live ones marked so, and
  those resting on the book with **Cancel**.

Live, the account is the wallet's: what the workflow is worth, the wallet's pUSD, and the
profit or loss its live holdings made, sold and still held. A workflow set up to trade
live has **Paper books** and **Live books** to look at either.

**Reset**, after asking, starts the paper account over: its cash goes back to the Paper
balance and its holdings are emptied. The orders stay in the list, under the reset, and
the results count from it.

The same is in the API: `GET /api/v1/workflows/{id}/trading`, with `?mode=paper` or
`live`, `/orders`, `/holdings` and `/paper-account`, and
`POST /api/v1/workflows/{id}/paper-account/reset`. How it trades is
`GET` and `PUT /api/v1/workflows/{id}/trading/setup`; going live there needs
`"confirm": true`. A wallet's pUSD is `GET /api/v1/wallets/{id}/cash`.

### Live orders

**Live orders**, beside Wallets in the top bar, lists every live order of all your
workflows, the newest first, deleted workflows' too, under the name they had: the record of
the real money they spent. Each says its workflow, its wallet, what it was, its market and
what came of it, with its fees. **Wallet**, **Workflow** and **Status** narrow the list,
and **Download CSV** downloads what they keep, the newest 10,000, a row per order. Each
wallet on the Wallets page has **Live orders** too, opening the list for that wallet. In the
API, it is `GET /api/v1/orders`, with `wallet`, `workflow` and `status`, and
`GET /api/v1/orders/export` for the CSV.

To hear of orders as they happen, switch on the order
[notifications](/docs/guides/telegram/#notifications) you want, live and paper.

### On Telegram

With [Telegram linked](/docs/guides/telegram/), the menu's **👥 Traders** section has each
workflow's traders with their results, and switches a trader on or off from your phone.
A switch saves the workflow as a new version. An editor open on it with changes not saved
yet then says, when it saves, that the workflow changed elsewhere, and reloading it drops
those changes.
