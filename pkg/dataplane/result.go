package dataplane

import (
	"fmt"
	"strings"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
)

const (
	statusSuccess     = "SUCCESS"
	noChangesDetected = "No changes detected"
)

// SyncMode indicates which sync strategy was used.
type SyncMode string

const (
	// SyncModeNoChanges indicates the diff was empty; no API calls were made.
	SyncModeNoChanges SyncMode = "no_changes"
	// SyncModeRuntime indicates the runtime path: one PushRawConfigurationSkipReload
	// with X-Runtime-Actions, no HAProxy reload.
	SyncModeRuntime SyncMode = "runtime"
	// SyncModeReload indicates the reload path: optional skip_reload+actions push
	// to seed the live worker, then force_reload.
	SyncModeReload SyncMode = "reload"
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

	// Details contains detailed diff information
	// This field is always populated regardless of SyncMode
	Details DiffDetails

	// Message provides additional context about the result
	Message string

	// PostSyncVersion is the config version on the pod after a successful sync.
	// Callers can cache this alongside the desired parsed config to skip
	// redundant GetRawConfiguration() + parse on subsequent syncs when the
	// pod's version hasn't changed. Zero means version was not captured.
	//
	// Never carries 1: version 1 is the headerless sentinel — a config
	// written by a skip_version push (the runtime bypass) has no
	// `# _version=N` header and GetVersion reads it as 1 regardless of
	// body, so 1 cannot discriminate states and must never be cached
	// (see fetchCurrentConfig). The runtime fast path (SyncRuntimeFast)
	// deliberately leaves PostSyncVersion zero and PostSyncParsedConfig
	// nil: it is fetch-free by design, and its skip_version push strips
	// the version header, forcing the next versioned sync to take a
	// cache miss and re-fetch the pod's actual state.
	PostSyncVersion int64

	// PostSyncParsedConfig is the pod's actual configuration AFTER the sync
	// completed, fetched and parsed from the dataplane API. Set only when
	// AppliedOperations is non-empty AND the post-sync fetch+parse
	// succeeded; nil otherwise (no-changes paths, or fetch/parse failure —
	// callers fall back to the desired config in those cases).
	//
	// Callers should cache this in preference to their input desired config:
	// incremental dataplane patches don't guarantee byte-identity with the
	// caller's intent (different starting baselines across pods produce
	// logically-equivalent-but-byte-different end states). Caching the
	// actual post-sync state lets subsequent drift checks compare apples to
	// apples — the comparator sees pod-actual vs desired, not
	// desired vs desired.
	PostSyncParsedConfig *parserconfig.StructuredConfig
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

	// Backend diff fields: for each modified backend, the BackendBase fields that differ.
	// Only populated when backend attribute changes (not nested collections) cause the update.
	BackendDiffFields map[string][]string

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

	status := statusSuccess
	if !r.Success {
		status = "FAILED"
	}
	parts = append(parts,
		fmt.Sprintf("Status: %s", status),
		fmt.Sprintf("Duration: %s", r.Duration))

	switch r.SyncMode {
	case SyncModeNoChanges:
		parts = append(parts, "Mode: No changes")
	case SyncModeRuntime:
		parts = append(parts, "Mode: Runtime (skip_reload + X-Runtime-Actions)")
	case SyncModeReload:
		parts = append(parts, "Mode: Reload (force_reload raw push)")
	}

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
		parts = append(parts, "Reload: Not triggered (runtime path)")
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
		return noChangesDetected
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
	sum := func(m map[string][]string) int {
		total := 0
		for _, items := range m {
			total += len(items)
		}
		return total
	}
	return d.appendSimpleCountChanges(parts, sum(added), sum(modified), sum(deleted), resourceType)
}

// appendIntMapCountChanges appends formatted counts from maps of ints.
func (d *DiffDetails) appendIntMapCountChanges(parts []string, added, modified, deleted map[string]int, resourceType string) []string {
	sum := func(m map[string]int) int {
		total := 0
		for _, count := range m {
			total += count
		}
		return total
	}
	return d.appendSimpleCountChanges(parts, sum(added), sum(modified), sum(deleted), resourceType)
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
