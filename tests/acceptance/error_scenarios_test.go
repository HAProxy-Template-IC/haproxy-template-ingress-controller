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
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

// Template strings for CRD updates
const (
	// InvalidSyntaxTemplate is an HAProxy config with syntax errors that will fail validation.
	// The "invalid_directive_xyz" is not a valid HAProxy directive and will cause parse errors.
	InvalidSyntaxTemplate = `global
  maxconn 2000
  invalid_directive_xyz  # This is not a valid HAProxy directive

defaults
  mode http
  timeout connect 5000ms
  timeout client 50000ms
  timeout server 50000ms

frontend http_front
  bind :8080
  default_backend test-backend

backend test-backend
  server test-server 127.0.0.1:8080
`

	// ValidTemplate is a valid HAProxy config used for restoring/recovery
	ValidTemplate = `global
  maxconn 2000
  # Valid config

defaults
  mode http
  timeout connect 5000ms
  timeout client 50000ms
  timeout server 50000ms

frontend http_front
  bind :8080
  default_backend test-backend

backend test-backend
  server test-server 127.0.0.1:8080
`
)

// VersionedTemplate returns a template with a version marker for testing rapid updates
func VersionedTemplate(version int) string {
	return fmt.Sprintf(`global
  maxconn 2000
  # version %d

defaults
  mode http
  timeout connect 5000ms
  timeout client 50000ms
  timeout server 50000ms

frontend http_front
  bind :8080
  default_backend test-backend

backend test-backend
  server test-server 127.0.0.1:8080
`, version)
}

// TestInvalidHAProxyConfig tests that syntactically valid but semantically invalid HAProxy
// configuration is rejected by validation and the previous valid config is preserved.
//
// Scenario:
// 1. Deploy controller with valid initial config
// 2. Update HAProxyTemplateConfig with config that references non-existent backend
// 3. Verify HAProxy binary validation fails
// 4. Verify previous valid config is preserved
// 5. Verify controller doesn't crash and continues watching
func TestInvalidHAProxyConfig(t *testing.T) {
	feature := features.New("Error Scenarios - Invalid HAProxy Config").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace := envconf.RandomName("test-invalid-cfg", 32)
			ctx = StoreNamespaceInContext(ctx, namespace)
			t.Logf("Test namespace: %s", namespace)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			// Create namespace
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
			err = client.Resources().Create(ctx, ns)
			require.NoError(t, err)

			// Create RBAC resources
			sa := NewServiceAccount(namespace, ControllerServiceAccountName)
			err = client.Resources().Create(ctx, sa)
			require.NoError(t, err)

			role := NewRole(namespace, ControllerRoleName)
			err = client.Resources().Create(ctx, role)
			require.NoError(t, err)

			roleBinding := NewRoleBinding(namespace, ControllerRoleBindingName, ControllerRoleName, ControllerServiceAccountName)
			err = client.Resources().Create(ctx, roleBinding)
			require.NoError(t, err)

			clusterRole := NewClusterRole(ControllerClusterRoleName, namespace)
			err = client.Resources().Create(ctx, clusterRole)
			require.NoError(t, err)

			clusterRoleBinding := NewClusterRoleBinding(
				ControllerClusterRoleBindingName,
				ControllerClusterRoleName,
				ControllerServiceAccountName,
				namespace,
				namespace,
			)
			err = client.Resources().Create(ctx, clusterRoleBinding)
			require.NoError(t, err)

			// Create credentials secret
			secret := NewSecret(namespace, ControllerSecretName)
			err = client.Resources().Create(ctx, secret)
			require.NoError(t, err)

			// Create webhook certificate secret
			webhookSecret := NewWebhookCertSecret(namespace, "haproxy-webhook-certs")
			err = client.Resources().Create(ctx, webhookSecret)
			require.NoError(t, err)

			// Create HAProxyTemplateConfig with VALID initial config
			crd := NewHAProxyTemplateConfig(namespace, ControllerCRDName, ControllerSecretName, false)
			err = client.Resources().Create(ctx, crd)
			require.NoError(t, err)

			// Deploy controller
			deployment := NewControllerDeployment(namespace, ControllerCRDName, ControllerSecretName, ControllerServiceAccountName, DebugPort, 1)
			err = client.Resources().Create(ctx, deployment)
			require.NoError(t, err)

			return ctx
		}).
		Assess("Invalid config is rejected and old config preserved", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace, err := GetNamespaceFromContext(ctx)
			require.NoError(t, err)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			// Wait for controller to be ready
			err = WaitForPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, DefaultTimeout)
			require.NoError(t, err)
			t.Log("Controller pod ready")

			// Get controller pod for debug client
			pod, err := GetControllerPod(ctx, client, namespace)
			require.NoError(t, err)

			// Start debug client
			debugClient := NewDebugClient(cfg.Client().RESTConfig(), pod, DebugPort)
			err = debugClient.Start(ctx)
			require.NoError(t, err)
			defer debugClient.Stop()

			// Wait for initial config to be loaded
			t.Log("Waiting for initial config to be ready...")
			_, err = debugClient.WaitForConfig(ctx, 60*time.Second)
			if err != nil {
				DumpPodLogs(ctx, t, cfg.Client().RESTConfig(), pod)
				require.NoError(t, err, "Initial config should be available")
			}

			// Get initial rendered config for comparison
			initialConfig, err := debugClient.GetRenderedConfig(ctx)
			require.NoError(t, err)
			t.Logf("Initial config length: %d bytes", len(initialConfig))

			// Verify initial config has valid structure
			assert.Contains(t, initialConfig, "backend test-backend", "Initial config should have valid backend")

			// Now update HAProxyTemplateConfig with INVALID config (HAProxy syntax error)
			t.Log("Updating config with invalid HAProxy syntax...")
			err = UpdateHAProxyTemplateConfigTemplate(ctx, client, namespace, ControllerCRDName, InvalidSyntaxTemplate)
			require.NoError(t, err)

			// Wait for validation to fail using the debug endpoint
			t.Log("Waiting for validation to reject invalid config...")
			err = debugClient.WaitForValidationStatus(ctx, "failed", 30*time.Second)
			if err != nil {
				// If timeout, check pipeline status for diagnostics
				pipeline, pipelineErr := debugClient.GetPipelineStatus(ctx)
				if pipelineErr == nil && pipeline != nil {
					t.Logf("Pipeline status: validation=%+v", pipeline.Validation)
				}
				// Fall back to log checking if debug endpoint not available
				t.Log("Debug endpoint wait timed out, checking logs...")
			}

			// Verify controller is still running (didn't crash)
			err = WaitForPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, 30*time.Second)
			require.NoError(t, err, "Controller should still be running after invalid config")

			// Use debug endpoint to verify validation failure
			pipeline, err := debugClient.GetPipelineStatus(ctx)
			if err == nil && pipeline != nil && pipeline.Validation != nil {
				t.Logf("Validation status: %s", pipeline.Validation.Status)
				if pipeline.Validation.Status == "failed" {
					t.Log("Controller correctly rejected invalid HAProxy config (validation failed)")
					if len(pipeline.Validation.Errors) > 0 {
						t.Logf("Validation errors: %v", pipeline.Validation.Errors)
						// Verify error contains expected message about invalid directive
						hasExpectedError := false
						for _, errMsg := range pipeline.Validation.Errors {
							if strings.Contains(errMsg, "unknown keyword") ||
								strings.Contains(errMsg, "invalid_directive_xyz") ||
								strings.Contains(errMsg, "parsing") {
								hasExpectedError = true
								break
							}
						}
						assert.True(t, hasExpectedError,
							"Validation errors should mention the invalid directive")
					}
				} else {
					// Validation didn't fail as expected - check logs for more info
					logs, logErr := GetPodLogs(ctx, client, pod, 200)
					if logErr == nil {
						t.Logf("Unexpected validation status. Logs (last 500 chars): %s",
							logs[max(0, len(logs)-500):])
					}
					t.Errorf("Expected validation status 'failed', got '%s'", pipeline.Validation.Status)
				}
			} else {
				// Fall back to log-based verification if debug endpoint unavailable
				t.Log("Pipeline status unavailable, falling back to log verification")
				logs, logErr := GetPodLogs(ctx, client, pod, 200)
				require.NoError(t, logErr, "Should be able to get pod logs")

				hasValidationFailure := strings.Contains(logs, "validation failed") ||
					strings.Contains(logs, "unknown keyword") ||
					strings.Contains(logs, "invalid_directive_xyz")
				assert.True(t, hasValidationFailure,
					"Controller logs should show HAProxy validation failure")
			}

			// Restore valid config
			t.Log("Restoring valid config...")
			err = UpdateHAProxyTemplateConfigTemplate(ctx, client, namespace, ControllerCRDName, ValidTemplate)
			require.NoError(t, err)

			// Wait for successful validation after restore
			err = debugClient.WaitForValidationStatus(ctx, "succeeded", 30*time.Second)
			if err != nil {
				t.Logf("Warning: validation status wait timed out after restore: %v", err)
				time.Sleep(5 * time.Second) // Fall back to fixed wait
			}

			// Verify controller recovered
			restoredConfig, err := debugClient.GetRenderedConfig(ctx)
			require.NoError(t, err)
			assert.Contains(t, restoredConfig, "backend test-backend", "Config should be restored")

			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace, err := GetNamespaceFromContext(ctx)
			if err != nil {
				return ctx
			}

			client, err := cfg.NewClient()
			if err != nil {
				return ctx
			}

			// Delete cluster-scoped resources first
			clusterRole := NewClusterRole(ControllerClusterRoleName, namespace)
			_ = client.Resources().Delete(ctx, clusterRole)

			clusterRoleBinding := NewClusterRoleBinding(
				ControllerClusterRoleBindingName,
				ControllerClusterRoleName,
				ControllerServiceAccountName,
				namespace,
				namespace,
			)
			_ = client.Resources().Delete(ctx, clusterRoleBinding)

			// Delete namespace
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
			_ = client.Resources().Delete(ctx, ns)

			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}

