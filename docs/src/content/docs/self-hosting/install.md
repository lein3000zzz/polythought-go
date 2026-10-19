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
  with a signed-in session, and an `https` address makes the session cookie secure.
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

## Upgrading and backups

Get the new code and run `make up` again. The database schema is updated when the server
starts, so back up the database first: `pg_dump` of the `polythought` database is enough,
together with your keys.

## For development

| Command | What it does |
|---|---|
| `make db-up` | Starts PostgreSQL alone on port 5432. |
| `make run` | Runs the API on port 8080 with the settings in `.env`. |
| `make web-dev` | Runs the app on port 5173, with live reload. |
| `make docs-dev` | Runs these docs on port 4321, at `/docs`. |
| `make help` | Lists every target. |
