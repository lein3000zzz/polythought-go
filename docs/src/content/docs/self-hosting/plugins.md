---
title: Plugins
description: Install, approve and run plugins, which bring nodes, triggers and credential types of their own.
---

A plugin is a program that brings Polythought new nodes, triggers, credential types and
scenarios. It runs in a process of its own, beside the server, and reaches nothing but what
the server lets it: the network only at the addresses it names, and Telegram, the owners'
wallets and orders only by permissions you approve. On Linux that process runs in a sandbox.

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

## The sandbox

On Linux 6.2 or later, the server starts each plugin's process in a sandbox built from
Landlock and seccomp. There is nothing to set up, in Docker or out. In the sandbox a plugin:

- reads its own version's files, and runs no program but its own;
- writes only its own directory and a temporary one, which the server empties when it stops;
- opens no network connection, so every request goes through the server and its permissions;
- starts no other process, and cannot signal or trace the server or anything else;
- keeps at most 1024 files open.

Its card says **Sandboxed**.

Where the server has no sandbox, plugins do not start, and the Plugins page says why. That is
so on a Mac, and on a Linux kernel older than 6.2 or built without Landlock. **Unsandboxed
plugins** on the Config page lets them start anyway. A plugin started that way reaches whatever
the server can: its files, its network, its other processes. Turn it on only for plugins you
would run yourself. While it is on, the Plugins page warns of it and each card says **Not
sandboxed**. It applies from each plugin's next start.

## Restarting

Approving, enabling, disabling and uninstalling take effect when the server restarts: a banner
says what waits for it, with **Restart now**, as on the Config page. The server then loads each
enabled plugin's approved version, checking its files again.

When the server starts, the active workflows using a plugin that is disabled or uninstalled are
paused, and their owners told on Telegram and by email.

## Who may use a plugin

Everyone may use a plugin, unless you choose otherwise: **Who may use it** picks the users who
may, admins included. This applies at once: the active workflows of anyone who may no longer use
it are paused, and they are told. Nobody else sees its nodes in the editor, nor can save or run a
workflow with them.

## Its settings

A plugin may have settings of its own, a key for the service it talks to, say. Once a version
of it is approved, **Settings** on its card sets them: kept as the Config page's are, secret
ones sealed and never shown again, every change in its **History**, the old and new value but
a secret's. Its node runs get them from the next one; a trigger, as it starts.

## When a plugin crashes

A plugin's process that stops is started again after a second, then after longer and longer
waits, up to 5 minutes. After 5 crashes in 10 minutes it is stopped and marked **Crashed**, and
admins are told. Its card shows its last error and the last lines of its log; **Start again**
starts it once it is fixed. One that would not start at all says so the same way.

While a plugin is not running, its nodes fail, saying so.

## What a plugin may take

Each plugin has a directory of its own, which it keeps across restarts and upgrades, in the
plugins directory under `.data/<its ID>`. Its card shows two bars, the memory its process takes
and its data on disk, each against its cap. A bar turns amber at 80% of its cap and red at 95%.

| Setting | Default | Past it |
| --- | --- | --- |
| **Plugin data cap (MiB)** | 1024 | The server stops the plugin, marks it **Crashed** and tells admins. The cap counts its own directory and its temporary one together. |
| **Plugin memory cap (MiB)** | 256 | The server kills its process. That counts as a crash, so it starts again after a wait. A plugin written in Go also gets a soft limit a little under the cap, which its garbage collector keeps to. |

The server measures a plugin's memory twice a second, on Linux only, and its data every 30
seconds. **Caps** on a plugin's card sets caps for that plugin alone. Leave a field blank for
the Config page's. A new cap holds from the next measurement, and a Go plugin's soft limit from
its next start.

A plugin stopped for its data stays stopped while its data is past its cap: after **Start
again**, the server stops it at once. Raise its cap first.

## What a plugin may ask of the server

A node's run of a plugin may ask the server for something, a fetch, a market search, its store,
up to **Data requests per run** times, the setting code shares; a trigger as often each minute.
A fetch takes at most **Plugin fetch time** and **Plugin fetch size (KiB)**; each node keeps at
most **Plugin store per node (KiB)**. All of these are on the Config page.

## Uninstalling

**Uninstall** on a plugin's card removes its files at once. It runs until the server restarts.
Its data stays unless you tick **Also remove its data**: its own directory, and what its nodes
stored in workflows. A reinstall finds the data it kept.

What uninstalled plugins kept is listed under **Data left behind** on the Plugins page, with
what it takes. **Remove** there deletes it for good.

**History**, on a plugin's card, lists what admins did with it, kept after it is uninstalled.
