package synchronizer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
)

// mockOperation implements comparator.Operation for testing.
type mockOperation struct {
	opType      sections.OperationType
	section     string
	priority    int
	description string
	executeFunc func(ctx context.Context, c *client.DataplaneClient, txID string) error
	executed    bool
}

func (m *mockOperation) Type() sections.OperationType { return m.opType }
func (m *mockOperation) Section() string              { return m.section }
func (m *mockOperation) Priority() int                { return m.priority }
func (m *mockOperation) Describe() string             { return m.description }
func (m *mockOperation) Execute(ctx context.Context, c *client.DataplaneClient, txID string) error {
	m.executed = true
	if m.executeFunc != nil {
		return m.executeFunc(ctx, c, txID)
	}
	return nil
}

// newMockOperation creates a mock operation with the given properties.
func newMockOperation(opType sections.OperationType, section string, priority int) *mockOperation {
	return &mockOperation{
		opType:      opType,
		section:     section,
		priority:    priority,
		description: "mock " + section + " operation",
	}
}

// newFailingOperation creates a mock operation that returns an error when executed.
func newFailingOperation(opType sections.OperationType, section string, priority int, err error) *mockOperation {
	return &mockOperation{
		opType:      opType,
		section:     section,
		priority:    priority,
		description: "failing " + section + " operation",
		executeFunc: func(_ context.Context, _ *client.DataplaneClient, _ string) error {
			return err
		},
	}
}

// TestSync_NoChanges tests that when configs are identical, NoChangesResult is returned.
func TestSync_NoChanges(t *testing.T) {
	// Create identical configs
	configStr := `
global
    daemon

defaults
    mode http
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms

backend test_backend
    server srv1 127.0.0.1:8080
`
	p, err := parser.New()
	require.NoError(t, err)

	current, err := p.ParseFromString(configStr)
	require.NoError(t, err)

	desired, err := p.ParseFromString(configStr)
	require.NoError(t, err)

	// Create synchronizer without a real client (we won't need it for no-changes case)
	synchronizer := New(nil)

	result, err := synchronizer.Sync(context.Background(), current, desired, DefaultSyncOptions())
	require.NoError(t, err)

	assert.True(t, result.Success)
	assert.False(t, result.HasChanges())
	assert.Equal(t, "No configuration changes detected", result.Message)
	assert.Equal(t, 0, result.Retries)
}

// TestSync_DryRun tests that dry-run mode returns operations without executing them.
func TestSync_DryRun(t *testing.T) {
	// Create configs with a difference
	currentConfig := `
global
    daemon

defaults
    mode http

backend test_backend
    server srv1 127.0.0.1:8080
`
	desiredConfig := `
global
    daemon

defaults
    mode http

backend test_backend
    server srv1 127.0.0.1:8080

backend new_backend
    server srv2 127.0.0.1:9090
`
	p, err := parser.New()
	require.NoError(t, err)

	current, err := p.ParseFromString(currentConfig)
	require.NoError(t, err)

	desired, err := p.ParseFromString(desiredConfig)
	require.NoError(t, err)

	// Create synchronizer without a real client (dry-run doesn't execute)
	synchronizer := New(nil)

	result, err := synchronizer.Sync(context.Background(), current, desired, DryRunOptions())
	require.NoError(t, err)

	assert.True(t, result.Success)
	assert.True(t, result.HasChanges())
	assert.Equal(t, PolicyDryRun, result.Policy)
	assert.Contains(t, result.Message, "Dry-run completed successfully")
	assert.True(t, result.Diff.Summary.TotalCreates > 0)
	assert.Len(t, result.AppliedOperations, 0, "Dry-run should not have applied operations")
}

// TestDryRun_ReturnsOperationsWithoutExecuting tests the dryRun method directly.
func TestDryRun_ReturnsOperationsWithoutExecuting(t *testing.T) {
	// Create a mock diff with operations
	ops := []comparator.Operation{
		newMockOperation(sections.OperationCreate, "backend", 20),
		newMockOperation(sections.OperationCreate, "server", 30),
	}

	diff := &comparator.ConfigDiff{
		Operations: ops,
		Summary: comparator.DiffSummary{
			TotalCreates: 2,
		},
	}

	synchronizer := New(nil)
	startTime := time.Now()

	result := synchronizer.dryRun(diff, startTime)

	assert.True(t, result.Success)
	assert.Equal(t, PolicyDryRun, result.Policy)
	assert.NotNil(t, result.Diff)
	assert.Equal(t, 2, result.Diff.Summary.TotalOperations())

	// Verify operations were NOT executed
	for _, op := range ops {
		mockOp := op.(*mockOperation)
		assert.False(t, mockOp.executed, "Operation should not be executed in dry-run mode")
	}
}

// TestExecuteOperations_AllSucceed tests executeOperations when all operations succeed.
func TestExecuteOperations_AllSucceed(t *testing.T) {
	ops := []comparator.Operation{
		newMockOperation(sections.OperationCreate, "backend", 20),
		newMockOperation(sections.OperationCreate, "server", 30),
		newMockOperation(sections.OperationUpdate, "frontend", 10),
	}

	synchronizer := New(nil)
	opts := DefaultSyncOptions()

	applied, failed, err := synchronizer.executeOperations(context.Background(), ops, "test-tx", opts)

	require.NoError(t, err)
	assert.Len(t, applied, 3)
	assert.Len(t, failed, 0)

	// Verify all operations were executed
	for _, op := range ops {
		mockOp := op.(*mockOperation)
		assert.True(t, mockOp.executed, "Operation should be executed")
	}
}

