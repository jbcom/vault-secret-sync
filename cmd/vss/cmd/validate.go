package cmd

import (
	"context"
	"fmt"

	"github.com/jbcom/secretsync/pkg/pipeline"
	"github.com/spf13/cobra"
	log "github.com/sirupsen/logrus"
)

var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate configuration",
	Long: `Validates the pipeline configuration file.

Checks:
- YAML syntax
- Required fields
- Target references (sources exist)
- Dependency graph (no cycles)
- AWS execution context (optional)

Examples:
  vss validate --config config.yaml
  vss validate --config config.yaml --check-aws`,
	RunE: runValidate,
}

var checkAWS bool

func init() {
	rootCmd.AddCommand(validateCmd)
	validateCmd.Flags().BoolVar(&checkAWS, "check-aws", false, "also validate AWS credentials and access")
}

func runValidate(cmd *cobra.Command, args []string) error {
	l := log.WithFields(log.Fields{
		"action": "runValidate",
	})

	fmt.Printf("Validating configuration: %s\n\n", cfgFile)

	// Load config
	cfg, err := pipeline.LoadConfig(cfgFile)
	if err != nil {
		fmt.Printf("❌ Config load failed: %v\n", err)
		return err
	}
	fmt.Println("✅ Config file parsed successfully")

	// Validate config structure
	if err := cfg.Validate(); err != nil {
		fmt.Printf("❌ Config validation failed: %v\n", err)
		return err
	}
	fmt.Println("✅ Config structure validated")

	// Build dependency graph
	graph, err := pipeline.BuildGraph(cfg)
	if err != nil {
		fmt.Printf("❌ Dependency graph failed: %v\n", err)
		return err
	}
	fmt.Println("✅ Dependency graph validated (no cycles)")

	// Print summary
	fmt.Printf("\nConfiguration Summary:\n")
	fmt.Printf("  Sources: %d\n", len(cfg.Sources))
	fmt.Printf("  Targets: %d\n", len(cfg.Targets))
	fmt.Printf("  Dynamic Targets: %d\n", len(cfg.DynamicTargets))
	fmt.Printf("  Vault Address: %s\n", cfg.Vault.Address)
	fmt.Printf("  AWS Region: %s\n", cfg.AWS.Region)
	fmt.Printf("  Control Tower: %v\n", cfg.AWS.ControlTower.Enabled)

	// Print dependency levels
	levels := graph.GroupByLevel()
	fmt.Printf("\nDependency Levels:\n")
	for i, level := range levels {
		fmt.Printf("  Level %d: %v\n", i, level)
	}

	// Check AWS if requested
	if checkAWS {
		fmt.Println("\nValidating AWS access...")
		ctx := context.Background()

		awsCtx, err := pipeline.NewAWSExecutionContext(ctx, &cfg.AWS)
		if err != nil {
			fmt.Printf("❌ AWS validation failed: %v\n", err)
			return err
		}

		fmt.Println("✅ AWS credentials valid")
		fmt.Printf("\n%s", awsCtx.Summary())
	}

	l.Info("Validation completed successfully")
	fmt.Println("\n✅ All validations passed")
	return nil
}
