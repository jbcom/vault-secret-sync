// Package pipeline provides unified configuration and orchestration for secrets syncing pipelines.
// It supports AWS Control Tower / Organizations patterns for multi-account secrets management.
package pipeline

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

// Config represents the unified pipeline configuration
type Config struct {
	Log        LogConfig        `mapstructure:"log" yaml:"log"`
	Vault      VaultConfig      `mapstructure:"vault" yaml:"vault"`
	AWS        AWSConfig        `mapstructure:"aws" yaml:"aws"`
	Sources    map[string]Source `mapstructure:"sources" yaml:"sources"`
	MergeStore MergeStoreConfig `mapstructure:"merge_store" yaml:"merge_store"`
	Targets    map[string]Target `mapstructure:"targets" yaml:"targets"`
	DynamicTargets map[string]DynamicTarget `mapstructure:"dynamic_targets" yaml:"dynamic_targets"`
	Pipeline   PipelineSettings `mapstructure:"pipeline" yaml:"pipeline"`
}

// LogConfig controls logging behavior
type LogConfig struct {
	Level  string `mapstructure:"level" yaml:"level"`
	Format string `mapstructure:"format" yaml:"format"`
}

// VaultConfig configures Vault connection and authentication
type VaultConfig struct {
	Address   string          `mapstructure:"address" yaml:"address"`
	Namespace string          `mapstructure:"namespace" yaml:"namespace"`
	Auth      VaultAuthConfig `mapstructure:"auth" yaml:"auth"`
}

// VaultAuthConfig supports multiple authentication methods
type VaultAuthConfig struct {
	AppRole    *AppRoleAuth    `mapstructure:"approle" yaml:"approle"`
	Token      *TokenAuth      `mapstructure:"token" yaml:"token"`
	Kubernetes *KubernetesAuth `mapstructure:"kubernetes" yaml:"kubernetes"`
}

// AppRoleAuth configures AppRole authentication
type AppRoleAuth struct {
	Mount    string `mapstructure:"mount" yaml:"mount"`
	RoleID   string `mapstructure:"role_id" yaml:"role_id"`
	SecretID string `mapstructure:"secret_id" yaml:"secret_id"`
}

// TokenAuth configures token authentication
type TokenAuth struct {
	Token string `mapstructure:"token" yaml:"token"`
}

// KubernetesAuth configures Kubernetes authentication
type KubernetesAuth struct {
	Role      string `mapstructure:"role" yaml:"role"`
	MountPath string `mapstructure:"mount_path" yaml:"mount_path"`
}

// AWSConfig configures AWS with Control Tower / Organizations awareness
type AWSConfig struct {
	Region           string                  `mapstructure:"region" yaml:"region"`
	ExecutionContext ExecutionContextConfig  `mapstructure:"execution_context" yaml:"execution_context"`
	ControlTower     ControlTowerConfig      `mapstructure:"control_tower" yaml:"control_tower"`
	Organizations    OrganizationsConfig     `mapstructure:"organizations" yaml:"organizations"`
	IdentityCenter   IdentityCenterConfig    `mapstructure:"identity_center" yaml:"identity_center"`
}

// ExecutionContextType defines where the pipeline runs from
type ExecutionContextType string

const (
	// ExecutionContextManagement runs from the AWS Organizations management account
	ExecutionContextManagement ExecutionContextType = "management_account"
	// ExecutionContextDelegated runs from a delegated administrator account
	ExecutionContextDelegated ExecutionContextType = "delegated_admin"
	// ExecutionContextHub runs from a custom secrets hub account
	ExecutionContextHub ExecutionContextType = "hub_account"
)

// ExecutionContextConfig defines where the pipeline is running from
type ExecutionContextConfig struct {
	Type              ExecutionContextType `mapstructure:"type" yaml:"type"`
	AccountID         string               `mapstructure:"account_id" yaml:"account_id"`
	Delegation        *DelegationConfig    `mapstructure:"delegation" yaml:"delegation"`
	CustomRolePattern string               `mapstructure:"custom_role_pattern" yaml:"custom_role_pattern"`
}