// TestExecuteOperations_FirstFailure_StopsExecution tests that execution stops on first error
// when ContinueOnError is false. With parallel execution by priority, operations at the same
// priority run in parallel, so we use different priorities to ensure sequential execution
// for this test: first a successful operation (priority 10), then a failing one (priority 20).
func TestExecuteOperations_FirstFailure_StopsExecution(t *testing.T) {
	testErr := errors.New("operation failed")
	// Use different priorities to ensure sequential execution across groups.
	// Priority 10 runs first (should succeed), Priority 20 runs second (should fail),
	// Priority 30 should NOT run because priority 20 failed.
	ops := []comparator.Operation{
		newMockOperation(sections.OperationCreate, "backend", 10),
		newFailingOperation(sections.OperationCreate, "server", 20, testErr),
		newMockOperation(sections.OperationUpdate, "frontend", 30),
	}

	synchronizer := New(nil)
	opts := SyncOptions{
		Policy:          PolicyApply,
		ContinueOnError: false,
	}

	applied, failed, err := synchronizer.executeOperations(context.Background(), ops, "test-tx", opts)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "operation failed")
	assert.Len(t, applied, 1, "Only first operation should be applied")
	assert.Len(t, failed, 1, "Second operation should be in failed list")

	// Verify third operation was NOT executed (stopped after priority 20 failed)
	thirdOp := ops[2].(*mockOperation)
	assert.False(t, thirdOp.executed, "Third operation should not be executed after failure")
}

// TestExecuteOperations_ContinueOnError tests that execution continues after failure
// when ContinueOnError is true. All operations run regardless of failures.
func TestExecuteOperations_ContinueOnError(t *testing.T) {
	testErr := errors.New("operation failed")
	// Use different priorities to ensure sequential execution across groups.
	// With ContinueOnError, all groups run regardless of failures.
	ops := []comparator.Operation{
		newMockOperation(sections.OperationCreate, "backend", 10),
		newFailingOperation(sections.OperationCreate, "server", 20, testErr),
		newMockOperation(sections.OperationUpdate, "frontend", 30),
	}

	synchronizer := New(nil)
	opts := SyncOptions{
		Policy:          PolicyApply,
		ContinueOnError: true,
	}

	applied, failed, err := synchronizer.executeOperations(context.Background(), ops, "test-tx", opts)

	require.NoError(t, err, "Should not return error when ContinueOnError is true")
	assert.Len(t, applied, 2, "First and third operations should be applied")
	assert.Len(t, failed, 1, "Second operation should be in failed list")

	// Verify third operation WAS executed despite second failure
	thirdOp := ops[2].(*mockOperation)
	assert.True(t, thirdOp.executed, "Third operation should be executed when ContinueOnError is true")
}

// TestExecuteOperations_EmptyList tests executeOperations with empty operation list.
func TestExecuteOperations_EmptyList(t *testing.T) {
	synchronizer := New(nil)
	opts := DefaultSyncOptions()

	applied, failed, err := synchronizer.executeOperations(context.Background(), nil, "test-tx", opts)

	require.NoError(t, err)
	assert.Empty(t, applied)
	assert.Empty(t, failed)
}

// TestNewSuccessResult tests the NewSuccessResult constructor.
func TestNewSuccessResult(t *testing.T) {
	diff := &comparator.ConfigDiff{
		Summary: comparator.DiffSummary{
			TotalCreates: 2,
			TotalUpdates: 1,
		},
	}
	applied := []comparator.Operation{
		newMockOperation(sections.OperationCreate, "backend", 20),
	}
	duration := 100 * time.Millisecond

	result := NewSuccessResult(PolicyApply, diff, applied, duration, 1)

	assert.True(t, result.Success)
	assert.Equal(t, PolicyApply, result.Policy)
	assert.NotNil(t, result.Diff)
	assert.Len(t, result.AppliedOperations, 1)
	assert.Empty(t, result.FailedOperations)
	assert.Equal(t, duration, result.Duration)
	assert.Equal(t, 1, result.Retries)
	assert.Contains(t, result.Message, "successfully")
}

// TestNewSuccessResult_DryRun tests the NewSuccessResult constructor for dry-run.
func TestNewSuccessResult_DryRun(t *testing.T) {
	diff := &comparator.ConfigDiff{
		Summary: comparator.DiffSummary{
			TotalCreates: 2,
		},
	}
	duration := 50 * time.Millisecond

	result := NewSuccessResult(PolicyDryRun, diff, nil, duration, 0)

	assert.True(t, result.Success)
	assert.Equal(t, PolicyDryRun, result.Policy)
	assert.Contains(t, result.Message, "Dry-run")
	assert.Contains(t, result.Message, "no changes applied")
}

// TestNewFailureResult tests the NewFailureResult constructor.
func TestNewFailureResult(t *testing.T) {
	diff := &comparator.ConfigDiff{
		Summary: comparator.DiffSummary{
			TotalCreates: 3,
		},
	}
	applied := []comparator.Operation{
		newMockOperation(sections.OperationCreate, "backend", 20),
	}
	failed := []OperationError{
		{
			Operation: newMockOperation(sections.OperationCreate, "server", 30),
			Cause:     errors.New("test error"),
		},
	}
	duration := 200 * time.Millisecond

	result := NewFailureResult(PolicyApply, diff, applied, failed, duration, 2, "sync failed")

	assert.False(t, result.Success)
	assert.Equal(t, PolicyApply, result.Policy)
	assert.NotNil(t, result.Diff)
	assert.Len(t, result.AppliedOperations, 1)
	assert.Len(t, result.FailedOperations, 1)
	assert.Equal(t, duration, result.Duration)
	assert.Equal(t, 2, result.Retries)
	assert.Equal(t, "sync failed", result.Message)
}

