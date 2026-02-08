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
func (d *Discovery) isDataplaneContainerReady(pod *unstructured.Unstructured, logger *slog.Logger) (bool, error) {
	dataplaneContainerName, err := d.findDataplaneContainerName(pod)
	if err != nil {
		return false, err
	}

	if logger != nil {
		logger.Log(context.Background(), logging.LevelTrace, "Found dataplane container in spec",
			"pod", pod.GetName(),
			"container", dataplaneContainerName,
			"port", d.dataplanePort)
	}

	return checkContainerReady(pod, dataplaneContainerName, logger)
}

// findDataplaneContainerName finds which container exposes the dataplane port.
func (d *Discovery) findDataplaneContainerName(pod *unstructured.Unstructured) (string, error) {
	containersSpec, found, err := unstructured.NestedSlice(pod.Object, "spec", "containers")
	if err != nil || !found {
		return "", fmt.Errorf("failed to get containers spec: %w", err)
	}

	for _, c := range containersSpec {
		container, ok := c.(map[string]interface{})
		if !ok {
			continue
		}

		name, found, err := unstructured.NestedString(container, "name")
		if err != nil || !found {
			continue
		}

		if containerHasPort(container, d.dataplanePort) {
			return name, nil
		}
	}

	return "", fmt.Errorf("no container found with dataplane port %d", d.dataplanePort)
}

// containerHasPort checks if a container spec contains the given port.
func containerHasPort(container map[string]interface{}, targetPort int) bool {
	ports, found, err := unstructured.NestedSlice(container, "ports")
	if err != nil || !found {
		return false
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

		if int(containerPort) == targetPort {
			return true
		}
	}

	return false
}

// checkContainerReady checks the ready status of a named container in pod status.
func checkContainerReady(pod *unstructured.Unstructured, containerName string, logger *slog.Logger) (bool, error) {
	containerStatuses, found, err := unstructured.NestedSlice(pod.Object, "status", "containerStatuses")
	if err != nil || !found {
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

		if name != containerName {
			continue
		}

		ready, found, err := unstructured.NestedBool(status, "ready")

		if logger != nil {
			logContainerStatus(logger, pod.GetName(), name, status, ready, found, err)
		}

		if err != nil {
			return false, fmt.Errorf("failed to get ready status: %w", err)
		}
		if !found {
			return false, nil
		}
		return ready, nil
	}

	if logger != nil {
		logger.Log(context.Background(), logging.LevelTrace, "Dataplane container not found in containerStatuses",
			"pod", pod.GetName(),
			"expected_container", containerName)
	}
	return false, nil
}

// logContainerStatus logs detailed container status for debugging.
func logContainerStatus(logger *slog.Logger, podName, containerName string, status map[string]interface{}, ready, readyFound bool, readyErr error) {
	started, _, _ := unstructured.NestedBool(status, "started")
	restartCount, _, _ := unstructured.NestedInt64(status, "restartCount")

	state, stateFound, _ := unstructured.NestedMap(status, "state")
	var stateType string
	if stateFound {
		switch {
		case hasKey(state, "running"):
			stateType = "running"
		case hasKey(state, "waiting"):
			stateType = "waiting"
		case hasKey(state, "terminated"):
			stateType = "terminated"
		}
	}

	logger.Log(context.Background(), logging.LevelTrace, "Dataplane container status check",
		"pod", podName,
		"container", containerName,
		"ready", ready,
		"ready_found", readyFound,
		"ready_error", readyErr,
		"started", started,
		"restart_count", restartCount,
		"state_type", stateType)
}

func hasKey(m map[string]interface{}, key string) bool {
	_, ok := m[key]
	return ok
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
func (d *Discovery) DiscoverEndpointsWithLogger(
	podStore types.Store,
	credentials coreconfig.Credentials,
	logger *slog.Logger,
) ([]dataplane.Endpoint, error) {
	if podStore == nil {
		return nil, fmt.Errorf("pod store is nil")
	}

	resources, err := podStore.List()
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}

	endpoints := make([]dataplane.Endpoint, 0, len(resources))

	for _, resource := range resources {
		endpoint, ok, err := d.evaluatePod(resource, credentials, logger)
		if err != nil {
			return nil, err
		}
		if ok {
			endpoints = append(endpoints, endpoint)
		}
	}

	return endpoints, nil
}

