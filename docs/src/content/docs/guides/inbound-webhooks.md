---
title: Inbound webhooks
description: A workflow started by any service that calls its secret address.
---

The **Inbound webhook** trigger starts a workflow when another service calls it: an alert
from a charting site, a message from your own script, a form sent somewhere. Each call is a
run, its body and query on the item.

Inbound webhooks are off on a new instance. An admin turns them on under **Config →
Workflows → Inbound webhooks**.

## Its address

1. Add an **Inbound webhook** trigger, under **Triggers**, and save the workflow.
2. Select the trigger: its **Address** is in its settings, like
   `https://polythought.example.com/hooks/k3Jx…`. **Copy** it into the service that calls.
3. Switch the workflow on. Until then, a call gets `409`.

The address is secret: whoever has it can start the workflow. It shows only to you, in the
editor; sharing the workflow, or an admin looking at it, leaves it out, and no API token or
agent reads it. Should it leak, **Renew the address**: the old one answers `404` from then
on, and the service needs the new one.

## A call

A call is a `POST` to the address:

```sh
curl -X POST 'https://polythought.example.com/hooks/k3Jx…?city=nyc' \
  -H 'Content-Type: application/json' \
  -d '{"temperature": 71.5, "station": "KLGA"}'
```

It is answered at once, `202` with the run's ID, `{"executionId": "…"}`, before the run
starts. The item the trigger sends on holds:

| Field | What it holds |
|---|---|
| `body` | The body: what its JSON holds, read, numbers exact; text that is not JSON, as text; `null` for none. |
| `query` | The query, by name: `?city=nyc` is `query.city`; a name given twice, a list. |
| `receivedAt` | When the call came. |

So `{{ body.temperature }}` is `71.5`, and `{{ query.city }}` is `nyc`. A body is at most
1 MiB.

## Limits

- Calls take their turn: a run starts once the one before it ends. While 50 wait, the next
  call gets `429`.
- A hook takes 60 calls a minute unless an admin sets another number (**Calls a minute per
  inbound webhook**); past it, `429`. The runs count toward **Runs per workflow per hour**
  too, past which the workflow is switched off, as any trigger's are.
- An address that calls 10 addresses that are not hooks, and more after that than one
  every 30 seconds, is refused for a minute, its right ones too.
- Admins switching inbound webhooks off make every address answer `503`.

| Answer | Why |
|---|---|
| `202` | Taken: the run's ID is in it. |
| `400` | The body is neither JSON nor text. |
| `404` | No such address, or it was renewed. |
| `409` | The workflow is not on. |
| `429` | Too many calls: this minute's, those waiting, or wrong addresses from where it came. |
| `503` | Inbound webhooks are off on this instance. |
