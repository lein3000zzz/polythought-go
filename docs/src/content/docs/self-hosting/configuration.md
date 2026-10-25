---
title: Configuration
description: The Config page, the config file, environment variables, and restarting.
---

Most of Polythought is set on the **Config** page, by admins, while it runs. A few things
the server needs before it can reach its database are set in the environment or the config
file; those are listed in [Install](/docs/self-hosting/install/#environment-variables).

A value comes from the first of these that has one: an environment variable, the config
file, what an admin saved on the Config page, the default.

## The Config page

Open **Admin → Config**. It opens on an overview: whether Polymarket takes orders from the
server, and a card for each page with how many of its settings are saved or set by the
environment or the config file. A page holds a group of settings, like **Accounts**,
**Trading** or **Mail**, and the menu beside it lists them all; on a narrow screen it is a
dropdown above the page. Each setting says where its value comes from:

- **Default**: nobody changed it.
- **Saved**: an admin saved a value on this page. **Reset to default** removes it.
- **Set by `POLYTHOUGHT_…`**: an environment variable sets it. It cannot be changed here; see
  below.
- **Set in the config file**: the config file sets it. It cannot be changed here either.

Change what you need and press **Save**, which saves that page's changes. If a value is
wrong, it is marked and nothing is saved. Changes on other pages wait for their own Save: a
dot marks their page in the menu, and leaving Config with any unsaved asks first. Most
settings apply at once. The few that apply after a restart say so; once one of them changed,
a banner on every page names it and offers **Restart now**.

**Search settings**, above the menu, finds a setting on any page by its label, its key or
its description; press `/` to get there. Enter, or a click, opens the setting's page at it.
A link to a setting is its page with its key, like `/admin/config/mail#mail.port`.

**History**, at the bottom of the menu, lists every change: when, by whom, and the value
before and after. Secret values are never recorded, only that they changed.

All settings, with their defaults, are in the [settings reference](/docs/reference/settings/).

## The config file

A YAML file can hold the same values: the bootstrap ones and any setting, each under its key,
nested at its dots. `auth.registration` is:

```yaml
auth:
  registration: invite
```

The server reads `configs/polythought.yml` when it is there, or the file `--config` or
`POLYTHOUGHT_CONFIG` names, which must then exist: `polythought --config
/etc/polythought.yml serve`. `configs/polythought.example.yml` lists every value it may hold,
at its default, with what each is; copy it to start. A key the server does not know, or a
value that does not fit, stops it from starting, with a message naming it.

A setting the file sets is locked on the Config page, as one an environment variable sets,
which suits settings you manage with your deployment. An environment variable wins over the
file. Secrets, like the master keys and the admin's password, are better kept in the
environment than in a file.

With Docker, mount the file into the `api` container and point `POLYTHOUGHT_CONFIG` at it.

## Environment variables

Every setting also has an environment variable, named in the reference. A variable that is
set wins over the config file and the Config page, and locks the setting there. An empty
variable counts as not set.

With Docker Compose, `deployments/docker-compose.yml` passes some variables through from `.env`.
Add others under the `api` service's `environment` in a file of your own beside it, such as
`deployments/docker-compose.override.yml`, named with a second `-f`:

```sh
docker compose -f deployments/docker-compose.yml -f deployments/docker-compose.override.yml \
  --env-file .env --profile app up -d
```

The Compose file then stays as released, and the updater, which runs the stack with every
file it was started with, keeps yours.

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
proxy or a tunnel sits in front of `web`, every client looks like that proxy. Set
`POLYTHOUGHT_WEB_TRUSTED_PROXIES` in `.env` to the addresses it connects from, ranges
separated by spaces, such as `100.64.0.0/10` through Tailscale. `web` then passes on each
client's own address, as that proxy reported it. Leave it unset otherwise: anyone could then
say where they come from.
