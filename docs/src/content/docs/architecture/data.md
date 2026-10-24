---
title: Data and storage
description: What Polythought keeps in Postgres, what it seals and with which keys, and how long it keeps things.
---

Everything Polythought keeps is in one Postgres database, except plugins' files and data,
which are on disk. There is no cache server and no queue: one server, one database.

## Postgres

The server talks to Postgres through pgx's pool. Queries are written with
[go-jet](https://github.com/go-jet/jet), whose types are generated from the schema, so a
query naming a column that is gone does not compile. The schema is built by migrations,
embedded in the binary and run with goose when the server starts, unless
`POLYTHOUGHT_AUTO_MIGRATE` says not to. Work that must happen together runs in one
transaction, which nested calls join.

What the server needs before it can reach its database, its address and its keys, comes from
the environment or a config file. Everything else is a setting on the Config page, kept in
the database; for each, the environment wins over the file, the file over the database, and
the database over the default.

## Sealed

Secrets are sealed before they reach the database, with envelope encryption: each secret gets
its own AES-256-GCM key, which a master key wraps. The master keys come from
`POLYTHOUGHT_SECRET_KEYS`, as `id:key` pairs: the first seals, the others only open, which is
how keys rotate. Each sealed value is bound to where it belongs, so a value copied into
another row does not open:

| Sealed | Bound to |
| --- | --- |
| Secret settings (Telegram's token, mail's password, the Builder key) | the setting |
| Users' credentials | the credential |
| A wallet's Polymarket API key, secret and passphrase, and an imported private key | the wallet and field |
| Session keys | the wallet and signer |
| Invite codes | the invite |
| Plugins' secret settings | the plugin and setting |

Secrets never come back out of the API: a page can replace or clear one, not read it. A
signing key is opened in memory only to sign an order, by the trading service.

Passwords are hashed with Argon2id (19 MiB, two passes). Sessions, API tokens, agents' OAuth
codes and tokens, mailed links and invite codes are random 256-bit secrets kept only as their
SHA-256 hash, so the database alone signs nobody in.

## Kept, and for how long

| What | Kept |
| --- | --- |
| Finished executions | 30 days by default, pruned daily |
| Waiting executions | until they finish, at most 7 days |
| Sessions | 7 days idle, 30 in all, pruned hourly |
| Accounts that never verified | 7 days |
| Requests to join never confirmed | a day after their link stopped working |
| Agents' OAuth codes and tokens | until they end (a code 10 minutes, an access token an hour, a refresh token 30 days), pruned hourly |
| Agents that registered themselves and were never allowed | a day |
| Agents' grants | always, revoked ones too, one for each user and agent |
| Agents' sessions on `/mcp` | while open; one open as the server restarted until its agent opens another, or an hour without a call |
| Tasks for agents | with their waiting execution: until answered, failed, or the node's timeout |
| Agents' and tokens' confirmations | always, what was asked and how it was decided |
| What nodes remember | each value until its expiry, swept every ten minutes |
| Orders, fills and holdings | always, including those of deleted workflows' live trading |
| The Config page's history, plugins' audit | always |

Plugins' files are under the plugins directory, a version each; their lasting data under
`.data/<id>`, capped at 1 GiB unless an admin says; their temporary directory is emptied when
they stop.

## Backups

A backup is a `pg_dump` of the database and, kept apart from it, `POLYTHOUGHT_SECRET_KEYS`.
Without the keys, the sealed values cannot be read; with both, they can, imported private
keys included, so the two are guarded like the keys themselves. The schema migrates forward
when a new version starts, so back up before upgrading.
