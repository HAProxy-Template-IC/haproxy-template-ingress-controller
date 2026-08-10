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
	"fmt"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/names"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

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
		c.Logger().Debug("TryInitialDiscovery: already done, skipping", "source", source)
		return
	}

	// Check all requirements
	if !c.initialSyncComplete {
		c.mu.Unlock()
		c.Logger().Debug("TryInitialDiscovery: initial sync not complete", "source", source)
		return
	}

	if !c.hasCredentials || !c.hasDataplanePort || c.podStore == nil {
		c.mu.Unlock()
		c.Logger().Debug("TryInitialDiscovery: missing requirements",
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

	c.Logger().Debug("Performing initial discovery", "source", source)
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
		c.Logger().Error("Invalid config type in ConfigValidatedEvent",
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

	c.Logger().Debug("Config validated, updated dataplane port",
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
		c.Logger().Error("Invalid credentials type in CredentialsUpdatedEvent",
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

	c.Logger().Debug("Credentials updated", "secret_version", event.SecretVersion)

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
	if event.ResourceTypeName != names.HAProxyPodsResourceType {
		return
	}

	// Skip initial sync events (wait for ResourceSyncCompleteEvent)
	if event.ChangeStats.IsInitialSync {
		c.Logger().Debug("Skipping initial sync event for haproxy-pods")
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

	c.Logger().Debug("HAProxy pods changed",
		"created", event.ChangeStats.Created,
		"modified", event.ChangeStats.Modified,
		"deleted", event.ChangeStats.Deleted)

	// Trigger discovery if we have everything (for subsequent changes after initial)
	if hasCredentials && hasDataplanePort && podStore != nil {
		c.triggerDiscovery(podStore, *credentials, "resource_index_updated")
	} else {
		c.Logger().Debug("Skipping discovery, missing requirements",
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
	if event.ResourceTypeName != names.HAProxyPodsResourceType {
		return
	}

	c.Logger().Debug("HAProxy pods initial sync complete")

	// Set initialSyncComplete atomically and check for duplicate
	c.mu.Lock()
	if c.initialSyncComplete {
		// Already processed - skip duplicate
		c.mu.Unlock()
		c.Logger().Debug("Skipping duplicate ResourceSyncCompleteEvent for haproxy-pods")
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
		c.Logger().Debug("Became leader but no discovery result available yet, skipping state replay")
		return
	}

	c.Logger().Info("Became leader, re-publishing last discovered endpoints for deployment scheduler",
		"endpoint_count", event.Count)

	c.discoveredReplayer.Replay()
}

// handleDriftPrevention re-runs discovery from the pod store on every drift tick.
//
// Discovery is the only writer of the deployer's endpoint set and every other
// path into it is an edge: a dropped haproxy-pods ResourceIndexUpdatedEvent has
// no successor, so without this the new pod never receives config until an
// unrelated pod, config, or leadership change happens. Re-reading the store is a
// level read, so it repairs any missed edge — the same backstop the render path
// already gets from this event.
func (c *Component) handleDriftPrevention(_ *events.DriftPreventionTriggeredEvent) {
	c.mu.RLock()
	podStore := c.podStore
	credentials := c.credentials
	ready := c.hasCredentials && c.hasDataplanePort && c.initialDiscoveryDone
	c.mu.RUnlock()

	if !ready || podStore == nil {
		return
	}

	c.triggerDiscovery(podStore, *credentials, "drift_prevention")
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

	c.Logger().Debug("Pod store set")
}
