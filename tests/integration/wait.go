//go:build integration

package integration

import (
	"context"
	"time"

	"haptic/tests/testutil"
)

// WaitConfig is an alias for testutil.WaitConfig for backward compatibility.
type WaitConfig = testutil.WaitConfig

// DefaultWaitConfig returns sensible defaults for wait operations.
func DefaultWaitConfig() WaitConfig {
	return testutil.DefaultWaitConfig()
}

// FastWaitConfig returns config optimized for quick conditions.
func FastWaitConfig() WaitConfig {
	return testutil.FastWaitConfig()
}

// SlowWaitConfig returns config for conditions that may take longer.
func SlowWaitConfig() WaitConfig {
	return testutil.SlowWaitConfig()
}

// WaitForCondition polls with exponential backoff until the condition returns true.
func WaitForCondition(ctx context.Context, cfg WaitConfig, condition func(context.Context) (bool, error)) error {
	return testutil.WaitForCondition(ctx, cfg, condition)
}

// WaitForConditionWithProgress is like WaitForCondition but calls a progress callback.
func WaitForConditionWithProgress(
	ctx context.Context,
	cfg WaitConfig,
	condition func(context.Context) (bool, error),
	onProgress func(attempt int, elapsed time.Duration, lastErr error),
) error {
	return testutil.WaitForConditionWithProgress(ctx, cfg, condition, onProgress)
}
