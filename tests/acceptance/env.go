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

//go:build acceptance

// Package acceptance provides acceptance tests for the HAProxy Template Ingress Controller.
//
// These tests run against a real Kubernetes cluster (kind) and verify end-to-end behavior
// including configuration reloading, template rendering, and HAProxy deployment.
package acceptance

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/e2e-framework/klient"
	"sigs.k8s.io/e2e-framework/klient/k8s/resources"
	"sigs.k8s.io/e2e-framework/pkg/env"

	"haproxy-template-ic/tests/testutil"
)

// namespaceContextKey is a type-safe key for storing namespace in context.
type namespaceContextKey struct{}

const (
	// ControllerDeploymentName is the name of the controller deployment.
	ControllerDeploymentName = "haproxy-template-ic"

	// ControllerCRDName is the name of the HAProxyTemplateConfig CRD.
	ControllerCRDName = "haproxy-config"

	// ControllerSecretName is the name of the controller credentials Secret.
	//nolint:gosec // G101: This is a Kubernetes Secret name, not actual credentials
	ControllerSecretName = "haproxy-credentials"

	// ControllerServiceAccountName is the name of the controller ServiceAccount.
	ControllerServiceAccountName = "haproxy-template-ic"

	// ControllerRoleName is the name of the controller Role.
	ControllerRoleName = "haproxy-template-ic"

	// ControllerRoleBindingName is the name of the controller RoleBinding.
	ControllerRoleBindingName = "haproxy-template-ic"

	// ControllerClusterRoleName is the name of the controller ClusterRole.
	ControllerClusterRoleName = "haproxy-template-ic"

	// ControllerClusterRoleBindingName is the name of the controller ClusterRoleBinding.
	ControllerClusterRoleBindingName = "haproxy-template-ic"

	// DebugPort is the port for the debug HTTP server.
	DebugPort = 6060

	// DefaultTimeout for operations.
	DefaultTimeout = 2 * time.Minute
)

var (
	// testEnv is the shared test environment.
	testEnv env.Environment
)

// StoreNamespaceInContext stores a namespace name in the context for use across test phases.
func StoreNamespaceInContext(ctx context.Context, namespace string) context.Context {
	return context.WithValue(ctx, namespaceContextKey{}, namespace)
}

// GetNamespaceFromContext retrieves the namespace name from the context.
// Returns an error if the namespace is not found or has the wrong type.
func GetNamespaceFromContext(ctx context.Context) (string, error) {
	namespace, ok := ctx.Value(namespaceContextKey{}).(string)
	if !ok || namespace == "" {
		return "", fmt.Errorf("namespace not found in context")
	}
	return namespace, nil
}

// WaitForServiceEndpoints waits until a service has at least one ready endpoint.
func WaitForServiceEndpoints(ctx context.Context, client klient.Client, namespace, serviceName string, timeout time.Duration) error {
	cfg := testutil.FastWaitConfig()
	cfg.Timeout = timeout

	return testutil.WaitForConditionWithDescription(ctx, cfg, "service endpoints ready",
		func(ctx context.Context) (bool, error) {
			var endpoints corev1.Endpoints
			res := client.Resources(namespace)

			if err := res.Get(ctx, serviceName, namespace, &endpoints); err != nil {
				return false, err
			}

			// Check if there's at least one ready address
			for _, subset := range endpoints.Subsets {
				if len(subset.Addresses) > 0 {
					return true, nil
				}
			}
			return false, fmt.Errorf("no ready endpoints")
		})
}

// WaitForPodReady waits for a pod matching the label selector to be ready.
// Uses exponential backoff for efficient polling.
func WaitForPodReady(ctx context.Context, client klient.Client, namespace, labelSelector string, timeout time.Duration) error {
	cfg := testutil.DefaultWaitConfig()
	cfg.Timeout = timeout

	return testutil.WaitForConditionWithDescription(ctx, cfg, "pod ready with selector "+labelSelector,
		func(ctx context.Context) (bool, error) {
			var podList corev1.PodList
			res := client.Resources(namespace)

			if err := res.List(ctx, &podList, resources.WithLabelSelector(labelSelector)); err != nil {
				return false, err
			}

			if len(podList.Items) == 0 {
				return false, fmt.Errorf("no pods found")
			}

			for i := range podList.Items {
				pod := &podList.Items[i]
				for _, condition := range pod.Status.Conditions {
					if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
						return true, nil
					}
				}
			}
			return false, fmt.Errorf("pod exists but not ready")
		})
}

