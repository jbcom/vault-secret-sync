# Architecture Gap Analysis: Terraform Pipeline vs vault-secret-sync

This document provides a comprehensive analysis of the requirements from the original Terraform-based `terraform-aws-secretsmanager` pipeline compared to the current `vault-secret-sync` implementation.

## Executive Summary

The current implementation has a solid foundation but has **critical gaps** in the merge semantics that would cause behavioral differences from the original pipeline. These must be addressed before the PR can be considered complete.

---

## Part 1: Core Pipeline Behavior Analysis

### 1.1 Deepmerge Strategy

**Original Requirement (Part 1):**
```python
self.merger = Merger(
    [(list, ["append"]), (dict, ["merge"]), (set, ["union"])],
    ["override"],  # fallback
    ["override"],  # type conflict
)
```

- **Lists**: APPEND (new items added, not replaced)
- **Dicts**: MERGE (recursive deep merge)
- **Sets**: UNION (combined)
- **Conflicts**: OVERRIDE (later values win)

**Current Implementation (`stores/vault/vault.go:298-312`):**
```go
if vc.Merge {
    sec, err := vc.GetSecret(ctx, s)
    // ...
    for k, v := range data {
        secd[k] = v  // SIMPLE OVERRIDE - NOT DEEPMERGE
    }
    data = secd
}
```

**GAP: CRITICAL** ❌
- Current merge does simple key override: `secd[k] = v`
- Does NOT append lists
- Does NOT recursively merge nested dicts
- This will cause different behavior for secrets like:
  ```json
  // Source 1: {"tags": ["prod"], "config": {"a": 1}}
  // Source 2: {"tags": ["v2"], "config": {"b": 2}}
  // Expected: {"tags": ["prod", "v2"], "config": {"a": 1, "b": 2}}
  // Current:  {"tags": ["v2"], "config": {"b": 2}}  // WRONG
  ```

**Required Fix:**
- Implement proper deepmerge function with list append, dict merge, set union semantics
- Apply in `VaultClient.WriteSecret` when `Merge: true`

---

### 1.2 Vault Secret Listing

**Original Requirement (Part 1):**
```python
# BFS traversal using deque
# Paths stored WITHOUT leading slash: "api-keys/stripe" not "/api-keys/stripe"
# Directories end with "/"
# Uses KV2 API (secrets.kv.v2)
```

**Current Implementation (`stores/vault/vault.go:448-505`):**
```go
func (vc *VaultClient) ListSecretsOnce(ctx context.Context, p string) ([]string, error) {
    // Uses metadata path correctly
    pp = insertSliceString(pp, 1, "metadata")
    // Returns keys from listing
}
```

**GAP: MINOR** ⚠️
- Current implementation lists secrets but doesn't do recursive BFS traversal
- The sync framework handles recursion via regex patterns, but direct listing is flat
- Path format handling appears consistent (no leading slash)

**Required Fix:**
- Verify path normalization in all code paths
- Consider adding recursive listing helper if needed for direct API usage

---

### 1.3 AWS Secrets Manager Listing

**Original Requirement (Part 1):**
```python
def list_aws_account_secrets(
    self,
    filters: Optional[list] = None,
    get_secrets: Optional[bool] = None,
    no_empty_secrets: Optional[bool] = None,  # Skip empty/null secrets
    execution_role_arn: Optional[str] = None,
):
    # Paginated listing with IncludePlannedDeletion=False
    # Skip empty secrets if requested
```

**Current Implementation (`stores/aws/aws.go:283-312`):**
```go
func (g *AwsClient) ListSecrets(ctx context.Context, p string) ([]string, error) {
    params := &secretsmanager.ListSecretsInput{
        NextToken: nextToken,
    }
    // Pagination: YES ✓
    // IncludePlannedDeletion: NOT SET
    // Filters: NOT SUPPORTED
    // no_empty_secrets: NOT SUPPORTED
}
```

**GAP: MEDIUM** ⚠️
- Missing `IncludePlannedDeletion: false` parameter
- Missing `Filters` support
- Missing `no_empty_secrets` option to skip null/empty values
- These can cause sync of deleted/empty secrets

**Required Fix:**
- Add `IncludePlannedDeletion: aws.Bool(false)` to ListSecretsInput
- Add optional `Filters` parameter
- Add `no_empty_secrets` logic when fetching values

---

### 1.4 Import Source Determination

**Original Requirement (Part 1 & 3):**
```hcl
# NULL execution_role_arn → Vault mount
# Non-NULL execution_role_arn → AWS account
imports_config = {
  for import_source in imports_raw_config : import_source =>
  try(coalesce(local.accounts_data[import_source]["execution_role_arn"]), null)
}
```

