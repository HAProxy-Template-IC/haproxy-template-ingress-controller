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
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
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
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/util/flowcontrol"
	"sigs.k8s.io/e2e-framework/klient"
	"sigs.k8s.io/e2e-framework/klient/k8s/resources"
	"sigs.k8s.io/e2e-framework/pkg/env"

	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

// namespaceContextKey is a type-safe key for storing namespace in context.
type namespaceContextKey struct{}

const (
	// ControllerDeploymentName is the name of the controller deployment.
	ControllerDeploymentName = "haptic-controller"

	// ControllerCRDName is the name of the HAProxyTemplateConfig CRD.
	ControllerCRDName = "haproxy-config"

	// ControllerSecretName is the name of the controller credentials Secret.
	//nolint:gosec // G101: This is a Kubernetes Secret name, not actual credentials
	ControllerSecretName = "haproxy-credentials"

	// ControllerServiceAccountName is the name of the controller ServiceAccount.
	ControllerServiceAccountName = "haptic-controller"

	// ControllerRoleName is the name of the controller Role.
	ControllerRoleName = "haptic-controller"

	// ControllerRoleBindingName is the name of the controller RoleBinding.
	ControllerRoleBindingName = "haptic-controller"

	// ControllerClusterRoleName is the name of the controller ClusterRole.
	ControllerClusterRoleName = "haptic-controller"

	// ControllerClusterRoleBindingName is the name of the controller ClusterRoleBinding.
	ControllerClusterRoleBindingName = "haptic-controller"

	// DebugPort is the port for the debug HTTP server.
	DebugPort = 6060

	// MetricsPort is the port for the Prometheus metrics server.
	MetricsPort = 9090

	// DefaultTimeout for operations.
	DefaultTimeout = 2 * time.Minute

	// DefaultPodReadyTimeout is the timeout for waiting for pods to become ready.
	DefaultPodReadyTimeout = 2 * time.Minute

	// DefaultConfigUpdateTimeout is the timeout for config update operations.
	DefaultConfigUpdateTimeout = 60 * time.Second

	// DefaultClientSetupTimeout is the timeout for setting up debug/metrics clients.
	DefaultClientSetupTimeout = 30 * time.Second

	// DefaultLeaseWaitTimeout is the timeout for leader election lease operations.
	DefaultLeaseWaitTimeout = 60 * time.Second

	// PodRestartTimeout is a longer timeout for operations involving pod restarts
	// (e.g., leader failover, pod recreation after deletion).
	PodRestartTimeout = 90 * time.Second

	// WebhookCertSecretName is the name of the webhook certificate secret.
	WebhookCertSecretName = "haproxy-webhook-certs"
)

var (
	// testEnv is the shared test environment.
	testEnv env.Environment

	// sharedClientset is the shared Kubernetes clientset for all tests.
	// Created once during environment setup with rate limiting disabled.
	// All tests should use Clientset() instead of creating their own.
	sharedClientset kubernetes.Interface

	// sharedRESTConfig is the shared REST config for operations that need it
	// (like pod exec which requires SPDY upgrade).
	sharedRESTConfig *rest.Config
)

// ShouldKeepNamespace returns whether namespaces should be preserved after tests.
// Set KEEP_NAMESPACE=true to preserve namespaces for debugging failed tests.
// When enabled, test namespaces and their resources (pods, logs) remain after
// test completion, allowing inspection of controller behavior.
//
// Usage:
//
//	KEEP_NAMESPACE=true go test -tags=acceptance ./tests/acceptance/... -run TestName -v
func ShouldKeepNamespace() bool {
	return os.Getenv("KEEP_NAMESPACE") == "true"
}

// RESTConfig returns the shared REST config for operations that require it.
// This is needed for operations like pod exec that use SPDY streaming.
func RESTConfig() *rest.Config {
	if sharedRESTConfig == nil {
		panic("sharedRESTConfig not initialized - TestMain must run first")
	}
	return sharedRESTConfig
}

// SetSharedRESTConfig sets the shared REST config (called from main_test.go).
func SetSharedRESTConfig(cfg *rest.Config) {
	sharedRESTConfig = cfg
}

// Clientset returns the shared Kubernetes clientset for acceptance tests.
// This clientset has rate limiting disabled to support parallel test execution.
// All tests MUST use this instead of creating clientsets via kubernetes.NewForConfig().
func Clientset() kubernetes.Interface {
	if sharedClientset == nil {
		panic("sharedClientset not initialized - TestMain must run first")
	}
	return sharedClientset
}

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

