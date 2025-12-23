package comparator

import (
	"fmt"
	"sort"
	"strings"

	"haptic/pkg/dataplane/comparator/sections"
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

	// Other section changes (extensible for future sections)
	OtherChanges map[string]int // section name -> count of changes
}

// NewDiffSummary creates an empty DiffSummary with initialized maps.
func NewDiffSummary() DiffSummary {
	return DiffSummary{
		ServersAdded:    make(map[string][]string),
		ServersModified: make(map[string][]string),
		ServersDeleted:  make(map[string][]string),
		OtherChanges:    make(map[string]int),
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
	parts = append(parts, s.formatServerChanges()...)
	parts = append(parts, s.formatOtherChanges()...)

	return strings.Join(parts, "\n")
}

// formatFrontendChanges formats the frontend changes section.
func (s *DiffSummary) formatFrontendChanges() []string {
	var parts []string

	if len(s.FrontendsAdded) > 0 {
		parts = append(parts, fmt.Sprintf("- Frontends added: %s", strings.Join(s.FrontendsAdded, ", ")))
	}
	if len(s.FrontendsModified) > 0 {
		parts = append(parts, fmt.Sprintf("- Frontends modified: %s", strings.Join(s.FrontendsModified, ", ")))
	}
	if len(s.FrontendsDeleted) > 0 {
		parts = append(parts, fmt.Sprintf("- Frontends deleted: %s", strings.Join(s.FrontendsDeleted, ", ")))
	}

	return parts
}

// formatBackendChanges formats the backend changes section.
func (s *DiffSummary) formatBackendChanges() []string {
	var parts []string

	if len(s.BackendsAdded) > 0 {
		parts = append(parts, fmt.Sprintf("- Backends added: %s", strings.Join(s.BackendsAdded, ", ")))
	}
	if len(s.BackendsModified) > 0 {
		parts = append(parts, fmt.Sprintf("- Backends modified: %s", strings.Join(s.BackendsModified, ", ")))
	}
	if len(s.BackendsDeleted) > 0 {
		parts = append(parts, fmt.Sprintf("- Backends deleted: %s", strings.Join(s.BackendsDeleted, ", ")))
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
	sort.Strings(serverChanges)
	return fmt.Sprintf("- Servers %s: %s", changeType, strings.Join(serverChanges, ", "))
}

// formatOtherChanges formats the other changes section.
func (s *DiffSummary) formatOtherChanges() []string {
	var parts []string

	if len(s.OtherChanges) > 0 {
		var otherSections []string
		for section, count := range s.OtherChanges {
			otherSections = append(otherSections, fmt.Sprintf("%s: %d", section, count))
		}
		sort.Strings(otherSections)
		parts = append(parts, fmt.Sprintf("- Other changes: %s", strings.Join(otherSections, ", ")))
	}

	return parts
}

// 3. Updates (any order - resources already exist).
func OrderOperations(ops []Operation) []Operation {
	if len(ops) == 0 {
		return ops
	}

	// Separate operations by type
	var creates, updates, deletes []Operation
	for _, op := range ops {
		switch op.Type() {
		case sections.OperationCreate:
			creates = append(creates, op)
		case sections.OperationUpdate:
			updates = append(updates, op)
		case sections.OperationDelete:
			deletes = append(deletes, op)
		}
	}

	// Sort creates by priority (ascending: parents first)
	// Using stable sort to preserve original order when priorities are equal
	// (e.g., ACLs must be created in index order: 0, 1, 2, not randomized)
	sort.SliceStable(creates, func(i, j int) bool {
		return creates[i].Priority() < creates[j].Priority()
	})

	// Sort deletes by priority (descending: children first)
	// Using stable sort for consistent ordering of operations with equal priority
	sort.SliceStable(deletes, func(i, j int) bool {
		return deletes[i].Priority() > deletes[j].Priority()
	})

	// Combine in execution order: deletes → creates → updates
	ordered := make([]Operation, 0, len(ops))
	ordered = append(ordered, deletes...)
	ordered = append(ordered, creates...)
	ordered = append(ordered, updates...)

	return ordered
}
