# Pipeline Configuration Guide

The Pipeline feature provides a unified configuration format for multi-account secrets management, with native support for AWS Control Tower and Organizations patterns.

## Overview

Traditional secrets syncing requires separate configs for each source-to-destination pair. The Pipeline feature introduces:

- **Unified Configuration**: Single YAML file defining sources, targets, and inheritance
- **AWS Control Tower Awareness**: Native support for Control Tower execution roles
- **Organizations Integration**: Run from management account or delegated administrator
- **Inheritance Hierarchies**: Dev → Staging → Production secret propagation
- **Dynamic Target Discovery**: AWS Identity Center / Organizations-based account discovery

## Quick Start

### Basic Configuration

```yaml
# config.yaml
vault:
  address: https://vault.example.com/
  namespace: eng/data-platform
  auth:
    approle:
      role_id: ${VAULT_ROLE_ID}
      secret_id: ${VAULT_SECRET_ID}

aws:
  region: us-east-1
  control_tower:
    enabled: true
    execution_role:
      name: AWSControlTowerExecution

sources:
  analytics:
    vault:
      mount: analytics
  analytics-engineers:
    vault:
      mount: analytics-engineers

merge_store:
  vault:
    mount: merged-secrets

targets:
  Serverless_Stg:
    account_id: "111111111111"
    imports:
      - analytics
      - analytics-engineers
  
  Serverless_Prod:
    account_id: "222222222222"
    imports:
      - Serverless_Stg  # Inherits from Stg!
```

### Running the Pipeline

```bash
# Full pipeline (merge + sync)
vss pipeline --config config.yaml

# Dry run
vss pipeline --config config.yaml --dry-run

# Specific targets
vss pipeline --config config.yaml --targets Serverless_Stg

# Merge only (no AWS sync)
vss pipeline --config config.yaml --merge-only

# Validate configuration
vss validate --config config.yaml

# Show dependency graph
vss graph --config config.yaml
```

## AWS Execution Context

### Understanding Execution Context

The pipeline needs to run FROM an AWS account that can assume roles INTO target accounts. There are three supported patterns:

#### 1. Management Account (Not Recommended)

The AWS Organizations management account has implicit trust from all member accounts via `OrganizationAccountAccessRole`.

```yaml
aws:
  execution_context:
    type: management_account
    account_id: "123456789012"
```

⚠️ **Warning**: Running from the management account is not recommended for security reasons.

#### 2. Delegated Administrator (Recommended)

A member account that has been granted delegated administrator permissions.

```yaml
aws:
  execution_context:
    type: delegated_admin
    account_id: "987654321098"
    delegation:
      services:
        - sso.amazonaws.com
        - organizations.amazonaws.com
```

To set up delegation:

```bash
# From management account
aws organizations register-delegated-administrator \
  --account-id 987654321098 \
  --service-principal sso.amazonaws.com
```

#### 3. Hub Account (Custom)

A designated "secrets hub" account with custom cross-account roles.

```yaml
aws:
  execution_context:
    type: hub_account
    account_id: "111111111111"
    custom_role_pattern: "arn:aws:iam::{{.AccountID}}:role/SecretsHubAccess"
```

This requires deploying custom roles to all target accounts (via StackSets, AFT, etc.).

### Control Tower Integration

When running in a Control Tower environment:

```yaml
aws:
  control_tower:
    enabled: true
    execution_role:
      name: AWSControlTowerExecution  # Default
      # Or custom role:
      # name: CustomSecretsRole
      # path: /secrets/
    
    account_factory:
      enabled: true
      on_account_creation: true  # Sync secrets when new accounts are created
```

Control Tower provides the `AWSControlTowerExecution` role in all enrolled accounts, which is automatically trusted by the management account.

## Inheritance Model

### How Inheritance Works

Targets can import from:
1. **Sources**: Direct Vault mounts or AWS accounts
2. **Other Targets**: Inheriting merged secrets from another target