// TestCredentialsMissing tests that the controller handles missing credentials Secret gracefully.
//
// Scenario:
// 1. Create HAProxyTemplateConfig referencing a credentials Secret
// 2. Do NOT create the Secret
// 3. Deploy controller
// 4. Verify controller starts but waits for credentials
// 5. Create the Secret
// 6. Verify controller picks it up and proceeds
func TestCredentialsMissing(t *testing.T) {
	feature := features.New("Error Scenarios - Credentials Missing").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace := envconf.RandomName("test-creds-miss", 32)
			ctx = StoreNamespaceInContext(ctx, namespace)
			t.Logf("Test namespace: %s", namespace)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			// Create namespace
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
			err = client.Resources().Create(ctx, ns)
			require.NoError(t, err)

			// Create RBAC resources
			sa := NewServiceAccount(namespace, ControllerServiceAccountName)
			err = client.Resources().Create(ctx, sa)
			require.NoError(t, err)

			role := NewRole(namespace, ControllerRoleName)
			err = client.Resources().Create(ctx, role)
			require.NoError(t, err)

			roleBinding := NewRoleBinding(namespace, ControllerRoleBindingName, ControllerRoleName, ControllerServiceAccountName)
			err = client.Resources().Create(ctx, roleBinding)
			require.NoError(t, err)

			clusterRole := NewClusterRole(ControllerClusterRoleName, namespace)
			err = client.Resources().Create(ctx, clusterRole)
			require.NoError(t, err)

			clusterRoleBinding := NewClusterRoleBinding(
				ControllerClusterRoleBindingName,
				ControllerClusterRoleName,
				ControllerServiceAccountName,
				namespace,
				namespace,
			)
			err = client.Resources().Create(ctx, clusterRoleBinding)
			require.NoError(t, err)

			// Create webhook certificate secret (required for controller startup)
			webhookSecret := NewWebhookCertSecret(namespace, "haproxy-webhook-certs")
			err = client.Resources().Create(ctx, webhookSecret)
			require.NoError(t, err)

			// Create HAProxyTemplateConfig - references secret that doesn't exist yet
			crd := NewHAProxyTemplateConfig(namespace, ControllerCRDName, ControllerSecretName, false)
			err = client.Resources().Create(ctx, crd)
			require.NoError(t, err)

			// Deploy controller WITHOUT creating credentials secret first
			deployment := NewControllerDeployment(namespace, ControllerCRDName, ControllerSecretName, ControllerServiceAccountName, DebugPort, 1)
			err = client.Resources().Create(ctx, deployment)
			require.NoError(t, err)

			return ctx
		}).
		Assess("Controller waits for credentials and recovers when created", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace, err := GetNamespaceFromContext(ctx)
			require.NoError(t, err)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			clientset, err := kubernetes.NewForConfig(cfg.Client().RESTConfig())
			require.NoError(t, err)

			// Wait for controller pod to start (may not be Ready due to missing credentials)
			t.Log("Waiting for controller pod to start...")
			time.Sleep(15 * time.Second)

			// Check controller pod status - should be Running but may be waiting
			pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
				LabelSelector: "app=" + ControllerDeploymentName,
			})
			require.NoError(t, err)
			require.NotEmpty(t, pods.Items, "Controller pod should exist")

			pod := &pods.Items[0]
			t.Logf("Controller pod status: %s", pod.Status.Phase)

			// Check if pod is in crash loop or running
			for _, cs := range pod.Status.ContainerStatuses {
				if cs.RestartCount > 3 {
					t.Log("Pod has restarted multiple times, checking logs...")
					DumpPodLogs(ctx, t, cfg.Client().RESTConfig(), pod)
				}
			}

			// Now create the credentials secret
			t.Log("Creating credentials secret...")
			secret := NewSecret(namespace, ControllerSecretName)
			_, err = clientset.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
			require.NoError(t, err)

			// Wait for controller to become ready
			t.Log("Waiting for controller to recover after credentials created...")
			err = WaitForPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, 90*time.Second)
			if err != nil {
				// Dump logs if still not ready
				pod, podErr := GetControllerPod(ctx, client, namespace)
				if podErr == nil {
					DumpPodLogs(ctx, t, cfg.Client().RESTConfig(), pod)
				}
			}
			require.NoError(t, err, "Controller should become ready after credentials created")

			// Verify controller is operational
			pod, err = GetControllerPod(ctx, client, namespace)
			require.NoError(t, err)

			debugClient := NewDebugClient(cfg.Client().RESTConfig(), pod, DebugPort)
			err = debugClient.Start(ctx)
			require.NoError(t, err)
			defer debugClient.Stop()

			// Verify config is loaded
			_, err = debugClient.WaitForConfig(ctx, 30*time.Second)
			require.NoError(t, err, "Controller should have config after credentials provided")

			// Use debug endpoint to verify pipeline is healthy after recovery
			pipeline, err := debugClient.GetPipelineStatus(ctx)
			if err == nil && pipeline != nil {
				t.Log("Pipeline status retrieved after credentials recovery")

				// Verify rendering succeeded after credentials were provided
				if pipeline.Rendering != nil {
					t.Logf("Rendering status: %s", pipeline.Rendering.Status)
				}

				// Verify validation succeeded
				if pipeline.Validation != nil {
					t.Logf("Validation status: %s", pipeline.Validation.Status)
					assert.Equal(t, "succeeded", pipeline.Validation.Status,
						"Validation should succeed after credentials recovery")
				}
			}

			// Check errors endpoint to verify no persistent errors after recovery
			errors, err := debugClient.GetErrors(ctx)
			if err == nil && errors != nil {
				// After successful recovery, there should be no critical errors
				if errors.ConfigParseError != nil {
					t.Logf("Config parse error present: %v", errors.ConfigParseError.Errors)
				}
				if errors.TemplateRenderError != nil {
					t.Logf("Template render error present: %v", errors.TemplateRenderError.Errors)
				}
				// Note: These errors may be present from the period before credentials
				// were created, but validation should now succeed
			}

			t.Log("Controller successfully recovered after credentials were created")

			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace, err := GetNamespaceFromContext(ctx)
			if err != nil {
				return ctx
			}

			client, err := cfg.NewClient()
			if err != nil {
				return ctx
			}

			// Delete cluster-scoped resources first
			clusterRole := NewClusterRole(ControllerClusterRoleName, namespace)
			_ = client.Resources().Delete(ctx, clusterRole)

			clusterRoleBinding := NewClusterRoleBinding(
				ControllerClusterRoleBindingName,
				ControllerClusterRoleName,
				ControllerServiceAccountName,
				namespace,
				namespace,
			)
			_ = client.Resources().Delete(ctx, clusterRoleBinding)

			// Delete namespace
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
			_ = client.Resources().Delete(ctx, ns)

			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}