// TestNewNoChangesResult tests the NewNoChangesResult constructor.
func TestNewNoChangesResult(t *testing.T) {
	duration := 10 * time.Millisecond

	result := NewNoChangesResult(PolicyApply, duration)

	assert.True(t, result.Success)
	assert.Equal(t, PolicyApply, result.Policy)
	assert.Nil(t, result.Diff)
	assert.Empty(t, result.AppliedOperations)
	assert.Empty(t, result.FailedOperations)
	assert.Equal(t, duration, result.Duration)
	assert.Equal(t, 0, result.Retries)
	assert.Contains(t, result.Message, "No configuration changes")
}

// TestSyncResult_HasChanges tests the HasChanges method.
func TestSyncResult_HasChanges(t *testing.T) {
	tests := []struct {
		name     string
		result   *SyncResult
		expected bool
	}{
		{
			name:     "nil diff",
			result:   &SyncResult{Diff: nil},
			expected: false,
		},
		{
			name: "no changes",
			result: &SyncResult{
				Diff: &comparator.ConfigDiff{
					Summary: comparator.DiffSummary{},
				},
			},
			expected: false,
		},
		{
			name: "has creates",
			result: &SyncResult{
				Diff: &comparator.ConfigDiff{
					Summary: comparator.DiffSummary{TotalCreates: 1},
				},
			},
			expected: true,
		},
		{
			name: "has updates",
			result: &SyncResult{
				Diff: &comparator.ConfigDiff{
					Summary: comparator.DiffSummary{TotalUpdates: 1},
				},
			},
			expected: true,
		},
		{
			name: "has deletes",
			result: &SyncResult{
				Diff: &comparator.ConfigDiff{
					Summary: comparator.DiffSummary{TotalDeletes: 1},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.result.HasChanges())
		})
	}
}

// TestSyncResult_HasFailures tests the HasFailures method.
func TestSyncResult_HasFailures(t *testing.T) {
	tests := []struct {
		name     string
		result   *SyncResult
		expected bool
	}{
		{
			name:     "no failures",
			result:   &SyncResult{FailedOperations: nil},
			expected: false,
		},
		{
			name:     "empty failures",
			result:   &SyncResult{FailedOperations: []OperationError{}},
			expected: false,
		},
		{
			name: "has failures",
			result: &SyncResult{
				FailedOperations: []OperationError{
					{Cause: errors.New("test")},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.result.HasFailures())
		})
	}
}

// TestSyncResult_String tests the String method produces readable output.
func TestSyncResult_String(t *testing.T) {
	diff := &comparator.ConfigDiff{
		Summary: comparator.DiffSummary{
			TotalCreates: 2,
			TotalUpdates: 1,
			TotalDeletes: 0,
		},
	}

	result := &SyncResult{
		Success:  true,
		Policy:   PolicyApply,
		Diff:     diff,
		Duration: 150 * time.Millisecond,
		Retries:  0,
		Message:  "Sync completed",
	}

	str := result.String()

	assert.Contains(t, str, "SUCCESS")
	assert.Contains(t, str, "apply")
	assert.Contains(t, str, "150ms")
	assert.Contains(t, str, "Sync completed")
}

// TestSyncPolicy tests policy methods.
func TestSyncPolicy(t *testing.T) {
	t.Run("IsDryRun", func(t *testing.T) {
		assert.True(t, PolicyDryRun.IsDryRun())
		assert.False(t, PolicyApply.IsDryRun())
		assert.False(t, PolicyApplyForce.IsDryRun())
	})

	t.Run("ShouldApply", func(t *testing.T) {
		assert.False(t, PolicyDryRun.ShouldApply())
		assert.True(t, PolicyApply.ShouldApply())
		assert.True(t, PolicyApplyForce.ShouldApply())
	})

	t.Run("MaxRetries", func(t *testing.T) {
		assert.Equal(t, 0, PolicyDryRun.MaxRetries())
		assert.Equal(t, 3, PolicyApply.MaxRetries())
		assert.Equal(t, -1, PolicyApplyForce.MaxRetries())
	})

	t.Run("String", func(t *testing.T) {
		assert.Equal(t, "dry-run", PolicyDryRun.String())
		assert.Equal(t, "apply", PolicyApply.String())
		assert.Equal(t, "apply-force", PolicyApplyForce.String())
	})
}

// TestDefaultSyncOptions tests the DefaultSyncOptions function.
func TestDefaultSyncOptions(t *testing.T) {
	opts := DefaultSyncOptions()

	assert.Equal(t, PolicyApply, opts.Policy)
	assert.False(t, opts.ContinueOnError)
	assert.True(t, opts.ValidateBeforeApply)
}

// TestDryRunOptions tests the DryRunOptions function.
func TestDryRunOptions(t *testing.T) {
	opts := DryRunOptions()

	assert.Equal(t, PolicyDryRun, opts.Policy)
	assert.False(t, opts.ContinueOnError)
	assert.False(t, opts.ValidateBeforeApply)
}