```yaml
targets:
  # Base target - imports from Vault sources
  Serverless_Stg:
    account_id: "111111111111"
    imports:
      - analytics           # Source
      - analytics-engineers # Source
  
  # Derived target - inherits from Stg
  Serverless_Prod:
    account_id: "222222222222"
    imports:
      - Serverless_Stg      # Inherits ALL secrets from Stg
  
  # Further inheritance
  livequery_demos:
    account_id: "333333333333"
    imports:
      - Serverless_Prod     # Inherits ALL secrets from Prod
```

### Inheritance Flow

```
analytics ─────────────┐
                       ├──► Serverless_Stg ──► Serverless_Prod ──► livequery_demos
analytics-engineers ───┘
```

### Execution Order

The pipeline automatically determines the correct execution order:

1. **Level 0**: Sources (no processing needed)
2. **Level 1**: Targets that import only from sources (Serverless_Stg)
3. **Level 2**: Targets that import from Level 1 (Serverless_Prod)
4. **Level 3**: Targets that import from Level 2 (livequery_demos)

Within each level, targets can be processed in parallel.

## Merge Store

The merge store is an intermediate location where secrets are aggregated before syncing to targets.

### Vault Merge Store (Recommended)

```yaml
merge_store:
  vault:
    mount: merged-secrets
```

Secrets for target "Serverless_Stg" are stored at `merged-secrets/Serverless_Stg/*`.

### S3 Merge Store

```yaml
merge_store:
  s3:
    bucket: my-secrets-bucket
    prefix: merged/
    kms_key_id: alias/secrets-key
```

## Dynamic Target Discovery

Dynamic targets are discovered at runtime from AWS Organizations and Identity Center.
They support **all the same options as static targets** plus discovery configuration.

### Identity Center Discovery

Discover accounts based on permission set assignments or group membership:

```yaml
dynamic_targets:
  analytics_engineer_sandboxes:
    discovery:
      identity_center:
        # Recommended: Permission sets map directly to accounts
        permission_set: "AnalyticsEngineerAccess"
        # Or by group membership:
        # group: "Analytics Engineers"
    imports:
      - analytics
      - analytics-engineers
    exclude:
      - "123456789012"  # Exclude production accounts
    # All static target options are supported:
    region: us-west-2
    secret_prefix: /sandbox/
    role_arn: "arn:aws:iam::{{.AccountID}}:role/SecretsAccess"
```

### Organizations Discovery

Discover accounts in specific OUs or with specific tags:

```yaml
dynamic_targets:
  dev_accounts:
    discovery:
      organizations:
        ou: "ou-xxxx-development"
        recursive: true  # Include accounts in child OUs
    imports:
      - dev-secrets

  sandbox_accounts:
    discovery:
      organizations:
        tags:
          Environment: sandbox
          Team: analytics
    imports:
      - analytics
    exclude:
      - "111111111111"  # Exclude specific accounts
```

### External Account List Discovery

Discover accounts from an external source (e.g., SSM Parameter Store):

```yaml
dynamic_targets:
  managed_accounts:
    discovery:
      accounts_list:
        source: "ssm:/platform/managed-account-ids"
    imports:
      - shared-secrets
```

### Dynamic Target Options

Dynamic targets support all static target options:

| Option | Description |
|--------|-------------|
| `region` | Override AWS region for all discovered accounts |
| `secret_prefix` | Prefix for secrets in target accounts |
| `role_arn` | Custom role ARN (supports `{{.AccountID}}` template) |
| `exclude` | List of account IDs to exclude from discovery |

## Pipeline Settings

```yaml
pipeline:
  merge:
    parallel: 4           # Max concurrent merge operations per level
  
  sync:
    parallel: 4           # Max concurrent sync operations
    delete_orphans: false # Remove secrets not in source
  
  dry_run: false          # Can be overridden with --dry-run
  continue_on_error: true # Don't fail entire pipeline on single target failure
```

## CI/CD Integration

### GitHub Actions