// ControllerEnvironmentOptions configures the controller setup for CreateControllerEnvironment.
type ControllerEnvironmentOptions struct {
	// Replicas is the number of controller replicas to deploy.
	Replicas int32
	// CRDName is the name for the HAProxyTemplateConfig CRD instance.
	CRDName string
	// EnableLeaderElection enables leader election for multi-replica deployments.
	EnableLeaderElection bool
	// SkipCRDAndDeployment skips creation of HAProxyTemplateConfig, Deployment, and Services.
	// Use this when you need to create custom CRD or deployment resources.
	SkipCRDAndDeployment bool
	// SkipCredentialsSecret skips creation of the credentials secret.
	// Use this for testing missing credentials handling.
	SkipCredentialsSecret bool
}

// DefaultControllerEnvironmentOptions returns sensible defaults for controller setup.
func DefaultControllerEnvironmentOptions() ControllerEnvironmentOptions {
	return ControllerEnvironmentOptions{
		Replicas:             1,
		CRDName:              ControllerCRDName,
		EnableLeaderElection: false,
	}
}

// CreateControllerEnvironment sets up all resources needed to run the controller.
// This includes namespace, RBAC, secrets, CRD, deployment, and services.
// Tests that need custom configurations can pass a modified ControllerEnvironmentOptions.
func CreateControllerEnvironment(ctx context.Context, t *testing.T, cfg klient.Client, namespace string, opts ControllerEnvironmentOptions) error {
	t.Helper()

	// 1. Create namespace
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
	if err := cfg.Resources().Create(ctx, ns); err != nil {
		return fmt.Errorf("failed to create namespace: %w", err)
	}
	t.Logf("Created namespace: %s", namespace)

	// 2. Create RBAC resources
	if err := createControllerRBAC(ctx, cfg, namespace); err != nil {
		return fmt.Errorf("failed to create RBAC: %w", err)
	}
	t.Log("Created RBAC resources")

	// 3. Create secrets (optionally skip credentials secret for testing missing credentials)
	if !opts.SkipCredentialsSecret {
		if err := cfg.Resources().Create(ctx, NewSecret(namespace, ControllerSecretName)); err != nil {
			return fmt.Errorf("failed to create credentials secret: %w", err)
		}
	}
	if err := cfg.Resources().Create(ctx, NewWebhookCertSecret(namespace, WebhookCertSecretName)); err != nil {
		return fmt.Errorf("failed to create webhook cert secret: %w", err)
	}
	if opts.SkipCredentialsSecret {
		t.Log("Created webhook cert secret (credentials secret skipped)")
	} else {
		t.Log("Created secrets")
	}

	// Skip CRD, deployment, and services if requested (for custom setups like HTTP store tests)
	if opts.SkipCRDAndDeployment {
		return nil
	}

	// 4. Create HAProxyTemplateConfig CRD
	htplConfig := NewHAProxyTemplateConfig(namespace, opts.CRDName, ControllerSecretName, opts.EnableLeaderElection)
	if err := cfg.Resources().Create(ctx, htplConfig); err != nil {
		return fmt.Errorf("failed to create HAProxyTemplateConfig: %w", err)
	}
	t.Log("Created HAProxyTemplateConfig")

	// 5. Create deployment
	deployment := NewControllerDeployment(namespace, opts.CRDName, ControllerSecretName, ControllerServiceAccountName, DebugPort, opts.Replicas)
	if err := cfg.Resources().Create(ctx, deployment); err != nil {
		return fmt.Errorf("failed to create deployment: %w", err)
	}
	t.Log("Created controller deployment")

	// 6. Create services
	if err := createControllerServices(ctx, cfg, namespace); err != nil {
		return fmt.Errorf("failed to create services: %w", err)
	}
	t.Log("Created debug and metrics services")

	return nil
}

