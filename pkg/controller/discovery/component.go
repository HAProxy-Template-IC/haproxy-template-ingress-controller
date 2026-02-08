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

// Package discovery provides the Discovery event adapter component.
//
// This package wraps the pure Discovery component (pkg/dataplane/discovery)
// with event-driven coordination. It subscribes to configuration, credentials,
// and pod change events, and publishes discovered HAProxy endpoints.
package discovery

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/leadership"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

const (
	// ComponentName is the unique identifier for this component.
	ComponentName = "discovery"

	// EventBufferSize is the buffer size for event subscriptions.
	EventBufferSize = 100

	// Version check retry configuration.
	initialRetryInterval = 5 * time.Second
	maxRetryInterval     = 1 * time.Minute
	retryBackoffFactor   = 2
)

// retryState tracks retry information for pods pending version check.
type retryState struct {
	lastAttempt time.Time
	retryCount  int
}

// Component is the Discovery event adapter.
//
// This component:
//   - Subscribes to ConfigValidatedEvent, CredentialsUpdatedEvent, ResourceIndexUpdatedEvent, and BecameLeaderEvent
//   - Maintains current state (dataplanePort, credentials, podStore)
//   - Calls Discovery.DiscoverEndpoints() when relevant events occur
//   - Publishes HAProxyPodsDiscoveredEvent with discovered endpoints
//   - Publishes HAProxyPodTerminatedEvent when pods are removed
//
// Event Flow:
//  1. ConfigValidatedEvent → Update dataplanePort → Trigger discovery
//  2. CredentialsUpdatedEvent → Update credentials → Trigger discovery
//  3. ResourceIndexUpdatedEvent (haproxy-pods) → Trigger discovery
//  4. BecameLeaderEvent → Re-trigger discovery for new leader's DeploymentScheduler
//  5. Discovery completes → Compare with previous endpoints → Publish HAProxyPodTerminatedEvent for removed pods → Publish HAProxyPodsDiscoveredEvent
type Component struct {
	discovery *Discovery
	eventBus  *busevents.EventBus
	logger    *slog.Logger

	// Subscribed in constructor for proper startup synchronization
	eventChan <-chan busevents.Event

	// State replay for leadership transitions
	discoveredReplayer *leadership.StateReplayer[*events.HAProxyPodsDiscoveredEvent]

	// State protected by mutex
	mu                   sync.RWMutex
	dataplanePort        int
	credentials          *coreconfig.Credentials
	podStore             types.Store
	lastEndpoints        map[string]string // Map of PodName → PodNamespace for tracking removals
	hasCredentials       bool
	hasDataplanePort     bool
	initialSyncComplete  bool // Set when ResourceSyncCompleteEvent for haproxy-pods is received
	initialDiscoveryDone bool // Set after the first discovery is performed

	// Version filtering state
	localVersion   *dataplane.Version             // Local HAProxy version detected at startup
	admittedPods   map[string]*dataplane.Endpoint // Map of PodName → admitted Endpoint with cached version
	pendingRetries map[string]*retryState         // Map of PodName → retry state for pending pods

	// Retry timer for pending pods
	retryTimer   *time.Timer
	retryTimerMu sync.Mutex
}

// New creates a new Discovery event adapter component.
//
// Parameters:
//   - eventBus: The event bus for subscribing to and publishing events
//   - logger: Structured logger for observability
//
// Returns a configured Component ready to be started, or an error if
// local HAProxy version detection fails (which is fatal - the controller
// cannot start without knowing its local version for compatibility checking).
//
// Note: The Discovery pure component is created lazily when the dataplane port
// is configured via ConfigValidatedEvent. This constructor only detects the
// local HAProxy version for future compatibility checking.
func New(eventBus *busevents.EventBus, logger *slog.Logger) (*Component, error) {
	componentLogger := logger.With("component", ComponentName)

	// Detect local HAProxy version at startup (fatal if fails)
	localVersion, err := dataplane.DetectLocalVersion()
	if err != nil {
		return nil, fmt.Errorf("failed to detect local HAProxy version: %w", err)
	}

	componentLogger.Debug("detected local HAProxy version",
		"version", localVersion.Full,
		"major", localVersion.Major,
		"minor", localVersion.Minor)

	// Subscribe to EventBus during construction (before EventBus.Start())
	// This ensures proper startup synchronization without timing-based sleeps
	// Use typed subscription to only receive events we handle (reduces buffer pressure)
	eventChan := eventBus.SubscribeTypes(ComponentName, EventBufferSize,
		events.EventTypeConfigValidated,
		events.EventTypeCredentialsUpdated,
		events.EventTypeResourceIndexUpdated,
		events.EventTypeResourceSyncComplete,
		events.EventTypeBecameLeader,
	)

	return &Component{
		eventBus:           eventBus,
		logger:             componentLogger,
		eventChan:          eventChan,
		discoveredReplayer: leadership.NewStateReplayer[*events.HAProxyPodsDiscoveredEvent](eventBus),
		lastEndpoints:      make(map[string]string),
		localVersion:       localVersion,
		admittedPods:       make(map[string]*dataplane.Endpoint),
		pendingRetries:     make(map[string]*retryState),
	}, nil
}