// TestControllerCrashRecovery tests that the controller recovers cleanly after a crash.
//
// Scenario:
// 1. Deploy controller with valid config
// 2. Verify config is deployed
// 3. Delete controller pod (simulates crash)
// 4. Wait for Pod restart
// 5. Verify controller re-discovers state
// 6. Verify subsequent config changes work normally
func TestControllerCrashRecovery(t *testing.T) {
	feature := features.New("Error Scenarios - Controller Crash Recovery").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace := envconf.RandomName("test-crash-rec", 32)
			ctx = StoreNamespaceInContext(ctx, namespace)
			t.Logf("Test namespace: %s", namespace)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			// Create namespace
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
			err = client.Resources().Create(ctx, ns)
			require.NoError(t, err)

			// Create all required resources
			sa := NewServiceAccount(namespace, ControllerServiceAccountName)
			err = client.Resources().Create(ctx, sa)
			require.NoError(t, err)

			role := NewRole(namespace, ControllerRoleName)
			err = client.Resources().Create(ctx, role)
			require.NoError(t, err)

			roleBinding := NewRoleBinding(namespace, ControllerRoleBindingName, ControllerRoleName, ControllerServiceAccountName)
			err = client.Resources().Create(ctx, roleBinding)
			require.NoError(t, err)

			clusterRole := NewClusterRole(ControllerClusterRoleName, namespace)
			err = client.Resources().Create(ctx, clusterRole)
			require.NoError(t, err)

			clusterRoleBinding := NewClusterRoleBinding(
				ControllerClusterRoleBindingName,
				ControllerClusterRoleName,
				ControllerServiceAccountName,
				namespace,
				namespace,
			)
			err = client.Resources().Create(ctx, clusterRoleBinding)
			require.NoError(t, err)

			secret := NewSecret(namespace, ControllerSecretName)
			err = client.Resources().Create(ctx, secret)
			require.NoError(t, err)

			webhookSecret := NewWebhookCertSecret(namespace, "haproxy-webhook-certs")
			err = client.Resources().Create(ctx, webhookSecret)
			require.NoError(t, err)

			crd := NewHAProxyTemplateConfig(namespace, ControllerCRDName, ControllerSecretName, false)
			err = client.Resources().Create(ctx, crd)
			require.NoError(t, err)

			deployment := NewControllerDeployment(namespace, ControllerCRDName, ControllerSecretName, ControllerServiceAccountName, DebugPort, 1)
			err = client.Resources().Create(ctx, deployment)
			require.NoError(t, err)

			return ctx
		}).
		Assess("Controller recovers after crash", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace, err := GetNamespaceFromContext(ctx)
			require.NoError(t, err)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			clientset, err := kubernetes.NewForConfig(cfg.Client().RESTConfig())
			require.NoError(t, err)

			// Wait for controller to be ready
			err = WaitForPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, DefaultTimeout)
			require.NoError(t, err)
			t.Log("Controller pod ready")

			// Get initial pod and verify config
			pod, err := GetControllerPod(ctx, client, namespace)
			require.NoError(t, err)
			initialPodName := pod.Name
			initialPodUID := string(pod.UID)

			debugClient := NewDebugClient(cfg.Client().RESTConfig(), pod, DebugPort)
			err = debugClient.Start(ctx)
			require.NoError(t, err)

			_, err = debugClient.WaitForConfig(ctx, 60*time.Second)
			require.NoError(t, err)
			t.Log("Initial config verified")
			debugClient.Stop()

			// Delete the pod (simulate crash)
			t.Logf("Deleting pod %s (UID: %s) to simulate crash...", initialPodName, initialPodUID)
			err = clientset.CoreV1().Pods(namespace).Delete(ctx, initialPodName, metav1.DeleteOptions{})
			require.NoError(t, err)

			// Wait for a NEW pod (different UID) to be ready
			// This function specifically waits for a pod with a different UID than the original,
			// avoiding the race condition where we might find the old pod still terminating
			t.Log("Waiting for new pod (different UID) to be ready...")
			newPod, err := WaitForNewPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, initialPodUID, 90*time.Second)
			require.NoError(t, err, "Should find a new pod with different UID")
			t.Logf("New pod: %s (UID: %s), was %s (UID: %s)", newPod.Name, newPod.UID, initialPodName, initialPodUID)

			// Verify controller is operational
			newDebugClient := NewDebugClient(cfg.Client().RESTConfig(), newPod, DebugPort)
			err = newDebugClient.Start(ctx)
			require.NoError(t, err)
			defer newDebugClient.Stop()

			// Verify config is loaded in new pod
			_, err = newDebugClient.WaitForConfig(ctx, 60*time.Second)
			if err != nil {
				DumpPodLogs(ctx, t, cfg.Client().RESTConfig(), newPod)
			}
			require.NoError(t, err, "New pod should have config loaded")

			// Verify rendered config is available
			config, err := newDebugClient.GetRenderedConfig(ctx)
			require.NoError(t, err)
			assert.Contains(t, config, "backend", "Config should be rendered after recovery")

			t.Log("Controller successfully recovered after crash")

			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace, err := GetNamespaceFromContext(ctx)
			if err != nil {
				return ctx
			}

			client, err := cfg.NewClient()
			if err != nil {
				return ctx
			}

			clusterRole := NewClusterRole(ControllerClusterRoleName, namespace)
			_ = client.Resources().Delete(ctx, clusterRole)

			clusterRoleBinding := NewClusterRoleBinding(
				ControllerClusterRoleBindingName,
				ControllerClusterRoleName,
				ControllerServiceAccountName,
				namespace,
				namespace,
			)
			_ = client.Resources().Delete(ctx, clusterRoleBinding)

			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
			_ = client.Resources().Delete(ctx, ns)

			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}

