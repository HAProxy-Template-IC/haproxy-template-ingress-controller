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
// This package implements pure business logic for discovering HAProxy pod
// endpoints from pod resources: it extracts pod IPs, checks that the agent
// container is running, and constructs agent endpoints with credentials.
// Whether the agent answers is the event adapter's half of the rule.
//
// This is a pure component with no event bus dependency - event coordination is
// handled by the adapter in pkg/controller/discovery.
package discovery

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sort"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/logging"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

const (
	stateRunning = "running"
	phaseRunning = "Running"

	// agentContainerName is the container HAPTIC deploys to every HAProxy pod
	// and talks to. It is the controller's own operational identity, not a
	// resource an operator describes, so naming it here is correct.
	agentContainerName = "agent"
)

// Rejection reasons, reported as haptic_haproxy_pods_rejected_total{reason}.
const (
	RejectionAgentNotRunning  = "agent_container_not_running"
	RejectionAgentUnreachable = "agent_unreachable"
)

// Discovery discovers HAProxy pod endpoints from Kubernetes resources.
//
// This is a pure component that takes a pod store and credentials and returns
// the pods worth probing. It has no knowledge of events or the event bus -
// that coordination is handled by the event adapter.
type Discovery struct {
	dataplanePort int
}

// traceIf logs msg at the trace level with the supplied attributes when
// logger is non-nil. The discovery functions accept an optional *slog.Logger
// (callers pass nil to silence the verbose pod-evaluation trace output) and
// every direct trace site in this file routes through this helper rather
// than open-coding "if logger != nil { logger.Log(context.Background(),
// logging.LevelTrace, ...) }".
func traceIf(logger *slog.Logger, msg string, args ...any) {
	if logger == nil {
		return
	}
	logger.Log(context.Background(), logging.LevelTrace, msg, args...)
}

// agentContainerRunning reports whether the pod's agent container is running.
//
// The container's ready flag is deliberately not consulted, and neither is pod
// Ready: HAProxy's readiness probe only turns 200 after the first apply lands,
// so gating discovery on it would never admit a fresh pod.
func agentContainerRunning(pod *unstructured.Unstructured, logger *slog.Logger) bool {
	statuses, found, err := unstructured.NestedSlice(pod.Object, "status", "containerStatuses")
	if err != nil || !found {
		traceIf(logger, "No containerStatuses found in pod status", "pod", pod.GetName(), "error", err)
		return false
	}
	for _, entry := range statuses {
		status, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		name, found, err := unstructured.NestedString(status, "name")
		if err != nil || !found || name != agentContainerName {
			continue
		}
		state, found, _ := unstructured.NestedMap(status, "state")
		running := found && hasKey(state, stateRunning)
		logContainerStatus(logger, pod.GetName(), name, status, running)
		return running
	}
	traceIf(logger, "Agent container not found in containerStatuses",
		"pod", pod.GetName(), "expected_container", agentContainerName)
	return false
}

// logContainerStatus logs detailed container status for debugging. When
// logger is nil the function returns immediately without computing the
// auxiliary fields, matching the rest of the trace-on-demand sites in this
// file.
func logContainerStatus(logger *slog.Logger, podName, containerName string, status map[string]any, running bool) {
	if logger == nil {
		return
	}

	restartCount, _, _ := unstructured.NestedInt64(status, "restartCount")
	state, stateFound, _ := unstructured.NestedMap(status, "state")
	var stateType string
	if stateFound {
		switch {
		case hasKey(state, stateRunning):
			stateType = stateRunning
		case hasKey(state, "waiting"):
			stateType = "waiting"
		case hasKey(state, "terminated"):
			stateType = "terminated"
		}
	}

	traceIf(logger, "Agent container status check",
		"pod", podName,
		"container", containerName,
		"running", running,
		"restart_count", restartCount,
		"state_type", stateType)
}

func hasKey(m map[string]any, key string) bool {
	_, ok := m[key]
	return ok
}

// resourceToPod converts a store resource to *unstructured.Unstructured.
//
// Resources in stores may be either:
//   - *unstructured.Unstructured (legacy format, used in some tests)
//   - map[string]any (production format after float-to-int conversion)
//
// Returns nil if the resource type is not supported.
func resourceToPod(resource any) *unstructured.Unstructured {
	switch r := resource.(type) {
	case *unstructured.Unstructured:
		return r
	case map[string]any:
		return &unstructured.Unstructured{Object: r}
	default:
		return nil
	}
}

// Candidate is one pod's verdict: an endpoint the adapter should probe, or the
// reason the pod is not one.
type Candidate struct {
	Endpoint dataplane.Endpoint
	Reason   string // empty when the pod is a candidate
}

// DiscoverEndpoints returns the candidate endpoints from pod resources.
//
// It lists every pod in the store, keeps the ones with an IP whose agent
// container is running, and builds the agent endpoint from the pod IP, the
// configured port and the credentials. Whether the agent answers is decided by
// the caller, which owns the HTTP client.
func (d *Discovery) DiscoverEndpoints(
	podStore types.Store,
	credentials coreconfig.Credentials,
) ([]Candidate, error) {
	return d.DiscoverEndpointsWithLogger(podStore, credentials, nil)
}

