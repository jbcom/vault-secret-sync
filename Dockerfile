# syntax=docker/dockerfile:1.7

###
# Build a static vault-secret-sync binary for the requested platform.
# Tests now run in CI (outside Docker), so this Dockerfile focuses purely
# on compiling and packaging the runtime image.
###
FROM golang:1.24-bookworm AS builder

ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG TARGETVARIANT
ARG CGO_ENABLED=0

ARG VERSION=dev

ENV CGO_ENABLED=${CGO_ENABLED} \
    GOTOOLCHAIN=auto
WORKDIR /src

COPY go.mod go.sum ./

# Cache module and build downloads between runs
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY . .

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOOS=${TARGETOS} \
    GOARCH=${TARGETARCH} \
    GOARM=${TARGETVARIANT#v} \
    go build -trimpath \
      -ldflags="-s -w" \
      -o /out/vss ./cmd/vss

###
# Runtime image: tiny BusyBox container that only carries the binary and certs.
###
FROM busybox:1.36.1-musl AS runtime

ARG VERSION=dev
ARG VSS_CONFIG=/etc/vss/config.yaml

ENV VSS_CONFIG=${VSS_CONFIG} \
    VSS_VERSION=${VERSION}

LABEL org.opencontainers.image.title="vault-secret-sync" \
      org.opencontainers.image.source="https://github.com/jbcom/jbcom-oss-ecosystem" \
      org.opencontainers.image.version=${VERSION}

WORKDIR /app

RUN mkdir -p /etc/ssl/certs
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /out/vss /usr/local/bin/vss

USER 65532:65532

ENTRYPOINT ["/usr/local/bin/vss"]