// DelegationConfig defines delegated administrator settings
type DelegationConfig struct {
	Services []string `mapstructure:"services" yaml:"services"`
}

// ControlTowerConfig configures AWS Control Tower integration
type ControlTowerConfig struct {
	Enabled        bool                   `mapstructure:"enabled" yaml:"enabled"`
	ExecutionRole  ExecutionRoleConfig    `mapstructure:"execution_role" yaml:"execution_role"`
	AccountFactory AccountFactoryConfig   `mapstructure:"account_factory" yaml:"account_factory"`
}

// ExecutionRoleConfig defines the cross-account execution role
type ExecutionRoleConfig struct {
	Name string `mapstructure:"name" yaml:"name"`
	Path string `mapstructure:"path" yaml:"path"`
}

// AccountFactoryConfig configures Account Factory integration
type AccountFactoryConfig struct {
	Enabled           bool `mapstructure:"enabled" yaml:"enabled"`
	OnAccountCreation bool `mapstructure:"on_account_creation" yaml:"on_account_creation"`
	AFTIntegration    bool `mapstructure:"aft_integration" yaml:"aft_integration"`
}

// OrganizationsConfig configures AWS Organizations integration
type OrganizationsConfig struct {
	AutoDiscover bool              `mapstructure:"auto_discover" yaml:"auto_discover"`
	RootID       string            `mapstructure:"root_id" yaml:"root_id"`
	OUs          map[string]OUConfig `mapstructure:"ous" yaml:"ous"`
}

// OUConfig represents an Organizational Unit
type OUConfig struct {
	ID       string            `mapstructure:"id" yaml:"id"`
	Accounts []string          `mapstructure:"accounts" yaml:"accounts"`
	Children map[string]OUConfig `mapstructure:"children" yaml:"children"`
}

// IdentityCenterConfig configures AWS Identity Center (SSO) integration
type IdentityCenterConfig struct {
	Enabled         bool   `mapstructure:"enabled" yaml:"enabled"`
	AutoDiscover    bool   `mapstructure:"auto_discover" yaml:"auto_discover"`
	InstanceARN     string `mapstructure:"instance_arn" yaml:"instance_arn"`
	IdentityStoreID string `mapstructure:"identity_store_id" yaml:"identity_store_id"`
}

// Source defines where secrets can be imported from
type Source struct {
	Vault *VaultSource `mapstructure:"vault" yaml:"vault"`
	AWS   *AWSSource   `mapstructure:"aws" yaml:"aws"`
}

// VaultSource imports secrets from a Vault KV2 mount
type VaultSource struct {
	Address   string   `mapstructure:"address" yaml:"address"`
	Namespace string   `mapstructure:"namespace" yaml:"namespace"`
	Mount     string   `mapstructure:"mount" yaml:"mount"`
	Paths     []string `mapstructure:"paths" yaml:"paths"`
}

// AWSSource imports secrets from AWS Secrets Manager
type AWSSource struct {
	AccountID string            `mapstructure:"account_id" yaml:"account_id"`
	Region    string            `mapstructure:"region" yaml:"region"`
	Prefix    string            `mapstructure:"prefix" yaml:"prefix"`
	Tags      map[string]string `mapstructure:"tags" yaml:"tags"`
}

// MergeStoreConfig defines intermediate storage for merged secrets
type MergeStoreConfig struct {
	Vault *MergeStoreVault `mapstructure:"vault" yaml:"vault"`
	S3    *MergeStoreS3    `mapstructure:"s3" yaml:"s3"`
}

// MergeStoreVault uses Vault as the merge store
type MergeStoreVault struct {
	Mount string `mapstructure:"mount" yaml:"mount"`
}

