---
title: Write your first custom node
description: Make a node type of your own, with its settings, outputs and items, and TypeScript that does the work.
---

A custom node is a node type of your own. A form says what it looks like in the editor:
its settings, its outputs and the items it sends on. TypeScript says what it does. Once
saved, it is in the **Custom** group of the node list, for your workflows only.

This page makes **Spread split**, which sends order books with a wide spread one way and
the others another, with the threshold as a setting.

## Make it

1. Open **Nodes** in the top bar and choose **New custom node**.
2. Give it a **Name**, `spread-split`. Its type is then `custom.spread-split`, which is how
   workflows name it. The name cannot change later.
3. Give it a **Label**, `Spread split`: what the node list and the canvas call it.
4. Under **Settings**, add one: name `threshold`, kind **Decimal**, default `0.02`.
5. Under **Outputs**, rename `main` to `wide` and add `tight`.
6. Leave **Items** as **Passes on the items it gets**.

The code on the right follows the form as you go, while you leave it as it is. It is the
same editor as for [code nodes](/docs/guides/code/#writing-it), which knows the SDK and
the types of your nodes. Change it to:

```ts
import { dec } from "@polythought/sdk"
import type { Context, PolymarketOrderbookItem } from "@polythought/sdk"

type Settings = { threshold: string }

// Gets every item at once; returns the items of each output, by name.
export function run(items: PolymarketOrderbookItem[], ctx: Context<Settings>) {
  const wide = items.filter((b) => b.spread !== null && dec(b.spread).gt(ctx.params.threshold))
  return { wide, tight: items.filter((b) => !wide.includes(b)) }
}
```

Then **Make the node**. In a workflow, place it after **Order books**, and connect its
`wide` and `tight` outputs to what each should do.

## How it runs

`run(items, ctx)` is called once per node run, with every item at once, as in
[Code: transform](/docs/guides/code/). With one output, it returns the items to send on;
with several, an object with the items of each output by name. Leave an output out to send
nothing there.

The code works with what [code nodes](/docs/guides/code/#what-the-code-works-with) do:
`dec()` for exact decimals, `ctx.params` for the node's settings, `ctx.execution`, and
the SDK's data requests.

Throwing, or returning what the node cannot use, fails every item it got: as for any
failed item, the run fails unless the node has **Continue on failure** on, which sends
them out of its `error` port.

## Settings

Each setting has a name the code reads it by, `ctx.params.threshold`, and one of these
kinds:

| Kind | The code gets |
|---|---|
| Text, Duration | text; a duration like `30s` |
| Whole number | a number |
| Decimal | exact text like `"0.02"`, for `dec()` |
| True or false | `true` or `false` |
| List of text | a list of text |
| One of a few choices | one of the choices you list |
| A field of the items | a path like `spread`; the editor offers the fields of the items there, and saving checks it |
| Wallet | the ID of one of the wallets of whoever places the node, for `positions.list` |

A setting with a default has it when left empty. **Required** ones must be filled in.
Credentials are not a kind a custom node takes, so its code never sees a secret.

## Items

A node either passes on the items it gets, as Spread split does, or makes items of its
own. Then declare their fields: a name, a kind, and whether they may be missing or null.
An object or a list opens rows for what it holds, so `outcomes[].price` can be declared.

The nodes after it know these fields: their field settings offer them and saving checks
them. So that they can count on them, the items `run` returns are checked when it runs: if
one lacks a declared field, or has one of the wrong kind, every item the node got fails,
saying which and how. Fields beyond the declared ones are fine.

## Changing it

Workflows always use the latest version of a custom node. If a change breaks some of
them, say by taking away an output they are connected to or a setting they set, saving
first lists them and what breaks. Going ahead, they fail when they run until you edit
them; opening one marks what no longer holds. Deleting a node that workflows use asks the
same way.

An agent over MCP with the build scope can make and change your custom nodes too, held to
the same rules: a change that breaks workflows is refused, saying which and how, until it
asks again to go ahead. It deletes one only where you let it, with **Agents and tokens may
delete** on **Settings → Connected agents**.

## Sharing it

Sharing a workflow that uses a custom node to the [library](/docs/guides/library/) brings
the node along, code included, as it is then. Whoever uses the workflow gets a copy of the
node of their own, renamed if they have a different one by its name.

## Who may, and how many

Custom nodes are code: an admin decides who may make them, as for
[code nodes](/docs/guides/code/#who-may-write-code). Without it, you can still look at
yours and delete them, and workflows that use them do not run.

Each user may have up to 50 custom nodes, a [setting](/docs/reference/settings/) an admin
can change, each with up to 20 settings, 10 outputs and 64 KiB of code.
