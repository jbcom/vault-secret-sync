// Package diff provides change detection and reporting for secrets synchronization.
// It enables dry-run validation, zero-sum differential verification, and
// CI/CD-friendly output formats.
package diff

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/jbcom/secretsync/pkg/utils"
)

// ChangeType represents the type of change detected
type ChangeType string

const (
	ChangeTypeAdded     ChangeType = "added"
	ChangeTypeRemoved   ChangeType = "removed"
	ChangeTypeModified  ChangeType = "modified"
	ChangeTypeUnchanged ChangeType = "unchanged"
)

// SecretChange represents a change to a single secret
type SecretChange struct {
	Path       string                 `json:"path"`
	ChangeType ChangeType             `json:"change_type"`
	Target     string                 `json:"target,omitempty"`
	
	// For modified secrets, track key-level changes
	KeysAdded    []string `json:"keys_added,omitempty"`
	KeysRemoved  []string `json:"keys_removed,omitempty"`
	KeysModified []string `json:"keys_modified,omitempty"`
	
	// Current and desired states (values redacted by default)
	CurrentKeys []string `json:"current_keys,omitempty"`
	DesiredKeys []string `json:"desired_keys,omitempty"`
	
	// Hash comparison for change detection without exposing values
	CurrentHash string `json:"current_hash,omitempty"`
	DesiredHash string `json:"desired_hash,omitempty"`
}

// TargetDiff represents all changes for a single target
type TargetDiff struct {
	Target  string          `json:"target"`
	Changes []SecretChange  `json:"changes"`
	Summary ChangeSummary   `json:"summary"`
}

// ChangeSummary provides statistics about changes
type ChangeSummary struct {
	Added     int `json:"added"`
	Removed   int `json:"removed"`
	Modified  int `json:"modified"`
	Unchanged int `json:"unchanged"`
	Total     int `json:"total"`
}

// IsZeroSum returns true if there are no changes
func (s ChangeSummary) IsZeroSum() bool {
	return s.Added == 0 && s.Removed == 0 && s.Modified == 0
}

// HasChanges returns true if there are any changes
func (s ChangeSummary) HasChanges() bool {
	return !s.IsZeroSum()
}

// PipelineDiff represents the complete diff for a pipeline run
type PipelineDiff struct {
	Targets     []TargetDiff  `json:"targets"`
	Summary     ChangeSummary `json:"summary"`
	DryRun      bool          `json:"dry_run"`
	ConfigPath  string        `json:"config_path,omitempty"`
}

// IsZeroSum returns true if the entire pipeline has no changes
func (p *PipelineDiff) IsZeroSum() bool {
	return p.Summary.IsZeroSum()
}

// ExitCode returns an appropriate exit code for CI/CD:
//   - 0: No changes (zero-sum)
//   - 1: Changes detected
//   - 2: Errors occurred (not handled here)
func (p *PipelineDiff) ExitCode() int {
	if p.IsZeroSum() {
		return 0
	}
	return 1
}

// AddTargetDiff adds a target diff and updates the summary
func (p *PipelineDiff) AddTargetDiff(td TargetDiff) {
	p.Targets = append(p.Targets, td)
	p.Summary.Added += td.Summary.Added
	p.Summary.Removed += td.Summary.Removed
	p.Summary.Modified += td.Summary.Modified
	p.Summary.Unchanged += td.Summary.Unchanged
	p.Summary.Total += td.Summary.Total
}

// DiffSecrets compares two secret maps and returns the changes
func DiffSecrets(current, desired map[string]interface{}) []SecretChange {
	var changes []SecretChange
	seen := make(map[string]bool)

	// Check desired secrets
	for path, desiredVal := range desired {
		seen[path] = true
		currentVal, exists := current[path]

		if !exists {
			// New secret
			changes = append(changes, SecretChange{
				Path:        path,
				ChangeType:  ChangeTypeAdded,
				DesiredKeys: getMapKeys(desiredVal),
			})
			continue
		}

		// Compare values
		if utils.DeepEqual(currentVal, desiredVal) {
			changes = append(changes, SecretChange{
				Path:        path,
				ChangeType:  ChangeTypeUnchanged,
				CurrentKeys: getMapKeys(currentVal),
				DesiredKeys: getMapKeys(desiredVal),
			})
		} else {
			// Modified - compute key-level diff
			change := SecretChange{
				Path:        path,
				ChangeType:  ChangeTypeModified,
				CurrentKeys: getMapKeys(currentVal),
				DesiredKeys: getMapKeys(desiredVal),
			}
			change.KeysAdded, change.KeysRemoved, change.KeysModified = diffMapKeys(currentVal, desiredVal)
			changes = append(changes, change)
		}
	}

	// Check for removed secrets
	for path, currentVal := range current {
		if !seen[path] {
			changes = append(changes, SecretChange{
				Path:        path,
				ChangeType:  ChangeTypeRemoved,
				CurrentKeys: getMapKeys(currentVal),
			})
		}
	}

	// Sort for deterministic output
	sort.Slice(changes, func(i, j int) bool {
		return changes[i].Path < changes[j].Path
	})

	return changes
}