// MergeStoreS3 uses S3 as the merge store
type MergeStoreS3 struct {
	Bucket    string `mapstructure:"bucket" yaml:"bucket"`
	Prefix    string `mapstructure:"prefix" yaml:"prefix"`
	KMSKeyID  string `mapstructure:"kms_key_id" yaml:"kms_key_id"`
}

// Target defines a sync destination.
// Supports two YAML formats:
//  1. Explicit: target: {account_id: "...", imports: [...]}
//  2. Shorthand inheritance: target: [parent1, parent2]  (list IS the imports)
type Target struct {
	AccountID    string   `mapstructure:"account_id" yaml:"account_id"`
	Imports      []string `mapstructure:"imports" yaml:"imports"`
	Region       string   `mapstructure:"region" yaml:"region"`
	SecretPrefix string   `mapstructure:"secret_prefix" yaml:"secret_prefix"`
	RoleARN      string   `mapstructure:"role_arn" yaml:"role_arn"`
}

// UnmarshalYAML implements custom YAML unmarshaling to support shorthand format.
// This matches terraform-aws-secretsmanager targets.yaml format where:
//
//	Serverless_Stg:
//	  imports: [analytics]  # explicit format
//
//	Serverless_Prod:
//	  - Serverless_Stg      # shorthand: list IS the imports
func (t *Target) UnmarshalYAML(unmarshal func(interface{}) error) error {
	// First try to unmarshal as a list (shorthand format)
	var shorthand []string
	if err := unmarshal(&shorthand); err == nil {
		t.Imports = shorthand
		return nil
	}

	// Otherwise unmarshal as the full struct
	type targetAlias Target // avoid infinite recursion
	var full targetAlias
	if err := unmarshal(&full); err != nil {
		return err
	}
	*t = Target(full)
	return nil
}

// DynamicTarget defines targets discovered at runtime
// It supports all the same options as static targets, plus discovery configuration
type DynamicTarget struct {
	Discovery DiscoveryConfig `mapstructure:"discovery" yaml:"discovery"`
	Imports   []string        `mapstructure:"imports" yaml:"imports"`
	Exclude   []string        `mapstructure:"exclude" yaml:"exclude"`
	
	// All static target options are also available for dynamic targets
	Region       string `mapstructure:"region" yaml:"region"`
	SecretPrefix string `mapstructure:"secret_prefix" yaml:"secret_prefix"`
	RoleARN      string `mapstructure:"role_arn" yaml:"role_arn"` // Supports {{.AccountID}} template
}

// DiscoveryConfig defines how to discover dynamic targets
type DiscoveryConfig struct {
	IdentityCenter *IdentityCenterDiscovery `mapstructure:"identity_center" yaml:"identity_center"`
	Organizations  *OrganizationsDiscovery  `mapstructure:"organizations" yaml:"organizations"`
	AccountsList   *AccountsListDiscovery   `mapstructure:"accounts_list" yaml:"accounts_list"`
}

// IdentityCenterDiscovery discovers accounts from Identity Center
type IdentityCenterDiscovery struct {
	Group         string `mapstructure:"group" yaml:"group"`
	PermissionSet string `mapstructure:"permission_set" yaml:"permission_set"`
}

// OrganizationsDiscovery discovers accounts from AWS Organizations
type OrganizationsDiscovery struct {
	OU        string            `mapstructure:"ou" yaml:"ou"`
	Tags      map[string]string `mapstructure:"tags" yaml:"tags"`
	Recursive bool              `mapstructure:"recursive" yaml:"recursive"` // Whether to traverse child OUs
}

// AccountsListDiscovery discovers accounts from an external source (e.g., SSM Parameter Store)
type AccountsListDiscovery struct {
	Source string `mapstructure:"source" yaml:"source"` // e.g., "ssm:/platform/analytics-engineer-sandboxes"
}