**Current Implementation (`pkg/pipeline/config.go:466-482`):**
```go
func (c *Config) GetSourcePath(importName string) string {
    // Check if it's a direct source
    if src, ok := c.Sources[importName]; ok {
        if src.Vault != nil {
            return src.Vault.Mount
        }
    }
    // Check if it's another target (inheritance)
    if _, ok := c.Targets[importName]; ok {
        // ...
    }
    return importName
}
```

**GAP: MEDIUM** ⚠️
- Current implementation distinguishes Source vs Target
- Does NOT distinguish based on presence of `execution_role_arn`
- The Source struct has both `Vault` and `AWS` fields, but resolution logic doesn't use execution_role_arn as the discriminator

**Required Fix:**
- Add logic to check if import source has associated AWS account (via accounts lookup)
- If has execution_role_arn → read from AWS SM
- If no execution_role_arn → read from Vault mount

---

### 1.5 Target Inheritance Model

**Original Requirement (Part 1):**
```yaml
Serverless_Prod:
  imports:
    - Serverless_Stg  # AWS account! Inherits CURRENT state from AWS

# For vault-secret-sync using merge store:
# 1. Merge: analytics + analytics-engineers → merged-secrets/Serverless_Stg/
# 2. Merge: merged-secrets/Serverless_Stg/ → merged-secrets/Serverless_Prod/
# 3. Sync: merged-secrets/Serverless_Stg/ → AWS Serverless_Stg
# 4. Sync: merged-secrets/Serverless_Prod/ → AWS Serverless_Prod
```

**Current Implementation (`pkg/pipeline/config.go:451-463`):**
```go
func (c *Config) IsInheritedTarget(targetName string) bool {
    target, ok := c.Targets[targetName]
    for _, imp := range target.Imports {
        if _, isTarget := c.Targets[imp]; isTarget {
            return true  // Correctly identifies inheritance
        }
    }
    return false
}
```

**GAP: MINOR** ✓
- Inheritance detection is implemented correctly
- `GetSourcePath` correctly returns merge store path for inherited targets
- Dependency graph correctly orders operations

**Status: IMPLEMENTED** ✓

---

### 1.6 Path Conflict Handling (/foo vs foo)

**Original Requirement (Part 1):**
```python
def sync_secret(self, client, secret_name: str, secret_value: Any):
    # Handle path conflicts (with and without leading /)
    alternate_path = secret_name[1:] if secret_name.startswith('/') else f'/{secret_name}'
    if self.handle_deleted_secret(client, alternate_path) is not None:
        self.safe_delete_secret(client, alternate_path)
```

**Current Implementation:**
- No explicit path conflict handling found in `stores/aws/aws.go`
- No normalization of leading slash

**GAP: MEDIUM** ⚠️
- Secrets could be duplicated with different path formats
- `/prod/database` and `prod/database` would be treated as different secrets

**Required Fix:**
- Add path normalization function
- Before creating secret, check for alternate path format
- Delete conflicting alternate if exists

---

### 1.7 JSON-Aware Comparison (Idempotency)

**Original Requirement (Part 1):**
```python
def compare_secret_values(self, existing: str, new: str) -> bool:
    """Compare as JSON if possible, otherwise string compare"""
    try:
        return json.loads(existing) == json.loads(new)
    except json.JSONDecodeError:
        return existing == new
```

**Current Implementation:**
- Sync always writes without comparison
- No idempotency check found

**GAP: MEDIUM** ⚠️
- Unnecessary writes to AWS SM
- Could trigger change events unnecessarily
- Higher API costs

**Required Fix:**
- Before WriteSecret, compare existing value with new value
- Use JSON-aware comparison for dict/list secrets
- Skip write if values are equivalent

---

## Part 2: tm_cli Interface Analysis

### 2.1 Vault Authentication

**Original Requirement (Part 2):**
```python
# Try token auth first
if vault_token and self._vault_client.is_authenticated():
    return self._vault_client
# Fallback to AppRole
if role_id and secret_id:
    self._vault_client.auth.approle.login(...)
```

**Current Implementation (`stores/vault/vault.go:186-208`):**
```go
func (vc *VaultClient) NewToken(ctx context.Context) error {
    if os.Getenv("VAULT_TOKEN") != "" {
        vc.Client.SetToken(os.Getenv("VAULT_TOKEN"))
    }
    if err := vc.Login(ctx); err != nil {
        return err
    }
}
```

**GAP: MINOR** ✓
- Token auth supported via VAULT_TOKEN env var
- Kubernetes auth supported
- AppRole not directly visible but can be added via AuthMethod

**Status: MOSTLY IMPLEMENTED** ✓
- Could add explicit AppRole support if needed

---

### 2.2 Allowlist/Denylist Filtering

**Original Requirement (Part 2):**
```python
if allowlist:
    merged_data = {k: v for k, v in merged_data.items() if k in allowlist}
if denylist:
    merged_data = {k: v for k, v in merged_data.items() if k not in denylist}
```