// createControllerRBAC creates ServiceAccount, Role, RoleBinding, ClusterRole, and ClusterRoleBinding.
func createControllerRBAC(ctx context.Context, client klient.Client, namespace string) error {
	if err := client.Resources().Create(ctx, NewServiceAccount(namespace, ControllerServiceAccountName)); err != nil {
		return fmt.Errorf("ServiceAccount: %w", err)
	}
	if err := client.Resources().Create(ctx, NewRole(namespace, ControllerRoleName)); err != nil {
		return fmt.Errorf("Role: %w", err)
	}
	if err := client.Resources().Create(ctx, NewRoleBinding(namespace, ControllerRoleBindingName, ControllerRoleName, ControllerServiceAccountName)); err != nil {
		return fmt.Errorf("RoleBinding: %w", err)
	}
	if err := client.Resources().Create(ctx, NewClusterRole(ControllerClusterRoleName, namespace)); err != nil {
		return fmt.Errorf("ClusterRole: %w", err)
	}
	if err := client.Resources().Create(ctx, NewClusterRoleBinding(ControllerClusterRoleBindingName, ControllerClusterRoleName, ControllerServiceAccountName, namespace, namespace)); err != nil {
		return fmt.Errorf("ClusterRoleBinding: %w", err)
	}
	return nil
}

// createControllerServices creates the debug and metrics ClusterIP services.
func createControllerServices(ctx context.Context, client klient.Client, namespace string) error {
	if err := client.Resources().Create(ctx, NewDebugService(namespace, ControllerDeploymentName, DebugPort)); err != nil {
		return fmt.Errorf("DebugService: %w", err)
	}
	if err := client.Resources().Create(ctx, NewMetricsService(namespace, ControllerDeploymentName, MetricsPort)); err != nil {
		return fmt.Errorf("MetricsService: %w", err)
	}
	return nil
}

// CleanupControllerEnvironment removes cluster-scoped resources and namespace.
// It returns the context for chaining in Teardown functions.
// Errors are logged but not returned since cleanup should be best-effort.
//
// If KEEP_NAMESPACE=true is set, cleanup is skipped to allow debugging.
func CleanupControllerEnvironment(ctx context.Context, t *testing.T, cfg klient.Client) context.Context {
	t.Helper()
	namespace, err := GetNamespaceFromContext(ctx)
	if err != nil {
		t.Logf("Warning: failed to get namespace from context: %v", err)
		return ctx
	}

	// Check if we should preserve the namespace for debugging
	if ShouldKeepNamespace() {
		t.Logf("Keeping namespace %s (KEEP_NAMESPACE=true)", namespace)
		t.Logf("To inspect: kubectl --context kind-haproxy-test get pods -n %s", namespace)
		t.Logf("To cleanup: kubectl --context kind-haproxy-test delete namespace %s", namespace)
		return ctx
	}

	// Delete cluster-scoped resources first (namespace deletion won't cascade to these)
	clusterRole := NewClusterRole(ControllerClusterRoleName, namespace)
	if err := cfg.Resources().Delete(ctx, clusterRole); err != nil {
		t.Logf("Warning: failed to delete ClusterRole: %v", err)
	}

	clusterRoleBinding := NewClusterRoleBinding(ControllerClusterRoleBindingName, ControllerClusterRoleName, ControllerServiceAccountName, namespace, namespace)
	if err := cfg.Resources().Delete(ctx, clusterRoleBinding); err != nil {
		t.Logf("Warning: failed to delete ClusterRoleBinding: %v", err)
	}

	// Delete namespace (cascades to all namespace-scoped resources)
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
	if err := cfg.Resources().Delete(ctx, ns); err != nil {
		t.Logf("Warning: failed to delete namespace: %v", err)
	} else {
		t.Logf("Deleted namespace: %s", namespace)
	}

	return ctx
}

