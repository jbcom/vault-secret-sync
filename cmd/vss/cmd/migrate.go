package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jbcom/secretsync/pkg/pipeline"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	migrateFrom     string
	targetsFile     string
	secretsFile     string
	accountsFile    string
	outputFile      string
	vaultAddr       string
	vaultMergeMount string
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Migrate from other secret management tools",
	Long: `Migrate configuration from other secret management tools to vss pipeline format.

Supported sources:
  - terraform-secretsmanager: Terraform-based AWS Secrets Manager pipeline

Example:
  vss migrate --from terraform-secretsmanager \
              --targets config/targets.yaml \
              --secrets config/secrets.yaml \
              --accounts config/accounts.yaml \
              --output config.yaml`,
	RunE: runMigrate,
}

func init() {
	rootCmd.AddCommand(migrateCmd)

	migrateCmd.Flags().StringVar(&migrateFrom, "from", "", "Source format to migrate from (terraform-secretsmanager)")
	migrateCmd.Flags().StringVar(&targetsFile, "targets", "", "Path to targets configuration file")
	migrateCmd.Flags().StringVar(&secretsFile, "secrets", "", "Path to secrets configuration file")
	migrateCmd.Flags().StringVar(&accountsFile, "accounts", "", "Path to accounts configuration file")
	migrateCmd.Flags().StringVar(&outputFile, "output", "pipeline-config.yaml", "Output file path")
	migrateCmd.Flags().StringVar(&vaultAddr, "vault-addr", "", "Vault address (or set VAULT_ADDR)")
	migrateCmd.Flags().StringVar(&vaultMergeMount, "vault-merge-mount", "secret/merged", "Vault mount for merged secrets")

	migrateCmd.MarkFlagRequired("from")
}

func runMigrate(cmd *cobra.Command, args []string) error {
	switch migrateFrom {
	case "terraform-secretsmanager":
		return migrateTerraformSecretManager()
	default:
		return fmt.Errorf("unsupported migration source: %s", migrateFrom)
	}
}

// TerraformTarget represents a target in terraform-aws-secretsmanager format
type TerraformTarget struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description,omitempty"`
	Imports     []string `yaml:"imports,omitempty"`
	Secrets     []string `yaml:"secrets,omitempty"`
}

// TerraformSecret represents a secret definition
type TerraformSecret struct {
	Name       string            `yaml:"name"`
	VaultPath  string            `yaml:"vault_path"`
	VaultMount string            `yaml:"vault_mount,omitempty"`
	Keys       []string          `yaml:"keys,omitempty"`
	Transforms map[string]string `yaml:"transforms,omitempty"`
}

// TerraformAccount represents an account mapping
type TerraformAccount struct {
	Name      string `yaml:"name"`
	AccountID string `yaml:"account_id"`
	Region    string `yaml:"region,omitempty"`
	RoleARN   string `yaml:"role_arn,omitempty"`
}

// TerraformTargetsFile is the structure of targets.yaml
type TerraformTargetsFile struct {
	Targets []TerraformTarget `yaml:"targets"`
}

// TerraformSecretsFile is the structure of secrets.yaml
type TerraformSecretsFile struct {
	Secrets []TerraformSecret `yaml:"secrets"`
}

// TerraformAccountsFile is the structure of accounts.yaml
type TerraformAccountsFile struct {
	Accounts []TerraformAccount `yaml:"accounts"`
}

