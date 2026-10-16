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
what the SDK gives it.

```ts
import { dec, markets } from "@polythought/sdk"
import type { Context, PolymarketOrderbookItem } from "@polythought/sdk"

// Keep the books whose spread is under the node's own threshold.
export function keep(item: PolymarketOrderbookItem, ctx: Context<{ least: string }>): boolean {
  return item.spread !== null && dec(item.spread).lt(ctx.params.least ?? "0.02")
}
```

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
