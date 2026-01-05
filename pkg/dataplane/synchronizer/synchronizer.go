package synchronizer

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
	"golang.org/x/sync/errgroup"
)

// Synchronizer orchestrates configuration synchronization between
// desired state and HAProxy via the Dataplane API.
//
// It uses a Comparator to generate fine-grained diffs and executes
// operations within transactions with automatic retry on version conflicts.
type Synchronizer struct {
	client     *client.DataplaneClient
	comparator *comparator.Comparator
	logger     *slog.Logger
}

// New creates a new Synchronizer instance.
//
// Example:
//
//	client, _ := client.New(client.Config{
//	    BaseURL:  "http://localhost:5555/v2",
//	    Username: "admin",
//	    Password: "secret",
//	})
//	sync := synchronizer.New(client)
//	result, err := sync.Sync(ctx, currentConfig, desiredConfig, synchronizer.DefaultSyncOptions())
func New(c *client.DataplaneClient) *Synchronizer {
	return &Synchronizer{
		client:     c,
		comparator: comparator.New(),
		logger:     slog.Default(),
	}
}

// WithLogger sets a custom logger for the synchronizer.
func (s *Synchronizer) WithLogger(logger *slog.Logger) *Synchronizer {
	s.logger = logger
	return s
}

// Sync synchronizes the current configuration to match the desired configuration.
//
// The sync process:
// 1. Compares current and desired configurations
// 2. Generates ordered operations to transform current -> desired
// 3. Executes operations within a transaction (if policy allows)
// 4. Handles retries on version conflicts
// 5. Returns detailed results including successes/failures
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - current: The current HAProxy configuration
//   - desired: The desired HAProxy configuration
//   - opts: Sync options controlling behavior
//
// Returns a SyncResult with details about what was changed and any errors.
func (s *Synchronizer) Sync(ctx context.Context, current, desired *parser.StructuredConfig, opts SyncOptions) (*SyncResult, error) {
	startTime := time.Now()

	s.logger.Debug("starting synchronization",
		"policy", opts.Policy,
		"validate_before_apply", opts.ValidateBeforeApply,
		"continue_on_error", opts.ContinueOnError,
	)

	// Step 1: Compare configurations
	diff, err := s.comparator.Compare(current, desired)
	if err != nil {
		return nil, fmt.Errorf("failed to compare configurations: %w", err)
	}

	// Check if there are any changes
	if !diff.Summary.HasChanges() {
		s.logger.Info("No configuration changes detected")
		return NewNoChangesResult(opts.Policy, time.Since(startTime)), nil
	}

	s.logger.Debug("configuration changes detected",
		"total_operations", diff.Summary.TotalOperations(),
		"creates", diff.Summary.TotalCreates,
		"updates", diff.Summary.TotalUpdates,
		"deletes", diff.Summary.TotalDeletes,
	)

	// Step 2: Execute based on policy
	if opts.Policy.IsDryRun() {
		return s.dryRun(diff, startTime), nil
	}

	return s.apply(ctx, diff, opts, startTime)
}

// dryRun performs a dry-run sync (compare only, no apply).
func (s *Synchronizer) dryRun(diff *comparator.ConfigDiff, startTime time.Time) *SyncResult {
	s.logger.Info("Dry-run mode: Changes detected but not applied",
		"operations", diff.Summary.TotalOperations(),
	)

	// Log each operation that would be executed
	for _, op := range diff.Operations {
		s.logger.Debug("Would execute operation",
			"type", op.Type(),
			"section", op.Section(),
			"description", op.Describe(),
		)
	}

	return NewSuccessResult(PolicyDryRun, diff, nil, time.Since(startTime), 0)
}

