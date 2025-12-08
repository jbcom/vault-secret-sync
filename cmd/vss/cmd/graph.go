package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jbcom/secretsync/pkg/pipeline"
	"github.com/spf13/cobra"
)

var graphCmd = &cobra.Command{
	Use:   "graph",
	Short: "Display dependency graph",
	Long: `Displays the target dependency graph showing inheritance relationships.

The graph shows:
- Sources (Vault mounts, AWS accounts)
- Targets with their imports
- Inheritance relationships (target → target)
- Execution order (by dependency level)

Examples:
  vss graph --config config.yaml
  vss graph --config config.yaml --format dot`,
	RunE: runGraph,
}

var graphFormat string

func init() {
	rootCmd.AddCommand(graphCmd)
	graphCmd.Flags().StringVar(&graphFormat, "format", "text", "output format (text, dot)")
}

func runGraph(cmd *cobra.Command, args []string) error {
	// Load config
	cfg, err := pipeline.LoadConfig(cfgFile)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Build graph
	graph, err := pipeline.BuildGraph(cfg)
	if err != nil {
		return fmt.Errorf("failed to build graph: %w", err)
	}

	switch graphFormat {
	case "dot":
		printDotGraph(cfg, graph)
	default:
		printTextGraph(cfg, graph)
	}

	return nil
}

func printTextGraph(cfg *pipeline.Config, graph *pipeline.Graph) {
	fmt.Println("Secrets Pipeline Dependency Graph")
	fmt.Println(strings.Repeat("=", 50))

	// Print sources
	fmt.Println("\n📦 Sources:")
	for name, src := range cfg.Sources {
		if src.Vault != nil {
			fmt.Printf("   %s (vault: %s)\n", name, src.Vault.Mount)
		} else if src.AWS != nil {
			fmt.Printf("   %s (aws: %s)\n", name, src.AWS.AccountID)
		}
	}

	// Print targets by level
	fmt.Println("\n🎯 Targets (by dependency level):")
	levels := graph.GroupByLevel()
	for i, level := range levels {
		if len(level) == 0 {
			continue
		}
		fmt.Printf("\n   Level %d:\n", i)
		for _, name := range level {
			target := cfg.Targets[name]
			
			// Categorize imports
			var sources, inherited []string
			for _, imp := range target.Imports {
				if _, isTarget := cfg.Targets[imp]; isTarget {
					inherited = append(inherited, imp)
				} else {
					sources = append(sources, imp)
				}
			}

			fmt.Printf("   ├── %s (account: %s)\n", name, target.AccountID)
			if len(sources) > 0 {
				fmt.Printf("   │   └── sources: %v\n", sources)
			}
			if len(inherited) > 0 {
				fmt.Printf("   │   └── inherits: %v\n", inherited)
			}
		}
	}

	// Print execution order
	fmt.Println("\n📋 Execution Order:")
	order := graph.TopologicalOrder()
	for i, name := range order {
		fmt.Printf("   %d. %s\n", i+1, name)
	}

	// Print inheritance diagram
	fmt.Println("\n🔗 Inheritance Flow:")
	printInheritanceFlow(cfg, graph)
}

func printInheritanceFlow(cfg *pipeline.Config, graph *pipeline.Graph) {
	// Find root targets (no inheritance)
	var roots []string
	for name := range cfg.Targets {
		if !cfg.IsInheritedTarget(name) {
			roots = append(roots, name)
		}
	}
	// Sort roots for deterministic output
	sort.Strings(roots)

	// Build inheritance tree
	for _, root := range roots {
		printInheritanceTree(cfg, graph, root, "   ", true)
	}
}

func printInheritanceTree(cfg *pipeline.Config, graph *pipeline.Graph, name string, prefix string, isLast bool) {
	// Print current node
	connector := "├──"
	if isLast {
		connector = "└──"
	}
	
	target := cfg.Targets[name]
	fmt.Printf("%s%s %s (→ %s)\n", prefix, connector, name, target.AccountID)

	// Find children (targets that inherit from this one) using pre-computed graph
	var children []string
	if node, ok := graph.Nodes[name]; ok {
		children = append(children, node.DependedBy...)
	}
	// Sort children for deterministic output
	sort.Strings(children)

	// Print children
	newPrefix := prefix
	if isLast {
		newPrefix += "    "
	} else {
		newPrefix += "│   "
	}

	for i, child := range children {
		printInheritanceTree(cfg, graph, child, newPrefix, i == len(children)-1)
	}
}

func printDotGraph(cfg *pipeline.Config, graph *pipeline.Graph) {
	fmt.Println("digraph secrets_pipeline {")
	fmt.Println("  rankdir=LR;")
	fmt.Println("  node [shape=box];")
	fmt.Println()

	// Sources cluster
	fmt.Println("  subgraph cluster_sources {")
	fmt.Println("    label=\"Sources\";")
	fmt.Println("    style=dashed;")
	fmt.Println("    color=blue;")
	for name := range cfg.Sources {
		fmt.Printf("    \"%s\" [shape=cylinder, color=blue];\n", name)
	}
	fmt.Println("  }")
	fmt.Println()

	// Targets cluster
	fmt.Println("  subgraph cluster_targets {")
	fmt.Println("    label=\"Targets\";")
	fmt.Println("    style=dashed;")
	fmt.Println("    color=green;")
	for name, target := range cfg.Targets {
		fmt.Printf("    \"%s\" [label=\"%s\\n%s\", color=green];\n", name, name, target.AccountID)
	}
	fmt.Println("  }")
	fmt.Println()

	// Edges
	fmt.Println("  // Dependencies")
	for name, target := range cfg.Targets {
		for _, imp := range target.Imports {
			style := "solid"
			if _, isTarget := cfg.Targets[imp]; isTarget {
				style = "bold" // Inheritance edge
			}
			fmt.Printf("  \"%s\" -> \"%s\" [style=%s];\n", imp, name, style)
		}
	}

	fmt.Println("}")
}
