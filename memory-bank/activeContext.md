# Active Context

## vault-secret-sync

Kubernetes operator for syncing secrets between HashiCorp Vault and external secret stores.

### Supported Stores
- HashiCorp Vault (source)
- AWS Secrets Manager
- GCP Secret Manager
- Azure Key Vault
- Doppler
- Redis
- And more...

### Package Status
- **Registry**: Docker Hub
- **Language**: Go 1.23+
- **Deployment**: Kubernetes Helm chart

### Development
```bash
go mod download
go build ./...
go test ./...
golangci-lint run
```

### Deployment
```bash
# Build Docker image
docker build -t vault-secret-sync .

# Deploy with Helm
helm upgrade --install vault-secret-sync deploy/charts/vault-secret-sync
```

---
*Last updated: 2025-12-06*