// WaitForPodCount waits until the specified number of pods matching the selector exist.
func WaitForPodCount(ctx context.Context, client klient.Client, namespace, labelSelector string, count int, timeout time.Duration) error {
	cfg := testutil.DefaultWaitConfig()
	cfg.Timeout = timeout

	return testutil.WaitForConditionWithDescription(ctx, cfg, fmt.Sprintf("%d pods with selector %s", count, labelSelector),
		func(ctx context.Context) (bool, error) {
			var podList corev1.PodList
			res := client.Resources(namespace)

			if err := res.List(ctx, &podList, resources.WithLabelSelector(labelSelector)); err != nil {
				return false, err
			}

			if len(podList.Items) == count {
				return true, nil
			}
			return false, fmt.Errorf("found %d pods, expected %d", len(podList.Items), count)
		})
}

// WaitForPodTerminated waits until a specific pod no longer exists.
func WaitForPodTerminated(ctx context.Context, client klient.Client, namespace, podName string, timeout time.Duration) error {
	cfg := testutil.FastWaitConfig()
	cfg.Timeout = timeout

	return testutil.WaitForConditionWithDescription(ctx, cfg, fmt.Sprintf("pod %s terminated", podName),
		func(ctx context.Context) (bool, error) {
			var podList corev1.PodList
			res := client.Resources(namespace)

			if err := res.List(ctx, &podList); err != nil {
				return false, err
			}

			for i := range podList.Items {
				if podList.Items[i].Name == podName {
					return false, fmt.Errorf("pod still exists")
				}
			}
			return true, nil
		})
}

// WaitForControllerReadyWithMetrics waits for the controller to complete startup reconciliation.
// This is more reliable than WaitForPodReady because it verifies the controller
// has actually processed configuration (reconciliation_total > 0).
// The metricsClient should be obtained from SetupMetricsAccess.
func WaitForControllerReadyWithMetrics(ctx context.Context, client klient.Client, namespace string, metricsClient *MetricsClient, timeout time.Duration) (*corev1.Pod, error) {
	cfg := testutil.SlowWaitConfig()
	cfg.Timeout = timeout

	var pod *corev1.Pod

	err := testutil.WaitForConditionWithDescription(ctx, cfg, "controller ready",
		func(ctx context.Context) (bool, error) {
			// Step 1: Check pod exists and is ready
			var podList corev1.PodList
			res := client.Resources(namespace)

			if err := res.List(ctx, &podList, resources.WithLabelSelector("app="+ControllerDeploymentName)); err != nil {
				return false, err
			}

			if len(podList.Items) == 0 {
				return false, fmt.Errorf("no controller pods found")
			}

			pod = &podList.Items[0]

			if pod.Status.Phase != corev1.PodRunning {
				return false, fmt.Errorf("pod phase is %s", pod.Status.Phase)
			}

			// Check pod is ready
			podReady := false
			for _, condition := range pod.Status.Conditions {
				if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
					podReady = true
					break
				}
			}
			if !podReady {
				return false, fmt.Errorf("pod exists but not ready")
			}

			// Step 2: Check metrics indicate reconciliation completed
			reconciliationValue, err := metricsClient.GetMetricValue(ctx, "haproxy_ic_reconciliation_total")
			if err != nil {
				return false, fmt.Errorf("metrics not accessible: %w", err)
			}

			if reconciliationValue == 0 {
				return false, fmt.Errorf("reconciliation_total is 0, controller still initializing")
			}

			// Step 3: Check validation has also completed (happens after reconciliation)
			validationValue, err := metricsClient.GetMetricValue(ctx, "haproxy_ic_validation_total")
			if err != nil {
				return false, fmt.Errorf("validation metrics not accessible: %w", err)
			}

			if validationValue == 0 {
				return false, fmt.Errorf("validation_total is 0, validation not yet complete")
			}

			return true, nil
		})

	return pod, err
}


