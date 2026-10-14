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

The Compose file in `deploy/` suits a single machine. Before others use it:

- **Address.** Set `POLYTHOUGHT_PUBLIC_URL` in `deploy/docker-compose.yml` to the address
  people use, such as `https://polythought.example.com`. Only that origin may make changes
  with a signed-in session, and an `https` address makes the session cookie secure.
- **HTTPS.** Put a proxy that handles TLS in front of the `web` container, or give Caddy your
  domain in `web/Caddyfile` instead of `:80` and let it get certificates itself.
- **Database password.** Change `POSTGRES_PASSWORD` and the password in
  `POLYTHOUGHT_DATABASE_URL` from the development default.
- **Sign-up.** Registration is by invite unless an admin changes it.

## Environment variables

These are read when the server starts and cannot be changed on the Config page:

| Variable | Default | What it is |
|---|---|---|
| `POLYTHOUGHT_DATABASE_URL` | required | PostgreSQL connection URL, e.g. `postgres://user:pass@host:5432/db?sslmode=disable`. |
| `POLYTHOUGHT_SECRET_KEYS` | required | Keys for secrets at rest, `id:base64key`, comma-separated; see [Keys](#keys). |
| `POLYTHOUGHT_AUTO_MIGRATE` | `true` | Update the database schema when the server starts. |
| `POLYTHOUGHT_HTTP_ADDR` | `:8080` | Where the API listens. |
| `POLYTHOUGHT_PUBLIC_URL` | `http://localhost:8080` | The address people use; see above. |
| `POLYTHOUGHT_ADMIN_EMAIL`, `POLYTHOUGHT_ADMIN_PASSWORD` | none | The first admin, created when there is no admin. Set both or neither. |

Every other setting can also be set by an environment variable, which then locks it on the
Config page; see [Configuration](/docs/self-hosting/configuration/).

## Keys

`POLYTHOUGHT_SECRET_KEYS` encrypts wallet API credentials, [credentials](/docs/guides/credentials/)
and secret settings. Back it up apart from the database: without it they cannot be read.

To change keys, put the new one first and keep the old one after it, as in
`k2:…,k1:…`. New secrets use the first key; the others only read what was saved with them.
Saved secrets keep the key they were saved with, so keep an old key listed as long as
anything saved with it remains.

## More admins

Admins can promote users on **Admin → Users**. To create one from the command line, with the
password read from standard input:

```sh
printf '%s' "$PASSWORD" | docker compose -f deploy/docker-compose.yml --env-file .env \
  --profile app run --rm -T api admin create -email someone@example.com
```

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
