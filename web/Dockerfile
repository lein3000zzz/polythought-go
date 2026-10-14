# syntax=docker/dockerfile:1

# The web image: the app and the docs as static files behind Caddy, which also proxies
# /api to the API server. Build from the repository root, as the docs live beside web/:
#   docker build -f web/Dockerfile -t polythought-web .

FROM node:22-alpine AS app

WORKDIR /src
RUN corepack enable

COPY web/package.json web/pnpm-lock.yaml ./
RUN --mount=type=cache,target=/root/.local/share/pnpm/store pnpm install --frozen-lockfile

# The API types are committed (src/api/schema.d.ts), so the build does not need the spec.
COPY web/ ./
RUN pnpm exec vite build

FROM node:22-alpine AS docs

WORKDIR /src
RUN corepack enable
ENV ASTRO_TELEMETRY_DISABLED=1

COPY docs/package.json docs/pnpm-lock.yaml ./
RUN --mount=type=cache,target=/root/.local/share/pnpm/store pnpm install --frozen-lockfile

# The reference pages are generated from the Go code and committed, so no Go is needed.
COPY docs/ ./
RUN pnpm build

FROM caddy:2-alpine

COPY web/Caddyfile /etc/caddy/Caddyfile
COPY --from=docs /src/csp.caddy /etc/caddy/docs-csp.caddy
COPY --from=app /src/dist /srv
COPY --from=docs /src/dist /srv/docs
