package synchronizer

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"haproxy-template-ic/pkg/dataplane/client"
	"haproxy-template-ic/pkg/dataplane/comparator"
	"haproxy-template-ic/pkg/dataplane/parser"
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

	s.logger.Info("Starting synchronization",
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

	s.logger.Info("Configuration changes detected",
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
		s.logger.Info("Executing sync transaction",
			"attempt", retries,
			"transaction_id", tx.ID,
			"version", tx.Version,
		)

		applied, failed, err := s.executeOperations(ctx, diff.Operations, opts)
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
		s.logger.Info("Sync transaction completed",
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

// executeOperations executes a list of operations, respecting ContinueOnError.
func (s *Synchronizer) executeOperations(ctx context.Context, operations []comparator.Operation, opts SyncOptions) (applied []comparator.Operation, failed []OperationError, err error) {
	for _, op := range operations {
		s.logger.Debug("Executing operation",
			"type", op.Type(),
			"section", op.Section(),
			"description", op.Describe(),
		)

		// Execute the operation
		// Note: transactionID handling will be added when Execute is implemented
		if execErr := op.Execute(ctx, s.client, ""); execErr != nil {
			s.logger.Error("Operation failed",
				"operation", op.Describe(),
				"error", execErr,
			)

			failed = append(failed, OperationError{
				Operation: op,
				Error:     execErr,
			})

			if !opts.ContinueOnError {
				return applied, failed, fmt.Errorf("operation failed: %w", execErr)
			}
			continue
		}

		applied = append(applied, op)
	}

	return applied, failed, nil
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
//
// Returns:
//   - SyncOperationsResult with reload information
//   - Error if any operation fails
//
// Example:
//
//	adapter := client.NewVersionAdapter(client, 3)
//	err := adapter.ExecuteTransaction(ctx, func(ctx context.Context, tx *client.Transaction) error {
//	    result, err := synchronizer.SyncOperations(ctx, client, diff.Operations, tx)
//	    return err
//	})
func SyncOperations(ctx context.Context, dpClient *client.DataplaneClient, operations []comparator.Operation, tx *client.Transaction) (*SyncOperationsResult, error) {
	// Execute all operations within the provided transaction
	for _, op := range operations {
		if err := op.Execute(ctx, dpClient, tx.ID); err != nil {
			return nil, fmt.Errorf("operation %q failed: %w", op.Describe(), err)
		}
	}

	// Operations succeeded - caller will commit the transaction
	// We don't know yet if reload will be triggered (depends on commit response)
	// Return minimal result - commit status will be added by caller
	return &SyncOperationsResult{
		ReloadTriggered: false, // Will be updated by caller after commit
		ReloadID:        "",
	}, nil
}
