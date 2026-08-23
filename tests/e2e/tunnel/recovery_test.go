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
	"errors"
	"testing"
	"time"
)

var errEstablish = errors.New("boom")

func fastRecoveryConfig() RecoveryConfig {
	return RecoveryConfig{MinBackoff: time.Millisecond, MaxBackoff: 4 * time.Millisecond, Budget: time.Second}
}

func TestReestablishRecoversAfterTransientFailures(t *testing.T) {
	calls := 0
	got := Reestablish(context.Background(), func(context.Context) error {
		calls++
		if calls < 3 {
			return errEstablish
		}
		return nil
	}, fastRecoveryConfig(), nil)

	if got != RecoveryRecovered {
		t.Fatalf("result = %v, want RecoveryRecovered", got)
	}
	if calls != 3 {
		t.Fatalf("establish called %d times, want 3", calls)
	}
}

func TestReestablishGivesUpAtBudget(t *testing.T) {
	// A tunnel that never comes back must stop at the budget, not retry forever.
	cfg := RecoveryConfig{MinBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond, Budget: 20 * time.Millisecond}
	got := Reestablish(context.Background(), func(context.Context) error {
		return errEstablish
	}, cfg, nil)

	if got != RecoveryBudgetExceeded {
		t.Fatalf("result = %v, want RecoveryBudgetExceeded", got)
	}
}

func TestReestablishStopsWhenContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := Reestablish(ctx, func(context.Context) error {
		t.Fatal("establish must not run once ctx is already done")
		return nil
	}, fastRecoveryConfig(), nil)

	if got != RecoveryCtxDone {
		t.Fatalf("result = %v, want RecoveryCtxDone", got)
	}
}

func TestReestablishReportsEachFailure(t *testing.T) {
	var events int
	Reestablish(context.Background(), func(context.Context) error {
		if events < 2 {
			return errEstablish
		}
		return nil
	}, fastRecoveryConfig(), func(string) { events++ })

	if events != 2 {
		t.Fatalf("onEvent fired %d times, want 2 (one per failed attempt)", events)
	}
}

func TestNextRecoveryBackoffIsCappedAndMonotonic(t *testing.T) {
	const minB, maxB = 250 * time.Millisecond, 5 * time.Second
	cur := nextRecoveryBackoff(0, minB, maxB)
	if cur != minB {
		t.Fatalf("first backoff = %v, want %v", cur, minB)
	}
	prev := cur
	for i := 0; i < 20; i++ {
		cur = nextRecoveryBackoff(cur, minB, maxB)
		if cur < prev {
			t.Fatalf("backoff decreased %v -> %v", prev, cur)
		}
		if cur > maxB {
			t.Fatalf("backoff %v exceeded cap %v", cur, maxB)
		}
		prev = cur
	}
	if cur != maxB {
		t.Fatalf("backoff never reached the cap: %v", cur)
	}
}
