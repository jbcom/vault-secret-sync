# SecretSync

> **Universal Secrets Synchronization Pipeline**

SecretSync provides fully automated, real-time secret synchronization across multiple cloud providers and secret stores. It supports a two-phase pipeline architecture (merge → sync) with inheritance, dynamic target discovery, and CI/CD-friendly diff reporting.

## Attribution

This project is a fork of [robertlestak/vault-secret-sync](https://github.com/robertlestak/vault-secret-sync). We acknowledge and thank **Robert Lestak** for creating the original foundation this project builds upon.

The fork has diverged significantly to support:
- Multi-phase pipeline architecture (merge + sync)
- S3 merge store support
- Dynamic target discovery (AWS Organizations, Identity Center, SSM)
- Comprehensive diff/dry-run system with CI/CD integration
- DeepMerge semantics matching terraform-aws-secretsmanager
- Universal multi-store support (beyond Vault-centric)

## Supported Secret Stores

| Store | Source | Target | Merge Store |
|-------|--------|--------|-------------|
| HashiCorp Vault (KV2) | ✅ | ✅ | ✅ |
| AWS Secrets Manager | ✅ | ✅ | ❌ |
| AWS S3 | ❌ | ❌ | ✅ |
| GCP Secret Manager | ✅ | ✅ | ❌ |
| GitHub Secrets | ❌ | ✅ | ❌ |
| Doppler | ❌ | ✅ | ❌ |
| Kubernetes Secrets | ❌ | ✅ | ❌ |
| HTTP/Webhook | ❌ | ✅ | ❌ |

## Two-Phase Pipeline Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                    MERGE PHASE (Optional)                        │
│  Source1 ──┐                                                     │
│  Source2 ──┼──▶ Merge Store (Vault/S3) ──▶ Aggregated Secrets   │
│  Source3 ──┘    (deepmerge, inheritance)                         │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                        SYNC PHASE                                │
│  Merge Store ──┬──▶ AWS Account 1 (via STS AssumeRole)          │
│  (or Source)   ├──▶ AWS Account 2                                │
│                ├──▶ Vault Cluster                                │
│                └──▶ GCP Project                                  │
└─────────────────────────────────────────────────────────────────┘
```

See [Two-Phase Architecture](./docs/TWO_PHASE_ARCHITECTURE.md) for detailed documentation.

## Quick Start

### Installation

```bash
# Go install
go install github.com/jbcom/secretsync/cmd/vss@latest

# Or download binary from releases
curl -LO https://github.com/jbcom/secretsync/releases/latest/download/secretsync-linux-amd64
chmod +x secretsync-linux-amd64
sudo mv secretsync-linux-amd64 /usr/local/bin/secretsync
```

### Basic Usage

```bash
# Validate configuration
secretsync validate --config pipeline.yaml

# Dry run with diff output
secretsync pipeline --config pipeline.yaml --dry-run --output json

# Full pipeline execution
secretsync pipeline --config pipeline.yaml

# CI/CD mode (exit codes: 0=no changes, 1=changes, 2=errors)
secretsync pipeline --config pipeline.yaml --dry-run --exit-code
```

### Example Configuration

```yaml
# pipeline.yaml
vault:
  address: "https://vault.example.com"
  namespace: "admin"

aws:
  region: "us-east-1"
  execution_role_pattern: "arn:aws:iam::{account_id}:role/SecretsSync"

merge_store:
  vault:
    mount: "secret/merged"

sources:
  api-keys:
    vault:
      path: "secret/api-keys"
  database:
    vault:
      path: "secret/database"

targets:
  Staging:
    imports: [api-keys, database]
    account_id: "111111111111"
  
  Production:
    inherits: Staging
    imports: [production-overrides]
    account_id: "222222222222"
```

## CI/CD Integration

### GitHub Actions

```yaml
- name: Validate secrets pipeline
  run: |
    secretsync pipeline --config pipeline.yaml --dry-run --output github --exit-code
  
- name: Apply secrets (on merge to main)
  if: github.ref == 'refs/heads/main'
  run: |
    secretsync pipeline --config pipeline.yaml
```

### Output Formats

| Format | Use Case |
|--------|----------|
| `human` | Interactive terminal output |
| `json` | Machine parsing, logging |
| `github` | GitHub Actions annotations |
| `compact` | One-line CI status |

## Documentation

- [Architecture Overview](./docs/ARCHITECTURE.md)
- [Two-Phase Pipeline](./docs/TWO_PHASE_ARCHITECTURE.md)
- [Pipeline Configuration](./docs/PIPELINE.md)
- [Deployment Guide](./docs/DEPLOYMENT.md)
- [Security Configuration](./docs/SECURITY.md)
- [Usage Reference](./docs/USAGE.md)

## Helm Deployment

```bash
# Add Helm repo
helm repo add secretsync https://jbcom.github.io/secretsync

# Install
helm install secretsync secretsync/secretsync \
  --set vault.address=https://vault.example.com
```

## Docker

```bash
# Run with config file
docker run -v $(pwd)/config.yaml:/config.yaml \
  jbcom/secretsync pipeline --config /config.yaml

# Multi-arch images available: linux/amd64, linux/arm64
```

## Development

```bash
# Clone
git clone https://github.com/jbcom/secretsync.git
cd secretsync

# Build
go build ./...

# Test
go test ./...

# Lint
golangci-lint run
```

## License

[MIT License](./LICENSE)

## Original Author

**Robert Lestak** - [github.com/robertlestak](https://github.com/robertlestak)

Original project: [vault-secret-sync](https://github.com/robertlestak/vault-secret-sync)

## Current Maintainer

**jbcom** - [github.com/jbcom](https://github.com/jbcom)