// DumpControllerLogsOnError logs controller pod output for debugging test failures.
// This is a best-effort operation - errors are silently ignored.
func DumpControllerLogsOnError(ctx context.Context, t *testing.T, client klient.Client, namespace string) {
	t.Helper()

	pod, err := GetControllerPod(ctx, client, namespace)
	if err != nil {
		return
	}

	logs, err := GetPodLogs(ctx, Clientset(), pod, 200)
	if err != nil {
		return
	}

	t.Logf("=== Controller logs (%s) ===\n%s", pod.Name, logs)
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
			reconciliationValue, err := metricsClient.GetMetricValue(ctx, "haptic_reconciliation_total")
			if err != nil {
				return false, fmt.Errorf("metrics not accessible: %w", err)
			}

			if reconciliationValue == 0 {
				return false, fmt.Errorf("reconciliation_total is 0, controller still initializing")
			}

			// Step 3: Check validation has also completed (happens after reconciliation)
			validationValue, err := metricsClient.GetMetricValue(ctx, "haptic_validation_total")
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
//
// IMPORTANT: This function accepts a pre-created clientset to avoid rate limiter exhaustion
// under parallel test execution.
func DumpPodLogs(ctx context.Context, t *testing.T, clientset kubernetes.Interface, pod *corev1.Pod) {
	t.Helper()

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
//
// IMPORTANT: This function accepts a pre-created clientset to avoid rate limiter exhaustion
// under parallel test execution.
func WaitForWebhookConfiguration(ctx context.Context, clientset kubernetes.Interface, name string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

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
//
// IMPORTANT: This function accepts a pre-created clientset to avoid rate limiter exhaustion
// under parallel test execution.
func GetLeaseHolder(ctx context.Context, clientset kubernetes.Interface, namespace, leaseName string) (string, error) {
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
//
// IMPORTANT: This function accepts a pre-created clientset to avoid rate limiter exhaustion
// under parallel test execution.
func WaitForLeaderElection(ctx context.Context, clientset kubernetes.Interface, namespace, leaseName string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

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
//
// IMPORTANT: This function accepts a pre-created clientset to avoid rate limiter exhaustion
// under parallel test execution.
func WaitForNewLeader(ctx context.Context, client klient.Client, clientset kubernetes.Interface,
	namespace, leaseName, oldLeaderIdentity string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	// Track state for better error reporting
	var lastLeaseErr error
	var lastPodErr error
	var lastLeaseHolder string
	var lastPodStates []string

	pollCount := 0
	for {
		select {
		case <-ctx.Done():
			// Build detailed error message
			errMsg := fmt.Sprintf("timeout waiting for new leader (old: %s, polls: %d", oldLeaderIdentity, pollCount)
			if lastLeaseErr != nil {
				errMsg += fmt.Sprintf(", lastLeaseErr: %v", lastLeaseErr)
			}
			if lastLeaseHolder != "" {
				errMsg += fmt.Sprintf(", lastLeaseHolder: %s", lastLeaseHolder)
			}
			if lastPodErr != nil {
				errMsg += fmt.Sprintf(", lastPodErr: %v", lastPodErr)
			}
			if len(lastPodStates) > 0 {
				errMsg += fmt.Sprintf(", lastPodStates: %v", lastPodStates)
			}
			errMsg += ")"
			return "", errors.New(errMsg)

		case <-ticker.C:
			pollCount++
			lease, err := clientset.CoordinationV1().Leases(namespace).Get(ctx, leaseName, metav1.GetOptions{})
			if err != nil {
				lastLeaseErr = err
				continue
			}
			lastLeaseErr = nil

			if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity == "" {
				lastLeaseHolder = "<empty>"
				continue
			}

			newLeader := *lease.Spec.HolderIdentity
			lastLeaseHolder = newLeader

			// Must be different from old leader
			if newLeader == oldLeaderIdentity {
				continue
			}

			// Verify the new leader pod exists and is running
			pods, err := GetAllControllerPods(ctx, client, namespace)
			if err != nil {
				lastPodErr = err
				continue
			}
			lastPodErr = nil

			// Track pod states for debugging
			lastPodStates = make([]string, 0, len(pods))
			for i := range pods {
				pod := &pods[i]
				lastPodStates = append(lastPodStates, fmt.Sprintf("%s=%s", pod.Name, pod.Status.Phase))
				if strings.Contains(newLeader, pod.Name) && pod.Status.Phase == corev1.PodRunning {
					return newLeader, nil
				}
			}
			// New leader in lease but pod not running yet, keep waiting
		}
	}
}

// GetPodLogs retrieves the last N lines of logs from a pod.
//
// IMPORTANT: This function accepts a pre-created clientset to avoid rate limiter exhaustion
// under parallel test execution.
func GetPodLogs(ctx context.Context, clientset kubernetes.Interface, pod *corev1.Pod, tailLines int) (string, error) {
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

// getDynamicClient creates a dynamic Kubernetes client with rate limiting disabled.
// This is necessary for parallel test execution where multiple tests may need
// to update HAProxyTemplateConfig resources simultaneously.
func getDynamicClient(config *rest.Config) (dynamic.Interface, error) {
	configCopy := rest.CopyConfig(config)
	configCopy.RateLimiter = flowcontrol.NewFakeAlwaysRateLimiter()
	return dynamic.NewForConfig(configCopy)
}

// haproxyTemplateConfigGVR returns the GroupVersionResource for HAProxyTemplateConfig.
func haproxyTemplateConfigGVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{
		Group:    "haproxy-haptic.org",
		Version:  "v1alpha1",
		Resource: "haproxytemplateconfigs",
	}
}

// EnsureDebugClientReady waits for the controller pod to be ready, then creates
// a debug client using Kubernetes API proxy. This is useful after operations that may cause pod restarts.
//
// IMPORTANT: This function accepts a pre-created clientset to avoid rate limiter exhaustion
// under parallel test execution.
func EnsureDebugClientReady(ctx context.Context, t *testing.T, client klient.Client, clientset kubernetes.Interface, namespace string, timeout time.Duration) (*DebugClient, error) {
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

	return NewDebugClient(clientset, namespace, serviceName, DebugPort), nil
}

// SetupDebugClient creates the debug service (if not exists) and returns a debug client.
// This should be called during test setup after creating the deployment.
// The returned debug client uses the Kubernetes API server proxy to access the service,
// which works reliably in all environments including DinD (Docker-in-Docker).
// This function is idempotent - it will not fail if the service already exists.
//
// IMPORTANT: This function accepts a pre-created clientset to avoid rate limiter exhaustion
// under parallel test execution.
func SetupDebugClient(ctx context.Context, client klient.Client, clientset kubernetes.Interface, namespace string, timeout time.Duration) (*DebugClient, error) {
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
		maxRetries        = 3 // Reduced from 5 to allow more Wait-level retries
		initialBackoff    = 100 * time.Millisecond
		maxBackoff        = 2 * time.Second // Reduced from 5s for tighter retry budget
		minTimeForRetries = 3 * time.Second // Minimum time needed for meaningful retries
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

		// Look for: haptic_leader_election_is_leader{pod="pod-name"} 1
		if strings.Contains(line, "haptic_leader_election_is_leader") {
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
//
// IMPORTANT: This function accepts a pre-created clientset to avoid rate limiter exhaustion
// under parallel test execution.
func SetupMetricsAccess(ctx context.Context, client klient.Client, clientset kubernetes.Interface, namespace string, timeout time.Duration) (*MetricsClient, error) {
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

	return NewMetricsClient(clientset, namespace, serviceName, MetricsPort), nil
}

// GetPodsByLabel returns all pods matching a label selector in the namespace.
// This is useful for getting pods by their app label (e.g., "app=blocklist-server").
func GetPodsByLabel(ctx context.Context, client klient.Client, namespace, labelSelector string) ([]corev1.Pod, error) {
	var podList corev1.PodList
	res := client.Resources(namespace)
	if err := res.List(ctx, &podList, resources.WithLabelSelector(labelSelector)); err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}
	return podList.Items, nil
}

// ExecInPod executes a command inside a pod container and returns the output.
// This is useful for inspecting file contents inside pods during tests.
//
// IMPORTANT: This function uses the shared REST config for SPDY streaming.
// The clientset parameter is kept for API compatibility but the actual exec
// uses the shared REST config.
func ExecInPod(ctx context.Context, clientset kubernetes.Interface, namespace, podName, containerName string, command []string) (string, error) {
	restConfig := RESTConfig()

	// Create the exec request URL
	req := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: containerName,
			Command:   command,
			Stdin:     false,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, scheme.ParameterCodec)

	// Create the SPDY executor
	exec, err := remotecommand.NewSPDYExecutor(restConfig, "POST", req.URL())
	if err != nil {
		return "", fmt.Errorf("failed to create SPDY executor: %w", err)
	}

	// Execute and capture output
	var stdout, stderr bytes.Buffer
	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  nil,
		Stdout: &stdout,
		Stderr: &stderr,
		Tty:    false,
	})
	if err != nil {
		return "", fmt.Errorf("failed to exec in pod (stderr: %s): %w", stderr.String(), err)
	}

	return stdout.String(), nil
}

// WaitForNginxContent polls the nginx blocklist server until it returns content containing the expected string.
// This verifies the ConfigMap has been properly mounted before testing controller behavior.
// This is critical for isolating infrastructure issues (nginx serving wrong content) from controller bugs.
//
// IMPORTANT: This function accepts a pre-created clientset to avoid rate limiter exhaustion
// under parallel test execution. Creating new clientsets per call would overwhelm the rate limiter.
func WaitForNginxContent(ctx context.Context, clientset kubernetes.Interface, namespace, expectedContent string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	var lastContent string
	var lastError error

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for nginx to serve content containing %q (last content: %q, last error: %v)",
				expectedContent, lastContent, lastError)
		case <-ticker.C:
			// Get blocklist server pod
			pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
				LabelSelector: "app=blocklist-server",
			})
			if err != nil {
				lastError = fmt.Errorf("failed to list pods: %w", err)
				continue
			}
			if len(pods.Items) == 0 {
				lastError = fmt.Errorf("no blocklist-server pods found")
				continue
			}

			// Find a running pod
			var targetPod *corev1.Pod
			for i := range pods.Items {
				if pods.Items[i].Status.Phase == corev1.PodRunning {
					targetPod = &pods.Items[i]
					break
				}
			}
			if targetPod == nil {
				lastError = fmt.Errorf("no running blocklist-server pod found")
				continue
			}

			// Read the file content using exec
			content, err := ExecInPod(ctx, clientset, namespace, targetPod.Name, "nginx",
				[]string{"cat", "/usr/share/nginx/html/blocklist.txt"})
			if err != nil {
				lastError = fmt.Errorf("failed to exec in pod: %w", err)
				continue
			}

			lastContent = content
			lastError = nil

			if strings.Contains(content, expectedContent) {
				return nil
			}
		}
	}
}

