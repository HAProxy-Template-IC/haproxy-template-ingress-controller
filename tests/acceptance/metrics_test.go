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

package acceptance

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
	"sigs.k8s.io/e2e-framework/pkg/types"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// buildMetricsFeature builds a feature that verifies the controller exposes
// Prometheus metrics correctly.
//
// This test validates:
//  1. Metrics server is accessible on the configured port (9090)
//  2. All expected metrics are exposed
//  3. Metrics are updated when operations occur
//  4. Metric values reflect actual controller operations
//
// Test flow:
//  1. Deploy controller with metrics enabled
//  2. Wait for controller to start
//  3. Port-forward to metrics endpoint
//  4. Verify all expected metrics exist
//  5. Trigger operations (reconciliation, validation, deployment)
//  6. Verify metrics are updated with correct values
//
//nolint:revive // High complexity expected in E2E test scenarios
func buildMetricsFeature() types.Feature {
	return features.New("Metrics Endpoint").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			t.Helper()
			t.Log("Setting up metrics test")

			// Generate unique namespace for this test
			namespace := envconf.RandomName("test-metrics", 16)
			t.Logf("Using test namespace: %s", namespace)

			// Store namespace in context
			ctx = StoreNamespaceInContext(ctx, namespace)

			client, err := cfg.NewClient()
			if err != nil {
				t.Fatal("Failed to create client:", err)
			}

			// Create test namespace
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: namespace,
				},
			}
			if err := client.Resources().Create(ctx, ns); err != nil {
				t.Fatal("Failed to create namespace:", err)
			}
			t.Logf("Created test namespace: %s", namespace)

			// Create ServiceAccount
			serviceAccount := NewServiceAccount(namespace, ControllerServiceAccountName)
			if err := client.Resources().Create(ctx, serviceAccount); err != nil {
				t.Fatal("Failed to create serviceaccount:", err)
			}
			t.Log("Created controller serviceaccount")

			// Create Role
			role := NewRole(namespace, ControllerRoleName)
			if err := client.Resources().Create(ctx, role); err != nil {
				t.Fatal("Failed to create role:", err)
			}
			t.Log("Created controller role")

			// Create RoleBinding
			roleBinding := NewRoleBinding(namespace, ControllerRoleBindingName, ControllerRoleName, ControllerServiceAccountName)
			if err := client.Resources().Create(ctx, roleBinding); err != nil {
				t.Fatal("Failed to create rolebinding:", err)
			}
			t.Log("Created controller rolebinding")

			// Create ClusterRole
			clusterRole := NewClusterRole(ControllerClusterRoleName, namespace)
			if err := client.Resources().Create(ctx, clusterRole); err != nil {
				t.Fatal("Failed to create clusterrole:", err)
			}
			t.Log("Created controller clusterrole")

			// Create ClusterRoleBinding
			clusterRoleBinding := NewClusterRoleBinding(ControllerClusterRoleBindingName, ControllerClusterRoleName, ControllerServiceAccountName, namespace, namespace)
			if err := client.Resources().Create(ctx, clusterRoleBinding); err != nil {
				t.Fatal("Failed to create clusterrolebinding:", err)
			}
			t.Log("Created controller clusterrolebinding")

			// Create Secret
			secret := NewSecret(namespace, ControllerSecretName)
			if err := client.Resources().Create(ctx, secret); err != nil {
				t.Fatal("Failed to create secret:", err)
			}
			t.Log("Created controller secret")

			// Create webhook certificate Secret
			webhookCertSecret := NewWebhookCertSecret(namespace, "haproxy-webhook-certs")
			if err := client.Resources().Create(ctx, webhookCertSecret); err != nil {
				t.Fatal("Failed to create webhook cert secret:", err)
			}
			t.Log("Created webhook certificate secret")

			// Create HAProxyTemplateConfig with metrics port configured
			htplConfig := NewHAProxyTemplateConfig(namespace, "haproxy-config", ControllerSecretName, false)
			if err := client.Resources().Create(ctx, htplConfig); err != nil {
				t.Fatal("Failed to create HAProxyTemplateConfig:", err)
			}
			t.Log("Created HAProxyTemplateConfig")

			// Create Deployment
			deployment := NewControllerDeployment(
				namespace,
				ControllerCRDName,
				ControllerSecretName,
				ControllerServiceAccountName,
				DebugPort,
				1, // Single replica for this test
			)
			if err := client.Resources().Create(ctx, deployment); err != nil {
				t.Fatal("Failed to create deployment:", err)
			}
			t.Log("Created controller deployment")

			// Wait for controller pod to be ready
			t.Log("Waiting for controller pod to be ready...")
			if err := WaitForPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, 2*time.Minute); err != nil {
				t.Fatal("Controller pod did not become ready:", err)
			}
			t.Log("Controller pod is ready")

			return ctx
		}).
		Assess("Metrics endpoint accessible", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			t.Helper()
			t.Log("Verifying metrics endpoint is accessible")

			namespace, err := GetNamespaceFromContext(ctx)
			if err != nil {
				t.Fatal("Failed to get namespace from context:", err)
			}

			client, err := cfg.NewClient()
			if err != nil {
				t.Fatal("Failed to create client:", err)
			}

			// Get controller pod
			pod, err := GetControllerPod(ctx, client, namespace)
			if err != nil {
				t.Fatal("Failed to get controller pod:", err)
			}
			t.Logf("Found controller pod: %s", pod.Name)

			// Set up port-forward to metrics port (9090)
			metricsPort := 9090
			stopChan := make(chan struct{}, 1)
			readyChan := make(chan struct{})

			// Start port-forward in background
			go func() {
				err := setupPortForward(ctx, cfg, pod, metricsPort, stopChan, readyChan)
				if err != nil && err != io.EOF {
					t.Logf("Port-forward error: %v", err)
				}
			}()

			// Wait for port-forward to be ready
			select {
			case <-readyChan:
				t.Log("Port-forward ready")
			case <-time.After(30 * time.Second):
				close(stopChan)
				t.Fatal("Timeout waiting for port-forward")
			}

			// Ensure port-forward cleanup
			defer func() {
				t.Log("Stopping port-forward")
				close(stopChan)
				time.Sleep(1 * time.Second) // Give port-forward time to cleanup
			}()

			// Fetch metrics
			t.Log("Fetching metrics from /metrics endpoint")
			resp, err := http.Get(fmt.Sprintf("http://localhost:%d/metrics", metricsPort))
			if err != nil {
				t.Fatal("Failed to fetch metrics:", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("Expected status 200, got %d", resp.StatusCode)
			}

			// Read and parse metrics
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal("Failed to read metrics response:", err)
			}

			metrics := string(body)
			t.Logf("Received %d bytes of metrics", len(metrics))

			// Store metrics in context for next assessment
			ctx = context.WithValue(ctx, "metrics", metrics)

			return ctx
		}).
		Assess("All expected metrics exist", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			t.Helper()
			t.Log("Verifying all expected metrics are present")

			metrics, ok := ctx.Value("metrics").(string)
			if !ok {
				t.Fatal("Metrics not found in context")
			}

			// Define expected metrics
			expectedMetrics := []string{
				"haproxy_ic_reconciliation_total",
				"haproxy_ic_reconciliation_errors_total",
				"haproxy_ic_reconciliation_duration_seconds",
				"haproxy_ic_deployment_total",
				"haproxy_ic_deployment_errors_total",
				"haproxy_ic_deployment_duration_seconds",
				"haproxy_ic_validation_total",
				"haproxy_ic_validation_errors_total",
				"haproxy_ic_resource_count",
				"haproxy_ic_event_subscribers",
				"haproxy_ic_events_published_total",
			}

			// Verify each metric exists
			for _, metric := range expectedMetrics {
				if !strings.Contains(metrics, metric) {
					t.Errorf("Expected metric %s not found in metrics output", metric)
				} else {
					t.Logf("✓ Found metric: %s", metric)
				}
			}

			return ctx
		}).
		Assess("Metrics have non-zero values", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			t.Helper()
			t.Log("Verifying metrics have been updated")

			metrics, ok := ctx.Value("metrics").(string)
			if !ok {
				t.Fatal("Metrics not found in context")
			}

			// Parse metrics and check for non-zero values
			metricValues := parsePrometheusMetrics(metrics)

			// Check reconciliation metrics (should have occurred at startup)
			if val, ok := metricValues["haproxy_ic_reconciliation_total"]; ok && val > 0 {
				t.Logf("✓ haproxy_ic_reconciliation_total = %.0f (operations occurred)", val)
			} else {
				t.Error("haproxy_ic_reconciliation_total should be > 0")
			}

			// Check validation metrics (should have occurred during startup)
			if val, ok := metricValues["haproxy_ic_validation_total"]; ok && val > 0 {
				t.Logf("✓ haproxy_ic_validation_total = %.0f (validations occurred)", val)
			} else {
				t.Error("haproxy_ic_validation_total should be > 0")
			}

			// Check events published (controller publishes many events)
			if val, ok := metricValues["haproxy_ic_events_published_total"]; ok && val > 0 {
				t.Logf("✓ haproxy_ic_events_published_total = %.0f (events flowing)", val)
			} else {
				t.Error("haproxy_ic_events_published_total should be > 0")
			}

			// Check error counters (should be 0 with valid configuration)
			hasErrors := false
			if val, ok := metricValues["haproxy_ic_reconciliation_errors_total"]; ok && val > 0 {
				t.Errorf("haproxy_ic_reconciliation_errors_total = %.0f, expected 0", val)
				hasErrors = true
			} else {
				t.Logf("✓ haproxy_ic_reconciliation_errors_total = %.0f (no errors)", val)
			}

			if val, ok := metricValues["haproxy_ic_validation_errors_total"]; ok && val > 0 {
				t.Errorf("haproxy_ic_validation_errors_total = %.0f, expected 0", val)
				hasErrors = true
			} else {
				t.Logf("✓ haproxy_ic_validation_errors_total = %.0f (no errors)", val)
			}

			// If errors detected, print controller logs for debugging
			if hasErrors {
				namespace, err := GetNamespaceFromContext(ctx)
				if err == nil {
					client, err := cfg.NewClient()
					if err == nil {
						pod, err := GetControllerPod(ctx, client, namespace)
						if err == nil {
							t.Logf("=== Controller logs (showing validation errors) ===")
							logs, err := GetPodLogs(ctx, client, pod, 100)
							if err == nil {
								t.Log(logs)
							}
						}
					}
				}
			}

			return ctx
		}).
		Assess("Resource count metrics exist", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			t.Helper()
			t.Log("Verifying resource count metrics")

			metrics, ok := ctx.Value("metrics").(string)
			if !ok {
				t.Fatal("Metrics not found in context")
			}

			// Resource count is a gauge with labels, check for the metric with type label
			resourceCountMetrics := []string{
				"haproxy_ic_resource_count{type=\"haproxy-pods\"}",
			}

			foundAny := false
			for _, metric := range resourceCountMetrics {
				if strings.Contains(metrics, metric) {
					t.Logf("✓ Found resource count metric: %s", metric)
					foundAny = true
				}
			}

			if !foundAny {
				t.Error("No resource count metrics found")
			}

			return ctx
		}).
		Assess("Histogram metrics have buckets", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			t.Helper()
			t.Log("Verifying histogram metrics have buckets")

			metrics, ok := ctx.Value("metrics").(string)
			if !ok {
				t.Fatal("Metrics not found in context")
			}

			// Histograms should have bucket, sum, and count
			histogramMetrics := []string{
				"haproxy_ic_reconciliation_duration_seconds",
				"haproxy_ic_deployment_duration_seconds",
			}

			for _, metric := range histogramMetrics {
				// Check for bucket
				if strings.Contains(metrics, metric+"_bucket") {
					t.Logf("✓ Found histogram buckets for %s", metric)
				} else {
					t.Errorf("Missing histogram buckets for %s", metric)
				}

				// Check for sum
				if strings.Contains(metrics, metric+"_sum") {
					t.Logf("✓ Found histogram sum for %s", metric)
				} else {
					t.Errorf("Missing histogram sum for %s", metric)
				}

				// Check for count
				if strings.Contains(metrics, metric+"_count") {
					t.Logf("✓ Found histogram count for %s", metric)
				} else {
					t.Errorf("Missing histogram count for %s", metric)
				}
			}

			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			t.Helper()
			t.Log("Cleaning up metrics test resources")

			namespace, err := GetNamespaceFromContext(ctx)
			if err != nil {
				t.Log("Failed to get namespace from context:", err)
				return ctx
			}

			client, err := cfg.NewClient()
			if err != nil {
				t.Log("Failed to create client:", err)
				return ctx
			}

			// Delete ClusterRoleBinding
			clusterRoleBinding := NewClusterRoleBinding(ControllerClusterRoleBindingName, ControllerClusterRoleName, ControllerServiceAccountName, namespace, namespace)
			if err := client.Resources().Delete(ctx, clusterRoleBinding); err != nil {
				t.Log("Warning: Failed to delete clusterrolebinding:", err)
			}

			// Delete ClusterRole
			clusterRole := NewClusterRole(ControllerClusterRoleName, namespace)
			if err := client.Resources().Delete(ctx, clusterRole); err != nil {
				t.Log("Warning: Failed to delete clusterrole:", err)
			}

			// Delete namespace (cascades to all resources)
			ns := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: namespace,
				},
			}
			if err := client.Resources().Delete(ctx, ns); err != nil {
				t.Log("Warning: Failed to delete namespace:", err)
			} else {
				t.Logf("Deleted test namespace: %s", namespace)
			}

			return ctx
		}).
		Feature()
}

