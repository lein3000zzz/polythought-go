---
name: trading
description: Trade on Polymarket through Polythought workflows, on paper or live with real money: a workflow's trading setup, the user's wallets and pUSD, orders and fills, paper accounts, cancelling orders and the kill switch. Use when the user wants a workflow to trade, asks how one has done, or wants trading stopped.
---

# Trading with Polythought

Workflows trade, not you directly: a workflow's order nodes place orders, on paper with a
paper account, or live from one of the user's wallets, spending its pUSD. Paper is the same
order flow with no money; start there.

## Reading

- `list_wallets`, `get_wallet`: the user's linked wallets, whether each can trade, its pUSD.
- `get_trading`: how a workflow trades and has done: paper or live and from which wallet, its
  cash, worth and profit, what it holds, and whether the kill switch is on.
- `get_workflow_stats`: how all the user's workflows are doing at once: each one's profit in
  the mode it trades in now, its open positions and volume, its runs and failures in the last
  day and week, and paper and live totals, never added together; on its first page, what
  copying each trader came to across the workflows. Start here for "how are my workflows
  doing" or "which traders make me money", then `get_trading` for one of them.
- `list_orders`: a workflow's orders, paper and live, with status, price, fills and fees.
- `list_live_orders`: the user's live orders across workflows, narrowed to a workflow, a
  wallet or a status.

## Setting a workflow to trade

`set_trading` with `mode` `paper`, or `live` and the `wallet` ID. Paper needs the paper
scope; live needs the live scope and a wallet that can trade.

Going live, or to another wallet, waits for the user to confirm it where they have it wait,
as they do to start with: the answer gives a confirmation ID for `get_confirmation`. Where
they let it go unconfirmed, it needs `confirmed` true, which says the user agreed to its
orders spending real money from that wallet. Set `confirmed` only when the user told you so
in this conversation, for this workflow and wallet; never assume it. Back on paper, its live
orders resting on the book are cancelled.

Switching on or running a workflow that trades live, and changing one that is on, may wait
for the user too (the `workflows` skill).

## Orders and accounts

- `cancel_order`: cancel one of a workflow's orders resting on the book, paper with the paper
  scope, live with the live scope.
- `reset_paper_account`: start a paper account again, its orders, holdings and results gone.
  It cannot be undone; ask first.

## The kill switch

`set_kill_switch` with `on` true stops live trading at once, with any scope: every live
order of the user's workflows is refused and those resting on the book cancelled; paper
trades on. Turn it on without asking when the user wants trading stopped. Off needs the live
scope and waits for the user to confirm it where they have it wait.

## Limits you cannot change

The user's caps on live orders, an order's and a day's, and an admin's, hold every live
order; Polymarket's rules on prices, tick sizes and smallest orders too. A refused order
says why. Where Polymarket does not take orders from the server's region, live orders fail.
