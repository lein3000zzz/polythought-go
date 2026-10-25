# syntax=docker/dockerfile:1

# Polythought's updater (D425), which the Compose file's updater profile runs beside the
# stack with the Docker socket. Build from the repository root:
#   docker build -f deployments/updater.Dockerfile -t polythought-updater .
# On the builder's own platform, Go cross-compiling for the image's (D373): no emulation.
FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine AS build

ARG TARGETOS TARGETARCH

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY cmd ./cmd
COPY internal ./internal
COPY pkg ./pkg

# The version a release was tagged and the commit it was built from; dev for a build from
# the code.
ARG VERSION=dev
ARG COMMIT=

RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -o /out/polythought-updater \
      -ldflags="-s -w -X github.com/lein3000zzz/polythought-go/internal/buildinfo.version=${VERSION} -X github.com/lein3000zzz/polythought-go/internal/buildinfo.commit=${COMMIT}" \
      ./cmd/polythought-updater

# The docker CLI with its Compose plugin, which the updater runs: no shell reads its
# arguments. It runs as root, as the Docker socket asks.
FROM docker:29-cli

COPY --from=build /out/polythought-updater /usr/local/bin/polythought-updater

EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/polythought-updater"]
CMD ["serve"]
