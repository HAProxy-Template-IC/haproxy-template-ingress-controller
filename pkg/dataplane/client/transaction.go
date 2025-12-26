package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"

	v30 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v30"
	v30ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v30ee"
	v31 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v31"
	v31ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v31ee"
	v32 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32"
	v32ee "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32ee"
)

// Transaction represents an HAProxy Dataplane API transaction.
//
// Transactions provide atomic configuration changes - all operations within
// a transaction are applied together or none are applied. This prevents
// partial configuration updates that could leave HAProxy in an inconsistent state.
//
// Transaction lifecycle:
//  1. Create transaction with current version
//  2. Execute operations within transaction (passing transaction ID)
//  3. Commit transaction to apply all changes
//  4. OR Abort transaction to discard all changes
//
// Thread-safety: Transaction methods are safe for concurrent use.
// The transaction tracks its state to prevent double commits/aborts.
type Transaction struct {
	ID      string
	Version int64
	client  *DataplaneClient

	// State tracking (thread-safe with mutex)
	mu           sync.Mutex
	committed    bool
	aborted      bool
	commitResult *CommitResult // Cached result for idempotent Commit()
}

// TransactionResponse represents the response when creating a transaction.
type TransactionResponse struct {
	ID      string `json:"id"`
	Version int    `json:"version"`
}

// CreateTransaction creates a new transaction with the specified configuration version.
//
// The version parameter enables optimistic locking - if another process has
// modified the configuration since you fetched the version, transaction creation
// will fail with a 409 Conflict error.
// Works with all HAProxy DataPlane API versions (v3.0+).
//
// Example:
//
//	version, _ := client.GetVersion(context.Background())
//	tx, err := client.CreateTransaction(context.Background(), version)
//	if err != nil {
//	    slog.Error("failed to create transaction", "error", err)
//	    os.Exit(1)
//	}
//	defer tx.Abort(context.Background()) // Ensure cleanup on error
//
//	// Execute operations with tx.ID
//	// ...
//
//	err = tx.Commit(context.Background())
func (c *DataplaneClient) CreateTransaction(ctx context.Context, version int64) (*Transaction, error) {
	resp, err := c.Dispatch(ctx, CallFunc[*http.Response]{
		V32: func(c *v32.Client) (*http.Response, error) {
			return c.StartTransaction(ctx, &v32.StartTransactionParams{Version: int(version)})
		},
		V31: func(c *v31.Client) (*http.Response, error) {
			return c.StartTransaction(ctx, &v31.StartTransactionParams{Version: int(version)})
		},
		V30: func(c *v30.Client) (*http.Response, error) {
			return c.StartTransaction(ctx, &v30.StartTransactionParams{Version: int(version)})
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.StartTransaction(ctx, &v32ee.StartTransactionParams{Version: int(version)})
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.StartTransaction(ctx, &v31ee.StartTransactionParams{Version: int(version)})
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.StartTransaction(ctx, &v30ee.StartTransactionParams{Version: int(version)})
		},
	})

	if err != nil {
		return nil, fmt.Errorf("failed to start transaction: %w", err)
	}
	defer resp.Body.Close()

	// Check for version conflicts (both 409 and 406 indicate version conflicts)
	if resp.StatusCode == 409 || resp.StatusCode == 406 {
		// Version conflict - extract new version from header if available
		// 409 = Conflict (typical version mismatch)
		// 406 = Not Acceptable (transaction is outdated)
		newVersion := resp.Header.Get("Configuration-Version")
		if newVersion != "" {
			return nil, &VersionConflictError{
				ExpectedVersion: version,
				ActualVersion:   newVersion,
			}
		}
		// If no version header, still return VersionConflictError for retry logic
		return nil, &VersionConflictError{
			ExpectedVersion: version,
			ActualVersion:   "unknown",
		}
	}

	if resp.StatusCode != 201 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to start transaction: status %d: %s", resp.StatusCode, string(body))
	}

	// Parse transaction response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read transaction response: %w", err)
	}

	var txResp TransactionResponse
	if err := json.Unmarshal(body, &txResp); err != nil {
		return nil, fmt.Errorf("failed to parse transaction response: %w", err)
	}

	return &Transaction{
		ID:      txResp.ID,
		Version: version,
		client:  c,
	}, nil
}

// CommitResult contains information about a transaction commit operation.
type CommitResult struct {
	// StatusCode is the HTTP status code from the commit response.
	// 200 = configuration applied without reload
	// 202 = configuration applied with reload triggered
	StatusCode int

	// ReloadID is the reload identifier from the Reload-ID response header.
	// Only set when StatusCode is 202 (reload triggered).
	ReloadID string
}