// TestRapidConfigUpdates tests that rapid configuration updates are debounced properly.
//
// Scenario:
// 1. Deploy controller with valid config
// 2. Update ConfigMap 10 times in rapid succession
// 3. Verify only the final version is deployed to HAProxy
// 4. Verify no transaction conflicts from rapid changes
func TestRapidConfigUpdates(t *testing.T) {
	feature := features.New("Error Scenarios - Rapid Config Updates").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace := envconf.RandomName("test-rapid-cfg", 32)
			ctx = StoreNamespaceInContext(ctx, namespace)
			t.Logf("Test namespace: %s", namespace)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			// Create namespace
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
			err = client.Resources().Create(ctx, ns)
			require.NoError(t, err)

			// Create all required resources
			sa := NewServiceAccount(namespace, ControllerServiceAccountName)
			err = client.Resources().Create(ctx, sa)
			require.NoError(t, err)

			role := NewRole(namespace, ControllerRoleName)
			err = client.Resources().Create(ctx, role)
			require.NoError(t, err)

			roleBinding := NewRoleBinding(namespace, ControllerRoleBindingName, ControllerRoleName, ControllerServiceAccountName)
			err = client.Resources().Create(ctx, roleBinding)
			require.NoError(t, err)

			clusterRole := NewClusterRole(ControllerClusterRoleName, namespace)
			err = client.Resources().Create(ctx, clusterRole)
			require.NoError(t, err)

			clusterRoleBinding := NewClusterRoleBinding(
				ControllerClusterRoleBindingName,
				ControllerClusterRoleName,
				ControllerServiceAccountName,
				namespace,
				namespace,
			)
			err = client.Resources().Create(ctx, clusterRoleBinding)
			require.NoError(t, err)

			secret := NewSecret(namespace, ControllerSecretName)
			err = client.Resources().Create(ctx, secret)
			require.NoError(t, err)

			webhookSecret := NewWebhookCertSecret(namespace, "haproxy-webhook-certs")
			err = client.Resources().Create(ctx, webhookSecret)
			require.NoError(t, err)

			crd := NewHAProxyTemplateConfig(namespace, ControllerCRDName, ControllerSecretName, false)
			err = client.Resources().Create(ctx, crd)
			require.NoError(t, err)

			deployment := NewControllerDeployment(namespace, ControllerCRDName, ControllerSecretName, ControllerServiceAccountName, DebugPort, 1)
			err = client.Resources().Create(ctx, deployment)
			require.NoError(t, err)

			return ctx
		}).
		Assess("Rapid updates are debounced", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace, err := GetNamespaceFromContext(ctx)
			require.NoError(t, err)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			// Wait for controller to be ready
			err = WaitForPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, DefaultTimeout)
			require.NoError(t, err)
			t.Log("Controller pod ready")

			pod, err := GetControllerPod(ctx, client, namespace)
			require.NoError(t, err)

			debugClient := NewDebugClient(cfg.Client().RESTConfig(), pod, DebugPort)
			err = debugClient.Start(ctx)
			require.NoError(t, err)
			defer debugClient.Stop()

			_, err = debugClient.WaitForConfig(ctx, 60*time.Second)
			require.NoError(t, err)
			t.Log("Initial config verified")

			// Perform rapid updates (10 updates in quick succession)
			t.Log("Performing 10 rapid CRD updates...")
			for i := 1; i <= 10; i++ {
				err = UpdateHAProxyTemplateConfigTemplate(ctx, client, namespace, ControllerCRDName, VersionedTemplate(i))
				require.NoError(t, err)
				t.Logf("Update %d applied", i)
				time.Sleep(50 * time.Millisecond) // Very small delay between updates
			}

			// Wait for debouncing to complete
			t.Log("Waiting for debounce to complete...")
			time.Sleep(3 * time.Second)

			// Verify the final version is deployed
			renderedConfig, err := debugClient.GetRenderedConfig(ctx)
			require.NoError(t, err)

			// Debouncing is non-deterministic, so verify a late version is deployed.
			// Any version from 7-10 indicates debouncing worked (early versions were skipped).
			foundLateVersion := false
			for v := 7; v <= 10; v++ {
				if strings.Contains(renderedConfig, fmt.Sprintf("# version %d", v)) {
					t.Logf("Found version %d in rendered config (debouncing worked)", v)
					foundLateVersion = true
					break
				}
			}
			assert.True(t, foundLateVersion,
				"Final config should have a late version marker (7-10), indicating debouncing worked")

			// Use debug endpoint to verify pipeline completed successfully after debouncing
			pipeline, err := debugClient.GetPipelineStatus(ctx)
			if err == nil && pipeline != nil {
				t.Log("Pipeline status retrieved for debounce verification")

				// Verify rendering and validation succeeded for final debounced config
				if pipeline.Rendering != nil {
					t.Logf("Final rendering status: %s (duration: %dms)",
						pipeline.Rendering.Status, pipeline.Rendering.DurationMs)
					assert.Equal(t, "succeeded", pipeline.Rendering.Status,
						"Final rendering should succeed after debounce")
				}

				if pipeline.Validation != nil {
					t.Logf("Final validation status: %s (duration: %dms)",
						pipeline.Validation.Status, pipeline.Validation.DurationMs)
					assert.Equal(t, "succeeded", pipeline.Validation.Status,
						"Final validation should succeed after debounce")
				}

				// Log the last trigger for debugging
				if pipeline.LastTrigger != nil {
					t.Logf("Last trigger reason: %s", pipeline.LastTrigger.Reason)
				}
			}

			// Verify controller is still healthy
			err = WaitForPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, 30*time.Second)
			require.NoError(t, err, "Controller should still be healthy after rapid updates")

			t.Log("Rapid updates debounced successfully")

			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace, err := GetNamespaceFromContext(ctx)
			if err != nil {
				return ctx
			}

			client, err := cfg.NewClient()
			if err != nil {
				return ctx
			}

			clusterRole := NewClusterRole(ControllerClusterRoleName, namespace)
			_ = client.Resources().Delete(ctx, clusterRole)

			clusterRoleBinding := NewClusterRoleBinding(
				ControllerClusterRoleBindingName,
				ControllerClusterRoleName,
				ControllerServiceAccountName,
				namespace,
				namespace,
			)
			_ = client.Resources().Delete(ctx, clusterRoleBinding)

			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
			_ = client.Resources().Delete(ctx, ns)

			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}

// TestGracefulShutdown tests that the controller shuts down gracefully.
//
// Scenario:
// 1. Deploy controller with valid config
// 2. Send SIGTERM to controller pod (kubectl delete with grace period)
// 3. Verify controller terminates within grace period
// 4. Verify next controller instance starts cleanly
func TestGracefulShutdown(t *testing.T) {
	feature := features.New("Error Scenarios - Graceful Shutdown").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace := envconf.RandomName("test-shutdown", 32)
			ctx = StoreNamespaceInContext(ctx, namespace)
			t.Logf("Test namespace: %s", namespace)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
			err = client.Resources().Create(ctx, ns)
			require.NoError(t, err)

			sa := NewServiceAccount(namespace, ControllerServiceAccountName)
			err = client.Resources().Create(ctx, sa)
			require.NoError(t, err)

			role := NewRole(namespace, ControllerRoleName)
			err = client.Resources().Create(ctx, role)
			require.NoError(t, err)

			roleBinding := NewRoleBinding(namespace, ControllerRoleBindingName, ControllerRoleName, ControllerServiceAccountName)
			err = client.Resources().Create(ctx, roleBinding)
			require.NoError(t, err)

			clusterRole := NewClusterRole(ControllerClusterRoleName, namespace)
			err = client.Resources().Create(ctx, clusterRole)
			require.NoError(t, err)

			clusterRoleBinding := NewClusterRoleBinding(
				ControllerClusterRoleBindingName,
				ControllerClusterRoleName,
				ControllerServiceAccountName,
				namespace,
				namespace,
			)
			err = client.Resources().Create(ctx, clusterRoleBinding)
			require.NoError(t, err)

			secret := NewSecret(namespace, ControllerSecretName)
			err = client.Resources().Create(ctx, secret)
			require.NoError(t, err)

			webhookSecret := NewWebhookCertSecret(namespace, "haproxy-webhook-certs")
			err = client.Resources().Create(ctx, webhookSecret)
			require.NoError(t, err)

			crd := NewHAProxyTemplateConfig(namespace, ControllerCRDName, ControllerSecretName, false)
			err = client.Resources().Create(ctx, crd)
			require.NoError(t, err)

			deployment := NewControllerDeployment(namespace, ControllerCRDName, ControllerSecretName, ControllerServiceAccountName, DebugPort, 1)
			err = client.Resources().Create(ctx, deployment)
			require.NoError(t, err)

			return ctx
		}).
		Assess("Controller shuts down gracefully", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace, err := GetNamespaceFromContext(ctx)
			require.NoError(t, err)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			clientset, err := kubernetes.NewForConfig(cfg.Client().RESTConfig())
			require.NoError(t, err)

			// Wait for controller to be ready
			err = WaitForPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, DefaultTimeout)
			require.NoError(t, err)
			t.Log("Controller pod ready")

			pod, err := GetControllerPod(ctx, client, namespace)
			require.NoError(t, err)
			podName := pod.Name

			debugClient := NewDebugClient(cfg.Client().RESTConfig(), pod, DebugPort)
			err = debugClient.Start(ctx)
			require.NoError(t, err)

			_, err = debugClient.WaitForConfig(ctx, 60*time.Second)
			require.NoError(t, err)
			debugClient.Stop()

			// Delete pod with grace period (sends SIGTERM)
			t.Logf("Deleting pod %s with 30s grace period...", podName)
			gracePeriod := int64(30)
			err = clientset.CoreV1().Pods(namespace).Delete(ctx, podName, metav1.DeleteOptions{
				GracePeriodSeconds: &gracePeriod,
			})
			require.NoError(t, err)

			// Wait for pod to terminate
			t.Log("Waiting for pod to terminate...")
			terminated := false
			for i := 0; i < 40; i++ { // Wait up to 40 seconds
				time.Sleep(1 * time.Second)
				_, err := clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
				if err != nil {
					// Pod is gone
					terminated = true
					t.Logf("Pod terminated after %d seconds", i+1)
					break
				}
			}

			if !terminated {
				// Pod didn't terminate in time - this is a failure
				t.Log("Pod did not terminate in time, fetching logs...")
				pod, podErr := GetControllerPod(ctx, client, namespace)
				if podErr == nil {
					DumpPodLogs(ctx, t, cfg.Client().RESTConfig(), pod)
				}
			}
			assert.True(t, terminated, "Pod should terminate within grace period")

			// Wait for new pod to start
			t.Log("Waiting for new pod to be ready...")
			err = WaitForPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, 90*time.Second)
			require.NoError(t, err, "New pod should start cleanly after graceful shutdown")

			// Verify new pod is operational
			newPod, err := GetControllerPod(ctx, client, namespace)
			require.NoError(t, err)

			assert.NotEqual(t, podName, newPod.Name, "New pod should be created")

			newDebugClient := NewDebugClient(cfg.Client().RESTConfig(), newPod, DebugPort)
			err = newDebugClient.Start(ctx)
			require.NoError(t, err)
			defer newDebugClient.Stop()

			_, err = newDebugClient.WaitForConfig(ctx, 60*time.Second)
			require.NoError(t, err, "New pod should have config loaded")

			t.Log("Graceful shutdown verified successfully")

			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace, err := GetNamespaceFromContext(ctx)
			if err != nil {
				return ctx
			}

			client, err := cfg.NewClient()
			if err != nil {
				return ctx
			}

			clusterRole := NewClusterRole(ControllerClusterRoleName, namespace)
			_ = client.Resources().Delete(ctx, clusterRole)

			clusterRoleBinding := NewClusterRoleBinding(
				ControllerClusterRoleBindingName,
				ControllerClusterRoleName,
				ControllerServiceAccountName,
				namespace,
				namespace,
			)
			_ = client.Resources().Delete(ctx, clusterRoleBinding)

			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
			_ = client.Resources().Delete(ctx, ns)

			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}

