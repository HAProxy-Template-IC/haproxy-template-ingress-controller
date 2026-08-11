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

package configpublisher

import (
	"context"
	"testing"
	"time"
)

func TestDelayedSignalsStopCancelsPendingCallbacks(t *testing.T) {
	signals := newDelayedSignals()
	target := make(chan struct{}, 1)
	signals.Schedule(context.Background(), time.Hour, target)
	signals.Stop()
	signals.Stop()

	select {
	case <-target:
		t.Fatal("stopped delayed signal delivered a retry wakeup")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestDelayedSignalsObserveContextCancellation(t *testing.T) {
	signals := newDelayedSignals()
	defer signals.Stop()
	target := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	signals.Schedule(ctx, 20*time.Millisecond, target)
	cancel()

	select {
	case <-target:
		t.Fatal("cancelled delayed signal delivered a retry wakeup")
	case <-time.After(50 * time.Millisecond):
	}
}