// PipelineSettings configures pipeline execution
type PipelineSettings struct {
	Merge           MergeSettings `mapstructure:"merge" yaml:"merge"`
	Sync            SyncSettings  `mapstructure:"sync" yaml:"sync"`
	DryRun          bool          `mapstructure:"dry_run" yaml:"dry_run"`
	ContinueOnError bool          `mapstructure:"continue_on_error" yaml:"continue_on_error"`
}

// MergeSettings configures the merge phase
type MergeSettings struct {
	Parallel int `mapstructure:"parallel" yaml:"parallel"`
}

// SyncSettings configures the sync phase
type SyncSettings struct {
	Parallel      int  `mapstructure:"parallel" yaml:"parallel"`
	DeleteOrphans bool `mapstructure:"delete_orphans" yaml:"delete_orphans"`
}

// LoadConfig loads configuration from file
func LoadConfig(path string) (*Config, error) {
	// Read file directly for better YAML parsing
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	// Apply defaults
	cfg.applyDefaults()

	// Expand environment variables in sensitive fields
	cfg.expandEnvVars()

	// Also load via Viper for env var override support
	v := viper.New()
	v.SetConfigFile(path)
	v.SetEnvPrefix("VSS")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	
	// Override from environment if set
	if v.IsSet("log.level") {
		cfg.Log.Level = v.GetString("log.level")
	}
	if v.IsSet("aws.region") {
		cfg.AWS.Region = v.GetString("aws.region")
	}

	return &cfg, nil
}

// applyDefaults sets default values for unset fields
func (c *Config) applyDefaults() {
	if c.Log.Level == "" {
		c.Log.Level = "info"
	}
	if c.Log.Format == "" {
		c.Log.Format = "text"
	}
	if c.AWS.Region == "" {
		c.AWS.Region = "us-east-1"
	}
	if c.AWS.ControlTower.ExecutionRole.Name == "" {
		c.AWS.ControlTower.ExecutionRole.Name = "AWSControlTowerExecution"
	}
	if c.Pipeline.Merge.Parallel <= 0 {
		c.Pipeline.Merge.Parallel = 4
	}
	if c.Pipeline.Sync.Parallel <= 0 {
		c.Pipeline.Sync.Parallel = 4
	}
}

// expandEnvVars expands ${VAR} patterns in config values
func (c *Config) expandEnvVars() {
	// Use stricter regex to only allow valid environment variable names
	envPattern := regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

	// Maximum length for expanded values to prevent potential attacks
	const maxEnvValueLength = 10000

	expand := func(s string) string {
		return envPattern.ReplaceAllStringFunc(s, func(match string) string {
			varName := match[2 : len(match)-1] // Strip ${ and }
			if val := os.Getenv(varName); val != "" {
				// Security: reject suspiciously long values
				if len(val) > maxEnvValueLength {
					log.WithField("variable", varName).Warn("Environment variable value exceeds maximum length, keeping placeholder")
					return match
				}
				return val
			}
			return match // Keep original if not found
		})
	}

	// Expand Vault auth
	if c.Vault.Auth.AppRole != nil {
		c.Vault.Auth.AppRole.RoleID = expand(c.Vault.Auth.AppRole.RoleID)
		c.Vault.Auth.AppRole.SecretID = expand(c.Vault.Auth.AppRole.SecretID)
	}
	if c.Vault.Auth.Token != nil {
		c.Vault.Auth.Token.Token = expand(c.Vault.Auth.Token.Token)
	}
}

