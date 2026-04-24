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

package commentator

import (
	"fmt"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// Lookback window durations for event correlation in the ring buffer.
const (
	validationLookbackWindow     = 30 * time.Second
	reconciliationLookbackWindow = 5 * time.Minute
	startEventLookbackWindow     = 1 * time.Minute
	discoveryLookbackWindow      = 30 * time.Second
)

// generateInsight creates a contextual message and structured attributes for the event.
//
// This applies domain knowledge and uses the ring buffer for event correlation.
// Per-domain handlers live in insights_config.go (config + validation),
// insights_pipeline.go (resource, reconciliation, template, deployment, pod)
// and insights_platform.go (webhook, leader, status).
func (ec *EventCommentator) generateInsight(event busevents.Event) (insight string, args []any) {
	eventType := event.EventType()
	attrs := []any{
		"event_type", eventType,
		"timestamp", event.Timestamp(),
	}

	switch event.(type) {
	// Configuration Events
	case *events.ConfigParsedEvent, *events.ConfigValidationRequest, *events.ConfigValidationResponse,
		*events.ConfigValidatedEvent, *events.ConfigInvalidEvent,
		*events.CertResourceChangedEvent, *events.CertParsedEvent:
		return ec.configInsight(event, attrs)

	// Resource Events
	case *events.ResourceIndexUpdatedEvent, *events.ResourceSyncCompleteEvent,
		*events.IndexSynchronizedEvent:
		return ec.resourceInsight(event, attrs)

	// Reconciliation Events
	case *events.ReconciliationTriggeredEvent, *events.ReconciliationStartedEvent,
		*events.ReconciliationCompletedEvent, *events.ReconciliationFailedEvent:
		return ec.reconciliationInsight(event, attrs)

	// Template Events
	case *events.TemplateRenderedEvent, *events.TemplateRenderFailedEvent:
		return ec.templateInsight(event, attrs)

	// Validation Events
	case *events.ValidationStartedEvent, *events.ValidationCompletedEvent,
		*events.ValidationFailedEvent,
		*events.ValidationTestsStartedEvent, *events.ValidationTestsCompletedEvent,
		*events.ValidationTestsFailedEvent:
		return ec.validationInsight(event, attrs)

	// Deployment Events
	case *events.DeploymentStartedEvent, *events.InstanceDeployedEvent,
		*events.InstanceDeploymentFailedEvent, *events.DeploymentCompletedEvent:
		return ec.deploymentInsight(event, attrs)

	// HAProxy Pod Events
	case *events.HAProxyPodsDiscoveredEvent, *events.HAProxyPodTerminatedEvent:
		return ec.podInsight(event, attrs)

	// Webhook Validation Events
	case *events.WebhookValidationRequestEvent, *events.WebhookValidationAllowedEvent,
		*events.WebhookValidationDeniedEvent, *events.WebhookValidationErrorEvent:
		return ec.webhookInsight(event, attrs)

	// Leader Election Events
	case *events.LeaderElectionStartedEvent, *events.BecameLeaderEvent,
		*events.LostLeadershipEvent, *events.NewLeaderObservedEvent:
		return ec.leaderInsight(event, attrs)

	// Status Update Events
	case *events.StatusUpdateCompletedEvent, *events.StatusUpdateFailedEvent:
		return ec.statusInsight(event, attrs)

	default:
		// Fallback for unknown event types
		return fmt.Sprintf("Event: %s", eventType), attrs
	}
}

// namespacedName returns "namespace/name" for namespaced resources, or just "name"
// for cluster-scoped resources where namespace is empty.
func namespacedName(namespace, name string) string {
	if namespace == "" {
		return name
	}
	return namespace + "/" + name
}
