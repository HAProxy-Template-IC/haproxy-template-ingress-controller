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
	"fmt"
	"log/slog"
	"sync"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/metrics"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

type criticalEventDeliveryError struct {
	drop busevents.DropInfo
}

type eventDropKey struct {
	subscriberName string
	eventType      string
}

type persistentEventDropMetrics struct {
	mu      sync.Mutex
	counts  map[eventDropKey]uint64
	current *metrics.Metrics
}

func (e *criticalEventDeliveryError) Error() string {
	return fmt.Sprintf(
		"critical event %q was dropped for subscriber %q (buffer size %d)",
		e.drop.EventType,
		e.drop.SubscriberName,
		e.drop.BufferSize,
	)
}

func newCriticalEventDropCallback(
	logger *slog.Logger,
	recordDrop func(subscriberName, eventType string),
	cancel context.CancelCauseFunc,
) busevents.DropCallback {
	var cancelOnce sync.Once

	return func(info busevents.DropInfo) {
		firstDrop := false
		cancelOnce.Do(func() {
			firstDrop = true
			err := &criticalEventDeliveryError{drop: info}
			cancel(err)
		})
		recordDrop(info.SubscriberName, info.EventType)
		if firstDrop {
			logger.Error("Critical event delivery failed; restarting controller iteration",
				"subscriber", info.SubscriberName,
				"event_type", info.EventType,
				"buffer_size", info.BufferSize,
				"subscribed_types", info.EventTypes,
			)
		}
	}
}

func (m *persistentEventDropMetrics) Attach(current *metrics.Metrics) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.current = current
	for key, count := range m.counts {
		current.AddEventDrops(key.subscriberName, key.eventType, count)
	}
}

func (m *persistentEventDropMetrics) Record(subscriberName, eventType string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.counts == nil {
		m.counts = make(map[eventDropKey]uint64)
	}
	key := eventDropKey{subscriberName: subscriberName, eventType: eventType}
	m.counts[key]++
	if m.current != nil {
		m.current.RecordEventDrop(subscriberName, eventType)
	}
}

func iterationContextError(ctx context.Context) error {
	return context.Cause(ctx)
}

func startEventBus(setup *componentSetup) error {
	if err := iterationContextError(setup.IterCtx); err != nil {
		return err
	}
	if !setup.Bus.StartContext(setup.IterCtx) {
		return iterationContextError(setup.IterCtx)
	}
	return iterationContextError(setup.IterCtx)
}