// Validate validates the configuration
func (c *Config) Validate() error {
	if c.Vault.Address == "" {
		return fmt.Errorf("vault.address is required")
	}

	if c.MergeStore.Vault == nil && c.MergeStore.S3 == nil {
		return fmt.Errorf("merge_store must specify either vault or s3")
	}

	// Validate S3 merge store config if specified
	if c.MergeStore.S3 != nil {
		if c.MergeStore.S3.Bucket == "" {
			return fmt.Errorf("merge_store.s3.bucket is required")
		}
	}

	// At least one target is required (static or dynamic)
	if len(c.Targets) == 0 && len(c.DynamicTargets) == 0 {
		return fmt.Errorf("at least one target or dynamic_target is required")
	}

	// Validate targets
	for name, target := range c.Targets {
		if target.AccountID == "" {
			return fmt.Errorf("target %q: account_id is required", name)
		}
		// Validate AWS account ID format (must be 12 digits)
		if !isValidAWSAccountID(target.AccountID) {
			return fmt.Errorf("target %q: invalid account_id format %q (must be 12 digits)", name, target.AccountID)
		}
		// Validate imports reference valid sources or other targets
		for _, imp := range target.Imports {
			if _, ok := c.Sources[imp]; !ok {
				if _, ok := c.Targets[imp]; !ok {
					return fmt.Errorf("target %q: import %q not found in sources or targets", name, imp)
				}
			}
		}
	}

	// Validate dynamic targets
	for name, dt := range c.DynamicTargets {
		if dt.Discovery.IdentityCenter == nil && dt.Discovery.Organizations == nil && dt.Discovery.AccountsList == nil {
			return fmt.Errorf("dynamic_target %q: must specify identity_center, organizations, or accounts_list discovery", name)
		}
	}

	return nil
}

// isValidAWSAccountID validates that an AWS account ID is exactly 12 digits
func isValidAWSAccountID(accountID string) bool {
	if len(accountID) != 12 {
		return false
	}
	for _, c := range accountID {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// GetRoleARN returns the role ARN for a target account
func (c *Config) GetRoleARN(accountID string) string {
	// Check if target has explicit role ARN
	for _, target := range c.Targets {
		if target.AccountID == accountID && target.RoleARN != "" {
			return target.RoleARN
		}
	}

	// Use Control Tower execution role pattern
	if c.AWS.ControlTower.Enabled {
		roleName := c.AWS.ControlTower.ExecutionRole.Name
		if roleName == "" {
			roleName = "AWSControlTowerExecution"
		}
		path := c.AWS.ControlTower.ExecutionRole.Path
		if path == "" {
			path = "/"
		} else {
			if !strings.HasPrefix(path, "/") {
				path = "/" + path
			}
			if !strings.HasSuffix(path, "/") {
				path += "/"
			}
		}
		return fmt.Sprintf("arn:aws:iam::%s:role%s%s", accountID, path, roleName)
	}

	// Use custom role pattern from execution context
	if c.AWS.ExecutionContext.CustomRolePattern != "" {
		return strings.ReplaceAll(c.AWS.ExecutionContext.CustomRolePattern, "{{.AccountID}}", accountID)
	}

	// Default Control Tower role
	return fmt.Sprintf("arn:aws:iam::%s:role/AWSControlTowerExecution", accountID)
}

// WriteConfig writes the configuration to a file
func (c *Config) WriteConfig(path string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	return os.WriteFile(path, data, 0600)
}

// IsInheritedTarget checks if a target inherits from another target
func (c *Config) IsInheritedTarget(targetName string) bool {
	target, ok := c.Targets[targetName]
	if !ok {
		return false
	}
	for _, imp := range target.Imports {
		if _, isTarget := c.Targets[imp]; isTarget {
			return true
		}
	}
	return false
}

// GetSourcePath returns the full path for a source or inherited target
func (c *Config) GetSourcePath(importName string) string {
	// Check if it's a direct source
	if src, ok := c.Sources[importName]; ok {
		if src.Vault != nil {
			return src.Vault.Mount
		}
	}

	// Check if it's another target (inheritance)
	if _, ok := c.Targets[importName]; ok {
		if c.MergeStore.Vault != nil {
			return fmt.Sprintf("%s/%s", c.MergeStore.Vault.Mount, importName)
		}
	}

	return importName
}
