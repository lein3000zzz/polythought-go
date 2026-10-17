---
title: Expressions
description: Settings worked out for each item, like {{ question }} or {{ params.max }}.
---

A node's settings can hold `{{ }}`. What is inside is worked out for each item the node
gets, so one setting can differ from item to item: a message that names each market, a
limit taken from another setting.

## Fields

`{{question}}` puts in the item's `question` field. A path reaches inside it:
`{{ outcomes.0.price }}` is the first outcome's price. A field the item does not have gives
nothing.

## Expressions

Anything else between `{{ }}` is TypeScript; plain JavaScript works as it is. The item's
fields are there by name, and three more names:

| Expression | Gives |
|---|---|
| `{{ dec(spread).times(100) }}` | the item's `spread`, times 100, exactly |
| `{{ item.question.toUpperCase() }}` | `item` is the whole item |
| `{{ params.least }}` | `params` are the node's settings, as you set them |
| `{{ execution.id }}`, `{{ execution.startedAt }}` | the run |
| `{{ dec(bestAsk).minus(bestBid) }}` | exact arithmetic on prices |
| `` {{ `${question} (${outcomes.length} outcomes)` }} `` | text of your own |

A field named `item`, `params` or `execution` is `item.params`, and so on.

Prices and other decimals are text in items, like `"0.0200"`, so they stay exact.
`dec(spread)` computes with them exactly, for comparing and computing prices;
`Number(spread)` makes a floating-point number, fine for a message. `spread * 100` works
too, but the editor underlines it: to TypeScript, a decimal is text.

Expressions only work things out: they cannot fetch data or reach the network. Each node
run works out all of its items at once, in the same sandbox as code, within the limits an
admin sets on the Config page.

## In settings that are not text

A number, a duration, a choice or a true-or-false setting takes an expression when its whole
value is one `{{ }}`: **Limit** `{{ params.max }}`, **Operator** `{{ params.urgent ? 'gt' :
'gte' }}`. What it gives must suit the setting: a limit of `"lots"` fails the node with
`{{ word }} gave "lots", which must be a whole number`. An expression that gives nothing
leaves the setting's default.

Switches and dropdowns cannot hold text, so press **`{{ }}`** next to the setting's name to
write an expression instead; press it again to choose as before. A table takes one the same
way, for the whole table: it must give a list of rows, each as the table's columns say. Its
cells are plain values, `{{ }}` and all.

Text around expressions makes text, which the setting then reads: `{{ minutes }}m` is a
duration.

## Messages

The messages of Log, Webhook and the Telegram nodes are written once per item, with that
item's values. In Telegram's **html** format the message's tags stay, and every value is
escaped, so an item cannot change the formatting.

## Where they do not work

- Triggers: they run before there is an item.
- Wallets and credentials.
- Settings that name a field, like If's **Field**: they say where to look, not what to find.
- The buttons of a Telegram question, which make the node's ports.

The editor marks `{{ }}` in these at once, and saving refuses it: it would stay as written.

## Writing them

Inside `{{ }}` the editor completes as you type: the fields of the items there, with what
each holds, and `item`, `params`, `execution` and `dec` with its methods. Press Ctrl+Space
to see what can come where the cursor is. What TypeScript finds wrong is underlined, with
what is wrong when you point at it, like a field the items do not have, or a decimal used as
a number. That does not stop saving: the server's own check below does.

## When something goes wrong

Saving checks that every expression compiles, and points at the problem:
`{{ price * }}: line 1, column 8: Unexpected end of the expression`.

An expression that throws while running fails its item, as a node fails items it cannot
handle, with the setting and the expression in the error: `param "message": {{ nope() }}:
ReferenceError: nope is not defined`. The node goes on with the other items. Then, as for
any failed item, the run fails, unless the node has **Continue on failure** on: the item
goes out of its `error` port instead, for you to handle however you like; see
[When items fail](/docs/guides/concepts/#when-items-fail).