// apply executes the sync operations with retry logic.
func (s *Synchronizer) apply(ctx context.Context, diff *comparator.ConfigDiff, opts SyncOptions, startTime time.Time) (*SyncResult, error) {
	maxRetries := opts.Policy.MaxRetries()
	adapter := client.NewVersionAdapter(s.client, maxRetries)

	var lastErr error
	var appliedOps []comparator.Operation
	var failedOps []OperationError
	retries := 0

	// Execute with retry logic
	_, err := adapter.ExecuteTransaction(ctx, func(ctx context.Context, tx *client.Transaction) error {
		retries++
		s.logger.Debug("executing sync transaction",
			"attempt", retries,
			"transaction_id", tx.ID,
			"version", tx.Version,
		)

		applied, failed, err := s.executeOperations(ctx, diff.Operations, tx.ID, opts)
		appliedOps = applied
		failedOps = failed

		if err != nil {
			lastErr = err
			return err
		}

		// Track results for potential retry
		if len(failed) > 0 {
			lastErr = fmt.Errorf("%d operations failed", len(failed))
			if !opts.ContinueOnError {
				return lastErr
			}
		}

		// All operations succeeded or we're continuing despite errors
		duration := time.Since(startTime)
		s.logger.Debug("sync transaction completed",
			"applied", len(applied),
			"failed", len(failed),
			"duration", duration,
		)

		return nil
	})

	duration := time.Since(startTime)

	if err != nil {
		// Check if it's a version conflict that exceeded retries
		if verr, ok := err.(*client.VersionConflictError); ok {
			msg := fmt.Sprintf("Version conflict after %d retries (expected: %d, actual: %s)",
				retries, verr.ExpectedVersion, verr.ActualVersion)
			s.logger.Error("Sync failed due to version conflicts", "error", msg)
			return NewFailureResult(opts.Policy, diff, appliedOps, failedOps, duration, retries, msg), err
		}

		s.logger.Error("Sync failed", "error", err)
		return NewFailureResult(opts.Policy, diff, appliedOps, failedOps, duration, retries, err.Error()), err
	}

	s.logger.Info("Sync completed successfully",
		"operations", diff.Summary.TotalOperations(),
		"duration", duration,
		"retries", retries,
	)

	return NewSuccessResult(opts.Policy, diff, appliedOps, duration, retries), nil
}

// executeOperations executes a list of operations using parallel execution by priority.
// Operations at the same priority level run in parallel; priority groups run sequentially.
func (s *Synchronizer) executeOperations(ctx context.Context, operations []comparator.Operation, txID string, opts SyncOptions) (applied []comparator.Operation, failed []OperationError, err error) {
	result, err := executeOperationsParallel(ctx, s.client, operations, txID, parallelExecutionOptions{
		ContinueOnError: opts.ContinueOnError,
		Logger:          s.logger,
		MaxParallel:     opts.MaxParallel,
	})

	if result != nil {
		applied = result.Applied
		failed = result.Failed
	}

	return applied, failed, err
}

// SyncFromStrings is a convenience method that parses configuration strings
// and performs synchronization.
//
// This is useful for testing and simple use cases where you have raw
// HAProxy configuration strings.
func (s *Synchronizer) SyncFromStrings(ctx context.Context, currentConfig, desiredConfig string, opts SyncOptions) (*SyncResult, error) {
	// Parse current config
	p, err := parser.New()
	if err != nil {
		return nil, fmt.Errorf("failed to create parser: %w", err)
	}

	current, err := p.ParseFromString(currentConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to parse current config: %w", err)
	}

	// Parse desired config
	desired, err := p.ParseFromString(desiredConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to parse desired config: %w", err)
	}

	return s.Sync(ctx, current, desired, opts)
}

// SyncOperationsResult contains information about a synchronization operation.
type SyncOperationsResult struct {
	// ReloadTriggered indicates whether a HAProxy reload was triggered.
	// true when commit status is 202, false when 200.
	ReloadTriggered bool

	// ReloadID is the reload identifier from the Reload-ID response header.
	// Only set when ReloadTriggered is true.
	ReloadID string
}

