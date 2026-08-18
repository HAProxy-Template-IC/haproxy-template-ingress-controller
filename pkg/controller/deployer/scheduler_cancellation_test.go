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
	"context"
	"testing"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
)

func TestDeployLoopCancelledBeforeImmediatelyDuePendingDoesNotPublish(t *testing.T) {
	bus := testutil.NewTestBus()
	scheduledCh := bus.SubscribeTypes("cancelled-scheduler-watcher", 1, events.EventTypeDeploymentScheduled)
	bus.Start()

	s := newDeploymentScheduler(bus, testutil.NewTestLogger(), 0, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	s.ctx = ctx
	initLoopChannels(s)
	s.scheduleOrQueue(ctx, "config", nil, nil, oneEndpoint(), "config_validation", "correlation", nil, true, "checksum", nil, "")

	cancel()
	go s.runDeployLoop(ctx)

	select {
	case <-s.loopDone:
	case <-time.After(testutil.LongTimeout):
		t.Fatal("deploy loop did not stop after cancellation")
	}
	testutil.AssertNoEvent[*events.DeploymentScheduledEvent](t, scheduledCh, 50*time.Millisecond)
}