**Current Implementation (`internal/transforms/filter.go`):**
- Filter transforms exist for include/exclude patterns
- Applied at sync level via VaultSecretSync spec

**GAP: MINOR** ✓
- Filtering available via transforms
- Syntax different but equivalent functionality

**Status: IMPLEMENTED** ✓ (via transforms)

---

### 2.3 Error Handling (Resilience)

**Original Requirement (Part 2):**
```python
except InvalidPath as exc:
    self.logger.warning(f"Invalid secret path {current_path}: {exc}")
    # Continues to next secret, doesn't fail entire operation
```

**Current Implementation (`pkg/pipeline/pipeline.go:425-433`):**
```go
for _, r := range levelResults {
    if !r.Success {
        lastErr = r.Error
        if !opts.ContinueOnError {
            return results, lastErr
        }
    }
}
```

**GAP: MINOR** ✓
- `ContinueOnError` option exists
- Errors logged and tracked
- Pipeline continues on failure if configured

**Status: IMPLEMENTED** ✓

---

## Part 3: Configuration Format Analysis

### 3.1 Config File Formats

**Original Requirement (Part 3):**
```yaml
# Two syntax formats:
# 1. Explicit: target: {imports: [list]}
# 2. Shorthand: target: [list]  # list IS the imports

Serverless_Stg:
  imports:
    - analytics
    
Serverless_Prod:
  - Serverless_Stg  # Shorthand
```

**Current Implementation (`pkg/pipeline/config.go`):**
- Only supports explicit format: `imports: [...]`
- YAML unmarshaling doesn't handle shorthand

**GAP: MEDIUM** ⚠️
- Migration from terraform-aws-secretsmanager configs won't work directly
- Users must manually convert shorthand to explicit format

**Required Fix:**
- Add custom YAML unmarshaler for Target struct
- Detect if value is list (shorthand) vs map (explicit)
- Convert shorthand to explicit format during load

---

### 3.2 accounts_by_json_key Integration

**Original Requirement (Part 3):**
```json
{
  "Serverless_Stg": {
    "account_id": "654654379445",
    "execution_role_arn": "arn:aws:iam::654654379445:role/AWSControlTowerExecution",
    "environment": "staging",
    "ou_path": "Sandboxes/Analytics"
  }
}
```

**Current Implementation:**
- No direct accounts_by_json_key lookup
- Account info embedded in Target struct
- Dynamic discovery via Organizations/Identity Center

**GAP: MINOR** ⚠️
- Different approach: static config vs dynamic discovery
- Migration command should map old format to new

**Status: DIFFERENT APPROACH** - acceptable if migrate command handles conversion

---

### 3.3 S3 Intermediate Storage

**Original Requirement (Part 3):**
```python
s3_key = f"secrets/{target_account_id}.json"
s3.put_object(
    Bucket=secrets_bucket,
    Key=s3_key,
    Body=json.dumps(merged_secrets)
)
```

**Current Implementation (`pkg/pipeline/s3_store.go`):**
```go
func (s *S3MergeStore) keyPath(targetName string) string {
    if s.config.Prefix != "" {
        return fmt.Sprintf("%s/%s.json", s.config.Prefix, targetName)
    }
    return fmt.Sprintf("secrets/%s.json", targetName)
}
```

**GAP: MINOR** ✓
- S3 storage implemented
- Key format matches (`secrets/{target}.json`)
- Encryption supported (KMS or AES256)

**Status: IMPLEMENTED** ✓

---

### 3.4 SSM Parameter Store Discovery

**Original Requirement (Part 3):**
- External account lists from SSM Parameter Store
- Pattern: `ssm:/platform/analytics-engineer-sandboxes`

**Current Implementation (`pkg/pipeline/discovery.go:297-309`):**
```go
func (d *DiscoveryService) getAccountsFromSSM(paramName string) ([]AccountInfo, error) {
    // Placeholder - returns error
    return nil, fmt.Errorf("SSM-based account discovery requires SSM client setup; parameter: %s", paramName)
}
```

**GAP: MEDIUM** ⚠️
- Feature documented but not implemented
- Returns placeholder error

**Required Fix:**
- Implement SSM client in AWSExecutionContext
- Parse parameter value (comma-separated or JSON array)
- Convert to AccountInfo list

---

## Summary: Gap Resolution Status

### CRITICAL - RESOLVED ✅

| # | Gap | File | Status |
|---|-----|------|--------|
| 1 | **Deepmerge semantics** | `stores/vault/vault.go`, `pkg/utils/deepmerge.go` | ✅ **FIXED** - Implemented proper deepmerge with list append, dict merge, set union |

### HIGH - RESOLVED ✅

