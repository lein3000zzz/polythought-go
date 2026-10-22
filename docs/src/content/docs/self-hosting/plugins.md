---
title: Plugins
description: Install, approve and run plugins, which bring nodes, triggers and credential types of their own.
---

A plugin is a program that brings Polythought new nodes, triggers, credential types and
scenarios. It runs in a process of its own, beside the server, and reaches nothing but what
the server lets it: the network only at the addresses it names, and Telegram, the owners'
wallets and orders only by permissions you approve.

Only admins install plugins. Nothing of a plugin runs until you approve what it asks for and
the server restarts.

## Installing

Open **Admin → Plugins**. Under **Install a plugin**, pick its archive, a `.tar.gz`, and press
**Install**, or give the address of one under **Or from a URL**: `https` only, never an address
inside the server's network unless **Private addresses** on the Config page allows it.

The archive is checked whole before anything of it is kept: its files against the checksums it
carries, its signature, if it has one, against the keys you trust, and its manifest. One that is
not as it should be is refused, saying why. Archives may be up to **Largest archive (MB)** on
the Config page, 100 MB unless you change it.

Installed plugins are kept in the plugins directory, `plugins` beside the server unless
**Plugins directory** says otherwise; in Docker it is a volume, so they outlive the container.
What admins decide about them is kept in the database.

## Signatures

A plugin's author may sign its archive with [minisign](https://jedisct1.github.io/minisign/).
List the public keys you trust under **Trusted signing keys** on the Config page, one a line:
the key's line from its `.pub` file, after a name and a space if you like:

```text
Polythought RWQf6LRCGA9i53mlYecO4IzT51TGPpvWucNSCh1CBM0QTaLn73Y7GFO3
```

A plugin one of them signed shows who did. One signed by a key you do not trust, or not signed
at all, installs too, with a warning you tick before approving it. One whose signature by a
trusted key does not hold is refused: it is not what it says it is.

## Approving

Once it is installed, the permissions screen opens: what the plugin brings, who signed it, and
each permission it asks for, in words. Tick each one, and the warning when nobody you trust
signed it, then **Approve**.

| Permission | What it lets the plugin do |
| --- | --- |
| `fetch:api.example.com` | Send requests to that host, on any port, and read the answers. |
| `fetch:*` | Send requests to any public address. |
| `telegram` | Message the owners of the workflows using it in their linked chat, after its name, and ask them questions their workflows wait on. |
| `wallets` | See the names and addresses of those owners' wallets. |
| `orders` | Place and cancel orders for the workflows using its trading nodes, on paper or live as each trades, within the caps and the kill switch. |

`fetch:*`, `wallets` and `orders` are marked **Take care**. Every plugin may, without asking,
read Polymarket's public data, keep data for its nodes that say they remember, and read the
credentials people pick in its nodes' settings.

An upgrade, a newer version of a plugin installed, waits beside the version that runs, which
keeps running with what you approved, until you review it; the permissions it asks for that are
new are marked. **Remove** drops a version you do not want.

## Restarting

Approving, enabling, disabling and uninstalling take effect when the server restarts: a banner
says what waits for it, with **Restart now**, as on the Config page. The server then loads each
enabled plugin's approved version, checking its files again.

When the server starts, the active workflows using a plugin that is disabled or uninstalled are
paused, and their owners told on Telegram and by email.

## Who may use a plugin

Everyone may use a plugin, unless you choose otherwise: **Who may use it** picks the users who
may, admins included. This applies at once: the active workflows of anyone who may no longer use
it are paused, and they are told. Nobody else can save or run a workflow with its nodes.

## When a plugin crashes

A plugin's process that stops is started again after a second, then after longer and longer
waits, up to 5 minutes. After 5 crashes in 10 minutes it is stopped and marked **Crashed**, and
admins are told. Its card shows its last error and the last lines of its log; **Start again**
starts it once it is fixed. One that would not start at all says so the same way.

While a plugin is not running, its nodes fail, saying so.

## What a plugin may ask of the server

A node's run of a plugin may ask the server for something, a fetch, a market search, its store,
up to **Data requests per run** times, the setting code shares; a trigger as often each minute.
A fetch takes at most **Plugin fetch time** and **Plugin fetch size (KiB)**; each node keeps at
most **Plugin store per node (KiB)**. All of these are on the Config page.

**History**, on a plugin's card, lists what admins did with it, kept after it is uninstalled.
