package dataplane

import (
	"fmt"
	"strings"
	"time"
)

// SyncMode indicates which sync strategy was used.
type SyncMode string

const (
	// SyncModeFineGrained indicates fine-grained API operations were used.
	SyncModeFineGrained SyncMode = "fine_grained"
	// SyncModeRawInitial indicates raw push was used for initial configuration (version=1).
	SyncModeRawInitial SyncMode = "raw_initial"
	// SyncModeRawThreshold indicates raw push was used because changes exceeded threshold.
	SyncModeRawThreshold SyncMode = "raw_threshold"
	// SyncModeRawFallback indicates raw push was used as fallback after fine-grained failure.
	SyncModeRawFallback SyncMode = "raw_fallback"
)

// SyncResult contains detailed information about a sync operation.
type SyncResult struct {
	// Success indicates whether the sync completed successfully
	Success bool

	// AppliedOperations contains structured information about operations that were applied
	AppliedOperations []AppliedOperation

	// ReloadTriggered indicates whether a HAProxy reload was triggered
	// true when commit status is 202, false when 200
	ReloadTriggered bool

	// ReloadID is the reload identifier from the Reload-ID response header
	// Only set when ReloadTriggered is true
	ReloadID string

	// ReloadVerified indicates whether the reload was verified as successful.
	// Only set when VerifyReload option is enabled and ReloadTriggered is true.
	ReloadVerified bool

	// ReloadVerificationError contains an error message if reload verification failed.
	// This includes timeout errors or explicit reload failures from HAProxy.
	ReloadVerificationError string

	// SyncMode indicates which sync strategy was used.
	// See SyncMode* constants for possible values.
	SyncMode SyncMode

	// Duration of the sync operation
	Duration time.Duration

	// Retries indicates how many times operations were retried (for 409 conflicts)
	Retries int

	// Details contains detailed diff information
	// This field is always populated regardless of SyncMode
	Details DiffDetails

	// Message provides additional context about the result
	Message string

	// PostSyncVersion is the config version on the pod after a successful sync.
	// Callers can cache this alongside the desired parsed config to skip
	// redundant GetRawConfiguration() + parse on subsequent syncs when the
	// pod's version hasn't changed. Zero means version was not captured.
	PostSyncVersion int64
}

// UsedRawPush returns true if raw configuration push was used instead of fine-grained sync.
// This is a convenience helper for code that needs to know whether any form of raw push was used.
func (r *SyncResult) UsedRawPush() bool {
	return r.SyncMode != SyncModeFineGrained && r.SyncMode != ""
}

// AppliedOperation represents a single applied configuration change.
type AppliedOperation struct {
	// Type is the operation type: "create", "update", or "delete"
	Type string

	// Section is the configuration section: "backend", "server", "frontend", "acl", "http-rule", etc.
	Section string

	// Resource is the resource name or identifier (e.g., backend name, server name)
	Resource string

	// Description is a human-readable description of what was changed
	Description string
}

// DiffResult contains comparison results without applying changes.
type DiffResult struct {
	// HasChanges indicates whether any differences were detected
	HasChanges bool

	// PlannedOperations contains structured information about operations that would be executed
	PlannedOperations []PlannedOperation

	// Details contains detailed diff information
	Details DiffDetails
}

// PlannedOperation represents an operation that would be executed.
type PlannedOperation struct {
	// Type is the operation type: "create", "update", or "delete"
	Type string

	// Section is the configuration section: "backend", "server", "frontend", "acl", "http-rule", etc.
	Section string

	// Resource is the resource name or identifier
	Resource string

	// Description is a human-readable description of what would be changed
	Description string

	// Priority indicates execution order (lower = earlier for creates, higher = earlier for deletes)
	Priority int
}

// DiffDetails contains detailed diff information about configuration changes.
type DiffDetails struct {
	// Total operation counts
	TotalOperations int
	Creates         int
	Updates         int
	Deletes         int

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

	// ACL changes (map of parent resource -> ACL names)
	ACLsAdded    map[string][]string
	ACLsModified map[string][]string
	ACLsDeleted  map[string][]string

	// HTTP rule changes (map of parent resource -> count)
	HTTPRulesAdded    map[string]int
	HTTPRulesModified map[string]int
	HTTPRulesDeleted  map[string]int

	// Auxiliary file changes
	MapsAdded            int
	MapsModified         int
	MapsDeleted          int
	SSLCertsAdded        int
	SSLCertsModified     int
	SSLCertsDeleted      int
	SSLCaFilesAdded      int
	SSLCaFilesModified   int
	SSLCaFilesDeleted    int
	GeneralFilesAdded    int
	GeneralFilesModified int
	GeneralFilesDeleted  int
}

