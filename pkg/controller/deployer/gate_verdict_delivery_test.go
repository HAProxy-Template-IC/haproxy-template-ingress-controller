// Copyright 2026 Philipp Hossner
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

package deployer

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
)

func TestScheduler_VerdictSupersededBeforeDelivery(t *testing.T) {
	for _, pinned := range []bool{false, true} {
		for _, verdict := range []struct {
			name    string
			ok      bool
			refused bool
		}{
			{name: "pass", ok: true},
			{name: "refusal", refused: true},
			{name: "check-error"},
		} {
			name := verdict.name
			if pinned {
				name += "-pinned"
			}
			t.Run(name, func(t *testing.T) {
				var logs bytes.Buffer
				logger := slog.New(slog.NewTextHandler(&logs, nil))
				scheduler := newDeploymentScheduler(testutil.NewTestBus(), logger, 0, time.Second)
				ctx := context.Background()
				scheduler.handleEvent(ctx, renderEvent("accepted"))
				scheduler.handleEvent(ctx, gateEvent("accepted", true, false, true, ""))
				accepted := scheduler.acceptedRender
				scheduler.gatePinned = pinned

				scheduler.handleEvent(ctx, renderEvent("old"))
				delayed := gateEvent("old", verdict.ok, verdict.refused, true, "")
				scheduler.handleEvent(ctx, renderEvent("new"))
				rendered := scheduler.lastRenderedOccurrence
				validated := scheduler.lastValidatedOccurrence
				correlationID := scheduler.lastCorrelationID
				pending := &scheduledDeployment{occurrence: rendered}
				scheduler.state.pending = pending
				revision := scheduler.workRevision
				logs.Reset()

				scheduler.handleEvent(ctx, delayed)

				assert.Empty(t, logs.String(), "an authentic verdict can become stale before delivery")
				assert.Equal(t, pinned, scheduler.gatePinned)
				assert.Same(t, rendered, scheduler.lastRenderedOccurrence)
				assert.Same(t, validated, scheduler.lastValidatedOccurrence)
				assert.Same(t, accepted, scheduler.acceptedRender)
				assert.Same(t, pending, scheduler.state.pending)
				assert.Equal(t, correlationID, scheduler.lastCorrelationID)
				assert.Equal(t, revision, scheduler.workRevision)

				scheduler.handleEvent(ctx, gateEvent("new", true, false, true, ""))
				require.NotNil(t, scheduler.acceptedRender)
				assert.False(t, scheduler.gatePinned)
				assert.Same(t, rendered, scheduler.acceptedRender.occurrence)
			})
		}
	}
}

func TestScheduler_UnauthenticatedVerdictRemainsAnError(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	scheduler := newDeploymentScheduler(testutil.NewTestBus(), logger, 0, time.Second)
	scheduler.handleEvent(context.Background(), &events.RenderGateCompletedEvent{OK: true, Newest: true})
	assert.Contains(t, logs.String(), "level=ERROR")
	assert.Contains(t, logs.String(), "without exact deployment identity")
	assert.Nil(t, scheduler.acceptedRender)
}