// GetControllerPod returns the controller pod.
func GetControllerPod(ctx context.Context, client klient.Client, namespace string) (*corev1.Pod, error) {
	var podList corev1.PodList
	res := client.Resources(namespace)
	if err := res.List(ctx, &podList, resources.WithLabelSelector("app="+ControllerDeploymentName)); err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}

	if len(podList.Items) == 0 {
		return nil, fmt.Errorf("no controller pods found")
	}

	return &podList.Items[0], nil
}

// DumpPodLogs captures and prints pod logs to test output.
// This is useful for debugging test failures by providing visibility into what
// happened inside the pod.
func DumpPodLogs(ctx context.Context, t *testing.T, restConfig *rest.Config, pod *corev1.Pod) {
	t.Helper()

	// Create Kubernetes clientset
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		t.Logf("Failed to create Kubernetes clientset for log capture: %v", err)
		return
	}

	t.Logf("=== Pod %s/%s logs ===", pod.Namespace, pod.Name)

	// Capture logs for each container in the pod
	for _, container := range pod.Spec.Containers {
		t.Logf("--- Container: %s ---", container.Name)

		// Get pod logs
		podLogOpts := corev1.PodLogOptions{
			Container: container.Name,
		}

		req := clientset.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &podLogOpts)
		podLogs, err := req.Stream(ctx)
		if err != nil {
			t.Logf("Error getting logs for container %s: %v", container.Name, err)
			continue
		}
		defer podLogs.Close()

		// Read and print logs
		buf := make([]byte, 2048)
		for {
			n, err := podLogs.Read(buf)
			if n > 0 {
				t.Logf("%s", string(buf[:n]))
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Logf("Error reading logs: %v", err)
				break
			}
		}
	}

	t.Logf("=== End of pod logs ===")
}

// WaitForWebhookConfiguration waits for a ValidatingWebhookConfiguration to exist in the cluster.
// This is useful when testing webhook functionality - the controller creates the configuration
// dynamically, so tests must wait for it to appear before attempting webhook validation.
func WaitForWebhookConfiguration(ctx context.Context, restConfig *rest.Config, name string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Create Kubernetes clientset
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create clientset: %w", err)
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for ValidatingWebhookConfiguration %s", name)

		case <-ticker.C:
			_, err := clientset.AdmissionregistrationV1().
				ValidatingWebhookConfigurations().
				Get(ctx, name, metav1.GetOptions{})

			if err == nil {
				// Found the webhook configuration
				return nil
			}
			// Keep waiting if not found yet
		}
	}
}

// GetAllControllerPods returns all controller pods in the namespace.
func GetAllControllerPods(ctx context.Context, client klient.Client, namespace string) ([]corev1.Pod, error) {
	var podList corev1.PodList
	res := client.Resources(namespace)
	if err := res.List(ctx, &podList, resources.WithLabelSelector("app="+ControllerDeploymentName)); err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}

	return podList.Items, nil
}

// WaitForNewPodReady waits for a ready pod that has a different UID than the original pod.
// This is useful when testing pod restart scenarios where we need to ensure the old pod
// has been replaced by a new one.
func WaitForNewPodReady(ctx context.Context, client klient.Client, namespace, labelSelector, originalUID string, timeout time.Duration) (*corev1.Pod, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("timeout waiting for new pod (different from UID %s) to be ready", originalUID)

		case <-ticker.C:
			var podList corev1.PodList
			res := client.Resources(namespace)

			// List pods with label selector
			if err := res.List(ctx, &podList, resources.WithLabelSelector(labelSelector)); err != nil {
				continue
			}

			// Check if any pod is ready AND has a different UID
			for i := range podList.Items {
				pod := &podList.Items[i]
				// Skip pods with the original UID
				if string(pod.UID) == originalUID {
					continue
				}
				// Check if this new pod is ready
				for _, condition := range pod.Status.Conditions {
					if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
						return pod, nil
					}
				}
			}
		}
	}
}

