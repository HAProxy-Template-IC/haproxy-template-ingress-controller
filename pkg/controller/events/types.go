package events

import (
	"fmt"
	"time"

	"haproxy-template-ic/pkg/k8s/types"
)

// This file contains all event type definitions for the haproxy-template-ic controller.
//
// # Event Immutability Contract
//
// Events in this system are intended to be immutable after creation. They represent
// historical facts about what happened in the system and should not be modified after
// being published to the EventBus.
//
// To support this immutability contract:.
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
// This approach balances performance, Go idioms, and practical immutability for an.
// internal project where all consumers are controlled.
//
// Events are organized into categories:.
// - Lifecycle Events: System startup and shutdown
// - Configuration Events: ConfigMap/Secret changes and validation
// - Resource Events: Kubernetes resource indexing and synchronization
// - Reconciliation Events: Template rendering and deployment cycles
// - Template Events: Template rendering operations
// - Validation Events: Configuration validation (syntax and semantics)
// - Deployment Events: HAProxy configuration deployment
// - Storage Events: Auxiliary file synchronization
// - HAProxy Pod Events: HAProxy pod discovery

// -----------------------------------------------------------------------------
// Event Type Constants.
// -----------------------------------------------------------------------------

const (
	// Lifecycle event types.
	EventTypeControllerStarted  = "controller.started"
	EventTypeControllerShutdown = "controller.shutdown"

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
	EventTypeDriftPreventionTriggered = "drift.prevention.triggered"

	// Storage event types.
	EventTypeStorageSyncStarted   = "storage.sync.started"
	EventTypeStorageSyncCompleted = "storage.sync.completed"
	EventTypeStorageSyncFailed    = "storage.sync.failed"

	// HAProxy pod event types.
	EventTypeHAProxyPodsDiscovered = "haproxy.pods.discovered"
	EventTypeHAProxyPodAdded       = "haproxy.pod.added"
	EventTypeHAProxyPodRemoved     = "haproxy.pod.removed"
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
)

// -----------------------------------------------------------------------------
// Lifecycle Events.
// -----------------------------------------------------------------------------

// ControllerStartedEvent is published when the controller has completed startup.
// and all components are ready to process events.
type ControllerStartedEvent struct {
	ConfigVersion string
	SecretVersion string
	timestamp     time.Time
}

// NewControllerStartedEvent creates a new ControllerStartedEvent.
func NewControllerStartedEvent(configVersion, secretVersion string) *ControllerStartedEvent {
	return &ControllerStartedEvent{
		ConfigVersion: configVersion,
		SecretVersion: secretVersion,
		timestamp:     time.Now(),
	}
}

func (e *ControllerStartedEvent) EventType() string    { return EventTypeControllerStarted }
func (e *ControllerStartedEvent) Timestamp() time.Time { return e.timestamp }

// ControllerShutdownEvent is published when the controller is shutting down gracefully.
type ControllerShutdownEvent struct {
	Reason    string
	timestamp time.Time
}

// NewControllerShutdownEvent creates a new ControllerShutdownEvent.
func NewControllerShutdownEvent(reason string) *ControllerShutdownEvent {
	return &ControllerShutdownEvent{
		Reason:    reason,
		timestamp: time.Now(),
	}
}

func (e *ControllerShutdownEvent) EventType() string    { return EventTypeControllerShutdown }
func (e *ControllerShutdownEvent) Timestamp() time.Time { return e.timestamp }

// -----------------------------------------------------------------------------
// Configuration Events.
// -----------------------------------------------------------------------------

// ConfigParsedEvent is published when the configuration ConfigMap/Secret has been.
// successfully parsed into a Config structure.
//
// This event does not mean the config is valid - only that it could be parsed.
// Validation occurs in a subsequent step.
type ConfigParsedEvent struct {
	// Config contains the parsed configuration.
	// Type: interface{} to avoid circular dependencies.
	// Consumers should type-assert to their expected config type.
	Config interface{}

	// TemplateConfig is the original HAProxyTemplateConfig CRD.
	// Type: interface{} to avoid circular dependencies.
	// Needed by ConfigPublisher to extract Kubernetes metadata (name, namespace, UID).
	TemplateConfig interface{}

	// Version is the resourceVersion of the ConfigMap.
	Version string

	// SecretVersion is the resourceVersion of the credentials Secret.
	SecretVersion string

	timestamp time.Time
}

// NewConfigParsedEvent creates a new ConfigParsedEvent.
func NewConfigParsedEvent(config, templateConfig interface{}, version, secretVersion string) *ConfigParsedEvent {
	return &ConfigParsedEvent{
		Config:         config,
		TemplateConfig: templateConfig,
		Version:        version,
		SecretVersion:  secretVersion,
		timestamp:      time.Now(),
	}
}

func (e *ConfigParsedEvent) EventType() string    { return EventTypeConfigParsed }
func (e *ConfigParsedEvent) Timestamp() time.Time { return e.timestamp }

// ConfigValidationRequest is published to request validation of a parsed config.
//
// This is a Request event used in the scatter-gather pattern. Multiple validators
// (basic, template, jsonpath) will respond with ConfigValidationResponse events.
type ConfigValidationRequest struct {
	reqID string

	// Config contains the configuration to validate.
	Config interface{}

	// Version is the resourceVersion being validated.
	Version string

	timestamp time.Time
}

// NewConfigValidationRequest creates a new ConfigValidationRequest.
func NewConfigValidationRequest(config interface{}, version string) *ConfigValidationRequest {
	return &ConfigValidationRequest{
		reqID:     fmt.Sprintf("config-validation-%s-%d", version, time.Now().UnixNano()),
		Config:    config,
		Version:   version,
		timestamp: time.Now(),
	}
}

func (e *ConfigValidationRequest) EventType() string    { return EventTypeConfigValidationRequest }
func (e *ConfigValidationRequest) Timestamp() time.Time { return e.timestamp }
func (e *ConfigValidationRequest) RequestID() string    { return e.reqID }

// ConfigValidationResponse is sent by validators in response to ConfigValidationRequest.
//
// This is a Response event used in the scatter-gather pattern. The ValidationCoordinator
// collects all responses and determines if the config is valid overall.
type ConfigValidationResponse struct {
	reqID     string
	responder string

	// ValidatorName identifies which validator produced this response (basic, template, jsonpath).
	ValidatorName string

	// Valid is true if this validator found no errors.
	Valid bool

	// Errors contains validation error messages, empty if Valid is true.
	Errors []string

	timestamp time.Time
}

// NewConfigValidationResponse creates a new ConfigValidationResponse.
// Performs defensive copy of the errors slice.
func NewConfigValidationResponse(requestID, validatorName string, valid bool, errors []string) *ConfigValidationResponse {
	// Defensive copy of errors slice
	var errorsCopy []string
	if len(errors) > 0 {
		errorsCopy = make([]string, len(errors))
		copy(errorsCopy, errors)
	}

	return &ConfigValidationResponse{
		reqID:         requestID,
		responder:     validatorName,
		ValidatorName: validatorName,
		Valid:         valid,
		Errors:        errorsCopy,
		timestamp:     time.Now(),
	}
}

func (e *ConfigValidationResponse) EventType() string    { return EventTypeConfigValidationResponse }
func (e *ConfigValidationResponse) Timestamp() time.Time { return e.timestamp }
func (e *ConfigValidationResponse) RequestID() string    { return e.reqID }
func (e *ConfigValidationResponse) Responder() string    { return e.responder }

