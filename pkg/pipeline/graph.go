package pipeline

import (
	"fmt"
	"sort"
	"strings"
)

// NodeType represents the type of node in the dependency graph
type NodeType int

const (
	NodeTypeSource NodeType = iota
	NodeTypeTarget
)

// Node represents a node in the dependency graph
type Node struct {
	Name     string
	Type     NodeType
	Level    int      // Dependency depth (0 = no dependencies)
	Deps     []string // Nodes this depends on
	DependedBy []string // Nodes that depend on this
}

// Graph represents the dependency graph for targets
type Graph struct {
	Nodes map[string]*Node
}

// NewGraph creates a new dependency graph
func NewGraph() *Graph {
	return &Graph{
		Nodes: make(map[string]*Node),
	}
}

// BuildGraph builds a dependency graph from the configuration
func BuildGraph(cfg *Config) (*Graph, error) {
	g := NewGraph()

	// Add all sources as leaf nodes (level 0)
	for name := range cfg.Sources {
		g.Nodes[name] = &Node{
			Name:  name,
			Type:  NodeTypeSource,
			Level: 0,
		}
	}

	// Add all targets
	for name := range cfg.Targets {
		g.Nodes[name] = &Node{
			Name: name,
			Type: NodeTypeTarget,
		}
	}

	// Build edges
	for name, target := range cfg.Targets {
		node := g.Nodes[name]
		for _, imp := range target.Imports {
			depNode, ok := g.Nodes[imp]
			if !ok {
				return nil, fmt.Errorf("target %q imports unknown source/target %q", name, imp)
			}
			node.Deps = append(node.Deps, imp)
			depNode.DependedBy = append(depNode.DependedBy, name)
		}
	}

	// Calculate levels using BFS
	if err := g.calculateLevels(); err != nil {
		return nil, err
	}

	return g, nil
}

// calculateLevels calculates the dependency level for each node
func (g *Graph) calculateLevels() error {
	// Track visited nodes for cycle detection
	visited := make(map[string]bool)
	inStack := make(map[string]bool)

	var calcLevel func(name string) (int, error)
	calcLevel = func(name string) (int, error) {
		node := g.Nodes[name]
		if node == nil {
			return 0, fmt.Errorf("node %q not found", name)
		}

		// Cycle detection
		if inStack[name] {
			return 0, fmt.Errorf("circular dependency detected involving %q", name)
		}

		// Already calculated
		if visited[name] {
			return node.Level, nil
		}

		inStack[name] = true

		// Sources have level 0
		if node.Type == NodeTypeSource {
			node.Level = 0
			visited[name] = true
			inStack[name] = false
			return 0, nil
		}

		// Calculate max level of dependencies
		maxDepLevel := -1
		for _, dep := range node.Deps {
			depLevel, err := calcLevel(dep)
			if err != nil {
				return 0, err
			}
			if depLevel > maxDepLevel {
				maxDepLevel = depLevel
			}
		}

		node.Level = maxDepLevel + 1
		visited[name] = true
		inStack[name] = false
		return node.Level, nil
	}

	// Calculate levels for all nodes
	for name := range g.Nodes {
		if _, err := calcLevel(name); err != nil {
			return err
		}
	}

	return nil
}

// TopologicalOrder returns targets in dependency order (base first, then derived)
func (g *Graph) TopologicalOrder() []string {
	var targets []string
	for name, node := range g.Nodes {
		if node.Type == NodeTypeTarget {
			targets = append(targets, name)
		}
	}

	// Sort by level, then by name for determinism
	sort.Slice(targets, func(i, j int) bool {
		li := g.Nodes[targets[i]].Level
		lj := g.Nodes[targets[j]].Level
		if li != lj {
			return li < lj
		}
		return targets[i] < targets[j]
	})

	return targets
}

// GroupByLevel groups targets by their dependency level
func (g *Graph) GroupByLevel() [][]string {
	maxLevel := 0
	for _, node := range g.Nodes {
		if node.Type == NodeTypeTarget && node.Level > maxLevel {
			maxLevel = node.Level
		}
	}

	levels := make([][]string, maxLevel+1)
	for i := range levels {
		levels[i] = []string{}
	}

	for name, node := range g.Nodes {
		if node.Type == NodeTypeTarget {
			levels[node.Level] = append(levels[node.Level], name)
		}
	}

	// Sort each level for determinism
	for i := range levels {
		sort.Strings(levels[i])
	}

	return levels
}

// IncludeDependencies expands a list of targets to include their dependencies
func (g *Graph) IncludeDependencies(targets []string) []string {
	included := make(map[string]bool)
	
	var addDeps func(name string)
	addDeps = func(name string) {
		if included[name] {
			return
		}
		included[name] = true
		
		node := g.Nodes[name]
		if node == nil {
			return
		}
		
		for _, dep := range node.Deps {
			// Only include target dependencies, not sources
			if depNode := g.Nodes[dep]; depNode != nil && depNode.Type == NodeTypeTarget {
				addDeps(dep)
			}
		}
	}

	for _, target := range targets {
		addDeps(target)
	}

	// Convert to slice and sort by level
	var result []string
	for name := range included {
		result = append(result, name)
	}

	sort.Slice(result, func(i, j int) bool {
		li := g.Nodes[result[i]].Level
		lj := g.Nodes[result[j]].Level
		if li != lj {
			return li < lj
		}
		return result[i] < result[j]
	})

	return result
}

// PrintGraph returns a visual representation of the graph
func (g *Graph) PrintGraph() string {
	levels := g.GroupByLevel()
	
	var sb strings.Builder
	sb.WriteString("Dependency Graph:\n")
	
	for i, level := range levels {
		sb.WriteString(fmt.Sprintf("  Level %d: %v\n", i, level))
	}
	
	sb.WriteString("\nInheritance:\n")
	order := g.TopologicalOrder()
	for _, name := range order {
		node := g.Nodes[name]
		if len(node.Deps) > 0 {
			var targetDeps []string
			for _, dep := range node.Deps {
				if depNode := g.Nodes[dep]; depNode != nil && depNode.Type == NodeTypeTarget {
					targetDeps = append(targetDeps, dep)
				}
			}
			if len(targetDeps) > 0 {
				sb.WriteString(fmt.Sprintf("  %s <- %v\n", name, targetDeps))
			}
		}
	}
	
	return sb.String()
}