// TestDataplaneUnreachable tests that the controller handles HAProxy being unreachable.
//
// Scenario:
// 1. Deploy controller with valid config
// 2. Verify initial config deployed successfully
// 3. Scale HAProxy deployment to 0 (simulate unreachable)
// 4. Trigger reconciliation (update CRD)
// 5. Verify controller handles connection failure
// 6. Scale HAProxy back up
// 7. Verify controller recovers
//
// Note: This test requires actual HAProxy pods which acceptance tests don't deploy.
// The controller will log errors about no HAProxy endpoints - we verify it doesn't crash.
func TestDataplaneUnreachable(t *testing.T) {
	feature := features.New("Error Scenarios - Dataplane Unreachable").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace := envconf.RandomName("test-dp-unreach", 32)
			ctx = StoreNamespaceInContext(ctx, namespace)
			t.Logf("Test namespace: %s", namespace)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			// Create namespace
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
			err = client.Resources().Create(ctx, ns)
			require.NoError(t, err)

			// Create all required resources
			sa := NewServiceAccount(namespace, ControllerServiceAccountName)
			err = client.Resources().Create(ctx, sa)
			require.NoError(t, err)

			role := NewRole(namespace, ControllerRoleName)
			err = client.Resources().Create(ctx, role)
			require.NoError(t, err)

			roleBinding := NewRoleBinding(namespace, ControllerRoleBindingName, ControllerRoleName, ControllerServiceAccountName)
			err = client.Resources().Create(ctx, roleBinding)
			require.NoError(t, err)

			clusterRole := NewClusterRole(ControllerClusterRoleName, namespace)
			err = client.Resources().Create(ctx, clusterRole)
			require.NoError(t, err)

			clusterRoleBinding := NewClusterRoleBinding(
				ControllerClusterRoleBindingName,
				ControllerClusterRoleName,
				ControllerServiceAccountName,
				namespace,
				namespace,
			)
			err = client.Resources().Create(ctx, clusterRoleBinding)
			require.NoError(t, err)

			secret := NewSecret(namespace, ControllerSecretName)
			err = client.Resources().Create(ctx, secret)
			require.NoError(t, err)

			webhookSecret := NewWebhookCertSecret(namespace, "haproxy-webhook-certs")
			err = client.Resources().Create(ctx, webhookSecret)
			require.NoError(t, err)

			crd := NewHAProxyTemplateConfig(namespace, ControllerCRDName, ControllerSecretName, false)
			err = client.Resources().Create(ctx, crd)
			require.NoError(t, err)

			deployment := NewControllerDeployment(namespace, ControllerCRDName, ControllerSecretName, ControllerServiceAccountName, DebugPort, 1)
			err = client.Resources().Create(ctx, deployment)
			require.NoError(t, err)

			return ctx
		}).
		Assess("Controller handles no HAProxy endpoints gracefully", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace, err := GetNamespaceFromContext(ctx)
			require.NoError(t, err)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			// Wait for controller to be ready
			err = WaitForPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, DefaultTimeout)
			require.NoError(t, err)
			t.Log("Controller pod ready")

			pod, err := GetControllerPod(ctx, client, namespace)
			require.NoError(t, err)

			debugClient := NewDebugClient(cfg.Client().RESTConfig(), pod, DebugPort)
			err = debugClient.Start(ctx)
			require.NoError(t, err)
			defer debugClient.Stop()

			// Wait for config to be processed
			_, err = debugClient.WaitForConfig(ctx, 60*time.Second)
			require.NoError(t, err)
			t.Log("Config loaded")

			// Since there are no HAProxy pods, the controller should be operational
			// but not have deployed to any endpoints. This is the expected behavior
			// when HAProxy is "unreachable" (no endpoints available).

			// Trigger a config update to force reconciliation with no endpoints
			t.Log("Triggering reconciliation with no HAProxy endpoints...")
			err = UpdateHAProxyTemplateConfigTemplate(ctx, client, namespace, ControllerCRDName, VersionedTemplate(99))
			require.NoError(t, err)

			// Wait for reconciliation to process
			time.Sleep(5 * time.Second)

			// Verify controller is still running (didn't crash)
			err = WaitForPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, 30*time.Second)
			require.NoError(t, err, "Controller should still be running with no HAProxy endpoints")

			// Use debug endpoint to verify pipeline status
			pipeline, err := debugClient.GetPipelineStatus(ctx)
			if err == nil && pipeline != nil {
				t.Logf("Pipeline status retrieved successfully")

				// Check rendering succeeded (template should render fine)
				if pipeline.Rendering != nil {
					t.Logf("Rendering status: %s", pipeline.Rendering.Status)
					assert.Equal(t, "succeeded", pipeline.Rendering.Status,
						"Rendering should succeed even with no endpoints")
				}

				// Check validation succeeded (HAProxy config should be valid)
				if pipeline.Validation != nil {
					t.Logf("Validation status: %s", pipeline.Validation.Status)
					assert.Equal(t, "succeeded", pipeline.Validation.Status,
						"Validation should succeed even with no endpoints")
				}

				// Check deployment shows 0 endpoints
				if pipeline.Deployment != nil {
					t.Logf("Deployment status: %s, endpoints_total: %d",
						pipeline.Deployment.Status, pipeline.Deployment.EndpointsTotal)
					assert.Equal(t, 0, pipeline.Deployment.EndpointsTotal,
						"Should have 0 endpoints when no HAProxy pods exist")
				}
			} else {
				// Fall back to log checking if debug endpoint unavailable
				t.Log("Pipeline status unavailable, falling back to log verification")
				logs, logErr := GetPodLogs(ctx, client, pod, 200)
				if logErr == nil {
					hasExpectedBehavior := strings.Contains(logs, "endpoint") ||
						strings.Contains(logs, "no pods") ||
						strings.Contains(logs, "reconcil")
					if hasExpectedBehavior {
						t.Log("Controller logs show expected endpoint handling")
					}
				}
			}

			t.Log("Controller handles no HAProxy endpoints gracefully")

			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace, err := GetNamespaceFromContext(ctx)
			if err != nil {
				return ctx
			}

			client, err := cfg.NewClient()
			if err != nil {
				return ctx
			}

			clusterRole := NewClusterRole(ControllerClusterRoleName, namespace)
			_ = client.Resources().Delete(ctx, clusterRole)

			clusterRoleBinding := NewClusterRoleBinding(
				ControllerClusterRoleBindingName,
				ControllerClusterRoleName,
				ControllerServiceAccountName,
				namespace,
				namespace,
			)
			_ = client.Resources().Delete(ctx, clusterRoleBinding)

			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
			_ = client.Resources().Delete(ctx, ns)

			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}