// NewDiffDetails creates an empty DiffDetails with initialized maps.
func NewDiffDetails() DiffDetails {
	return DiffDetails{
		ServersAdded:      make(map[string][]string),
		ServersModified:   make(map[string][]string),
		ServersDeleted:    make(map[string][]string),
		ACLsAdded:         make(map[string][]string),
		ACLsModified:      make(map[string][]string),
		ACLsDeleted:       make(map[string][]string),
		HTTPRulesAdded:    make(map[string]int),
		HTTPRulesModified: make(map[string]int),
		HTTPRulesDeleted:  make(map[string]int),
	}
}

// String returns a human-readable summary of the sync result.
func (r *SyncResult) String() string {
	var parts []string

	// Status
	status := "SUCCESS"
	if !r.Success {
		status = "FAILED"
	}
	parts = append(parts,
		fmt.Sprintf("Status: %s", status),
		fmt.Sprintf("Duration: %s (retries: %d)", r.Duration, r.Retries))

	// Sync mode indicator
	switch r.SyncMode {
	case SyncModeFineGrained:
		parts = append(parts, "Mode: Fine-grained sync")
	case SyncModeRawInitial:
		parts = append(parts, "Mode: Raw config push (initial configuration)")
	case SyncModeRawThreshold:
		parts = append(parts, "Mode: Raw config push (threshold exceeded)")
	case SyncModeRawFallback:
		parts = append(parts, "Mode: Raw config push (fallback)")
	default:
		parts = append(parts, "Mode: Fine-grained sync")
	}

	// Reload info
	if r.ReloadTriggered {
		if r.ReloadID != "" {
			if r.ReloadVerified {
				parts = append(parts, fmt.Sprintf("Reload: Verified (ID: %s)", r.ReloadID))
			} else if r.ReloadVerificationError != "" {
				parts = append(parts, fmt.Sprintf("Reload: Failed (ID: %s) - %s", r.ReloadID, r.ReloadVerificationError))
			} else {
				parts = append(parts, fmt.Sprintf("Reload: Triggered (ID: %s)", r.ReloadID))
			}
		} else {
			parts = append(parts, "Reload: Triggered")
		}
	} else {
		parts = append(parts, "Reload: Not triggered (runtime API used)")
	}

	// Operations summary
	if len(r.AppliedOperations) > 0 {
		parts = append(parts,
			fmt.Sprintf("\nApplied: %d operations", len(r.AppliedOperations)),
			fmt.Sprintf("  Creates: %d, Updates: %d, Deletes: %d",
				r.Details.Creates, r.Details.Updates, r.Details.Deletes))
	}

	// Details summary
	if r.Details.TotalOperations > 0 {
		parts = append(parts, fmt.Sprintf("\n%s", r.Details.String()))
	}

	// Message
	if r.Message != "" {
		parts = append(parts, fmt.Sprintf("\nMessage: %s", r.Message))
	}

	return strings.Join(parts, "\n")
}

// String returns a human-readable summary of the diff details.
func (d *DiffDetails) String() string {
	if d.TotalOperations == 0 {
		return "No changes detected"
	}

	var parts []string

	// Global/defaults changes
	if d.GlobalChanged {
		parts = append(parts, "- Global settings modified")
	}
	if d.DefaultsChanged {
		parts = append(parts, "- Defaults modified")
	}

	// Resource changes (frontends, backends)
	parts = d.appendResourceChanges(parts, d.FrontendsAdded, d.FrontendsModified, d.FrontendsDeleted, "Frontends")
	parts = d.appendResourceChanges(parts, d.BackendsAdded, d.BackendsModified, d.BackendsDeleted, "Backends")

	// Map-based changes (servers, ACLs)
	parts = d.appendMapCountChanges(parts, d.ServersAdded, d.ServersModified, d.ServersDeleted, "Servers")
	parts = d.appendMapCountChanges(parts, d.ACLsAdded, d.ACLsModified, d.ACLsDeleted, "ACLs")

	// Int map changes (HTTP rules)
	parts = d.appendIntMapCountChanges(parts, d.HTTPRulesAdded, d.HTTPRulesModified, d.HTTPRulesDeleted, "HTTP rules")

	// Auxiliary file changes
	parts = d.appendSimpleCountChanges(parts, d.MapsAdded, d.MapsModified, d.MapsDeleted, "Maps")
	parts = d.appendSimpleCountChanges(parts, d.SSLCertsAdded, d.SSLCertsModified, d.SSLCertsDeleted, "SSL certs")
	parts = d.appendSimpleCountChanges(parts, d.GeneralFilesAdded, d.GeneralFilesModified, d.GeneralFilesDeleted, "General files")

	return strings.Join(parts, "\n")
}