// DiscoverEndpointsWithLogger is like DiscoverEndpoints but accepts an optional logger for debugging.
func (d *Discovery) DiscoverEndpointsWithLogger(
	podStore types.Store,
	credentials coreconfig.Credentials,
	logger *slog.Logger,
) ([]Candidate, error) {
	if podStore == nil {
		return nil, errors.New("pod store is nil")
	}

	resources, err := podStore.List()
	if err != nil {
		return nil, fmt.Errorf("listing pods: %w", err)
	}

	candidates := make([]Candidate, 0, len(resources))
	for _, resource := range resources {
		candidate, ok, err := d.evaluatePod(resource, credentials, logger)
		if err != nil {
			return nil, err
		}
		if ok {
			candidates = append(candidates, candidate)
		}
	}
	return candidates, nil
}

// evaluatePod evaluates a single pod resource. ok is false for a pod that is
// not HAPTIC's to talk to at all — one that is terminating, not Running, or not
// a pod at all — which is a state change rather than a rejection.
func (d *Discovery) evaluatePod(
	resource any,
	credentials coreconfig.Credentials,
	logger *slog.Logger,
) (Candidate, bool, error) {
	var zero Candidate

	pod := resourceToPod(resource)
	if pod == nil {
		return zero, false, nil
	}

	traceIf(logger, "Evaluating pod for discovery",
		"pod", pod.GetName(),
		"namespace", pod.GetNamespace(),
		"uid", pod.GetUID())

	// Skip terminating pods — they may still report phase="Running" and ready=true
	// during graceful shutdown, but their ports are shutting down
	if pod.GetDeletionTimestamp() != nil {
		traceIf(logger, "Skipping terminating pod",
			"pod", pod.GetName(),
			"deletion_timestamp", pod.GetDeletionTimestamp())
		return zero, false, nil
	}

	phase, err := extractPodPhase(pod, logger)
	if err != nil {
		return zero, false, err
	}
	if phase != phaseRunning {
		return zero, false, nil
	}

	podIP, err := extractPodIP(pod, logger)
	if err != nil {
		return zero, false, err
	}
	// A Running pod without an IP is the kubelet mid-flight, like a Pending
	// pod: skipped silently, never counted as a rejection.
	if podIP == "" {
		return zero, false, nil
	}

	podRuntimeID, err := extractPodRuntimeID(pod)
	if err != nil {
		return zero, false, fmt.Errorf("identifying pod runtime for %s: %w", pod.GetName(), err)
	}
	candidate := Candidate{Endpoint: dataplane.Endpoint{
		URL:          "http://" + net.JoinHostPort(podIP, strconv.Itoa(d.dataplanePort)),
		Username:     credentials.DataplaneUsername,
		Password:     credentials.DataplanePassword,
		PodName:      pod.GetName(),
		PodNamespace: pod.GetNamespace(),
		PodUID:       string(pod.GetUID()),
		PodRuntimeID: podRuntimeID,
	}}

	if !agentContainerRunning(pod, logger) {
		candidate.Reason = RejectionAgentNotRunning
		return candidate, true, nil
	}

	traceIf(logger, "Pod is a candidate - agent container is running",
		"pod", pod.GetName(),
		"pod_ip", podIP,
		"phase", phase)
	return candidate, true, nil
}

func extractPodRuntimeID(pod *unstructured.Unstructured) (string, error) {
	statuses, found, err := unstructured.NestedSlice(pod.Object, "status", "containerStatuses")
	if err != nil {
		return "", fmt.Errorf("reading container statuses: %w", err)
	}
	if !found {
		return "", nil
	}

	runtimes := make([]string, 0, len(statuses))
	for _, value := range statuses {
		status, ok := value.(map[string]any)
		if !ok {
			continue
		}
		name, nameFound, nameErr := unstructured.NestedString(status, "name")
		imageID, _, imageErr := unstructured.NestedString(status, "imageID")
		containerID, _, containerErr := unstructured.NestedString(status, "containerID")
		if nameErr != nil || imageErr != nil || containerErr != nil {
			return "", errors.New("container status name, imageID, or containerID is not a string")
		}
		if nameFound && (imageID != "" || containerID != "") {
			runtimes = append(runtimes, name+"\x00"+imageID+"\x00"+containerID)
		}
	}
	if len(runtimes) == 0 {
		return "", nil
	}

	sort.Strings(runtimes)
	sum := sha256.Sum256([]byte(strings.Join(runtimes, "\x00")))
	return fmt.Sprintf("%x", sum), nil
}

// extractPodIP extracts the pod IP from status.podIP.
// Returns empty string (without error) if the pod has no IP assigned yet.
func extractPodIP(pod *unstructured.Unstructured, logger *slog.Logger) (string, error) {
	podIP, found, err := unstructured.NestedString(pod.Object, "status", "podIP")
	if err != nil {
		return "", fmt.Errorf("extracting pod IP from %s: %w", pod.GetName(), err)
	}
	if !found || podIP == "" {
		traceIf(logger, "Skipping pod - no IP assigned",
			"pod", pod.GetName())
		return "", nil
	}
	return podIP, nil
}

// extractPodPhase extracts the pod phase from status.phase.
// Returns empty string (without error) if the phase is not found.
func extractPodPhase(pod *unstructured.Unstructured, logger *slog.Logger) (string, error) {
	phase, found, err := unstructured.NestedString(pod.Object, "status", "phase")
	if err != nil {
		return "", fmt.Errorf("extracting pod phase from %s: %w", pod.GetName(), err)
	}
	if !found || phase != phaseRunning {
		traceIf(logger, "Skipping pod - not in Running phase",
			"pod", pod.GetName(),
			"phase", phase)
		return phase, nil
	}
	return phase, nil
}
