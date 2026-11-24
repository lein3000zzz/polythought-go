---
title: Models
description: Your own models, served by workers on your machines or at a URL, for workflows to ask.
---

A **model** is yours alone, by a name that never changes. Polythought runs none of it. A
model is served by **workers** on your own machines, a notebook or a GPU box, which call in
for its requests, or it answers at a **URL** you host. Workflows ask it about a batch of
items and it answers each one. The **Data → Models** page lists yours, with the workers
connected now, its last answer and its last error.

An admin decides who may use models, the same way as datasets (**Config → Models and
datasets → Who may use models and datasets**).

## Making one

Make one on **Data → Models** with **New model**, or with an API token that may **Write
datasets and models**:

```sh
curl -X POST -H 'Authorization: Bearer pt_…' -H 'Content-Type: application/json' \
  https://polythought.example.com/api/v1/models \
  -d '{"name": "temperature-model", "kind": "worker"}'
```

- **name** is lower-case letters, digits, `-` and `_`, up to 64 characters.
- **kind** is `worker` or `url`. A `url` model also takes `url` and, optionally,
  `credentialId`, an HTTP header credential sent with each call.

## Workers

A worker serves one model with one of its **worker keys**. Make keys on the Data page, under
the model's **Keys**. A key is shown once, when you make it, and serves that model alone:
it can't read your data or do anything else. Revoke one there and its workers are refused
from their next call.

A worker runs a loop of three calls, each with `Authorization: Bearer pw_…`:

1. `POST /api/v1/worker/next` with `{"worker": "gpu-box-1", "version": "1.2.0"}` asks for the
   next request. Polythought holds the call open until one comes, 50 seconds at most, then
   answers `204` with none. Call again either way. `worker` names this worker, unique among
   the model's. `version` is what each answer says it came from.
2. A request comes as `{"model": "…", "request": {"id": "…", "items": [{…}, …], "schema": {…},
   "deadline": "…"}}`. Each item is an object of the fields the workflow chose. `schema`, if
   there, is a JSON Schema each answer should keep to.
3. `POST /api/v1/worker/requests/{id}/answer` with `{"worker": "…", "version": "…", "answers":
   [{…}, …]}` answers it: one answer per item, in their order, any JSON. Or
   `POST /api/v1/worker/requests/{id}/fail` with `{"worker": "…", "reason": "…"}` fails it,
   and each of its items fails with the reason.

```sh
KEY=pw_…
while true; do
  curl -s -X POST -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
    https://polythought.example.com/api/v1/worker/next -d '{"worker": "laptop"}'
  # …answer the request it gave, if any
done
```

From Python, the SDK runs this loop for you. Your Polythought serves the SDK of its own
version:

```sh
pip install "polythought[pandas]" --find-links https://polythought.example.com/api/v1/sdk/python/
```

```python
from polythought import serve

def predict(items):
    # one answer per item, in their order
    return [{"high": model.predict(item)} for item in items]

serve(predict, key="pw_…", url="https://polythought.example.com", version="1.2.0")
```

An exception in `predict` fails that request, saying why, and the worker carries on.

A worker counts as connected while it calls for requests, and 30 seconds after, or while it
holds one. Any of a model's connected workers takes the next request. One that holds a
request a minute without answering loses it to another worker, and the first answer counts.
A model asked with no worker connected fails at once, rather than waiting.

## At a URL

A `url` model is called the way webhooks are, with the same guard on private addresses and
the same outbound proxy. Polythought POSTs
`{"model": "…", "items": [{…}, …], "schema": {…}}` and expects a `2xx` answer of
`{"answers": [{…}, …], "version": "…"}`, one answer per item.

## Trying one

`POST /api/v1/models/{name}/ask` asks a model the way a workflow does:

```sh
curl -X POST -H 'Authorization: Bearer pt_…' -H 'Content-Type: application/json' \
  https://polythought.example.com/api/v1/models/temperature-model/ask \
  -d '{"items": [{"city": "NYC"}], "waitSeconds": 30}'
```

It gives `{"answers": […], "version": "…", "worker": "…"}`. With a `schema`, the answers
that don't keep to it are listed under `refused`, each with why.

## In workflows

The **Ask a model** node, in the editor's **Data** group, asks one of your models about the
items it receives, all of them in one request a run. Each item sends the **Fields** you
choose, like `city` or `outcomes.0.price`, each under its name as written, or the whole item
if you choose none.

Each answer goes on its item under **Answer field**, `prediction` to begin with:

```json
{"answer": {"high": 51.25}, "model": "temperature-model", "version": "1.2.0", "worker": "gpu-box-1"}
```

- **Answer schema**, a JSON Schema, is passed to the model, and an answer that doesn't keep to
  it fails its item alone.
- **Wait** is how long the node waits for the answers: 30 seconds to begin with, at most what
  an admin allows.
- With no worker connected, no answer in time, or a worker's failure, every item fails, saying
  why. Turn on **Continue on failure** and they take the error port instead.

The model a node names is yours alone. Sharing a workflow, or copying its nodes, leaves it
out, the same as credentials.

## Deleting one, or losing them

Deleting a model takes its keys with it and switches off your running workflows that ask it,
and they say why. The Data page lists them before you confirm. Switching one back on is
refused while a node names a model you no longer have.

If an admin keeps models from you, Polythought switches off your running workflows that ask
one the same way, and a run of one fails at once.

## Limits

An admin sets them under **Config → Models and datasets**:

- 20 models a user;
- 10 workers connected to a model at once;
- 1,000 items and 1 MiB a request;
- 1 MiB an answer;
- 5 minutes the longest wait for an answer;
- 50 seconds a worker's call waits, and a minute a worker holds a request.
