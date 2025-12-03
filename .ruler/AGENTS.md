# vault-secret-sync

Kubernetes operator for secret synchronization (Go).

## Stores

- **Source**: Vault, Doppler
- **Destination**: AWS Secrets Manager, GitHub Secrets, GCP Secret Manager

## Deployment

```bash
# Helm
helm install vault-secret-sync oci://docker.io/jbcom/vault-secret-sync

# Docker
docker run jbcom/vault-secret-sync
```
