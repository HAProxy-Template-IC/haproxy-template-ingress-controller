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

// Package events contains all domain event type definitions for the HAPTIC controller.
//
// # Event Immutability Contract
//
// Events in this system are intended to be immutable after creation. They represent
// historical facts about what happened in the system and should not be modified after
// being published to the EventBus.
//
// To support this immutability contract:
//
//  1. All event types use pointer receivers for their Event interface methods.
//     This avoids copying large structs (200+ bytes) and follows Go best practices.
//
//  2. All event fields are exported to support JSON serialization and idiomatic Go access.
//     This follows industry standards (Kubernetes, NATS) rather than enforcing immutability
//     through unexported fields and getters.
//
//  3. Constructors perform defensive copying of slices and maps to prevent mutations
//     from affecting the published event. Publishers cannot modify events after creation.
//
//  4. Consumers MUST NOT modify event fields. This immutability contract is enforced through:
//     - A custom static analyzer (tools/linters/eventimmutability) that detects parameter mutations
//     - Code review for cases not caught by the analyzer
//     - Team discipline and documentation
//
// This approach balances performance, Go idioms, and practical immutability for an
// internal project where all consumers are controlled.
//
// # Event Categories
//
// Events are organized into separate files by category:
//
//   - config.go:              ConfigMap/Secret changes and validation events
//   - resource.go:            Kubernetes resource indexing and synchronization events
//   - reconciliation.go:      Template rendering and deployment cycle events
//   - template.go:            Template rendering operation events
//   - validation.go:          Configuration validation (syntax and semantics) events
//   - deployment.go:          HAProxy configuration deployment events
//   - discovery.go:           HAProxy pod discovery events
//   - credentials.go:         Credentials loading and validation events
//   - leader.go:              Leader election events
//   - publishing.go:          Config publishing events (including SyncMetadata types)
//   - certificate.go:         Webhook certificate events
//   - webhookobservability.go: Webhook validation observability events
//   - http.go:                HTTP resource events
//   - webhook.go:             Scatter-gather request/response events for webhook validation
package events

// -----------------------------------------------------------------------------
// Event Type Constants.
// -----------------------------------------------------------------------------

const (
	// Configuration event types.
	EventTypeConfigParsed             = "config.parsed"
	EventTypeConfigValidationRequest  = "config.validation.request"
	EventTypeConfigValidationResponse = "config.validation.response"
	EventTypeConfigValidated          = "config.validated"
	EventTypeConfigInvalid            = "config.invalid"
	EventTypeConfigResourceChanged    = "config.resource.changed"

	// Resource event types.
	EventTypeResourceIndexUpdated = "resource.index.updated"
	EventTypeResourceSyncComplete = "resource.sync.complete"
	EventTypeIndexSynchronized    = "index.synchronized"

	// Reconciliation event types.
	EventTypeReconciliationTriggered = "reconciliation.triggered"
	EventTypeReconciliationStarted   = "reconciliation.started"
	EventTypeReconciliationCompleted = "reconciliation.completed"
	EventTypeReconciliationFailed    = "reconciliation.failed"

	// Template event types.
	EventTypeTemplateRendered     = "template.rendered"
	EventTypeTemplateRenderFailed = "template.render.failed"

	// Validation event types (HAProxy dataplane API validation).
	EventTypeValidationStarted   = "validation.started"
	EventTypeValidationCompleted = "validation.completed"
	EventTypeValidationFailed    = "validation.failed"

	// Validation test event types (embedded validation tests).
	EventTypeValidationTestsStarted   = "validation_tests.started"
	EventTypeValidationTestsCompleted = "validation_tests.completed"
	EventTypeValidationTestsFailed    = "validation_tests.failed"

	// Deployment event types.
	EventTypeDeploymentScheduled      = "deployment.scheduled"
	EventTypeDeploymentStarted        = "deployment.started"
	EventTypeInstanceDeployed         = "instance.deployed"
	EventTypeInstanceDeploymentFailed = "instance.deployment.failed"
	EventTypeDeploymentCompleted      = "deployment.completed"
	EventTypeDeploymentCancelRequest  = "deployment.cancel.request"
	EventTypeDriftPreventionTriggered = "drift.prevention.triggered"

	// HAProxy pod event types.
	EventTypeHAProxyPodsDiscovered = "haproxy.pods.discovered"
	EventTypeHAProxyPodTerminated  = "haproxy.pod.terminated"

	// Config publishing event types.
	EventTypeConfigPublished     = "config.published"
	EventTypeConfigPublishFailed = "config.publish.failed"
	EventTypeConfigAppliedToPod  = "config.applied.to.pod"

	// Credentials event types.
	EventTypeSecretResourceChanged = "secret.resource.changed"
	EventTypeCredentialsUpdated    = "credentials.updated"
	EventTypeCredentialsInvalid    = "credentials.invalid"

	// Webhook certificate event types.
	EventTypeCertResourceChanged = "cert.resource.changed"
	EventTypeCertParsed          = "cert.parsed"

	// Webhook validation event types (observability only).
	// Note: Scatter-gather request/response events are in webhook.go.
	EventTypeWebhookValidationRequest = "webhook.validation.request"
	EventTypeWebhookValidationAllowed = "webhook.validation.allowed"
	EventTypeWebhookValidationDenied  = "webhook.validation.denied"
	EventTypeWebhookValidationError   = "webhook.validation.error"

	// Leader election event types.
	EventTypeLeaderElectionStarted = "leader.election.started"
	EventTypeBecameLeader          = "leader.became"
	EventTypeLostLeadership        = "leader.lost"
	EventTypeNewLeaderObserved     = "leader.observed"

	// HTTP resource event types.
	EventTypeHTTPResourceUpdated  = "http.resource.updated"
	EventTypeHTTPResourceAccepted = "http.resource.accepted"
	EventTypeHTTPResourceRejected = "http.resource.rejected"

	// Proposal validation event types.
	// Used for validating hypothetical configuration changes before committing them.
	// See proposal.go for event definitions.
	EventTypeProposalValidationRequested = "proposal.validation.requested"
	EventTypeProposalValidationCompleted = "proposal.validation.completed"
)

// -----------------------------------------------------------------------------
// Trigger Reason Constants.
// -----------------------------------------------------------------------------

// TriggerReason constants for reconciliation events.
// These are propagated through the event chain via TriggerReason fields.
const (
	TriggerReasonDriftPrevention = "drift_prevention"
)
