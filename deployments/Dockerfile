# syntax=docker/dockerfile:1

# The API server. Build from the repository root:
#   docker build -f deployments/Dockerfile -t polythought .
# The image's GOTOOLCHAIN is local, so go.mod's toolchain line does not reach it: the patch
# release is pinned here too, to build with the same Go.
# On the builder's own platform, Go cross-compiling for the image's (D373): no emulation.
# The Python SDK's wheel (sdk/python), for the server to serve: built for its version, on
# the builder's platform, as it is pure Python.
FROM --platform=$BUILDPLATFORM python:3.12-slim AS python-sdk

WORKDIR /sdk
COPY sdk/python ./

ARG VERSION=dev
RUN --mount=type=cache,target=/root/.cache/pip \
    python set_version.py "$VERSION" && pip wheel --no-deps -w dist .

FROM --platform=$BUILDPLATFORM golang:1.27.4-alpine AS build

ARG TARGETOS TARGETARCH

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY cmd ./cmd
COPY internal ./internal
COPY pkg ./pkg
COPY sdk ./sdk
COPY --from=python-sdk /sdk/dist/ ./sdk/python/dist/

# The version a release was tagged and the commit it was built from (D425); dev for a
# build from the code.
ARG VERSION=dev
ARG COMMIT=

# stdjson keeps Hertz on encoding/json: sonic does not support this Go version and would
# only fall back to it at runtime, with a warning.
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -tags stdjson -o /out/polythought \
      -ldflags="-s -w -X github.com/lein3000zzz/polythought-go/internal/buildinfo.version=${VERSION} -X github.com/lein3000zzz/polythought-go/internal/buildinfo.commit=${COMMIT}" \
      ./cmd/polythought

# The plugins directory (D315), the server's to write: a volume, so installed plugins
# outlive the container.
RUN mkdir -p /out/plugins

# static: no libc needed with CGO off; it carries CA certificates for the Polymarket APIs.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/polythought /polythought
COPY --from=build --chown=nonroot:nonroot /out/plugins /var/lib/polythought/plugins

ENV POLYTHOUGHT_PLUGINS_DIR=/var/lib/polythought/plugins
VOLUME /var/lib/polythought/plugins

EXPOSE 8080
ENTRYPOINT ["/polythought"]
CMD ["serve"]
