---
title: Code nodes
description: Transform, filter, route and aggregate items with TypeScript.
---

When no node does what you need, write it. Four nodes in the **Code** group of the node
list run TypeScript you write in their **Code** setting, starting from code that already
works:

| Node | Your code | Gets | Returns |
|---|---|---|---|
| [Code: transform](/docs/reference/nodes/code/transform/) | `export function run(items, ctx)` | every item at once | the items to send on |
| [Code: filter](/docs/reference/nodes/code/filter/) | `export function keep(item, ctx)` | one item at a time | `true` to keep it |
| [Code: route](/docs/reference/nodes/code/route/) | `export function route(item, ctx)` | one item at a time | the name of one of its **Routes** |
| [Code: aggregate](/docs/reference/nodes/code/aggregate/) | `export function run(items, ctx)` | every item at once | one item, or `null` for none |

Each node run calls your code once, in a sandbox: it has no files and no network, only
what the SDK gives it. For a node type of your own, with settings and outputs you choose,
see [Write your first custom node](/docs/guides/custom-nodes/); for a whole workflow as
code, [Workflows as code](/docs/guides/workflow-code/).

```ts
import { dec, markets } from "@polythought/sdk"
import type { Context, PolymarketOrderbookItem } from "@polythought/sdk"

// Keep the books whose spread is under the node's own threshold.
export function keep(item: PolymarketOrderbookItem, ctx: Context<{ least: string }>): boolean {
  return item.spread !== null && dec(item.spread).lt(ctx.params.least ?? "0.02")
}
```

## Writing it

The **Code** setting is an editor that knows the SDK and your node types, your custom nodes
included: it completes as you type, shows what a name is when you point at it, and
underlines type errors, listing them under the code. Type errors do not stop saving: saving
refuses only code that does not compile.

## What the code works with

- **Items** are plain objects. Decimals in them, like prices, are text such as `"0.0200"`, so
  they stay exact; `dec()` does arithmetic on them. Times are text too.
- **`ctx`**, after your function's own arguments: `ctx.params`, the node's other settings;
  `ctx.execution`, the run's ID, workflow and start; `ctx.log(...)`, like `console.log`.
- **Data**, through the SDK's requests, which you `await`: `markets.search` and
  `markets.get`, `orderBooks.get`, and `positions.list` for one of your own wallets. Each one
  counts against the run's limit.
- **Types** for every node's items and settings come with the SDK, named after the node:
  `PolymarketMarketsItem`, `PolymarketOrderbookItem`. See [Types for code](/docs/reference/code-types/).

The code may import only from `@polythought/sdk`. Everything else, npm packages and files
alike, is refused when you save.

## Limits

Each run has limits an admin sets on the Config page: memory, time, the size of what it
returns, and how many data requests it makes. Going over one stops the run of the node.
Runs share the server's sandbox slots, and one user's take at most half of them, so a
busy workflow cannot keep everyone else's waiting. An admin may run code in
[processes of its own](/docs/self-hosting/install/#user-code) rather than the server's; it
works the same either way.

Code compiles if it nests at most 256 levels of brackets, has at most 256 of `! ~ + -` in
a row, and at most 1,000 arrow functions; an expression, at most 100. Real code stays far
below; past them, the compiler would take the server down or minutes.

## Who may write code

An admin decides, with [Who may write code](/docs/reference/settings/#who-may-write-code) on
the Config page:

- **everyone**, the default;
- **chosen**: admins, and the users an admin switched on in the **Code** column of
  **Admin → Users**;
- **off**: no one, admins included.

Expressions are not code: they work for everyone either way.

If you may not write code, the Code group of the node list is greyed out, and code nodes
already in a workflow say *Code is off for you*. A workflow with code nodes cannot be
saved, and the editor's Code view shows a workflow's code without applying it. When one is triggered, it fails at once, before any node runs, and says why. It
stays on, so once you may write code again it runs as before.

## When something goes wrong

Saving compiles the code and points at what does not compile, with its line and column.

When the code throws, or returns what the node cannot use, the items it was given fail:
all of them for Transform and Aggregate, only the one it was working on for Filter and
Route. As for any failed item, the run fails, unless the node has **Continue on failure**
on: then they go out of its `error` port. See
[When items fail](/docs/guides/concepts/#when-items-fail).

What the code writes with `console.log` or `ctx.log` goes to the server's log for now.

## After a code node

What Transform and Aggregate make is up to your code, so the nodes after them cannot know
its fields: their field settings are not checked, and the editor offers no fields there.
Filter and Route pass items on as they came, so the nodes after them know the items as
well as before.
