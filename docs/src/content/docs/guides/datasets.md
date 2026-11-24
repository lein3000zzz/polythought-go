---
title: Datasets
description: Named tables of rows that notebooks publish and workflows read, or that workflows record and notebooks read.
---

A **dataset** is a named table of JSON rows, yours alone. A notebook publishes one, such as
a model's predictions by city, and workflows read it. Or workflows record into one, and a
notebook reads what they saw. The **Data** page lists yours: their rows and size, a look at
the first rows, a CSV of all of them, and Delete.

An admin decides who may use datasets (**Config → Models and datasets → Who may use models
and datasets**): everyone, as on a new instance, admins and the users switched on for them on
the Users page, or no one.

## Making one

A notebook makes one with an API token that may **Write datasets and models**. Reading them takes
**Read** alone. Make the token under **Settings → API tokens**.

```sh
curl -X POST -H 'Authorization: Bearer pt_…' -H 'Content-Type: application/json' \
  https://polythought.example.com/api/v1/datasets \
  -d '{"name": "forecasts", "keyField": "city"}'
```

- **name** is lower-case letters, digits, `-` and `_`, up to 64 characters. It is unique
  among yours and never changes, since nodes and notebooks name it.
- **keyField**, if given, is the field rows are looked up by, such as `city`: every row then
  needs it.
- **keepDays** drops rows older than so many days, and **keepRows** keeps no more than so many,
  the oldest dropped first. Both are 0 to begin with, which keeps everything.
  `PUT /api/v1/datasets/{name}/keep` changes them.

From Python, the SDK does the same, and reads and writes pandas tables:

```python
from polythought import Client

pt = Client("https://polythought.example.com", token="pt_…")
pt.datasets.create("forecasts", key_field="city")
pt.datasets.replace_frame("forecasts", predictions)
seen = pt.datasets.frame("bets-seen")
```

Install it from your Polythought:
`pip install "polythought[pandas]" --find-links https://polythought.example.com/api/v1/sdk/python/`.

## Rows

Every call is under `/api/v1/datasets/{name}`.

| Call | What it does |
|---|---|
| `POST …/rows` with `{"rows": [{…}, …]}` | Adds rows. Past **keepRows**, the oldest go; with no keepRows, rows past the instance's most are refused. |
| `GET …/rows?after=0&limit=1000` | A page of rows, oldest first, with `next` to pass as `after` for the page after. |
| `GET …/rows/{key}` | The row last added with that key, in a dataset with a key field. |
| `GET …/csv` | Every row as CSV: a column for each field, an object or list as its JSON. |
| `DELETE /api/v1/datasets/{name}` | The dataset and its rows, for good, and your workflows on naming it switched off. |

A row is a JSON object. Its numbers stay as written: `0.53` is `0.53`.

## Replacing them at once

A notebook publishing a fresh table replaces the old one whole. Workflows reading it never
see it half-written:

1. `POST …/replace` starts a replace and gives its `stage`.
2. `POST …/replace/{stage}/rows` sends rows to it, as many times as it takes: a request is
   at most 1 MiB.
3. `POST …/replace/{stage}/finish` puts them in, at once, the old rows gone.

Until the finish, the dataset is as it was. `DELETE …/replace/{stage}` drops a replace, and one
left unfinished a day goes by itself.

## In workflows

Two nodes in the editor's **Data** group use them.

**Record** adds a row for each item it receives. The row holds the fields you choose, like
`city` or `outcomes.0.price`, each under its name as written, or the whole item if you
choose none. Every row also gets `_at` for when, `_workflow` and `_execution` for which
workflow and run, and `_mode`, which is `paper` or `live`. Items pass on unchanged.

A row the dataset refuses, too big or missing its key field, fails only its own item. A full
dataset refuses them all. Turn on **Continue on failure** and those items take the error
port, so the rest of the workflow carries on.

**Read dataset** reads one of two ways:

- **By key** finds, for each item, the row last added whose key is the item's **Key**, like
  `{{ city }}`, and puts it on the item under **Row field**, `row` to begin with. An item
  with no row goes out of `missing`.
- **Every row** sends the newest rows, 1,000 at most, an item each, with the row under
  **Row field**.

The **Dataset** setting picks one of yours, or **New dataset…** makes one there and then. A
dataset made there keeps 90 days of rows, as your admin sets, and as many rows as a dataset
may hold, dropping the oldest past that. Recording never stops for want of room. You can
change both as you make it.

The dataset a node names is yours alone. Sharing a workflow, or copying its nodes, leaves it
out, the same as credentials.

### Deleting one, or losing them

Deleting a dataset switches off your running workflows that name it, and they say why. The
Data page lists them before you confirm. Switching one back on is refused while a node names
a dataset you no longer have.

If an admin keeps datasets from you, Polythought switches off your running workflows that
name one the same way, and a run of one fails at once.

## Limits

An admin sets them under **Config → Models and datasets**:

- 100 datasets a user;
- 100,000 rows a dataset;
- 16 KiB a row;
- 100 MiB a user, rows waiting in a replace counted;
- 90 days kept by a dataset made in the editor, to begin with.

**Admin → Users** shows what each user's datasets take.
