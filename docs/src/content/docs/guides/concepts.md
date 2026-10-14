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
best prices to its item, Filter passes on only the items that match.

A node's settings can use an item's fields. The Webhook node's message, for example, puts
in a field with `{{question}}` or `{{spread}}`.

## Ports

Nodes take items in and send them out through **ports**. Most have one input and one output,
both called `main`. Some have more: If sends each item out of `true` or `false`, depending
on a comparison. A line connects one node's output port to another's input port, and every
node connected to a port gets the items sent through it.

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
holds its items for a while or until a time of day. The execution then **waits**: it keeps
nothing busy meanwhile, survives a restart of the server, and carries on from that node when
the wait ends, with the nodes after it.

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

A workflow only ever reaches its owner's wallets and credentials, and sharing it leaves them
out.

## Limits

To keep a shared instance fair, each user has a limit on workflows, active workflows and
nodes per workflow, and schedules cannot fire faster than a minimum interval. Admins set
them; see [Settings](/docs/reference/settings/#workflows).
