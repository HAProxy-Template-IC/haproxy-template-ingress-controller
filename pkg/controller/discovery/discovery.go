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

// Package discovery provides HAProxy pod discovery functionality.
//
// This package implements pure business logic for discovering HAProxy pod endpoints
// based on pod resources from the Kubernetes API. It extracts pod IPs and constructs
// Dataplane API endpoints with credentials.
//
// This is a pure component with no event bus dependency - event coordination is
// handled by the adapter in pkg/controller/discovery.
package discovery

import (
	"context"
	"fmt"
	"log/slog"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/logging"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

// Discovery discovers HAProxy pod endpoints from Kubernetes resources.
//
// This is a pure component that takes a pod store and credentials and returns
// a list of Dataplane API endpoints. It has no knowledge of events or the
// event bus - that coordination is handled by the event adapter.
//
// The Discovery also holds the local HAProxy version, detected at startup,
// which is used by the event adapter for version compatibility checking.
type Discovery struct {
	dataplanePort int
	localVersion  *dataplane.Version
}

// newDiscoveryEngine creates a new Discovery instance.
//
// Parameters:
//   - dataplanePort: The port where Dataplane API is exposed on HAProxy pods
//
// Returns a configured Discovery instance and an error if local HAProxy version
// detection fails (which is fatal - the controller cannot start without knowing
// its local version for compatibility checking).
func newDiscoveryEngine(dataplanePort int) (*Discovery, error) {
	// Detect local HAProxy version at startup
	localVer, err := dataplane.DetectLocalVersion()
	if err != nil {
		return nil, fmt.Errorf("failed to detect local HAProxy version: %w", err)
	}

	return &Discovery{
		dataplanePort: dataplanePort,
		localVersion:  localVer,
	}, nil
}

// LocalVersion returns the detected local HAProxy version.
// This is used by the event adapter for version compatibility checking.
func (d *Discovery) LocalVersion() *dataplane.Version {
	return d.localVersion
}

// isDataplaneContainerReady checks if the container exposing the dataplane port is ready.
//
// This method:
//   - Finds which container has the dataplane port in spec.containers[].ports
//   - Checks that container's ready status in status.containerStatuses[]
//
// Returns true only if the dataplane container exists and is ready.
//
//nolint:gocyclo,revive // Complex pod status checking required for robust discovery
func (d *Discovery) isDataplaneContainerReady(pod *unstructured.Unstructured, logger *slog.Logger) (bool, error) {
	// Step 1: Find which container has the dataplane port
	containersSpec, found, err := unstructured.NestedSlice(pod.Object, "spec", "containers")
	if err != nil || !found {
		return false, fmt.Errorf("failed to get containers spec: %w", err)
	}

	var dataplaneContainerName string
	for _, c := range containersSpec {
		container, ok := c.(map[string]interface{})
		if !ok {
			continue
		}

		// Get container name
		name, found, err := unstructured.NestedString(container, "name")
		if err != nil || !found {
			continue
		}

		// Check if this container has the dataplane port
		ports, found, err := unstructured.NestedSlice(container, "ports")
		if err != nil || !found {
			continue
		}

		for _, p := range ports {
			port, ok := p.(map[string]interface{})
			if !ok {
				continue
			}

			containerPort, found, err := unstructured.NestedInt64(port, "containerPort")
			if err != nil || !found {
				continue
			}

			if int(containerPort) == d.dataplanePort {
				dataplaneContainerName = name
				break
			}
		}

		if dataplaneContainerName != "" {
			break
		}
	}

	if dataplaneContainerName == "" {
		return false, fmt.Errorf("no container found with dataplane port %d", d.dataplanePort)
	}

	if logger != nil {
		logger.Log(context.Background(), logging.LevelTrace, "Found dataplane container in spec",
			"pod", pod.GetName(),
			"container", dataplaneContainerName,
			"port", d.dataplanePort)
	}

	// Step 2: Check that container's ready status
	containerStatuses, found, err := unstructured.NestedSlice(pod.Object, "status", "containerStatuses")
	if err != nil || !found {
		// No container statuses yet
		if logger != nil {
			logger.Log(context.Background(), logging.LevelTrace, "No containerStatuses found in pod status",
				"pod", pod.GetName(),
				"error", err)
		}
		return false, nil
	}

	for _, cs := range containerStatuses {
		status, ok := cs.(map[string]interface{})
		if !ok {
			continue
		}

		name, found, err := unstructured.NestedString(status, "name")
		if err != nil || !found {
			continue
		}

		if name == dataplaneContainerName {
			ready, found, err := unstructured.NestedBool(status, "ready")

			// Trace logging to investigate connection refused despite ready status
			if logger != nil {
				started, _, _ := unstructured.NestedBool(status, "started")
				restartCount, _, _ := unstructured.NestedInt64(status, "restartCount")

				// Extract state information
				state, stateFound, _ := unstructured.NestedMap(status, "state")
				var stateType string
				if stateFound {
					if _, ok := state["running"]; ok {
						stateType = "running"
					} else if _, ok := state["waiting"]; ok {
						stateType = "waiting"
					} else if _, ok := state["terminated"]; ok {
						stateType = "terminated"
					}
				}

				logger.Log(context.Background(), logging.LevelTrace, "Dataplane container status check",
					"pod", pod.GetName(),
					"container", name,
					"ready", ready,
					"ready_found", found,
					"ready_error", err,
					"started", started,
					"restart_count", restartCount,
					"state_type", stateType)
			}

			if err != nil {
				return false, fmt.Errorf("failed to get ready status: %w", err)
			}
			if !found {
				return false, nil
			}
			return ready, nil
		}
	}

	// Container not found in status (shouldn't happen)
	if logger != nil {
		logger.Log(context.Background(), logging.LevelTrace, "Dataplane container not found in containerStatuses",
			"pod", pod.GetName(),
			"expected_container", dataplaneContainerName)
	}
	return false, nil
}

// resourceToPod converts a store resource to *unstructured.Unstructured.
//
// Resources in stores may be either:
//   - *unstructured.Unstructured (legacy format, used in some tests)
//   - map[string]interface{} (production format after float-to-int conversion)
//
// Returns nil if the resource type is not supported.
func resourceToPod(resource interface{}) *unstructured.Unstructured {
	switch r := resource.(type) {
	case *unstructured.Unstructured:
		return r
	case map[string]interface{}:
		return &unstructured.Unstructured{Object: r}
	default:
		return nil
	}
}

// DiscoverEndpoints discovers HAProxy Dataplane API endpoints from pod resources.
//
// This method:
//   - Lists all pods from the provided store
//   - Extracts pod IPs from pod.status.podIP
//   - Checks that the dataplane container is ready
//   - Constructs Dataplane API URLs (http://{IP}:{port})
//   - Creates Endpoint structs with credentials
//
// Parameters:
//   - podStore: Store containing HAProxy pod resources
//   - credentials: Dataplane API credentials to use for all endpoints
//
// Returns:
//   - A slice of discovered Endpoint structs
//   - An error if discovery fails
//
// Example:
//
//	endpoints, err := discovery.DiscoverEndpoints(podStore, credentials)
//	if err != nil {
//	    return fmt.Errorf("discovery failed: %w", err)
//	}
//	// Use endpoints for HAProxy synchronization
func (d *Discovery) DiscoverEndpoints(
	podStore types.Store,
	credentials coreconfig.Credentials,
) ([]dataplane.Endpoint, error) {
	return d.DiscoverEndpointsWithLogger(podStore, credentials, nil)
}

// DiscoverEndpointsWithLogger is like DiscoverEndpoints but accepts an optional logger for debugging.
//
//nolint:revive // High cognitive complexity required for robust pod filtering and error handling
func (d *Discovery) DiscoverEndpointsWithLogger(
	podStore types.Store,
	credentials coreconfig.Credentials,
	logger *slog.Logger,
) ([]dataplane.Endpoint, error) {
	if podStore == nil {
		return nil, fmt.Errorf("pod store is nil")
	}

	// List all pods from store
	resources, err := podStore.List()
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}

	endpoints := make([]dataplane.Endpoint, 0, len(resources))

	for _, resource := range resources {
		// Convert resource to unstructured.Unstructured
		pod := resourceToPod(resource)
		if pod == nil {
			// Skip unsupported resource types
			continue
		}

		// Log pod evaluation start
		if logger != nil {
			logger.Log(context.Background(), logging.LevelTrace, "Evaluating pod for discovery",
				"pod", pod.GetName(),
				"namespace", pod.GetNamespace(),
				"uid", pod.GetUID())
		}

		// Skip terminating pods (pods with deletionTimestamp set)
		// Terminating pods may still have phase="Running" and ready=true during graceful shutdown,
		// but their ports are shutting down and will refuse connections
		if pod.GetDeletionTimestamp() != nil {
			if logger != nil {
				logger.Log(context.Background(), logging.LevelTrace, "Skipping terminating pod",
					"pod", pod.GetName(),
					"deletionTimestamp", pod.GetDeletionTimestamp())
			}
			continue
		}

		// Extract pod IP from status.podIP
		podIP, found, err := unstructured.NestedString(pod.Object, "status", "podIP")
		if err != nil {
			return nil, fmt.Errorf("failed to extract pod IP from %s: %w",
				pod.GetName(), err)
		}
		if !found || podIP == "" {
			// Skip pods without IP (not running yet)
			if logger != nil {
				logger.Log(context.Background(), logging.LevelTrace, "Skipping pod - no IP assigned",
					"pod", pod.GetName())
			}
			continue
		}

		// Extract pod phase from status.phase
		phase, found, err := unstructured.NestedString(pod.Object, "status", "phase")
		if err != nil {
			return nil, fmt.Errorf("failed to extract pod phase from %s: %w",
				pod.GetName(), err)
		}
		if !found || phase != "Running" {
			// Skip pods that aren't in Running phase
			if logger != nil {
				logger.Log(context.Background(), logging.LevelTrace, "Skipping pod - not in Running phase",
					"pod", pod.GetName(),
					"phase", phase)
			}
			continue
		}

		// Check if dataplane container is ready
		ready, err := d.isDataplaneContainerReady(pod, logger)
		if err != nil {
			return nil, fmt.Errorf("failed to check dataplane container readiness for %s: %w",
				pod.GetName(), err)
		}
		if !ready {
			// Skip pods where dataplane container isn't ready yet
			if logger != nil {
				logger.Log(context.Background(), logging.LevelTrace, "Skipping pod - dataplane container not ready",
					"pod", pod.GetName(),
					"podIP", podIP,
					"phase", phase)
			}
			continue
		}

		// Pod passed readiness check
		if logger != nil {
			logger.Log(context.Background(), logging.LevelTrace, "Including pod - dataplane container is ready",
				"pod", pod.GetName(),
				"podIP", podIP,
				"phase", phase)
		}

		// Construct Dataplane API URL with v3 prefix
		url := fmt.Sprintf("http://%s:%d/v3", podIP, d.dataplanePort)

		// Create endpoint with credentials
		endpoint := dataplane.Endpoint{
			URL:          url,
			Username:     credentials.DataplaneUsername,
			Password:     credentials.DataplanePassword,
			PodName:      pod.GetName(),
			PodNamespace: pod.GetNamespace(),
		}

		endpoints = append(endpoints, endpoint)
	}

	return endpoints, nil
}
