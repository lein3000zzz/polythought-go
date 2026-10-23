---
title: Observability
description: What the server logs, the metrics and traces it keeps, and what leaves it today.
---

The server reports through [the-watchers](https://github.com/lein3000zzz/the-watchers), a
small library with one interface each for logs, metrics and traces, and adapters to slog,
Prometheus and OpenTelemetry behind them. Every client and adapter in the server, the
Polymarket clients, the model providers, the webhook sender, Telegram, the code engine, the
plugin host, uses it the same way.

## Logs

Logs go to standard output, as text or JSON (**Log format**, applied on restart), at the
level **Log level** says, applied at once. Each line carries the trace it belongs to, when
there is one. A request's line holds its method, its route template, its status, how long it
took and the client's address: the template, `/api/v1/workflows/{id}`, never the path, which
might hold an ID or a token. Secrets never reach the log: errors from Telegram and the model
providers have their tokens and keys scrubbed out, and a webhook's error names only its host.

## Metrics

Each part of the server times its operations the same way: how long each took, how many,
how many ended in error, how many are in flight, by operation. The HTTP API, webhooks, the
model providers, the compiler and the code engine, the code runners, the plugin host,
executions and node runs, Telegram, GitHub and each Polymarket client (CLOB, Data, Gamma,
Polygon's RPC, the Relayer, the geoblock check) have theirs; the market and trader feeds and
the live events hub count what they drop and how often they reconnect. A plugin's own metrics
are prefixed with its ID, at most a thousand label sets each.

The server keeps these in Prometheus's registry, but does not serve them yet: it has no
metrics endpoint, only `/healthz`.

## Traces

A request that carries a `traceparent` header continues its trace, and every request gets a
span, as does each operation the metrics time. Calls to GitHub and to Polymarket over HTTP and
WebSocket are traced with their requests; webhooks, plugins' fetches, model calls, Polygon's
RPC and Telegram are not, as a whole URL in a span may carry a token.

The spans are made, but nothing exports them yet: the server sets up no OpenTelemetry
exporter, so they go nowhere until one is.

## Health

`/healthz` answers while the server runs, for Docker's health check and a load balancer's.
What it does not show, the Config page does: Polymarket's answer for the server's address,
whether code runs in runners and whether they are sandboxed, plugins' state, a restart
waiting for settings that need one.
