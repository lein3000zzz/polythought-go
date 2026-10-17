# syntax=docker/dockerfile:1

# The API server. Build from the repository root:
#   docker build -f deployments/Dockerfile -t polythought .
FROM golang:1.27-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY cmd ./cmd
COPY internal ./internal
COPY pkg ./pkg

# stdjson keeps Hertz on encoding/json: sonic does not support this Go version and would
# only fall back to it at runtime, with a warning.
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -tags stdjson -ldflags="-s -w" -o /out/polythought ./cmd/polythought

# static: no libc needed with CGO off; it carries CA certificates for the Polymarket APIs.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/polythought /polythought

EXPOSE 8080
ENTRYPOINT ["/polythought"]
CMD ["serve"]
