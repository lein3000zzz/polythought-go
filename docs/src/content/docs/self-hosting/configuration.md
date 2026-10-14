---
title: Configuration
description: The Config page, environment variables, and restarting.
---

Most of Polythought is set on the **Config** page, by admins, while it runs. A few things
the server needs before it can reach its database are set only in the environment; those
are listed in [Install](/docs/self-hosting/install/#environment-variables).

## The Config page

Open **Admin → Config**. Settings are grouped by what they are about, and each says where its
value comes from:

- **Default**: nobody changed it.
- **Saved**: an admin saved a value on this page. **Reset to default** removes it.
- **Set by `POLYTHOUGHT_…`**: an environment variable sets it. It cannot be changed here; see
  below.

Change what you need and press **Save**. If a value is wrong, it is marked and nothing is
saved. Most settings apply at once. The few that apply after a restart say so; once one of
them changed, a banner offers **Restart now**.

The **History** below lists every change: when, by whom, and the value before and after.
Secret values are never recorded, only that they changed.

All settings, with their defaults, are in the [settings reference](/docs/reference/settings/).

## Environment variables

Every setting also has an environment variable, named in the reference. A variable that is
set wins over the Config page and locks the setting there, which suits settings you manage
with your deployment. An empty variable counts as not set.

With Docker Compose, `deploy/docker-compose.yml` passes some variables through from `.env`;
add others under the `api` service's `environment`.

A variable with a bad value stops the server from starting, with a message naming it. A
value saved on the Config page that no longer fits, say after an upgrade made a rule
stricter, is ignored with a warning in the log, and the default applies until an admin fixes
it.

## Restarting

**Restart now** stops the server gracefully and starts it again, which takes a few seconds.
Active workflows start again by themselves; runs in progress are cut short. Signed-in users
stay signed in. Restarting the `api` container does the same.

## Client addresses

Sign-in attempts are limited per client address. The API learns a client's address from the
connection, or from `X-Forwarded-For` when the connection comes from a **trusted proxy**. With
Docker Compose, the `web` container is that proxy, and its network is trusted. If another
proxy sits in front of `web`, every client looks like that proxy; set `trusted_proxies` in
Caddy's global options so the client's own address is passed on.
