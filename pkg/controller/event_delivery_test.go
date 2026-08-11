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

package controller

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	promtestutil "github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	controllermetrics "gitlab.com/haproxy-haptic/haptic/pkg/controller/metrics"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

type deliveryTestEvent struct {
	timestamp time.Time
}

func (e deliveryTestEvent) EventType() string { return "test.delivery" }
func (e deliveryTestEvent) Timestamp() time.Time {
	return e.timestamp
}

func TestCriticalEventDropCancelsIterationOnce(t *testing.T) {
	ctx, cancelCause := context.WithCancelCause(t.Context())
	var cancellations atomic.Int64
	var recordedDrops atomic.Int64
	var recordedAfterCancellation atomic.Bool
	cancel := func(err error) {
		cancellations.Add(1)
		cancelCause(err)
	}

	bus := busevents.NewEventBus(1)
	bus.Subscribe("blocked", 1)
	bus.SetDropCallback(newCriticalEventDropCallback(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		func(_, _ string) {
			recordedDrops.Add(1)
			recordedAfterCancellation.Store(context.Cause(ctx) != nil)
		},
		cancel,
	))
	bus.Start()

	event := deliveryTestEvent{timestamp: time.Now()}
	bus.Publish(event)
	bus.Publish(event)
	bus.Publish(event)

	require.Error(t, context.Cause(ctx))
	var deliveryErr *criticalEventDeliveryError
	require.ErrorAs(t, context.Cause(ctx), &deliveryErr)
	assert.Equal(t, "test.delivery", deliveryErr.drop.EventType)
	assert.Equal(t, "blocked", deliveryErr.drop.SubscriberName)
	assert.Equal(t, int64(1), cancellations.Load())
	assert.Equal(t, int64(2), recordedDrops.Load())
	assert.True(t, recordedAfterCancellation.Load())
}

func TestLossyEventDropDoesNotCancelIteration(t *testing.T) {
	ctx, cancelCause := context.WithCancelCause(t.Context())
	var recordedDrops atomic.Int64

	bus := busevents.NewEventBus(1)
	bus.SubscribeLossy("observability", 1)
	bus.SetDropCallback(newCriticalEventDropCallback(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		func(_, _ string) { recordedDrops.Add(1) },
		cancelCause,
	))
	bus.Start()

	event := deliveryTestEvent{timestamp: time.Now()}
	bus.Publish(event)
	bus.Publish(event)

	assert.NoError(t, context.Cause(ctx))
	assert.Equal(t, int64(0), recordedDrops.Load())
	assert.Equal(t, uint64(1), bus.DroppedEventsObservability())
}

func TestStartEventBusReturnsReplayDrop(t *testing.T) {
	iterCtx, cancelCause := context.WithCancelCause(t.Context())
	_, groupCtx := errgroup.WithContext(iterCtx)
	bus := busevents.NewEventBus(2)
	bus.Subscribe("blocked", 1)
	bus.SetDropCallback(newCriticalEventDropCallback(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		func(_, _ string) {},
		cancelCause,
	))

	event := deliveryTestEvent{timestamp: time.Now()}
	bus.Publish(event)
	bus.Publish(event)

	err := startEventBus(&componentSetup{Bus: bus, IterCtx: groupCtx})
	var deliveryErr *criticalEventDeliveryError
	require.ErrorAs(t, err, &deliveryErr)
	assert.Equal(t, "blocked", deliveryErr.drop.SubscriberName)
}

func TestStartEventBusDoesNotReplayAfterIterationFailure(t *testing.T) {
	iterCtx, cancelCause := context.WithCancelCause(t.Context())
	bus := busevents.NewEventBus(1)
	received := bus.Subscribe("receiver", 1)
	bus.Publish(deliveryTestEvent{timestamp: time.Now()})
	failure := errors.New("required component failed")
	cancelCause(failure)

	err := startEventBus(&componentSetup{Bus: bus, IterCtx: iterCtx})
	require.ErrorIs(t, err, failure)
	select {
	case event := <-received:
		t.Fatalf("replayed %T after iteration failure", event)
	default:
	}
}

func TestCriticalDropMetricsSurviveIterationRegistrySwap(t *testing.T) {
	totals := &persistentEventDropMetrics{}
	firstRegistry := prometheus.NewRegistry()
	totals.Attach(controllermetrics.NewMetrics(firstRegistry))
	totals.Record("blocked", "test.delivery")

	secondRegistry := prometheus.NewRegistry()
	totals.Attach(controllermetrics.NewMetrics(secondRegistry))
	require.NoError(t, promtestutil.GatherAndCompare(
		secondRegistry,
		strings.NewReader(`# HELP haptic_events_dropped_by_subscriber_total Events dropped per subscriber and event type
# TYPE haptic_events_dropped_by_subscriber_total counter
haptic_events_dropped_by_subscriber_total{event_type="test.delivery",subscriber="blocked"} 1
# HELP haptic_events_dropped_critical_total Events dropped from critical subscribers (alert if > 0)
# TYPE haptic_events_dropped_critical_total counter
haptic_events_dropped_critical_total 1
# HELP haptic_events_dropped_total Total number of events dropped due to full subscriber buffers
# TYPE haptic_events_dropped_total counter
haptic_events_dropped_total 1
`),
		"haptic_events_dropped_by_subscriber_total",
		"haptic_events_dropped_critical_total",
		"haptic_events_dropped_total",
	))
}

func TestCriticalPreStartDropTearsDownBeforeRetry(t *testing.T) {
	ctx, stop := context.WithCancel(t.Context())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	var attempts atomic.Int64
	var workerStopped atomic.Bool
	var cleanupObservedStop atomic.Bool

	err := runIterations(ctx, logger, 0, func() error {
		if attempts.Add(1) > 1 {
			assert.True(t, workerStopped.Load())
			assert.True(t, cleanupObservedStop.Load())
			stop()
			return nil
		}

		iterCtx, cancelCause := context.WithCancelCause(ctx)
		group, groupCtx := errgroup.WithContext(iterCtx)
		setup := &componentSetup{
			Bus:      busevents.NewEventBus(1),
			IterCtx:  groupCtx,
			Cancel:   func() { cancelCause(nil) },
			ErrGroup: group,
		}
		group.Go(func() error {
			<-groupCtx.Done()
			workerStopped.Store(true)
			return nil
		})
		setup.AddCleanup(func() { cleanupObservedStop.Store(workerStopped.Load()) })
		defer teardownIteration(setup, logger)

		setup.Bus.SetDropCallback(newCriticalEventDropCallback(logger, func(_, _ string) {}, cancelCause))
		event := deliveryTestEvent{timestamp: time.Now()}
		for range busevents.MaxPreStartBufferSize + 1 {
			setup.Bus.Publish(event)
		}
		return iterationContextError(groupCtx)
	})

	require.NoError(t, err)
	assert.Equal(t, int64(2), attempts.Load())
}
