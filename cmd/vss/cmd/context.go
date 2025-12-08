package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/jbcom/secretsync/pkg/pipeline"
	"github.com/spf13/cobra"
)

var contextCmd = &cobra.Command{
	Use:   "context",
	Short: "Show AWS execution context",
	Long: `Displays information about the AWS execution context.

This shows:
- Current AWS identity (account, ARN)
- Organization membership
- Whether running from management account or delegated admin
- Delegated services (if applicable)
- Control Tower configuration
- Cross-account role pattern

Understanding your execution context is critical for multi-account operations.

Examples:
  vss context
  vss context --config config.yaml`,
	RunE: runContext,
}

func init() {
	rootCmd.AddCommand(contextCmd)
}

func runContext(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Try to load config for AWS settings
	var awsConfig *pipeline.AWSConfig
	if cfgFile != "" {
		cfg, err := pipeline.LoadConfig(cfgFile)
		if err != nil {
			return fmt.Errorf("failed to load config file '%s': %w", cfgFile, err)
		}
		awsConfig = &cfg.AWS
	}

	// Use defaults if no config
	if awsConfig == nil {
		awsConfig = &pipeline.AWSConfig{
			Region: "us-east-1",
			ControlTower: pipeline.ControlTowerConfig{
				Enabled: true,
				ExecutionRole: pipeline.ExecutionRoleConfig{
					Name: "AWSControlTowerExecution",
				},
			},
		}
	}

	// Create execution context
	awsCtx, err := pipeline.NewAWSExecutionContext(ctx, awsConfig)
	if err != nil {
		return fmt.Errorf("failed to create AWS execution context: %w", err)
	}

	// Print summary
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println("AWS Execution Context")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println()
	fmt.Print(awsCtx.Summary())

	// Print recommendations
	fmt.Println()
	fmt.Println(strings.Repeat("-", 60))
	fmt.Println("Recommendations:")
	fmt.Println()

	if awsCtx.OrganizationInfo != nil && awsCtx.OrganizationInfo.IsManagementAccount {
		fmt.Println("⚠️  Running from MANAGEMENT ACCOUNT")
		fmt.Println("   This is not recommended for production workloads.")
		fmt.Println("   Consider setting up a delegated administrator account.")
		fmt.Println()
		fmt.Println("   To delegate SSO administration:")
		fmt.Println("   aws organizations register-delegated-administrator \\")
		fmt.Println("     --account-id <ADMIN_ACCOUNT_ID> \\")
		fmt.Println("     --service-principal sso.amazonaws.com")
	} else if awsCtx.OrganizationInfo != nil && awsCtx.OrganizationInfo.IsDelegatedAdmin {
		fmt.Println("✅ Running from DELEGATED ADMINISTRATOR account")
		fmt.Println("   This is the recommended configuration.")
		
		if !awsCtx.CanAccessIdentityCenter() {
			fmt.Println()
			fmt.Println("⚠️  No Identity Center delegation detected.")
			fmt.Println("   Dynamic target discovery may not work.")
		}
	} else {
		fmt.Println("ℹ️  Running from MEMBER ACCOUNT")
		fmt.Println("   Ensure cross-account roles are deployed to target accounts.")
		fmt.Println("   Control Tower execution role or custom role required.")
	}

	// Print example role assumption
	fmt.Println()
	fmt.Println(strings.Repeat("-", 60))
	fmt.Println("Cross-Account Role Pattern:")
	fmt.Println()
	exampleAccountID := "123456789012"
	roleARN := awsCtx.GetRoleARN(exampleAccountID)
	fmt.Printf("   Example: %s\n", roleARN)
	fmt.Println()
	fmt.Println("   This role will be assumed in each target account.")
	fmt.Println("   Ensure the role exists and trusts this account.")

	return nil
}
