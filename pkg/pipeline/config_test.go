package pipeline

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfig(t *testing.T) {
	// Create temp config file
	configContent := `
log:
  level: debug
  format: json

vault:
  address: https://vault.example.com/
  namespace: eng/data-platform
  auth:
    approle:
      mount: approle
      role_id: ${VAULT_ROLE_ID}
      secret_id: ${VAULT_SECRET_ID}

aws:
  region: us-east-1
  execution_context:
    type: management_account
    account_id: "123456789012"
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
      - Serverless_Stg
  livequery_demos:
    account_id: "222222222222"
    imports:
      - Serverless_Prod

pipeline:
  merge:
    parallel: 4
  sync:
    parallel: 4
    delete_orphans: false
`

	tmpfile, err := os.CreateTemp("", "config-*.yaml")
	require.NoError(t, err)
	defer os.Remove(tmpfile.Name())

	_, err = tmpfile.WriteString(configContent)
	require.NoError(t, err)
	tmpfile.Close()

	// Set env vars for expansion test
	os.Setenv("VAULT_ROLE_ID", "test-role-id")
	os.Setenv("VAULT_SECRET_ID", "test-secret-id")
	defer os.Unsetenv("VAULT_ROLE_ID")
	defer os.Unsetenv("VAULT_SECRET_ID")

	// Load config
	cfg, err := LoadConfig(tmpfile.Name())
	require.NoError(t, err)

	// Validate structure
	assert.Equal(t, "debug", cfg.Log.Level)
	assert.Equal(t, "https://vault.example.com/", cfg.Vault.Address)
	assert.Equal(t, "eng/data-platform", cfg.Vault.Namespace)
	
	// Check env var expansion
	assert.Equal(t, "test-role-id", cfg.Vault.Auth.AppRole.RoleID)
	assert.Equal(t, "test-secret-id", cfg.Vault.Auth.AppRole.SecretID)

	// Check AWS config
	assert.Equal(t, "us-east-1", cfg.AWS.Region)
	assert.Equal(t, ExecutionContextManagement, cfg.AWS.ExecutionContext.Type)
	assert.True(t, cfg.AWS.ControlTower.Enabled)
	assert.Equal(t, "AWSControlTowerExecution", cfg.AWS.ControlTower.ExecutionRole.Name)

	// Check sources
	assert.Len(t, cfg.Sources, 2)
	assert.Equal(t, "analytics", cfg.Sources["analytics"].Vault.Mount)

	// Check targets
	assert.Len(t, cfg.Targets, 3)
	assert.Equal(t, "111111111111", cfg.Targets["Serverless_Stg"].AccountID)
	assert.Equal(t, []string{"analytics", "analytics-engineers"}, cfg.Targets["Serverless_Stg"].Imports)
	assert.Equal(t, []string{"Serverless_Stg"}, cfg.Targets["Serverless_Prod"].Imports)
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid config",
			config: Config{
				Vault: VaultConfig{Address: "https://vault.example.com"},
				Sources: map[string]Source{
					"analytics": {Vault: &VaultSource{Mount: "analytics"}},
				},
				MergeStore: MergeStoreConfig{Vault: &MergeStoreVault{Mount: "merged"}},
				Targets: map[string]Target{
					"Stg": {AccountID: "111111111111", Imports: []string{"analytics"}},
				},
			},
			wantErr: false,
		},
		{
			name: "missing vault address",
			config: Config{
				Sources: map[string]Source{
					"analytics": {Vault: &VaultSource{Mount: "analytics"}},
				},
				MergeStore: MergeStoreConfig{Vault: &MergeStoreVault{Mount: "merged"}},
				Targets: map[string]Target{
					"Stg": {AccountID: "111111111111", Imports: []string{"analytics"}},
				},
			},
			wantErr: true,
			errMsg:  "vault.address is required",
		},
		{
			name: "missing merge store",
			config: Config{
				Vault: VaultConfig{Address: "https://vault.example.com"},
				Sources: map[string]Source{
					"analytics": {Vault: &VaultSource{Mount: "analytics"}},
				},
				Targets: map[string]Target{
					"Stg": {AccountID: "111111111111", Imports: []string{"analytics"}},
				},
			},
			wantErr: true,
			errMsg:  "merge_store must specify",
		},
		{
			name: "no targets",
			config: Config{
				Vault:      VaultConfig{Address: "https://vault.example.com"},
				MergeStore: MergeStoreConfig{Vault: &MergeStoreVault{Mount: "merged"}},
			},
			wantErr: true,
			errMsg:  "at least one target",
		},
		{
			name: "target missing account_id",
			config: Config{
				Vault: VaultConfig{Address: "https://vault.example.com"},
				Sources: map[string]Source{
					"analytics": {Vault: &VaultSource{Mount: "analytics"}},
				},
				MergeStore: MergeStoreConfig{Vault: &MergeStoreVault{Mount: "merged"}},
				Targets: map[string]Target{
					"Stg": {Imports: []string{"analytics"}},
				},
			},
			wantErr: true,
			errMsg:  "account_id is required",
		},
		{
			name: "invalid import reference",
			config: Config{
				Vault: VaultConfig{Address: "https://vault.example.com"},
				Sources: map[string]Source{
					"analytics": {Vault: &VaultSource{Mount: "analytics"}},
				},
				MergeStore: MergeStoreConfig{Vault: &MergeStoreVault{Mount: "merged"}},
				Targets: map[string]Target{
					"Stg": {AccountID: "111111111111", Imports: []string{"nonexistent"}},
				},
			},
			wantErr: true,
			errMsg:  "import \"nonexistent\" not found",
		},
		{
			name: "valid S3 merge store",
			config: Config{
				Vault: VaultConfig{Address: "https://vault.example.com"},
				Sources: map[string]Source{
					"analytics": {Vault: &VaultSource{Mount: "analytics"}},
				},
				MergeStore: MergeStoreConfig{S3: &MergeStoreS3{Bucket: "my-bucket", Prefix: "secrets/"}},
				Targets: map[string]Target{
					"Stg": {AccountID: "111111111111", Imports: []string{"analytics"}},
				},
			},
			wantErr: false,
		},
		{
			name: "S3 merge store missing bucket",
			config: Config{
				Vault: VaultConfig{Address: "https://vault.example.com"},
				Sources: map[string]Source{
					"analytics": {Vault: &VaultSource{Mount: "analytics"}},
				},
				MergeStore: MergeStoreConfig{S3: &MergeStoreS3{Prefix: "secrets/"}},
				Targets: map[string]Target{
					"Stg": {AccountID: "111111111111", Imports: []string{"analytics"}},
				},
			},
			wantErr: true,
			errMsg:  "merge_store.s3.bucket is required",
		},
		{
			name: "valid dynamic target",
			config: Config{
				Vault: VaultConfig{Address: "https://vault.example.com"},
				Sources: map[string]Source{
					"analytics": {Vault: &VaultSource{Mount: "analytics"}},
				},
				MergeStore: MergeStoreConfig{Vault: &MergeStoreVault{Mount: "merged"}},
				DynamicTargets: map[string]DynamicTarget{
					"sandboxes": {
						Discovery: DiscoveryConfig{
							IdentityCenter: &IdentityCenterDiscovery{Group: "Engineers"},
						},
						Imports: []string{"analytics"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "dynamic target missing discovery config",
			config: Config{
				Vault: VaultConfig{Address: "https://vault.example.com"},
				Sources: map[string]Source{
					"analytics": {Vault: &VaultSource{Mount: "analytics"}},
				},
				MergeStore: MergeStoreConfig{Vault: &MergeStoreVault{Mount: "merged"}},
				DynamicTargets: map[string]DynamicTarget{
					"sandboxes": {
						Discovery: DiscoveryConfig{},
						Imports:   []string{"analytics"},
					},
				},
			},
			wantErr: true,
			errMsg:  "must specify identity_center, organizations, or accounts_list discovery",
		},
		{
			name: "dynamic target with accounts_list",
			config: Config{
				Vault: VaultConfig{Address: "https://vault.example.com"},
				Sources: map[string]Source{
					"analytics": {Vault: &VaultSource{Mount: "analytics"}},
				},
				MergeStore: MergeStoreConfig{Vault: &MergeStoreVault{Mount: "merged"}},
				DynamicTargets: map[string]DynamicTarget{
					"sandboxes": {
						Discovery: DiscoveryConfig{
							AccountsList: &AccountsListDiscovery{Source: "ssm:/platform/sandboxes"},
						},
						Imports: []string{"analytics"},
					},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGetRoleARN(t *testing.T) {
	tests := []struct {
		name      string
		config    Config
		accountID string
		expected  string
	}{
		{
			name: "control tower role",
			config: Config{
				AWS: AWSConfig{
					ControlTower: ControlTowerConfig{
						Enabled: true,
						ExecutionRole: ExecutionRoleConfig{
							Name: "AWSControlTowerExecution",
						},
					},
				},
			},
			accountID: "123456789012",
			expected:  "arn:aws:iam::123456789012:role/AWSControlTowerExecution",
		},
		{
			name: "control tower role with path",
			config: Config{
				AWS: AWSConfig{
					ControlTower: ControlTowerConfig{
						Enabled: true,
						ExecutionRole: ExecutionRoleConfig{
							Name: "CustomRole",
							Path: "/secrets/",
						},
					},
				},
			},
			accountID: "123456789012",
			expected:  "arn:aws:iam::123456789012:role/secrets/CustomRole",
		},
		{
			name: "custom role pattern",
			config: Config{
				AWS: AWSConfig{
					ExecutionContext: ExecutionContextConfig{
						CustomRolePattern: "arn:aws:iam::{{.AccountID}}:role/SecretsHub",
					},
				},
			},
			accountID: "123456789012",
			expected:  "arn:aws:iam::123456789012:role/SecretsHub",
		},
		{
			name: "explicit target role",
			config: Config{
				AWS: AWSConfig{
					ControlTower: ControlTowerConfig{
						Enabled: true,
						ExecutionRole: ExecutionRoleConfig{
							Name: "AWSControlTowerExecution",
						},
					},
				},
				Targets: map[string]Target{
					"Special": {
						AccountID: "123456789012",
						RoleARN:   "arn:aws:iam::123456789012:role/SpecialRole",
					},
				},
			},
			accountID: "123456789012",
			expected:  "arn:aws:iam::123456789012:role/SpecialRole",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.config.GetRoleARN(tt.accountID)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsInheritedTarget(t *testing.T) {
	cfg := Config{
		Sources: map[string]Source{
			"analytics": {Vault: &VaultSource{Mount: "analytics"}},
		},
		Targets: map[string]Target{
			"Stg":  {AccountID: "111111111111", Imports: []string{"analytics"}},
			"Prod": {AccountID: "222222222222", Imports: []string{"Stg"}},
		},
	}

	assert.False(t, cfg.IsInheritedTarget("Stg"))  // imports only source
	assert.True(t, cfg.IsInheritedTarget("Prod"))  // imports another target
}

func TestGetSourcePath(t *testing.T) {
	cfg := Config{
		Sources: map[string]Source{
			"analytics": {Vault: &VaultSource{Mount: "analytics"}},
		},
		MergeStore: MergeStoreConfig{Vault: &MergeStoreVault{Mount: "merged-secrets"}},
		Targets: map[string]Target{
			"Stg":  {AccountID: "111111111111", Imports: []string{"analytics"}},
			"Prod": {AccountID: "222222222222", Imports: []string{"Stg"}},
		},
	}

	// Direct source
	assert.Equal(t, "analytics", cfg.GetSourcePath("analytics"))
	
	// Inherited target
	assert.Equal(t, "merged-secrets/Stg", cfg.GetSourcePath("Stg"))
}

func TestIsValidAWSAccountID(t *testing.T) {
	tests := []struct {
		name      string
		accountID string
		valid     bool
	}{
		{"valid 12 digits", "123456789012", true},
		{"valid all zeros", "000000000000", true},
		{"too short", "12345678901", false},
		{"too long", "1234567890123", false},
		{"contains letters", "12345678901a", false},
		{"contains special chars", "123456789-12", false},
		{"empty", "", false},
		{"spaces", "123456789 12", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidAWSAccountID(tt.accountID)
			assert.Equal(t, tt.valid, result)
		})
	}
}

func TestConfigValidateAccountIDFormat(t *testing.T) {
	// Test that invalid account IDs are rejected
	cfg := Config{
		Vault: VaultConfig{Address: "https://vault.example.com"},
		Sources: map[string]Source{
			"analytics": {Vault: &VaultSource{Mount: "analytics"}},
		},
		MergeStore: MergeStoreConfig{Vault: &MergeStoreVault{Mount: "merged"}},
		Targets: map[string]Target{
			"Stg": {AccountID: "invalid", Imports: []string{"analytics"}},
		},
	}

	err := cfg.Validate()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid account_id format")
}