// Commit commits all changes made within this transaction.
//
// After successful commit, all operations executed with this transaction ID
// are applied atomically to the HAProxy configuration. If commit fails,
// no changes are applied.
//
// This method is idempotent - calling it multiple times will return the cached
// result from the first successful commit, but will log a WARNING to indicate
// a programming error (double commit).
//
// Returns CommitResult containing status code and reload ID (if reload triggered).
func (tx *Transaction) Commit(ctx context.Context) (*CommitResult, error) {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	// WARN: Already committed - return cached result
	if tx.committed {
		slog.Warn("Transaction.Commit() called multiple times - this is a programming error",
			"transaction_id", tx.ID,
			"version", tx.Version,
		)
		if tx.commitResult != nil {
			return tx.commitResult, nil // Idempotent: return cached result
		}
		return nil, fmt.Errorf("transaction %s already committed but no cached result available", tx.ID)
	}

	// ERROR: Cannot commit aborted transaction
	if tx.aborted {
		return nil, fmt.Errorf("cannot commit aborted transaction %s", tx.ID)
	}

	// Perform actual commit
	forceReload := false

	resp, err := tx.client.Dispatch(ctx, CallFunc[*http.Response]{
		V32: func(c *v32.Client) (*http.Response, error) {
			return c.CommitTransaction(ctx, tx.ID, &v32.CommitTransactionParams{ForceReload: &forceReload})
		},
		V31: func(c *v31.Client) (*http.Response, error) {
			return c.CommitTransaction(ctx, tx.ID, &v31.CommitTransactionParams{ForceReload: &forceReload})
		},
		V30: func(c *v30.Client) (*http.Response, error) {
			return c.CommitTransaction(ctx, tx.ID, &v30.CommitTransactionParams{ForceReload: &forceReload})
		},
		V32EE: func(c *v32ee.Client) (*http.Response, error) {
			return c.CommitTransaction(ctx, tx.ID, &v32ee.CommitTransactionParams{ForceReload: &forceReload})
		},
		V31EE: func(c *v31ee.Client) (*http.Response, error) {
			return c.CommitTransaction(ctx, tx.ID, &v31ee.CommitTransactionParams{ForceReload: &forceReload})
		},
		V30EE: func(c *v30ee.Client) (*http.Response, error) {
			return c.CommitTransaction(ctx, tx.ID, &v30ee.CommitTransactionParams{ForceReload: &forceReload})
		},
	})

	if err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}
	defer resp.Body.Close()

	// Check for version conflicts (both 409 and 406 indicate version conflicts)
	if resp.StatusCode == 409 || resp.StatusCode == 406 {
		// Version conflict during commit
		// 409 = Conflict (typical version mismatch)
		// 406 = Not Acceptable (transaction is outdated)
		newVersion := resp.Header.Get("Configuration-Version")
		if newVersion != "" {
			return nil, &VersionConflictError{
				ExpectedVersion: tx.Version,
				ActualVersion:   newVersion,
			}
		}
		// If no version header, still return VersionConflictError for retry logic
		return nil, &VersionConflictError{
			ExpectedVersion: tx.Version,
			ActualVersion:   "unknown",
		}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to commit transaction: status %d: %s", resp.StatusCode, string(body))
	}

	// Extract reload information
	result := &CommitResult{
		StatusCode: resp.StatusCode,
	}

	// Status 202 indicates reload was triggered
	if resp.StatusCode == 202 {
		result.ReloadID = resp.Header.Get("Reload-ID")
	}

	// Mark as committed and cache result for idempotent behavior
	tx.committed = true
	tx.commitResult = result

	return result, nil
}

// Abort aborts and discards all changes made within this transaction.
//
// All operations executed with this transaction ID are discarded and the
// HAProxy configuration remains unchanged. This is useful for cleanup
// when an error occurs during transaction execution.
//
// This method is idempotent - calling it multiple times is safe but will
// log a WARNING to indicate a programming error (double abort). Calling
// Abort() after Commit() is silently ignored (common in defer cleanup).
func (tx *Transaction) Abort(ctx context.Context) error {
	tx.mu.Lock()
	defer tx.mu.Unlock()

	// Already committed - cannot abort, but don't fail (idempotent cleanup)
	// This is common in defer cleanup: defer tx.Abort()
	if tx.committed {
		return nil // Silent: expected in defer cleanup after successful commit
	}

	// WARN: Already aborted
	if tx.aborted {
		slog.Warn("Transaction.Abort() called multiple times - this is a programming error",
			"transaction_id", tx.ID,
			"version", tx.Version,
		)
		return nil // Idempotent: safe to call multiple times
	}

	// Perform actual abort
	resp, err := tx.client.Dispatch(ctx, CallFunc[*http.Response]{
		V32:   func(c *v32.Client) (*http.Response, error) { return c.DeleteTransaction(ctx, tx.ID) },
		V31:   func(c *v31.Client) (*http.Response, error) { return c.DeleteTransaction(ctx, tx.ID) },
		V30:   func(c *v30.Client) (*http.Response, error) { return c.DeleteTransaction(ctx, tx.ID) },
		V32EE: func(c *v32ee.Client) (*http.Response, error) { return c.DeleteTransaction(ctx, tx.ID) },
		V31EE: func(c *v31ee.Client) (*http.Response, error) { return c.DeleteTransaction(ctx, tx.ID) },
		V30EE: func(c *v30ee.Client) (*http.Response, error) { return c.DeleteTransaction(ctx, tx.ID) },
	})

	if err != nil {
		return fmt.Errorf("failed to abort transaction: %w", err)
	}
	defer resp.Body.Close()

	// 404 means transaction already gone (committed or aborted elsewhere) - that's ok
	if resp.StatusCode == 404 {
		tx.aborted = true // Mark as aborted even though API said 404
		return nil
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to abort transaction: status %d: %s", resp.StatusCode, string(body))
	}

	// Mark as aborted
	tx.aborted = true

	return nil
}

// IsCommitted returns true if the transaction has been committed.
//
// This can be useful for conditional logic or debugging, but generally
// you should not need to check this - use proper control flow instead.
func (tx *Transaction) IsCommitted() bool {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return tx.committed
}

// IsAborted returns true if the transaction has been aborted.
//
// This can be useful for conditional logic or debugging, but generally
// you should not need to check this - use proper control flow instead.
func (tx *Transaction) IsAborted() bool {
	tx.mu.Lock()
	defer tx.mu.Unlock()
	return tx.aborted
}
