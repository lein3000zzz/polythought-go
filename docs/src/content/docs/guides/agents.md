---
title: AI agents
description: Ask a language model about your items, with your own key, and get its answer in the shape you want.
---

The **AI agent** node asks a language model, for each item or once for all of them, and adds
its answer to the item. It works with your own key: Anthropic's for Claude, OpenAI's or any
API compatible with it, such as OpenRouter or a model on your own network, and Google's for
Gemini. Calls are billed to that key's account, not to Polythought, and nothing calls a
provider unless one of your workflows does.

## Setting it up

1. Add your key on the **Credentials** page and press **Test**; see
   [Language model keys](/docs/guides/credentials/#language-model-keys).
2. Add an **AI agent** node, pick the key under **Key**, and a **Model**. The model field
   suggests those the key can use, as the provider lists them, refreshed every hour; any
   other name can be typed too.
3. Write the **Prompt**. It takes [expressions](/docs/guides/expressions/), like
   `Is {{ question }} worth watching?`, worked out for each item. The **System prompt** says
   how the model should go about it, such as `You are a careful prediction market analyst.`

## Each item, or all together

With **Call** on `each`, the node calls the model once per item, a few at a time, and adds
the answer to each item. Twenty markets in means twenty calls.

With `together`, it calls once for all of them, and sends one item on, with the answer
alone. **Include the items**, on by default, adds them after the prompt as JSON, so a
prompt like `Rank these markets by how likely they are to move` is enough. Expressions see
them too, as `items`: `{{ items.map((m) => m.question).join('\n') }}`. In this node a field
named `items` is `item.items`.

## The answer

The answer goes under the **Answer field**, `answer` unless you name another:

| Field | What it holds |
|---|---|
| `answer.text` | The answer as written, answering as text. |
| `answer.json` | The answer as JSON, answering in fields or to a schema. |
| `answer.model` | The model that answered. |
| `answer.usage.input`, `answer.usage.output` | The tokens it took, thinking included, every try counted. |
| `answer.cost` | What the provider says the calls cost, in dollars: OpenRouter says; the others leave it empty. |
| `answer.tries` | How many answers it took: more than 1 when one did not fit. |
| `answer.toolCalls`, `answer.tools` | The tool calls it made; see [Tools](#tools). |

**Answer as** says its shape:

- `text`: as the model writes it.
- `fields`: JSON with the fields of the **Fields** table, a row each: a name, a type (text, a
  number, a whole number, true or false, or a list of text), a description the model reads,
  for text the answers allowed, and whether it may be left out, as null. The node builds a
  schema every provider holds to.
- `schema`: JSON keeping to the **JSON schema** you give, for nested shapes. Claude wants
  `"additionalProperties": false` on every object; OpenAI holds the answer to it strictly
  only when every object also requires all its fields.

The next nodes read it like any field: an If on `answer.json.watch` is `true`, a message with
`{{ answer.json.reason }}`.

## Tools

**Tools** lets the model look things up before it answers, read-only: it decides what to
call, and the node runs it and tells it what came back. Pick any of:

| Tool | What the model gets |
|---|---|
| `searchMarkets` | Markets whose event title has the words it gives: questions, outcomes and their token IDs, prices, volume and liquidity. |
| `orderBook` | An outcome's live book: best bid and ask, midpoint, spread, and the top levels of each side. |
| `priceHistory` | An outcome's prices over the last hour, 6 hours, day, week or month, with how much it moved. |
| `trades` | A market's most recent trades: side, price, size and the trader's address. |
| `positions` | The open positions of any Polymarket address, which are public: size, average price, value and profit. |

**Tool calls**, 10 by default and 25 at the most, is how many it may make for one answer;
past that it is told to answer with what it has, and an item whose model keeps asking
fails. **Results per call**, 20 by default and 50 at the most, is how much one call gives
it. Each call's result is read as input, so more calls and more results mean more tokens,
billed.

The answer lists the calls in `answer.tools`, each with its `name`, its `input`, how many
`results` it gave, and its `output` when **Tool results** is `full`, the default; `inputs`
keeps only what was asked. `answer.toolCalls` counts them. A call that fails, such as an
outcome that does not exist, says why under `error`, and the model is told too, so it can
try another way.

## Caps

What your agent nodes spend with your keys is counted by calendar month, UTC, all your
workflows together, and shown under **Settings → AI agents**: dollars, input, output and
total tokens, and when the month starts again. Polythought keeps no prices: dollars are what
providers report, which OpenRouter does and the others do not, so their calls count in
tokens only.

An admin caps each of them a month, $20.00 and 20,000,000 tokens to start with, input and
output not capped apart unless they set it; they can give you caps of your own. A call that
could pass a token cap, its input and its **Max tokens** counted, is refused before it is
made, and so is every call once the dollars spent reach theirs: a call's dollars are known
only when it ends, so the last one may pass it. A refused call fails its item, with the
reason, like
`this call could take up to 18,000 tokens, past the 20,000,000 a month your agents may use: 19,990,000 used or held`.
Calls already under way finish, and count.

The node can cap one run of its own, beside the month's: **Max $ a run**, **Max input tokens
a run**, **Max output tokens a run** and **Max tokens a run**, each empty for none.

Two notifications tell you, each once a month, and again only if an admin raises the cap:
when your agents pass 80% of a cap, the first that they do, and when one is reached. See
[Notifications](/docs/guides/telegram/#notifications).

Your [kill switch](/docs/guides/copy-traders/#stopping-it) stops live trading only, unless
you turn on **My kill switch stops my agents too** under Settings → AI agents: then, while
it is on, agent nodes make no calls either.

## When it goes wrong

An answer that is not JSON, or does not fit the shape, is asked for again, with what was
wrong, as many times as **Retries** says: once by default, three at the most. Each try is a
call, and billed. After the last, the item fails with the reason. An answer cut off by **Max
tokens** says so: raise it, as thinking counts toward it.

A refusal fails the item at once, and so does the provider refusing the call, in its own
words, like `Anthropic answered 401: invalid x-api-key`. Errors the provider may get over,
such as being busy, are tried again a few times before that. As with other nodes, failed
items stop the run unless **Continue on failure** is on.

**Effort**, low, medium or high, says how hard the model thinks before it answers, for
the models that take it; empty leaves the model's own. **Temperature** is left to the
model when empty; the newest Claude models take none.