// GetLeaseHolder queries the Lease resource and returns the holder identity.
// Returns empty string if Lease doesn't exist or has no holder.
func GetLeaseHolder(ctx context.Context, restConfig *rest.Config, namespace, leaseName string) (string, error) {
	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return "", fmt.Errorf("failed to create clientset: %w", err)
	}

	lease, err := clientset.CoordinationV1().Leases(namespace).Get(ctx, leaseName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get lease: %w", err)
	}

	if lease.Spec.HolderIdentity == nil {
		return "", nil
	}

	return *lease.Spec.HolderIdentity, nil
}

// WaitForLeaderElection waits until a Lease exists with a holder identity.
// Returns error if timeout occurs before leader is elected.
func WaitForLeaderElection(ctx context.Context, restConfig *rest.Config, namespace, leaseName string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("failed to create clientset: %w", err)
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for leader election")

		case <-ticker.C:
			lease, err := clientset.CoordinationV1().Leases(namespace).Get(ctx, leaseName, metav1.GetOptions{})
			if err != nil {
				// Lease doesn't exist yet, keep waiting
				continue
			}

			if lease.Spec.HolderIdentity != nil && *lease.Spec.HolderIdentity != "" {
				// Leader elected
				return nil
			}
			// Lease exists but no holder yet, keep waiting
		}
	}
}

// WaitForNewLeader waits until a different leader is elected than oldLeaderIdentity.
// It verifies the new leader pod exists and is running.
func WaitForNewLeader(ctx context.Context, client klient.Client, restConfig *rest.Config,
	namespace, leaseName, oldLeaderIdentity string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return "", fmt.Errorf("failed to create clientset: %w", err)
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("timeout waiting for new leader (old: %s)", oldLeaderIdentity)

		case <-ticker.C:
			lease, err := clientset.CoordinationV1().Leases(namespace).Get(ctx, leaseName, metav1.GetOptions{})
			if err != nil {
				continue
			}

			if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity == "" {
				continue
			}

			newLeader := *lease.Spec.HolderIdentity

			// Must be different from old leader
			if newLeader == oldLeaderIdentity {
				continue
			}

			// Verify the new leader pod exists and is running
			pods, err := GetAllControllerPods(ctx, client, namespace)
			if err != nil {
				continue
			}

			for i := range pods {
				pod := &pods[i]
				if strings.Contains(newLeader, pod.Name) && pod.Status.Phase == corev1.PodRunning {
					return newLeader, nil
				}
			}
			// New leader in lease but pod not running yet, keep waiting
		}
	}
}

// GetPodLogs retrieves the last N lines of logs from a pod.
func GetPodLogs(ctx context.Context, client klient.Client, pod *corev1.Pod, tailLines int) (string, error) {
	clientset, err := kubernetes.NewForConfig(client.RESTConfig())
	if err != nil {
		return "", fmt.Errorf("failed to create clientset: %w", err)
	}

	tailLinesInt64 := int64(tailLines)
	logOptions := &corev1.PodLogOptions{
		TailLines: &tailLinesInt64,
	}

	req := clientset.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, logOptions)
	podLogs, err := req.Stream(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to stream logs: %w", err)
	}
	defer podLogs.Close()

	buf := new(strings.Builder)
	_, err = io.Copy(buf, podLogs)
	if err != nil {
		return "", fmt.Errorf("failed to copy logs: %w", err)
	}

	return buf.String(), nil
}

// WaitForCondition polls a condition function until it returns true or timeout occurs.
// The condition function should return (satisfied bool, err error).
// If err is non-nil, polling continues (transient errors are expected).
// If satisfied is true, polling stops immediately and returns nil.
// If timeout occurs, returns an error with the provided description.
//
// This is a generic polling helper that replaces sleep-based synchronization
// with proper condition checking, making tests faster and more reliable.
//
// Example:
//
//	err := WaitForCondition(ctx, "HAProxy config deployed", 30*time.Second, 100*time.Millisecond, func() (bool, error) {
//	    config, err := getHAProxyConfig()
//	    if err != nil {
//	        return false, err // Transient error, keep trying
//	    }
//	    return strings.Contains(config, "expected-content"), nil
//	})
func WaitForCondition(ctx context.Context, description string, timeout, pollInterval time.Duration, condition func() (bool, error)) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	// Check immediately before first tick
	if satisfied, err := condition(); err == nil && satisfied {
		return nil
	}

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for %s (waited %v)", description, timeout)

		case <-ticker.C:
			satisfied, err := condition()
			if err == nil && satisfied {
				return nil
			}
			// Keep polling on error or unsatisfied condition
		}
	}
}

