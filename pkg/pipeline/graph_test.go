package pipeline

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildGraph(t *testing.T) {
	cfg := &Config{
		Sources: map[string]Source{
			"analytics":           {Vault: &VaultSource{Mount: "analytics"}},
			"analytics-engineers": {Vault: &VaultSource{Mount: "analytics-engineers"}},
		},
		Targets: map[string]Target{
			"Serverless_Stg": {
				AccountID: "111",
				Imports:   []string{"analytics", "analytics-engineers"},
			},
			"Serverless_Prod": {
				AccountID: "222",
				Imports:   []string{"Serverless_Stg"},
			},
			"livequery_demos": {
				AccountID: "222",
				Imports:   []string{"Serverless_Prod"},
			},
		},
	}

	graph, err := BuildGraph(cfg)
	require.NoError(t, err)

	// Check nodes exist
	assert.Len(t, graph.Nodes, 5) // 2 sources + 3 targets

	// Check levels
	assert.Equal(t, 0, graph.Nodes["analytics"].Level)
	assert.Equal(t, 0, graph.Nodes["analytics-engineers"].Level)
	assert.Equal(t, 1, graph.Nodes["Serverless_Stg"].Level)
	assert.Equal(t, 2, graph.Nodes["Serverless_Prod"].Level)
	assert.Equal(t, 3, graph.Nodes["livequery_demos"].Level)

	// Check dependencies
	assert.Contains(t, graph.Nodes["Serverless_Stg"].Deps, "analytics")
	assert.Contains(t, graph.Nodes["Serverless_Stg"].Deps, "analytics-engineers")
	assert.Contains(t, graph.Nodes["Serverless_Prod"].Deps, "Serverless_Stg")
}

func TestBuildGraphCircularDependency(t *testing.T) {
	cfg := &Config{
		Sources: map[string]Source{
			"analytics": {Vault: &VaultSource{Mount: "analytics"}},
		},
		Targets: map[string]Target{
			"A": {AccountID: "111", Imports: []string{"B"}},
			"B": {AccountID: "222", Imports: []string{"C"}},
			"C": {AccountID: "333", Imports: []string{"A"}}, // Cycle!
		},
	}

	_, err := BuildGraph(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "circular dependency")
}

