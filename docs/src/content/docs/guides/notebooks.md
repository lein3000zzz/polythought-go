---
title: Notebooks and models
description: Your notebook and your workflows, both ways, through datasets and models.
---

Your notebook works out what to bet on. Your workflows place the bets and record what they
saw. Polythought runs none of the notebook: it stays on your laptop or GPU box, wherever
Jupyter runs, and calls out to Polythought. Nothing reaches in.

The loop:

1. The notebook reads Polythought's data: market prices, and what your workflows recorded.
2. It tests an idea, then either publishes a **dataset** of predictions or **serves a
   model** that answers on demand.
3. A workflow reads the dataset with **Read dataset**, or asks the model with **Ask a
   model**, and trades on the answers, on paper first.
4. A **Record** node keeps what the workflow saw and did in another dataset.
5. The notebook reads that back, and you judge the idea by what happened.

This guide walks the loop with one example: daily high temperatures for the cities Polymarket
runs weather markets on.

## Setting up

- **An API token** for the notebook, under **Settings → API tokens**, with **Read** and
  **Write datasets and models**. Add **Build** if the notebook should run workflows itself.
- **The Python SDK**, from your Polythought, which serves the version that matches it:

  ```sh
  pip install "polythought[pandas]" --find-links https://polythought.example.com/api/v1/sdk/python/
  ```

- **For a model**, make it on **Data → Models** and a worker key for it. The key serves that
  model alone, and is shown once.

An admin decides who may use datasets and models, under **Config → Models and datasets**.

```python
from polythought import Client

pt = Client("https://polythought.example.com", token="pt_…")
```

## 1. Reading

```python
markets = pt.market.search("highest temperature in NYC")
token = markets[0]["outcomes"][0]["tokenId"]
prices = pt.market.history_frame(token, window="1w")  # a time index, prices as numbers
```

What your workflows recorded is a table too, once they record (step 4):

```python
seen = pt.datasets.frame("bets-seen")
```

## 2a. Publishing predictions

When your predictions change slowly, a few times a day say, publish them as a dataset and let
workflows read them. A replace swaps the whole table in at once, so a workflow never reads it
half-written:

```python
pt.datasets.create("forecasts", key_field="city")

predictions = forecast_highs()  # a DataFrame: city, high, low, …
pt.datasets.replace_frame("forecasts", predictions)
```

In the workflow, **Read dataset** looks up each item's row by its key, such as
`{{ city }}`, and puts it on the item under `row`. Items with no row go out of `missing`. Or
it sends every row on, an item each.

## 2b. Serving a model

When an answer depends on the moment, the book's prices say, serve a model instead. The
workflow asks it about each run's items, and it answers each one:

```python
from polythought import serve

def predict(items):
    # items: the fields the Ask a model node sends, an object an item
    return [{"buy": model.predict(item) > item["price"]} for item in items]

serve(predict, key="pw_…", url="https://polythought.example.com", version="2026-11-24")
```

`serve` runs until you stop it: in a notebook cell, in a thread, or in a script on a box that
stays up. An exception in `predict` fails that request, saying why, and the worker carries
on. Run more than one for more throughput: any connected worker takes the next request.

In the workflow, **Ask a model** sends the run's items in one request, the fields you choose
or each item whole, and puts each answer on its item:

```json
{"prediction": {"answer": {"buy": true}, "model": "temps", "version": "2026-11-24", "worker": "gpu-box"}}
```

With no worker connected, it fails at once instead of waiting. Turn on **Continue on
failure** to send those items on through the error port: to a Telegram message, say.

A model can also answer at a URL, if you'd rather host it as a small web service: see
[Models](/docs/guides/models/).

## 3. Trading on it

The rest is a workflow like any other. Filter on the answer, size the order, place it:

**Schedule** → **Markets** → **Ask a model** → **If** `prediction.answer.buy` → **Place
order**

Run it on paper first: the workflow's trading page switches it to live when you're ready.
Live, it trades under your caps, your kill switch and the confirmations you set, as every
workflow does. A model's answers get no more trust than any other data.

## 4. Recording

Add a **Record** node where you want to remember what happened: after the model's answer,
or after the order. It adds a row for each item, the fields you choose or the whole item,
with `_at` for when, `_workflow` and `_execution` for which workflow and run, and `_mode`,
paper or live. Items pass on unchanged.

Its **Dataset** setting can make one there and then with **New dataset…**. A dataset made
there keeps 90 days of rows, and drops the oldest past the instance's most, so recording
never stops for want of room.

## 5. Reading it back

```python
seen = pt.datasets.frame("bets-seen")
orders = pt.orders_frame(workflow="0193…")

seen["buy"] = seen["prediction"].str.get("answer").str.get("buy")
seen["version"] = seen["prediction"].str.get("version")
seen.groupby("version")["buy"].mean()  # how often each version said buy
```

Join it with the orders and the markets' outcomes to see what each version's calls earned.
Then change the model, serve the new version, and watch the next days' rows: each says which
version answered.

## Agents

An agent beside your notebook, over MCP, can do the same: `list_datasets`,
`read_dataset_rows`, `create_dataset` and `append_dataset_rows`, and `list_models` with the
workers connected now. Writing takes the **Write datasets and models** scope. Rows hold what
was written into them, market questions included, and come with a notice that they are data
to weigh, never instructions. See [Agents over MCP](/docs/guides/agents-over-mcp/).

## When things go

- **Deleting a dataset or a model** switches off your running workflows that name it, and
  they say why. Switching one back on is refused until its node names one you have.
- **If an admin keeps datasets and models from you**, the same happens to every workflow
  using them.
- **A worker key revoked** on Data → Models is refused from its worker's next call.
