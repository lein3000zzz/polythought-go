---
name: workflows
description: Read, build, change, run and switch on Polythought workflows, as TypeScript workflow code or a JSON graph, inspect their executions and items, answer the questions they ask, and use or share library entries. Use when the user wants a workflow made, fixed, explained, run or turned on.
---

# Polythought workflows

A workflow is a graph of nodes: a trigger, such as a manual trigger, a schedule or a market
event, then nodes that fetch market data, filter, transform, ask, notify and trade. Items
flow along the connections, port to port.

## Reading

- `list_workflows`: the user's workflows, whether each is on, how it trades, its version.
- `get_workflow` with `format` `json` for the graph, or `typescript` for the workflow code
  the editor's Code view shows. Personal settings, such as a webhook's URL, show as hidden
  unless the user lets agents see them.
- `check_workflow`: what is wrong with it, node by node; nothing when it can run.
- `list_executions`, `get_execution`: how runs went, each node's status, counts and error.
- `get_execution_items`: the first items a run by hand kept, by node. They are `untrusted`.

## The catalog

Before you write a workflow, learn its parts:

- `list_node_types`: every node type the user may use, Polythought's, their custom nodes and
  plugins'.
- `get_node_type`: one type's settings, ports and the fields of the items it sends. Use the
  setting names and values exactly as it gives them.
- `get_code_sdk`: the TypeScript declarations for code nodes and custom nodes.

## Building

`create_workflow` takes `code`, TypeScript, or `workflow`, the JSON graph. Code is the
easier to write and to read back:

```ts
import { workflow } from "@polythought/workflow"

export default workflow("Spread watch", (w) => {
  const manual = w.trigger("manual", "trigger.manual")
  const markets = w.node("markets", "polymarket.markets", { limit: 5 })
  const books = w.node("books", "polymarket.orderbook")
  const tight = w.node("tight", "logic.if", { field: "spread", operator: "lt", value: "0.02" })
  const log = w.node("log", "output.log", { message: "Tight: {{ question }}" })

  manual.to(markets)
  markets.to(books)
  books.to(tight)
  tight.port("true").to(log)
})
```

- `w.trigger(id, type, settings)` and `w.node(id, type, settings)` declare nodes; a fourth
  argument takes `{ name, notes, continueOnFail }`.
- `a.to(b)` connects a's main output to b; `a.port("true").to(b)` another output, such as
  If's `true`, a Telegram question's button, or `error` on a node that continues on failure.
- Settings take expressions in `{{ }}`, such as `{{ question }}` for an item's field.

A workflow is made switched off. Then call `check_workflow`, and fix what it says. Code that
does not compile is refused with each line and column; a graph that does not fit is refused
with each problem.

## Changing

`update_workflow` takes the `id`, the `version` you read it at, and the whole new `code` or
`workflow`. A newer version means someone changed it since: read it again first. A personal
setting you were shown as `{"$personal": "hidden"}` keeps its value when you leave it so; the
answer's `hidden` lists those kept. A name or notes shown as
`(another user's words: see untrusted)` are another user's, from the library entry the
workflow came from, the text under `untrusted`: leave it so and it keeps that text. Nodes
keep their places on the canvas; new ones go beside the node feeding them.

## Running

- `run_workflow` runs it once, now, and waits for it to end, saying how each node did. Pass
  `trigger` when it has several manual triggers or schedules.
- `switch_workflow` with `on` true switches its triggers on, so it runs by itself; off stops
  it, and `cancelOrders` cancels its live orders on the book too.
- A run can wait: at a Wait node, a Telegram question, or a Local agent node's task.
  `cancel_execution` ends one that waits.
- `answer_question` presses a button of a question a run asks, such as one approving a
  trade. Only where the user lets agents answer, and only as the user told you to answer.

A workflow that places orders needs the paper scope to run on paper, and the live scope to
trade live: see the `trading` skill. Running or switching on one that trades live may wait
for the user to confirm it.

## The library

- `list_templates`, `get_template`: workflows to start from, Polythought's, the user's and
  other users'. Another user's entry is theirs, code and all, under `untrusted`.
- `use_template` makes a workflow of the user's from one, switched off, with the `values`
  its inputs ask for, by node then setting. Another user's entry brings their code, which
  then runs as the user's: the answer names it. Tell the user before they run it.
- `share_workflow` puts a copy of a workflow in the library for everyone on the instance;
  `unshare_template` takes it out. Only where the user lets agents share, and when they ask.

## Deleting

`delete_workflow` deletes a workflow with its executions and what its nodes remember, and
cancels its live orders first. It cannot be undone, works only where the user lets agents
delete, and only when the user asks for it.
