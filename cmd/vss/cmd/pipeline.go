package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"

	"github.com/jbcom/secretsync/pkg/diff"
	"github.com/jbcom/secretsync/pkg/pipeline"
	"github.com/spf13/cobra"
	log "github.com/sirupsen/logrus"
)

var (
	targets         string
	mergeOnly       bool
	syncOnly        bool
	dryRun          bool
	discoverTargets bool
	outputFormat    string
	computeDiff     bool
	exitCodeMode    bool
)

// pipelineCmd runs the full merge-then-sync pipeline
var pipelineCmd = &cobra.Command{
	Use:   "pipeline",
	Short: "Run the full secrets pipeline (merge → sync)",
	Long: `Runs the complete secrets synchronization pipeline:

1. MERGE PHASE: Aggregate secrets from sources into the merge store
   - Processes targets in dependency order (base before derived)
   - Supports inheritance (Prod inherits from Stg)
   - Uses Vault merge mode for aggregation

2. SYNC PHASE: Sync merged secrets to target AWS accounts
   - Assumes Control Tower execution role in each account
   - Runs in parallel (respects --parallel setting)

3. DIFF REPORTING: Track and report all changes
   - Zero-sum validation for migration verification
   - Multiple output formats (human, JSON, GitHub Actions)
   - CI/CD-friendly exit codes (0=no changes, 1=changes, 2=errors)

Examples:
  # Full pipeline
  vss pipeline --config config.yaml

  # Dry run with diff output (validates zero-sum)
  vss pipeline --config config.yaml --dry-run --output json

  # CI/CD mode with exit codes
  vss pipeline --config config.yaml --dry-run --exit-code
  # Returns: 0 if no changes, 1 if changes detected, 2 on errors

  # GitHub Actions compatible output
  vss pipeline --config config.yaml --dry-run --output github

  # Specific targets only
  vss pipeline --config config.yaml --targets "Serverless_Stg,Serverless_Prod"

  # Merge only (no AWS sync)
  vss pipeline --config config.yaml --merge-only

  # Compute diff even when applying changes (for audit trail)
  vss pipeline --config config.yaml --diff`,
	RunE: runPipeline,
}

func init() {
	rootCmd.AddCommand(pipelineCmd)

	pipelineCmd.Flags().StringVar(&targets, "targets", "", "comma-separated list of targets (default: all)")
	pipelineCmd.Flags().BoolVar(&mergeOnly, "merge-only", false, "only run merge phase")
	pipelineCmd.Flags().BoolVar(&syncOnly, "sync-only", false, "only run sync phase")
	pipelineCmd.Flags().BoolVar(&dryRun, "dry-run", false, "dry run mode (no changes)")
	pipelineCmd.Flags().BoolVar(&discoverTargets, "discover", false, "enable dynamic target discovery from AWS Organizations/Identity Center")
	
	// Diff and output options
	pipelineCmd.Flags().StringVarP(&outputFormat, "output", "o", "human", "output format: human, json, github, compact")
	pipelineCmd.Flags().BoolVar(&computeDiff, "diff", false, "compute and show diff even when not in dry-run mode")
	pipelineCmd.Flags().BoolVar(&exitCodeMode, "exit-code", false, "use exit codes: 0=no changes, 1=changes, 2=errors (useful for CI/CD)")
}