// ConfigValidatedEvent is published when all validators have confirmed the config is valid.
//
// After receiving this event, the controller proceeds to start resource watchers.
// with the validated configuration.
type ConfigValidatedEvent struct {
	Config interface{}

	// TemplateConfig is the original HAProxyTemplateConfig CRD.
	// Type: interface{} to avoid circular dependencies.
	// Needed by ConfigPublisher to extract Kubernetes metadata (name, namespace, UID).
	TemplateConfig interface{}

	Version       string
	SecretVersion string
	timestamp     time.Time
}

// NewConfigValidatedEvent creates a new ConfigValidatedEvent.
func NewConfigValidatedEvent(config, templateConfig interface{}, version, secretVersion string) *ConfigValidatedEvent {
	return &ConfigValidatedEvent{
		Config:         config,
		TemplateConfig: templateConfig,
		Version:        version,
		SecretVersion:  secretVersion,
		timestamp:      time.Now(),
	}
}

func (e *ConfigValidatedEvent) EventType() string    { return EventTypeConfigValidated }
func (e *ConfigValidatedEvent) Timestamp() time.Time { return e.timestamp }

// ConfigInvalidEvent is published when config validation fails.
//
// The controller will continue running with the previous valid config and wait.
// for the next ConfigMap update.
type ConfigInvalidEvent struct {
	Version string

	// ValidationErrors maps validator names to their error messages.
	ValidationErrors map[string][]string

	timestamp time.Time
}

// NewConfigInvalidEvent creates a new ConfigInvalidEvent.
// Performs defensive copy of the validation errors map and its slice values.
func NewConfigInvalidEvent(version string, validationErrors map[string][]string) *ConfigInvalidEvent {
	// Defensive copy of map with slice values
	errorsCopy := make(map[string][]string, len(validationErrors))
	for k, v := range validationErrors {
		if len(v) > 0 {
			vCopy := make([]string, len(v))
			copy(vCopy, v)
			errorsCopy[k] = vCopy
		} else {
			errorsCopy[k] = nil
		}
	}

	return &ConfigInvalidEvent{
		Version:          version,
		ValidationErrors: errorsCopy,
		timestamp:        time.Now(),
	}
}

func (e *ConfigInvalidEvent) EventType() string    { return EventTypeConfigInvalid }
func (e *ConfigInvalidEvent) Timestamp() time.Time { return e.timestamp }

// -----------------------------------------------------------------------------
// Resource Events.
// -----------------------------------------------------------------------------

// ResourceIndexUpdatedEvent is published when a watched Kubernetes resource.
// has been added, updated, or deleted in the local index.
type ResourceIndexUpdatedEvent struct {
	// ResourceTypeName identifies the resource type from config (e.g., "ingresses", "services").
	ResourceTypeName string

	// ChangeStats provides detailed change statistics including Created, Modified, Deleted counts
	// and whether this event occurred during initial sync.
	ChangeStats types.ChangeStats

	timestamp time.Time
}

// NewResourceIndexUpdatedEvent creates a new ResourceIndexUpdatedEvent.
// Performs a value copy of ChangeStats (it's a small struct with no pointers).
func NewResourceIndexUpdatedEvent(resourceTypeName string, changeStats types.ChangeStats) *ResourceIndexUpdatedEvent {
	return &ResourceIndexUpdatedEvent{
		ResourceTypeName: resourceTypeName,
		ChangeStats:      changeStats,
		timestamp:        time.Now(),
	}
}

func (e *ResourceIndexUpdatedEvent) EventType() string    { return EventTypeResourceIndexUpdated }
func (e *ResourceIndexUpdatedEvent) Timestamp() time.Time { return e.timestamp }

// ResourceSyncCompleteEvent is published when a resource watcher has completed.
// its initial sync with the Kubernetes API.
type ResourceSyncCompleteEvent struct {
	// ResourceTypeName identifies the resource type from config (e.g., "ingresses").
	ResourceTypeName string

	// InitialCount is the number of resources loaded during initial sync.
	InitialCount int

	timestamp time.Time
}

// NewResourceSyncCompleteEvent creates a new ResourceSyncCompleteEvent.
func NewResourceSyncCompleteEvent(resourceTypeName string, initialCount int) *ResourceSyncCompleteEvent {
	return &ResourceSyncCompleteEvent{
		ResourceTypeName: resourceTypeName,
		InitialCount:     initialCount,
		timestamp:        time.Now(),
	}
}

func (e *ResourceSyncCompleteEvent) EventType() string    { return EventTypeResourceSyncComplete }
func (e *ResourceSyncCompleteEvent) Timestamp() time.Time { return e.timestamp }

// IndexSynchronizedEvent is published when all resource watchers have completed.
// their initial sync and the system has a complete view of all resources.
//
// This is a critical milestone - the controller waits for this event before.
// starting reconciliation to ensure it has complete data.
type IndexSynchronizedEvent struct {
	// ResourceCounts maps resource types to their counts.
	ResourceCounts map[string]int
	timestamp      time.Time
}

// NewIndexSynchronizedEvent creates a new IndexSynchronizedEvent.
// Performs defensive copy of the resource counts map.
func NewIndexSynchronizedEvent(resourceCounts map[string]int) *IndexSynchronizedEvent {
	// Defensive copy of map
	countsCopy := make(map[string]int, len(resourceCounts))
	for k, v := range resourceCounts {
		countsCopy[k] = v
	}

	return &IndexSynchronizedEvent{
		ResourceCounts: countsCopy,
		timestamp:      time.Now(),
	}
}

func (e *IndexSynchronizedEvent) EventType() string    { return EventTypeIndexSynchronized }
func (e *IndexSynchronizedEvent) Timestamp() time.Time { return e.timestamp }

// -----------------------------------------------------------------------------
// Reconciliation Events.
// -----------------------------------------------------------------------------

// ReconciliationTriggeredEvent is published when a reconciliation cycle should start.
//
// This event is typically published by the Reconciler after the debounce timer.
// expires, or immediately for config changes.
type ReconciliationTriggeredEvent struct {
	// Reason describes why reconciliation was triggered.
	// Examples: "debounce_timer", "config_change", "manual_trigger"
	Reason    string
	timestamp time.Time
}

// NewReconciliationTriggeredEvent creates a new ReconciliationTriggeredEvent.
func NewReconciliationTriggeredEvent(reason string) *ReconciliationTriggeredEvent {
	return &ReconciliationTriggeredEvent{
		Reason:    reason,
		timestamp: time.Now(),
	}
}

func (e *ReconciliationTriggeredEvent) EventType() string    { return EventTypeReconciliationTriggered }
func (e *ReconciliationTriggeredEvent) Timestamp() time.Time { return e.timestamp }

// ReconciliationStartedEvent is published when the Executor begins a reconciliation cycle.
type ReconciliationStartedEvent struct {
	// Trigger describes what triggered this reconciliation.
	Trigger   string
	timestamp time.Time
}

// NewReconciliationStartedEvent creates a new ReconciliationStartedEvent.
func NewReconciliationStartedEvent(trigger string) *ReconciliationStartedEvent {
	return &ReconciliationStartedEvent{
		Trigger:   trigger,
		timestamp: time.Now(),
	}
}

func (e *ReconciliationStartedEvent) EventType() string    { return EventTypeReconciliationStarted }
func (e *ReconciliationStartedEvent) Timestamp() time.Time { return e.timestamp }

// ReconciliationCompletedEvent is published when a reconciliation cycle completes successfully.
type ReconciliationCompletedEvent struct {
	DurationMs int64
	timestamp  time.Time
}