// MetricsPort is the port for Prometheus metrics.
const MetricsPort = 9090

// UpdateHAProxyTemplateConfigTemplate fetches the current HAProxyTemplateConfig from the cluster
// and replaces its spec.haproxyConfig.template with the new template, preserving resourceVersion.
// This is necessary because Kubernetes requires resourceVersion for optimistic concurrency control.
// The function includes retry logic to handle conflicts during rapid updates.
func UpdateHAProxyTemplateConfigTemplate(ctx context.Context, client klient.Client, namespace, name, newTemplate string) error {
	// Use dynamic client to get and update the HAProxyTemplateConfig
	dynamicClient, err := getDynamicClient(client.RESTConfig())
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}

	gvr := haproxyTemplateConfigGVR()

	// Retry loop for optimistic concurrency conflicts
	maxRetries := 5
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		// Get the existing resource (fresh on each attempt to get latest resourceVersion)
		existing, err := dynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get existing HAProxyTemplateConfig: %w", err)
		}

		// Verify resourceVersion is present
		rv, found, _ := unstructured.NestedString(existing.Object, "metadata", "resourceVersion")
		if !found || rv == "" {
			return fmt.Errorf("existing resource has no resourceVersion")
		}

		// Set the new template at spec.haproxyConfig.template
		if err := unstructured.SetNestedField(existing.Object, newTemplate, "spec", "haproxyConfig", "template"); err != nil {
			return fmt.Errorf("failed to set spec.haproxyConfig.template: %w", err)
		}

		// Update the resource (resourceVersion is already set from Get)
		_, err = dynamicClient.Resource(gvr).Namespace(namespace).Update(ctx, existing, metav1.UpdateOptions{})
		if err != nil {
			// Check if it's a conflict error (optimistic concurrency)
			if strings.Contains(err.Error(), "Conflict") || strings.Contains(err.Error(), "conflict") ||
				strings.Contains(err.Error(), "resourceVersion") || strings.Contains(err.Error(), "modified") {
				lastErr = err
				time.Sleep(time.Duration(attempt*100) * time.Millisecond) // Exponential backoff
				continue
			}
			return fmt.Errorf("failed to update HAProxyTemplateConfig: %w", err)
		}

		// Success
		return nil
	}

	return fmt.Errorf("failed to update HAProxyTemplateConfig after %d retries: %w", maxRetries, lastErr)
}

// getDynamicClient creates a dynamic Kubernetes client.
func getDynamicClient(config *rest.Config) (dynamic.Interface, error) {
	return dynamic.NewForConfig(config)
}

// haproxyTemplateConfigGVR returns the GroupVersionResource for HAProxyTemplateConfig.
func haproxyTemplateConfigGVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{
		Group:    "haproxy-template-ic.gitlab.io",
		Version:  "v1alpha1",
		Resource: "haproxytemplateconfigs",
	}
}

// EnsureDebugClientReady waits for the controller pod to be ready, then creates
// a debug client using Kubernetes API proxy. This is useful after operations that may cause pod restarts.
func EnsureDebugClientReady(ctx context.Context, t *testing.T, client klient.Client, namespace string, timeout time.Duration) (*DebugClient, error) {
	t.Helper()

	// First wait for pod to be ready
	err := WaitForPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, timeout)
	if err != nil {
		return nil, fmt.Errorf("pod not ready: %w", err)
	}

	// Wait for debug service endpoints to be ready
	serviceName := ControllerDeploymentName + "-debug"
	if err := WaitForServiceEndpoints(ctx, client, namespace, serviceName, timeout); err != nil {
		return nil, fmt.Errorf("failed waiting for debug service endpoints: %w", err)
	}

	// Create Kubernetes clientset for API proxy access
	clientset, err := kubernetes.NewForConfig(client.RESTConfig())
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %w", err)
	}

	return NewDebugClient(clientset, namespace, serviceName, DebugPort), nil
}

