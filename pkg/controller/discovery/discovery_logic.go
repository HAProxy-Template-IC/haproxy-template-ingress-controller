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

package discovery

import (
	"context"
	"fmt"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

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