// Name returns the unique identifier for this component.
// Implements the lifecycle.Component interface.
func (c *Component) Name() string {
	return ComponentName
}

// Start begins the Discovery component's event processing loop.
//
// This method:
//   - Maintains state from config and credential updates
//   - Triggers discovery when HAProxy pods change (via ResourceSyncCompleteEvent)
//   - Publishes discovered endpoints
//   - Runs until context is cancelled
//
// Returns an error if the event loop fails.
//
// Note: Event subscription occurs in the constructor (New()) to ensure proper
// startup synchronization. ResourceSyncCompleteEvent is buffered until EventBus.Start()
// is called, so no events are missed.
func (c *Component) Start(ctx context.Context) error {
	c.logger.Debug("discovery starting")

	for {
		select {
		case event := <-c.eventChan:
			c.handleEvent(event)

		case <-ctx.Done():
			c.logger.Info("Discovery shutting down", "reason", ctx.Err())
			return ctx.Err()
		}
	}
}

// handleEvent processes incoming events and triggers discovery as needed.
func (c *Component) handleEvent(event interface{}) {
	switch e := event.(type) {
	case *events.ConfigValidatedEvent:
		c.handleConfigValidated(e)

	case *events.CredentialsUpdatedEvent:
		c.handleCredentialsUpdated(e)

	case *events.ResourceIndexUpdatedEvent:
		c.handleResourceIndexUpdated(e)

	case *events.ResourceSyncCompleteEvent:
		c.handleResourceSyncComplete(e)

	case *events.BecameLeaderEvent:
		c.handleBecameLeader(e)
	}
}

// tryInitialDiscovery attempts to perform the initial discovery if all conditions are met.
// This function is called by multiple handlers (ConfigValidated, CredentialsUpdated,
// ResourceSyncComplete) to ensure exactly ONE initial discovery is performed at startup.
//
// Thread-safe: Uses mutex to ensure atomic check-and-set of initialDiscoveryDone.
func (c *Component) tryInitialDiscovery(source string) {
	c.mu.Lock()

	// Already done - skip
	if c.initialDiscoveryDone {
		c.mu.Unlock()
		c.logger.Debug("tryInitialDiscovery: already done, skipping", "source", source)
		return
	}

	// Check all requirements
	if !c.initialSyncComplete {
		c.mu.Unlock()
		c.logger.Debug("tryInitialDiscovery: initial sync not complete", "source", source)
		return
	}

	if !c.hasCredentials || !c.hasDataplanePort || c.podStore == nil {
		c.mu.Unlock()
		c.logger.Debug("tryInitialDiscovery: missing requirements",
			"source", source,
			"has_credentials", c.hasCredentials,
			"has_dataplane_port", c.hasDataplanePort,
			"has_pod_store", c.podStore != nil)
		return
	}

	// All conditions met - mark as done and capture state
	c.initialDiscoveryDone = true
	podStore := c.podStore
	credentials := c.credentials
	c.mu.Unlock()

	c.logger.Debug("performing initial discovery", "source", source)
	c.triggerDiscovery(podStore, *credentials, source)
}