// SetupDebugClient creates the debug service (if not exists) and returns a debug client.
// This should be called during test setup after creating the deployment.
// The returned debug client uses the Kubernetes API server proxy to access the service,
// which works reliably in all environments including DinD (Docker-in-Docker).
// This function is idempotent - it will not fail if the service already exists.
func SetupDebugClient(ctx context.Context, client klient.Client, namespace string, timeout time.Duration) (*DebugClient, error) {
	// Create debug service (idempotent - ignore if already exists)
	debugSvc := NewDebugService(namespace, ControllerDeploymentName, DebugPort)
	res := client.Resources(namespace)
	if err := res.Create(ctx, debugSvc); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return nil, fmt.Errorf("failed to create debug service: %w", err)
		}
		// Service already exists, continue
	}

	// Wait for service endpoints to be ready
	serviceName := ControllerDeploymentName + "-debug"
	if err := WaitForServiceEndpoints(ctx, client, namespace, serviceName, timeout); err != nil {
		return nil, fmt.Errorf("failed waiting for debug service endpoints: %w", err)
	}

	// Create Kubernetes clientset for API proxy access
	clientset, err := kubernetes.NewForConfig(client.RESTConfig())
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %w", err)
	}

	return NewDebugClient(clientset, namespace, serviceName, DebugPort), nil
}

// MetricsClient provides access to the controller's metrics endpoint via Kubernetes API proxy.
type MetricsClient struct {
	clientset   kubernetes.Interface
	namespace   string
	serviceName string
	port        string
}

// NewMetricsClient creates a new metrics client for accessing the controller via API proxy.
func NewMetricsClient(clientset kubernetes.Interface, namespace, serviceName string, port int32) *MetricsClient {
	return &MetricsClient{
		clientset:   clientset,
		namespace:   namespace,
		serviceName: serviceName,
		port:        strconv.Itoa(int(port)),
	}
}

// GetMetrics fetches the raw metrics from the controller.
// Includes retry logic with exponential backoff for resilience during parallel test execution.
//
// The retry budget is intentionally kept small (3 retries, 2s max backoff) to ensure
// that higher-level Wait functions get enough retry opportunities. In CI with 17
// parallel tests, the API server can be temporarily overloaded.
func (mc *MetricsClient) GetMetrics(ctx context.Context) (string, error) {
	const (
		maxRetries        = 3                   // Reduced from 5 to allow more Wait-level retries
		initialBackoff    = 100 * time.Millisecond
		maxBackoff        = 2 * time.Second    // Reduced from 5s for tighter retry budget
		minTimeForRetries = 3 * time.Second    // Minimum time needed for meaningful retries
	)

	// Check if we have enough time remaining for retries.
	// If deadline is tight, try once and fail fast to let the Wait function retry.
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining < minTimeForRetries {
			// Not enough time for meaningful retries - try once and return
			data, err := mc.clientset.CoreV1().Services(mc.namespace).ProxyGet(
				"http",
				mc.serviceName,
				mc.port,
				"metrics",
				nil,
			).DoRaw(ctx)
			if err != nil {
				return "", fmt.Errorf("failed to fetch metrics: %w", err)
			}
			return string(data), nil
		}
	}

	var lastErr error
	backoff := initialBackoff

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			// Wait before retry with exponential backoff
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(backoff):
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
		}

		data, err := mc.clientset.CoreV1().Services(mc.namespace).ProxyGet(
			"http",
			mc.serviceName,
			mc.port,
			"metrics",
			nil,
		).DoRaw(ctx)

		if err == nil {
			return string(data), nil
		}

		lastErr = err

		// Check if the parent context is cancelled - don't retry in that case
		if ctx.Err() != nil {
			return "", ctx.Err()
		}

		// Check if error is retryable (server overloaded, temporary unavailability)
		errStr := err.Error()
		if strings.Contains(errStr, "unable to handle the request") ||
			strings.Contains(errStr, "connection refused") ||
			strings.Contains(errStr, "no endpoints available") {
			// Retryable error, continue to next attempt
			continue
		}

		// Non-retryable error, return immediately
		return "", fmt.Errorf("failed to fetch metrics: %w", err)
	}

	return "", fmt.Errorf("failed to fetch metrics after %d retries: %w", maxRetries, lastErr)
}