// NewReconciliationCompletedEvent creates a new ReconciliationCompletedEvent.
func NewReconciliationCompletedEvent(durationMs int64) *ReconciliationCompletedEvent {
	return &ReconciliationCompletedEvent{
		DurationMs: durationMs,
		timestamp:  time.Now(),
	}
}

func (e *ReconciliationCompletedEvent) EventType() string    { return EventTypeReconciliationCompleted }
func (e *ReconciliationCompletedEvent) Timestamp() time.Time { return e.timestamp }

// ReconciliationFailedEvent is published when a reconciliation cycle fails.
type ReconciliationFailedEvent struct {
	Error     string
	Phase     string // Which phase failed: "render", "validate", "deploy"
	timestamp time.Time
}

// NewReconciliationFailedEvent creates a new ReconciliationFailedEvent.
func NewReconciliationFailedEvent(err, phase string) *ReconciliationFailedEvent {
	return &ReconciliationFailedEvent{
		Error:     err,
		Phase:     phase,
		timestamp: time.Now(),
	}
}

func (e *ReconciliationFailedEvent) EventType() string    { return EventTypeReconciliationFailed }
func (e *ReconciliationFailedEvent) Timestamp() time.Time { return e.timestamp }

// -----------------------------------------------------------------------------
// Template Events.
// -----------------------------------------------------------------------------

// TemplateRenderedEvent is published when template rendering completes successfully.
//
// This event carries two versions of the rendered HAProxy configuration:
// - Production version with absolute paths for deployment to HAProxy pods
// - Validation version with temp directory paths for controller validation
//
// Both configurations are rendered from the same templates but with different PathResolver instances.
//
// The event also carries two versions of auxiliary files:
// - Production version with accepted HTTP content only (for deployment)
// - Validation version with pending HTTP content (for testing new content before promotion).
type TemplateRenderedEvent struct {
	// HAProxyConfig is the rendered main HAProxy configuration for production deployment.
	// Contains absolute paths like /etc/haproxy/maps/host.map for HAProxy pods.
	HAProxyConfig string

	// ValidationHAProxyConfig is the rendered configuration for controller validation.
	// Contains temp directory paths matching ValidationPaths for isolated validation.
	ValidationHAProxyConfig string

	// ValidationPaths specifies temp directories where auxiliary files should be written for validation.
	// Type: interface{} to avoid circular dependencies with pkg/dataplane.
	// Consumers should type-assert to dataplane.ValidationPaths.
	ValidationPaths interface{}

	// AuxiliaryFiles contains all rendered auxiliary files (maps, certificates, general files)
	// for production deployment. Uses accepted HTTP content only.
	// Type: interface{} to avoid circular dependencies with pkg/dataplane.
	// Consumers should type-assert to *dataplane.AuxiliaryFiles.
	AuxiliaryFiles interface{}

	// ValidationAuxiliaryFiles contains auxiliary files for validation.
	// Uses pending HTTP content if available (for testing new content before promotion).
	// Type: interface{} to avoid circular dependencies with pkg/dataplane.
	// Consumers should type-assert to *dataplane.AuxiliaryFiles.
	ValidationAuxiliaryFiles interface{}

	// Metrics for observability
	ConfigBytes           int   // Size of HAProxyConfig (production)
	ValidationConfigBytes int   // Size of ValidationHAProxyConfig
	AuxiliaryFileCount    int   // Number of auxiliary files
	DurationMs            int64 // Total rendering duration (both configs)

	timestamp time.Time
}

// NewTemplateRenderedEvent creates a new TemplateRenderedEvent.
// Performs defensive copy of the haproxyConfig strings.
func NewTemplateRenderedEvent(
	haproxyConfig string,
	validationHAProxyConfig string,
	validationPaths interface{},
	auxiliaryFiles interface{},
	validationAuxiliaryFiles interface{},
	auxFileCount int,
	durationMs int64,
) *TemplateRenderedEvent {
	// Calculate config sizes
	configBytes := len(haproxyConfig)
	validationConfigBytes := len(validationHAProxyConfig)

	return &TemplateRenderedEvent{
		HAProxyConfig:            haproxyConfig,
		ValidationHAProxyConfig:  validationHAProxyConfig,
		ValidationPaths:          validationPaths,
		AuxiliaryFiles:           auxiliaryFiles,
		ValidationAuxiliaryFiles: validationAuxiliaryFiles,
		ConfigBytes:              configBytes,
		ValidationConfigBytes:    validationConfigBytes,
		AuxiliaryFileCount:       auxFileCount,
		DurationMs:               durationMs,
		timestamp:                time.Now(),
	}
}

func (e *TemplateRenderedEvent) EventType() string    { return EventTypeTemplateRendered }
func (e *TemplateRenderedEvent) Timestamp() time.Time { return e.timestamp }

// TemplateRenderFailedEvent is published when template rendering fails.
type TemplateRenderFailedEvent struct {
	// TemplateName is the name of the template that failed to render.
	TemplateName string

	// Error is the error message.
	Error string

	// StackTrace provides additional debugging context.
	StackTrace string

	timestamp time.Time
}

// NewTemplateRenderFailedEvent creates a new TemplateRenderFailedEvent.
func NewTemplateRenderFailedEvent(templateName, err, stackTrace string) *TemplateRenderFailedEvent {
	return &TemplateRenderFailedEvent{
		TemplateName: templateName,
		Error:        err,
		StackTrace:   stackTrace,
		timestamp:    time.Now(),
	}
}

func (e *TemplateRenderFailedEvent) EventType() string    { return EventTypeTemplateRenderFailed }
func (e *TemplateRenderFailedEvent) Timestamp() time.Time { return e.timestamp }

// -----------------------------------------------------------------------------
// Validation Events.
// -----------------------------------------------------------------------------

// ValidationStartedEvent is published when local configuration validation begins.
//
// Validation is performed locally using the HAProxy binary to check configuration syntax.
// It does not involve HAProxy endpoints - those are only used later for deployment.
type ValidationStartedEvent struct {
	timestamp time.Time
}

// NewValidationStartedEvent creates a new ValidationStartedEvent.
func NewValidationStartedEvent() *ValidationStartedEvent {
	return &ValidationStartedEvent{
		timestamp: time.Now(),
	}
}

func (e *ValidationStartedEvent) EventType() string    { return EventTypeValidationStarted }
func (e *ValidationStartedEvent) Timestamp() time.Time { return e.timestamp }

// ValidationCompletedEvent is published when local configuration validation succeeds.
//
// Validation is performed locally using the HAProxy binary. Endpoints are not involved.
type ValidationCompletedEvent struct {
	Warnings   []string // Non-fatal warnings from HAProxy validation
	DurationMs int64
	timestamp  time.Time
}

// NewValidationCompletedEvent creates a new ValidationCompletedEvent.
// Performs defensive copy of the warnings slice.
func NewValidationCompletedEvent(warnings []string, durationMs int64) *ValidationCompletedEvent {
	// Defensive copy of warnings slice
	var warningsCopy []string
	if len(warnings) > 0 {
		warningsCopy = make([]string, len(warnings))
		copy(warningsCopy, warnings)
	}

	return &ValidationCompletedEvent{
		Warnings:   warningsCopy,
		DurationMs: durationMs,
		timestamp:  time.Now(),
	}
}

func (e *ValidationCompletedEvent) EventType() string    { return EventTypeValidationCompleted }
func (e *ValidationCompletedEvent) Timestamp() time.Time { return e.timestamp }

