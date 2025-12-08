package diff

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDiffSecrets_NoChanges(t *testing.T) {
	current := map[string]interface{}{
		"api-keys/stripe": map[string]interface{}{
			"STRIPE_KEY": "sk_xxx",
		},
	}
	desired := map[string]interface{}{
		"api-keys/stripe": map[string]interface{}{
			"STRIPE_KEY": "sk_xxx",
		},
	}

	changes := DiffSecrets(current, desired)
	summary := ComputeSummary(changes)

	if !summary.IsZeroSum() {
		t.Errorf("expected zero-sum, got: +%d -%d ~%d", summary.Added, summary.Removed, summary.Modified)
	}
	if summary.Unchanged != 1 {
		t.Errorf("expected 1 unchanged, got %d", summary.Unchanged)
	}
}

func TestDiffSecrets_AddedSecret(t *testing.T) {
	current := map[string]interface{}{}
	desired := map[string]interface{}{
		"api-keys/stripe": map[string]interface{}{
			"STRIPE_KEY": "sk_xxx",
		},
	}

	changes := DiffSecrets(current, desired)
	summary := ComputeSummary(changes)

	if summary.Added != 1 {
		t.Errorf("expected 1 added, got %d", summary.Added)
	}
	if changes[0].ChangeType != ChangeTypeAdded {
		t.Errorf("expected added change type, got %s", changes[0].ChangeType)
	}
}

func TestDiffSecrets_RemovedSecret(t *testing.T) {
	current := map[string]interface{}{
		"api-keys/stripe": map[string]interface{}{
			"STRIPE_KEY": "sk_xxx",
		},
	}
	desired := map[string]interface{}{}

	changes := DiffSecrets(current, desired)
	summary := ComputeSummary(changes)

	if summary.Removed != 1 {
		t.Errorf("expected 1 removed, got %d", summary.Removed)
	}
	if changes[0].ChangeType != ChangeTypeRemoved {
		t.Errorf("expected removed change type, got %s", changes[0].ChangeType)
	}
}

func TestDiffSecrets_ModifiedSecret(t *testing.T) {
	current := map[string]interface{}{
		"api-keys/stripe": map[string]interface{}{
			"STRIPE_KEY": "sk_old",
		},
	}
	desired := map[string]interface{}{
		"api-keys/stripe": map[string]interface{}{
			"STRIPE_KEY": "sk_new",
		},
	}

	changes := DiffSecrets(current, desired)
	summary := ComputeSummary(changes)

	if summary.Modified != 1 {
		t.Errorf("expected 1 modified, got %d", summary.Modified)
	}
	if changes[0].ChangeType != ChangeTypeModified {
		t.Errorf("expected modified change type, got %s", changes[0].ChangeType)
	}
}

func TestDiffSecrets_KeyLevelChanges(t *testing.T) {
	current := map[string]interface{}{
		"config": map[string]interface{}{
			"existing_key":  "value1",
			"removed_key":   "value2",
			"modified_key":  "old_value",
		},
	}
	desired := map[string]interface{}{
		"config": map[string]interface{}{
			"existing_key":  "value1",     // unchanged
			"added_key":     "new_value",  // added
			"modified_key":  "new_value",  // modified
			// removed_key is gone
		},
	}

	changes := DiffSecrets(current, desired)
	
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}

	c := changes[0]
	if c.ChangeType != ChangeTypeModified {
		t.Errorf("expected modified, got %s", c.ChangeType)
	}

	if len(c.KeysAdded) != 1 || c.KeysAdded[0] != "added_key" {
		t.Errorf("expected [added_key], got %v", c.KeysAdded)
	}
	if len(c.KeysRemoved) != 1 || c.KeysRemoved[0] != "removed_key" {
		t.Errorf("expected [removed_key], got %v", c.KeysRemoved)
	}
	if len(c.KeysModified) != 1 || c.KeysModified[0] != "modified_key" {
		t.Errorf("expected [modified_key], got %v", c.KeysModified)
	}
}

func TestDiffSecrets_ComplexScenario(t *testing.T) {
	// Simulate a real-world migration scenario
	current := map[string]interface{}{
		"api-keys/stripe":   map[string]interface{}{"KEY": "sk_xxx"},
		"api-keys/datadog":  map[string]interface{}{"API_KEY": "dd_xxx"},
		"database/postgres": map[string]interface{}{"HOST": "old.db.com", "PASSWORD": "old"},
		"legacy/config":     map[string]interface{}{"OLD": "value"},
	}
	desired := map[string]interface{}{
		"api-keys/stripe":   map[string]interface{}{"KEY": "sk_xxx"},           // unchanged
		"api-keys/datadog":  map[string]interface{}{"API_KEY": "dd_yyy"},       // modified
		"database/postgres": map[string]interface{}{"HOST": "new.db.com", "PASSWORD": "new"}, // modified
		"api-keys/newrelic": map[string]interface{}{"KEY": "nr_xxx"},           // added
		// legacy/config removed
	}

	changes := DiffSecrets(current, desired)
	summary := ComputeSummary(changes)

	if summary.Added != 1 {
		t.Errorf("expected 1 added, got %d", summary.Added)
	}
	if summary.Removed != 1 {
		t.Errorf("expected 1 removed, got %d", summary.Removed)
	}
	if summary.Modified != 2 {
		t.Errorf("expected 2 modified, got %d", summary.Modified)
	}
	if summary.Unchanged != 1 {
		t.Errorf("expected 1 unchanged, got %d", summary.Unchanged)
	}
	if summary.Total != 5 {
		t.Errorf("expected 5 total, got %d", summary.Total)
	}
}

