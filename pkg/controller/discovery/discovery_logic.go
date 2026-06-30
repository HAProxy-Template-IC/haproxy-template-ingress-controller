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
	c.Logger().Debug("Triggering HAProxy pod discovery", "source", source)

	// Call pure Discovery component with logger for debugging
	candidates, err := c.discovery.DiscoverEndpointsWithLogger(podStore, credentials, c.Logger())
	if err != nil {
		c.Logger().Error("Discovery failed", "error", err)
		return
	}

	c.Logger().Debug("Discovered candidate pods", "count", len(candidates))

	// Build map of current candidates for tracking removals
	currentCandidates := make(map[string]string)
	for _, ep := range candidates {
		currentCandidates[ep.PodName] = ep.PodNamespace
	}

	// Clean up state for removed pods
	c.cleanupRemovedPods(currentCandidates)

	// Filter candidates by version compatibility. Rejections are published
	// as HAProxyPodRejectedEvent below (outside the discovery mutex) so the
	// metrics component can increment haptic_haproxy_pods_rejected_total.
	admittedEndpoints, rejections := c.filterByVersion(candidates, credentials)
	for _, r := range rejections {
		c.EventBus().Publish(events.NewHAProxyPodRejectedEvent(r.podName, r.reason))
	}

	// Log summary - only at INFO level when count changes or pods are admitted
	// This prevents log spam when repeatedly discovering the same empty/non-empty set
	c.mu.RLock()
	previousCount := len(c.lastEndpoints)
	c.mu.RUnlock()

	countChanged := len(admittedEndpoints) != previousCount
	if len(admittedEndpoints) > 0 || countChanged {
		c.Logger().Info("Discovered HAProxy pods",
			"source", source,
			"candidates", len(candidates),
			"admitted", len(admittedEndpoints))
	} else {
		c.Logger().Debug("Discovered HAProxy pods",
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
			c.Logger().Info("Detected pod termination",
				"pod_name", podName,
				"pod_namespace", podNamespace)

			// Publish HAProxyPodTerminatedEvent (without holding lock)
			c.mu.Unlock()
			c.EventBus().Publish(events.NewHAProxyPodTerminatedEvent(podName, podNamespace))
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
	c.EventBus().Publish(event)
}

// rejection captures a pod rejected during admission, accumulated under
// the discovery mutex and published as HAProxyPodRejectedEvent after the
// lock is released (avoids fanning out events while holding the lock).
type rejection struct {
	podName string
	reason  string
}

// filterByVersion filters candidate endpoints by version compatibility.
//
// For each candidate:
//   - If already admitted, return cached endpoint (skip version check)
//   - If new pod, check remote version via /v3/info
//   - If version check fails, add to pending retries
//   - If the remote DataPlane API major version matches the controller's
//     series, admit and cache version info; otherwise permanently reject
//
// Returns the admitted endpoint set and the list of rejections. Rejections
// are published as HAProxyPodRejectedEvent by the caller (after the mutex
// is released) so operators can alert on persistent rejections via the
// haptic_haproxy_pods_rejected_total Prometheus counter.
func (c *Component) filterByVersion(candidates []dataplane.Endpoint, credentials coreconfig.Credentials) ([]*dataplane.Endpoint, []rejection) {
	admitted := make([]*dataplane.Endpoint, 0, len(candidates))
	var rejections []rejection

	c.mu.Lock()
	defer c.mu.Unlock()

	for i := range candidates {
		candidate := &candidates[i]
		podName := candidate.PodName

		// Check if already admitted
		if cachedEndpoint, exists := c.admittedPods[podName]; exists {
			c.Logger().Debug("Pod already admitted, using cached version",
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
			rejections = append(rejections, rejection{podName: podName, reason: "version_check_failed"})
			continue
		}

		// Admit when the remote DataPlane API major version matches the
		// controller's series. We compare MAJOR ONLY, deliberately: the pod's
		// reported version (remoteVersion, from /v3/info) is the DataPlane API
		// version, while c.localVersion is the controller's `haproxy -v` binary
		// version. As of HAProxy 3.4 these decouple — the 3.4 image ships
		// DataPlane API v3.3 — so they no longer share a minor, and a strict
		// major.minor match would wrongly reject a correctly-paired 3.4 fleet.
		// The controller's DataPlane API client supports every v3 minor (newer
		// ones clamp down), and the chart pins the controller image and the
		// HAProxy pods to the same series, so the major is the right gate; a
		// different major (v2/v4) is genuinely unsupported.
		if remoteVersion.Major != c.localVersion.Major {
			// Version mismatch - permanently reject
			direction := "older"
			if remoteVersion.Major > c.localVersion.Major {
				direction = "newer"
			}
			c.Logger().Error("Rejecting pod: remote HAProxy major version does not match local series",
				"pod", podName,
				"remote_version", remoteVersion.Full,
				"local_version", c.localVersion.Full,
				"remote_major", remoteVersion.Major,
				"remote_minor", remoteVersion.Minor,
				"local_major", c.localVersion.Major,
				"local_minor", c.localVersion.Minor,
				"direction", direction)
			rejections = append(rejections, rejection{
				podName: podName,
				reason:  "version_mismatch_" + direction,
			})
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

		c.Logger().Info("Pod admitted with matching version",
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

	return admitted, rejections
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
	versionInfo, err := client.DetectVersion(ctx, clientEndpoint, c.Logger())
	if err != nil {
		return nil, fmt.Errorf("detecting version for pod %s: %w", endpoint.PodName, err)
	}

	// Convert to Version struct
	version, err := dataplane.VersionFromAPIInfo(versionInfo)
	if err != nil {
		return nil, fmt.Errorf("parsing version for pod %s: %w", endpoint.PodName, err)
	}

	return version, nil
}

// backoffInterval computes the retry interval for the given retry count using
// exponential backoff, clamped to maxRetryInterval.
func backoffInterval(retryCount int) time.Duration {
	interval := initialRetryInterval
	for range retryCount - 1 {
		interval *= retryBackoffFactor
		if interval > maxRetryInterval {
			interval = maxRetryInterval
			break
		}
	}
	return interval
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
	interval := backoffInterval(retry.retryCount)

	c.Logger().Warn("Version check failed, will retry",
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
			c.Logger().Debug("Cleaning up state for removed pod", "pod", podName)
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
		interval := backoffInterval(retry.retryCount)

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
	delay := max(time.Until(nextRetry), time.Second)

	c.Logger().Debug("Scheduling retry timer for pending pods",
		"pending_count", len(c.pendingRetries),
		"delay", delay)

	c.retryTimer = time.AfterFunc(delay, func() {
		c.handleRetryTimer()
	})
}

// handleRetryTimer is called when the retry timer fires to re-check pending pods.
func (c *Component) handleRetryTimer() {
	c.Logger().Debug("Retry timer fired, re-triggering discovery for pending pods")

	// Get current state
	c.mu.RLock()
	podStore := c.podStore
	credentials := c.credentials
	hasCredentials := c.hasCredentials
	hasDataplanePort := c.hasDataplanePort
	pendingCount := len(c.pendingRetries)
	c.mu.RUnlock()

	if pendingCount == 0 {
		c.Logger().Debug("No pending pods to retry")
		return
	}

	// Trigger discovery if we have everything
	if hasCredentials && hasDataplanePort && podStore != nil {
		c.triggerDiscovery(podStore, *credentials, "retry_timer")
	} else {
		c.Logger().Warn("Retry timer fired but cannot discover pods, missing requirements",
			"has_credentials", hasCredentials,
			"has_dataplane_port", hasDataplanePort,
			"has_pod_store", podStore != nil)
	}
}