```yaml
name: secrets
on:
  schedule:
    - cron: '0,30 * * * *'
  workflow_dispatch:
    inputs:
      targets:
        description: 'Targets (comma-separated or "all")'
        default: 'all'
      dry_run:
        type: boolean
        default: false

jobs:
  sync:
    runs-on: ubuntu-latest
    permissions:
      id-token: write
      contents: read
    
    steps:
      - uses: actions/checkout@v4
      
      - uses: aws-actions/configure-aws-credentials@v4
        with:
          role-to-assume: ${{ secrets.AWS_OIDC_ROLE_ARN }}
          aws-region: us-east-1
      
      - name: Install vss
        run: |
          curl -sL https://github.com/jbcom/vault-secret-sync/releases/latest/download/vss_linux_amd64 \
            -o /usr/local/bin/vss && chmod +x /usr/local/bin/vss
      
      - name: Run Pipeline
        env:
          VAULT_ROLE_ID: ${{ secrets.VAULT_ROLE_ID }}
          VAULT_SECRET_ID: ${{ secrets.VAULT_SECRET_ID }}
        run: |
          vss pipeline \
            --config config.yaml \
            --targets "${{ inputs.targets || 'all' }}" \
            ${{ inputs.dry_run && '--dry-run' || '' }}
```

### GitLab CI

```yaml
secrets-sync:
  stage: deploy
  image: alpine
  before_script:
    - wget -O /usr/local/bin/vss https://github.com/jbcom/vault-secret-sync/releases/latest/download/vss_linux_amd64
    - chmod +x /usr/local/bin/vss
  script:
    - vss pipeline --config config.yaml
  only:
    - schedules
    - web
```

## Troubleshooting

### Validate Configuration

```bash
vss validate --config config.yaml
vss validate --config config.yaml --check-aws
```

### View Dependency Graph

```bash
vss graph --config config.yaml
vss graph --config config.yaml --format dot | dot -Tpng -o graph.png
```

### Check AWS Context

```bash
vss context
vss context --config config.yaml
```

### Debug Logging

```bash
vss pipeline --config config.yaml --log-level debug --log-format json
```

### Common Issues

1. **Circular Dependency Error**
   ```
   Error: circular dependency detected involving "TargetA"
   ```
   Check your target imports for cycles.

2. **Role Assumption Failed**
   ```
   Error: failed to assume role arn:aws:iam::123456789012:role/AWSControlTowerExecution
   ```
   - Verify the role exists in the target account
   - Check trust policy allows assumption from your execution account
   - Verify Control Tower enrollment status

3. **Identity Center Access Denied**
   ```
   Error: no access to Identity Center from this execution context
   ```
   - You need to run from management account or delegated SSO admin
   - Set up delegation: `aws organizations register-delegated-administrator --service-principal sso.amazonaws.com`

## Migration from terraform-aws-secretsmanager

If you're migrating from the Terraform-based pipeline, use the `vss migrate` command:

```bash
# Migrate from terraform-aws-secretsmanager format
vss migrate --from terraform-secretsmanager \
            --targets config/targets.yaml \
            --secrets config/secrets.yaml \
            --accounts config/accounts.yaml \
            --output pipeline-config.yaml

# Optional: specify Vault address and merge mount
vss migrate --from terraform-secretsmanager \
            --targets config/targets.yaml \
            --secrets config/secrets.yaml \
            --accounts config/accounts.yaml \
            --vault-addr https://vault.example.com \
            --vault-merge-mount secret/merged \
            --output pipeline-config.yaml
```

### Expected Input Format

**targets.yaml**:
```yaml
targets:
  - name: production
    description: Production account
    imports: []
    secrets:
      - analytics_secrets
      - shared_config
  - name: staging
    imports:
      - production
    secrets:
      - staging_overrides
```

**secrets.yaml**:
```yaml
secrets:
  - name: analytics_secrets
    vault_path: analytics/config
    vault_mount: secret
  - name: shared_config
    vault_path: shared/settings
```

**accounts.yaml**:
```yaml
accounts:
  - name: production
    account_id: "123456789012"
    region: us-east-1
  - name: staging
    account_id: "234567890123"
    region: us-west-2
```

### Post-Migration Steps

1. Review the generated config file
2. Add Vault authentication (token, AppRole, etc.)
3. Validate: `vss validate --config pipeline-config.yaml`
4. Dry run: `vss pipeline --config pipeline-config.yaml --dry-run`

Key differences from the Terraform-based approach:
- No Terraform state required
- No Lambda functions needed
- Vault merge store eliminates intermediate S3 storage (or use S3 merge store if preferred)
- Single binary, runs anywhere
