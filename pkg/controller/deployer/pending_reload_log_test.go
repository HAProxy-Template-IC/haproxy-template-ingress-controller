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
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

const pendingReloadLogMessage = "Reloads pending on the fleet; following up when they fire"

// pendingReloadLogCount counts the pending-reload follow-up lines at level.
func pendingReloadLogCount(t *testing.T, buf *bytes.Buffer, level slog.Level) int {
	t.Helper()
	n := 0
	for _, line := range bytes.Split(buf.Bytes(), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var entry struct {
			Level string `json:"level"`
			Msg   string `json:"msg"`
		}
		require.NoError(t, json.Unmarshal(line, &entry))
		if entry.Msg == pendingReloadLogMessage && entry.Level == level.String() {
			n++
		}
	}
	return n
}

func pendingReloadCompletion(s *DeploymentScheduler, pendingReloads int) *events.DeploymentCompletedEvent {
	return completionForActiveDeployment(s, &events.DeploymentResult{
		Total: 2, Succeeded: 2,
		PendingReloads:     pendingReloads,
		PendingReloadUntil: time.Now().Add(time.Second),
	})
}

func TestSchedulePendingReloadFollowUp_RepeatedStateLogsAtDebug(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	scheduler := newDeploymentScheduler(
		busevents.NewEventBus(10), logger, 100*time.Millisecond, 30*time.Second,
	)
	t.Cleanup(func() {
		scheduler.schedulerMutex.Lock()
		scheduler.stopRetryTimerLocked()
		scheduler.schedulerMutex.Unlock()
	})

	event := pendingReloadCompletion(scheduler, 3)
	scheduler.schedulePendingReloadFollowUp(event)
	scheduler.schedulePendingReloadFollowUp(event)
	scheduler.schedulePendingReloadFollowUp(event)

	assert.Equal(t, 1, pendingReloadLogCount(t, buf, slog.LevelInfo),
		"renders dispatched during one pending-reload window re-arm the follow-up per completion; the wait state is worth one Info, not one per render")
	assert.Equal(t, 2, pendingReloadLogCount(t, buf, slog.LevelDebug))

	scheduler.schedulePendingReloadFollowUp(pendingReloadCompletion(scheduler, 2))
	assert.Equal(t, 2, pendingReloadLogCount(t, buf, slog.LevelInfo),
		"a pod finishing its reload changes the wait state and is progress worth an Info")

	scheduler.clearPendingReloadLogState()
	scheduler.schedulePendingReloadFollowUp(pendingReloadCompletion(scheduler, 2))
	assert.Equal(t, 3, pendingReloadLogCount(t, buf, slog.LevelInfo),
		"a new pending window after a fully deployed completion starts at Info again")
}
