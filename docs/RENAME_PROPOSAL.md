# Project Rename Proposal: vault-secret-sync → secretsync

## Background

This project has evolved significantly beyond its original scope as a Vault-specific synchronization tool. It now supports:

- **Multiple source stores**: Vault, AWS Secrets Manager, S3, HTTP APIs
- **Multiple target stores**: AWS Secrets Manager, Vault, GCP Secret Manager, GitHub Secrets, Doppler, Kubernetes
- **Multiple merge stores**: Vault KV2, S3
- **Complex orchestration**: Two-phase pipeline (merge → sync), dependency graphs, inheritance

The name "vault-secret-sync" no longer accurately represents what this tool does.

## Proposed Names

| Name | Binary | Go Module | Pros | Cons |
|------|--------|-----------|------|------|
| **secretsync** | `secretsync` | `github.com/org/secretsync` | Clear, universal, memorable | Generic |
| **secpipe** | `secpipe` | `github.com/org/secpipe` | Emphasizes pipeline architecture | Less obvious |
| **polysync** | `polysync` | `github.com/org/polysync` | Emphasizes multi-store nature | Abstract |
| **keystroke** | `keystroke` | `github.com/org/keystroke` | Memorable, unique | Could confuse with keylogger |

**Recommendation**: `secretsync` - it's what users will search for and immediately understand.

## Migration Plan

### Phase 1: Preparation (Current PR)
- [ ] Complete all outstanding features
- [ ] Ensure tests pass
- [ ] Document current architecture

### Phase 2: Rename Execution
1. **Create new repository**: `github.com/jbcom/secretsync`
2. **Update Go module path**: `github.com/jbcom/secretsync`
3. **Update all imports**: sed/find-replace across codebase
4. **Update binary name**: `vss` → `secretsync` (or keep `vss` as alias)
5. **Update Helm charts**: `vault-secret-sync` → `secretsync`
6. **Update Docker images**: `vault-secret-sync` → `secretsync`

### Phase 3: Documentation
1. **Update README**: New name, broader scope
2. **Update architecture docs**: Reflect multi-store reality
3. **Add migration guide**: For existing users
4. **Update examples**: Remove Vault-centric assumptions

### Phase 4: Release
1. **Final release on old repo**: v1.x.x with deprecation notice
2. **Initial release on new repo**: v2.0.0
3. **Archive old repository**: Point to new location

## Breaking Changes to Consider

### Module Path
```go
// Old
import "github.com/robertlestak/vault-secret-sync/..."

// New
import "github.com/jbcom/secretsync/..."
```

### Binary Name Options
```bash
# Option A: New name
secretsync pipeline --config config.yaml

# Option B: Keep vss (vault-secret-sync legacy)
vss pipeline --config config.yaml

# Option C: Both (symlink)
secretsync -> vss
```

### Helm Chart
```yaml
# Old
helm install vault-secret-sync deploy/charts/vault-secret-sync

# New
helm install secretsync deploy/charts/secretsync
```

### Docker Image
```bash
# Old
docker pull jbcom/vault-secret-sync:latest

# New
docker pull jbcom/secretsync:latest
```

## Timeline

| Phase | Duration | Dependencies |
|-------|----------|--------------|
| Phase 1 | Current PR | None |
| Phase 2 | 1 day | Phase 1 merged |
| Phase 3 | 2-3 days | Phase 2 complete |
| Phase 4 | 1 day | Phase 3 complete |

## Attribution

This project was originally forked from [robertlestak/vault-secret-sync](https://github.com/robertlestak/vault-secret-sync). We acknowledge and thank the original author for the foundation this project is built upon.

The fork has diverged significantly with:
- Unified pipeline configuration
- Two-phase merge/sync architecture
- Dynamic target discovery (AWS Organizations, Identity Center)
- S3 merge store support
- Comprehensive diff/dry-run system
- CI/CD integration (GitHub Actions output)
- Multi-store support beyond Vault

## Decision Required

- [ ] Approve rename to `secretsync`
- [ ] Confirm new repository location
- [ ] Confirm binary name strategy
- [ ] Set timeline for migration
