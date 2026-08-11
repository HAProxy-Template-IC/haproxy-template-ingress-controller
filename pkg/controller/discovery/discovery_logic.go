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
	"sync"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
)

// triggerDiscovery performs endpoint discovery with version filtering and publishes the results.
//
// This method:
//  1. Calls the pure Discovery component to discover candidate pods
//  2. Filters candidates by DataPlane API support and HAProxy series compatibility
//  3. Caches version admission proofs for exact endpoint identities
//  4. Schedules retries for pods with transient version check failures
//  5. Permanently rejects pods with incompatible versions
//  6. Publishes HAProxyPodTerminatedEvent for removed pods
//  7. Publishes HAProxyPodsDiscoveredEvent with version-validated endpoints
func (c *Component) triggerDiscovery(source string) {
	c.discoveryMu.Lock()
	defer c.discoveryMu.Unlock()

	c.mu.RLock()
	ctx := c.lifecycleCtx
	discovery := c.discovery
	podStore := c.podStore
	hasCredentials := c.hasCredentials
	hasDataplanePort := c.hasDataplanePort
	credentialsValue := c.credentials
	c.mu.RUnlock()

	if ctx == nil || ctx.Err() != nil || discovery == nil || podStore == nil || !hasCredentials || !hasDataplanePort || credentialsValue == nil {
		return
	}
	credentials := *credentialsValue
	c.Logger().Debug("Triggering HAProxy pod discovery", "source", source)

	// Call pure Discovery component with logger for debugging
	candidates, err := discovery.DiscoverEndpointsWithLogger(podStore, credentials, c.Logger())
	if err != nil {
		c.Logger().Error("Discovery failed", "error", err)
		return
	}

	c.Logger().Debug("Discovered candidate pods", "count", len(candidates))

	// Build map of current candidates for tracking removals
	currentCandidates := make(map[endpointIdentity]struct{}, len(candidates))
	for i := range candidates {
		currentCandidates[endpointIdentityOf(&candidates[i])] = struct{}{}
	}

	// Clean up state for removed pods
	c.cleanupRemovedPods(currentCandidates)
	// Retire changed authorities before a replacement can block in its version probe.
	if retained, changed := c.retainProvenAuthorities(candidates); changed {
		c.publishDiscoveryResult(source, len(candidates), retained, nil)
	}

	// Filter candidates by version compatibility. Rejections are published
	// as HAProxyPodRejectedEvent below (outside the state mutex) so the
	// metrics component can increment haptic_haproxy_pods_rejected_total.
	admittedEndpoints, rejections := c.filterByVersion(ctx, candidates)
	if ctx.Err() != nil {
		return
	}
	c.publishDiscoveryResult(source, len(candidates), admittedEndpoints, rejections)
}

func (c *Component) retainProvenAuthorities(candidates []dataplane.Endpoint) ([]*dataplane.Endpoint, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if len(c.lastEndpoints) == 0 {
		return nil, false
	}
	retained := make([]*dataplane.Endpoint, 0, len(c.lastEndpoints))
	for i := range candidates {
		candidate := &candidates[i]
		previous, exists := c.lastEndpoints[podIdentity{
			podNamespace: candidate.PodNamespace,
			podName:      candidate.PodName,
		}]
		if !exists || previous.identity != endpointIdentityOf(candidate) ||
			previous.username != candidate.Username || previous.password != candidate.Password {
			continue
		}
		retainedCandidate := *candidate
		proof := versionAdmissionProof{
			dataPlaneAPI: dataplane.Version{
				Major: previous.detectedMajorVersion,
				Minor: previous.detectedMinorVersion,
				Full:  previous.detectedFullVersion,
			},
		}
		retained = append(retained, applyVersionProof(&retainedCandidate, &proof))
	}
	return retained, len(retained) != len(c.lastEndpoints)
}

type terminatedEndpoint struct {
	podName      string
	podNamespace string
	podUID       string
}

