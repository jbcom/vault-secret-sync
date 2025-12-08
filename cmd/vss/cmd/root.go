package cmd

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	log "github.com/sirupsen/logrus"
)

var (
	cfgFile  string
	logLevel string
	logFormat string
)

// rootCmd represents the base command
var rootCmd = &cobra.Command{
	Use:   "vss",
	Short: "Vault Secret Sync - Multi-account secrets management",
	Long: `Vault Secret Sync (vss) synchronizes secrets between Vault and cloud providers.

It supports:
- AWS Control Tower / Organizations for multi-account management
- Inheritance hierarchies (dev → staging → prod)
- Dynamic target discovery via Identity Center / Organizations
- Merge stores for centralized secret aggregation

Examples:
  # Run full pipeline
  vss pipeline --config config.yaml

  # Dry run for specific targets
  vss pipeline --config config.yaml --targets Serverless_Stg --dry-run

  # Merge only (no AWS sync)
  vss pipeline --config config.yaml --merge-only

  # Validate configuration
  vss validate --config config.yaml

  # Show dependency graph
  vss graph --config config.yaml`,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		// Set log level
		level, err := log.ParseLevel(logLevel)
		if err != nil {
			level = log.InfoLevel
		}
		log.SetLevel(level)

		// Set log format
		if logFormat == "json" {
			log.SetFormatter(&log.JSONFormatter{})
		}
	},
}

// Execute runs the root command
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	// Global flags
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "config.yaml", "config file path")
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "log level (debug, info, warn, error)")
	rootCmd.PersistentFlags().StringVar(&logFormat, "log-format", "text", "log format (text, json)")

	// Bind to viper
	viper.BindPFlag("log.level", rootCmd.PersistentFlags().Lookup("log-level"))
	viper.BindPFlag("log.format", rootCmd.PersistentFlags().Lookup("log-format"))
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		// Default config locations
		viper.AddConfigPath(".")
		viper.AddConfigPath("/config")
		viper.SetConfigName("config")
		viper.SetConfigType("yaml")
	}

	// Environment variables
	viper.SetEnvPrefix("VSS")
	viper.AutomaticEnv()
}