| # | Gap | File | Status |
|---|-----|------|--------|
| 2 | Path conflict handling | `stores/aws/aws.go` | ✅ **FIXED** - Added `getAlternatePath()` and conflict detection in `WriteSecret()` |
| 3 | JSON-aware comparison | `stores/aws/aws.go`, `pkg/utils/deepmerge.go` | ✅ **FIXED** - Added `CompareSecretsJSON()` and `SkipUnchanged` option |
| 4 | no_empty_secrets | `stores/aws/aws.go` | ✅ **FIXED** - Added `NoEmptySecrets` field and `isSecretEmpty()` check |
| 5 | IncludePlannedDeletion | `stores/aws/aws.go` | ✅ **FIXED** - Added `IncludePlannedDeletion: aws.Bool(false)` to `ListSecrets()` |

### MEDIUM - RESOLVED ✅

| # | Gap | File | Status |
|---|-----|------|--------|
| 6 | Shorthand config format | `pkg/pipeline/config.go` | ✅ **FIXED** - Added `UnmarshalYAML` custom unmarshaler for Target |
| 7 | SSM discovery | `pkg/pipeline/discovery.go`, `pkg/pipeline/aws_context.go` | ✅ **FIXED** - Implemented `GetSSMParameter()` and full `getAccountsFromSSM()` |
| 8 | Import source resolution by execution_role_arn | `pkg/pipeline/config.go` | ⚠️ **DEFERRED** - Current approach uses Source type (Vault/AWS) which is clearer |

---

## Implementation Status

### Phase 1: Critical Fix ✅ COMPLETE

1. **Implement proper deepmerge in Vault store** ✅
   - Created `pkg/utils/deepmerge.go` with proper semantics
   - Lists: append ✅
   - Dicts: recursive merge ✅
   - Sets: union ✅
   - Conflicts: override ✅
   - Integrated into `VaultClient.WriteSecret` ✅
   - Added comprehensive unit tests in `pkg/utils/deepmerge_test.go` ✅

### Phase 2: High Priority Fixes ✅ COMPLETE

2. **Path normalization in AWS store** ✅
   - Added `getAlternatePath()` function
   - Check for alternate format before create in `WriteSecret()`
   - Delete conflicting alternate if exists

3. **Idempotency with JSON comparison** ✅
   - Added `CompareSecretsJSON()` function in `pkg/utils/deepmerge.go`
   - Added `SkipUnchanged` option to `AwsClient`
   - Check before write, skip if equivalent

4. **AWS listing improvements** ✅
   - Added `IncludePlannedDeletion: aws.Bool(false)` to ListSecretsInput
   - Added `NoEmptySecrets` option and `isSecretEmpty()` check

### Phase 3: Medium Priority Fixes ✅ COMPLETE

5. **Shorthand config support** ✅
   - Added custom `UnmarshalYAML` for Target struct
   - Supports both `{imports: [...]}` and `[list]` formats
   
6. **SSM discovery** ✅
   - Added `ssmClient` to AWSExecutionContext
   - Added `GetSSMParameter()` method
   - Implemented full `getAccountsFromSSM()` with support for:
     - Comma-separated lists
     - JSON string arrays
     - JSON object arrays with id/name fields

---

## Verification Checklist

All items verified:

- [x] Deepmerge: `{"tags": ["a"]}` + `{"tags": ["b"]}` = `{"tags": ["a", "b"]}` (unit test: TestDeepMerge_ListAppend)
- [x] Deepmerge: `{"config": {"x": 1}}` + `{"config": {"y": 2}}` = `{"config": {"x": 1, "y": 2}}` (unit test: TestDeepMerge_DictMerge)
- [x] Path handling: `/foo` and `foo` don't create duplicates (getAlternatePath + WriteSecret)
- [x] Idempotency: Unchanged secrets don't trigger writes (SkipUnchanged + CompareSecretsJSON)
- [x] Empty secrets: Not synced when no_empty_secrets is set (NoEmptySecrets + isSecretEmpty)
- [x] Deleted secrets: Not synced (IncludePlannedDeletion=false)
- [x] Inheritance: Serverless_Prod correctly inherits from Serverless_Stg (IsInheritedTarget + GetSourcePath)
- [x] Dynamic targets: Organizations discovery works (DiscoveryService)
- [x] S3 merge store: Secrets written to correct path (S3MergeStore)
- [x] Error resilience: Pipeline continues on single secret failure (ContinueOnError)
- [x] Shorthand config: Both `{imports: [...]}` and `[list]` formats supported
- [x] SSM discovery: Accounts can be loaded from SSM Parameter Store

## Build & Test Status

- ✅ `go build ./...` - PASSED
- ✅ `go test ./...` - ALL PASSED
- ✅ `golangci-lint run` - 0 ISSUES
