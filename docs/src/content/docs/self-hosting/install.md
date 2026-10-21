---
title: Install
description: Run Polythought with Docker.
---

Polythought is three containers: PostgreSQL, the API server, and a web server (Caddy) that
serves the app and these docs and passes `/api` on to the API. Docker Compose runs them
together.

## What you need

- Docker with Compose.
- `make` and `openssl`, to follow the steps below.

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

3. Start everything:

   ```sh
   make up
   ```

   The first start builds the images, which takes a few minutes. Then open
   <http://localhost:3000> and sign in as the admin. `Ctrl+C` stops it; `make down` removes
   the containers. Your data stays in a Docker volume.

Everything else, such as who may sign up, is set by admins on the **Config** page; see
[Configuration](/docs/self-hosting/configuration/).

## On a server

The Compose file in `deployments/` suits a single machine. Before others use it:

- **Address.** Set `POLYTHOUGHT_PUBLIC_URL` in `deployments/docker-compose.yml` to the address
  people use, such as `https://polythought.example.com`. Only that origin may make changes
  with a signed-in session, and an `https` address makes the session cookie secure, named
  `__Host-polythought_session` so no other site of the domain can set it.
- **HTTPS.** Put a proxy that handles TLS in front of the `web` container, or give Caddy your
  domain in `web/Caddyfile` instead of `:80` and let it get certificates itself.
- **Database password.** Change `POSTGRES_PASSWORD` and the password in
  `POLYTHOUGHT_DATABASE_URL` from the development default.
- **Sign-up.** Registration is by invite unless an admin changes it.

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

## Where the server runs

Polymarket refuses new orders from restricted regions: the United States, the United
Kingdom, France, Germany and others take only the closing of positions, and sanctioned
ones nothing at all. Live orders go from the server, so where it runs decides. **Polymarket
access**, at the top of the Config page, says what Polymarket answers for the server's
address: checked at start, every 6 hours and on **Check now**, and logged as a warning while
blocked. Its answer also says blocked where Polymarket restricts only its website and its
API takes orders (Ireland, the Netherlands, Japan and a few more); see
[its list of regions](https://docs.polymarket.com/api-reference/geoblock). Polymarket's own
servers are in AWS eu-west-2, and eu-west-1 is the nearest region it does not restrict.
Paper trading works anywhere.

Only the server's address is checked: whether each user may trade on Polymarket where they
are is between them and Polymarket's terms.

## More admins

Admins can promote users on **Admin → Users**. To create one from the command line, with the
password read from standard input:

```sh
printf '%s' "$PASSWORD" | docker compose -f deployments/docker-compose.yml --env-file .env \
  --profile app run --rm -T api admin create -email someone@example.com
```

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

While mail is set up, the Invites section of **Admin → Users** takes an address: **Send
invite** mails the invite there, naming you, unless **Invites by email** (under Accounts, on
by default) is off. Only that address signs up with it, filled in on the form, and the
account counts as verified, as the mail proved the address. An address that has an account
already is refused. If the mail does not go out, the invite is made anyway and its link
shown, to send yourself. **Create invite** still makes a link for anyone, to copy.

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

## Upgrading and backups

Get the new code and run `make up` again. The database schema is updated when the server
starts, so back up the database first: `pg_dump` of the `polythought` database is enough,
together with your keys.

## For development

| Command | What it does |
|---|---|
| `make db-up` | Starts PostgreSQL alone on port 5432, reachable from this machine only. |
| `make run` | Runs the API on port 8080 with the settings in `.env`. |
| `make web-dev` | Runs the app on port 5173, with live reload. |
| `make docs-dev` | Runs these docs on port 4321, at `/docs`. |
| `make help` | Lists every target. |
