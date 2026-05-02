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
	"crypto/x509"
	"encoding/pem"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/timeouts"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// ComponentName is the unique identifier for the metrics component.
const ComponentName = "metrics"

// Component is an event-driven metrics collector.
//
// Subscribes to controller events and updates metrics via the Metrics struct.
// This is an event adapter that bridges domain events to Prometheus metrics.
//
// IMPORTANT: Instance-based, created fresh per application iteration.
// When the iteration ends (context cancelled), the component stops and
// the metrics it was updating become eligible for garbage collection.
type Component struct {
	metrics        *Metrics
	eventBus       *busevents.EventBus
	eventChan      <-chan busevents.Event // Subscribed in constructor for proper startup synchronization
	resourceCounts map[string]int         // Tracks current resource counts

	// Leader election tracking
	becameLeaderAt time.Time // When this replica became leader (zero if not leader)

	// Queue wait tracking: correlationID → when ReconciliationTriggeredEvent was received
	triggeredAt map[string]time.Time
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
//	go component.Start(ctx)
//	eventBus.Start()
func New(metrics *Metrics, eventBus *busevents.EventBus) *Component {
	// Subscribe to EventBus during construction (before EventBus.Start())
	// This ensures proper startup synchronization without timing-based sleeps
	// Use typed subscription to only receive events we handle (reduces buffer pressure)
	eventChan := eventBus.SubscribeTypes(ComponentName, 200,
		events.EventTypeReconciliationCompleted,
		events.EventTypeReconciliationFailed,
		events.EventTypeReconciliationTriggered,
		events.EventTypeReconciliationStarted,
		events.EventTypeDeploymentCompleted,
		events.EventTypeInstanceDeploymentFailed,
		events.EventTypeValidationCompleted,
		events.EventTypeValidationFailed,
		events.EventTypeValidationTestsCompleted,
		events.EventTypeIndexSynchronized,
		events.EventTypeResourceIndexUpdated,
		events.EventTypeBecameLeader,
		events.EventTypeLostLeadership,
		events.EventTypeCertParsed,
		events.EventTypeHAProxyPodRejected,
	)

	return &Component{
		metrics:        metrics,
		eventBus:       eventBus,
		eventChan:      eventChan,
		resourceCounts: make(map[string]int),
		triggeredAt:    make(map[string]time.Time),
	}
}

// Start begins the metrics event processing loop.
//
// This method blocks until the context is cancelled.
// It also periodically updates the observability drop metric from EventBus stats.
func (c *Component) Start(ctx context.Context) error {
	// Ticker to poll EventBus stats for observability drops (every 5 seconds)
	ticker := time.NewTicker(timeouts.TickerPollInterval)
	defer ticker.Stop()

	for {
		select {
		case event := <-c.eventChan:
			c.handleEvent(event)
		case <-ticker.C:
			// Update observability drops from EventBus (not exposed via callback)
			c.metrics.SetObservabilityDrops(c.eventBus.DroppedEventsObservability())
			// Update parser cache stats
			hits, misses := parser.CacheStats()
			c.metrics.UpdateParserCacheStats(hits, misses)
			// Update subscriber count
			c.metrics.SetEventSubscribers(c.eventBus.SubscriberCount())
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
func (c *Component) handleEvent(event busevents.Event) {
	// Record every event for total events metric
	c.metrics.RecordEvent()

	// Handle specific event types
	switch e := event.(type) {
	case *events.ReconciliationTriggeredEvent:
		c.triggeredAt[e.CorrelationID()] = e.Timestamp()
	case *events.ReconciliationStartedEvent:
		c.handleReconciliationStarted(e)
	case *events.ReconciliationCompletedEvent:
		c.metrics.RecordReconciliation(msToSeconds(e.DurationMs), true)
	case *events.ReconciliationFailedEvent:
		delete(c.triggeredAt, e.CorrelationID()) // cleanup to prevent map growth
		c.metrics.RecordReconciliation(0, false)
	case *events.DeploymentCompletedEvent:
		c.metrics.RecordDeployment(msToSeconds(e.DurationMs), e.Succeeded > 0)
	case *events.InstanceDeploymentFailedEvent:
		c.metrics.RecordDeployment(0, false)
	case *events.ValidationCompletedEvent:
		c.metrics.RecordValidation(true)
	case *events.ValidationFailedEvent:
		c.metrics.RecordValidation(false)
	case *events.ValidationTestsCompletedEvent:
		c.metrics.RecordValidationTests(e.TotalTests, e.PassedTests, e.FailedTests, msToSeconds(e.DurationMs))
	case *events.IndexSynchronizedEvent:
		c.handleIndexSynchronized(e)
	case *events.ResourceIndexUpdatedEvent:
		c.handleResourceIndexUpdated(e)
	case *events.BecameLeaderEvent:
		c.becameLeaderAt = e.Timestamp()
		c.metrics.SetIsLeader(true)
		c.metrics.RecordLeadershipTransition()
	case *events.LostLeadershipEvent:
		c.handleLostLeadership(e)
	case *events.CertParsedEvent:
		c.handleCertParsed(e)
	case *events.HAProxyPodRejectedEvent:
		c.metrics.RecordHAProxyPodRejected(e.Reason)
	}
}

func (c *Component) handleReconciliationStarted(e *events.ReconciliationStartedEvent) {
	if t, ok := c.triggeredAt[e.CorrelationID()]; ok {
		c.metrics.RecordQueueWait(time.Since(t).Seconds())
		delete(c.triggeredAt, e.CorrelationID())
	}
}

func (c *Component) handleIndexSynchronized(e *events.IndexSynchronizedEvent) {
	for resourceType, count := range e.ResourceCounts {
		c.resourceCounts[resourceType] = count
		c.metrics.SetResourceCount(resourceType, count)
	}
}

func (c *Component) handleResourceIndexUpdated(e *events.ResourceIndexUpdatedEvent) {
	// Skip initial sync events - we'll get the totals from IndexSynchronizedEvent
	if e.ChangeStats.IsInitialSync {
		return
	}

	newCount := c.resourceCounts[e.ResourceTypeName] + e.ChangeStats.Created - e.ChangeStats.Deleted
	c.resourceCounts[e.ResourceTypeName] = newCount
	c.metrics.SetResourceCount(e.ResourceTypeName, newCount)
}

func (c *Component) handleLostLeadership(e *events.LostLeadershipEvent) {
	c.metrics.SetIsLeader(false)
	c.metrics.RecordLeadershipTransition()

	if !c.becameLeaderAt.IsZero() {
		c.metrics.AddTimeAsLeader(e.Timestamp().Sub(c.becameLeaderAt).Seconds())
		c.becameLeaderAt = time.Time{}
	}
}

func (c *Component) handleCertParsed(e *events.CertParsedEvent) {
	block, _ := pem.Decode(e.CertPEM)
	if block == nil {
		return
	}
	if cert, err := x509.ParseCertificate(block.Bytes); err == nil {
		c.metrics.SetWebhookCertExpiry(cert.NotAfter.Unix())
	}
}

// msToSeconds converts a duration in milliseconds to seconds.
func msToSeconds(ms int64) float64 {
	return float64(ms) / 1000.0
}