func TestPipelineDiff_ZeroSum(t *testing.T) {
	diff := &PipelineDiff{
		Summary: ChangeSummary{
			Unchanged: 10,
			Total:     10,
		},
	}

	if !diff.IsZeroSum() {
		t.Error("expected zero-sum")
	}
	if diff.ExitCode() != 0 {
		t.Errorf("expected exit code 0, got %d", diff.ExitCode())
	}
}

func TestPipelineDiff_HasChanges(t *testing.T) {
	diff := &PipelineDiff{
		Summary: ChangeSummary{
			Added:     2,
			Modified:  1,
			Unchanged: 7,
			Total:     10,
		},
	}

	if diff.IsZeroSum() {
		t.Error("expected not zero-sum")
	}
	if diff.ExitCode() != 1 {
		t.Errorf("expected exit code 1, got %d", diff.ExitCode())
	}
}

func TestFormatDiff_Human(t *testing.T) {
	diff := &PipelineDiff{
		DryRun: true,
		Targets: []TargetDiff{
			{
				Target: "Serverless_Stg",
				Changes: []SecretChange{
					{Path: "api-keys/new", ChangeType: ChangeTypeAdded, DesiredKeys: []string{"KEY"}},
					{Path: "api-keys/old", ChangeType: ChangeTypeRemoved},
				},
				Summary: ChangeSummary{Added: 1, Removed: 1, Total: 2},
			},
		},
		Summary: ChangeSummary{Added: 1, Removed: 1, Total: 2},
	}

	output := FormatDiff(diff, OutputFormatHuman)

	if !strings.Contains(output, "DRY RUN") {
		t.Error("expected DRY RUN header")
	}
	if !strings.Contains(output, "Added:     1") {
		t.Error("expected added count")
	}
	if !strings.Contains(output, "+ api-keys/new") {
		t.Error("expected added secret")
	}
	if !strings.Contains(output, "- api-keys/old") {
		t.Error("expected removed secret")
	}
}

func TestFormatDiff_JSON(t *testing.T) {
	diff := &PipelineDiff{
		Summary: ChangeSummary{Added: 1, Total: 1},
	}

	output := FormatDiff(diff, OutputFormatJSON)

	var parsed PipelineDiff
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}
	if parsed.Summary.Added != 1 {
		t.Error("JSON didn't preserve data")
	}
}

func TestFormatDiff_GitHub(t *testing.T) {
	diff := &PipelineDiff{
		Summary: ChangeSummary{Added: 2, Modified: 1, Unchanged: 5, Total: 8},
	}

	output := FormatDiff(diff, OutputFormatGitHub)

	if !strings.Contains(output, "::set-output name=changes::3") {
		t.Error("expected changes output")
	}
	if !strings.Contains(output, "::set-output name=zero_sum::false") {
		t.Error("expected zero_sum output")
	}
	if !strings.Contains(output, "::warning::") {
		t.Error("expected warning annotation")
	}
}

func TestFormatDiff_GitHubZeroSum(t *testing.T) {
	diff := &PipelineDiff{
		Summary: ChangeSummary{Unchanged: 5, Total: 5},
	}

	output := FormatDiff(diff, OutputFormatGitHub)

	if !strings.Contains(output, "::set-output name=zero_sum::true") {
		t.Error("expected zero_sum=true output")
	}
	if !strings.Contains(output, "::notice::") {
		t.Error("expected notice annotation for zero-sum")
	}
}

func TestFormatDiff_Compact(t *testing.T) {
	diff := &PipelineDiff{
		Summary: ChangeSummary{Added: 2, Removed: 1, Modified: 3, Unchanged: 10, Total: 16},
	}

	output := FormatDiff(diff, OutputFormatCompact)

	expected := "CHANGES: +2 -1 ~3 =10 (total: 16)"
	if output != expected {
		t.Errorf("expected %q, got %q", expected, output)
	}
}

func TestFormatDiff_CompactZeroSum(t *testing.T) {
	diff := &PipelineDiff{
		Summary: ChangeSummary{Unchanged: 5, Total: 5},
	}

	output := FormatDiff(diff, OutputFormatCompact)

	if !strings.Contains(output, "ZERO-SUM") {
		t.Error("expected ZERO-SUM in compact output")
	}
}

func TestChangeSummary_IsZeroSum(t *testing.T) {
	tests := []struct {
		name     string
		summary  ChangeSummary
		expected bool
	}{
		{"all unchanged", ChangeSummary{Unchanged: 10}, true},
		{"has added", ChangeSummary{Added: 1, Unchanged: 9}, false},
		{"has removed", ChangeSummary{Removed: 1, Unchanged: 9}, false},
		{"has modified", ChangeSummary{Modified: 1, Unchanged: 9}, false},
		{"empty", ChangeSummary{}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.summary.IsZeroSum(); got != tt.expected {
				t.Errorf("IsZeroSum() = %v, want %v", got, tt.expected)
			}
		})
	}
}
