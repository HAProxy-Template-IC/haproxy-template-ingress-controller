//go:build integration

package integration

import (
	"context"
	"fmt"
	"time"
)

// WaitConfig configures wait behavior with exponential backoff.
type WaitConfig struct {
	// InitialInterval is the starting interval between retry attempts.
	InitialInterval time.Duration

	// MaxInterval is the maximum interval between retry attempts.
	// The backoff will not exceed this value.
	MaxInterval time.Duration

	// Timeout is the total time allowed for the wait operation.
	Timeout time.Duration

	// Multiplier is applied to the interval after each attempt.
	// For example, 2.0 doubles the interval each time.
	Multiplier float64
}

// DefaultWaitConfig provides sensible defaults for wait operations.
func DefaultWaitConfig() WaitConfig {
	return WaitConfig{
		InitialInterval: 100 * time.Millisecond,
		MaxInterval:     5 * time.Second,
		Timeout:         5 * time.Minute,
		Multiplier:      2.0,
	}
}

// WaitForCondition polls with exponential backoff until the condition returns true
// or the timeout is exceeded. The condition function should return (true, nil) when
// the condition is satisfied, (false, nil) to continue waiting, or (false, error)
// to record the last error (but continue waiting).
//
// The function checks the condition immediately before waiting, so if the condition
// is already satisfied, it returns without delay.
func WaitForCondition(ctx context.Context, cfg WaitConfig, condition func(context.Context) (bool, error)) error {
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	interval := cfg.InitialInterval
	var lastErr error

	// Check immediately before first wait
	if done, err := condition(ctx); done {
		return nil
	} else if err != nil {
		lastErr = err
	}

	for {
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return fmt.Errorf("timeout waiting for condition (last error: %w)", lastErr)
			}
			return fmt.Errorf("timeout waiting for condition: %w", ctx.Err())

		case <-time.After(interval):
			done, err := condition(ctx)
			if done {
				return nil
			}
			if err != nil {
				lastErr = err
			}

			// Exponential backoff
			interval = time.Duration(float64(interval) * cfg.Multiplier)
			if interval > cfg.MaxInterval {
				interval = cfg.MaxInterval
			}
		}
	}
}

// WaitForConditionWithProgress is like WaitForCondition but calls a progress
// callback on each retry attempt. This is useful for logging progress during
// long waits.
func WaitForConditionWithProgress(
	ctx context.Context,
	cfg WaitConfig,
	condition func(context.Context) (bool, error),
	onProgress func(attempt int, elapsed time.Duration, lastErr error),
) error {
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	interval := cfg.InitialInterval
	var lastErr error
	attempt := 0
	start := time.Now()

	// Check immediately before first wait
	attempt++
	if done, err := condition(ctx); done {
		return nil
	} else if err != nil {
		lastErr = err
	}

	for {
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return fmt.Errorf("timeout waiting for condition after %d attempts (last error: %w)", attempt, lastErr)
			}
			return fmt.Errorf("timeout waiting for condition after %d attempts: %w", attempt, ctx.Err())

		case <-time.After(interval):
			attempt++
			done, err := condition(ctx)
			if done {
				return nil
			}
			if err != nil {
				lastErr = err
			}

			if onProgress != nil {
				onProgress(attempt, time.Since(start), lastErr)
			}

			// Exponential backoff
			interval = time.Duration(float64(interval) * cfg.Multiplier)
			if interval > cfg.MaxInterval {
				interval = cfg.MaxInterval
			}
		}
	}
}
