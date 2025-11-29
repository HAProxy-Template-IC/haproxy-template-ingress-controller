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
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/e2e-framework/klient"
	"sigs.k8s.io/e2e-framework/klient/k8s/resources"
	"sigs.k8s.io/e2e-framework/pkg/env"
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

// WaitForPodReady waits for a pod matching the label selector to be ready.
func WaitForPodReady(ctx context.Context, client klient.Client, namespace, labelSelector string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for pod to be ready")

		case <-ticker.C:
			var podList corev1.PodList
			res := client.Resources(namespace)

			// List pods with label selector
			if err := res.List(ctx, &podList, resources.WithLabelSelector(labelSelector)); err != nil {
				continue
			}

			// Check if any pod is ready
			for i := range podList.Items {
				pod := &podList.Items[i]
				for _, condition := range pod.Status.Conditions {
					if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
						return nil
					}
				}
			}
		}
	}
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

// GetAuxiliaryFiles retrieves the auxiliary files from the debug endpoint.
func (dc *DebugClient) GetAuxiliaryFiles(ctx context.Context) (map[string]interface{}, error) {
	url := fmt.Sprintf("http://localhost:%d/debug/vars/auxfiles", dc.localPort)
	return dc.getJSON(ctx, url)
}

// GetGeneralFileContent retrieves the content of a specific general file from auxiliary files.
// Returns the content string and any error encountered.
func (dc *DebugClient) GetGeneralFileContent(ctx context.Context, fileName string) (string, error) {
	auxFiles, err := dc.GetAuxiliaryFiles(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get auxiliary files: %w", err)
	}

	files, ok := auxFiles["files"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("files field not found or wrong type")
	}

	// The struct field is GeneralFiles (not general_files) - Go JSON serialization uses struct field names
	generalFiles, ok := files["GeneralFiles"].([]interface{})
	if !ok {
		return "", fmt.Errorf("GeneralFiles field not found or wrong type")
	}

	for _, file := range generalFiles {
		fileMap, ok := file.(map[string]interface{})
		if !ok {
			continue
		}
		// The struct field is Filename (not Name)
		name, ok := fileMap["Filename"].(string)
		if !ok {
			continue
		}
		if name == fileName {
			content, ok := fileMap["Content"].(string)
			if !ok {
				return "", fmt.Errorf("content field not found or wrong type for file %s", fileName)
			}
			return content, nil
		}
	}

	return "", fmt.Errorf("file %s not found in auxiliary files", fileName)
}

// WaitForAuxFileContains waits until a specific auxiliary file contains the expected content.
func (dc *DebugClient) WaitForAuxFileContains(ctx context.Context, fileName, expectedContent string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	var lastContent string
	var lastError error

	for {
		select {
		case <-ctx.Done():
			if lastError != nil {
				return fmt.Errorf("timeout waiting for file %s to contain %q (last error: %w)", fileName, expectedContent, lastError)
			}
			return fmt.Errorf("timeout waiting for file %s to contain %q (last content: %q)", fileName, expectedContent, lastContent)

		case <-ticker.C:
			content, err := dc.GetGeneralFileContent(ctx, fileName)
			if err != nil {
				lastError = err
				continue // Retry on error
			}
			lastContent = content

			if strings.Contains(content, expectedContent) {
				return nil
			}
		}
	}
}

// WaitForAuxFileNotContains waits until a specific auxiliary file does NOT contain the specified content.
// This is useful for verifying that invalid content was rejected.
func (dc *DebugClient) WaitForAuxFileNotContains(ctx context.Context, fileName, unexpectedContent string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	var lastContent string
	var lastError error

	for {
		select {
		case <-ctx.Done():
			if lastError != nil {
				return fmt.Errorf("timeout: file %s still contains %q or error occurred (last error: %w)", fileName, unexpectedContent, lastError)
			}
			return fmt.Errorf("timeout: file %s still contains %q (last content: %q)", fileName, unexpectedContent, lastContent)

		case <-ticker.C:
			content, err := dc.GetGeneralFileContent(ctx, fileName)
			if err != nil {
				lastError = err
				continue // Retry on error
			}
			lastContent = content

			if !strings.Contains(content, unexpectedContent) {
				return nil
			}
		}
	}
}

// WaitForAuxFileContentStable waits and verifies that the file content stays unchanged for the duration.
// This is useful for verifying that invalid updates are rejected and old content is preserved.
func (dc *DebugClient) WaitForAuxFileContentStable(ctx context.Context, fileName string, expectedContent string, stableDuration time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, stableDuration+5*time.Second)
	defer cancel()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	stableStart := time.Now()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout while verifying content stability for file %s", fileName)

		case <-ticker.C:
			content, err := dc.GetGeneralFileContent(ctx, fileName)
			if err != nil {
				return fmt.Errorf("error getting file content: %w", err)
			}

			if !strings.Contains(content, expectedContent) {
				return fmt.Errorf("file content changed unexpectedly, expected to contain %q but got %q", expectedContent, content)
			}

			if time.Since(stableStart) >= stableDuration {
				// Content has been stable for the required duration
				return nil
			}
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

// getDynamicClient creates a dynamic Kubernetes client.
func getDynamicClient(config *rest.Config) (dynamic.Interface, error) {
	return dynamic.NewForConfig(config)
}

// haproxyTemplateConfigGVR returns the GroupVersionResource for HAProxyTemplateConfig.
func haproxyTemplateConfigGVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{
		Group:    "haproxy-template-ic.github.io",
		Version:  "v1alpha1",
		Resource: "haproxytemplateconfigs",
	}
}