// SyncOperations executes a list of operations within the provided transaction.
//
// This function must be called within a transaction context (e.g., via VersionAdapter.ExecuteTransaction).
// The transaction provides automatic retry logic on version conflicts.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - client: The DataplaneClient
//   - operations: List of operations to execute
//   - tx: The transaction to execute operations within (from VersionAdapter)
//   - maxParallel: Maximum concurrent operations (0 = unlimited)
//
// Returns:
//   - SyncOperationsResult with reload information
//   - Error if any operation fails
//
// Example:
//
//	adapter := client.NewVersionAdapter(client, 3)
//	err := adapter.ExecuteTransaction(ctx, func(ctx context.Context, tx *client.Transaction) error {
//	    result, err := synchronizer.SyncOperations(ctx, client, diff.Operations, tx, 80)
//	    return err
//	})
func SyncOperations(ctx context.Context, dpClient *client.DataplaneClient, operations []comparator.Operation, tx *client.Transaction, maxParallel int) (*SyncOperationsResult, error) {
	_, err := executeOperationsParallel(ctx, dpClient, operations, tx.ID, parallelExecutionOptions{
		ContinueOnError: false,
		Logger:          nil, // No logging for standalone function - caller handles logging
		MaxParallel:     maxParallel,
	})
	if err != nil {
		return nil, err
	}

	// Operations succeeded - caller will commit the transaction
	// We don't know yet if reload will be triggered (depends on commit response)
	// Return minimal result - commit status will be added by caller
	return &SyncOperationsResult{
		ReloadTriggered: false, // Will be updated by caller after commit
		ReloadID:        "",
	}, nil
}

// parallelExecutionOptions configures parallel operation execution.
type parallelExecutionOptions struct {
	// ContinueOnError controls whether to continue executing operations after a failure.
	// If false, execution stops on first error. If true, all operations are attempted.
	ContinueOnError bool

	// Logger for operation execution logging. If nil, logging is skipped.
	Logger *slog.Logger

	// MaxParallel limits concurrent operations. 0 means unlimited.
	MaxParallel int
}

// parallelExecutionResult contains the results of parallel operation execution.
type parallelExecutionResult struct {
	// Applied contains operations that completed successfully.
	Applied []comparator.Operation

	// Failed contains operations that failed with their errors.
	Failed []OperationError
}

// executeOperationsParallel executes operations grouped by priority with parallel execution
// within each priority group. This is the unified execution function used by both
// SyncOperations and Synchronizer.executeOperations.
//
// Operations are grouped by priority and executed in priority order (lower first).
// Within each priority group, operations run in parallel since they have no dependencies.
//
// When ContinueOnError is false, execution stops on the first error.
// When ContinueOnError is true, all operations are attempted and failures are collected.
func executeOperationsParallel(
	ctx context.Context,
	dpClient *client.DataplaneClient,
	operations []comparator.Operation,
	txID string,
	opts parallelExecutionOptions,
) (*parallelExecutionResult, error) {
	if len(operations) == 0 {
		return &parallelExecutionResult{}, nil
	}

	result := &parallelExecutionResult{}

	// Group operations by priority
	groups := groupByPriority(operations)
	priorities := sortedPriorityKeys(groups)

	// Execute each priority group sequentially
	for _, priority := range priorities {
		ops := groups[priority]

		applied, failed, err := executePriorityGroup(ctx, dpClient, ops, txID, opts)
		result.Applied = append(result.Applied, applied...)
		result.Failed = append(result.Failed, failed...)

		if err != nil && !opts.ContinueOnError {
			return result, err
		}
	}

	// When ContinueOnError is false, we already returned on first error above.
	// When ContinueOnError is true, failures are tracked in result.Failed but we don't return error.
	// This allows the caller to check result.Failed to see what failed.
	if len(result.Failed) > 0 && !opts.ContinueOnError {
		return result, fmt.Errorf("%d operation(s) failed", len(result.Failed))
	}

	return result, nil
}

// executePriorityGroup executes all operations in a single priority group in parallel.
func executePriorityGroup(
	ctx context.Context,
	dpClient *client.DataplaneClient,
	ops []comparator.Operation,
	txID string,
	opts parallelExecutionOptions,
) (applied []comparator.Operation, failed []OperationError, err error) {
	if opts.ContinueOnError {
		return executePriorityGroupContinueOnError(ctx, dpClient, ops, txID, opts)
	}
	return executePriorityGroupStopOnError(ctx, dpClient, ops, txID, opts)
}

