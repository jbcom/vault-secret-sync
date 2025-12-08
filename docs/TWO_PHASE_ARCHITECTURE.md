# Two-Phase Pipeline Architecture

## Overview

The secrets synchronization pipeline operates in two distinct phases:

1. **MERGE Phase** (optional): Aggregate secrets from multiple sources into a unified pool
2. **SYNC Phase**: Propagate secrets from source(s) to target(s)

```
┌──────────────────────────────────────────────────────────────────────────────┐
│                           PIPELINE EXECUTION                                  │
├──────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                         MERGE PHASE (Optional)                       │    │
│  │                                                                       │    │
│  │   ┌─────────┐                                                        │    │
│  │   │ Source1 │──┐                                                     │    │
│  │   │ (Vault) │  │     ┌──────────────────┐                           │    │
│  │   └─────────┘  │     │                  │                           │    │
│  │   ┌─────────┐  ├────▶│   MERGE STORE    │                           │    │
│  │   │ Source2 │──┤     │   (Vault/S3)     │                           │    │
│  │   │  (AWS)  │  │     │                  │                           │    │
│  │   └─────────┘  │     │  • Aggregation   │                           │    │
│  │   ┌─────────┐  │     │  • Deduplication │                           │    │
│  │   │ Source3 │──┘     │  • Inheritance   │                           │    │
│  │   │ (HTTP)  │        │  • DeepMerge     │                           │    │
│  │   └─────────┘        └────────┬─────────┘                           │    │
│  │                               │                                      │    │
│  └───────────────────────────────┼──────────────────────────────────────┘    │
│                                  │                                           │
│                                  ▼ (automatic source injection)              │
│                                                                              │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                           SYNC PHASE                                 │    │
│  │                                                                       │    │
│  │   ┌──────────────────┐                                               │    │
│  │   │                  │     ┌─────────────────┐                       │    │
│  │   │   SOURCE         │────▶│  Target1 (AWS)  │                       │    │
│  │   │   (Merge Store   │     └─────────────────┘                       │    │
│  │   │    or Direct)    │     ┌─────────────────┐                       │    │
│  │   │                  │────▶│  Target2 (Vault)│                       │    │
│  │   │                  │     └─────────────────┘                       │    │
│  │   │                  │     ┌─────────────────┐                       │    │
│  │   │                  │────▶│  Target3 (GCP)  │                       │    │
│  │   └──────────────────┘     └─────────────────┘                       │    │
│  │                                                                       │    │
│  └───────────────────────────────────────────────────────────────────────┘    │
│                                                                              │
└──────────────────────────────────────────────────────────────────────────────┘
```

## Phase Details

### MERGE Phase

**Purpose**: Construct a unified secret pool from multiple sources with inheritance and aggregation.

**When to use**:
- Multiple sources need to be combined
- Secrets have inheritance relationships (e.g., Prod inherits from Stg)
- DeepMerge semantics are required (list append, dict merge)

**Supported Merge Stores**:
| Store | Use Case | Advantages |
|-------|----------|------------|
| **Vault KV2** | Standard merge with versioning | Native merge support, CAS writes, audit trail |
| **S3** | Large-scale or cross-region | Scalable, cheap, good for large secrets |

**Merge Process**:
```
For each target in topological order:
  1. Resolve dependencies (inheritance)
  2. For each import:
     a. Read secrets from source
     b. Apply transforms/filters
     c. DeepMerge into merge store:
        - Lists: APPEND
        - Dicts: RECURSIVE MERGE
        - Scalars: OVERRIDE
        - Conflicts: OVERRIDE
```

### SYNC Phase

**Purpose**: Propagate secrets from source to target stores.

**Source determination**:
- If MERGE phase ran: Merge store becomes the source automatically
- If SYNC-only: Explicitly configured source

**Supported Store Combinations**:
| Source | Target | Status |
|--------|--------|--------|
| Vault | AWS Secrets Manager | ✅ Supported |
| Vault | Vault | ✅ Supported |
| Vault | GCP Secret Manager | ✅ Supported |
| Vault | GitHub Secrets | ✅ Supported |
| Vault | Kubernetes Secrets | ✅ Supported |
| AWS SM | AWS SM | ✅ Supported |
| AWS SM | Vault | ✅ Supported |
| S3 | AWS SM | ✅ Supported (via S3 merge store) |

**Sync Process**:
```
For each target (parallel):
  1. Assume execution role (if cross-account)
  2. Read secrets from source
  3. Compute diff (current vs desired)
  4. Apply changes:
     - CREATE new secrets
     - UPDATE modified secrets
     - DELETE orphaned secrets (if sync_delete=true)
  5. Report diff statistics
```