// appendResourceChanges appends formatted resource change messages.
func (d *DiffDetails) appendResourceChanges(parts, added, modified, deleted []string, resourceType string) []string {
	if len(added) > 0 {
		parts = append(parts, fmt.Sprintf("- %s added: %s", resourceType, strings.Join(added, ", ")))
	}
	if len(modified) > 0 {
		parts = append(parts, fmt.Sprintf("- %s modified: %s", resourceType, strings.Join(modified, ", ")))
	}
	if len(deleted) > 0 {
		parts = append(parts, fmt.Sprintf("- %s deleted: %s", resourceType, strings.Join(deleted, ", ")))
	}
	return parts
}

// appendMapCountChanges appends formatted counts from maps of slices.
func (d *DiffDetails) appendMapCountChanges(parts []string, added, modified, deleted map[string][]string, resourceType string) []string {
	totalAdded := 0
	for _, items := range added {
		totalAdded += len(items)
	}
	totalModified := 0
	for _, items := range modified {
		totalModified += len(items)
	}
	totalDeleted := 0
	for _, items := range deleted {
		totalDeleted += len(items)
	}

	if totalAdded > 0 {
		parts = append(parts, fmt.Sprintf("- %s added: %d", resourceType, totalAdded))
	}
	if totalModified > 0 {
		parts = append(parts, fmt.Sprintf("- %s modified: %d", resourceType, totalModified))
	}
	if totalDeleted > 0 {
		parts = append(parts, fmt.Sprintf("- %s deleted: %d", resourceType, totalDeleted))
	}
	return parts
}

// appendIntMapCountChanges appends formatted counts from maps of ints.
func (d *DiffDetails) appendIntMapCountChanges(parts []string, added, modified, deleted map[string]int, resourceType string) []string {
	totalAdded := 0
	for _, count := range added {
		totalAdded += count
	}
	totalModified := 0
	for _, count := range modified {
		totalModified += count
	}
	totalDeleted := 0
	for _, count := range deleted {
		totalDeleted += count
	}

	if totalAdded > 0 {
		parts = append(parts, fmt.Sprintf("- %s added: %d", resourceType, totalAdded))
	}
	if totalModified > 0 {
		parts = append(parts, fmt.Sprintf("- %s modified: %d", resourceType, totalModified))
	}
	if totalDeleted > 0 {
		parts = append(parts, fmt.Sprintf("- %s deleted: %d", resourceType, totalDeleted))
	}
	return parts
}

// appendSimpleCountChanges appends formatted counts for simple integer counters.
func (d *DiffDetails) appendSimpleCountChanges(parts []string, added, modified, deleted int, resourceType string) []string {
	if added > 0 {
		parts = append(parts, fmt.Sprintf("- %s added: %d", resourceType, added))
	}
	if modified > 0 {
		parts = append(parts, fmt.Sprintf("- %s modified: %d", resourceType, modified))
	}
	if deleted > 0 {
		parts = append(parts, fmt.Sprintf("- %s deleted: %d", resourceType, deleted))
	}
	return parts
}

// String returns a human-readable summary of the diff result.
func (r *DiffResult) String() string {
	if !r.HasChanges {
		return "No changes detected"
	}

	var parts []string
	parts = append(parts,
		fmt.Sprintf("Total operations: %d", len(r.PlannedOperations)),
		fmt.Sprintf("\n%s", r.Details.String()))

	return strings.Join(parts, "\n")
}
