package synchronizer

import (
	"fmt"
	"strings"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator"
)

// SyncResult represents the outcome of a synchronization operation.
type SyncResult struct {
	// Success indicates whether the sync completed successfully
	Success bool

	// Policy used for this sync
	Policy SyncPolicy

	// Diff contains the configuration differences that were (or would be) applied
	Diff *comparator.ConfigDiff

	// Applied operations (may be subset if ContinueOnError is false)
	AppliedOperations []comparator.Operation

	// Failed operations with their errors
	FailedOperations []OperationError

	// Duration of the sync operation
	Duration time.Duration

	// Retries indicates how many times the operation was retried
	Retries int

	// Message provides additional context about the result
	Message string
}

// OperationError represents a failed operation with its error.
type OperationError struct {
	Operation comparator.Operation
	Cause     error
}

// HasChanges returns true if there are configuration changes.
func (r *SyncResult) HasChanges() bool {
	return r.Diff != nil && r.Diff.Summary.HasChanges()
}

// HasFailures returns true if any operations failed.
func (r *SyncResult) HasFailures() bool {
	return len(r.FailedOperations) > 0
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
		fmt.Sprintf("Policy: %s", r.Policy),
		fmt.Sprintf("Duration: %s (retries: %d)", r.Duration, r.Retries))

	// Changes summary
	if r.Diff != nil {
		parts = append(parts, fmt.Sprintf("\n%s", r.Diff.Summary.String()))
	}

	// Applied operations
	if len(r.AppliedOperations) > 0 {
		parts = append(parts, fmt.Sprintf("\nApplied: %d operations", len(r.AppliedOperations)))
	}

	// Failed operations
	if r.HasFailures() {
		parts = append(parts, fmt.Sprintf("\nFailed: %d operations", len(r.FailedOperations)))
		for _, fe := range r.FailedOperations {
			parts = append(parts, fmt.Sprintf("  - %s: %v", fe.Operation.Describe(), fe.Cause))
		}
	}

	// Message
	if r.Message != "" {
		parts = append(parts, fmt.Sprintf("\nMessage: %s", r.Message))
	}

	return strings.Join(parts, "\n")
}

// NewSuccessResult creates a successful sync result.
func NewSuccessResult(policy SyncPolicy, diff *comparator.ConfigDiff, applied []comparator.Operation, duration time.Duration, retries int) *SyncResult {
	message := "Synchronization completed successfully"
	if policy.IsDryRun() {
		message = "Dry-run completed successfully (no changes applied)"
	}

	return &SyncResult{
		Success:           true,
		Policy:            policy,
		Diff:              diff,
		AppliedOperations: applied,
		FailedOperations:  nil,
		Duration:          duration,
		Retries:           retries,
		Message:           message,
	}
}

// NewFailureResult creates a failed sync result.
func NewFailureResult(policy SyncPolicy, diff *comparator.ConfigDiff, applied []comparator.Operation, failed []OperationError, duration time.Duration, retries int, message string) *SyncResult {
	return &SyncResult{
		Success:           false,
		Policy:            policy,
		Diff:              diff,
		AppliedOperations: applied,
		FailedOperations:  failed,
		Duration:          duration,
		Retries:           retries,
		Message:           message,
	}
}

// NewNoChangesResult creates a result for when there are no changes to apply.
func NewNoChangesResult(policy SyncPolicy, duration time.Duration) *SyncResult {
	return &SyncResult{
		Success:           true,
		Policy:            policy,
		Diff:              nil,
		AppliedOperations: nil,
		FailedOperations:  nil,
		Duration:          duration,
		Retries:           0,
		Message:           "No configuration changes detected",
	}
}