func (c *Component) publishDiscoveryResult(source string, candidateCount int, admittedEndpoints []*dataplane.Endpoint, rejections []rejection) {
	for _, r := range rejections {
		c.EventBus().Publish(events.NewHAProxyPodRejectedEvent(r.podName, r.reason))
	}

	currentEndpoints := make(map[podIdentity]endpointAuthority, len(admittedEndpoints))
	for _, ep := range admittedEndpoints {
		currentEndpoints[podIdentity{podNamespace: ep.PodNamespace, podName: ep.PodName}] = endpointAuthorityOf(ep)
	}

	terminated := make([]terminatedEndpoint, 0)
	c.mu.Lock()
	previousCount := len(c.lastEndpoints)
	for identity := range c.lastEndpoints {
		previous := c.lastEndpoints[identity]
		current, exists := currentEndpoints[identity]
		if !exists || current != previous {
			terminated = append(terminated, terminatedEndpoint{
				podName:      previous.identity.podName,
				podNamespace: previous.identity.podNamespace,
				podUID:       previous.identity.podUID,
			})
		}
	}
	c.lastEndpoints = currentEndpoints
	c.mu.Unlock()

	log := c.Logger().Debug
	if len(admittedEndpoints) > 0 || len(admittedEndpoints) != previousCount {
		log = c.Logger().Info
	}
	log("Discovered HAProxy pods",
		"source", source,
		"candidates", candidateCount,
		"admitted", len(admittedEndpoints))

	for _, endpoint := range terminated {
		c.Logger().Info("Detected pod termination",
			"pod_name", endpoint.podName,
			"pod_namespace", endpoint.podNamespace)
		c.EventBus().Publish(events.NewHAProxyPodTerminatedEvent(endpoint.podName, endpoint.podNamespace, endpoint.podUID))
	}

	endpointValues := make([]dataplane.Endpoint, len(admittedEndpoints))
	for i, ep := range admittedEndpoints {
		endpointValues[i] = *ep
	}

	event := events.NewHAProxyPodsDiscoveredEvent(endpointValues, len(admittedEndpoints))
	c.discoveredReplayer.Cache(event)
	c.EventBus().Publish(event)
}

// rejection captures a pod rejected during admission, accumulated under
// the state mutex and published as HAProxyPodRejectedEvent after the
// lock is released (avoids fanning out events while holding the lock).
type rejection struct {
	podName string
	reason  string
}

// filterByVersion filters candidate endpoints by version compatibility.
//
// For each candidate:
//   - If the candidate identity has an admission proof, reuse its versions
//   - If no admission proof exists, prove the DataPlane API and HAProxy versions
//   - If version check fails, add to pending retries
//   - Admit only a supported DataPlane API major and matching HAProxy series
//
// Returns the admitted endpoint set and the list of rejections. Rejections
// are published as HAProxyPodRejectedEvent by the caller (after the mutex
// is released) so operators can alert on persistent rejections via the
// haptic_haproxy_pods_rejected_total Prometheus counter.
func (c *Component) filterByVersion(
	ctx context.Context,
	candidates []dataplane.Endpoint,
) ([]*dataplane.Endpoint, []rejection) {
	admitted := make([]*dataplane.Endpoint, 0, len(candidates))
	var rejections []rejection

	c.mu.Lock()
	defer c.mu.Unlock()

	for i := range candidates {
		if ctx.Err() != nil {
			break
		}
		candidate := &candidates[i]
		podName := candidate.PodName
		identity := endpointIdentityOf(candidate)

		if proof, exists := c.admissionProofs[identity]; exists {
			c.Logger().Debug("Pod already admitted, using cached version proofs",
				"pod", podName,
				"dataplane_api_version", proof.dataPlaneAPI.Full,
				"haproxy_version", proof.haproxy.Full)
			admitted = append(admitted, applyVersionProof(candidate, &proof))
			continue
		}
		if reason, exists := c.versionRejections[identity]; exists {
			rejections = append(rejections, rejection{podName: podName, reason: reason})
			continue
		}
		if retry, exists := c.pendingRetries[identity]; exists && time.Now().Before(retry.lastAttempt.Add(backoffInterval(retry.retryCount))) {
			continue
		}

		remoteProof, err := c.checkRemoteVersions(ctx, candidate)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			// Version check failed - add to pending retries
			c.handleVersionCheckFailure(&identity, err)
			rejections = append(rejections, rejection{podName: podName, reason: "version_check_failed"})
			continue
		}

		if remoteProof.dataPlaneAPI.Major != client.SupportedDataPlaneAPIMajor {
			expectedAPI := dataplane.Version{
				Major: client.SupportedDataPlaneAPIMajor,
				Full:  fmt.Sprintf("v%d.x", client.SupportedDataPlaneAPIMajor),
			}
			rejections = append(rejections, c.rejectVersionMismatchLocked(
				&identity, &remoteProof.dataPlaneAPI, &expectedAPI, "DataPlane API"))
			continue
		}
		if remoteProof.haproxy.Compare(c.localVersion) != 0 {
			rejections = append(rejections, c.rejectVersionMismatchLocked(
				&identity, &remoteProof.haproxy, c.localVersion, "HAProxy"))
			continue
		}

		admittedEndpoint := applyVersionProof(candidate, &remoteProof)

		c.Logger().Info("Pod admitted with matching version",
			"pod", podName,
			"dataplane_api_version", remoteProof.dataPlaneAPI.Full,
			"haproxy_version", remoteProof.haproxy.Full)

		c.admissionProofs[identity] = remoteProof
		delete(c.versionRejections, identity)

		delete(c.pendingRetries, identity)

		admitted = append(admitted, admittedEndpoint)
	}

	// Schedule retry timer if there are pending pods
	if ctx.Err() == nil {
		c.scheduleRetryTimerLocked()
	}

	return admitted, rejections
}