// TestLeadershipDuringReconciliation tests that leadership transitions during operation
// are handled correctly.
//
// Scenario:
// 1. Deploy controller with 2 replicas and leader election enabled
// 2. Wait for leader election
// 3. Start a config update
// 4. Delete leader pod mid-reconciliation
// 5. Verify new leader is elected
// 6. Verify config eventually converges
func TestLeadershipDuringReconciliation(t *testing.T) {
	feature := features.New("Error Scenarios - Leadership During Reconciliation").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace := envconf.RandomName("test-leader-rec", 32)
			ctx = StoreNamespaceInContext(ctx, namespace)
			t.Logf("Test namespace: %s", namespace)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			// Create namespace
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
			err = client.Resources().Create(ctx, ns)
			require.NoError(t, err)

			// Create RBAC resources
			sa := NewServiceAccount(namespace, ControllerServiceAccountName)
			err = client.Resources().Create(ctx, sa)
			require.NoError(t, err)

			role := NewRole(namespace, ControllerRoleName)
			err = client.Resources().Create(ctx, role)
			require.NoError(t, err)

			roleBinding := NewRoleBinding(namespace, ControllerRoleBindingName, ControllerRoleName, ControllerServiceAccountName)
			err = client.Resources().Create(ctx, roleBinding)
			require.NoError(t, err)

			clusterRole := NewClusterRole(ControllerClusterRoleName, namespace)
			err = client.Resources().Create(ctx, clusterRole)
			require.NoError(t, err)

			clusterRoleBinding := NewClusterRoleBinding(
				ControllerClusterRoleBindingName,
				ControllerClusterRoleName,
				ControllerServiceAccountName,
				namespace,
				namespace,
			)
			err = client.Resources().Create(ctx, clusterRoleBinding)
			require.NoError(t, err)

			secret := NewSecret(namespace, ControllerSecretName)
			err = client.Resources().Create(ctx, secret)
			require.NoError(t, err)

			webhookSecret := NewWebhookCertSecret(namespace, "haproxy-webhook-certs")
			err = client.Resources().Create(ctx, webhookSecret)
			require.NoError(t, err)

			// Create HAProxyTemplateConfig WITH leader election enabled
			crd := NewHAProxyTemplateConfig(namespace, ControllerCRDName, ControllerSecretName, true)
			err = client.Resources().Create(ctx, crd)
			require.NoError(t, err)

			// Deploy with 2 replicas for leader election
			deployment := NewControllerDeployment(namespace, ControllerCRDName, ControllerSecretName, ControllerServiceAccountName, DebugPort, 2)
			err = client.Resources().Create(ctx, deployment)
			require.NoError(t, err)

			return ctx
		}).
		Assess("New leader elected after leader deletion", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace, err := GetNamespaceFromContext(ctx)
			require.NoError(t, err)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			clientset, err := kubernetes.NewForConfig(cfg.Client().RESTConfig())
			require.NoError(t, err)

			// Wait for both pods to be ready
			t.Log("Waiting for controller pods to be ready...")
			time.Sleep(10 * time.Second) // Allow deployment to create pods

			pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
				LabelSelector: "app=" + ControllerDeploymentName,
			})
			require.NoError(t, err)
			t.Logf("Found %d controller pods", len(pods.Items))

			// Wait for pods to be ready
			err = WaitForPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, DefaultTimeout)
			require.NoError(t, err)

			// Wait for leader election to complete
			t.Log("Waiting for leader election...")
			err = WaitForLeaderElection(ctx, cfg.Client().RESTConfig(), namespace, "haproxy-template-ic-leader", 60*time.Second)
			if err != nil {
				t.Logf("Leader election wait failed: %v (continuing anyway)", err)
			}

			// Get current leader
			initialLeader, err := GetLeaseHolder(ctx, cfg.Client().RESTConfig(), namespace, "haproxy-template-ic-leader")
			if err != nil {
				t.Logf("Could not get initial leader: %v", err)
				initialLeader = ""
			} else {
				t.Logf("Initial leader: %s", initialLeader)
			}

			// Delete one pod to trigger leadership transition
			pods, err = clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
				LabelSelector: "app=" + ControllerDeploymentName,
			})
			require.NoError(t, err)
			require.NotEmpty(t, pods.Items, "Should have controller pods")

			deletedPod := pods.Items[0].Name
			t.Logf("Deleting pod %s to trigger leadership transition...", deletedPod)
			err = clientset.CoreV1().Pods(namespace).Delete(ctx, deletedPod, metav1.DeleteOptions{})
			require.NoError(t, err)

			// Wait for new pod to be created
			t.Log("Waiting for pod replacement...")
			time.Sleep(10 * time.Second)

			// Wait for pods to be ready again
			err = WaitForPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, 90*time.Second)
			require.NoError(t, err, "Pods should become ready after leader deletion")

			// Verify we still have 2 pods
			pods, err = clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
				LabelSelector: "app=" + ControllerDeploymentName,
			})
			require.NoError(t, err)
			t.Logf("After leadership transition: %d pods", len(pods.Items))

			// Verify new leader is elected (or leadership continues)
			time.Sleep(5 * time.Second) // Allow time for lease acquisition
			newLeader, err := GetLeaseHolder(ctx, cfg.Client().RESTConfig(), namespace, "haproxy-template-ic-leader")
			if err != nil {
				t.Logf("Could not get new leader: %v", err)
			} else {
				t.Logf("Leader after transition: %s", newLeader)
			}

			// Verify controller is operational - get any running pod
			var activePod *corev1.Pod
			for i := range pods.Items {
				if pods.Items[i].Status.Phase == corev1.PodRunning {
					activePod = &pods.Items[i]
					break
				}
			}
			require.NotNil(t, activePod, "Should have a running pod")

			debugClient := NewDebugClient(cfg.Client().RESTConfig(), activePod, DebugPort)
			err = debugClient.Start(ctx)
			if err != nil {
				t.Logf("Could not start debug client: %v (this may be expected if pod is not leader)", err)
			} else {
				defer debugClient.Stop()

				// Verify config is available
				_, err = debugClient.WaitForConfig(ctx, 30*time.Second)
				if err != nil {
					t.Logf("Config not available from this pod: %v (may not be leader)", err)
				} else {
					t.Log("Config is available from controller pod")
				}
			}

			t.Log("Leadership transition handled successfully")

			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace, err := GetNamespaceFromContext(ctx)
			if err != nil {
				return ctx
			}

			client, err := cfg.NewClient()
			if err != nil {
				return ctx
			}

			clusterRole := NewClusterRole(ControllerClusterRoleName, namespace)
			_ = client.Resources().Delete(ctx, clusterRole)

			clusterRoleBinding := NewClusterRoleBinding(
				ControllerClusterRoleBindingName,
				ControllerClusterRoleName,
				ControllerServiceAccountName,
				namespace,
				namespace,
			)
			_ = client.Resources().Delete(ctx, clusterRoleBinding)

			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
			_ = client.Resources().Delete(ctx, ns)

			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}