// ValidationFailedEvent is published when local configuration validation fails.
//
// Validation is performed locally using the HAProxy binary. Endpoints are not involved.
type ValidationFailedEvent struct {
	Errors     []string // Validation errors from HAProxy
	DurationMs int64
	timestamp  time.Time
}

// NewValidationFailedEvent creates a new ValidationFailedEvent.
// Performs defensive copy of the errors slice.
func NewValidationFailedEvent(errors []string, durationMs int64) *ValidationFailedEvent {
	// Defensive copy of errors slice
	var errorsCopy []string
	if len(errors) > 0 {
		errorsCopy = make([]string, len(errors))
		copy(errorsCopy, errors)
	}

	return &ValidationFailedEvent{
		Errors:     errorsCopy,
		DurationMs: durationMs,
		timestamp:  time.Now(),
	}
}

func (e *ValidationFailedEvent) EventType() string    { return EventTypeValidationFailed }
func (e *ValidationFailedEvent) Timestamp() time.Time { return e.timestamp }

// ValidationTestsStartedEvent is published when embedded validation tests begin execution.
//
// This is used for both CLI validation and webhook validation.
type ValidationTestsStartedEvent struct {
	TestCount int // Number of tests to execute
	timestamp time.Time
}

// NewValidationTestsStartedEvent creates a new ValidationTestsStartedEvent.
func NewValidationTestsStartedEvent(testCount int) *ValidationTestsStartedEvent {
	return &ValidationTestsStartedEvent{
		TestCount: testCount,
		timestamp: time.Now(),
	}
}

func (e *ValidationTestsStartedEvent) EventType() string    { return EventTypeValidationTestsStarted }
func (e *ValidationTestsStartedEvent) Timestamp() time.Time { return e.timestamp }

// ValidationTestsCompletedEvent is published when all validation tests finish execution.
//
// This event is published regardless of whether tests passed or failed.
type ValidationTestsCompletedEvent struct {
	TotalTests  int   // Total number of tests executed
	PassedTests int   // Number of tests that passed
	FailedTests int   // Number of tests that failed
	DurationMs  int64 // Time taken to execute all tests
	timestamp   time.Time
}

// NewValidationTestsCompletedEvent creates a new ValidationTestsCompletedEvent.
func NewValidationTestsCompletedEvent(total, passed, failed int, durationMs int64) *ValidationTestsCompletedEvent {
	return &ValidationTestsCompletedEvent{
		TotalTests:  total,
		PassedTests: passed,
		FailedTests: failed,
		DurationMs:  durationMs,
		timestamp:   time.Now(),
	}
}

func (e *ValidationTestsCompletedEvent) EventType() string    { return EventTypeValidationTestsCompleted }
func (e *ValidationTestsCompletedEvent) Timestamp() time.Time { return e.timestamp }

// ValidationTestsFailedEvent is published when validation tests fail during webhook validation.
//
// This event is only published during webhook validation when tests fail and admission is denied.
type ValidationTestsFailedEvent struct {
	FailedTests []string // Names of tests that failed
	timestamp   time.Time
}

// NewValidationTestsFailedEvent creates a new ValidationTestsFailedEvent.
// Performs defensive copy of the failed tests slice.
func NewValidationTestsFailedEvent(failedTests []string) *ValidationTestsFailedEvent {
	// Defensive copy of slice
	var failedCopy []string
	if len(failedTests) > 0 {
		failedCopy = make([]string, len(failedTests))
		copy(failedCopy, failedTests)
	}

	return &ValidationTestsFailedEvent{
		FailedTests: failedCopy,
		timestamp:   time.Now(),
	}
}

func (e *ValidationTestsFailedEvent) EventType() string    { return EventTypeValidationTestsFailed }
func (e *ValidationTestsFailedEvent) Timestamp() time.Time { return e.timestamp }

// -----------------------------------------------------------------------------
// Deployment Events.
// -----------------------------------------------------------------------------

// DeploymentStartedEvent is published when deployment to HAProxy instances begins.
type DeploymentStartedEvent struct {
	Endpoints []interface{}
	timestamp time.Time
}

// NewDeploymentStartedEvent creates a new DeploymentStartedEvent.
// Performs defensive copy of the endpoints slice.
func NewDeploymentStartedEvent(endpoints []interface{}) *DeploymentStartedEvent {
	// Defensive copy of slice
	var endpointsCopy []interface{}
	if len(endpoints) > 0 {
		endpointsCopy = make([]interface{}, len(endpoints))
		copy(endpointsCopy, endpoints)
	}

	return &DeploymentStartedEvent{
		Endpoints: endpointsCopy,
		timestamp: time.Now(),
	}
}

func (e *DeploymentStartedEvent) EventType() string    { return EventTypeDeploymentStarted }
func (e *DeploymentStartedEvent) Timestamp() time.Time { return e.timestamp }

// InstanceDeployedEvent is published when deployment to a single HAProxy instance succeeds.
type InstanceDeployedEvent struct {
	Endpoint       interface{} // The HAProxy endpoint that was deployed to
	DurationMs     int64
	ReloadRequired bool // Whether this deployment required an HAProxy reload
	timestamp      time.Time
}

// NewInstanceDeployedEvent creates a new InstanceDeployedEvent.
func NewInstanceDeployedEvent(endpoint interface{}, durationMs int64, reloadRequired bool) *InstanceDeployedEvent {
	return &InstanceDeployedEvent{
		Endpoint:       endpoint,
		DurationMs:     durationMs,
		ReloadRequired: reloadRequired,
		timestamp:      time.Now(),
	}
}

func (e *InstanceDeployedEvent) EventType() string    { return EventTypeInstanceDeployed }
func (e *InstanceDeployedEvent) Timestamp() time.Time { return e.timestamp }

// InstanceDeploymentFailedEvent is published when deployment to a single HAProxy instance fails.
type InstanceDeploymentFailedEvent struct {
	Endpoint  interface{}
	Error     string
	Retryable bool // Whether this failure is retryable
	timestamp time.Time
}

// NewInstanceDeploymentFailedEvent creates a new InstanceDeploymentFailedEvent.
func NewInstanceDeploymentFailedEvent(endpoint interface{}, err string, retryable bool) *InstanceDeploymentFailedEvent {
	return &InstanceDeploymentFailedEvent{
		Endpoint:  endpoint,
		Error:     err,
		Retryable: retryable,
		timestamp: time.Now(),
	}
}

func (e *InstanceDeploymentFailedEvent) EventType() string    { return EventTypeInstanceDeploymentFailed }
func (e *InstanceDeploymentFailedEvent) Timestamp() time.Time { return e.timestamp }

// DeploymentCompletedEvent is published when deployment to all HAProxy instances completes.
type DeploymentCompletedEvent struct {
	Total      int // Total number of instances
	Succeeded  int // Number of successful deployments
	Failed     int // Number of failed deployments
	DurationMs int64
	timestamp  time.Time
}

// NewDeploymentCompletedEvent creates a new DeploymentCompletedEvent.
func NewDeploymentCompletedEvent(total, succeeded, failed int, durationMs int64) *DeploymentCompletedEvent {
	return &DeploymentCompletedEvent{
		Total:      total,
		Succeeded:  succeeded,
		Failed:     failed,
		DurationMs: durationMs,
		timestamp:  time.Now(),
	}
}

func (e *DeploymentCompletedEvent) EventType() string    { return EventTypeDeploymentCompleted }
func (e *DeploymentCompletedEvent) Timestamp() time.Time { return e.timestamp }