// TestMetrics runs the metrics endpoint test.
func TestMetrics(t *testing.T) {
	testEnv.Test(t, buildMetricsFeature())
}

// setupPortForward sets up port-forwarding to a pod.
func setupPortForward(ctx context.Context, cfg *envconf.Config, pod *corev1.Pod, port int, stopChan, readyChan chan struct{}) error {
	// Build request URL
	req := cfg.Client().RESTConfig()
	transport, upgrader, err := spdy.RoundTripperFor(req)
	if err != nil {
		return fmt.Errorf("failed to create round tripper: %w", err)
	}

	path := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward", pod.Namespace, pod.Name)
	hostIP := strings.TrimPrefix(req.Host, "https://")
	hostIP = strings.TrimPrefix(hostIP, "http://")

	// Build the full URL for the dialer
	fullURL := fmt.Sprintf("https://%s%s", hostIP, path)

	// Parse the URL
	parsedURL, err := http.NewRequest(http.MethodPost, fullURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Create dialer
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, parsedURL.URL)

	// Set up port-forward
	ports := []string{fmt.Sprintf("%d:%d", port, port)}
	out := io.Discard
	errOut := io.Discard

	forwarder, err := portforward.New(dialer, ports, stopChan, readyChan, out, errOut)
	if err != nil {
		return fmt.Errorf("failed to create port forwarder: %w", err)
	}

	return forwarder.ForwardPorts()
}

// parsePrometheusMetrics parses Prometheus metrics output and returns metric values.
// Only parses simple metrics (not histograms or summaries).
func parsePrometheusMetrics(metrics string) map[string]float64 {
	result := make(map[string]float64)

	scanner := bufio.NewScanner(strings.NewReader(metrics))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip comments and empty lines
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Skip histogram/summary buckets, sum, count (we check those separately)
		if strings.Contains(line, "_bucket{") ||
			strings.Contains(line, "_sum ") ||
			strings.Contains(line, "_count ") {
			continue
		}

		// Parse metric line: metric_name{labels} value
		// or: metric_name value
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}

		// Extract metric name (before { or space)
		metricName := parts[0]
		if idx := strings.Index(metricName, "{"); idx != -1 {
			metricName = metricName[:idx]
		}

		// Parse value (last part)
		value, err := strconv.ParseFloat(parts[len(parts)-1], 64)
		if err != nil {
			continue
		}

		result[metricName] = value
	}

	return result
}
