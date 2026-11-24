---
title: Datasets
description: Named tables of rows that notebooks publish and workflows read, or the other way round.
---

A **dataset** is a named table of JSON rows, yours alone. A notebook publishes one, such as
a model's predictions by city, and workflows read it. Or workflows record into one, and a
notebook reads what they saw. The **Data** page lists yours: their rows and size, a look at
the first rows, a CSV of all of them, and Delete.

An admin decides who may use datasets (**Config → Models and datasets → Who may use models
and datasets**): everyone, as on a new instance, admins and the users switched on for them on
the Users page, or no one.

## Making one

A notebook makes one with an API token that may **Write datasets**. Reading them takes
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

## Rows

Every call is under `/api/v1/datasets/{name}`.

| Call | What it does |
|---|---|
| `POST …/rows` with `{"rows": [{…}, …]}` | Adds rows. Past **keepRows**, the oldest go; with no keepRows, rows past the instance's most are refused. |
| `GET …/rows?after=0&limit=1000` | A page of rows, oldest first, with `next` to pass as `after` for the page after. |
| `GET …/rows/{key}` | The row last added with that key, in a dataset with a key field. |
| `GET …/csv` | Every row as CSV: a column for each field, an object or list as its JSON. |
| `DELETE /api/v1/datasets/{name}` | The dataset and its rows, for good. |

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

## Limits

An admin sets them under **Config → Models and datasets**:

- 100 datasets a user;
- 100,000 rows a dataset;
- 16 KiB a row;
- 100 MiB a user, rows waiting in a replace counted.

**Admin → Users** shows what each user's datasets take.
