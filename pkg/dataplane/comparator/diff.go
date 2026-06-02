package comparator

import (
	"fmt"
	"slices"
	"strings"
)

// ConfigDiff represents the difference between two HAProxy configurations.
//
// It contains all operations needed to transform the current configuration
// into the desired configuration, along with a summary of changes.
type ConfigDiff struct {
	// Operations is the ordered list of operations to execute
	Operations []Operation

	// Summary provides a high-level overview of changes
	Summary DiffSummary
}

// DiffSummary provides a high-level overview of configuration changes.
//
// This is useful for logging, monitoring, and decision-making about
// whether to proceed with a configuration update.
type DiffSummary struct {
	// Total counts by operation type
	TotalCreates int
	TotalUpdates int
	TotalDeletes int

	// Global and defaults changes
	GlobalChanged   bool
	DefaultsChanged bool

	// Frontend changes
	FrontendsAdded    []string
	FrontendsModified []string
	FrontendsDeleted  []string

	// Backend changes
	BackendsAdded    []string
	BackendsModified []string
	BackendsDeleted  []string

	// Server changes (map of backend -> server names)
	ServersAdded    map[string][]string
	ServersModified map[string][]string
	ServersDeleted  map[string][]string

	// Backend diff fields: for each modified backend, the list of BackendBase fields that differ.
	// Populated only when backend attributes (not nested collections) cause the update.
	// Useful for diagnosing false diffs from parser round-trip asymmetries.
	BackendDiffFields map[string][]string

	// Other section changes (extensible for future sections)
	OtherChanges map[string]int // section name -> count of changes
}

// NewDiffSummary creates an empty DiffSummary with initialized maps.
func NewDiffSummary() DiffSummary {
	return DiffSummary{
		ServersAdded:      make(map[string][]string),
		ServersModified:   make(map[string][]string),
		ServersDeleted:    make(map[string][]string),
		BackendDiffFields: make(map[string][]string),
		OtherChanges:      make(map[string]int),
	}
}

// HasChanges returns true if any configuration changes are present.
func (s *DiffSummary) HasChanges() bool {
	return s.TotalCreates > 0 || s.TotalUpdates > 0 || s.TotalDeletes > 0
}

// TotalOperations returns the total number of operations across all types.
func (s *DiffSummary) TotalOperations() int {
	return s.TotalCreates + s.TotalUpdates + s.TotalDeletes
}

// StructuralOperations returns the number of operations that require HAProxy reload.
// This excludes server UPDATE operations which are runtime-eligible and can be
// applied without reload via the HAProxy Runtime API.
//
// Use this to choose the apply strategy: zero structural operations means the
// diff can be applied with a skip-reload runtime push, since runtime-eligible
// operations should not trigger reloads.
func (s *DiffSummary) StructuralOperations() int {
	// Count server modifications (runtime-eligible, no reload needed)
	serverModifications := 0
	for _, servers := range s.ServersModified {
		serverModifications += len(servers)
	}

	// Structural = all operations minus server modifications
	return s.TotalOperations() - serverModifications
}

// String returns a human-readable summary of changes.
func (s *DiffSummary) String() string {
	if !s.HasChanges() {
		return "No changes"
	}

	var parts []string

	// Operation counts
	parts = append(parts, fmt.Sprintf("Total: %d operations (%d creates, %d updates, %d deletes)",
		s.TotalOperations(), s.TotalCreates, s.TotalUpdates, s.TotalDeletes))

	// Global/defaults changes
	if s.GlobalChanged {
		parts = append(parts, "- Global settings modified")
	}
	if s.DefaultsChanged {
		parts = append(parts, "- Defaults modified")
	}

	// Append section-specific changes
	parts = append(parts, s.formatFrontendChanges()...)
	parts = append(parts, s.formatBackendChanges()...)
	parts = append(parts, s.formatBackendDiffFields()...)
	parts = append(parts, s.formatServerChanges()...)
	parts = append(parts, s.formatOtherChanges()...)

	return strings.Join(parts, "\n")
}

// formatFrontendChanges formats the frontend changes section.
func (s *DiffSummary) formatFrontendChanges() []string {
	return formatNamedChanges("Frontends", s.FrontendsAdded, s.FrontendsModified, s.FrontendsDeleted)
}

// formatBackendChanges formats the backend changes section.
func (s *DiffSummary) formatBackendChanges() []string {
	return formatNamedChanges("Backends", s.BackendsAdded, s.BackendsModified, s.BackendsDeleted)
}

// formatNamedChanges builds the "- <Label> {added,modified,deleted}: a, b, c"
// lines for a section whose changes are tracked as comma-joined name slices.
// Empty slices are skipped so callers don't end up with blank lines.
func formatNamedChanges(label string, added, modified, deleted []string) []string {
	var parts []string
	if len(added) > 0 {
		parts = append(parts, fmt.Sprintf("- %s added: %s", label, strings.Join(added, ", ")))
	}
	if len(modified) > 0 {
		parts = append(parts, fmt.Sprintf("- %s modified: %s", label, strings.Join(modified, ", ")))
	}
	if len(deleted) > 0 {
		parts = append(parts, fmt.Sprintf("- %s deleted: %s", label, strings.Join(deleted, ", ")))
	}
	return parts
}

// formatServerChanges formats the server changes section.
func (s *DiffSummary) formatServerChanges() []string {
	var parts []string

	if len(s.ServersAdded) > 0 {
		parts = append(parts, s.formatServerMapChanges(s.ServersAdded, "added"))
	}
	if len(s.ServersModified) > 0 {
		parts = append(parts, s.formatServerMapChanges(s.ServersModified, "modified"))
	}
	if len(s.ServersDeleted) > 0 {
		parts = append(parts, s.formatServerMapChanges(s.ServersDeleted, "deleted"))
	}

	return parts
}

// formatServerMapChanges formats a server change map (backend -> server list).
func (s *DiffSummary) formatServerMapChanges(serverMap map[string][]string, changeType string) string {
	serverChanges := make([]string, 0, len(serverMap))
	for backend, servers := range serverMap {
		serverChanges = append(serverChanges, fmt.Sprintf("%s: %d", backend, len(servers)))
	}
	slices.Sort(serverChanges)
	return fmt.Sprintf("- Servers %s: %s", changeType, strings.Join(serverChanges, ", "))
}

// formatBackendDiffFields formats the backend diff field diagnostics.
// Groups backends by their differing fields to produce compact output like:
//
//	"- Backend diff fields: [GUID] (48 backends)"
func (s *DiffSummary) formatBackendDiffFields() []string {
	if len(s.BackendDiffFields) == 0 {
		return nil
	}

	// Group backends by their diff field signature for compact output.
	groups := make(map[string]int) // "Field1, Field2" -> count
	for _, fields := range s.BackendDiffFields {
		slices.Sort(fields)
		key := strings.Join(fields, ", ")
		groups[key]++
	}

	var parts []string
	for fields, count := range groups {
		parts = append(parts, fmt.Sprintf("- Backend diff fields: [%s] (%d backends)", fields, count))
	}
	slices.Sort(parts)
	return parts
}

// formatOtherChanges formats the other changes section.
func (s *DiffSummary) formatOtherChanges() []string {
	var parts []string

	if len(s.OtherChanges) > 0 {
		otherSections := make([]string, 0, len(s.OtherChanges))
		for section, count := range s.OtherChanges {
			otherSections = append(otherSections, fmt.Sprintf("%s: %d", section, count))
		}
		slices.Sort(otherSections)
		parts = append(parts, fmt.Sprintf("- Other changes: %s", strings.Join(otherSections, ", ")))
	}

	return parts
}