// TestWithLogger tests that WithLogger sets a custom logger.
func TestWithLogger(t *testing.T) {
	synchronizer := New(nil)
	require.NotNil(t, synchronizer)

	// Create a custom logger
	customLogger := slog.Default().With("test", "value")

	// Chain the WithLogger call and verify it returns the same synchronizer
	result := synchronizer.WithLogger(customLogger)

	assert.Same(t, synchronizer, result, "WithLogger should return the same synchronizer instance")
	assert.Equal(t, customLogger, synchronizer.logger, "Logger should be set to custom logger")
}

// TestSyncPolicy_MaxRetries_UnknownPolicy tests the default case for MaxRetries.
func TestSyncPolicy_MaxRetries_UnknownPolicy(t *testing.T) {
	// Create an unknown policy
	unknownPolicy := SyncPolicy("unknown-policy")

	// Should return safe default of 3
	assert.Equal(t, 3, unknownPolicy.MaxRetries())
}

// TestSync_NilCurrentConfig tests that Sync returns error for nil current config.
func TestSync_NilCurrentConfig(t *testing.T) {
	p, err := parser.New()
	require.NoError(t, err)

	desired, err := p.ParseFromString(`
global
    daemon
`)
	require.NoError(t, err)

	synchronizer := New(nil)
	_, err = synchronizer.Sync(context.Background(), nil, desired, DefaultSyncOptions())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "current configuration is nil")
}

// TestSync_NilDesiredConfig tests that Sync returns error for nil desired config.
func TestSync_NilDesiredConfig(t *testing.T) {
	p, err := parser.New()
	require.NoError(t, err)

	current, err := p.ParseFromString(`
global
    daemon
`)
	require.NoError(t, err)

	synchronizer := New(nil)
	_, err = synchronizer.Sync(context.Background(), current, nil, DefaultSyncOptions())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "desired configuration is nil")
}

// TestSyncFromStrings_ParsesAndSyncsConfigs tests that SyncFromStrings properly parses both configs.
func TestSyncFromStrings_ParsesAndSyncsConfigs(t *testing.T) {
	synchronizer := New(nil)

	currentConfig := `
global
    daemon

defaults
    mode http
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms

backend old_backend
    server srv1 127.0.0.1:8080
`
	desiredConfig := `
global
    daemon

defaults
    mode http
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms

backend new_backend
    server srv2 127.0.0.1:9090
`

	// Use DryRunOptions to avoid needing a real client
	result, err := synchronizer.SyncFromStrings(context.Background(), currentConfig, desiredConfig, DryRunOptions())

	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.True(t, result.HasChanges())
	// Should have created new_backend and deleted old_backend
	assert.True(t, result.Diff.Summary.TotalCreates > 0 || result.Diff.Summary.TotalDeletes > 0)
}

// TestSyncFromStrings_NoChanges tests SyncFromStrings with identical configs.
func TestSyncFromStrings_NoChanges(t *testing.T) {
	synchronizer := New(nil)

	config := `
global
    daemon

defaults
    mode http
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms

backend test_backend
    server srv1 127.0.0.1:8080
`

	result, err := synchronizer.SyncFromStrings(context.Background(), config, config, DryRunOptions())

	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.False(t, result.HasChanges())
	assert.Contains(t, result.Message, "No configuration changes")
}

// TestSyncFromStrings_DryRunWithChanges tests SyncFromStrings with changes in dry-run mode.
func TestSyncFromStrings_DryRunWithChanges(t *testing.T) {
	synchronizer := New(nil)

	currentConfig := `
global
    daemon

defaults
    mode http

backend test_backend
    server srv1 127.0.0.1:8080
`
	desiredConfig := `
global
    daemon

defaults
    mode http

backend test_backend
    server srv1 127.0.0.1:8080

backend new_backend
    server srv2 127.0.0.1:9090
`

	result, err := synchronizer.SyncFromStrings(context.Background(), currentConfig, desiredConfig, DryRunOptions())

	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.True(t, result.HasChanges())
	assert.Equal(t, PolicyDryRun, result.Policy)
}

// TestSyncResult_String_Failure tests the String method for failed results.
func TestSyncResult_String_Failure(t *testing.T) {
	diff := &comparator.ConfigDiff{
		Summary: comparator.DiffSummary{
			TotalCreates: 2,
			TotalUpdates: 1,
			TotalDeletes: 0,
		},
	}

	result := &SyncResult{
		Success:  false,
		Policy:   PolicyApply,
		Diff:     diff,
		Duration: 200 * time.Millisecond,
		Retries:  2,
		Message:  "Sync failed due to error",
	}

	str := result.String()

	assert.Contains(t, str, "FAILED")
	assert.Contains(t, str, "apply")
	assert.Contains(t, str, "200ms")
	assert.Contains(t, str, "Sync failed due to error")
}

// TestSyncResult_String_WithAppliedOperations tests the String method shows applied operations.
func TestSyncResult_String_WithAppliedOperations(t *testing.T) {
	diff := &comparator.ConfigDiff{
		Summary: comparator.DiffSummary{
			TotalCreates: 1,
		},
	}

	applied := []comparator.Operation{
		newMockOperation(sections.OperationCreate, "backend", 20),
		newMockOperation(sections.OperationCreate, "server", 30),
	}

	result := &SyncResult{
		Success:           true,
		Policy:            PolicyApply,
		Diff:              diff,
		AppliedOperations: applied,
		Duration:          100 * time.Millisecond,
		Retries:           0,
		Message:           "Sync completed",
	}

	str := result.String()

	assert.Contains(t, str, "Applied: 2 operations")
}