## Configuration Modes

### Mode 1: Merge + Sync (Full Pipeline)

```yaml
# Both phases enabled
pipeline:
  merge:
    enabled: true
    parallel: 4
  sync:
    enabled: true
    parallel: 8

merge_store:
  vault:
    mount: "secret/merge"

sources:
  api-keys:
    vault:
      path: "secret/api-keys"

targets:
  Production:
    imports: [api-keys]
    account_id: "123456789012"
```

**Execution**:
```bash
vss pipeline --config config.yaml
# Phase 1: MERGE - sources → merge store
# Phase 2: SYNC  - merge store → targets
```

### Mode 2: Sync Only (Direct)

```yaml
# No merge phase - direct source to target
pipeline:
  merge:
    enabled: false
  sync:
    enabled: true

# Source is specified directly per target or globally
source:
  vault:
    path: "secret/app-secrets"

targets:
  Production:
    account_id: "123456789012"
```

**Execution**:
```bash
vss pipeline --config config.yaml --sync-only
# Only SYNC phase - source → targets directly
```

### Mode 3: Merge Only (Aggregation)

```yaml
# Only merge phase - useful for preparing secrets
pipeline:
  merge:
    enabled: true
  sync:
    enabled: false

merge_store:
  s3:
    bucket: "secrets-merge-bucket"
    prefix: "merged/"

sources:
  team-a: { vault: { path: "secret/team-a" } }
  team-b: { vault: { path: "secret/team-b" } }

targets:
  Combined:
    imports: [team-a, team-b]
```

**Execution**:
```bash
vss pipeline --config config.yaml --merge-only
# Only MERGE phase - sources → merge store
# Sync can be triggered later or by another process
```

## Diff and Dry-Run

Both phases support diff computation:

```bash
# Dry-run with diff output
vss pipeline --config config.yaml --dry-run --output json

# Output:
{
  "dry_run": true,
  "summary": {
    "added": 5,
    "modified": 2,
    "removed": 0,
    "unchanged": 43
  },
  "targets": [...]
}
```

### Zero-Sum Validation

For migration validation, ensure the new pipeline produces identical results:

```bash
# Should return exit code 0 (no changes)
vss pipeline --config config.yaml --dry-run --exit-code
echo $?  # 0 = zero-sum, 1 = changes detected, 2 = errors
```

### CI/CD Integration

```yaml
# GitHub Actions example
- name: Validate secrets pipeline
  run: |
    vss pipeline --config config.yaml --dry-run --output github --exit-code
  continue-on-error: true
  
- name: Check for unexpected changes
  if: ${{ steps.validate.outcome == 'failure' }}
  run: echo "Secrets diff detected - review required"
```

## Inheritance and Dependency Resolution

Targets are processed in topological order based on their dependencies:

```yaml
targets:
  Base:
    imports: [common-secrets]
  
  Staging:
    inherits: Base
    imports: [staging-secrets]
  
  Production:
    inherits: Staging
    imports: [production-secrets]
```

**Processing Order**:
1. `Base` (no dependencies)
2. `Staging` (depends on Base)
3. `Production` (depends on Staging)

**Merge Result for Production**:
```
common-secrets + staging-secrets + production-secrets
(with DeepMerge semantics)
```

## Store-Specific Behaviors

### Vault as Source
- Recursive listing of KV2 secrets
- Namespace support
- Token refresh handling

### Vault as Merge Store
- CAS (Check-and-Set) writes for safety
- Native merge mode (`merge: true`)
- Version history preserved

### S3 as Merge Store
- JSON serialization of secrets
- Per-target/per-import organization
- Manifest file for tracking

### AWS Secrets Manager as Target
- Cross-account via STS AssumeRole
- Path conflict handling (`/foo` vs `foo`)
- Idempotent writes (JSON comparison)
- Planned deletion exclusion

## Best Practices

1. **Use MERGE phase when**:
   - Multiple sources need combining
   - You have inheritance requirements
   - You need an intermediate audit point

2. **Use SYNC-only when**:
   - Simple source-to-target replication
   - No aggregation needed
   - Direct 1:1 mapping

3. **Always use dry-run first**:
   ```bash
   vss pipeline --config config.yaml --dry-run --output human
   ```

4. **For migrations, validate zero-sum**:
   ```bash
   vss pipeline --config old-config.yaml --dry-run --exit-code
   # Must return 0 before switching to new solution
   ```