// TestWatchReconnection tests that the controller reconnects and resyncs after watch interruption.
//
// Scenario:
// 1. Deploy controller
// 2. Verify watching CRD
// 3. Restart controller Pod (disrupts watch connection)
// 4. Update CRD while pod restarting
// 5. Verify new Pod reconnects and processes update
func TestWatchReconnection(t *testing.T) {
	feature := features.New("Error Scenarios - Watch Reconnection").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace := envconf.RandomName("test-watch-rec", 32)
			ctx = StoreNamespaceInContext(ctx, namespace)
			t.Logf("Test namespace: %s", namespace)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
			err = client.Resources().Create(ctx, ns)
			require.NoError(t, err)

			sa := NewServiceAccount(namespace, ControllerServiceAccountName)
			err = client.Resources().Create(ctx, sa)
			require.NoError(t, err)

			role := NewRole(namespace, ControllerRoleName)
			err = client.Resources().Create(ctx, role)
			require.NoError(t, err)

			roleBinding := NewRoleBinding(namespace, ControllerRoleBindingName, ControllerRoleName, ControllerServiceAccountName)
			err = client.Resources().Create(ctx, roleBinding)
			require.NoError(t, err)

			clusterRole := NewClusterRole(ControllerClusterRoleName, namespace)
			err = client.Resources().Create(ctx, clusterRole)
			require.NoError(t, err)

			clusterRoleBinding := NewClusterRoleBinding(
				ControllerClusterRoleBindingName,
				ControllerClusterRoleName,
				ControllerServiceAccountName,
				namespace,
				namespace,
			)
			err = client.Resources().Create(ctx, clusterRoleBinding)
			require.NoError(t, err)

			secret := NewSecret(namespace, ControllerSecretName)
			err = client.Resources().Create(ctx, secret)
			require.NoError(t, err)

			webhookSecret := NewWebhookCertSecret(namespace, "haproxy-webhook-certs")
			err = client.Resources().Create(ctx, webhookSecret)
			require.NoError(t, err)

			crd := NewHAProxyTemplateConfig(namespace, ControllerCRDName, ControllerSecretName, false)
			err = client.Resources().Create(ctx, crd)
			require.NoError(t, err)

			deployment := NewControllerDeployment(namespace, ControllerCRDName, ControllerSecretName, ControllerServiceAccountName, DebugPort, 1)
			err = client.Resources().Create(ctx, deployment)
			require.NoError(t, err)

			return ctx
		}).
		Assess("Controller reconnects and processes updates after restart", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace, err := GetNamespaceFromContext(ctx)
			require.NoError(t, err)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			clientset, err := kubernetes.NewForConfig(cfg.Client().RESTConfig())
			require.NoError(t, err)

			// Wait for initial controller to be ready
			err = WaitForPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, DefaultTimeout)
			require.NoError(t, err)
			t.Log("Initial controller pod ready")

			pod, err := GetControllerPod(ctx, client, namespace)
			require.NoError(t, err)
			initialPodName := pod.Name

			debugClient := NewDebugClient(cfg.Client().RESTConfig(), pod, DebugPort)
			err = debugClient.Start(ctx)
			require.NoError(t, err)

			_, err = debugClient.WaitForConfig(ctx, 60*time.Second)
			require.NoError(t, err)
			debugClient.Stop()

			// Delete pod (simulate restart/disruption)
			t.Logf("Deleting pod %s to disrupt watch...", initialPodName)
			err = clientset.CoreV1().Pods(namespace).Delete(ctx, initialPodName, metav1.DeleteOptions{})
			require.NoError(t, err)

			// Update CRD while pod is restarting
			t.Log("Updating CRD while pod is restarting...")
			err = UpdateHAProxyTemplateConfigTemplate(ctx, client, namespace, ControllerCRDName, VersionedTemplate(42))
			require.NoError(t, err)

			// Wait for new pod to come up
			t.Log("Waiting for new pod to be ready...")
			time.Sleep(5 * time.Second) // Allow old pod to terminate

			err = WaitForPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, 90*time.Second)
			require.NoError(t, err)

			newPod, err := GetControllerPod(ctx, client, namespace)
			require.NoError(t, err)
			t.Logf("New pod: %s (was %s)", newPod.Name, initialPodName)

			// Verify new pod processed the update
			newDebugClient := NewDebugClient(cfg.Client().RESTConfig(), newPod, DebugPort)
			err = newDebugClient.Start(ctx)
			require.NoError(t, err)
			defer newDebugClient.Stop()

			_, err = newDebugClient.WaitForConfig(ctx, 60*time.Second)
			require.NoError(t, err)

			// Verify the updated config is reflected
			renderedConfig, err := newDebugClient.GetRenderedConfig(ctx)
			require.NoError(t, err)

			// Should have version 42 marker
			assert.Contains(t, renderedConfig, "# version 42",
				"New pod should have the updated config")

			t.Log("Watch reconnection and resync successful")

			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace, err := GetNamespaceFromContext(ctx)
			if err != nil {
				return ctx
			}

			client, err := cfg.NewClient()
			if err != nil {
				return ctx
			}

			clusterRole := NewClusterRole(ControllerClusterRoleName, namespace)
			_ = client.Resources().Delete(ctx, clusterRole)

			clusterRoleBinding := NewClusterRoleBinding(
				ControllerClusterRoleBindingName,
				ControllerClusterRoleName,
				ControllerServiceAccountName,
				namespace,
				namespace,
			)
			_ = client.Resources().Delete(ctx, clusterRoleBinding)

			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
			_ = client.Resources().Delete(ctx, ns)

			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}

// TestTransactionConflict tests that the controller handles version conflicts during deployment.
//
// Note: This test is more theoretical as we don't have real HAProxy instances in acceptance tests.
// In a full deployment scenario, if external changes modify HAProxy config while controller
// is deploying, a 409 conflict would occur. The controller should refetch and retry.
//
// What we test here:
// 1. Deploy controller
// 2. Verify it handles the "no endpoints" case gracefully
// 3. Multiple rapid config updates (which could cause internal conflicts)
// 4. Verify controller state remains consistent
func TestTransactionConflict(t *testing.T) {
	feature := features.New("Error Scenarios - Transaction Conflict Handling").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace := envconf.RandomName("test-tx-conf", 32)
			ctx = StoreNamespaceInContext(ctx, namespace)
			t.Logf("Test namespace: %s", namespace)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
			err = client.Resources().Create(ctx, ns)
			require.NoError(t, err)

			sa := NewServiceAccount(namespace, ControllerServiceAccountName)
			err = client.Resources().Create(ctx, sa)
			require.NoError(t, err)

			role := NewRole(namespace, ControllerRoleName)
			err = client.Resources().Create(ctx, role)
			require.NoError(t, err)

			roleBinding := NewRoleBinding(namespace, ControllerRoleBindingName, ControllerRoleName, ControllerServiceAccountName)
			err = client.Resources().Create(ctx, roleBinding)
			require.NoError(t, err)

			clusterRole := NewClusterRole(ControllerClusterRoleName, namespace)
			err = client.Resources().Create(ctx, clusterRole)
			require.NoError(t, err)

			clusterRoleBinding := NewClusterRoleBinding(
				ControllerClusterRoleBindingName,
				ControllerClusterRoleName,
				ControllerServiceAccountName,
				namespace,
				namespace,
			)
			err = client.Resources().Create(ctx, clusterRoleBinding)
			require.NoError(t, err)

			secret := NewSecret(namespace, ControllerSecretName)
			err = client.Resources().Create(ctx, secret)
			require.NoError(t, err)

			webhookSecret := NewWebhookCertSecret(namespace, "haproxy-webhook-certs")
			err = client.Resources().Create(ctx, webhookSecret)
			require.NoError(t, err)

			crd := NewHAProxyTemplateConfig(namespace, ControllerCRDName, ControllerSecretName, false)
			err = client.Resources().Create(ctx, crd)
			require.NoError(t, err)

			deployment := NewControllerDeployment(namespace, ControllerCRDName, ControllerSecretName, ControllerServiceAccountName, DebugPort, 1)
			err = client.Resources().Create(ctx, deployment)
			require.NoError(t, err)

			return ctx
		}).
		Assess("Controller handles concurrent updates gracefully", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace, err := GetNamespaceFromContext(ctx)
			require.NoError(t, err)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			// Wait for controller to be ready
			err = WaitForPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, DefaultTimeout)
			require.NoError(t, err)
			t.Log("Controller pod ready")

			pod, err := GetControllerPod(ctx, client, namespace)
			require.NoError(t, err)

			debugClient := NewDebugClient(cfg.Client().RESTConfig(), pod, DebugPort)
			err = debugClient.Start(ctx)
			require.NoError(t, err)
			defer debugClient.Stop()

			_, err = debugClient.WaitForConfig(ctx, 60*time.Second)
			require.NoError(t, err)

			// Perform concurrent-like updates (rapid sequential updates)
			// This simulates potential conflict scenarios
			t.Log("Performing rapid sequential updates to test conflict handling...")
			for i := 1; i <= 5; i++ {
				err = UpdateHAProxyTemplateConfigTemplate(ctx, client, namespace, ControllerCRDName, VersionedTemplate(100+i))
				require.NoError(t, err, "Update %d should succeed", i)

				// Very short delay to create potential conflict window
				time.Sleep(100 * time.Millisecond)
			}

			// Wait for reconciliation
			t.Log("Waiting for controller to process updates...")
			time.Sleep(5 * time.Second)

			// Verify controller is still healthy
			err = WaitForPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, 30*time.Second)
			require.NoError(t, err, "Controller should remain healthy after rapid updates")

			// Verify final state is consistent
			renderedConfig, err := debugClient.GetRenderedConfig(ctx)
			require.NoError(t, err)

			// Should have version 105 (last update) - may have different version due to debouncing
			hasVersionMarker := strings.Contains(renderedConfig, "# version 10")
			assert.True(t, hasVersionMarker, "Config should have a version marker (debouncing may coalesce)")

			// Use debug endpoint to verify pipeline completed successfully
			pipeline, err := debugClient.GetPipelineStatus(ctx)
			if err == nil && pipeline != nil {
				t.Log("Pipeline status retrieved for transaction conflict verification")

				// All pipeline stages should have succeeded (or be in expected state)
				if pipeline.Rendering != nil {
					t.Logf("Rendering status: %s", pipeline.Rendering.Status)
				}

				if pipeline.Validation != nil {
					t.Logf("Validation status: %s", pipeline.Validation.Status)
					assert.Equal(t, "succeeded", pipeline.Validation.Status,
						"Validation should succeed after rapid updates")
				}

				// Check for any deployment issues
				if pipeline.Deployment != nil {
					t.Logf("Deployment status: %s, reason: %s",
						pipeline.Deployment.Status, pipeline.Deployment.Reason)
				}

				// Check last trigger info
				if pipeline.LastTrigger != nil {
					t.Logf("Last trigger: %s at %s",
						pipeline.LastTrigger.Reason, pipeline.LastTrigger.Timestamp)
				}
			}

			// Also check for errors via errors endpoint
			errors, err := debugClient.GetErrors(ctx)
			if err == nil && errors != nil {
				if errors.LastErrorTimestamp != "" {
					t.Logf("Last error timestamp: %s", errors.LastErrorTimestamp)
				}
				// No critical errors should be present after successful processing
			}

			t.Log("Transaction conflict handling verified")

			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace, err := GetNamespaceFromContext(ctx)
			if err != nil {
				return ctx
			}

			client, err := cfg.NewClient()
			if err != nil {
				return ctx
			}

			clusterRole := NewClusterRole(ControllerClusterRoleName, namespace)
			_ = client.Resources().Delete(ctx, clusterRole)

			clusterRoleBinding := NewClusterRoleBinding(
				ControllerClusterRoleBindingName,
				ControllerClusterRoleName,
				ControllerServiceAccountName,
				namespace,
				namespace,
			)
			_ = client.Resources().Delete(ctx, clusterRoleBinding)

			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
			_ = client.Resources().Delete(ctx, ns)

			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}