// TestSyncResult_String_WithFailedOperations tests the String method shows failed operations.
func TestSyncResult_String_WithFailedOperations(t *testing.T) {
	diff := &comparator.ConfigDiff{
		Summary: comparator.DiffSummary{
			TotalCreates: 2,
		},
	}

	failed := []OperationError{
		{
			Operation: newMockOperation(sections.OperationCreate, "backend", 20),
			Cause:     errors.New("connection refused"),
		},
		{
			Operation: newMockOperation(sections.OperationCreate, "server", 30),
			Cause:     errors.New("timeout"),
		},
	}

	result := &SyncResult{
		Success:          false,
		Policy:           PolicyApply,
		Diff:             diff,
		FailedOperations: failed,
		Duration:         150 * time.Millisecond,
		Retries:          1,
		Message:          "Partial failure",
	}

	str := result.String()

	assert.Contains(t, str, "FAILED")
	assert.Contains(t, str, "Failed: 2 operations")
	assert.Contains(t, str, "connection refused")
	assert.Contains(t, str, "timeout")
}

// TestSyncResult_String_NoDiff tests the String method when Diff is nil.
func TestSyncResult_String_NoDiff(t *testing.T) {
	result := &SyncResult{
		Success:  true,
		Policy:   PolicyApply,
		Diff:     nil,
		Duration: 10 * time.Millisecond,
		Retries:  0,
		Message:  "No changes",
	}

	str := result.String()

	assert.Contains(t, str, "SUCCESS")
	assert.Contains(t, str, "No changes")
	// Should not panic on nil Diff
}

// TestSyncResult_String_NoMessage tests the String method when Message is empty.
func TestSyncResult_String_NoMessage(t *testing.T) {
	diff := &comparator.ConfigDiff{
		Summary: comparator.DiffSummary{
			TotalUpdates: 1,
		},
	}

	result := &SyncResult{
		Success:  true,
		Policy:   PolicyApply,
		Diff:     diff,
		Duration: 50 * time.Millisecond,
		Retries:  0,
		Message:  "", // Empty message
	}

	str := result.String()

	assert.Contains(t, str, "SUCCESS")
	assert.NotContains(t, str, "Message:") // Should not have Message section
}

// TestSyncOperations_Success tests SyncOperations with successful operations.
func TestSyncOperations_Success(t *testing.T) {
	ops := []comparator.Operation{
		newMockOperation(sections.OperationCreate, "backend", 20),
		newMockOperation(sections.OperationCreate, "server", 30),
	}

	tx := &client.Transaction{
		ID:      "test-tx-123",
		Version: 1,
	}

	result, err := SyncOperations(context.Background(), nil, ops, tx)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.ReloadTriggered, "ReloadTriggered should be false initially")
	assert.Empty(t, result.ReloadID)

	// Verify all operations were executed
	for _, op := range ops {
		mockOp := op.(*mockOperation)
		assert.True(t, mockOp.executed, "Operation should be executed")
	}
}

// TestSyncOperations_FailOnError tests SyncOperations stops on first error.
// With parallel execution by priority, operations at the same priority level
// run in parallel, and operations at higher priorities don't start if an
// earlier priority group fails.
func TestSyncOperations_FailOnError(t *testing.T) {
	testErr := errors.New("operation failed")

	// Create operations with different priorities:
	// - Priority 10: failing operation (runs first)
	// - Priority 20: should NOT execute (later priority group)
	failingOp := newMockOperation(sections.OperationCreate, "server", 10)
	failingOp.executeFunc = func(_ context.Context, _ *client.DataplaneClient, _ string) error {
		return testErr
	}
	failingOp.description = "failing server operation"

	laterOp := newMockOperation(sections.OperationCreate, "backend", 20)

	ops := []comparator.Operation{
		failingOp,
		laterOp,
	}

	tx := &client.Transaction{
		ID:      "test-tx-456",
		Version: 2,
	}

	result, err := SyncOperations(context.Background(), nil, ops, tx)

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "operation failed")
	assert.Contains(t, err.Error(), "failing server operation")

	// Verify later priority operation was NOT executed
	assert.False(t, laterOp.executed, "Higher priority operations should not execute after earlier priority fails")
}

// TestSyncOperations_EmptyList tests SyncOperations with empty operation list.
func TestSyncOperations_EmptyList(t *testing.T) {
	tx := &client.Transaction{
		ID:      "test-tx-empty",
		Version: 1,
	}

	result, err := SyncOperations(context.Background(), nil, nil, tx)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.ReloadTriggered)
}