// GetMetricValue fetches a single metric value from the controller's metrics endpoint.
func (mc *MetricsClient) GetMetricValue(ctx context.Context, metricName string) (float64, error) {
	metricsBody, err := mc.GetMetrics(ctx)
	if err != nil {
		return 0, err
	}

	// Parse the specific metric
	scanner := bufio.NewScanner(strings.NewReader(metricsBody))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}

		// Handle metrics with labels like: metric_name{label="value"} 123
		// or simple metrics like: metric_name 123
		if strings.HasPrefix(line, metricName) {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				value, err := strconv.ParseFloat(parts[len(parts)-1], 64)
				if err != nil {
					continue
				}
				return value, nil
			}
		}
	}

	return 0, nil // Metric not found, return 0
}

// GetLeaderPod parses metrics to find which pod is the leader.
// Returns the pod name if found, empty string if no leader, or an error.
func (mc *MetricsClient) GetLeaderPod(ctx context.Context) (string, error) {
	metricsBody, err := mc.GetMetrics(ctx)
	if err != nil {
		return "", err
	}

	// Parse the metrics to find the leader pod
	scanner := bufio.NewScanner(strings.NewReader(metricsBody))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}

		// Look for: haproxy_ic_leader_election_is_leader{pod="pod-name"} 1
		if strings.Contains(line, "haproxy_ic_leader_election_is_leader") {
			// Check if this metric reports is_leader=1
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				value := parts[len(parts)-1]
				if value == "1" || value == "1.0" {
					// Extract pod name from label: pod="pod-name"
					start := strings.Index(line, `pod="`)
					if start != -1 {
						start += 5 // len(`pod="`)
						end := strings.Index(line[start:], `"`)
						if end != -1 {
							return line[start : start+end], nil
						}
					}
				}
			}
		}
	}

	return "", nil // No leader found
}

// SetupMetricsAccess creates the metrics service (if not exists) and returns a MetricsClient.
// This is used by WaitForControllerReady to check reconciliation metrics.
// The MetricsClient uses the Kubernetes API server proxy, which works reliably in DinD environments.
// This function is idempotent - it will not fail if the service already exists.
func SetupMetricsAccess(ctx context.Context, client klient.Client, namespace string, timeout time.Duration) (*MetricsClient, error) {
	// Create metrics service (idempotent - ignore if already exists)
	metricsSvc := NewMetricsService(namespace, ControllerDeploymentName, MetricsPort)
	res := client.Resources(namespace)
	if err := res.Create(ctx, metricsSvc); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return nil, fmt.Errorf("failed to create metrics service: %w", err)
		}
		// Service already exists, continue
	}

	// Wait for service endpoints to be ready
	serviceName := ControllerDeploymentName + "-metrics"
	if err := WaitForServiceEndpoints(ctx, client, namespace, serviceName, timeout); err != nil {
		return nil, fmt.Errorf("failed waiting for metrics service endpoints: %w", err)
	}

	// Create Kubernetes clientset for API proxy access
	clientset, err := kubernetes.NewForConfig(client.RESTConfig())
	if err != nil {
		return nil, fmt.Errorf("failed to create clientset: %w", err)
	}

	return NewMetricsClient(clientset, namespace, serviceName, MetricsPort), nil
}

// GetLeaderPodFromMetrics fetches metrics via API proxy and returns which pod is the leader.
// It parses the haproxy_ic_leader_election_is_leader{pod="..."} metric to find the pod
// with value 1. Returns the pod name if found, empty string if no leader, or an error.
func GetLeaderPodFromMetrics(ctx context.Context, metricsClient *MetricsClient) (string, error) {
	return metricsClient.GetLeaderPod(ctx)
}

// CheckPodIsLeaderViaMetrics checks if a specific pod is the leader by fetching metrics via API proxy
// and parsing the haproxy_ic_leader_election_is_leader metric.
func CheckPodIsLeaderViaMetrics(ctx context.Context, metricsClient *MetricsClient, podName string) (bool, error) {
	leaderPod, err := metricsClient.GetLeaderPod(ctx)
	if err != nil {
		return false, err
	}

	return leaderPod == podName, nil
}
