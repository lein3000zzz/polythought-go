---
title: Install
description: Run Polythought with Docker.
---

Polythought is three containers: PostgreSQL, the API server, and a web server (Caddy) that
serves the app and these docs and passes `/api`, `/mcp` and the OAuth metadata under
`/.well-known` on to the API. Docker Compose runs them together.

## What you need

- Docker with Compose.
- `openssl`, to make a key below, and `make` to build from the code.

Each release is three images on GitHub's container registry, for `linux/amd64` and
`linux/arm64`: `ghcr.io/lein3000zzz/polythought-api`, `ghcr.io/lein3000zzz/polythought-web`
and, for [updates](#the-updater) from the Config page, `ghcr.io/lein3000zzz/polythought-updater`.
A version such as `1.2.3` is tagged `1.2.3`, `1.2` and `latest`; a pre-release such as
`0.1.0-beta.1` only itself.

## Start

1. Get the code and, in its folder, copy the settings file:

   ```sh
   cp .env.example .env
   ```

2. Fill in `.env`:
   - `POLYTHOUGHT_SECRET_KEYS`: the key that encrypts stored secrets. Make one with
     `echo "k1:$(openssl rand -base64 32)"`. Keep it safe; see [Keys](#keys).
   - `POLYTHOUGHT_ADMIN_EMAIL` and `POLYTHOUGHT_ADMIN_PASSWORD`: the first admin, created when
     the server starts and there is no admin yet. The password needs at least 12 characters.

3. Start everything, with the released images:

   ```sh
   docker compose -f deployments/docker-compose.yml --env-file .env --profile app up -d
   ```

   `POLYTHOUGHT_VERSION` in `.env` picks the version, such as `1.2.3` or `1.2`; `latest`
   unless set. Or build the images from the code you have, which takes a few minutes the
   first time: `make up`, which `Ctrl+C` stops. Either way, open <http://localhost:3000> and
   sign in as the admin. `make down` removes the containers; your data stays in a Docker
   volume.

Everything else, such as who may sign up, is set by admins on the **Config** page; see
[Configuration](/docs/self-hosting/configuration/).

## On a server

The Compose file in `deployments/` suits a single machine as it is. On a server, `.env` sets
what changes, and the file stays as released. Before others use it:

- **Address.** Set `POLYTHOUGHT_PUBLIC_URL` to the address people use, such as
  `https://polythought.example.com`. Links in mail point there, only that origin may make
  changes with a signed-in session, and an `https` address makes the session cookie secure,
  named `__Host-polythought_session` so no other site of the domain can set it.
- **HTTPS.** Caddy, in the `web` container, gets a certificate itself, or a proxy or a tunnel
  in front of it does TLS:
  - Caddy: set `POLYTHOUGHT_DOMAIN` to the domain, and publish `web` on ports 80 and 443 with
    `POLYTHOUGHT_HTTP_PORT` and `POLYTHOUGHT_HTTPS_PORT`. The domain must point at the server
    and port 80 be open, for Let's Encrypt to check it. The certificates stay in a volume,
    across updates.
  - A proxy or a tunnel, such as Cloudflare Tunnel: leave `POLYTHOUGHT_DOMAIN` unset and
    point it at `web`'s port, 3000 unless `POLYTHOUGHT_HTTP_PORT` says otherwise. Set
    `POLYTHOUGHT_WEB_TRUSTED_PROXIES` to the addresses it connects from, such as
    `100.64.0.0/10` through Tailscale, or every client looks like the proxy; see
    [Client addresses](/docs/self-hosting/configuration/#client-addresses).
- **Database password.** Set `POSTGRES_PASSWORD` before the first start: hex, such as from
  `openssl rand -hex 24`, as it goes in the API's database URL. The database keeps the
  password it began with, so changing it later takes an `ALTER USER` in Postgres too.
- **Sign-up.** Registration is by invite unless an admin changes it.

With Caddy getting the certificate, `.env` has, beside the keys and the admin:

```sh
POLYTHOUGHT_PUBLIC_URL='https://polythought.example.com'
POLYTHOUGHT_DOMAIN='polythought.example.com'
POLYTHOUGHT_HTTP_PORT='80'
POLYTHOUGHT_HTTPS_PORT='443'
POSTGRES_PASSWORD='…'
```

The containers start again with Docker, after a reboot too, unless they were stopped. More
variables go in a file of your own beside the Compose file; see
[Configuration](/docs/self-hosting/configuration/#environment-variables).

## Environment variables

These are read when the server starts and cannot be changed on the Config page. Each can
also go in the [config file](/docs/self-hosting/configuration/#the-config-file), under the
key in brackets; the variable wins.

| Variable | Default | What it is |
|---|---|---|
| `POLYTHOUGHT_DATABASE_URL` (`database.url`) | required | PostgreSQL connection URL, e.g. `postgres://user:pass@host:5432/db?sslmode=disable`. |
| `POLYTHOUGHT_SECRET_KEYS` (`secrets.keys`) | required | Keys for secrets at rest, `id:base64key`, comma-separated; see [Keys](#keys). |
| `POLYTHOUGHT_AUTO_MIGRATE` (`database.auto_migrate`) | `true` | Update the database schema when the server starts. |
| `POLYTHOUGHT_HTTP_ADDR` (`http.addr`) | `:8080` | Where the API listens. |
| `POLYTHOUGHT_PUBLIC_URL` (`http.public_url`) | `http://localhost:8080` | The address people use; see above. |
| `POLYTHOUGHT_ADMIN_EMAIL`, `POLYTHOUGHT_ADMIN_PASSWORD` (`admin.email`, `admin.password`) | none | The first admin, created when there is no admin. Set both or neither. |
| `POLYTHOUGHT_CONFIG` | `configs/polythought.yml`, if there | The config file; `--config` does the same. |

Every other setting can also be set by an environment variable or in the config file, which
then locks it on the Config page; see [Configuration](/docs/self-hosting/configuration/).

## Keys

`POLYTHOUGHT_SECRET_KEYS` encrypts wallet API credentials, private keys and session keys,
[credentials](/docs/guides/credentials/) and secret settings. Back it up apart from the
database: without it they cannot be read, and with it and the database, imported keys can
be used. Whether users may import keys at all is **Private keys** on the Config page: auto,
the default, takes them unless anyone may sign up.

Session keys, for Deposit Wallets, need your Polymarket Builder API key, from
polymarket.com/settings?tab=builder: **Builder API key**, **Builder secret** and **Builder
passphrase** under Trading on the Config page. Users are offered session keys once all three
are in. Polymarket must enable the key for session keys first (builder@polymarket.com), and
gives unverified builders 100 Relayer transactions a day: each authorisation and each
revocation is one, and so is each setup of a wallet's trading approvals. The server reaches
Polymarket's Relayer at relayer-v2.polymarket.com.

Before it grants a wallet's approvals, the server reads them on chain through **Polygon
RPC** (Trading): https://polygon.drpc.org, the endpoint Polymarket's client uses, unless you
set another, such as your own node or a provider's URL. It is kept secret, as such URLs often
hold a key.

To change keys, put the new one first and keep the old one after it, as in
`k2:…,k1:…`. New secrets use the first key; the others only read what was saved with them.
Saved secrets keep the key they were saved with, so keep an old key listed as long as
anything saved with it remains.

## Live trading

Workflows trade on paper until an admin turns on **Live trading** under Trading on the
Config page: then an owner can switch a workflow to live in its Trading view, confirming
it, and its orders go to Polymarket from one of their wallets, spending its pUSD (see
[Trading live](/docs/guides/copy-traders/#trading-live)). Turned off again, workflows
already switched keep their switch, but their orders are refused, and every live order
resting on the book is cancelled, after the page asks. Like every setting, it can be fixed
in the environment instead, `POLYTHOUGHT_TRADING_LIVE=false`, which the Config page then
shows locked.

**Max $ a live order** and **Max $ a day of live buys**, also under Trading, cap what each
user's live buys may spend: $100 an order and $1,000 in 24 hours to start with, 0 for no
cap. The day counts what a user's live buys spent and what their resting ones may still
spend, all their workflows together; a buy over either cap is refused before it is sent.
On the Users page, **Live caps** gives a user caps of their own, over these, empty for the
instance's. Disabling a user turns their kill switch on, cancelling their resting live
orders (see [Stopping it](/docs/guides/copy-traders/#stopping-it)).

## AI agents

Users' [agent nodes](/docs/guides/agents/) call language models with their own keys, so their
calls are billed to them, not to you. Under **AI agents** on the Config page, **Max $ a
month of agent spend**, **Max tokens a month**, **Max input tokens a month** and **Max
output tokens a month** cap what each user's agents may spend in a calendar month, UTC:
$20.00 and 20,000,000 tokens to start with, input and output not capped apart, 0 for no
cap. Dollars count what providers report, which only OpenRouter does: Polythought keeps no
prices, so the token caps are what stop the others. On the Users page, **Agent caps** gives
a user caps of their own, empty for the instance's, and shows what their agents spent this
month.

Live orders go from the server to Polymarket's CLOB at clob.polymarket.com, signed there
with the wallet's imported or session key. The server follows each until its trades are
final on chain, and tells the owner when one fails.

**Builder code**, also under Trading, is your Polymarket builder code, from
polymarket.com/settings?tab=builder: live orders then carry it, crediting your builder
account with their volume, and pay the builder fee you set there on top of Polymarket's.
Empty, which it is to start with, orders carry none.

## Agents over MCP

Users can connect agents such as Claude Code and Codex at the server's address followed by
`/mcp`, such as `https://polythought.example.com/mcp`. **Settings → Connected agents** has
the steps for each client, the address filled in. The agent signs in through its user's
browser, where they choose what it may do, and the same page lists the agents connected now
and those allowed, to revoke. An API token works there too, with the scopes it was made with.
Agents read workflows, executions, the node catalog, the library, the markets and the user's
wallets and orders, build workflows, and trade, what reaches real money waiting for the
user to confirm it where they say so; `whoami` says who an agent acts for and what it may do.
An admin's agent with the admin scope also manages users, invites, Config and plugins, its
changes waiting for the admin where they say so. With the tasks scope, an agent takes the
tasks a workflow's Local agent node hands it.

Under **Agents over MCP** on the Config page:

- **Agents over MCP** switches it all on or off. Off, `/mcp` and agents' sign-in are not
  there; agents already connected stop, and their grants stay for users to revoke.
- **Agents register themselves** lets an agent with no metadata document of its own
  register, as older agents do; the consent page marks it unverified.
- **Documents at private addresses** lets the server fetch an agent's metadata document from
  inside its own network. Off to start with: any signed-in user could have it fetch one.
- **Let allowed agents through** skips the consent page when an agent asks for no more than
  it has and comes back to its own site over https. One coming back to its user's machine,
  as command-line agents do, takes a click, its scopes ticked as allowed.
- **Agents registered, not yet allowed**, 1,000 to start with: past it, agents registering
  themselves are refused until the daily prune.
- **Access token lifetime** and **Refresh token lifetime**, an hour and 30 days to start
  with: an agent unused for the second signs in again through the browser.
- **Tool calls a minute**, 120 to start with, is how many tool calls one agent, or one API
  token, may make in a minute.
- **Live actions wait**, an hour to start with, is how long an agent's or a token's live
  action waits for its user to confirm it before it expires.
- **Agent window waits**, 2 minutes to start with, is how long an agent's call waits for its
  user's answer in the agent's own window, where the user has it ask there.
- **Agents wait for tasks**, 50 seconds to start with, is how long an agent's call waiting
  for a Local agent node's task is held open. Keep it under what the agents' clients wait
  for a call: Codex gives up after a minute unless told otherwise.
- **A task is an agent's for**, 10 minutes to start with, is how long a task stays with the
  agent it was handed to, while it neither answers nor waits again, before it goes back to
  the user's other agents.
- **Agents count as around for**, a minute to start with, is how long after an agent last
  waited it still counts as there, so a Local agent node hands it a task rather than fail.
- **Clients with steps** lists the clients Connected agents gives steps for, a tab each,
  in order: `claude-code`, `codex`, `gemini-cli`, `cursor`, `opencode`, `vscode`,
  `claude-ai` and `other`, all of them to start with. Claude Desktop and claude.ai connect
  from Anthropic's cloud, so leave `claude-ai` out where the server is not reachable from
  the internet.
- **Address agents connect at**, empty to start with for the public URL followed by `/mcp`,
  is the address those steps give, for a server agents reach by another name.
- **Agents' sessions last**, an hour to start with, is how long an agent's session stays
  open with no call; it applies from the next restart. Sessions are kept in the database,
  so Connected agents still lists those open as the server restarts, until their agents
  come back with new ones.
- **Agents create accounts with passwords**, off to start with, lets an admin's agents and
  tokens create accounts with a password they give. Off, they invite people, by mail where
  mail is set up, else with a link, and no password passes through an agent.

A proxy of your own in front of the API must pass `/mcp` and `/.well-known/oauth-*` on as
Caddy does, and must not buffer `/mcp`'s answers, which stream.

## Where the server runs

Polymarket refuses new orders from restricted regions: the United States, the United
Kingdom, France, Germany and others take only the closing of positions, and sanctioned
ones nothing at all. Live orders go from the server, so where it runs decides. **Polymarket
access**, on the Config page's overview, says what Polymarket answers for the server's
address: checked at start, every 6 hours and on **Check now**, and logged as a warning while
blocked. Its answer also says blocked where Polymarket restricts only its website and its
API takes orders (Ireland, the Netherlands, Japan and a few more); see
[its list of regions](https://docs.polymarket.com/api-reference/geoblock). Polymarket's own
servers are in AWS eu-west-2, and eu-west-1 is the nearest region it does not restrict.
Paper trading works anywhere.

Only the server's address is checked: whether each user may trade on Polymarket where they
are is between them and Polymarket's terms.

## GitHub stars

Every page, the docs too, links to Polythought's repository on GitHub with its star count.
The server asks GitHub's API for the count at start and about once an hour, without a
token, and pages read it from the server. While GitHub does not answer, the link shows no
count. Turn off **GitHub stars** in the Network group of the Config page, or set
`POLYTHOUGHT_GITHUB_STARS=false`, and the server no longer asks; to keep it from calling
GitHub at all, turn off the update check too (see [Updates](#updates)).

## User code

Code that users write, in code nodes, custom nodes and workflows as code, runs in an engine
of its own: QuickJS compiled to WebAssembly, with no files, no network and nothing but what
the SDK gives it, within the limits under **Code** on the Config page. To start with, that
engine runs in the server's process.

On a public instance, where people you do not know write code, turn **Runner processes** on.
Code then runs in processes of their own, one for each of **Runs at once**, each running one
run at a time. A runner reaches nothing but the server. On Linux 6.2 or later, in Docker
too, each runs in [the sandbox plugins run in](/docs/self-hosting/plugins/#the-sandbox). A
runner that stops fails only its own run, and starts again for the next. Each takes about
80 MiB idle, more while it runs.

The setting applies when the server restarts. The **Code** group on the Config page then says
where code runs and how the runners are. Where the server has no sandbox, on a Mac for one,
the runners start without it and the Config page says so. Runners that do not start leave
the server running, but code does not run until they do: it never runs in the server's
process instead.

## Invites

While registration is by invite, people sign up with a code an admin gives them. On
**Admin → Invites**, **Make invite** makes one:

- **Code**: type one, 8 to 64 letters, digits, `-` and `_`, or leave it empty to have one
  made. Any case is the same code, so `SPRING-2026` and `spring-2026` are one; the field
  says as you type whether another invite has it already.
- **Sign-ups**: how many accounts it admits, 1 to 1,000.
- **Expires**: a day within a year. Left empty, it lasts **Invite lifetime**, under Accounts
  on the Config page, 7 days unless changed.

Share its link, `/register?invite=<code>`, or the code itself, which the sign-up form takes.
The list shows every invite: its code, to copy again; how many sign-ups it admitted of those
it allows, and who they were; when it expires; and whether it is open, used up, expired or
revoked. **Revoke** ends an open invite, and the accounts made with it stay. Codes are kept
sealed with the server's keys, as its other secrets are; an invite made before Polythought
kept codes shows none.

### Requests to join

With **Requests to join** on, under Accounts on the Config page, people without an invite can
ask for one: **Ask for one**, under the sign-up form's invite code, takes their address and an
optional note of up to 500 characters. It needs mail set up, under Mail, and registration by
invite.

The address gets a link to confirm the request, working once for 24 hours; until it is
opened, the admins see nothing. Once it is, the request waits on **Admin → Invites**, under
**Requests to join**, counted on the tab, and each admin is told, as **Someone asks to join**
says on their Notifications page. **Approve** mails the address an invite of its own, one use,
as **Invite by email** does; **Deny** closes the request, and mails the address so while
**Tell denied requests**, under Accounts, is on. Decided requests stay listed for 30 days.

The form answers the same whatever comes of it, so it tells nobody which addresses have an
account or a request. An address with an account, one already waiting, or one denied in the
last 30 days gets no mail; a request not confirmed gets a new link at most once a minute and
five times in all, and is removed a day after its link stops working. At most 200 requests
are open at once.

## More admins

Admins can promote users on **Admin → Users**. To create one from the command line, with the
password read from standard input:

```sh
printf '%s' "$PASSWORD" | docker compose -f deployments/docker-compose.yml --env-file .env \
  --profile app run --rm -T api admin create -email someone@example.com
```

## How everyone is doing

**Admin → Stats** shows every user's workflows together, on paper or live: the instance's
totals and runs, and a row for each user with workflows, sorted by the column you click.
A user's row opens their dashboard, read-only: their totals, workflows and traders, as
they see them. Their workflows are not opened from there.

An admin's agent with the `admin` scope reads the same through `get_instance_stats`, with
users by ID and never their email addresses.

## Mail

Polythought sends mail through an SMTP server you connect, your own or a provider's. Under
**Mail** on the Config page:

- **Mail server** is its host, like `mail.example.com`, and **Port** its port: 587 for
  STARTTLS, 465 for TLS from the start, 25 for a plain connection.
- **Security** is `starttls`, `tls` or `none`. The server's certificate is always checked,
  so it must be valid for the host as you entered it (Let's Encrypt's are). A plain
  connection is for a server on the same host or network, and carries no password except
  to this host.
- **Username** and **Password** are the account Polythought signs in with; leave them empty
  for a server that takes mail without signing in.
- **From address** and **From name** are who the mail comes from. The server must let the
  account send as that address, and for mail to arrive rather than land in spam, the
  address's domain needs SPF, DKIM and DMARC records for the server.

Mail is set up once the server and the From address are saved. **Send test email** sends
one to any address, and shows what the server answered when it did not take it. Like every
setting, these can come from environment variables instead, such as
`POLYTHOUGHT_MAIL_HOST` and `POLYTHOUGHT_MAIL_PASSWORD`; see
[Configuration](/docs/self-hosting/configuration/).

### Verifying sign-ups

With mail set up, **Verify sign-ups** (under Accounts) makes people who sign up themselves,
openly or with an invite, prove their address: they get a link by mail, working once for 24
hours, and sign in once they have opened it, with their password. The sign-in page offers to
send the link again, at most once a minute and five times a day; it says the same whether
or not the address has an account. An account that does not verify within 7 days is
removed, and signing up again with its address replaces it, so nobody can hold an address
that is not theirs.

Accounts from before, those an admin creates and the first admin count as verified. On
**Admin → Users**, unverified accounts are marked: **Send link** mails them a new one, and
**Mark verified** takes your word for the address, for when the mail does not get through.
The setting cannot be turned on without mail, nor mail removed while it is on.

### Invites by email

While mail is set up, **Admin → Invites** takes an address: **Send invite** mails an invite
there, naming you, unless **Invites by email** (under Accounts, on by default) is off. Only
that address signs up with it, once, filled in on the form, and the account counts as
verified, as the mail proved the address. An address that has an account already is
refused. If the mail does not go out, the invite is made anyway and its link shown, to send
yourself. **Make invite** still makes one for anyone, to share.

### Notifications by email

**Notifications by email** (under Mail, off by default) lets users get their
[notifications](/docs/guides/telegram/#notifications) by email too, beside Telegram: each
chooses per kind in Settings, by email off until they turn it on. Each mail links back to
the app and says where to turn it off.

### Password reset

While mail is set up, **Forgot password?** on the sign-in page mails a link to set a new
password, unless **Password reset by email** (under Accounts, on by default) is off. The
link works once, for an hour, and is sent at most once a minute and five times a day; the
page says the same whether or not the address has an account, and a disabled account is
sent nothing. Setting the password signs the account out of every session (its API tokens
keep working), verifies its address, and leads to the sign-in form, where the new password
is needed. After a reset, or a password change in Settings, the account is mailed a notice,
so an owner learns of a change they did not make.

## Updates

The **Config** page shows the version the server runs, with its commit; so does
`polythought version`, run in the `api` container. Images built from the code say `dev`.

Once a day, and on **Check now**, the server asks GitHub's API for Polythought's newest
release, without a token. When one is newer than the server's, the Config page says so, with
a link to what changed, and each admin hears of it once, on Telegram or by mail as they chose
under **Settings → Notifications**. Pre-releases, such as `1.3.0-beta.1`, count only with
**Pre-releases** on. To keep the server from asking, turn off **Check for updates** in the
Updates group of the Config page, or set `POLYTHOUGHT_UPDATE_CHECK=false`.

### The updater

With the updater, an admin updates from the Config page: **Update to 1.2.4**. It is a small
container beside the others with the Docker socket, which it needs to pull images and
recreate containers. Anything holding the socket is root on the machine, so leave it out if
you would rather update by hand. To set it up:

1. Make a token, `openssl rand -hex 32`, and put it in `.env` as
   `POLYTHOUGHT_UPDATER_TOKEN`. The API and the updater both read it there.
2. Start the stack with the updater's profile as well:

   ```sh
   docker compose -f deployments/docker-compose.yml --env-file .env --profile app --profile updater up -d
   ```

An update pulls the release's `api` and `web` images, sets `POLYTHOUGHT_VERSION` to it in
`.env`, so a later `docker compose up` stays on it, and recreates those two containers; the
server then updates the database as it starts, so back it up first, as below. The updater
updates itself last, where it can. It runs the update as the stack was started: the same
Compose files and env file, at the same paths, in a container of its own, which finishes even
if the updater stops. So the stack must have been started with `--env-file`, and its files
must not be at `/`. If an update fails, the Config page says why, with the end of what the
updater did; `docker compose logs updater` has the rest.

The updater also reads `POLYTHOUGHT_UPDATER_SERVICES`, the services it updates (`api,web`
unless set), `POLYTHOUGHT_UPDATER_PROJECT`, the stack's Compose project, and
`POLYTHOUGHT_UPDATER_IMAGE`, the image an update runs in, both its own unless set. The API
reaches it at **Updater address**, `http://updater:8080` unless set.

## Upgrading and backups

Back up the database first, as the server updates its schema when it starts: `pg_dump` of
the `polythought` database is enough, together with your keys. Then, with the released
images, set `POLYTHOUGHT_VERSION` to the new version, or leave it at `latest`, and pull:

```sh
docker compose -f deployments/docker-compose.yml --env-file .env --profile app pull
docker compose -f deployments/docker-compose.yml --env-file .env --profile app up -d
```

Or let the updater do it, as above. Built from the code, get the new code and run `make up`
again.

Stacks started before 0.0.1 ran as the Compose project `deployments`, after the Compose
file's folder; the file now names it `polythought`. Before running the new file with such a
stack, set `COMPOSE_PROJECT_NAME='deployments'` in `.env`, so it keeps its containers and its
data. Without it, Compose starts a new, empty stack beside the old one. The updater keeps the
name it found either way. A release's notes, on GitHub, say what changed. A version pinned as `1.2` takes its
patch releases with each pull, and `latest` every release.

Once the repository is public, each image is signed: `cosign verify
ghcr.io/lein3000zzz/polythought-api:1.2.3 --certificate-identity-regexp
'https://github.com/lein3000zzz/polythought-go/' --certificate-oidc-issuer
https://token.actions.githubusercontent.com` checks it was built by this repository's
release workflow. Each also carries its build provenance and a software bill of materials:
`docker buildx imagetools inspect ghcr.io/lein3000zzz/polythought-api:1.2.3 --format
'{{json .SBOM}}'` shows the packages in it.

## For development

The app and these docs need Node 26, which `nvm use` takes from `.nvmrc`, and pnpm, such as
from `npm install --global pnpm`. Whichever pnpm you install runs the version the app and the
docs pin, fetching it the first time.

| Command | What it does |
|---|---|
| `make db-up` | Starts PostgreSQL alone on port 5432, reachable from this machine only. |
| `make run` | Runs the API on port 8080 with the settings in `.env`. |
| `make web-dev` | Runs the app on port 5173, with live reload. |
| `make docs-dev` | Runs these docs on port 4321, at `/docs`. |
| `make check` | Format, vet, lint and unit tests, as CI runs them on each push. |
| `make vulncheck` | Known vulnerabilities the Go code reaches, and those of what the app and docs ship. |
| `make help` | Lists every target. |