// DeploymentScheduledEvent is published when the deployment scheduler has decided.
// to execute a deployment. This event contains all necessary data for the deployer
// to execute the deployment without maintaining state.
//
// Published by: DeploymentScheduler.
// Consumed by: Deployer component.
type DeploymentScheduledEvent struct {
	// Config is the rendered HAProxy configuration to deploy.
	Config string

	// AuxiliaryFiles contains all rendered auxiliary files.
	// Type: interface{} to avoid circular dependencies with pkg/dataplane.
	// Consumers should type-assert to *dataplane.AuxiliaryFiles.
	AuxiliaryFiles interface{}

	// Endpoints is the list of HAProxy endpoints to deploy to.
	Endpoints []interface{}

	// RuntimeConfigName is the name of the HAProxyCfg resource.
	// Used for publishing ConfigAppliedToPodEvent after successful deployment.
	RuntimeConfigName string

	// RuntimeConfigNamespace is the namespace of the HAProxyCfg resource.
	// Used for publishing ConfigAppliedToPodEvent after successful deployment.
	RuntimeConfigNamespace string

	// Reason describes why this deployment was scheduled.
	// Examples: "config_validation", "pod_discovery", "drift_prevention"
	Reason string

	timestamp time.Time
}

// NewDeploymentScheduledEvent creates a new DeploymentScheduledEvent.
// Performs defensive copy of endpoints slice.
func NewDeploymentScheduledEvent(config string, auxFiles interface{}, endpoints []interface{}, runtimeConfigName, runtimeConfigNamespace, reason string) *DeploymentScheduledEvent {
	// Defensive copy of endpoints slice
	var endpointsCopy []interface{}
	if len(endpoints) > 0 {
		endpointsCopy = make([]interface{}, len(endpoints))
		copy(endpointsCopy, endpoints)
	}

	return &DeploymentScheduledEvent{
		Config:                 config,
		AuxiliaryFiles:         auxFiles,
		Endpoints:              endpointsCopy,
		RuntimeConfigName:      runtimeConfigName,
		RuntimeConfigNamespace: runtimeConfigNamespace,
		Reason:                 reason,
		timestamp:              time.Now(),
	}
}

func (e *DeploymentScheduledEvent) EventType() string    { return EventTypeDeploymentScheduled }
func (e *DeploymentScheduledEvent) Timestamp() time.Time { return e.timestamp }

// DriftPreventionTriggeredEvent is published when the drift prevention monitor.
// detects that no deployment has occurred within the configured interval and
// triggers a deployment to prevent configuration drift.
//
// Published by: DriftPreventionMonitor.
// Consumed by: DeploymentScheduler (which then schedules a deployment).
type DriftPreventionTriggeredEvent struct {
	// TimeSinceLastDeployment is the duration since the last deployment completed.
	TimeSinceLastDeployment time.Duration

	timestamp time.Time
}

// NewDriftPreventionTriggeredEvent creates a new DriftPreventionTriggeredEvent.
func NewDriftPreventionTriggeredEvent(timeSinceLast time.Duration) *DriftPreventionTriggeredEvent {
	return &DriftPreventionTriggeredEvent{
		TimeSinceLastDeployment: timeSinceLast,
		timestamp:               time.Now(),
	}
}

func (e *DriftPreventionTriggeredEvent) EventType() string    { return EventTypeDriftPreventionTriggered }
func (e *DriftPreventionTriggeredEvent) Timestamp() time.Time { return e.timestamp }

// -----------------------------------------------------------------------------
// Storage Events (Auxiliary Files).
// -----------------------------------------------------------------------------

// StorageSyncStartedEvent is published when auxiliary file synchronization begins.
type StorageSyncStartedEvent struct {
	// Phase describes which sync phase: "pre-config", "config", "post-config"
	Phase     string
	Endpoints []interface{}
	timestamp time.Time
}

// NewStorageSyncStartedEvent creates a new StorageSyncStartedEvent.
// Performs defensive copy of the endpoints slice.
func NewStorageSyncStartedEvent(phase string, endpoints []interface{}) *StorageSyncStartedEvent {
	// Defensive copy of slice
	var endpointsCopy []interface{}
	if len(endpoints) > 0 {
		endpointsCopy = make([]interface{}, len(endpoints))
		copy(endpointsCopy, endpoints)
	}

	return &StorageSyncStartedEvent{
		Phase:     phase,
		Endpoints: endpointsCopy,
		timestamp: time.Now(),
	}
}

func (e *StorageSyncStartedEvent) EventType() string    { return EventTypeStorageSyncStarted }
func (e *StorageSyncStartedEvent) Timestamp() time.Time { return e.timestamp }

// StorageSyncCompletedEvent is published when auxiliary file synchronization completes.
type StorageSyncCompletedEvent struct {
	Phase string

	// Stats contains sync statistics.
	// Type: interface{} to avoid circular dependencies.
	Stats interface{}

	DurationMs int64
	timestamp  time.Time
}

// NewStorageSyncCompletedEvent creates a new StorageSyncCompletedEvent.
func NewStorageSyncCompletedEvent(phase string, stats interface{}, durationMs int64) *StorageSyncCompletedEvent {
	return &StorageSyncCompletedEvent{
		Phase:      phase,
		Stats:      stats,
		DurationMs: durationMs,
		timestamp:  time.Now(),
	}
}

func (e *StorageSyncCompletedEvent) EventType() string    { return EventTypeStorageSyncCompleted }
func (e *StorageSyncCompletedEvent) Timestamp() time.Time { return e.timestamp }

// StorageSyncFailedEvent is published when auxiliary file synchronization fails.
type StorageSyncFailedEvent struct {
	Phase     string
	Error     string
	timestamp time.Time
}

// NewStorageSyncFailedEvent creates a new StorageSyncFailedEvent.
func NewStorageSyncFailedEvent(phase, err string) *StorageSyncFailedEvent {
	return &StorageSyncFailedEvent{
		Phase:     phase,
		Error:     err,
		timestamp: time.Now(),
	}
}

func (e *StorageSyncFailedEvent) EventType() string    { return EventTypeStorageSyncFailed }
func (e *StorageSyncFailedEvent) Timestamp() time.Time { return e.timestamp }

// -----------------------------------------------------------------------------
// HAProxy Pod Events.
// -----------------------------------------------------------------------------

// HAProxyPodsDiscoveredEvent is published when HAProxy pods are discovered or updated.
type HAProxyPodsDiscoveredEvent struct {
	// Endpoints is the list of discovered HAProxy Dataplane API endpoints.
	Endpoints []interface{}
	Count     int
	timestamp time.Time
}

// NewHAProxyPodsDiscoveredEvent creates a new HAProxyPodsDiscoveredEvent.
// Performs defensive copy of the endpoints slice.
func NewHAProxyPodsDiscoveredEvent(endpoints []interface{}, count int) *HAProxyPodsDiscoveredEvent {
	// Defensive copy of slice
	var endpointsCopy []interface{}
	if len(endpoints) > 0 {
		endpointsCopy = make([]interface{}, len(endpoints))
		copy(endpointsCopy, endpoints)
	}

	return &HAProxyPodsDiscoveredEvent{
		Endpoints: endpointsCopy,
		Count:     count,
		timestamp: time.Now(),
	}
}

func (e *HAProxyPodsDiscoveredEvent) EventType() string    { return EventTypeHAProxyPodsDiscovered }
func (e *HAProxyPodsDiscoveredEvent) Timestamp() time.Time { return e.timestamp }