// handleConfigValidated processes ConfigValidatedEvent.
//
// Updates dataplanePort from config and tries to trigger initial discovery.
// Also triggers re-discovery after initial discovery for subsequent config changes.
func (c *Component) handleConfigValidated(event *events.ConfigValidatedEvent) {
	// Type-assert config
	config, ok := event.Config.(*coreconfig.Config)
	if !ok {
		c.logger.Error("invalid config type in ConfigValidatedEvent",
			"expected", "*coreconfig.Config",
			"actual", fmt.Sprintf("%T", event.Config))
		return
	}

	c.mu.Lock()
	oldPort := c.dataplanePort
	c.dataplanePort = config.Dataplane.Port
	c.hasDataplanePort = true
	initialDiscoveryDone := c.initialDiscoveryDone

	// Recreate discovery instance with new port and local version
	c.discovery = &Discovery{
		dataplanePort: c.dataplanePort,
		localVersion:  c.localVersion,
	}

	// Capture state for re-discovery (after initial)
	podStore := c.podStore
	credentials := c.credentials
	hasCredentials := c.hasCredentials
	c.mu.Unlock()

	c.logger.Debug("config validated, updated dataplane port",
		"old_port", oldPort,
		"new_port", c.dataplanePort)

	// Try initial discovery (might be blocked by missing requirements)
	if !initialDiscoveryDone {
		c.tryInitialDiscovery("config_validated")
		return
	}

	// After initial discovery, trigger re-discovery for config changes
	if hasCredentials && podStore != nil {
		c.triggerDiscovery(podStore, *credentials, "config_validated")
	}
}

// handleCredentialsUpdated processes CredentialsUpdatedEvent.
//
// Updates credentials and tries to trigger initial discovery.
// Also triggers re-discovery after initial discovery for subsequent credential changes.
func (c *Component) handleCredentialsUpdated(event *events.CredentialsUpdatedEvent) {
	// Type-assert credentials
	credentials, ok := event.Credentials.(*coreconfig.Credentials)
	if !ok {
		c.logger.Error("invalid credentials type in CredentialsUpdatedEvent",
			"expected", "*coreconfig.Credentials",
			"actual", fmt.Sprintf("%T", event.Credentials))
		return
	}

	c.mu.Lock()
	c.credentials = credentials
	c.hasCredentials = true
	initialDiscoveryDone := c.initialDiscoveryDone

	// Capture state for re-discovery (after initial)
	podStore := c.podStore
	hasDataplanePort := c.hasDataplanePort
	c.mu.Unlock()

	c.logger.Debug("credentials updated", "secret_version", event.SecretVersion)

	// Try initial discovery (might be blocked by missing requirements)
	if !initialDiscoveryDone {
		c.tryInitialDiscovery("credentials_updated")
		return
	}

	// After initial discovery, trigger re-discovery for credential changes
	if hasDataplanePort && podStore != nil {
		c.triggerDiscovery(podStore, *credentials, "credentials_updated")
	}
}

// handleResourceIndexUpdated processes ResourceIndexUpdatedEvent.
//
// Triggers discovery when HAProxy pods change AFTER initial discovery is complete.
// This handler is for subsequent changes only, not for initial startup.
func (c *Component) handleResourceIndexUpdated(event *events.ResourceIndexUpdatedEvent) {
	// Only handle haproxy-pods resource type
	if event.ResourceTypeName != "haproxy-pods" {
		return
	}

	// Skip initial sync events (wait for ResourceSyncCompleteEvent)
	if event.ChangeStats.IsInitialSync {
		c.logger.Debug("skipping initial sync event for haproxy-pods")
		return
	}

	// Get current state
	c.mu.RLock()
	podStore := c.podStore
	credentials := c.credentials
	hasCredentials := c.hasCredentials
	hasDataplanePort := c.hasDataplanePort
	initialDiscoveryDone := c.initialDiscoveryDone
	c.mu.RUnlock()

	// If initial discovery hasn't been done yet, this event might be part of the startup
	// sequence arriving concurrently with other events. Try initial discovery which will
	// atomically check and set the flag to prevent duplicates.
	if !initialDiscoveryDone {
		// Try initial discovery - it will check atomically and only trigger once
		c.tryInitialDiscovery("resource_index_updated")
		return
	}

	c.logger.Debug("haproxy pods changed",
		"created", event.ChangeStats.Created,
		"modified", event.ChangeStats.Modified,
		"deleted", event.ChangeStats.Deleted)

	// Trigger discovery if we have everything (for subsequent changes after initial)
	if hasCredentials && hasDataplanePort && podStore != nil {
		c.triggerDiscovery(podStore, *credentials, "resource_index_updated")
	} else {
		c.logger.Debug("skipping discovery, missing requirements",
			"has_credentials", hasCredentials,
			"has_dataplane_port", hasDataplanePort,
			"has_pod_store", podStore != nil)
	}
}

