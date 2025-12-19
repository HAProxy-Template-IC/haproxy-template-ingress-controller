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

package metrics

import (
	"context"
	"sync"
	"time"

	"haproxy-template-ic/pkg/controller/events"
	pkgevents "haproxy-template-ic/pkg/events"
)

// Component is an event-driven metrics collector.
//
// Subscribes to controller events and updates metrics via the Metrics struct.
// This is an event adapter that bridges domain events to Prometheus metrics.
//
// IMPORTANT: Instance-based, created fresh per application iteration.
// When the iteration ends (context cancelled), the component stops and
// the metrics it was updating become eligible for garbage collection.
//
// Lifecycle: NewComponent() → Start(ctx)
//   - Start() must be called (in a goroutine) before eventBus.Start()
//   - This ensures the component receives all buffered startup events
type Component struct {
	metrics        *Metrics
	eventBus       *pkgevents.EventBus
	resourceCounts map[string]int // Tracks current resource counts

	// Leader election tracking
	becameLeaderAt time.Time // When this replica became leader (zero if not leader)

	// Initialization state (guarded by initOnce)
	initOnce  sync.Once
	eventChan <-chan pkgevents.Event
}

// New creates a new metrics component that listens to events.
//
// Parameters:
//   - metrics: The Metrics instance to update (created with metrics.NewMetrics)
//   - eventBus: The EventBus to subscribe to for events
//
// Usage:
//
//	registry := prometheus.NewRegistry()
//	metrics := metrics.NewMetrics(registry)
//	component := metrics.New(metrics, eventBus)
//	go component.Start(ctx)  // Subscribe and process events in background
//	eventBus.Start()         // Release buffered events
func New(metrics *Metrics, eventBus *pkgevents.EventBus) *Component {
	return &Component{
		metrics:        metrics,
		eventBus:       eventBus,
		resourceCounts: make(map[string]int),
	}
}

// Start subscribes to the EventBus and begins processing events.
//
// This method:
// 1. Subscribes to the EventBus (exactly once, thread-safe)
// 2. Starts the event processing loop
//
// IMPORTANT: Call this (in a goroutine) BEFORE bus.Start() to ensure the component
// receives all buffered startup events. The subscription happens immediately when
// this method is called, before the event loop starts.
//
// This method blocks until the context is cancelled.
func (c *Component) Start(ctx context.Context) error {
	// Subscribe to EventBus exactly once (thread-safe)
	c.initOnce.Do(func() {
		c.eventChan = c.eventBus.Subscribe(200) // Large buffer for high-frequency metrics
	})

	// Event processing loop
	for {
		select {
		case event := <-c.eventChan:
			c.handleEvent(event)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Metrics returns the underlying Metrics instance for direct access.
//
// This allows other components (like webhook) to record metrics directly
// without going through the event bus.
func (c *Component) Metrics() *Metrics {
	return c.metrics
}

// handleEvent processes individual events and updates corresponding metrics.
func (c *Component) handleEvent(event pkgevents.Event) {
	// Record every event for total events metric
	c.metrics.RecordEvent()

	// Handle specific event types
	switch e := event.(type) {
	// Reconciliation events
	case *events.ReconciliationCompletedEvent:
		durationSeconds := float64(e.DurationMs) / 1000.0
		c.metrics.RecordReconciliation(durationSeconds, true)

	case *events.ReconciliationFailedEvent:
		c.metrics.RecordReconciliation(0, false)

	// Deployment events
	case *events.DeploymentCompletedEvent:
		durationSeconds := float64(e.DurationMs) / 1000.0
		// Consider deployment successful if at least some instances succeeded
		success := e.Succeeded > 0
		c.metrics.RecordDeployment(durationSeconds, success)

	case *events.InstanceDeploymentFailedEvent:
		// Record individual instance failures
		c.metrics.RecordDeployment(0, false)

	// Validation events
	case *events.ValidationCompletedEvent:
		c.metrics.RecordValidation(true)

	case *events.ValidationFailedEvent:
		c.metrics.RecordValidation(false)

	// Validation test events
	case *events.ValidationTestsCompletedEvent:
		durationSeconds := float64(e.DurationMs) / 1000.0
		c.metrics.RecordValidationTests(e.TotalTests, e.PassedTests, e.FailedTests, durationSeconds)

	// Resource events - initialize counts from IndexSynchronizedEvent
	case *events.IndexSynchronizedEvent:
		// Initialize all resource counts from the synchronized index
		for resourceType, count := range e.ResourceCounts {
			c.resourceCounts[resourceType] = count
			c.metrics.SetResourceCount(resourceType, count)
		}

	// Resource events - update counts from ResourceIndexUpdatedEvent
	case *events.ResourceIndexUpdatedEvent:
		// Skip initial sync events - we'll get the totals from IndexSynchronizedEvent
		if e.ChangeStats.IsInitialSync {
			return
		}

		// Apply deltas to tracked count
		currentCount := c.resourceCounts[e.ResourceTypeName]
		newCount := currentCount + e.ChangeStats.Created - e.ChangeStats.Deleted
		c.resourceCounts[e.ResourceTypeName] = newCount
		c.metrics.SetResourceCount(e.ResourceTypeName, newCount)

	// Leader election events
	case *events.BecameLeaderEvent:
		c.becameLeaderAt = e.Timestamp()
		c.metrics.SetIsLeader(true)
		c.metrics.RecordLeadershipTransition()

	case *events.LostLeadershipEvent:
		c.metrics.SetIsLeader(false)
		c.metrics.RecordLeadershipTransition()

		// Record time spent as leader
		if !c.becameLeaderAt.IsZero() {
			timeAsLeader := e.Timestamp().Sub(c.becameLeaderAt)
			c.metrics.AddTimeAsLeader(timeAsLeader.Seconds())
			c.becameLeaderAt = time.Time{} // Reset
		}
	}
}