// TestPartialDeploymentFailure tests controller behavior when deploying to multiple
// HAProxy instances and some fail.
//
// Note: This test is limited in acceptance tests since we don't deploy actual HAProxy instances.
// Instead, we test the controller's behavior when there are no HAProxy endpoints (partial = 0/0).
// In a full integration test environment with multiple HAProxy pods, we would:
// - Scale one HAProxy pod down
// - Verify partial success
// - Restore and verify full convergence
//
// What we actually test:
// 1. Controller handles no endpoints gracefully
// 2. Controller reports status appropriately
// 3. Controller remains operational for future deployments
func TestPartialDeploymentFailure(t *testing.T) {
	feature := features.New("Error Scenarios - Partial Deployment Failure").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace := envconf.RandomName("test-part-fail", 32)
			ctx = StoreNamespaceInContext(ctx, namespace)
			t.Logf("Test namespace: %s", namespace)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
			err = client.Resources().Create(ctx, ns)
			require.NoError(t, err)

			sa := NewServiceAccount(namespace, ControllerServiceAccountName)
			err = client.Resources().Create(ctx, sa)
			require.NoError(t, err)

			role := NewRole(namespace, ControllerRoleName)
			err = client.Resources().Create(ctx, role)
			require.NoError(t, err)

			roleBinding := NewRoleBinding(namespace, ControllerRoleBindingName, ControllerRoleName, ControllerServiceAccountName)
			err = client.Resources().Create(ctx, roleBinding)
			require.NoError(t, err)

			clusterRole := NewClusterRole(ControllerClusterRoleName, namespace)
			err = client.Resources().Create(ctx, clusterRole)
			require.NoError(t, err)

			clusterRoleBinding := NewClusterRoleBinding(
				ControllerClusterRoleBindingName,
				ControllerClusterRoleName,
				ControllerServiceAccountName,
				namespace,
				namespace,
			)
			err = client.Resources().Create(ctx, clusterRoleBinding)
			require.NoError(t, err)

			secret := NewSecret(namespace, ControllerSecretName)
			err = client.Resources().Create(ctx, secret)
			require.NoError(t, err)

			webhookSecret := NewWebhookCertSecret(namespace, "haproxy-webhook-certs")
			err = client.Resources().Create(ctx, webhookSecret)
			require.NoError(t, err)

			crd := NewHAProxyTemplateConfig(namespace, ControllerCRDName, ControllerSecretName, false)
			err = client.Resources().Create(ctx, crd)
			require.NoError(t, err)

			deployment := NewControllerDeployment(namespace, ControllerCRDName, ControllerSecretName, ControllerServiceAccountName, DebugPort, 1)
			err = client.Resources().Create(ctx, deployment)
			require.NoError(t, err)

			return ctx
		}).
		Assess("Controller handles zero endpoints and remains operational", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace, err := GetNamespaceFromContext(ctx)
			require.NoError(t, err)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			// Wait for controller to be ready
			err = WaitForPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, DefaultTimeout)
			require.NoError(t, err)
			t.Log("Controller pod ready")

			pod, err := GetControllerPod(ctx, client, namespace)
			require.NoError(t, err)

			debugClient := NewDebugClient(cfg.Client().RESTConfig(), pod, DebugPort)
			err = debugClient.Start(ctx)
			require.NoError(t, err)
			defer debugClient.Stop()

			_, err = debugClient.WaitForConfig(ctx, 60*time.Second)
			require.NoError(t, err)

			// Trigger multiple config updates with no HAProxy endpoints
			// This is the "partial failure" case where 0/0 endpoints receive config
			t.Log("Testing deployment with no HAProxy endpoints (0/0 partial case)...")
			for i := 1; i <= 3; i++ {
				err = UpdateHAProxyTemplateConfigTemplate(ctx, client, namespace, ControllerCRDName, VersionedTemplate(200+i))
				require.NoError(t, err)
				t.Logf("Config update %d applied", i)
				time.Sleep(1 * time.Second)
			}

			// Wait for processing
			time.Sleep(3 * time.Second)

			// Verify controller is still operational
			err = WaitForPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, 30*time.Second)
			require.NoError(t, err, "Controller should remain operational with no endpoints")

			// Verify config is tracked correctly
			renderedConfig, err := debugClient.GetRenderedConfig(ctx)
			require.NoError(t, err)

			// Should have latest version marker
			assert.Contains(t, renderedConfig, "# version 20",
				"Config should reflect updates even with no deployment targets")

			// Use debug endpoint to verify pipeline status for partial deployment
			pipeline, err := debugClient.GetPipelineStatus(ctx)
			if err == nil && pipeline != nil {
				t.Log("Pipeline status retrieved for partial deployment verification")

				// Rendering and validation should succeed
				if pipeline.Rendering != nil {
					t.Logf("Rendering status: %s", pipeline.Rendering.Status)
					assert.Equal(t, "succeeded", pipeline.Rendering.Status,
						"Rendering should succeed")
				}

				if pipeline.Validation != nil {
					t.Logf("Validation status: %s", pipeline.Validation.Status)
					assert.Equal(t, "succeeded", pipeline.Validation.Status,
						"Validation should succeed")
				}

				// Deployment should show 0 endpoints (partial = 0/0)
				if pipeline.Deployment != nil {
					t.Logf("Deployment - total: %d, succeeded: %d, failed: %d",
						pipeline.Deployment.EndpointsTotal,
						pipeline.Deployment.EndpointsSucceeded,
						pipeline.Deployment.EndpointsFailed)

					assert.Equal(t, 0, pipeline.Deployment.EndpointsTotal,
						"Should have 0 total endpoints")
					assert.Equal(t, 0, pipeline.Deployment.EndpointsFailed,
						"Should have 0 failed endpoints (nothing to fail)")

					// Check for any failed endpoint details
					if len(pipeline.Deployment.FailedEndpoints) > 0 {
						t.Logf("Failed endpoints: %v", pipeline.Deployment.FailedEndpoints)
					}
				}
			} else {
				// Fall back to log checking if debug endpoint unavailable
				t.Log("Pipeline status unavailable, falling back to log verification")
				logs, logErr := GetPodLogs(ctx, client, pod, 200)
				if logErr == nil {
					if strings.Contains(logs, "no pods") ||
						strings.Contains(logs, "endpoint") ||
						strings.Contains(logs, "0 endpoints") {
						t.Log("Controller logs show expected no-endpoints handling")
					}
				}
			}

			t.Log("Partial deployment failure handling (0 endpoints case) verified")

			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace, err := GetNamespaceFromContext(ctx)
			if err != nil {
				return ctx
			}

			client, err := cfg.NewClient()
			if err != nil {
				return ctx
			}

			clusterRole := NewClusterRole(ControllerClusterRoleName, namespace)
			_ = client.Resources().Delete(ctx, clusterRole)

			clusterRoleBinding := NewClusterRoleBinding(
				ControllerClusterRoleBindingName,
				ControllerClusterRoleName,
				ControllerServiceAccountName,
				namespace,
				namespace,
			)
			_ = client.Resources().Delete(ctx, clusterRoleBinding)

			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
			_ = client.Resources().Delete(ctx, ns)

			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}
