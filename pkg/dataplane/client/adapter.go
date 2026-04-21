package client

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// abortContext creates a fresh context for transaction abort operations.
// This allows cleanup to proceed even when the original context has timed out.
func abortContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}

// abortTransaction performs transaction cleanup with a fresh context.
// Uses abortContext to ensure cleanup proceeds even when the original context has timed out.
func abortTransaction(tx *Transaction) {
	abortCtx, abortCancel := abortContext()
	defer abortCancel()
	_ = tx.Abort(abortCtx)
}

// VersionAdapter wraps a DataplaneClient to provide automatic version management
// and 409 conflict retry logic.
//
// When a version conflict occurs (409 response), the adapter automatically:
// 1. Extracts the new version from the response header
// 2. Retries the operation with the new version
// 3. Repeats up to MaxRetries times
//
// This handles the common case of concurrent configuration updates without
// requiring manual retry logic in application code.
type VersionAdapter struct {
	client     *DataplaneClient
	maxRetries int
}

// NewVersionAdapter creates a new VersionAdapter with the specified client and retry limit.
//
// Parameters:
//   - client: The underlying DataplaneClient
//   - maxRetries: Maximum number of retry attempts on 409 conflicts (default: 3)
//
// Example:
//
//	client, _ := client.New(client.Config{...})
//	adapter := client.NewVersionAdapter(client, 3)
//	err := adapter.ExecuteTransaction(ctx, func(ctx context.Context, tx *Transaction) error {
//	    // Execute operations within transaction
//	    return nil
//	})
func NewVersionAdapter(client *DataplaneClient, maxRetries int) *VersionAdapter {
	if maxRetries <= 0 {
		maxRetries = 3 // Default to 3 retries
	}

	return &VersionAdapter{
		client:     client,
		maxRetries: maxRetries,
	}
}

// TransactionFunc is a function that executes operations within a transaction.
// The function receives the transaction and should perform all desired operations.
// If the function returns an error, the transaction will be aborted.
type TransactionFunc func(ctx context.Context, tx *Transaction) error

// versionResolver returns the config version to use for a given attempt of the
// retry loop. attempt is 0 on the first try and increments on each retry.
type versionResolver func(ctx context.Context, attempt int) (int64, error)

// executeTransactionWithRetry runs the retry loop shared by ExecuteTransaction
// and ExecuteTransactionWithVersion:
//  1. Resolve the version for this attempt
//  2. Create a transaction at that version
//  3. Run fn within the transaction
//  4. Commit
//
// 409 conflicts at steps 2 or 4 retry the whole loop up to a.maxRetries times.
// Any other error aborts the transaction and returns. Returns the CommitResult
// from the successful commit.
func (a *VersionAdapter) executeTransactionWithRetry(ctx context.Context, resolve versionResolver, fn TransactionFunc) (*CommitResult, error) {
	var lastErr error

	for attempt := 0; attempt <= a.maxRetries; attempt++ {
		version, err := resolve(ctx, attempt)
		if err != nil {
			return nil, err
		}

		tx, err := a.client.CreateTransaction(ctx, version)
		if err != nil {
			if _, ok := errors.AsType[*VersionConflictError](err); ok {
				lastErr = err
				continue
			}
			return nil, fmt.Errorf("creating transaction: %w", err)
		}

		if err := fn(ctx, tx); err != nil {
			abortTransaction(tx)
			return nil, fmt.Errorf("transaction operation failed: %w", err)
		}

		commitResult, err := tx.Commit(ctx)
		if err != nil {
			if _, ok := errors.AsType[*VersionConflictError](err); ok {
				lastErr = err
				abortTransaction(tx)
				continue
			}
			abortTransaction(tx)
			return nil, fmt.Errorf("committing transaction: %w", err)
		}

		return commitResult, nil
	}

	return nil, fmt.Errorf("transaction failed after %d retries: %w", a.maxRetries, lastErr)
}

// ExecuteTransaction executes a transactional operation with automatic 409 retry.
//
// This method:
// 1. Fetches the current configuration version
// 2. Creates a transaction with that version
// 3. Executes the provided function within the transaction
// 4. Commits the transaction if successful
// 5. Aborts the transaction if an error occurs
// 6. Retries on 409 conflicts with the new version
//
// Returns the CommitResult from the successful commit.
//
// Example:
//
//	adapter := client.NewVersionAdapter(client, 3)
//	result, err := adapter.ExecuteTransaction(ctx, func(ctx context.Context, tx *Transaction) error {
//	    // Create backend
//	    backend := &models.Backend{Name: "web"}
//	    _, err := client.Client().CreateBackend(ctx, &CreateBackendParams{
//	        TransactionID: &tx.ID,
//	    }, backend)
//	    return err
//	})
func (a *VersionAdapter) ExecuteTransaction(ctx context.Context, fn TransactionFunc) (*CommitResult, error) {
	return a.executeTransactionWithRetry(ctx, func(ctx context.Context, _ int) (int64, error) {
		version, err := a.client.GetVersion(ctx)
		if err != nil {
			return 0, fmt.Errorf("getting version: %w", err)
		}
		return version, nil
	}, fn)
}

// ExecuteTransactionWithVersion executes a transactional operation with a specific version.
//
// This is similar to ExecuteTransaction but allows specifying the version explicitly
// instead of fetching it. Useful when you already know the current version.
//
// Parameters:
//   - ctx: Context for the operation
//   - version: The configuration version to use on the first attempt
//   - fn: The function to execute within the transaction
//
// On 409 conflicts, the version is re-fetched for each retry.
func (a *VersionAdapter) ExecuteTransactionWithVersion(ctx context.Context, version int64, fn TransactionFunc) error {
	_, err := a.executeTransactionWithRetry(ctx, func(ctx context.Context, attempt int) (int64, error) {
		if attempt == 0 {
			return version, nil
		}
		v, err := a.client.GetVersion(ctx)
		if err != nil {
			return 0, fmt.Errorf("getting version on retry: %w", err)
		}
		return v, nil
	}, fn)
	return err
}

// ParseVersionFromHeader extracts the version number from a Configuration-Version header.
func ParseVersionFromHeader(header string) (int64, error) {
	if header == "" {
		return 0, errors.New("empty version header")
	}

	version, err := strconv.ParseInt(header, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid version header %q: %w", header, err)
	}

	return version, nil
}