func runPipeline(cmd *cobra.Command, args []string) error {
	l := log.WithFields(log.Fields{
		"action": "runPipeline",
	})

	// Setup context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create pipeline from config file
	var p *pipeline.Pipeline
	var err error
	
	if discoverTargets {
		// Use context-aware constructor for dynamic target discovery
		l.Info("Dynamic target discovery enabled")
		p, err = pipeline.NewFromFileWithContext(ctx, cfgFile)
	} else {
		p, err = pipeline.NewFromFile(cfgFile)
	}
	if err != nil {
		return fmt.Errorf("failed to create pipeline: %w", err)
	}

	// Handle signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		l.Warn("Received shutdown signal")
		cancel()
	}()

	// Parse targets
	var targetList []string
	if targets != "" {
		targetList = strings.Split(targets, ",")
		for i := range targetList {
			targetList[i] = strings.TrimSpace(targetList[i])
		}
	}

	// Determine operation
	op := pipeline.OperationPipeline
	if mergeOnly {
		op = pipeline.OperationMerge
	} else if syncOnly {
		op = pipeline.OperationSync
	}

	// Parse output format
	format := parseOutputFormat(outputFormat)

	// Run options
	opts := pipeline.Options{
		Operation:       op,
		Targets:         targetList,
		DryRun:          dryRun,
		ContinueOnError: true,
		OutputFormat:    format,
		ComputeDiff:     computeDiff || dryRun,
	}

	l.WithFields(log.Fields{
		"config":       cfgFile,
		"targets":      targetList,
		"operation":    op,
		"dryRun":       dryRun,
		"outputFormat": format,
	}).Info("Starting pipeline")

	// Run pipeline
	results, err := p.Run(ctx, opts)

	// Print diff output if computed
	if d := p.Diff(); d != nil {
		diffOutput := p.FormatDiff(format)
		if diffOutput != "" {
			fmt.Println(diffOutput)
		}
	} else {
		// Fall back to traditional results format
		printResults(results)
	}

	// Determine exit behavior
	if exitCodeMode {
		exitCode := p.ExitCode()
		if exitCode != 0 {
			os.Exit(exitCode)
		}
		return nil
	}

	if err != nil {
		return err
	}

	// Check for any failures
	for _, r := range results {
		if !r.Success {
			return fmt.Errorf("pipeline completed with errors")
		}
	}

	l.Info("Pipeline completed successfully")
	return nil
}

// parseOutputFormat converts string to OutputFormat
func parseOutputFormat(s string) diff.OutputFormat {
	switch strings.ToLower(s) {
	case "json":
		return diff.OutputFormatJSON
	case "github":
		return diff.OutputFormatGitHub
	case "compact":
		return diff.OutputFormatCompact
	default:
		return diff.OutputFormatHuman
	}
}

func printResults(results []pipeline.Result) {
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("Pipeline Results")
	fmt.Println(strings.Repeat("=", 60))

	var mergeResults, syncResults []pipeline.Result
	for _, r := range results {
		if r.Phase == "merge" {
			mergeResults = append(mergeResults, r)
		} else {
			syncResults = append(syncResults, r)
		}
	}

	// Sort results by target name for deterministic output
	sort.Slice(mergeResults, func(i, j int) bool { return mergeResults[i].Target < mergeResults[j].Target })
	sort.Slice(syncResults, func(i, j int) bool { return syncResults[i].Target < syncResults[j].Target })

	if len(mergeResults) > 0 {
		fmt.Println("\nMerge Phase:")
		for _, r := range mergeResults {
			status := "✅"
			if !r.Success {
				status = "❌"
			}
			fmt.Printf("  %s %s (%.2fs)\n", status, r.Target, r.Duration.Seconds())
			if r.Error != nil {
				fmt.Printf("      Error: %v\n", r.Error)
			}
		}
	}

	if len(syncResults) > 0 {
		fmt.Println("\nSync Phase:")
		for _, r := range syncResults {
			status := "✅"
			if !r.Success {
				status = "❌"
			}
			fmt.Printf("  %s %s (%.2fs)\n", status, r.Target, r.Duration.Seconds())
			if r.Error != nil {
				fmt.Printf("      Error: %v\n", r.Error)
			}
		}
	}

	// Count successes/failures
	successCount := 0
	for _, r := range results {
		if r.Success {
			successCount++
		}
	}

	fmt.Printf("\nTotal: %d/%d succeeded\n", successCount, len(results))
	fmt.Println(strings.Repeat("=", 60))
}