// ComputeSummary calculates summary statistics from changes
func ComputeSummary(changes []SecretChange) ChangeSummary {
	var summary ChangeSummary
	for _, c := range changes {
		switch c.ChangeType {
		case ChangeTypeAdded:
			summary.Added++
		case ChangeTypeRemoved:
			summary.Removed++
		case ChangeTypeModified:
			summary.Modified++
		case ChangeTypeUnchanged:
			summary.Unchanged++
		}
		summary.Total++
	}
	return summary
}

// getMapKeys returns the keys of a value if it's a map
func getMapKeys(v interface{}) []string {
	if m, ok := v.(map[string]interface{}); ok {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return keys
	}
	return nil
}

// diffMapKeys computes key-level differences between two maps
func diffMapKeys(current, desired interface{}) (added, removed, modified []string) {
	currentMap, okCurrent := current.(map[string]interface{})
	desiredMap, okDesired := desired.(map[string]interface{})

	if !okCurrent || !okDesired {
		// One or both aren't maps, treat as complete modification
		return nil, nil, []string{"<value>"}
	}

	seen := make(map[string]bool)

	for k, dv := range desiredMap {
		seen[k] = true
		cv, exists := currentMap[k]
		if !exists {
			added = append(added, k)
		} else if !utils.DeepEqual(cv, dv) {
			modified = append(modified, k)
		}
	}

	for k := range currentMap {
		if !seen[k] {
			removed = append(removed, k)
		}
	}

	sort.Strings(added)
	sort.Strings(removed)
	sort.Strings(modified)

	return added, removed, modified
}

// OutputFormat specifies the output format for diff reporting
type OutputFormat string

const (
	OutputFormatHuman   OutputFormat = "human"
	OutputFormatJSON    OutputFormat = "json"
	OutputFormatGitHub  OutputFormat = "github"  // GitHub Actions annotations
	OutputFormatCompact OutputFormat = "compact" // One-line summary
)

// FormatDiff formats the pipeline diff according to the specified format
func FormatDiff(diff *PipelineDiff, format OutputFormat) string {
	switch format {
	case OutputFormatJSON:
		return formatJSON(diff)
	case OutputFormatGitHub:
		return formatGitHub(diff)
	case OutputFormatCompact:
		return formatCompact(diff)
	default:
		return formatHuman(diff)
	}
}

func formatJSON(diff *PipelineDiff) string {
	data, err := json.MarshalIndent(diff, "", "  ")
	if err != nil {
		return fmt.Sprintf(`{"error": "%s"}`, err.Error())
	}
	return string(data)
}