// handleResourceSyncComplete processes ResourceSyncCompleteEvent.
//
// Sets the initialSyncComplete flag and tries to trigger initial discovery.
// Other handlers (ConfigValidated, CredentialsUpdated) also try initial discovery
// when they complete, ensuring discovery happens as soon as all requirements are met.
func (c *Component) handleResourceSyncComplete(event *events.ResourceSyncCompleteEvent) {
	// Only handle haproxy-pods resource type
	if event.ResourceTypeName != "haproxy-pods" {
		return
	}

	c.logger.Debug("haproxy pods initial sync complete")

	// Set initialSyncComplete atomically and check for duplicate
	c.mu.Lock()
	if c.initialSyncComplete {
		// Already processed - skip duplicate
		c.mu.Unlock()
		c.logger.Debug("skipping duplicate ResourceSyncCompleteEvent for haproxy-pods")
		return
	}
	c.initialSyncComplete = true
	c.mu.Unlock()

	// Try to perform initial discovery (might be blocked by missing credentials/config)
	c.tryInitialDiscovery("resource_sync_complete")
}

// handleBecameLeader processes BecameLeaderEvent.
//
// Re-publishes the cached HAProxyPodsDiscoveredEvent when this replica becomes leader.
// This ensures the DeploymentScheduler (which only starts on the leader) receives
// current endpoint state even if pods were discovered before leadership was acquired.
//
// Unlike other event handlers, this does NOT re-run discovery - it re-publishes
// the cached event to avoid duplicate work and duplicate log messages.
func (c *Component) handleBecameLeader(_ *events.BecameLeaderEvent) {
	event, ok := c.discoveredReplayer.Get()
	if !ok {
		c.logger.Debug("Became leader but no discovery result available yet, skipping state replay")
		return
	}

	c.logger.Info("Became leader, re-publishing last discovered endpoints for deployment scheduler",
		"endpoint_count", event.Count)

	c.discoveredReplayer.Replay()
}

// SetPodStore sets the pod store reference.
//
// This is called by the controller after creating the haproxy-pods resource watcher.
// It allows the Discovery component to access pod resources for endpoint discovery.
//
// Thread-safe.
func (c *Component) SetPodStore(store types.Store) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.podStore = store

	c.logger.Debug("pod store set")
}

// triggerDiscovery performs endpoint discovery with version filtering and publishes the results.
//
// This method:
//  1. Calls the pure Discovery component to discover candidate pods
//  2. Filters candidates by version compatibility (remote >= local)
//  3. Caches admitted endpoints for future discovery cycles
//  4. Schedules retries for pods with transient version check failures
//  5. Permanently rejects pods with incompatible versions
//  6. Publishes HAProxyPodTerminatedEvent for removed pods
//  7. Publishes HAProxyPodsDiscoveredEvent with version-validated endpoints
func (c *Component) triggerDiscovery(podStore types.Store, credentials coreconfig.Credentials, source string) {
	c.logger.Debug("triggering HAProxy pod discovery", "source", source)

	// Call pure Discovery component with logger for debugging
	candidates, err := c.discovery.DiscoverEndpointsWithLogger(podStore, credentials, c.logger)
	if err != nil {
		c.logger.Error("discovery failed", "error", err)
		return
	}

	c.logger.Debug("discovered candidate pods", "count", len(candidates))

	// Build map of current candidates for tracking removals
	currentCandidates := make(map[string]string)
	for _, ep := range candidates {
		currentCandidates[ep.PodName] = ep.PodNamespace
	}

	// Clean up state for removed pods
	c.cleanupRemovedPods(currentCandidates)

	// Filter candidates by version compatibility
	admittedEndpoints := c.filterByVersion(candidates, credentials)

	// Log summary - only at INFO level when count changes or pods are admitted
	// This prevents log spam when repeatedly discovering the same empty/non-empty set
	c.mu.RLock()
	previousCount := len(c.lastEndpoints)
	c.mu.RUnlock()

	countChanged := len(admittedEndpoints) != previousCount
	if len(admittedEndpoints) > 0 || countChanged {
		c.logger.Info("Discovered HAProxy pods",
			"source", source,
			"candidates", len(candidates),
			"admitted", len(admittedEndpoints))
	} else {
		c.logger.Debug("Discovered HAProxy pods",
			"source", source,
			"candidates", len(candidates),
			"admitted", len(admittedEndpoints))
	}

	// Build map of admitted endpoints for comparison
	currentEndpoints := make(map[string]string)
	for _, ep := range admittedEndpoints {
		currentEndpoints[ep.PodName] = ep.PodNamespace
	}

	// Detect removed pods (from admitted set) and publish termination events
	c.mu.Lock()
	for podName, podNamespace := range c.lastEndpoints {
		if _, exists := currentEndpoints[podName]; !exists {
			// Pod was removed from admitted set
			c.logger.Info("Detected pod termination",
				"pod_name", podName,
				"pod_namespace", podNamespace)

			// Publish HAProxyPodTerminatedEvent (without holding lock)
			c.mu.Unlock()
			c.eventBus.Publish(events.NewHAProxyPodTerminatedEvent(podName, podNamespace))
			c.mu.Lock()
		}
	}

	// Update last endpoints cache
	c.lastEndpoints = currentEndpoints
	c.mu.Unlock()

	// Dereference endpoint pointers for event (events use value types for immutability)
	endpointValues := make([]dataplane.Endpoint, len(admittedEndpoints))
	for i, ep := range admittedEndpoints {
		endpointValues[i] = *ep
	}

	// Create event and cache for state replay (used by handleBecameLeader)
	event := events.NewHAProxyPodsDiscoveredEvent(endpointValues, len(admittedEndpoints))
	c.discoveredReplayer.Cache(event)

	// Publish HAProxyPodsDiscoveredEvent
	c.eventBus.Publish(event)
}

