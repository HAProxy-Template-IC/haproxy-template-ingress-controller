// Package synchronizer provides configuration synchronization between
// desired state and HAProxy via the Dataplane API.
//
// It orchestrates the comparison, transaction management, and policy
// enforcement for safe configuration updates.
package synchronizer

// SyncPolicy defines how synchronization should be performed.
type SyncPolicy string

const (
	// PolicyDryRun performs comparison and validation but does not apply changes.
	// Useful for preview and testing.
	PolicyDryRun SyncPolicy = "dry-run"

	// PolicyApply applies all changes via the Dataplane API with automatic
	// retry on version conflicts. This is the standard policy for production use.
	PolicyApply SyncPolicy = "apply"

	// PolicyApplyForce applies changes without retry limits. Use with caution
	// as this may result in many retries in high-contention scenarios.
	PolicyApplyForce SyncPolicy = "apply-force"
)

// String returns the string representation of the policy.
func (p SyncPolicy) String() string {
	return string(p)
}

// IsDryRun returns true if this is a dry-run policy.
func (p SyncPolicy) IsDryRun() bool {
	return p == PolicyDryRun
}

// ShouldApply returns true if this policy should apply changes.
func (p SyncPolicy) ShouldApply() bool {
	return p == PolicyApply || p == PolicyApplyForce
}

// MaxRetries returns the maximum number of retries for this policy.
// Returns -1 for unlimited retries (PolicyApplyForce).
func (p SyncPolicy) MaxRetries() int {
	switch p {
	case PolicyDryRun:
		return 0
	case PolicyApply:
		return 3
	case PolicyApplyForce:
		return -1 // unlimited
	default:
		return 3 // safe default
	}
}

// SyncOptions configures the synchronization behavior.
type SyncOptions struct {
	// Policy determines how the sync is performed
	Policy SyncPolicy

	// ContinueOnError determines whether to continue applying operations
	// if one fails. If false, the first error stops execution.
	ContinueOnError bool

	// ValidateBeforeApply runs HAProxy validation before committing changes.
	// This adds an extra API call but provides safety.
	ValidateBeforeApply bool

	// MaxParallel limits concurrent operations during sync.
	// 0 means unlimited (not recommended for large configs).
	MaxParallel int
}

// DefaultSyncOptions returns the default sync options.
func DefaultSyncOptions() SyncOptions {
	return SyncOptions{
		Policy:              PolicyApply,
		ContinueOnError:     false,
		ValidateBeforeApply: true,
	}
}

// DryRunOptions returns options configured for dry-run mode.
func DryRunOptions() SyncOptions {
	return SyncOptions{
		Policy:              PolicyDryRun,
		ContinueOnError:     false,
		ValidateBeforeApply: false,
	}
}
