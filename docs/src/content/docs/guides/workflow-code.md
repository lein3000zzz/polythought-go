---
title: Workflows as code
description: Read and edit a whole workflow as TypeScript, then put it back on the canvas.
---

A workflow can be written as well as drawn. **Code**, at the top of the editor, shows the
workflow on the canvas as TypeScript; **Apply** puts what the code says on the canvas.
**Canvas** goes back to it.

```ts
import { workflow } from "@polythought/workflow"

export default workflow("Spread watch", (w) => {
  const manual = w.trigger("manual", "trigger.manual")
  const markets = w.node("markets", "polymarket.markets", { limit: 5 })
  const books = w.node("books", "polymarket.orderbook")
  const tight = w.node("tight", "logic.if", {
    field: "spread",
    operator: "lt",
    value: "0.02",
  }, { name: "Tight spread" })
  const log = w.node("log", "output.log", { message: "Tight: {{ question }}" })

  manual.to(markets)
  markets.to(books)
  books.to(tight)
  tight.port("true").to(log)
})
```

## Reading it

The code declares each node once, then connects them, one line per connection:

- `w.trigger(id, type, settings)` declares a trigger, and `w.node(id, type, settings)` any
  other node, with the settings its [reference page](/docs/reference/nodes/) lists. A fourth
  argument holds its name, notes and **Continue on failure**:
  `{ name, notes, continueOnFail }`.
- `a.to(b)` connects a's output to b. `a.port("true").to(b)` connects another of its
  outputs, like If's `true`, a button of Telegram: ask, or the `error` port of a node that
  continues on failure.
- `a.to(b.input("b"))` connects to an input other than b's main one, for a node with more
  than one.

Where the nodes sit on the canvas is not code. The editor prints a workflow the same way
every time, its nodes and connections in the order they were made.

## Writing it

The editor knows `@polythought/workflow` and your node types,
[custom nodes](/docs/guides/custom-nodes/) included. It completes node types, their settings
and port names, and underlines what does not fit: a setting the node does not have, one it
needs left out, a port it lacks. Type errors do not stop **Apply**: only code that does not
compile or does not run is refused.

It is TypeScript, so it does quickly what is slow to draw, like a node for each threshold:

```ts
import { workflow } from "@polythought/workflow"

export default workflow("Spread bands", (w) => {
  const manual = w.trigger("manual", "trigger.manual")
  const markets = w.node("markets", "polymarket.markets", { limit: 20 })
  const books = w.node("books", "polymarket.orderbook")
  manual.to(markets)
  markets.to(books)

  for (const [i, value] of ["0.01", "0.02", "0.05"].entries()) {
    const under = w.node(`under${i + 1}`, "logic.if", { field: "spread", operator: "lt", value })
    const log = w.node(`log${i + 1}`, "output.log", { message: `Under ${value}: {{ question }}` })

    books.to(under)
    under.port("true").to(log)
  }
})
```

What is saved is the workflow, not the code: printed again, the loop is three nodes of each,
one by one, and comments are gone.

The code may import `@polythought/sdk` as well, for `dec()` and the types, but it cannot ask
for data: `markets.search` and the other requests are refused. Nothing else can be imported.

## Applying it

**Apply** sends the code to the server, which runs it in the sandbox code nodes run in,
under the same [limits](/docs/guides/code/#limits), and puts the workflow it builds on the
canvas, its name included, as unsaved changes. Nodes the canvas has keep their place, by ID;
new ones go beside the node they connect to. Then save as for any change; **Save**, or
Ctrl+S, in the Code view applies the code first.

When the code does not compile, throws, or exports no workflow, the canvas stays as it was
and what went wrong is shown under the code, with its line and column when it does not
compile. The builder throws, saying why, for a node ID used twice and for a connection to a
node that is not in the workflow.

Saving checks the workflow as it checks any: a setting that is wrong is marked on its node,
on the canvas.

## Leaving the code

Going back to the canvas with code not applied asks first: **Apply**, **Discard**, or
**Keep editing**. Leaving the editor asks too: **Apply and save**; **Discard**, which drops
the canvas's unsaved changes as well; or **Stay**. Closing the browser tab gets only the
browser's own warning.

## Who may

Apply runs code on the server, so it follows
[Who may write code](/docs/guides/code/#who-may-write-code). Without it, the Code view shows
the code to read and copy, and **Apply** is off.