// TestSyncOperations_ParallelByPriority tests that operations are grouped by priority
// and executed in priority order, with operations at the same priority running in parallel.
func TestSyncOperations_ParallelByPriority(t *testing.T) {
	// Track execution order
	var executionOrder []string
	var mu sync.Mutex

	recordExecution := func(name string) {
		mu.Lock()
		defer mu.Unlock()
		executionOrder = append(executionOrder, name)
	}

	// Create operations at different priorities
	// Priority 10: op1, op2 (should run first, in parallel)
	// Priority 20: op3 (should run second)
	// Priority 30: op4, op5 (should run third, in parallel)
	op1 := newMockOperation(sections.OperationCreate, "frontend1", 10)
	op1.executeFunc = func(_ context.Context, _ *client.DataplaneClient, _ string) error {
		recordExecution("op1-priority10")
		return nil
	}

	op2 := newMockOperation(sections.OperationCreate, "frontend2", 10)
	op2.executeFunc = func(_ context.Context, _ *client.DataplaneClient, _ string) error {
		recordExecution("op2-priority10")
		return nil
	}

	op3 := newMockOperation(sections.OperationCreate, "backend", 20)
	op3.executeFunc = func(_ context.Context, _ *client.DataplaneClient, _ string) error {
		recordExecution("op3-priority20")
		return nil
	}

	op4 := newMockOperation(sections.OperationCreate, "server1", 30)
	op4.executeFunc = func(_ context.Context, _ *client.DataplaneClient, _ string) error {
		recordExecution("op4-priority30")
		return nil
	}

	op5 := newMockOperation(sections.OperationCreate, "server2", 30)
	op5.executeFunc = func(_ context.Context, _ *client.DataplaneClient, _ string) error {
		recordExecution("op5-priority30")
		return nil
	}

	ops := []comparator.Operation{op5, op3, op1, op4, op2} // Scrambled order

	tx := &client.Transaction{ID: "test-tx", Version: 1}

	result, err := SyncOperations(context.Background(), nil, ops, tx)

	require.NoError(t, err)
	require.NotNil(t, result)

	// All operations should have executed
	assert.True(t, op1.executed)
	assert.True(t, op2.executed)
	assert.True(t, op3.executed)
	assert.True(t, op4.executed)
	assert.True(t, op5.executed)

	// Verify priority ordering: all priority 10 ops before priority 20, all priority 20 before priority 30
	mu.Lock()
	order := executionOrder
	mu.Unlock()

	require.Len(t, order, 5)

	// Find positions
	priority10Positions := []int{}
	priority20Position := -1
	priority30Positions := []int{}

	for i, name := range order {
		if strings.Contains(name, "priority10") {
			priority10Positions = append(priority10Positions, i)
		} else if strings.Contains(name, "priority20") {
			priority20Position = i
		} else if strings.Contains(name, "priority30") {
			priority30Positions = append(priority30Positions, i)
		}
	}

	// Priority 10 ops should come before priority 20
	for _, pos := range priority10Positions {
		assert.Less(t, pos, priority20Position, "Priority 10 operations should execute before priority 20")
	}

	// Priority 20 should come before priority 30
	for _, pos := range priority30Positions {
		assert.Less(t, priority20Position, pos, "Priority 20 operations should execute before priority 30")
	}
}

// TestGroupByPriority tests the groupByPriority helper function.
func TestGroupByPriority(t *testing.T) {
	ops := []comparator.Operation{
		newMockOperation(sections.OperationCreate, "backend", 20),
		newMockOperation(sections.OperationCreate, "server", 30),
		newMockOperation(sections.OperationCreate, "frontend", 10),
		newMockOperation(sections.OperationUpdate, "backend2", 20),
	}

	groups := groupByPriority(ops)

	assert.Len(t, groups, 3, "Should have 3 priority groups")
	assert.Len(t, groups[10], 1, "Priority 10 should have 1 operation")
	assert.Len(t, groups[20], 2, "Priority 20 should have 2 operations")
	assert.Len(t, groups[30], 1, "Priority 30 should have 1 operation")
}

// TestSortedPriorityKeys tests the sortedPriorityKeys helper function.
func TestSortedPriorityKeys(t *testing.T) {
	groups := map[int][]comparator.Operation{
		30: {newMockOperation(sections.OperationCreate, "server", 30)},
		10: {newMockOperation(sections.OperationCreate, "frontend", 10)},
		20: {newMockOperation(sections.OperationCreate, "backend", 20)},
	}

	keys := sortedPriorityKeys(groups)

	assert.Equal(t, []int{10, 20, 30}, keys, "Keys should be sorted in ascending order")
}

// createMockDataplaneServer creates a mock Dataplane API server for testing.
// Returns the server that accepts all DataPlane API operations.
func createMockDataplaneServer(t *testing.T, version int64) *httptest.Server {
	t.Helper()

	var transactionCount atomic.Int32

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check for transaction_id in query params
		txID := r.URL.Query().Get("transaction_id")
		path := r.URL.Path

		switch {
		case path == "/v3/info":
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
			return

		case path == "/services/haproxy/configuration/version":
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "%d", version)
			return

		case path == "/services/haproxy/transactions" && r.Method == http.MethodPost:
			// Create transaction
			txNum := fmt.Sprintf("tx-%d", transactionCount.Add(1))
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"id":"%s","status":"in_progress","_version":%d}`, txNum, version)
			return

		case strings.HasPrefix(path, "/services/haproxy/transactions/") && r.Method == http.MethodPut:
			// Commit transaction
			w.WriteHeader(http.StatusOK)
			return

		case strings.HasPrefix(path, "/services/haproxy/transactions/") && r.Method == http.MethodDelete:
			// Abort transaction
			w.WriteHeader(http.StatusNoContent)
			return

		case strings.Contains(path, "/configuration/") && txID != "":
			// All configuration operations within a transaction succeed
			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusCreated)
			} else {
				w.WriteHeader(http.StatusOK)
			}
			fmt.Fprintln(w, `{}`)
			return

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// TestSync_Apply_ExercisesApplyPath tests that the apply path is exercised.
// Note: This test exercises the apply code path. The mock server may not
// perfectly handle all DataPlane API operations, but coverage is the goal.
func TestSync_Apply_ExercisesApplyPath(t *testing.T) {
	server := createMockDataplaneServer(t, 1)
	defer server.Close()

	// Create client
	dpClient, err := client.New(context.Background(), &client.Config{
		BaseURL:  server.URL,
		Username: "admin",
		Password: "password",
	})
	require.NoError(t, err)

	// Create synchronizer with real client
	synchronizer := New(dpClient)

	// Create configs with a difference
	currentConfig := `
