# syntax=docker/dockerfile:1

# The web UI: static files behind Caddy, which also proxies /api to the API server.
# Build from this directory: docker build -t polythought-web web
FROM node:22-alpine AS build

WORKDIR /src
RUN corepack enable

COPY package.json pnpm-lock.yaml ./
RUN --mount=type=cache,target=/root/.local/share/pnpm/store pnpm install --frozen-lockfile

# The API types are committed (src/api/schema.d.ts), so the build does not need the spec.
COPY . .
RUN pnpm exec vite build

FROM caddy:2-alpine

COPY Caddyfile /etc/caddy/Caddyfile
COPY --from=build /src/dist /srv
