package synchronizer

import (
	"context"
	"errors"
	"fmt"
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

func TestSyncOperations_Success(t *testing.T) {
	ops := []comparator.Operation{
		newMockOperation(sections.OperationCreate, "backend", 20),
		newMockOperation(sections.OperationCreate, "server", 30),
	}

	tx := &client.Transaction{
		ID:      "test-tx-123",
		Version: 1,
	}

	result, err := SyncOperations(context.Background(), nil, ops, tx, 0) // 0 = unlimited

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

	result, err := SyncOperations(context.Background(), nil, ops, tx, 0) // 0 = unlimited

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "operation failed")
	assert.Contains(t, err.Error(), "failing server operation")

	// Verify later priority operation was NOT executed
	assert.False(t, laterOp.executed, "Higher priority operations should not execute after earlier priority fails")
}

func TestSyncOperations_EmptyList(t *testing.T) {
	tx := &client.Transaction{
		ID:      "test-tx-empty",
		Version: 1,
	}

	result, err := SyncOperations(context.Background(), nil, nil, tx, 0) // 0 = unlimited

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.ReloadTriggered)
}

// newRecordingOp returns an op that appends name to the shared slice when run.
func newRecordingOp(name, section string, priority int, mu *sync.Mutex, out *[]string) *mockOperation {
	op := newMockOperation(sections.OperationCreate, section, priority)
	op.executeFunc = func(_ context.Context, _ *client.DataplaneClient, _ string) error {
		mu.Lock()
		defer mu.Unlock()
		*out = append(*out, name)
		return nil
	}
	return op
}

// Operations with different priorities are grouped and executed in priority
// order, with operations at the same priority running in parallel.
func TestSyncOperations_ParallelByPriority(t *testing.T) {
	var executionOrder []string
	var mu sync.Mutex

	// Priority 10: op1, op2 (first, in parallel)
	// Priority 20: op3 (second)
	// Priority 30: op4, op5 (third, in parallel)
	op1 := newRecordingOp("op1-priority10", "frontend1", 10, &mu, &executionOrder)
	op2 := newRecordingOp("op2-priority10", "frontend2", 10, &mu, &executionOrder)
	op3 := newRecordingOp("op3-priority20", "backend", 20, &mu, &executionOrder)
	op4 := newRecordingOp("op4-priority30", "server1", 30, &mu, &executionOrder)
	op5 := newRecordingOp("op5-priority30", "server2", 30, &mu, &executionOrder)

	ops := []comparator.Operation{op5, op3, op1, op4, op2} // Scrambled order
	tx := &client.Transaction{ID: "test-tx", Version: 1}

	result, err := SyncOperations(context.Background(), nil, ops, tx, 0) // 0 = unlimited

	require.NoError(t, err)
	require.NotNil(t, result)

	for _, op := range []*mockOperation{op1, op2, op3, op4, op5} {
		assert.True(t, op.executed, "%s should be executed", op.section)
	}

	mu.Lock()
	order := executionOrder
	mu.Unlock()
	require.Len(t, order, 5)

	var priority10Positions, priority30Positions []int
	priority20Position := -1
	for i, name := range order {
		switch {
		case strings.Contains(name, "priority10"):
			priority10Positions = append(priority10Positions, i)
		case strings.Contains(name, "priority20"):
			priority20Position = i
		case strings.Contains(name, "priority30"):
			priority30Positions = append(priority30Positions, i)
		}
	}

	for _, pos := range priority10Positions {
		assert.Less(t, pos, priority20Position, "Priority 10 operations should execute before priority 20")
	}
	for _, pos := range priority30Positions {
		assert.Less(t, priority20Position, pos, "Priority 20 operations should execute before priority 30")
	}
}

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

func TestSortedPriorityKeys(t *testing.T) {
	groups := map[int][]comparator.Operation{
		30: {newMockOperation(sections.OperationCreate, "server", 30)},
		10: {newMockOperation(sections.OperationCreate, "frontend", 10)},
		20: {newMockOperation(sections.OperationCreate, "backend", 20)},
	}

	keys := sortedPriorityKeys(groups)

	assert.Equal(t, []int{10, 20, 30}, keys, "Keys should be sorted in ascending order")
}