// executePriorityGroupStopOnError executes operations in parallel, stopping on first error.
func executePriorityGroupStopOnError(
	ctx context.Context,
	dpClient *client.DataplaneClient,
	ops []comparator.Operation,
	txID string,
	opts parallelExecutionOptions,
) (applied []comparator.Operation, failed []OperationError, err error) {
	g, gCtx := errgroup.WithContext(ctx)

	// Apply concurrency limit if specified
	if opts.MaxParallel > 0 {
		g.SetLimit(opts.MaxParallel)
	}

	// Track results (thread-safe via channels)
	appliedChan := make(chan comparator.Operation, len(ops))
	failedChan := make(chan OperationError, len(ops))

	for _, op := range ops {
		if opts.Logger != nil {
			opts.Logger.Debug("Executing operation",
				"type", op.Type(),
				"section", op.Section(),
				"description", op.Describe(),
			)
		}

		g.Go(func() error {
			if execErr := op.Execute(gCtx, dpClient, txID); execErr != nil {
				if opts.Logger != nil {
					opts.Logger.Error("Operation failed",
						"operation", op.Describe(),
						"error", execErr,
					)
				}
				failedChan <- OperationError{Operation: op, Cause: execErr}
				return fmt.Errorf("operation %q failed: %w", op.Describe(), execErr)
			}
			appliedChan <- op
			return nil
		})
	}

	err = g.Wait()
	close(appliedChan)
	close(failedChan)

	// Collect results
	for op := range appliedChan {
		applied = append(applied, op)
	}
	for opErr := range failedChan {
		failed = append(failed, opErr)
	}

	return applied, failed, err
}

// executePriorityGroupContinueOnError executes all operations, collecting failures.
func executePriorityGroupContinueOnError(
	ctx context.Context,
	dpClient *client.DataplaneClient,
	ops []comparator.Operation,
	txID string,
	opts parallelExecutionOptions,
) (applied []comparator.Operation, failed []OperationError, err error) {
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Create semaphore for concurrency limiting if MaxParallel is set
	var sem chan struct{}
	if opts.MaxParallel > 0 {
		sem = make(chan struct{}, opts.MaxParallel)
	}

	// Callback to record results
	onSuccess := func(op comparator.Operation) {
		mu.Lock()
		applied = append(applied, op)
		mu.Unlock()
	}
	onFailure := func(op comparator.Operation, execErr error) {
		mu.Lock()
		failed = append(failed, OperationError{Operation: op, Cause: execErr})
		mu.Unlock()
	}

	for _, op := range ops {
		if opts.Logger != nil {
			opts.Logger.Debug("Executing operation",
				"type", op.Type(),
				"section", op.Section(),
				"description", op.Describe(),
			)
		}

		wg.Add(1)
		go executeWithSemaphore(ctx, dpClient, txID, op, sem, opts.Logger, onSuccess, onFailure, wg.Done)
	}

	wg.Wait()

	if len(failed) > 0 {
		return applied, failed, fmt.Errorf("%d operation(s) failed in priority group", len(failed))
	}
	return applied, failed, nil
}

// executeWithSemaphore executes a single operation with optional semaphore-based concurrency limiting.
func executeWithSemaphore(
	ctx context.Context,
	dpClient *client.DataplaneClient,
	txID string,
	op comparator.Operation,
	sem chan struct{},
	logger *slog.Logger,
	onSuccess func(comparator.Operation),
	onFailure func(comparator.Operation, error),
	done func(),
) {
	defer done()

	// Acquire semaphore slot if limiting is enabled
	if sem != nil {
		select {
		case sem <- struct{}{}:
			defer func() { <-sem }()
		case <-ctx.Done():
			onFailure(op, ctx.Err())
			return
		}
	}

	if execErr := op.Execute(ctx, dpClient, txID); execErr != nil {
		if logger != nil {
			logger.Error("Operation failed",
				"operation", op.Describe(),
				"error", execErr,
			)
		}
		onFailure(op, execErr)
		return
	}

	onSuccess(op)
}

// groupByPriority groups operations by their priority level.
// Operations at the same priority level have no dependencies and can be executed in parallel.
func groupByPriority(ops []comparator.Operation) map[int][]comparator.Operation {
	groups := make(map[int][]comparator.Operation)
	for _, op := range ops {
		p := op.Priority()
		groups[p] = append(groups[p], op)
	}
	return groups
}

// sortedPriorityKeys returns the priority keys in ascending order.
// This ensures operations are executed in dependency order (lower priority first).
func sortedPriorityKeys(groups map[int][]comparator.Operation) []int {
	keys := make([]int, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}
