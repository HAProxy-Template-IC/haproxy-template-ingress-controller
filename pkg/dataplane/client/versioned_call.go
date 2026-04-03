package client

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// ExecuteWithVersion executes an API call that requires a version parameter with automatic retry on version conflicts.
//
// This is a generic helper for API calls that use version-based optimistic locking instead of transactions.
// It automatically:
// 1. Fetches the current configuration version
// 2. Executes the provided function with that version
// 3. Retries on version conflicts (409/406 responses) by fetching the new version
// 4. Repeats up to 3 times
//
// This is commonly used for runtime API operations (server updates, map updates, etc.)
// that don't require a full transaction.
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - client: The DataplaneClient to use for version fetching
//   - fn: The function to execute that takes a version parameter and returns a result
//
// Example:
//
//	resp, err := ExecuteWithVersion(ctx, client, func(ctx context.Context, version int) (*http.Response, error) {
//	    params := &dataplaneapi.CreateServerParams{
//	        Version: &version,
//	    }
//	    return apiClient.CreateServer(ctx, "backend1", params, server)
//	})
func ExecuteWithVersion[T any](
	ctx context.Context,
	client *DataplaneClient,
	fn func(ctx context.Context, version int) (T, error),
) (T, error) {
	return ExecuteWithVersionAndLogger(ctx, client, fn, nil)
}

// ExecuteWithVersionAndLogger is like ExecuteWithVersion but accepts a custom logger for retry logging.
//
// This is useful when you want retry attempts logged with component-specific context.
// If logger is nil, no retry logging is performed.
func ExecuteWithVersionAndLogger[T any](
	ctx context.Context,
	client *DataplaneClient,
	fn func(ctx context.Context, version int) (T, error),
	logger *slog.Logger,
) (T, error) {
	config := RetryConfig{
		MaxAttempts: 3,
		RetryIf:     IsVersionConflict(),
		Backoff:     BackoffNone, // No delay for version conflicts - retry immediately
		Logger:      logger,
	}

	return WithRetry(ctx, config, func(attempt int) (T, error) {
		// Fetch current version on each attempt (including first)
		version, err := client.GetVersion(ctx)
		if err != nil {
			var zero T
			return zero, fmt.Errorf("getting version: %w", err)
		}

		// Execute the provided function with the current version
		return fn(ctx, int(version))
	})
}

// ExecuteWithVersionTimeout is like ExecuteWithVersion but allows specifying a custom retry configuration.
//
// This is useful when you need different retry behavior (more attempts, different backoff strategy, etc.).
func ExecuteWithVersionCustom[T any](
	ctx context.Context,
	client *DataplaneClient,
	config RetryConfig,
	fn func(ctx context.Context, version int) (T, error),
) (T, error) {
	// Ensure RetryIf is set to version conflict detection
	if config.RetryIf == nil {
		config.RetryIf = IsVersionConflict()
	}

	// Default to 3 attempts if not specified
	if config.MaxAttempts == 0 {
		config.MaxAttempts = 3
	}

	return WithRetry(ctx, config, func(attempt int) (T, error) {
		version, err := client.GetVersion(ctx)
		if err != nil {
			var zero T
			return zero, fmt.Errorf("getting version: %w", err)
		}

		return fn(ctx, int(version))
	})
}

// ExecuteWithExponentialBackoff is a convenience wrapper that uses exponential backoff.
//
// This is useful for operations that may experience transient version conflicts
// and benefit from spacing out retry attempts.
func ExecuteWithExponentialBackoff[T any](
	ctx context.Context,
	client *DataplaneClient,
	baseDelay time.Duration,
	fn func(ctx context.Context, version int) (T, error),
) (T, error) {
	config := RetryConfig{
		MaxAttempts: 3,
		RetryIf:     IsVersionConflict(),
		Backoff:     BackoffExponential,
		BaseDelay:   baseDelay,
	}

	return ExecuteWithVersionCustom(ctx, client, config, fn)
}
