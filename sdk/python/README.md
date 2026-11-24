# Polythought for Python

Your notebook and Polythought, both ways. Read Polythought's data and Polymarket's as tables,
publish datasets your workflows read, read what they recorded, and serve a model your
workflows ask. Polythought runs none of your code: your notebook calls out to it.

Python 3.10 or newer. The docs' **Notebooks and models** guide walks the whole loop, from a
notebook's predictions to the bets a workflow placed on them.

## Install

Each Polythought serves the SDK of its own version:

```sh
pip install "polythought[pandas]" --find-links https://polythought.example.com/api/v1/sdk/python/
```

`[pandas]` adds tables as DataFrames. Leave it out for plain lists of dicts.

## Reading

Make an API token under **Settings → API tokens**. Reading takes **Read**. Writing datasets
and asking models takes **Write datasets and models**.

```python
from polythought import Client

pt = Client("https://polythought.example.com", token="pt_…")
# or set POLYTHOUGHT_URL and POLYTHOUGHT_TOKEN, and: pt = Client()

pt.workflows()  # your workflows
pt.executions("0193…", limit=20)  # a workflow's runs
pt.orders_frame(workflow="0193…")  # its orders, as a table

pt.market.search("highest temperature in NYC")
pt.market.history_frame("7155…", window="1w")  # a token's prices, indexed by time
pt.market.books(["7155…"], depth=5)
```

## Datasets

```python
pt.datasets.create("forecasts", key_field="city")
pt.datasets.replace_frame("forecasts", predictions)  # every row at once
pt.datasets.append("forecasts", [{"city": "NYC", "high": 51.2}])

seen = pt.datasets.frame("bets-seen")  # what a Record node wrote
pt.datasets.row("forecasts", "NYC")  # the row last added with that key
```

A replace is staged, then swapped in at once. Workflows reading the dataset never see it
half-written, and a failure part way leaves it as it was.

## Serving a model

Make a model on **Data → Models**, and a worker key for it. Then serve it:

```python
from polythought import serve


def predict(items):
    # items: the fields the workflow's Ask a model node sends, an object an item
    return [{"high": model.predict(item)} for item in items]


serve(predict, key="pw_…", url="https://polythought.example.com", version="1.2.0")
```

- `serve` calls Polythought for the model's next request, waits for one, and answers it with
  `predict`'s answers, one per item, in order.
- An exception in `predict` fails that request, saying why, and the worker carries on.
- With `frames=True`, `predict` gets a DataFrame, and gives a DataFrame, a Series or a list.
- `POLYTHOUGHT_WORKER_KEY` and `POLYTHOUGHT_URL` stand in for `key` and `url`.

Run as many workers as the model may have: any of them takes the next request.

## Errors

A call Polythought refuses raises `PolythoughtError`, with its `status`, its `code`, like
`not_found` or `data_off`, and its `message`.
