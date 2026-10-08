// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package testutil provides shared testing utilities for integration and acceptance tests.
package testutil

import (
	"context"
	"errors"
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

// FastWaitConfig is optimized for conditions expected to resolve quickly.
func FastWaitConfig() WaitConfig {
	return WaitConfig{
		InitialInterval: 50 * time.Millisecond,
		MaxInterval:     2 * time.Second,
		Timeout:         30 * time.Second,
		Multiplier:      1.5,
	}
}

// SlowWaitConfig is for conditions that may take longer to resolve.
func SlowWaitConfig() WaitConfig {
	return WaitConfig{
		InitialInterval: 200 * time.Millisecond,
		MaxInterval:     10 * time.Second,
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
	return WaitForConditionWithDescription(ctx, cfg, "", condition)
}

// waitTimeoutError formats the timeout error, omitting the attempt/elapsed
// detail when no description was given (the plain WaitForCondition case).
func waitTimeoutError(description string, attempt int, elapsed time.Duration, lastErr, ctxErr error) error {
	if description == "" {
		if lastErr != nil {
			return fmt.Errorf("timeout waiting for condition (last error: %w)", errors.Join(lastErr, ctxErr))
		}
		return fmt.Errorf("timeout waiting for condition: %w", ctxErr)
	}
	if lastErr != nil {
		return fmt.Errorf("timeout waiting for %s after %d attempts in %v (last error: %w)",
			description, attempt, elapsed, errors.Join(lastErr, ctxErr))
	}
	return fmt.Errorf("timeout waiting for %s after %d attempts in %v: %w",
		description, attempt, elapsed, ctxErr)
}

// WaitForConditionWithDescription is like WaitForCondition but includes a description
// in error messages for better debugging. This is the recommended function for most use cases.
func WaitForConditionWithDescription(
	ctx context.Context,
	cfg WaitConfig,
	description string,
	condition func(context.Context) (bool, error),
) error {
	return Poll(ctx, cfg, description, func(ctx context.Context) (PollResult, error) {
		done, err := condition(ctx)
		if done {
			return PollSucceeded, nil
		}
		return PollPending, err
	})
}