func TestBuildGraphInvalidImport(t *testing.T) {
	cfg := &Config{
		Sources: map[string]Source{
			"analytics": {Vault: &VaultSource{Mount: "analytics"}},
		},
		Targets: map[string]Target{
			"Stg": {AccountID: "111", Imports: []string{"nonexistent"}},
		},
	}

	_, err := BuildGraph(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown source/target")
}

func TestTopologicalOrder(t *testing.T) {
	cfg := &Config{
		Sources: map[string]Source{
			"analytics":           {Vault: &VaultSource{Mount: "analytics"}},
			"analytics-engineers": {Vault: &VaultSource{Mount: "analytics-engineers"}},
		},
		Targets: map[string]Target{
			"Serverless_Stg": {
				AccountID: "111",
				Imports:   []string{"analytics", "analytics-engineers"},
			},
			"Serverless_Prod": {
				AccountID: "222",
				Imports:   []string{"Serverless_Stg"},
			},
			"livequery_demos": {
				AccountID: "333",
				Imports:   []string{"Serverless_Prod"},
			},
			"Analytics_Testbed": {
				AccountID: "444",
				Imports:   []string{"analytics", "analytics-engineers"},
			},
		},
	}

	graph, err := BuildGraph(cfg)
	require.NoError(t, err)

	order := graph.TopologicalOrder()
	
	// Check that we have all targets
	assert.Len(t, order, 4)

	// Check ordering constraints
	stgIdx := indexOf(order, "Serverless_Stg")
	prodIdx := indexOf(order, "Serverless_Prod")
	demoIdx := indexOf(order, "livequery_demos")
	testbedIdx := indexOf(order, "Analytics_Testbed")

	// Stg must come before Prod
	assert.Less(t, stgIdx, prodIdx, "Serverless_Stg must come before Serverless_Prod")
	
	// Prod must come before demos
	assert.Less(t, prodIdx, demoIdx, "Serverless_Prod must come before livequery_demos")
	
	// Testbed has no dependency on Stg/Prod chain, but should be at same level as Stg
	assert.Equal(t, graph.Nodes["Serverless_Stg"].Level, graph.Nodes["Analytics_Testbed"].Level)
	
	// Both should come before Prod
	assert.Less(t, testbedIdx, prodIdx)
}

func TestGroupByLevel(t *testing.T) {
	cfg := &Config{
		Sources: map[string]Source{
			"analytics": {Vault: &VaultSource{Mount: "analytics"}},
		},
		Targets: map[string]Target{
			"Stg":   {AccountID: "111", Imports: []string{"analytics"}},
			"Prod":  {AccountID: "222", Imports: []string{"Stg"}},
			"Demo":  {AccountID: "333", Imports: []string{"Prod"}},
			"Other": {AccountID: "444", Imports: []string{"analytics"}}, // Same level as Stg
		},
	}

	graph, err := BuildGraph(cfg)
	require.NoError(t, err)

	levels := graph.GroupByLevel()

	// Level 0: empty (sources only)
	// Level 1: Stg, Other
	// Level 2: Prod
	// Level 3: Demo

	assert.Len(t, levels, 4)
	assert.Len(t, levels[0], 0) // No targets at level 0
	assert.Len(t, levels[1], 2) // Stg, Other
	assert.Len(t, levels[2], 1) // Prod
	assert.Len(t, levels[3], 1) // Demo

	assert.Contains(t, levels[1], "Stg")
	assert.Contains(t, levels[1], "Other")
	assert.Contains(t, levels[2], "Prod")
	assert.Contains(t, levels[3], "Demo")
}

func TestIncludeDependencies(t *testing.T) {
	cfg := &Config{
		Sources: map[string]Source{
			"analytics": {Vault: &VaultSource{Mount: "analytics"}},
		},
		Targets: map[string]Target{
			"Stg":   {AccountID: "111", Imports: []string{"analytics"}},
			"Prod":  {AccountID: "222", Imports: []string{"Stg"}},
			"Demo":  {AccountID: "333", Imports: []string{"Prod"}},
			"Other": {AccountID: "444", Imports: []string{"analytics"}},
		},
	}

	graph, err := BuildGraph(cfg)
	require.NoError(t, err)

	// Request just Demo - should include Prod and Stg
	expanded := graph.IncludeDependencies([]string{"Demo"})

	assert.Len(t, expanded, 3)
	assert.Contains(t, expanded, "Stg")
	assert.Contains(t, expanded, "Prod")
	assert.Contains(t, expanded, "Demo")
	assert.NotContains(t, expanded, "Other") // Not in dependency chain

	// Verify order
	stgIdx := indexOf(expanded, "Stg")
	prodIdx := indexOf(expanded, "Prod")
	demoIdx := indexOf(expanded, "Demo")
	assert.Less(t, stgIdx, prodIdx)
	assert.Less(t, prodIdx, demoIdx)
}

func indexOf(slice []string, item string) int {
	for i, v := range slice {
		if v == item {
			return i
		}
	}
	return -1
}

func TestPrintGraph(t *testing.T) {
	cfg := &Config{
		Sources: map[string]Source{
			"analytics": {Vault: &VaultSource{Mount: "analytics"}},
		},
		Targets: map[string]Target{
			"Stg":  {AccountID: "111", Imports: []string{"analytics"}},
			"Prod": {AccountID: "222", Imports: []string{"Stg"}},
		},
	}

	graph, err := BuildGraph(cfg)
	require.NoError(t, err)

	output := graph.PrintGraph()

	assert.Contains(t, output, "Dependency Graph")
	assert.Contains(t, output, "Level 1")
	assert.Contains(t, output, "Level 2")
	assert.Contains(t, output, "Prod <- [Stg]")
}
