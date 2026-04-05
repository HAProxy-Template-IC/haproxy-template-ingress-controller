package client

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"syscall"
	"time"
)

// RetryCondition determines whether an error should trigger a retry.
type RetryCondition func(err error) bool

// BackoffStrategy determines the delay between retry attempts.
type BackoffStrategy string

const (
	// BackoffNone applies no delay between retries.
	BackoffNone BackoffStrategy = "none"
	// BackoffLinear applies a fixed delay between retries.
	BackoffLinear BackoffStrategy = "linear"
	// BackoffExponential applies exponentially increasing delays between retries.
	BackoffExponential BackoffStrategy = "exponential"
)

// RetryConfig configures retry behavior for operations.
type RetryConfig struct {
	// MaxAttempts is the maximum number of attempts (including the first one).
	// Must be >= 1. Default: 1 (no retries).
	MaxAttempts int

	// RetryIf determines whether to retry based on the error.
	// If nil, no retries are performed.
	RetryIf RetryCondition

	// Backoff strategy for delays between retries.
	// Default: BackoffNone
	Backoff BackoffStrategy

	// BaseDelay is the base delay for backoff strategies.
	// - For BackoffLinear: the fixed delay between retries
	// - For BackoffExponential: the initial delay (doubles each retry)
	// Default: 100ms
	BaseDelay time.Duration

	// Logger for retry attempts. If nil, no logging is performed.
	Logger *slog.Logger
}

// DefaultRetryConfig returns a RetryConfig with sensible defaults.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts: 1,
		RetryIf:     nil,
		Backoff:     BackoffNone,
		BaseDelay:   100 * time.Millisecond,
		Logger:      nil,
	}
}

// IsVersionConflict returns a RetryCondition that retries on version conflict errors.
func IsVersionConflict() RetryCondition {
	return func(err error) bool {
		_, ok := errors.AsType[*VersionConflictError](err)
		return ok
	}
}

// IsConnectionError returns a RetryCondition that retries on transient connection errors.
//
// This condition detects network-level connection failures such as:
//   - Connection refused (ECONNREFUSED) - server not yet accepting connections
//   - Connection reset (ECONNRESET) - connection lost during communication
//
// These errors are typically transient and resolve within a few seconds as
// services start up or network conditions stabilize.
//
// This condition does NOT retry on:
//   - HTTP errors (4xx, 5xx status codes)
//   - Authentication failures
//   - Parsing errors
//   - Context cancellation
//
// Example:
//
//	retryConfig := RetryConfig{
//	    MaxAttempts: 3,
//	    RetryIf:     IsConnectionError(),
//	    Backoff:     BackoffExponential,
//	    BaseDelay:   100 * time.Millisecond,
//	}
//	result, err := WithRetry(ctx, retryConfig, func(attempt int) (*Result, error) {
//	    return fetchFromAPI(ctx)
//	})
func IsConnectionError() RetryCondition {
	return func(err error) bool {
		if err == nil {
			return false
		}

		// Check for net.OpError (network operation errors)
		if opErr, ok := errors.AsType[*net.OpError](err); ok {
			// Retry on connection refused (server not ready yet)
			if errors.Is(opErr.Err, syscall.ECONNREFUSED) {
				return true
			}
			// Retry on connection reset (connection lost)
			if errors.Is(opErr.Err, syscall.ECONNRESET) {
				return true
			}
		}

		// Check error message for common connection failure patterns
		// This handles wrapped errors where the underlying net.OpError
		// might not be directly accessible via errors.As
		errMsg := err.Error()
		return containsAny(errMsg,
			"connection refused",
			"connection reset",
			"dial tcp",
			"no such host",
		)
	}
}

// containsAny checks if the string s contains any of the substrings.
func containsAny(s string, substrings ...string) bool {
	for _, substr := range substrings {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}

// WithRetry executes fn with automatic retry logic based on config.
//
// The function fn is called with the current attempt number (1-indexed).
// If fn returns an error and config.RetryIf returns true, the operation
// is retried up to config.MaxAttempts times.
//
// Example:
//
//	config := RetryConfig{
//	    MaxAttempts: 3,
//	    RetryIf:     IsVersionConflict(),
//	    Backoff:     BackoffExponential,
//	    BaseDelay:   100 * time.Millisecond,
//	    Logger:      logger,
//	}
//	result, err := WithRetry(ctx, config, func(attempt int) (*Result, error) {
//	    return doOperation(ctx, attempt)
//	})
func WithRetry[T any](ctx context.Context, config RetryConfig, fn func(attempt int) (T, error)) (T, error) {
	var zero T

	// Validate config
	if config.MaxAttempts < 1 {
		config.MaxAttempts = 1
	}
	if config.BaseDelay == 0 {
		config.BaseDelay = 100 * time.Millisecond
	}

	var lastErr error
	for attempt := 1; attempt <= config.MaxAttempts; attempt++ {
		// Check context cancellation before each attempt
		select {
		case <-ctx.Done():
			return zero, fmt.Errorf("retry cancelled: %w", ctx.Err())
		default:
		}

		// Execute the function
		result, err := fn(attempt)
		if err == nil {
			return result, nil
		}

		lastErr = err

		// Check if we should retry
		shouldRetry := config.RetryIf != nil && config.RetryIf(err)
		isLastAttempt := attempt >= config.MaxAttempts

		if !shouldRetry || isLastAttempt {
			// Don't retry, return the error
			return zero, err
		}

		// Log retry attempt
		if config.Logger != nil {
			config.Logger.Warn("Operation failed, retrying",
				"attempt", attempt,
				"max_attempts", config.MaxAttempts,
				"error", err.Error())
		}

		// Apply backoff delay before next retry
		delay := calculateBackoff(config.Backoff, config.BaseDelay, attempt)
		if delay > 0 {
			select {
			case <-ctx.Done():
				return zero, fmt.Errorf("retry cancelled during backoff: %w", ctx.Err())
			case <-time.After(delay):
				// Continue to next attempt
			}
		}
	}

	// Should not reach here, but return lastErr for safety
	return zero, lastErr
}

// calculateBackoff calculates the delay before the next retry attempt.
func calculateBackoff(strategy BackoffStrategy, baseDelay time.Duration, attempt int) time.Duration {
	switch strategy {
	case BackoffNone:
		return 0
	case BackoffLinear:
		return baseDelay
	case BackoffExponential:
		// Exponential: baseDelay * 2^(attempt-1)
		// attempt 1 -> baseDelay
		// attempt 2 -> baseDelay * 2
		// attempt 3 -> baseDelay * 4
		multiplier := 1 << (attempt - 1)
		return baseDelay * time.Duration(multiplier)
	default:
		return 0
	}
}