func (c *Component) rejectVersionMismatchLocked(
	identity *endpointIdentity,
	remoteVersion *dataplane.Version,
	expectedVersion *dataplane.Version,
	versionSource string,
) rejection {
	direction := "older"
	if remoteVersion.Compare(expectedVersion) > 0 {
		direction = "newer"
	}
	reason := "version_mismatch_" + direction
	c.versionRejections[*identity] = reason
	delete(c.pendingRetries, *identity)
	c.Logger().Error("Rejecting pod: remote version is incompatible",
		"pod", identity.podName,
		"version_source", versionSource,
		"remote_version", remoteVersion.Full,
		"expected_version", expectedVersion.Full,
		"remote_major", remoteVersion.Major,
		"remote_minor", remoteVersion.Minor,
		"expected_major", expectedVersion.Major,
		"expected_minor", expectedVersion.Minor,
		"direction", direction)
	return rejection{podName: identity.podName, reason: reason}
}

func (c *Component) checkRemoteVersions(ctx context.Context, endpoint *dataplane.Endpoint) (versionAdmissionProof, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	clientEndpoint := &client.Endpoint{
		URL:      endpoint.URL,
		Username: endpoint.Username,
		Password: endpoint.Password,
		PodName:  endpoint.PodName,
	}

	versionInfo, err := client.DetectVersion(ctx, clientEndpoint, c.Logger())
	if err != nil {
		return versionAdmissionProof{}, fmt.Errorf("detecting DataPlane API version for pod %s: %w", endpoint.PodName, err)
	}
	apiVersion, err := dataplane.VersionFromAPIInfo(versionInfo)
	if err != nil {
		return versionAdmissionProof{}, fmt.Errorf("parsing DataPlane API version for pod %s: %w", endpoint.PodName, err)
	}
	proof := versionAdmissionProof{dataPlaneAPI: *apiVersion}
	if apiVersion.Major != client.SupportedDataPlaneAPIMajor {
		return proof, nil
	}

	haproxyVersionInfo, err := client.DetectHAProxyVersion(ctx, clientEndpoint, c.Logger())
	if err != nil {
		return versionAdmissionProof{}, fmt.Errorf("detecting HAProxy version for pod %s: %w", endpoint.PodName, err)
	}
	haproxyVersion, err := client.ParseVersion(haproxyVersionInfo.Info.Version)
	if err != nil {
		return versionAdmissionProof{}, fmt.Errorf("parsing HAProxy version for pod %s: %w", endpoint.PodName, err)
	}
	proof.haproxy = *haproxyVersion

	return proof, nil
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
func (c *Component) handleVersionCheckFailure(identity *endpointIdentity, err error) {
	retry, exists := c.pendingRetries[*identity]
	if !exists {
		retry = &retryState{}
		c.pendingRetries[*identity] = retry
	}

	retry.lastAttempt = time.Now()
	retry.retryCount++

	// Calculate next retry interval with exponential backoff
	interval := backoffInterval(retry.retryCount)

	c.Logger().Warn("Version check failed, will retry",
		"pod", identity.podName,
		"error", err,
		"retry_count", retry.retryCount,
		"next_retry_in", interval)
}

// cleanupRemovedPods removes state for pods that are no longer candidates.
func (c *Component) cleanupRemovedPods(currentCandidates map[endpointIdentity]struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Clean up admitted pods
	for identity := range c.admissionProofs {
		if _, exists := currentCandidates[identity]; !exists {
			c.Logger().Debug("Cleaning up state for removed pod", "pod", identity.podName)
			delete(c.admissionProofs, identity)
			delete(c.pendingRetries, identity)
		}
	}
	for identity := range c.versionRejections {
		if _, exists := currentCandidates[identity]; !exists {
			delete(c.versionRejections, identity)
		}
	}

	// Clean up pending retries for pods no longer candidates
	for identity := range c.pendingRetries {
		if _, exists := currentCandidates[identity]; !exists {
			delete(c.pendingRetries, identity)
		}
	}
}

// scheduleRetryTimerLocked schedules a timer to retry pending version checks.
// Must be called with c.mu held.
func (c *Component) scheduleRetryTimerLocked() {
	if len(c.pendingRetries) == 0 {
		c.cancelRetryTimer()
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
	if c.retryTimerStopped {
		return
	}

	// Calculate delay (minimum 1 second to avoid tight loops)
	delay := max(time.Until(nextRetry), time.Second)
	fireAt := time.Now().Add(delay)
	if c.retryTimer != nil && !c.retryTimerAt.IsZero() && !c.retryTimerAt.After(fireAt) {
		return
	}

	c.Logger().Debug("Scheduling retry timer for pending pods",
		"pending_count", len(c.pendingRetries),
		"delay", delay)
	c.armRetryTimerLocked(delay)
}

func (c *Component) cancelRetryTimer() {
	c.retryTimerMu.Lock()
	defer c.retryTimerMu.Unlock()
	c.retryGeneration++
	if c.retryTimer != nil && c.retryTimer.Stop() && c.retryTimerDone != nil {
		c.retryTimerDone()
	}
	c.retryTimer = nil
	c.retryTimerAt = time.Time{}
	c.retryTimerDone = nil
}

func (c *Component) armRetryTimerLocked(delay time.Duration) {
	if c.retryTimer != nil {
		if c.retryTimer.Stop() && c.retryTimerDone != nil {
			c.retryTimerDone()
		}
	}
	c.retryGeneration++
	generation := c.retryGeneration

	c.retryCallbacks.Add(1)
	var doneOnce sync.Once
	done := func() {
		doneOnce.Do(c.retryCallbacks.Done)
	}
	c.retryTimerDone = done
	c.retryTimerAt = time.Now().Add(delay)
	c.retryTimer = time.AfterFunc(delay, func() {
		defer done()
		c.runRetryTimer(generation)
	})
}

func (c *Component) runRetryTimer(generation uint64) {
	c.retryTimerMu.Lock()
	if c.retryTimerStopped || generation != c.retryGeneration {
		c.retryTimerMu.Unlock()
		return
	}
	c.retryTimer = nil
	c.retryTimerAt = time.Time{}
	c.retryTimerDone = nil
	c.retryTimerMu.Unlock()

	c.handleRetryTimer()
}

// handleRetryTimer is called when the retry timer fires to re-check pending pods.
func (c *Component) handleRetryTimer() {
	c.Logger().Debug("Retry timer fired, re-triggering discovery for pending pods")

	// Get current state
	c.mu.RLock()
	podStore := c.podStore
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
		c.triggerDiscovery("retry_timer")
	} else {
		c.Logger().Warn("Retry timer fired but cannot discover pods, missing requirements",
			"has_credentials", hasCredentials,
			"has_dataplane_port", hasDataplanePort,
			"has_pod_store", podStore != nil)
	}
}