// Index-based operations use unique priorities per index (basePriority*1000 +
// index), causing each index to be in its own priority group. Since priority
// groups execute sequentially, this guarantees correct ordering.
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

	result, err := SyncOperations(context.Background(), nil, ops, tx, 0) // 0 = unlimited

	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify all operations executed
	for _, op := range ops {
		assert.True(t, op.(*mockOperation).executed, "Operation should be executed")
	}

	// Verify timing: each index must complete BEFORE the next index starts
	mu.Lock()
	defer mu.Unlock()

	for i := range 4 {
		current := timings[i]
		next := timings[i+1]

		assert.True(t, current.completeTime.Before(next.startTime) || current.completeTime.Equal(next.startTime),
			"Index %d should complete before index %d starts (completed: %v, started: %v)",
			i, i+1, current.completeTime, next.startTime)
	}
}

// Delete operations execute in reverse index order (higher indices first). This
// matters because deleting index 0 while index 1 still exists could cause array
// reindexing issues.
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

	result, err := SyncOperations(context.Background(), nil, ops, tx, 0) // 0 = unlimited

	require.NoError(t, err)
	require.NotNil(t, result)

	mu.Lock()
	defer mu.Unlock()

	// Verify execution order is reverse (4, 3, 2, 1, 0)
	expected := []int{4, 3, 2, 1, 0}
	assert.Equal(t, expected, executionOrder,
		"Delete operations should execute in reverse index order (highest first)")
}

// MaxParallel limits concurrency: creates many operations at the same priority
// and verifies that no more than MaxParallel run concurrently.
func TestSyncOperations_MaxParallel_LimitsConcurrency(t *testing.T) {
	const (
		totalOps    = 50
		maxParallel = 5
		opDuration  = 10 * time.Millisecond
	)

	// Track max concurrent operations
	var currentConcurrent atomic.Int32
	var maxConcurrent atomic.Int32

	// Create operations that track concurrency
	ops := make([]comparator.Operation, totalOps)
	for i := range totalOps {
		op := newMockOperation(sections.OperationCreate, fmt.Sprintf("backend-%d", i), 20) // Same priority
		op.executeFunc = func(_ context.Context, _ *client.DataplaneClient, _ string) error {
			// Increment current concurrent count
			current := currentConcurrent.Add(1)

			// Update max if this is a new high
			for {
				max := maxConcurrent.Load()
				if current <= max {
					break
				}
				if maxConcurrent.CompareAndSwap(max, current) {
					break
				}
			}

			// Simulate work
			time.Sleep(opDuration)

			// Decrement concurrent count
			currentConcurrent.Add(-1)
			return nil
		}
		ops[i] = op
	}

	tx := &client.Transaction{ID: "test-tx-maxparallel", Version: 1}

	result, err := SyncOperations(context.Background(), nil, ops, tx, maxParallel)

	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify all operations executed
	for _, op := range ops {
		assert.True(t, op.(*mockOperation).executed, "All operations should be executed")
	}

	// Verify max concurrent never exceeded limit
	observedMax := maxConcurrent.Load()
	assert.LessOrEqual(t, observedMax, int32(maxParallel),
		"Max concurrent operations (%d) should not exceed MaxParallel (%d)", observedMax, maxParallel)

	// Verify we actually hit the limit (operations should have parallelized)
	assert.GreaterOrEqual(t, observedMax, int32(2),
		"Should have some parallelism (observed max: %d)", observedMax)
}

func TestSyncOperations_MaxParallel_Unlimited(t *testing.T) {
	const (
		totalOps   = 20
		opDuration = 10 * time.Millisecond
	)

	// Track max concurrent operations
	var currentConcurrent atomic.Int32
	var maxConcurrent atomic.Int32

	// Create operations that track concurrency
	ops := make([]comparator.Operation, totalOps)
	for i := range totalOps {
		op := newMockOperation(sections.OperationCreate, fmt.Sprintf("backend-%d", i), 20) // Same priority
		op.executeFunc = func(_ context.Context, _ *client.DataplaneClient, _ string) error {
			current := currentConcurrent.Add(1)

			for {
				max := maxConcurrent.Load()
				if current <= max {
					break
				}
				if maxConcurrent.CompareAndSwap(max, current) {
					break
				}
			}

			time.Sleep(opDuration)
			currentConcurrent.Add(-1)
			return nil
		}
		ops[i] = op
	}

	tx := &client.Transaction{ID: "test-tx-unlimited", Version: 1}

	// MaxParallel=0 means unlimited
	result, err := SyncOperations(context.Background(), nil, ops, tx, 0)

	require.NoError(t, err)
	require.NotNil(t, result)

	// With unlimited concurrency and same priority, all ops should run together
	observedMax := maxConcurrent.Load()
	assert.GreaterOrEqual(t, observedMax, int32(totalOps/2),
		"With unlimited concurrency, should see high parallelism (observed: %d, expected at least: %d)",
		observedMax, totalOps/2)
}
