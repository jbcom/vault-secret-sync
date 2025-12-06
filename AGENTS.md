# Agent Instructions for vault-secret-sync

## Overview

Kubernetes operator for syncing secrets from HashiCorp Vault to external stores.

## Before Starting

```bash
cat memory-bank/activeContext.md
```

## Development Commands

```bash
# Download dependencies
go mod download

# Build
go build ./...

# Test
go test ./...

# Lint
golangci-lint run

# Run locally
go run cmd/vss/main.go
```

## Docker

```bash
# Build image
docker build -t vault-secret-sync .

# Run with docker-compose
docker-compose up
```

## Kubernetes

```bash
# Deploy with Helm
helm upgrade --install vault-secret-sync deploy/charts/vault-secret-sync
```

## Architecture

- `cmd/` - Application entrypoints
- `pkg/` - Core library code
- `stores/` - Secret store implementations
- `internal/` - Internal packages
- `deploy/` - Kubernetes manifests and Helm charts

## Commit Messages

Use conventional commits:
- `feat(store): new secret store` → minor
- `fix(sync): bug fix` → patch

## Important Notes

- Go 1.23+ required
- Docker Hub for image releases
- Helm OCI for chart releases
