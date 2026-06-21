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
	// BackoffExponential applies exponentially increasing delays between retries.
	// It is the only strategy production uses; any other value (including the
	// zero value) means no delay.
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
	// Default: zero value (no delay between retries).
	Backoff BackoffStrategy

	// BaseDelay is the initial delay for BackoffExponential (doubles each retry).
	// Default: 100ms
	BaseDelay time.Duration

	// Logger for retry attempts. If nil, no logging is performed.
	Logger *slog.Logger
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

// IsReloadInProgress returns a RetryCondition that retries a runtime apply that
// failed because HAProxy was mid-reload. While the dataplaneapi drives a reload
// its master socket is briefly unavailable, so runtime commands fail transiently
// with signatures like:
//
//   - "cannot execute SetServerState: ... haproxy-master.sock: connect: connection refused"
//   - "runtime server '<be>/<srv>' not found" (the runtime view is reloading)
//
// A reload completes in tens-to-low-hundreds of ms, so a short bounded retry
// lets the runtime change land right after it — instead of leaving the new
// slot unset until the next reconcile, which is the rolling-restart gap that
// produced 503s under parallel-test reload churn. Version conflicts (409) and
// other genuine 4xx do NOT match these markers, so they fall through to the
// caller unchanged.
func IsReloadInProgress() RetryCondition {
	return func(err error) bool {
		if err == nil {
			return false
		}
		return containsAny(err.Error(),
			"connection refused",
			"cannot execute",
			"master.sock",
			"not found",
		)
	}
}

// reloadInProgressTimeout bounds how long a runtime apply keeps retrying across
// a concurrent HAProxy reload before giving up to the scheduled deploy. A reload
// re-execs the master (src/mworker.c mworker_reexec → execvp), which drops the
// -S master CLI socket only for the re-exec + worker handoff — well under a
// second in practice — so 2s is generous headroom.
const reloadInProgressTimeout = 2 * time.Second

// retryWhileReloadInProgress re-runs fn with NO backoff — the dataplaneapi HTTP
// round-trip is the natural spacing — while fn keeps failing with a reload-in-
// progress signature, until fn succeeds, returns any other error, ctx is
// cancelled, or reloadInProgressTimeout elapses.
//
// While HAProxy re-execs its master on reload, mworker_cli_proxy_stop() closes
// the -S master CLI socket listener, so a fresh connect gets ECONNREFUSED until
// the new master re-creates it. Retrying tightly re-lands the runtime change
// within ~one round-trip of the listener returning — inside option redispatch's
// rescue window — instead of waiting out a fixed backoff that can miss it. A
// fixed delay isn't needed: the round-trip (even a refused one goes
// controller→dataplaneapi→controller) already paces the loop. The scheduled
// deploy is the correctness floor if the budget is exhausted.
func retryWhileReloadInProgress(ctx context.Context, logger *slog.Logger, fn func() error) error {
	deadline := time.Now().Add(reloadInProgressTimeout)
	isReload := IsReloadInProgress()
	for attempt := 1; ; attempt++ {
		err := fn()
		if err == nil || !isReload(err) {
			return err
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			if logger != nil {
				logger.Debug("reload-in-progress retry exhausted; scheduled deploy will converge",
					"attempts", attempt, "error", err.Error())
			}
			return err
		}
		// Minimal pause so a completely-unreachable dataplaneapi (ECONNREFUSED
		// returns in µs, not paced by an HTTP round-trip) can't spin this loop at
		// CPU speed for the whole reloadInProgressTimeout budget. Under a normal
		// reload the round-trip dominates this sleep, so recovery latency is
		// unchanged.
		time.Sleep(2 * time.Millisecond)
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

// calculateBackoff calculates the delay before the next retry attempt. Only
// BackoffExponential delays; any other strategy (including the zero value)
// retries immediately.
func calculateBackoff(strategy BackoffStrategy, baseDelay time.Duration, attempt int) time.Duration {
	if strategy != BackoffExponential {
		return 0
	}
	// Exponential: baseDelay * 2^(attempt-1)
	// attempt 1 -> baseDelay
	// attempt 2 -> baseDelay * 2
	// attempt 3 -> baseDelay * 4
	multiplier := 1 << (attempt - 1)
	return baseDelay * time.Duration(multiplier)
}