// filterByVersion filters candidate endpoints by version compatibility.
//
// For each candidate:
//   - If already admitted, return cached endpoint (skip version check)
//   - If new pod, check remote version via /v3/info
//   - If version check fails, add to pending retries
//   - If remote < local, permanently reject
//   - If remote >= local, admit and cache version info
//   - If remote > local, log warning once
func (c *Component) filterByVersion(candidates []dataplane.Endpoint, credentials coreconfig.Credentials) []*dataplane.Endpoint {
	admitted := make([]*dataplane.Endpoint, 0, len(candidates))

	c.mu.Lock()
	defer c.mu.Unlock()

	for i := range candidates {
		candidate := &candidates[i]
		podName := candidate.PodName

		// Check if already admitted
		if cachedEndpoint, exists := c.admittedPods[podName]; exists {
			c.logger.Debug("pod already admitted, using cached version",
				"pod", podName,
				"version", cachedEndpoint.DetectedFullVersion)
			admitted = append(admitted, cachedEndpoint)
			continue
		}

		// New pod - check remote version
		remoteVersion, err := c.checkRemoteVersion(candidate)
		if err != nil {
			// Version check failed - add to pending retries
			c.handleVersionCheckFailure(podName, err)
			continue
		}

		// Compare versions: remote must match local (major.minor)
		comparison := remoteVersion.Compare(c.localVersion)
		if comparison != 0 {
			// Version mismatch - permanently reject
			direction := "older"
			if comparison > 0 {
				direction = "newer"
			}
			c.logger.Error("rejecting pod: remote HAProxy version does not match local",
				"pod", podName,
				"remote_version", remoteVersion.Full,
				"local_version", c.localVersion.Full,
				"remote_major", remoteVersion.Major,
				"remote_minor", remoteVersion.Minor,
				"local_major", c.localVersion.Major,
				"local_minor", c.localVersion.Minor,
				"direction", direction)
			// Don't add to pending retries - version mismatch is permanent
			// K8s pods are replaced on upgrade, not mutated
			continue
		}

		// Version matches - admit pod
		admittedEndpoint := &dataplane.Endpoint{
			URL:                  candidate.URL,
			Username:             credentials.DataplaneUsername,
			Password:             credentials.DataplanePassword,
			PodName:              candidate.PodName,
			PodNamespace:         candidate.PodNamespace,
			DetectedMajorVersion: remoteVersion.Major,
			DetectedMinorVersion: remoteVersion.Minor,
			DetectedFullVersion:  remoteVersion.Full,
		}

		c.logger.Info("Pod admitted with matching version",
			"pod", podName,
			"version", remoteVersion.Full)

		// Cache admitted endpoint
		c.admittedPods[podName] = admittedEndpoint

		// Remove from pending retries if present
		delete(c.pendingRetries, podName)

		admitted = append(admitted, admittedEndpoint)
	}

	// Schedule retry timer if there are pending pods
	c.scheduleRetryTimerLocked()

	return admitted
}