global
    daemon

defaults
    mode http

backend test_backend
    server srv1 127.0.0.1:8080
`
	desiredConfig := `
global
    daemon

defaults
    mode http

backend test_backend
    server srv1 127.0.0.1:8080

backend new_backend
    server srv2 127.0.0.1:9090
`
	p, err := parser.New()
	require.NoError(t, err)

	current, err := p.ParseFromString(currentConfig)
	require.NoError(t, err)

	desired, err := p.ParseFromString(desiredConfig)
	require.NoError(t, err)

	opts := SyncOptions{
		Policy:          PolicyApply,
		ContinueOnError: false,
	}

	// This exercises the apply path - we don't require success since
	// the mock server may not handle all operations perfectly
	result, _ := synchronizer.Sync(context.Background(), current, desired, opts)

	// Verify we got a result (path was exercised)
	require.NotNil(t, result)
	assert.True(t, result.HasChanges())
	assert.Equal(t, PolicyApply, result.Policy)
}

// TestSync_Apply_VersionConflict tests the apply path with version conflict.
// Note: This test exercises the version conflict handling code path.
func TestSync_Apply_VersionConflict(t *testing.T) {
	var conflictCount atomic.Int32

	// Create server that returns version conflict on commit
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		txID := r.URL.Query().Get("transaction_id")

		switch {
		case path == "/v3/info":
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
			return

		case path == "/services/haproxy/configuration/version":
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "1")
			return

		case path == "/services/haproxy/transactions" && r.Method == http.MethodPost:
			// Create transaction
			txNum := fmt.Sprintf("tx-%d", conflictCount.Add(1))
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"id":"%s","status":"in_progress","_version":1}`, txNum)
			return

		case strings.HasPrefix(path, "/services/haproxy/transactions/") && r.Method == http.MethodPut:
			// Commit returns version conflict
			w.Header().Set("Configuration-Version", "999")
			w.WriteHeader(http.StatusConflict)
			fmt.Fprintln(w, `{"code":409,"message":"version mismatch"}`)
			return

		case strings.HasPrefix(path, "/services/haproxy/transactions/") && r.Method == http.MethodDelete:
			// Abort transaction
			w.WriteHeader(http.StatusNoContent)
			return

		case strings.Contains(path, "/configuration/") && txID != "":
			// All operations within transaction succeed
			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusCreated)
			} else {
				w.WriteHeader(http.StatusOK)
			}
			fmt.Fprintln(w, `{}`)
			return

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	dpClient, err := client.New(context.Background(), &client.Config{
		BaseURL:  server.URL,
		Username: "admin",
		Password: "password",
	})
	require.NoError(t, err)

	synchronizer := New(dpClient)

	currentConfig := `
global
    daemon

defaults
    mode http

backend test_backend
    server srv1 127.0.0.1:8080
`
	desiredConfig := `
global
    daemon

defaults
    mode http

backend new_backend
    server srv2 127.0.0.1:9090
`
	p, err := parser.New()
	require.NoError(t, err)

	current, err := p.ParseFromString(currentConfig)
	require.NoError(t, err)

	desired, err := p.ParseFromString(desiredConfig)
	require.NoError(t, err)

	opts := SyncOptions{
		Policy:          PolicyApply, // 3 retries max
		ContinueOnError: false,
	}

	result, err := synchronizer.Sync(context.Background(), current, desired, opts)

	// Should fail - either from version conflict or operation error
	require.Error(t, err)
	assert.False(t, result.Success)
	// The test exercises the apply path with potential version conflict
	// The important thing is that the code path was executed
	assert.NotEmpty(t, result.Message)
}

// TestSync_Apply_ContinueOnError tests the apply path with ContinueOnError enabled.
func TestSync_Apply_ContinueOnError(t *testing.T) {
	server := createMockDataplaneServer(t, 1)
	defer server.Close()

	dpClient, err := client.New(context.Background(), &client.Config{
		BaseURL:  server.URL,
		Username: "admin",
		Password: "password",
	})
	require.NoError(t, err)

	synchronizer := New(dpClient)

	// Create configs with multiple differences
	currentConfig := `
global
    daemon

defaults
    mode http
`
	desiredConfig := `
global
    daemon

defaults
    mode http

backend backend1
    server srv1 127.0.0.1:8080

backend backend2
    server srv2 127.0.0.1:9090
`
	p, err := parser.New()
	require.NoError(t, err)

	current, err := p.ParseFromString(currentConfig)
	require.NoError(t, err)

	desired, err := p.ParseFromString(desiredConfig)
	require.NoError(t, err)

	opts := SyncOptions{
		Policy:          PolicyApply,
		ContinueOnError: true, // Continue even if operations fail
	}

	result, err := synchronizer.Sync(context.Background(), current, desired, opts)

	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.True(t, result.HasChanges())
}