func formatHuman(diff *PipelineDiff) string {
	var sb strings.Builder

	// Header
	if diff.DryRun {
		sb.WriteString("=== DRY RUN - No changes will be applied ===\n\n")
	}

	// Overall summary
	sb.WriteString("Pipeline Diff Summary\n")
	sb.WriteString("=====================\n")
	sb.WriteString(fmt.Sprintf("  Added:     %d\n", diff.Summary.Added))
	sb.WriteString(fmt.Sprintf("  Removed:   %d\n", diff.Summary.Removed))
	sb.WriteString(fmt.Sprintf("  Modified:  %d\n", diff.Summary.Modified))
	sb.WriteString(fmt.Sprintf("  Unchanged: %d\n", diff.Summary.Unchanged))
	sb.WriteString(fmt.Sprintf("  Total:     %d\n", diff.Summary.Total))
	sb.WriteString("\n")

	if diff.IsZeroSum() {
		sb.WriteString("✅ ZERO-SUM: No changes detected\n")
		return sb.String()
	}

	sb.WriteString("⚠️  CHANGES DETECTED\n\n")

	// Per-target details
	for _, td := range diff.Targets {
		if !td.Summary.HasChanges() {
			continue
		}

		sb.WriteString(fmt.Sprintf("Target: %s\n", td.Target))
		sb.WriteString(strings.Repeat("-", 40) + "\n")

		for _, c := range td.Changes {
			if c.ChangeType == ChangeTypeUnchanged {
				continue
			}

			switch c.ChangeType {
			case ChangeTypeAdded:
				sb.WriteString(fmt.Sprintf("  + %s (new secret)\n", c.Path))
				if len(c.DesiredKeys) > 0 {
					sb.WriteString(fmt.Sprintf("    keys: %v\n", c.DesiredKeys))
				}
			case ChangeTypeRemoved:
				sb.WriteString(fmt.Sprintf("  - %s (removed)\n", c.Path))
			case ChangeTypeModified:
				sb.WriteString(fmt.Sprintf("  ~ %s (modified)\n", c.Path))
				if len(c.KeysAdded) > 0 {
					sb.WriteString(fmt.Sprintf("    + keys: %v\n", c.KeysAdded))
				}
				if len(c.KeysRemoved) > 0 {
					sb.WriteString(fmt.Sprintf("    - keys: %v\n", c.KeysRemoved))
				}
				if len(c.KeysModified) > 0 {
					sb.WriteString(fmt.Sprintf("    ~ keys: %v\n", c.KeysModified))
				}
			}
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

func formatGitHub(diff *PipelineDiff) string {
	var sb strings.Builder

	// Summary as workflow output
	sb.WriteString(fmt.Sprintf("::set-output name=changes::%d\n", diff.Summary.Added+diff.Summary.Removed+diff.Summary.Modified))
	sb.WriteString(fmt.Sprintf("::set-output name=added::%d\n", diff.Summary.Added))
	sb.WriteString(fmt.Sprintf("::set-output name=removed::%d\n", diff.Summary.Removed))
	sb.WriteString(fmt.Sprintf("::set-output name=modified::%d\n", diff.Summary.Modified))
	sb.WriteString(fmt.Sprintf("::set-output name=unchanged::%d\n", diff.Summary.Unchanged))
	sb.WriteString(fmt.Sprintf("::set-output name=zero_sum::%t\n", diff.IsZeroSum()))

	if diff.IsZeroSum() {
		sb.WriteString("::notice::✅ Zero-sum: No changes detected\n")
	} else {
		sb.WriteString(fmt.Sprintf("::warning::⚠️ %d changes detected (%d added, %d removed, %d modified)\n",
			diff.Summary.Added+diff.Summary.Removed+diff.Summary.Modified,
			diff.Summary.Added, diff.Summary.Removed, diff.Summary.Modified))
	}

	// Group annotations by target
	for _, td := range diff.Targets {
		if !td.Summary.HasChanges() {
			continue
		}

		sb.WriteString(fmt.Sprintf("::group::Target: %s (%d changes)\n", td.Target,
			td.Summary.Added+td.Summary.Removed+td.Summary.Modified))

		for _, c := range td.Changes {
			switch c.ChangeType {
			case ChangeTypeAdded:
				sb.WriteString(fmt.Sprintf("::notice::+ %s (new secret)\n", c.Path))
			case ChangeTypeRemoved:
				sb.WriteString(fmt.Sprintf("::warning::- %s (removed)\n", c.Path))
			case ChangeTypeModified:
				sb.WriteString(fmt.Sprintf("::notice::~ %s (modified)\n", c.Path))
			}
		}

		sb.WriteString("::endgroup::\n")
	}

	return sb.String()
}

func formatCompact(diff *PipelineDiff) string {
	if diff.IsZeroSum() {
		return fmt.Sprintf("ZERO-SUM: %d secrets unchanged", diff.Summary.Unchanged)
	}
	return fmt.Sprintf("CHANGES: +%d -%d ~%d =%d (total: %d)",
		diff.Summary.Added, diff.Summary.Removed, diff.Summary.Modified,
		diff.Summary.Unchanged, diff.Summary.Total)
}

// DiffResult wraps PipelineDiff with additional metadata for CLI output
type DiffResult struct {
	Diff     *PipelineDiff `json:"diff"`
	ExitCode int           `json:"exit_code"`
	Message  string        `json:"message"`
}

// NewDiffResult creates a DiffResult from a PipelineDiff
func NewDiffResult(diff *PipelineDiff) *DiffResult {
	result := &DiffResult{
		Diff:     diff,
		ExitCode: diff.ExitCode(),
	}

	if diff.IsZeroSum() {
		result.Message = "No changes detected - pipeline is in sync"
	} else {
		result.Message = fmt.Sprintf("%d changes detected",
			diff.Summary.Added+diff.Summary.Removed+diff.Summary.Modified)
	}

	return result
}