// checkRemoteVersion checks the remote HAProxy version via /v3/info endpoint.
func (c *Component) checkRemoteVersion(endpoint *dataplane.Endpoint) (*dataplane.Version, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Create client endpoint for version detection
	clientEndpoint := &client.Endpoint{
		URL:      endpoint.URL,
		Username: endpoint.Username,
		Password: endpoint.Password,
		PodName:  endpoint.PodName,
	}

	// Call the exported DetectVersion function
	versionInfo, err := client.DetectVersion(ctx, clientEndpoint, c.logger)
	if err != nil {
		return nil, fmt.Errorf("failed to detect version for pod %s: %w", endpoint.PodName, err)
	}

	// Convert to Version struct
	version, err := dataplane.VersionFromAPIInfo(versionInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to parse version for pod %s: %w", endpoint.PodName, err)
	}

	return version, nil
}

// handleVersionCheckFailure handles transient version check failures.
func (c *Component) handleVersionCheckFailure(podName string, err error) {
	retry, exists := c.pendingRetries[podName]
	if !exists {
		retry = &retryState{}
		c.pendingRetries[podName] = retry
	}

	retry.lastAttempt = time.Now()
	retry.retryCount++

	// Calculate next retry interval with exponential backoff
	interval := initialRetryInterval
	for range retry.retryCount - 1 {
		interval *= retryBackoffFactor
		if interval > maxRetryInterval {
			interval = maxRetryInterval
			break
		}
	}

	c.logger.Warn("version check failed, will retry",
		"pod", podName,
		"error", err,
		"retry_count", retry.retryCount,
		"next_retry_in", interval)
}

// cleanupRemovedPods removes state for pods that are no longer candidates.
func (c *Component) cleanupRemovedPods(currentCandidates map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Clean up admitted pods
	for podName := range c.admittedPods {
		if _, exists := currentCandidates[podName]; !exists {
			c.logger.Debug("cleaning up state for removed pod", "pod", podName)
			delete(c.admittedPods, podName)
			delete(c.pendingRetries, podName)
		}
	}

	// Clean up pending retries for pods no longer candidates
	for podName := range c.pendingRetries {
		if _, exists := currentCandidates[podName]; !exists {
			delete(c.pendingRetries, podName)
		}
	}
}

// scheduleRetryTimerLocked schedules a timer to retry pending version checks.
// Must be called with c.mu held.
func (c *Component) scheduleRetryTimerLocked() {
	if len(c.pendingRetries) == 0 {
		return
	}

	// Find the next retry time
	var nextRetry time.Time
	for _, retry := range c.pendingRetries {
		// Calculate next retry time based on retry count
		interval := initialRetryInterval
		for range retry.retryCount - 1 {
			interval *= retryBackoffFactor
			if interval > maxRetryInterval {
				interval = maxRetryInterval
				break
			}
		}

		retryAt := retry.lastAttempt.Add(interval)
		if nextRetry.IsZero() || retryAt.Before(nextRetry) {
			nextRetry = retryAt
		}
	}

	// Schedule timer
	c.retryTimerMu.Lock()
	defer c.retryTimerMu.Unlock()

	// Stop existing timer if any
	if c.retryTimer != nil {
		c.retryTimer.Stop()
	}

	// Calculate delay (minimum 1 second to avoid tight loops)
	delay := time.Until(nextRetry)
	if delay < time.Second {
		delay = time.Second
	}

	c.logger.Debug("scheduling retry timer for pending pods",
		"pending_count", len(c.pendingRetries),
		"delay", delay)

	c.retryTimer = time.AfterFunc(delay, func() {
		c.handleRetryTimer()
	})
}

// handleRetryTimer is called when the retry timer fires to re-check pending pods.
func (c *Component) handleRetryTimer() {
	c.logger.Debug("retry timer fired, re-triggering discovery for pending pods")

	// Get current state
	c.mu.RLock()
	podStore := c.podStore
	credentials := c.credentials
	hasCredentials := c.hasCredentials
	hasDataplanePort := c.hasDataplanePort
	pendingCount := len(c.pendingRetries)
	c.mu.RUnlock()

	if pendingCount == 0 {
		c.logger.Debug("no pending pods to retry")
		return
	}

	// Trigger discovery if we have everything
	if hasCredentials && hasDataplanePort && podStore != nil {
		c.triggerDiscovery(podStore, *credentials, "retry_timer")
	} else {
		c.logger.Warn("retry timer fired but cannot discover pods, missing requirements",
			"has_credentials", hasCredentials,
			"has_dataplane_port", hasDataplanePort,
			"has_pod_store", podStore != nil)
	}
}
