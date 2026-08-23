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

//go:build e2e

package tunnel

import (
	"context"
	"fmt"
	"time"
)

// RecoveryResult reports how Reestablish stopped.
type RecoveryResult int

const (
	// RecoveryRecovered means establish finally returned nil.
	RecoveryRecovered RecoveryResult = iota
	// RecoveryCtxDone means ctx ended before a success.
	RecoveryCtxDone
	// RecoveryBudgetExceeded means Budget elapsed with every attempt failing.
	RecoveryBudgetExceeded
)

// RecoveryConfig bounds how Reestablish retries a failed tunnel handshake.
type RecoveryConfig struct {
	MinBackoff time.Duration // pause after the first failed attempt
	MaxBackoff time.Duration // cap on the exponential backoff
	Budget     time.Duration // give up once this much has elapsed with no success
}

// Reestablish calls establish until it returns nil, ctx ends, or the elapsed
// time reaches Budget — whichever comes first. Between failed attempts it waits
// an exponentially increasing backoff capped at MaxBackoff, and reports each
// attempt's error through onEvent for the caller to log.
//
// The watchdog tears a stalled kubectl port-forward down; this re-opens it.
// Bounding the retries with a budget turns "the apiserver port-forward path is
// wedged" from an infinite silent retry (a later test then fails on an unrelated
// poll timeout) into one attributable failure the caller can surface.
func Reestablish(ctx context.Context, establish func(context.Context) error, cfg RecoveryConfig, onEvent func(string)) RecoveryResult {
	start := time.Now()
	backoff := time.Duration(0)
	for {
		if ctx.Err() != nil {
			return RecoveryCtxDone
		}
		if err := establish(ctx); err == nil {
			return RecoveryRecovered
		} else if onEvent != nil {
			onEvent(fmt.Sprintf("tunnel re-establish attempt failed: %v", err))
		}
		if ctx.Err() != nil {
			return RecoveryCtxDone
		}
		if time.Since(start) >= cfg.Budget {
			return RecoveryBudgetExceeded
		}
		backoff = nextRecoveryBackoff(backoff, cfg.MinBackoff, cfg.MaxBackoff)
		if !recoverySleep(ctx, backoff) {
			return RecoveryCtxDone
		}
	}
}

func nextRecoveryBackoff(current, minBackoff, maxBackoff time.Duration) time.Duration {
	if current <= 0 {
		return minBackoff
	}
	if doubled := current * 2; doubled < maxBackoff {
		return doubled
	}
	return maxBackoff
}

// recoverySleep waits out delay, reporting false when ctx ended first.
func recoverySleep(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
