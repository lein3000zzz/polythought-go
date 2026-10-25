# syntax=docker/dockerfile:1

# The web image: the app and the docs as static files behind Caddy, which also proxies
# /api to the API server. Build from the repository root, as the docs live beside web/:
#   docker build -f web/Dockerfile -t polythought-web .

# The app and the docs are files the same on every platform: they build on the builder's
# own (D373), and only Caddy is the image's.
FROM --platform=$BUILDPLATFORM node:26-alpine AS app

WORKDIR /src/web

COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
# pnpm as package.json pins it: Node no longer brings corepack (D423).
RUN npm install --global "pnpm@$(node -p "require('./package.json').packageManager.replace(/^pnpm@/, '')")"
RUN --mount=type=cache,target=/root/.local/share/pnpm/store pnpm install --frozen-lockfile

# The API types are committed (src/api/schema.d.ts), so the build does not need the spec.
# The builder of workflow code lives beside the SDK, where the server embeds it.
COPY internal/adapters/tscompiler/sdk/workflow.ts /src/internal/adapters/tscompiler/sdk/
COPY web/ ./
# The version a release was tagged (D425): the app shows it beside the server's.
ARG VERSION=dev
RUN VITE_POLYTHOUGHT_VERSION="$VERSION" pnpm exec vite build

# Debian, not Alpine: the docs' diagrams are drawn by a headless Chromium as they build
# (Mermaid), which needs glibc. Only this stage has it; the image is Caddy and files.
FROM --platform=$BUILDPLATFORM node:26-bookworm-slim AS docs

WORKDIR /src
ENV ASTRO_TELEMETRY_DISABLED=1

COPY docs/package.json docs/pnpm-lock.yaml docs/pnpm-workspace.yaml ./
RUN npm install --global "pnpm@$(node -p "require('./package.json').packageManager.replace(/^pnpm@/, '')")"
RUN --mount=type=cache,target=/root/.local/share/pnpm/store pnpm install --frozen-lockfile
RUN pnpm exec playwright install --with-deps --only-shell chromium

# The reference pages are generated from the Go code and committed, so no Go is needed.
COPY docs/ ./
RUN pnpm build

FROM caddy:2-alpine

COPY web/Caddyfile /etc/caddy/Caddyfile
COPY --from=docs /src/csp.caddy /etc/caddy/docs-csp.caddy
COPY --from=app /src/web/dist /srv
COPY --from=docs /src/dist /srv/docs