// HAProxyPodAddedEvent is published when a new HAProxy pod is discovered.
type HAProxyPodAddedEvent struct {
	Endpoint  interface{}
	timestamp time.Time
}

// NewHAProxyPodAddedEvent creates a new HAProxyPodAddedEvent.
func NewHAProxyPodAddedEvent(endpoint interface{}) *HAProxyPodAddedEvent {
	return &HAProxyPodAddedEvent{
		Endpoint:  endpoint,
		timestamp: time.Now(),
	}
}

func (e *HAProxyPodAddedEvent) EventType() string    { return EventTypeHAProxyPodAdded }
func (e *HAProxyPodAddedEvent) Timestamp() time.Time { return e.timestamp }

// HAProxyPodRemovedEvent is published when an HAProxy pod is removed.
type HAProxyPodRemovedEvent struct {
	Endpoint  interface{}
	timestamp time.Time
}

// NewHAProxyPodRemovedEvent creates a new HAProxyPodRemovedEvent.
func NewHAProxyPodRemovedEvent(endpoint interface{}) *HAProxyPodRemovedEvent {
	return &HAProxyPodRemovedEvent{
		Endpoint:  endpoint,
		timestamp: time.Now(),
	}
}

func (e *HAProxyPodRemovedEvent) EventType() string    { return EventTypeHAProxyPodRemoved }
func (e *HAProxyPodRemovedEvent) Timestamp() time.Time { return e.timestamp }

// HAProxyPodTerminatedEvent is published when an HAProxy pod terminates.
//
// This triggers cleanup of the pod from all runtime config status fields.
type HAProxyPodTerminatedEvent struct {
	PodName      string
	PodNamespace string
	timestamp    time.Time
}

// NewHAProxyPodTerminatedEvent creates a new HAProxyPodTerminatedEvent.
func NewHAProxyPodTerminatedEvent(podName, podNamespace string) *HAProxyPodTerminatedEvent {
	return &HAProxyPodTerminatedEvent{
		PodName:      podName,
		PodNamespace: podNamespace,
		timestamp:    time.Now(),
	}
}

func (e *HAProxyPodTerminatedEvent) EventType() string    { return EventTypeHAProxyPodTerminated }
func (e *HAProxyPodTerminatedEvent) Timestamp() time.Time { return e.timestamp }

// -----------------------------------------------------------------------------
// Config Publishing Events.
// -----------------------------------------------------------------------------

// ConfigPublishedEvent is published after runtime configuration resources are created/updated.
//
// This is a non-critical event - publishing failures do not affect controller operation.
type ConfigPublishedEvent struct {
	RuntimeConfigName      string
	RuntimeConfigNamespace string
	MapFileCount           int
	SecretCount            int
	timestamp              time.Time
}

// NewConfigPublishedEvent creates a new ConfigPublishedEvent.
func NewConfigPublishedEvent(runtimeConfigName, runtimeConfigNamespace string, mapFileCount, secretCount int) *ConfigPublishedEvent {
	return &ConfigPublishedEvent{
		RuntimeConfigName:      runtimeConfigName,
		RuntimeConfigNamespace: runtimeConfigNamespace,
		MapFileCount:           mapFileCount,
		SecretCount:            secretCount,
		timestamp:              time.Now(),
	}
}

func (e *ConfigPublishedEvent) EventType() string    { return EventTypeConfigPublished }
func (e *ConfigPublishedEvent) Timestamp() time.Time { return e.timestamp }

// ConfigPublishFailedEvent is published when runtime configuration publishing fails.
//
// This is logged but does not affect controller operation.
type ConfigPublishFailedEvent struct {
	Error     string
	timestamp time.Time
}

// NewConfigPublishFailedEvent creates a new ConfigPublishFailedEvent.
func NewConfigPublishFailedEvent(err error) *ConfigPublishFailedEvent {
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}

	return &ConfigPublishFailedEvent{
		Error:     errStr,
		timestamp: time.Now(),
	}
}

func (e *ConfigPublishFailedEvent) EventType() string    { return EventTypeConfigPublishFailed }
func (e *ConfigPublishFailedEvent) Timestamp() time.Time { return e.timestamp }

// ConfigAppliedToPodEvent is published after configuration is successfully applied to an HAProxy pod.
//
// This triggers updating the deployment status in runtime config resources.
type ConfigAppliedToPodEvent struct {
	RuntimeConfigName      string
	RuntimeConfigNamespace string
	PodName                string
	PodNamespace           string
	Checksum               string

	// IsDriftCheck indicates whether this was a drift prevention check (GET-only)
	// or an actual sync operation (POST/PUT/DELETE).
	//
	// True:  Drift check - no actual changes were made, just verified config is current
	// False: Actual sync - configuration was written to HAProxy
	IsDriftCheck bool

	// SyncMetadata contains detailed information about the sync operation.
	// Only populated for actual syncs (IsDriftCheck=false).
	SyncMetadata *SyncMetadata

	timestamp time.Time
}

// SyncMetadata contains detailed information about a sync operation.
type SyncMetadata struct {
	// ReloadTriggered indicates whether HAProxy was reloaded during this sync.
	// Reloads occur for structural changes via transaction API (status 202).
	// Runtime-only changes don't trigger reloads (status 200).
	ReloadTriggered bool

	// ReloadID is the reload identifier from HAProxy dataplane API.
	// Only populated when ReloadTriggered is true.
	ReloadID string

	// SyncDuration is how long the sync operation took.
	SyncDuration time.Duration

	// VersionConflictRetries is the number of retries due to version conflicts.
	// HAProxy's dataplane API uses optimistic concurrency control.
	VersionConflictRetries int

	// FallbackUsed indicates whether incremental sync failed and a full
	// raw configuration push was used instead.
	FallbackUsed bool

	// OperationCounts provides a breakdown of operations performed.
	OperationCounts OperationCounts

	// Error contains the error message if sync failed.
	// Empty string indicates success.
	Error string
}

// OperationCounts provides statistics about sync operations.
type OperationCounts struct {
	TotalAPIOperations int
	BackendsAdded      int
	BackendsRemoved    int
	BackendsModified   int
	ServersAdded       int
	ServersRemoved     int
	ServersModified    int
	FrontendsAdded     int
	FrontendsRemoved   int
	FrontendsModified  int
}

// NewConfigAppliedToPodEvent creates a new ConfigAppliedToPodEvent.
func NewConfigAppliedToPodEvent(runtimeConfigName, runtimeConfigNamespace, podName, podNamespace, checksum string, isDriftCheck bool, syncMetadata *SyncMetadata) *ConfigAppliedToPodEvent {
	return &ConfigAppliedToPodEvent{
		RuntimeConfigName:      runtimeConfigName,
		RuntimeConfigNamespace: runtimeConfigNamespace,
		PodName:                podName,
		PodNamespace:           podNamespace,
		Checksum:               checksum,
		IsDriftCheck:           isDriftCheck,
		SyncMetadata:           syncMetadata,
		timestamp:              time.Now(),
	}
}

func (e *ConfigAppliedToPodEvent) EventType() string    { return EventTypeConfigAppliedToPod }
func (e *ConfigAppliedToPodEvent) Timestamp() time.Time { return e.timestamp }

// -----------------------------------------------------------------------------
// Configuration Resource Events.
// -----------------------------------------------------------------------------

