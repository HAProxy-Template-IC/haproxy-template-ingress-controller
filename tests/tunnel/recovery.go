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

package tunnel

import (
	"context"
	"fmt"
	"time"

	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
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

// Reestablish retries within Budget; attempt contexts end on return and must
// not own the recovered process.
func Reestablish(ctx context.Context, establish func(context.Context) error, cfg RecoveryConfig, onEvent func(string)) RecoveryResult {
	err := testutil.Poll(ctx, testutil.WaitConfig{Timeout: cfg.Budget, InitialInterval: cfg.MinBackoff, MaxInterval: cfg.MaxBackoff, Multiplier: 2}, "restore tunnel", func(attempt context.Context) (testutil.PollResult, error) {
		if err := establish(attempt); err != nil {
			if onEvent != nil {
				onEvent(fmt.Sprintf("tunnel re-establish attempt failed: %v", err))
			}
			return testutil.PollPending, err
		}
		return testutil.PollSucceeded, nil
	})
	if ctx.Err() != nil {
		return RecoveryCtxDone
	}
	if err != nil {
		return RecoveryBudgetExceeded
	}
	return RecoveryRecovered
}
