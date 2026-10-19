---
title: Concepts
description: Workflows, nodes, items, ports and executions.
---

A **workflow** is a small program you draw: **nodes** connected by lines. Each node does one
thing, such as looking up markets, keeping the ones with a tight spread, or posting a
message to a chat. Data flows along the lines from node to node.

## Items

What flows is a list of **items**. An item is one record, such as a market, a trade or a
position, with named fields: `question`, `spread`, `price` and so on. Most nodes take items
in and send items out: Markets sends one item per market, Order books adds each market's
best prices to its item, Filter passes on only the items that match, and
[Split out](/docs/reference/nodes/transform/split-out/) makes an item of each entry of a
list, like a market's outcomes, each with the market's other fields.

A node's settings can use an item's fields. The Webhook node's message, for example, puts
in a field with `{{question}}` or `{{spread}}`, and `{{ dec(spread).times(100) }}` works it out; see
[Expressions](/docs/guides/expressions/).

Every node's page in the [reference](/docs/reference/nodes/) lists the fields of the items
it sends out, or says it passes on the items it receives. Prices, sizes and other decimals
are text, like `"0.0200"`, so they stay exact; times are text too, like
`"2026-10-15T12:00:00Z"`.

## Ports

Nodes take items in and send them out through **ports**. Most have one input and one output,
both called `main`. Some have more: If sends each item out of `true` or `false`, depending
on its [tests](#testing-items). A line connects one node's output port to another's input port, and every
node connected to a port gets the items sent through it.

A few nodes take some of their ports from a setting: a
[Telegram question](/docs/reference/nodes/telegram/ask/) has a port for each of its buttons,
named like the button, besides `timeout`, and a [Switch](/docs/reference/nodes/logic/switch/)
one for each of its rules, besides `other`. Renaming a button or a rule's port in the editor
keeps its lines, and so does moving it; removing one removes them.

## Testing items

[If](/docs/reference/nodes/logic/if/) and [Filter](/docs/reference/nodes/logic/filter/) test
each item: a number compared with a value, like `spread` `lt` `0.02`; text that is a value
(`is`, `isNot`) or contains it, in any case; or a field being there at all, `exists` or
`missing`. **More tests** adds more, a row each, and **Match** says how they count together:

| Match | The item passes when |
|---|---|
| `all` | every test passes (AND) |
| `any` | one or more do (OR) |
| `none` | not one does (NOT) |
| `one` | exactly one does (XOR) |

The first test takes expressions: `{{ bestAsk }}` as its value compares one field with
another. The rows of More tests are plain values. A field a test cannot use, like text
where a number is needed, fails the item, whatever the other tests say: it stops the
execution, or with **Continue on failure** on, leaves on the `error` port.

[Switch](/docs/reference/nodes/logic/switch/) has the same tests, one in each of its
rules, a row each with the port it sends to. Rules are tried in order: the first an item
passes takes it, and items no rule takes leave from `other`. A Switch with `price` `lt`
`0.2` as `longshot` and `price` `gte` `0.8` as `favourite` splits copies by price. Its rules
are plain values too, and one that cannot use an item's field fails the item, as there is
no telling where it goes.

## Merging branches

A port can feed several nodes, splitting the items into branches;
[Merge](/docs/reference/nodes/logic/merge/) brings two back together. It has two inputs, `a`
and `b`, and matches their items on a field: **A's field**, and **B's field** when B names it
otherwise. **Keep** says what goes on:

| Keep | What goes on |
|---|---|
| `both` | A's items with a match in B (AND) |
| `either` | A's and B's, once per key (OR) |
| `onlyA` | A's items without a match in B (NOT) |
| `onlyOne` | the items of A or B with no match in the other (XOR) |
| `join` | A's items with a match, each with B's first matching item under `b` |

An item without the field matches nothing. Both branches start from the same trigger, so
Merge combines the items of one run: copies of a trade only in outcomes a wallet already
holds, say, are the trades on `a` and the wallet's [Positions](/docs/reference/nodes/polymarket/positions/)
on `b`, matched on `tokenId`, keeping `both`.

## Thinning out items

[Sample](/docs/reference/nodes/logic/sample/) takes about a share of the items: at **Chance %**
`30`, about 3 in 10 go on, and the rest leave from `rest`. Each item is decided by a field,
**Decide by**, a trade's `txHash` unless you say: the same item gets the same answer every
time, a re-run or a restart too, and two Sample nodes take different ones.

[Throttle](/docs/reference/nodes/logic/throttle/) lets at most so many items through in a
period, **At most** `3` **In** `24h`, counted per value of **Count per**, like
`trader.address`, or all together when it is empty. A period is any stretch of that length
back from each item, not a calendar day. Items over the limit leave from `limited`.

[Consensus](/docs/reference/nodes/logic/consensus/) lets an item on once enough different
traders agreed: at **At least** `2` **Within** `6h`, a trade goes on when another trader made
the same one in the 6 hours before it. **Agree on** lists what must be the same, the outcome
(`tokenId`) and the `side` by default, and **Who** tells traders apart, `trader.address`; a
trader counts once, however often they trade. From then on in the window, each such item goes
on; the ones before leave from `alone`.

## Memory

A few nodes remember what they saw, across runs and server restarts:
[Only new](/docs/reference/nodes/logic/dedupe/) remembers the items it has let through, each
until **New again after** has gone by, Throttle the items it counts, for its period, and
Consensus who agreed on what, for its window. Memory belongs to the node in its workflow. Runs
started by hand count too; it is kept when the node's settings change; and it goes with the
node when the node, or its workflow, is deleted. A node remembers at most 10,000 items:
past that, the oldest are forgotten first.

The node's settings say how many items it remembers, and **Forget** clears them, so every
item counts as new again.

## Triggers

A workflow starts at a **trigger**, a node with no input:

- [Manual](/docs/reference/nodes/triggers/manual/) starts it when you press **Run**.
- [Schedule](/docs/reference/nodes/triggers/schedule/) starts it at a fixed interval, such as every 15 minutes.
- [Market activity](/docs/reference/nodes/triggers/market/) starts it on live trades, order
  book changes or resolutions of the outcomes you choose.
- [Telegram message](/docs/reference/nodes/triggers/telegram/) starts it when you message the
  instance's bot; see [Telegram](/docs/guides/telegram/).

Schedules, market and Telegram triggers only fire while the workflow is **active**. Switch it on in the
editor; switch it off to stop it. You can always run a workflow by hand, active or not: Run
starts from its Manual trigger, or from its Schedule if it has no Manual one.

## Executions

Each start is an **execution**. The trigger's items go to the nodes connected to it, and
the nodes run in order, each once, with every item that reached it. A node no item reached
is skipped.

An execution **succeeds** when every node that ran finished, or **waits** when a node paused
it; see [Waiting](#waiting). It **fails** when a node
fails, for example when the Polymarket API cannot be reached, and then stops. It is
**cancelled** when the workflow is switched off or the server stops while it runs.

Each execution records which nodes ran, how long they took and how many items they took in
and sent out on each port. Runs you start by hand also keep the first few items of every
port, so you can see what flowed through. Finished executions are deleted after a while,
30 days unless an admin
[changed it](/docs/reference/settings/#keep-executions-for).

## Waiting

Some nodes pause the execution instead of finishing at once: [Wait](/docs/reference/nodes/logic/wait/)
holds its items for a while or until a time of day, and
[Telegram question](/docs/reference/nodes/telegram/ask/) waits until you press one of its
buttons. The execution then **waits**: it keeps nothing busy meanwhile, survives a restart
of the server, and carries on from that node when the wait ends, with the nodes after it.

- In the editor, a waiting execution says where it waits and until when, and
  **Cancel execution** ends it.
- Switching a workflow off cancels its executions that wait.
- A wait carries on the workflow as it was when the execution started, even if you edit the
  workflow meanwhile.
- Each user may have a limited number of executions waiting at once, 50 unless an admin
  changed it, and a wait ends within 7 days; see
  [Settings](/docs/reference/settings/#executions).

## When items fail

A node can fail for one item and still work for the others, as when one webhook message is
refused. By default a failed item fails the whole execution. Turn on **Continue on failure**
in the node's settings, and failed items leave on the node's `error` port instead, with the
reason. Connect that port to handle them, say with a Webhook node that tells you, or leave
it unconnected to ignore them.

## Wallets

Some nodes read your own Polymarket account: [Positions](/docs/reference/nodes/polymarket/positions/),
[Open orders](/docs/reference/nodes/polymarket/orders/) and
[Balance](/docs/reference/nodes/polymarket/balance/). They ask for one of your **wallets**,
which you link on the **Wallets** page by its address. Positions are public, so the address
is enough for them. Orders and balance also need the wallet's Polymarket API credentials,
which Polymarket checks before they are stored, encrypted.

To trade, a wallet needs its private key: **Import private key** on its card, where an admin
allows it. It must be the key of the wallet itself, or of the account that owns the wallet's
proxy, Safe or Deposit Wallet, as Polymarket derives them; Polythought checks that before it
keeps the key, and says whose key it is when it does not match. With the key, Polythought
makes the wallet's API credentials itself, replacing any added by hand, and the card says
**Can trade**. The key is kept encrypted and never shown again; **Remove credentials**
deletes it with them, and pasting credentials by hand replaces it.

A key lets the server sign for the wallet, so keep in it only what you mean to trade.

A workflow only ever reaches its owner's wallets and credentials, and sharing it leaves them
out.

## Limits

To keep a shared instance fair, each user has a limit on workflows, active workflows and
nodes per workflow, and schedules cannot fire faster than a minimum interval. Admins set
them; see [Settings](/docs/reference/settings/#workflows).
