---
title: Follow traders
description: Start a workflow on the trades of the Polymarket traders you follow, as they make them.
---

The [Wallet activity](/docs/reference/nodes/triggers/wallet-activity/) trigger follows
traders on Polymarket. Each trade one of them makes starts the workflow, about two seconds
after it, with the trade as its item.

## Add the traders

1. Place **Wallet activity**, from the **Triggers** group of the node list.
2. Under **Traders**, press **Edit traders**, then **Add a row** for each trader:
   - **Label**: who they are to you, like `Alice`.
   - **Address**: their wallet, the `0x…` address in the link of their profile,
     `polymarket.com/profile/0x…`. Saving checks it, a mistyped letter included.
   - **On**: switch a trader off to stop following them and keep their row.
   - **Size ×** and **Max $**: go with each of their trades, as `trader.scale` and
     `trader.maxDollars`, for the nodes after the trigger.
3. Save, and switch the workflow **Active**.

## What a trade is

A trade is one trader's fills in one transaction, outcome and side, added up: the shares,
the dollars, and the average price. An order that filled against many others is one trade,
and starts the workflow once.

Both kinds of trade count: orders they place against the book, whose `role` is `taker`,
and their resting orders that fill, `maker`. The item also has the market, the outcome
and the transaction; see the [list of fields](/docs/reference/nodes/triggers/wallet-activity/#items).

**Sides** and **At least $** choose which trades go on: only buys, say, or only trades of
$100 or more. For anything finer, put an [If](/docs/reference/nodes/logic/if/) after it,
like `role` is `taker`.

## When some are missed

The trades come from Polymarket's live feed of activity. When the feed drops, or the server
restarts, Wallet activity looks up what it missed and starts the workflow for the trades
younger than **Catch up within**, 2 minutes by default and up to an hour. Older ones are
skipped: a trade is never passed on late. Those found this way have no `role`, as the
lookup does not say.

Switching a workflow on follows from then: trades from before are not caught up. Saving an
active workflow catches up on what came in while it restarted.

The lookup also runs every 30 seconds, and every 5 seconds while the feed is down, so a
trade the feed lost still comes through.