// evaluatePod evaluates a single pod resource and returns an endpoint if it is eligible.
// Returns ok=false if the pod should be skipped.
func (d *Discovery) evaluatePod(
	resource interface{},
	credentials coreconfig.Credentials,
	logger *slog.Logger,
) (dataplane.Endpoint, bool, error) {
	var zero dataplane.Endpoint

	pod := resourceToPod(resource)
	if pod == nil {
		return zero, false, nil
	}

	if logger != nil {
		logger.Log(context.Background(), logging.LevelTrace, "Evaluating pod for discovery",
			"pod", pod.GetName(),
			"namespace", pod.GetNamespace(),
			"uid", pod.GetUID())
	}

	// Skip terminating pods — they may still report phase="Running" and ready=true
	// during graceful shutdown, but their ports are shutting down
	if pod.GetDeletionTimestamp() != nil {
		if logger != nil {
			logger.Log(context.Background(), logging.LevelTrace, "Skipping terminating pod",
				"pod", pod.GetName(),
				"deletionTimestamp", pod.GetDeletionTimestamp())
		}
		return zero, false, nil
	}

	podIP, err := extractPodIP(pod, logger)
	if err != nil {
		return zero, false, err
	}
	if podIP == "" {
		return zero, false, nil
	}

	phase, err := extractPodPhase(pod, logger)
	if err != nil {
		return zero, false, err
	}
	if phase != "Running" {
		return zero, false, nil
	}

	ready, err := d.isDataplaneContainerReady(pod, logger)
	if err != nil {
		return zero, false, fmt.Errorf("failed to check dataplane container readiness for %s: %w",
			pod.GetName(), err)
	}
	if !ready {
		if logger != nil {
			logger.Log(context.Background(), logging.LevelTrace, "Skipping pod - dataplane container not ready",
				"pod", pod.GetName(),
				"podIP", podIP,
				"phase", phase)
		}
		return zero, false, nil
	}

	if logger != nil {
		logger.Log(context.Background(), logging.LevelTrace, "Including pod - dataplane container is ready",
			"pod", pod.GetName(),
			"podIP", podIP,
			"phase", phase)
	}

	return dataplane.Endpoint{
		URL:          fmt.Sprintf("http://%s:%d/v3", podIP, d.dataplanePort),
		Username:     credentials.DataplaneUsername,
		Password:     credentials.DataplanePassword,
		PodName:      pod.GetName(),
		PodNamespace: pod.GetNamespace(),
	}, true, nil
}

// extractPodIP extracts the pod IP from status.podIP.
// Returns empty string (without error) if the pod has no IP assigned yet.
func extractPodIP(pod *unstructured.Unstructured, logger *slog.Logger) (string, error) {
	podIP, found, err := unstructured.NestedString(pod.Object, "status", "podIP")
	if err != nil {
		return "", fmt.Errorf("failed to extract pod IP from %s: %w", pod.GetName(), err)
	}
	if !found || podIP == "" {
		if logger != nil {
			logger.Log(context.Background(), logging.LevelTrace, "Skipping pod - no IP assigned",
				"pod", pod.GetName())
		}
		return "", nil
	}
	return podIP, nil
}

// extractPodPhase extracts the pod phase from status.phase.
// Returns empty string (without error) if the phase is not found.
func extractPodPhase(pod *unstructured.Unstructured, logger *slog.Logger) (string, error) {
	phase, found, err := unstructured.NestedString(pod.Object, "status", "phase")
	if err != nil {
		return "", fmt.Errorf("failed to extract pod phase from %s: %w", pod.GetName(), err)
	}
	if !found || phase != "Running" {
		if logger != nil {
			logger.Log(context.Background(), logging.LevelTrace, "Skipping pod - not in Running phase",
				"pod", pod.GetName(),
				"phase", phase)
		}
		return phase, nil
	}
	return phase, nil
}