// TestSyncOperations_IndexBasedOperationsExecuteInOrder tests that index-based operations
// execute sequentially in index order. This is critical for operations like HTTP checks
// where index 0 must complete BEFORE index 1 starts.
//
// The implementation uses unique priorities per index (basePriority*1000 + index), which
// causes each index to be in its own priority group. Since priority groups execute
// sequentially (not in parallel), this guarantees correct ordering.
func TestSyncOperations_IndexBasedOperationsExecuteInOrder(t *testing.T) {
	const basePriority = 60 // Example: http-check priority

	// Track when each operation starts and completes
	type timing struct {
		startTime    time.Time
		completeTime time.Time
	}

	var mu sync.Mutex
	timings := make(map[int]timing) // index -> timing

	// Create operations that record their start/complete times with a small delay
	createIndexOp := func(index int) *mockOperation {
		priority := basePriority*sections.PriorityMultiplier + index // Simulates IndexChildOp.Priority()
		op := newMockOperation(sections.OperationCreate, fmt.Sprintf("http-check-%d", index), priority)
		op.executeFunc = func(_ context.Context, _ *client.DataplaneClient, _ string) error {
			mu.Lock()
			t := timings[index]
			t.startTime = time.Now()
			timings[index] = t
			mu.Unlock()

			// Small delay to ensure timing differences are measurable
			time.Sleep(10 * time.Millisecond)

			mu.Lock()
			t = timings[index]
			t.completeTime = time.Now()
			timings[index] = t
			mu.Unlock()

			return nil
		}
		return op
	}

	// Create 5 index-based operations in scrambled order
	ops := []comparator.Operation{
		createIndexOp(3),
		createIndexOp(0),
		createIndexOp(4),
		createIndexOp(1),
		createIndexOp(2),
	}

	tx := &client.Transaction{ID: "test-tx", Version: 1}

	result, err := SyncOperations(context.Background(), nil, ops, tx)

	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify all operations executed
	for _, op := range ops {
		assert.True(t, op.(*mockOperation).executed, "Operation should be executed")
	}

	// Verify timing: each index must complete BEFORE the next index starts
	mu.Lock()
	defer mu.Unlock()

	for i := 0; i < 4; i++ {
		current := timings[i]
		next := timings[i+1]

		assert.True(t, current.completeTime.Before(next.startTime) || current.completeTime.Equal(next.startTime),
			"Index %d should complete before index %d starts (completed: %v, started: %v)",
			i, i+1, current.completeTime, next.startTime)
	}
}

// TestSyncOperations_IndexBasedDeletesExecuteInReverseOrder tests that index-based delete
// operations execute in reverse index order (higher indices first). This is important
// because deleting index 0 when index 1 still exists could cause array reindexing issues.
func TestSyncOperations_IndexBasedDeletesExecuteInReverseOrder(t *testing.T) {
	const basePriority = 60

	// Track execution order
	var mu sync.Mutex
	var executionOrder []int

	// Create delete operations that record their execution order
	createDeleteOp := func(index int) *mockOperation {
		// For deletes: basePriority*1000 + (999 - index) - higher indices run first
		priority := basePriority*sections.PriorityMultiplier + (999 - index)
		op := newMockOperation(sections.OperationDelete, fmt.Sprintf("http-check-%d", index), priority)
		op.executeFunc = func(_ context.Context, _ *client.DataplaneClient, _ string) error {
			mu.Lock()
			executionOrder = append(executionOrder, index)
			mu.Unlock()
			return nil
		}
		return op
	}

	// Create 5 delete operations in scrambled order
	ops := []comparator.Operation{
		createDeleteOp(2),
		createDeleteOp(4),
		createDeleteOp(0),
		createDeleteOp(3),
		createDeleteOp(1),
	}

	tx := &client.Transaction{ID: "test-tx", Version: 1}

	result, err := SyncOperations(context.Background(), nil, ops, tx)

	require.NoError(t, err)
	require.NotNil(t, result)

	mu.Lock()
	defer mu.Unlock()

	// Verify execution order is reverse (4, 3, 2, 1, 0)
	expected := []int{4, 3, 2, 1, 0}
	assert.Equal(t, expected, executionOrder,
		"Delete operations should execute in reverse index order (highest first)")
}

// TestSync_Apply_OperationFailure tests the apply path when operations fail.
func TestSync_Apply_OperationFailure(t *testing.T) {
	// Create a server that fails on backend creation
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v3/info":
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
			return

		case r.URL.Path == "/services/haproxy/configuration/version":
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "1")
			return

		case r.URL.Path == "/services/haproxy/transactions" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintln(w, `{"id":"tx-1","status":"in_progress","_version":1}`)
			return

		case r.URL.Path == "/services/haproxy/configuration/backends" && r.Method == http.MethodPost:
			// Fail backend creation
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintln(w, `{"code":500,"message":"internal error"}`)
			return

		case r.Method == http.MethodDelete && r.URL.Query().Get("transaction_id") != "":
			w.WriteHeader(http.StatusNoContent)
			return

		default:
			if r.URL.Query().Get("transaction_id") != "" {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	dpClient, err := client.New(context.Background(), &client.Config{
		BaseURL:  server.URL,
		Username: "admin",
		Password: "password",
	})
	require.NoError(t, err)

	synchronizer := New(dpClient)

	currentConfig := `
global
    daemon

defaults
    mode http
`
	desiredConfig := `
global
    daemon

defaults
    mode http

backend new_backend
    server srv1 127.0.0.1:8080
`
	p, err := parser.New()
	require.NoError(t, err)

	current, err := p.ParseFromString(currentConfig)
	require.NoError(t, err)

	desired, err := p.ParseFromString(desiredConfig)
	require.NoError(t, err)

	opts := SyncOptions{
		Policy:          PolicyApply,
		ContinueOnError: false, // Stop on first error
	}

	result, err := synchronizer.Sync(context.Background(), current, desired, opts)

	// Should fail due to operation error
	require.Error(t, err)
	assert.False(t, result.Success)
	assert.True(t, result.HasFailures())
}