// SetupBlocklistServer creates all resources needed for the blocklist HTTP server.
// This includes the ConfigMap with blocklist content, Deployment, and Service.
// It waits for the server pod to become ready before returning.
func SetupBlocklistServer(ctx context.Context, t *testing.T, client klient.Client, namespace, content string) error {
	t.Helper()

	blocklistCM := NewBlocklistContentConfigMap(namespace, content)
	if err := client.Resources().Create(ctx, blocklistCM); err != nil {
		return fmt.Errorf("failed to create blocklist configmap: %w", err)
	}

	blocklistDeploy := NewBlocklistServerDeployment(namespace)
	if err := client.Resources().Create(ctx, blocklistDeploy); err != nil {
		return fmt.Errorf("failed to create blocklist deployment: %w", err)
	}

	blocklistSvc := NewBlocklistService(namespace)
	if err := client.Resources().Create(ctx, blocklistSvc); err != nil {
		return fmt.Errorf("failed to create blocklist service: %w", err)
	}

	if err := WaitForPodReady(ctx, client, namespace, "app="+BlocklistServerName, DefaultTimeout); err != nil {
		return fmt.Errorf("blocklist server not ready: %w", err)
	}

	t.Log("Blocklist server ready")
	return nil
}

// UpdateBlocklistAndRestart updates the blocklist ConfigMap content and restarts the server pod.
// This is used to simulate blocklist content changes that the controller should detect.
// The function waits for the new pod to be ready before returning.
//
// IMPORTANT: This function accepts a pre-created clientset to avoid rate limiter exhaustion
// under parallel test execution.
func UpdateBlocklistAndRestart(ctx context.Context, t *testing.T, client klient.Client, clientset kubernetes.Interface, namespace, newContent string) error {
	t.Helper()

	blocklistCM, err := clientset.CoreV1().ConfigMaps(namespace).Get(ctx, BlocklistContentConfigMapName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get blocklist configmap: %w", err)
	}

	blocklistCM.Data["blocklist.txt"] = newContent
	_, err = clientset.CoreV1().ConfigMaps(namespace).Update(ctx, blocklistCM, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update blocklist configmap: %w", err)
	}

	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=" + BlocklistServerName,
	})
	if err != nil {
		return fmt.Errorf("failed to list blocklist pods: %w", err)
	}
	if len(pods.Items) == 0 {
		return fmt.Errorf("no blocklist server pod found")
	}

	err = clientset.CoreV1().Pods(namespace).Delete(ctx, pods.Items[0].Name, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete blocklist pod: %w", err)
	}

	if err := WaitForPodReady(ctx, client, namespace, "app="+BlocklistServerName, DefaultConfigUpdateTimeout); err != nil {
		return fmt.Errorf("blocklist server not ready after restart: %w", err)
	}

	t.Log("Blocklist content updated and server restarted")
	return nil
}
