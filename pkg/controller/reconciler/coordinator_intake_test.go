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

package reconciler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pipeline"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

type blockedIntakePipeline struct {
	started chan struct{}
}

func (p *blockedIntakePipeline) Execute(ctx context.Context, _ stores.StoreProvider, _ rendercontext.RenderMode,
	_ ...rendercontext.Option,
) (*pipeline.PipelineResult, error) {
	close(p.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestCoordinatorDrainsTriggersDuringRender(t *testing.T) {
	bus, logger := testutil.NewTestBusAndLogger()
	executor := &blockedIntakePipeline{started: make(chan struct{})}
	coordinator := NewCoordinator(&CoordinatorConfig{
		EventBus: bus, Pipeline: executor, StoreProvider: stores.NewRealStoreProvider(nil), Logger: logger,
	})
	bus.Start()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- coordinator.Start(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			assert.NoError(t, err)
		case <-time.After(testutil.EventTimeout):
			t.Error("coordinator did not stop")
		}
		assert.Equal(t, 0, bus.SubscriberCount())
	})
	select {
	case <-coordinator.SubscriptionReady():
	case <-time.After(testutil.EventTimeout):
		t.Fatal("coordinator did not subscribe")
	}
	bus.Publish(events.NewReconciliationTriggeredEvent("blocked", true))
	select {
	case <-executor.started:
	case <-time.After(testutil.EventTimeout):
		t.Fatal("pipeline did not start")
	}
	for range 32 {
		for range 128 {
			bus.Publish(events.NewReconciliationTriggeredEvent("resource_change", true))
		}
		require.Eventually(t, func() bool { return len(coordinator.eventChan) == 0 },
			testutil.EventTimeout, time.Millisecond, "rendering must not block subscription intake")
	}
	assert.Zero(t, bus.DroppedEventsCritical())
}