// ConfigResourceChangedEvent is published when the ConfigMap resource is added, updated, or deleted.
//
// This is a low-level event published directly by the SingleWatcher callback in the controller package.
// The ConfigLoaderComponent subscribes to this event and handles parsing.
type ConfigResourceChangedEvent struct {
	// Resource contains the raw ConfigMap resource.
	// Type: interface{} to avoid circular dependencies.
	// Consumers should type-assert to *unstructured.Unstructured or *corev1.ConfigMap.
	Resource interface{}

	timestamp time.Time
}

// NewConfigResourceChangedEvent creates a new ConfigResourceChangedEvent.
func NewConfigResourceChangedEvent(resource interface{}) *ConfigResourceChangedEvent {
	return &ConfigResourceChangedEvent{
		Resource:  resource,
		timestamp: time.Now(),
	}
}

func (e *ConfigResourceChangedEvent) EventType() string    { return EventTypeConfigResourceChanged }
func (e *ConfigResourceChangedEvent) Timestamp() time.Time { return e.timestamp }

// SecretResourceChangedEvent is published when the Secret resource is added, updated, or deleted.
//
// This is a low-level event published directly by the SingleWatcher callback in the controller package.
// The CredentialsLoaderComponent subscribes to this event and handles parsing.
type SecretResourceChangedEvent struct {
	// Resource contains the raw Secret resource.
	// Type: interface{} to avoid circular dependencies.
	// Consumers should type-assert to *unstructured.Unstructured or *corev1.Secret.
	Resource interface{}

	timestamp time.Time
}

// NewSecretResourceChangedEvent creates a new SecretResourceChangedEvent.
func NewSecretResourceChangedEvent(resource interface{}) *SecretResourceChangedEvent {
	return &SecretResourceChangedEvent{
		Resource:  resource,
		timestamp: time.Now(),
	}
}

func (e *SecretResourceChangedEvent) EventType() string    { return EventTypeSecretResourceChanged }
func (e *SecretResourceChangedEvent) Timestamp() time.Time { return e.timestamp }

// -----------------------------------------------------------------------------
// Credentials Events.
// -----------------------------------------------------------------------------

// CredentialsUpdatedEvent is published when credentials have been successfully.
// loaded and validated from the Secret.
type CredentialsUpdatedEvent struct {
	// Credentials contains the validated credentials.
	// Type: interface{} to avoid circular dependencies.
	// Consumers should type-assert to their expected credentials type.
	Credentials interface{}

	// SecretVersion is the resourceVersion of the Secret.
	SecretVersion string

	timestamp time.Time
}

// NewCredentialsUpdatedEvent creates a new CredentialsUpdatedEvent.
func NewCredentialsUpdatedEvent(credentials interface{}, secretVersion string) *CredentialsUpdatedEvent {
	return &CredentialsUpdatedEvent{
		Credentials:   credentials,
		SecretVersion: secretVersion,
		timestamp:     time.Now(),
	}
}

func (e *CredentialsUpdatedEvent) EventType() string    { return EventTypeCredentialsUpdated }
func (e *CredentialsUpdatedEvent) Timestamp() time.Time { return e.timestamp }

// CredentialsInvalidEvent is published when credential loading or validation fails.
//
// The controller will continue running with the previous valid credentials and wait.
// for the next Secret update.
type CredentialsInvalidEvent struct {
	SecretVersion string
	Error         string

	timestamp time.Time
}

// NewCredentialsInvalidEvent creates a new CredentialsInvalidEvent.
func NewCredentialsInvalidEvent(secretVersion, errMsg string) *CredentialsInvalidEvent {
	return &CredentialsInvalidEvent{
		SecretVersion: secretVersion,
		Error:         errMsg,
		timestamp:     time.Now(),
	}
}

func (e *CredentialsInvalidEvent) EventType() string    { return EventTypeCredentialsInvalid }
func (e *CredentialsInvalidEvent) Timestamp() time.Time { return e.timestamp }

// -----------------------------------------------------------------------------
// Webhook Certificate Events
// -----------------------------------------------------------------------------

// CertResourceChangedEvent is published when the webhook certificate Secret changes.
//
// This event is published by the resource watcher when the Secret resource
// is created, updated, or modified.
type CertResourceChangedEvent struct {
	Resource interface{} // *unstructured.Unstructured

	timestamp time.Time
}

// NewCertResourceChangedEvent creates a new CertResourceChangedEvent.
func NewCertResourceChangedEvent(resource interface{}) *CertResourceChangedEvent {
	return &CertResourceChangedEvent{
		Resource:  resource,
		timestamp: time.Now(),
	}
}

func (e *CertResourceChangedEvent) EventType() string    { return EventTypeCertResourceChanged }
func (e *CertResourceChangedEvent) Timestamp() time.Time { return e.timestamp }

// CertParsedEvent is published when webhook certificates are successfully extracted and parsed.
//
// The controller will use these certificates to initialize the webhook server.
type CertParsedEvent struct {
	CertPEM []byte
	KeyPEM  []byte
	Version string // Secret resourceVersion

	timestamp time.Time
}

// NewCertParsedEvent creates a new CertParsedEvent.
func NewCertParsedEvent(certPEM, keyPEM []byte, version string) *CertParsedEvent {
	// Defensive copy of byte slices
	certCopy := make([]byte, len(certPEM))
	copy(certCopy, certPEM)

	keyCopy := make([]byte, len(keyPEM))
	copy(keyCopy, keyPEM)

	return &CertParsedEvent{
		CertPEM:   certCopy,
		KeyPEM:    keyCopy,
		Version:   version,
		timestamp: time.Now(),
	}
}

func (e *CertParsedEvent) EventType() string    { return EventTypeCertParsed }
func (e *CertParsedEvent) Timestamp() time.Time { return e.timestamp }

// -----------------------------------------------------------------------------
// Webhook Validation Events (Observability)
// -----------------------------------------------------------------------------

// WebhookValidationRequestEvent is published when an admission request is received.
type WebhookValidationRequestEvent struct {
	RequestUID string
	Kind       string
	Name       string
	Namespace  string
	Operation  string
	timestamp  time.Time
}

// NewWebhookValidationRequestEvent creates a new WebhookValidationRequestEvent.
func NewWebhookValidationRequestEvent(requestUID, kind, name, namespace, operation string) *WebhookValidationRequestEvent {
	return &WebhookValidationRequestEvent{
		RequestUID: requestUID,
		Kind:       kind,
		Name:       name,
		Namespace:  namespace,
		Operation:  operation,
		timestamp:  time.Now(),
	}
}

func (e *WebhookValidationRequestEvent) EventType() string {
	return EventTypeWebhookValidationRequest
}
func (e *WebhookValidationRequestEvent) Timestamp() time.Time { return e.timestamp }

// WebhookValidationAllowedEvent is published when a resource is admitted.
type WebhookValidationAllowedEvent struct {
	RequestUID string
	Kind       string
	Name       string
	Namespace  string
	timestamp  time.Time
}

// NewWebhookValidationAllowedEvent creates a new WebhookValidationAllowedEvent.
func NewWebhookValidationAllowedEvent(requestUID, kind, name, namespace string) *WebhookValidationAllowedEvent {
	return &WebhookValidationAllowedEvent{
		RequestUID: requestUID,
		Kind:       kind,
		Name:       name,
		Namespace:  namespace,
		timestamp:  time.Now(),
	}
}

func (e *WebhookValidationAllowedEvent) EventType() string {
	return EventTypeWebhookValidationAllowed
}
func (e *WebhookValidationAllowedEvent) Timestamp() time.Time { return e.timestamp }

// WebhookValidationDeniedEvent is published when a resource is denied.
type WebhookValidationDeniedEvent struct {
	RequestUID string
	Kind       string
	Name       string
	Namespace  string
	Reason     string
	timestamp  time.Time
}

