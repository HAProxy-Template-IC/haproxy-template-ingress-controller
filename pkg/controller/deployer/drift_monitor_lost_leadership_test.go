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

package deployer

import (
	"bytes"
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
)

type leadershipLogWriter struct {
	once    sync.Once
	handled chan struct{}
}

func (w *leadershipLogWriter) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte("Lost leadership, stopping drift timer")) {
		w.once.Do(func() { close(w.handled) })
	}
	return len(p), nil
}

func TestDriftPreventionMonitor_HandleLostLeadership(t *testing.T) {
	monitor := NewDriftPreventionMonitor(
		testutil.NewTestBus(),
		testutil.NewTestLogger(),
		testutil.LongTimeout,
	)

	monitor.resetDriftTimer()
	require.NotNil(t, monitor.driftTimer.Chan())

	monitor.handleEvent(events.NewLostLeadershipEvent("test-pod", "test-reason"))

	assert.Nil(t, monitor.driftTimer.Chan())
	require.NotPanics(t, monitor.handleLostLeadership)
	assert.Nil(t, monitor.driftTimer.Chan())
}

func TestDriftPreventionMonitor_RoutedLostLeadershipDoesNotTriggerOrRearm(t *testing.T) {
	bus := testutil.NewTestBus()
	driftEvents := bus.SubscribeTypes(
		"drift-loss-regression",
		10,
		events.EventTypeDriftPreventionTriggered,
	)
	writer := &leadershipLogWriter{handled: make(chan struct{})}
	logger := slog.New(slog.NewTextHandler(writer, nil))
	monitor := NewDriftPreventionMonitor(bus, logger, 20*time.Millisecond)
	bus.Start()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- monitor.Start(ctx)
	}()
	defer func() {
		cancel()
		select {
		case err := <-done:
			assert.NoError(t, err)
		case <-time.After(testutil.LongTimeout):
			t.Errorf("drift monitor did not stop")
		}
	}()

	select {
	case <-monitor.SubscriptionReady():
	case <-time.After(testutil.LongTimeout):
		t.Fatal("drift monitor subscription was not ready")
	}

	require.Equal(t, 1, bus.Publish(events.NewLostLeadershipEvent("test-pod", "test-reason")))
	select {
	case <-writer.handled:
	case <-time.After(testutil.LongTimeout):
		t.Fatal("lost leadership event was not handled")
	}
	testutil.DrainChannel(driftEvents)

	testutil.AssertNoEvent[*events.DriftPreventionTriggeredEvent](
		t,
		driftEvents,
		testutil.NoEventTimeout,
	)
}