func migrateTerraformSecretManager() error {
	// Validate required files
	if targetsFile == "" {
		return fmt.Errorf("--targets is required for terraform-secretsmanager migration")
	}
	if secretsFile == "" {
		return fmt.Errorf("--secrets is required for terraform-secretsmanager migration")
	}
	if accountsFile == "" {
		return fmt.Errorf("--accounts is required for terraform-secretsmanager migration")
	}

	// Load source files
	targets, err := loadTerraformTargets(targetsFile)
	if err != nil {
		return fmt.Errorf("failed to load targets: %w", err)
	}

	secrets, err := loadTerraformSecrets(secretsFile)
	if err != nil {
		return fmt.Errorf("failed to load secrets: %w", err)
	}

	accounts, err := loadTerraformAccounts(accountsFile)
	if err != nil {
		return fmt.Errorf("failed to load accounts: %w", err)
	}

	// Build account lookup map
	accountMap := make(map[string]TerraformAccount)
	for _, acc := range accounts.Accounts {
		accountMap[acc.Name] = acc
	}

	// Build secret lookup map
	secretMap := make(map[string]TerraformSecret)
	for _, sec := range secrets.Secrets {
		secretMap[sec.Name] = sec
	}

	// Convert to pipeline config
	cfg := &pipeline.Config{
		Vault: pipeline.VaultConfig{
			Address: getVaultAddr(),
		},
		MergeStore: pipeline.MergeStoreConfig{
			Vault: &pipeline.MergeStoreVault{
				Mount: vaultMergeMount,
			},
		},
		Sources: make(map[string]pipeline.Source),
		Targets: make(map[string]pipeline.Target),
		AWS: pipeline.AWSConfig{
			Region: "us-east-1",
			ControlTower: pipeline.ControlTowerConfig{
				Enabled: true,
				ExecutionRole: pipeline.ExecutionRoleConfig{
					Name: "AWSControlTowerExecution",
				},
			},
		},
	}

	// Convert secrets to sources
	for _, sec := range secrets.Secrets {
		mount := sec.VaultMount
		if mount == "" {
			mount = "secret"
		}
		sourceName := sanitizeSourceName(sec.Name)
		cfg.Sources[sourceName] = pipeline.Source{
			Vault: &pipeline.VaultSource{
				Mount: mount,
				Paths: []string{sec.VaultPath},
			},
		}
	}

	// Convert targets
	for _, target := range targets.Targets {
		// Find account info
		account, ok := accountMap[target.Name]
		if !ok {
			fmt.Fprintf(os.Stderr, "Warning: no account found for target %q, skipping\n", target.Name)
			continue
		}

		// Build imports list from secrets referenced
		var imports []string
		for _, secName := range target.Secrets {
			imports = append(imports, sanitizeSourceName(secName))
		}
		// Add imports from target inheritance
		imports = append(imports, target.Imports...)

		pipelineTarget := pipeline.Target{
			AccountID: account.AccountID,
			Region:    account.Region,
			RoleARN:   account.RoleARN,
			Imports:   imports,
		}

		cfg.Targets[target.Name] = pipelineTarget
	}

	// Write output
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Add header comment
	header := `# Pipeline configuration migrated from terraform-aws-secretsmanager
# Generated by: vss migrate --from terraform-secretsmanager
#
# Review and adjust as needed:
# - Verify Vault address and authentication
# - Check source paths and mounts
# - Validate target account IDs and regions
# - Add any missing transforms or filters

`

	if err := os.WriteFile(outputFile, []byte(header+string(data)), 0600); err != nil {
		return fmt.Errorf("failed to write output: %w", err)
	}

	fmt.Printf("✅ Migration complete!\n")
	fmt.Printf("   Output: %s\n", outputFile)
	fmt.Printf("   Sources: %d\n", len(cfg.Sources))
	fmt.Printf("   Targets: %d\n", len(cfg.Targets))
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Printf("   1. Review the generated config: %s\n", outputFile)
	fmt.Println("   2. Add Vault authentication (token, approle, etc.)")
	fmt.Println("   3. Validate: vss validate --config " + outputFile)
	fmt.Println("   4. Dry run: vss pipeline --config " + outputFile + " --dry-run")

	return nil
}

func loadTerraformTargets(path string) (*TerraformTargetsFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var targets TerraformTargetsFile
	if err := yaml.Unmarshal(data, &targets); err != nil {
		// Try alternative format (list at root)
		var targetList []TerraformTarget
		if err2 := yaml.Unmarshal(data, &targetList); err2 == nil {
			targets.Targets = targetList
		} else {
			return nil, fmt.Errorf("failed to parse targets: %w", err)
		}
	}

	return &targets, nil
}

func loadTerraformSecrets(path string) (*TerraformSecretsFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var secrets TerraformSecretsFile
	if err := yaml.Unmarshal(data, &secrets); err != nil {
		// Try alternative format (list at root)
		var secretList []TerraformSecret
		if err2 := yaml.Unmarshal(data, &secretList); err2 == nil {
			secrets.Secrets = secretList
		} else {
			return nil, fmt.Errorf("failed to parse secrets: %w", err)
		}
	}

	return &secrets, nil
}

func loadTerraformAccounts(path string) (*TerraformAccountsFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var accounts TerraformAccountsFile
	if err := yaml.Unmarshal(data, &accounts); err != nil {
		// Try alternative format (map of name -> account)
		var accountMap map[string]TerraformAccount
		if err2 := yaml.Unmarshal(data, &accountMap); err2 == nil {
			for name, acc := range accountMap {
				acc.Name = name
				accounts.Accounts = append(accounts.Accounts, acc)
			}
		} else {
			// Try list at root
			var accountList []TerraformAccount
			if err3 := yaml.Unmarshal(data, &accountList); err3 == nil {
				accounts.Accounts = accountList
			} else {
				return nil, fmt.Errorf("failed to parse accounts: %w", err)
			}
		}
	}

	return &accounts, nil
}

func getVaultAddr() string {
	if vaultAddr != "" {
		return vaultAddr
	}
	if addr := os.Getenv("VAULT_ADDR"); addr != "" {
		return addr
	}
	return "https://vault.example.com"
}

func sanitizeSourceName(name string) string {
	// Convert to lowercase and replace special chars
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "-", "_")
	// Remove file extension if present
	name = strings.TrimSuffix(name, filepath.Ext(name))
	return name
}