// NewWebhookValidationDeniedEvent creates a new WebhookValidationDeniedEvent.
func NewWebhookValidationDeniedEvent(requestUID, kind, name, namespace, reason string) *WebhookValidationDeniedEvent {
	return &WebhookValidationDeniedEvent{
		RequestUID: requestUID,
		Kind:       kind,
		Name:       name,
		Namespace:  namespace,
		Reason:     reason,
		timestamp:  time.Now(),
	}
}

func (e *WebhookValidationDeniedEvent) EventType() string {
	return EventTypeWebhookValidationDenied
}
func (e *WebhookValidationDeniedEvent) Timestamp() time.Time { return e.timestamp }

// WebhookValidationErrorEvent is published when validation encounters an error.
type WebhookValidationErrorEvent struct {
	RequestUID string
	Kind       string
	Error      string
	timestamp  time.Time
}

// NewWebhookValidationErrorEvent creates a new WebhookValidationErrorEvent.
func NewWebhookValidationErrorEvent(requestUID, kind, errorMsg string) *WebhookValidationErrorEvent {
	return &WebhookValidationErrorEvent{
		RequestUID: requestUID,
		Kind:       kind,
		Error:      errorMsg,
		timestamp:  time.Now(),
	}
}

func (e *WebhookValidationErrorEvent) EventType() string    { return EventTypeWebhookValidationError }
func (e *WebhookValidationErrorEvent) Timestamp() time.Time { return e.timestamp }

// -----------------------------------------------------------------------------
// Leader Election Events.
// -----------------------------------------------------------------------------

// LeaderElectionStartedEvent is published when leader election is initiated.
type LeaderElectionStartedEvent struct {
	Identity       string
	LeaseName      string
	LeaseNamespace string
	timestamp      time.Time
}

// NewLeaderElectionStartedEvent creates a new LeaderElectionStartedEvent.
func NewLeaderElectionStartedEvent(identity, leaseName, leaseNamespace string) *LeaderElectionStartedEvent {
	return &LeaderElectionStartedEvent{
		Identity:       identity,
		LeaseName:      leaseName,
		LeaseNamespace: leaseNamespace,
		timestamp:      time.Now(),
	}
}

func (e *LeaderElectionStartedEvent) EventType() string    { return EventTypeLeaderElectionStarted }
func (e *LeaderElectionStartedEvent) Timestamp() time.Time { return e.timestamp }

// BecameLeaderEvent is published when this replica becomes the leader.
type BecameLeaderEvent struct {
	Identity  string
	timestamp time.Time
}

// NewBecameLeaderEvent creates a new BecameLeaderEvent.
func NewBecameLeaderEvent(identity string) *BecameLeaderEvent {
	return &BecameLeaderEvent{
		Identity:  identity,
		timestamp: time.Now(),
	}
}

func (e *BecameLeaderEvent) EventType() string    { return EventTypeBecameLeader }
func (e *BecameLeaderEvent) Timestamp() time.Time { return e.timestamp }

// LostLeadershipEvent is published when this replica loses leadership.
type LostLeadershipEvent struct {
	Identity  string
	Reason    string // graceful_shutdown, lease_expired, etc.
	timestamp time.Time
}

// NewLostLeadershipEvent creates a new LostLeadershipEvent.
func NewLostLeadershipEvent(identity, reason string) *LostLeadershipEvent {
	return &LostLeadershipEvent{
		Identity:  identity,
		Reason:    reason,
		timestamp: time.Now(),
	}
}

func (e *LostLeadershipEvent) EventType() string    { return EventTypeLostLeadership }
func (e *LostLeadershipEvent) Timestamp() time.Time { return e.timestamp }

// NewLeaderObservedEvent is published when a new leader is observed.
type NewLeaderObservedEvent struct {
	NewLeaderIdentity string
	IsSelf            bool // true if this replica is the new leader
	timestamp         time.Time
}

// NewNewLeaderObservedEvent creates a new NewLeaderObservedEvent.
func NewNewLeaderObservedEvent(newLeaderIdentity string, isSelf bool) *NewLeaderObservedEvent {
	return &NewLeaderObservedEvent{
		NewLeaderIdentity: newLeaderIdentity,
		IsSelf:            isSelf,
		timestamp:         time.Now(),
	}
}

func (e *NewLeaderObservedEvent) EventType() string    { return EventTypeNewLeaderObserved }
func (e *NewLeaderObservedEvent) Timestamp() time.Time { return e.timestamp }

// -----------------------------------------------------------------------------
// HTTP Resource Events.
// -----------------------------------------------------------------------------

// HTTPResourceUpdatedEvent is published when HTTP resource content has changed.
// This triggers a reconciliation cycle with the new content as "pending".
// The content must pass validation before being promoted to "accepted".
type HTTPResourceUpdatedEvent struct {
	URL             string // The URL that was refreshed
	ContentChecksum string // SHA256 checksum of new content
	ContentSize     int    // Size of new content in bytes
	timestamp       time.Time
}

// NewHTTPResourceUpdatedEvent creates a new HTTPResourceUpdatedEvent.
func NewHTTPResourceUpdatedEvent(url, checksum string, size int) *HTTPResourceUpdatedEvent {
	return &HTTPResourceUpdatedEvent{
		URL:             url,
		ContentChecksum: checksum,
		ContentSize:     size,
		timestamp:       time.Now(),
	}
}

func (e *HTTPResourceUpdatedEvent) EventType() string    { return EventTypeHTTPResourceUpdated }
func (e *HTTPResourceUpdatedEvent) Timestamp() time.Time { return e.timestamp }

// HTTPResourceAcceptedEvent is published when pending HTTP content passes validation.
// The content has been promoted from "pending" to "accepted" state.
type HTTPResourceAcceptedEvent struct {
	URL             string // The URL whose content was accepted
	ContentChecksum string // SHA256 checksum of accepted content
	ContentSize     int    // Size of accepted content in bytes
	timestamp       time.Time
}

// NewHTTPResourceAcceptedEvent creates a new HTTPResourceAcceptedEvent.
func NewHTTPResourceAcceptedEvent(url, checksum string, size int) *HTTPResourceAcceptedEvent {
	return &HTTPResourceAcceptedEvent{
		URL:             url,
		ContentChecksum: checksum,
		ContentSize:     size,
		timestamp:       time.Now(),
	}
}

func (e *HTTPResourceAcceptedEvent) EventType() string    { return EventTypeHTTPResourceAccepted }
func (e *HTTPResourceAcceptedEvent) Timestamp() time.Time { return e.timestamp }

// HTTPResourceRejectedEvent is published when pending HTTP content fails validation.
// The old accepted content remains in use.
type HTTPResourceRejectedEvent struct {
	URL             string // The URL whose content was rejected
	ContentChecksum string // SHA256 checksum of rejected content
	Reason          string // Why the content was rejected
	timestamp       time.Time
}

// NewHTTPResourceRejectedEvent creates a new HTTPResourceRejectedEvent.
func NewHTTPResourceRejectedEvent(url, checksum, reason string) *HTTPResourceRejectedEvent {
	return &HTTPResourceRejectedEvent{
		URL:             url,
		ContentChecksum: checksum,
		Reason:          reason,
		timestamp:       time.Now(),
	}
}

func (e *HTTPResourceRejectedEvent) EventType() string    { return EventTypeHTTPResourceRejected }
func (e *HTTPResourceRejectedEvent) Timestamp() time.Time { return e.timestamp }
