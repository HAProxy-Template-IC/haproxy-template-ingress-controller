package synchronizer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"haproxy-template-ic/pkg/dataplane/client"
	"haproxy-template-ic/pkg/dataplane/comparator"
	"haproxy-template-ic/pkg/dataplane/comparator/sections"
	"haproxy-template-ic/pkg/dataplane/parser"
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
func newFailingOperation(opType sections.OperationType, section string, err error) *mockOperation {
	return &mockOperation{
		opType:      opType,
		section:     section,
		priority:    10,
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
	sync := New(nil)

	result, err := sync.Sync(context.Background(), current, desired, DefaultSyncOptions())
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
	sync := New(nil)

	result, err := sync.Sync(context.Background(), current, desired, DryRunOptions())
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

	sync := New(nil)
	startTime := time.Now()

	result := sync.dryRun(diff, startTime)

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

	sync := New(nil)
	opts := DefaultSyncOptions()

	applied, failed, err := sync.executeOperations(context.Background(), ops, opts)

	require.NoError(t, err)
	assert.Len(t, applied, 3)
	assert.Len(t, failed, 0)

	// Verify all operations were executed
	for _, op := range ops {
		mockOp := op.(*mockOperation)
		assert.True(t, mockOp.executed, "Operation should be executed")
	}
}

// TestExecuteOperations_FirstFailure_StopsExecution tests that execution stops on first failure
// when ContinueOnError is false.
func TestExecuteOperations_FirstFailure_StopsExecution(t *testing.T) {
	testErr := errors.New("operation failed")
	ops := []comparator.Operation{
		newMockOperation(sections.OperationCreate, "backend", 20),
		newFailingOperation(sections.OperationCreate, "server", testErr),
		newMockOperation(sections.OperationUpdate, "frontend", 10),
	}

	sync := New(nil)
	opts := SyncOptions{
		Policy:          PolicyApply,
		ContinueOnError: false,
	}

	applied, failed, err := sync.executeOperations(context.Background(), ops, opts)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "operation failed")
	assert.Len(t, applied, 1, "Only first operation should be applied")
	assert.Len(t, failed, 1, "Second operation should be in failed list")

	// Verify third operation was NOT executed (stopped after first failure)
	thirdOp := ops[2].(*mockOperation)
	assert.False(t, thirdOp.executed, "Third operation should not be executed after failure")
}

// TestExecuteOperations_ContinueOnError tests that execution continues after failure
// when ContinueOnError is true.
func TestExecuteOperations_ContinueOnError(t *testing.T) {
	testErr := errors.New("operation failed")
	ops := []comparator.Operation{
		newMockOperation(sections.OperationCreate, "backend", 20),
		newFailingOperation(sections.OperationCreate, "server", testErr),
		newMockOperation(sections.OperationUpdate, "frontend", 10),
	}

	sync := New(nil)
	opts := SyncOptions{
		Policy:          PolicyApply,
		ContinueOnError: true,
	}

	applied, failed, err := sync.executeOperations(context.Background(), ops, opts)

	require.NoError(t, err, "Should not return error when ContinueOnError is true")
	assert.Len(t, applied, 2, "First and third operations should be applied")
	assert.Len(t, failed, 1, "Second operation should be in failed list")

	// Verify third operation WAS executed despite second failure
	thirdOp := ops[2].(*mockOperation)
	assert.True(t, thirdOp.executed, "Third operation should be executed when ContinueOnError is true")
}

// TestExecuteOperations_EmptyList tests executeOperations with empty operation list.
func TestExecuteOperations_EmptyList(t *testing.T) {
	sync := New(nil)
	opts := DefaultSyncOptions()

	applied, failed, err := sync.executeOperations(context.Background(), nil, opts)

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
			Error:     errors.New("test error"),
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
					{Error: errors.New("test")},
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

// TestSync_NilCurrentConfig tests that Sync returns error for nil current config.
func TestSync_NilCurrentConfig(t *testing.T) {
	p, err := parser.New()
	require.NoError(t, err)

	desired, err := p.ParseFromString(`
global
    daemon
`)
	require.NoError(t, err)

	sync := New(nil)
	_, err = sync.Sync(context.Background(), nil, desired, DefaultSyncOptions())

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

	sync := New(nil)
	_, err = sync.Sync(context.Background(), current, nil, DefaultSyncOptions())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "desired configuration is nil")
}
