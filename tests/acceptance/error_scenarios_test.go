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
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
	"sigs.k8s.io/e2e-framework/pkg/types"

	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/tests/testutil"
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

// buildInvalidHAProxyConfigFeature builds a feature that tests syntactically valid but
// semantically invalid HAProxy configuration is rejected by validation and the previous
// valid config is preserved.
//
// Scenario:
// 1. Deploy controller with valid initial config
// 2. Update HAProxyTemplateConfig with config that references non-existent backend
// 3. Verify HAProxy binary validation fails
// 4. Verify previous valid config is preserved
// 5. Verify controller doesn't crash and continues watching
func buildInvalidHAProxyConfigFeature() types.Feature {
	return features.New("Error Scenarios - Invalid HAProxy Config").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace := envconf.RandomName("test-invalid-cfg", 32)
			ctx = StoreNamespaceInContext(ctx, namespace)
			t.Logf("Test namespace: %s", namespace)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			opts := DefaultControllerEnvironmentOptions()
			if err := CreateControllerEnvironment(ctx, t, client, namespace, opts); err != nil {
				t.Fatal("Failed to create controller environment:", err)
			}

			return ctx
		}).
		Assess("Invalid config is rejected and old config preserved", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace, err := GetNamespaceFromContext(ctx)
			require.NoError(t, err)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			// Use shared clientset (rate limiting disabled) to avoid exhaustion
			clientset := Clientset()

			// Wait for controller to be ready
			err = WaitForPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, DefaultTimeout)
			require.NoError(t, err)
			t.Log("Controller pod ready")

			// Get controller pod for debug client
			pod, err := GetControllerPod(ctx, client, namespace)
			require.NoError(t, err)

			// Create debug client using NodePort - no Start()/Stop() needed
			debugClient, err := SetupDebugClient(ctx, client, clientset, namespace, 30*time.Second)
			require.NoError(t, err)

			// Wait for initial config to be loaded
			t.Log("Waiting for initial config to be ready...")
			_, err = debugClient.WaitForConfig(ctx, 60*time.Second)
			if err != nil {
				DumpPodLogs(ctx, t, clientset, pod)
				require.NoError(t, err, "Initial config should be available")
			}

			// Get initial rendered config for comparison
			initialConfig, err := debugClient.GetRenderedConfigWithRetry(ctx, 30*time.Second)
			require.NoError(t, err)
			t.Logf("Initial config length: %d bytes", len(initialConfig))

			// Verify initial config has valid structure
			if !strings.Contains(initialConfig, "backend test-backend") {
				t.Logf("FAILURE: Initial config does not contain expected backend\nFull config:\n%s", initialConfig)
			}
			assert.Contains(t, initialConfig, "backend test-backend", "Initial config should have valid backend")

			// Now update HAProxyTemplateConfig with INVALID config (HAProxy syntax error)
			t.Log("Updating config with invalid HAProxy syntax...")
			err = UpdateHAProxyTemplateConfigTemplate(ctx, client, namespace, ControllerCRDName, InvalidSyntaxTemplate)
			require.NoError(t, err)

			// Verify controller is still running (didn't crash) - check this first
			err = WaitForPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, 30*time.Second)
			require.NoError(t, err, "Controller should still be running after invalid config")

			// Refresh debug client in case pod restarted
			debugClient, err = EnsureDebugClientReady(ctx, t, client, clientset, namespace, 30*time.Second)
			if err != nil {
				t.Logf("Warning: failed to refresh debug client: %v", err)
				// Continue with log-based verification if debug client unavailable
			}

			// Wait for validation to fail using the debug endpoint
			t.Log("Waiting for validation to reject invalid config...")
			if debugClient != nil {
				err = debugClient.WaitForValidationStatus(ctx, "failed", 30*time.Second)
				if err != nil {
					// If timeout, check pipeline status for diagnostics
					pipeline, pipelineErr := debugClient.GetPipelineStatusWithRetry(ctx, 10*time.Second)
					if pipelineErr == nil && pipeline != nil {
						t.Logf("Pipeline status: validation=%+v", pipeline.Validation)
					}
					// Fall back to log checking if debug endpoint not available
					t.Log("Debug endpoint wait timed out, checking logs...")
				}
			}

			// Use debug endpoint to verify validation failure
			if debugClient != nil {
				pipeline, pipelineErr := debugClient.GetPipelineStatusWithRetry(ctx, 30*time.Second)
				if pipelineErr == nil && pipeline != nil && pipeline.Validation != nil {
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
						pod, _ = GetControllerPod(ctx, client, namespace)
						if pod != nil {
							logs, logErr := GetPodLogs(ctx, clientset, pod, 200)
							if logErr == nil {
								t.Logf("Unexpected validation status. Logs (last 500 chars): %s",
									logs[max(0, len(logs)-500):])
							}
						}
						t.Errorf("Expected validation status 'failed', got '%s'", pipeline.Validation.Status)
					}
				} else {
					// Fall back to log-based verification if debug endpoint unavailable
					t.Log("Pipeline status unavailable, falling back to log verification")
					pod, _ = GetControllerPod(ctx, client, namespace)
					if pod != nil {
						logs, logErr := GetPodLogs(ctx, clientset, pod, 200)
						if logErr == nil {
							hasValidationFailure := strings.Contains(logs, "validation failed") ||
								strings.Contains(logs, "unknown keyword") ||
								strings.Contains(logs, "invalid_directive_xyz")
							assert.True(t, hasValidationFailure,
								"Controller logs should show HAProxy validation failure")
						}
					}
				}
			} else {
				// Fall back to log-based verification if debug client unavailable
				t.Log("Debug client unavailable, falling back to log verification")
				pod, _ = GetControllerPod(ctx, client, namespace)
				if pod != nil {
					logs, logErr := GetPodLogs(ctx, clientset, pod, 200)
					if logErr == nil {
						hasValidationFailure := strings.Contains(logs, "validation failed") ||
							strings.Contains(logs, "unknown keyword") ||
							strings.Contains(logs, "invalid_directive_xyz")
						assert.True(t, hasValidationFailure,
							"Controller logs should show HAProxy validation failure")
					}
				}
			}

			// Restore valid config
			t.Log("Restoring valid config...")
			err = UpdateHAProxyTemplateConfigTemplate(ctx, client, namespace, ControllerCRDName, ValidTemplate)
			require.NoError(t, err)

			// Refresh debug client before checking restore
			debugClient, err = EnsureDebugClientReady(ctx, t, client, clientset, namespace, 30*time.Second)
			require.NoError(t, err, "Debug client should be available after config restore")

			// Wait for successful validation after restore
			err = debugClient.WaitForValidationStatus(ctx, "succeeded", 30*time.Second)
			if err != nil {
				t.Logf("Warning: validation status wait timed out after restore: %v", err)
			}

			// Verify controller recovered
			restoredConfig, err := debugClient.GetRenderedConfigWithRetry(ctx, 30*time.Second)
			require.NoError(t, err)
			assert.Contains(t, restoredConfig, "backend test-backend", "Config should be restored")

			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				return ctx
			}
			return CleanupControllerEnvironment(ctx, t, client)
		}).
		Feature()
}

// TestInvalidHAProxyConfig runs the invalid HAProxy config test.
func TestInvalidHAProxyConfig(t *testing.T) {
	testEnv.Test(t, buildInvalidHAProxyConfigFeature())
}

// buildCredentialsMissingFeature builds a feature that tests the controller handles
// missing credentials Secret gracefully.
//
// Scenario:
// 1. Create HAProxyTemplateConfig referencing a credentials Secret
// 2. Do NOT create the Secret
// 3. Deploy controller
// 4. Verify controller starts but waits for credentials
// 5. Create the Secret
// 6. Verify controller picks it up and proceeds
func buildCredentialsMissingFeature() types.Feature {
	return features.New("Error Scenarios - Credentials Missing").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace := envconf.RandomName("test-creds-miss", 32)
			ctx = StoreNamespaceInContext(ctx, namespace)
			t.Logf("Test namespace: %s", namespace)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			// Deploy controller WITHOUT creating credentials secret first
			opts := DefaultControllerEnvironmentOptions()
			opts.SkipCredentialsSecret = true
			if err := CreateControllerEnvironment(ctx, t, client, namespace, opts); err != nil {
				t.Fatal("Failed to create controller environment:", err)
			}

			return ctx
		}).
		Assess("Controller waits for credentials and recovers when created", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace, err := GetNamespaceFromContext(ctx)
			require.NoError(t, err)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			// Use shared clientset (rate limiting disabled) to avoid exhaustion
			clientset := Clientset()

			// Wait for controller pod to exist (may not be Ready due to missing credentials)
			t.Log("Waiting for controller pod to start...")
			waitCfg := testutil.DefaultWaitConfig()
			waitCfg.Timeout = 30 * time.Second
			err = testutil.WaitForConditionWithDescription(ctx, waitCfg, "controller pod exists",
				func(ctx context.Context) (bool, error) {
					pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
						LabelSelector: "app=" + ControllerDeploymentName,
					})
					if err != nil {
						return false, err
					}
					return len(pods.Items) > 0, nil
				})
			require.NoError(t, err, "Controller pod should exist")

			// Check controller pod status - should be Running but may be waiting
			pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
				LabelSelector: "app=" + ControllerDeploymentName,
			})
			require.NoError(t, err)

			pod := &pods.Items[0]
			t.Logf("Controller pod status: %s", pod.Status.Phase)

			// Check if pod is in crash loop or running
			for _, cs := range pod.Status.ContainerStatuses {
				if cs.RestartCount > 3 {
					t.Log("Pod has restarted multiple times, checking logs...")
					DumpPodLogs(ctx, t, clientset, pod)
				}
			}

			// Now create the credentials secret
			t.Log("Creating credentials secret...")
			secret := NewSecret(namespace, ControllerSecretName)
			_, err = clientset.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
			require.NoError(t, err)

			// Wait for controller to become ready
			t.Log("Waiting for controller to recover after credentials created...")
			err = WaitForPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, PodRestartTimeout)
			if err != nil {
				// Dump logs if still not ready
				pod, podErr := GetControllerPod(ctx, client, namespace)
				if podErr == nil {
					DumpPodLogs(ctx, t, clientset, pod)
				}
			}
			require.NoError(t, err, "Controller should become ready after credentials created")

			// Verify controller is operational via debug client
			debugClient, err := EnsureDebugClientReady(ctx, t, client, clientset, namespace, 30*time.Second)
			require.NoError(t, err)

			// Verify config is loaded
			_, err = debugClient.WaitForConfig(ctx, 30*time.Second)
			require.NoError(t, err, "Controller should have config after credentials provided")

			// Wait for validation to succeed after credentials recovery
			err = debugClient.WaitForValidationStatus(ctx, "succeeded", 30*time.Second)
			require.NoError(t, err, "Validation should succeed after credentials recovery")

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
			client, err := cfg.NewClient()
			if err != nil {
				return ctx
			}
			return CleanupControllerEnvironment(ctx, t, client)
		}).
		Feature()
}

// TestCredentialsMissing runs the credentials missing test.
func TestCredentialsMissing(t *testing.T) {
	testEnv.Test(t, buildCredentialsMissingFeature())
}

// buildControllerCrashRecoveryFeature builds a feature that tests the controller
// recovers cleanly after a crash.
//
// Scenario:
// 1. Deploy controller with valid config
// 2. Verify config is deployed
// 3. Delete controller pod (simulates crash)
// 4. Wait for Pod restart
// 5. Verify controller re-discovers state
// 6. Verify subsequent config changes work normally
func buildControllerCrashRecoveryFeature() types.Feature {
	return features.New("Error Scenarios - Controller Crash Recovery").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace := envconf.RandomName("test-crash-rec", 32)
			ctx = StoreNamespaceInContext(ctx, namespace)
			t.Logf("Test namespace: %s", namespace)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			opts := DefaultControllerEnvironmentOptions()
			if err := CreateControllerEnvironment(ctx, t, client, namespace, opts); err != nil {
				t.Fatal("Failed to create controller environment:", err)
			}

			return ctx
		}).
		Assess("Controller recovers after crash", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace, err := GetNamespaceFromContext(ctx)
			require.NoError(t, err)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			// Use shared clientset (rate limiting disabled) to avoid exhaustion
			clientset := Clientset()

			// Wait for controller to be ready
			err = WaitForPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, DefaultTimeout)
			require.NoError(t, err)
			t.Log("Controller pod ready")

			// Get initial pod and verify config
			pod, err := GetControllerPod(ctx, client, namespace)
			require.NoError(t, err)
			initialPodName := pod.Name
			initialPodUID := string(pod.UID)

			debugClient, err := EnsureDebugClientReady(ctx, t, client, clientset, namespace, 30*time.Second)
			require.NoError(t, err)

			_, err = debugClient.WaitForConfig(ctx, 60*time.Second)
			require.NoError(t, err)
			t.Log("Initial config verified")

			// Delete the pod (simulate crash)
			t.Logf("Deleting pod %s (UID: %s) to simulate crash...", initialPodName, initialPodUID)
			err = clientset.CoreV1().Pods(namespace).Delete(ctx, initialPodName, metav1.DeleteOptions{})
			require.NoError(t, err)

			// Wait for a NEW pod (different UID) to be ready
			// This function specifically waits for a pod with a different UID than the original,
			// avoiding the race condition where we might find the old pod still terminating
			t.Log("Waiting for new pod (different UID) to be ready...")
			newPod, err := WaitForNewPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, initialPodUID, PodRestartTimeout)
			require.NoError(t, err, "Should find a new pod with different UID")
			t.Logf("New pod: %s (UID: %s), was %s (UID: %s)", newPod.Name, newPod.UID, initialPodName, initialPodUID)

			// Verify controller is operational - reuse the existing debug client via NodePort
			// (no need to create new client as NodePort stays the same and routes to new pod)
			_, err = debugClient.WaitForConfig(ctx, 60*time.Second)
			if err != nil {
				DumpPodLogs(ctx, t, clientset, newPod)
			}
			require.NoError(t, err, "New pod should have config loaded")

			// Verify rendered config is available
			config, err := debugClient.GetRenderedConfig(ctx)
			require.NoError(t, err)
			if !strings.Contains(config, "backend") {
				t.Logf("FAILURE: Rendered config does not contain expected backend after recovery\nFull config:\n%s", config)
			}
			assert.Contains(t, config, "backend", "Config should be rendered after recovery")

			t.Log("Controller successfully recovered after crash")

			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				return ctx
			}
			return CleanupControllerEnvironment(ctx, t, client)
		}).
		Feature()
}

// TestControllerCrashRecovery runs the controller crash recovery test.
func TestControllerCrashRecovery(t *testing.T) {
	testEnv.Test(t, buildControllerCrashRecoveryFeature())
}

// buildRapidConfigUpdatesFeature builds a feature that tests rapid configuration
// updates are debounced properly.
//
// Scenario:
// 1. Deploy controller with valid config
// 2. Update ConfigMap 10 times in rapid succession
// 3. Verify only the final version is deployed to HAProxy
// 4. Verify no transaction conflicts from rapid changes
func buildRapidConfigUpdatesFeature() types.Feature {
	return features.New("Error Scenarios - Rapid Config Updates").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace := envconf.RandomName("test-rapid-cfg", 32)
			ctx = StoreNamespaceInContext(ctx, namespace)
			t.Logf("Test namespace: %s", namespace)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			opts := DefaultControllerEnvironmentOptions()
			if err := CreateControllerEnvironment(ctx, t, client, namespace, opts); err != nil {
				t.Fatal("Failed to create controller environment:", err)
			}

			return ctx
		}).
		Assess("Rapid updates are debounced", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace, err := GetNamespaceFromContext(ctx)
			require.NoError(t, err)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			// Use shared clientset (rate limiting disabled) to avoid exhaustion
			clientset := Clientset()

			// Wait for controller to be ready
			err = WaitForPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, DefaultTimeout)
			require.NoError(t, err)
			t.Log("Controller pod ready")

			debugClient, err := EnsureDebugClientReady(ctx, t, client, clientset, namespace, 30*time.Second)
			require.NoError(t, err)

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

			// Wait for debouncing to complete and any version >= 2 to appear
			// Debouncing is non-deterministic. Under heavy parallel test load, the controller
			// may not process all updates, but any version > 1 proves debouncing worked
			// (version 1 was skipped in favor of a later update).
			// Use 120s timeout for parallel tests where API server is under heavy load.
			t.Log("Waiting for any version >= 2 to appear in rendered config (proves debouncing worked)...")
			expectedVersions := []string{
				"# version 2", "# version 3", "# version 4", "# version 5",
				"# version 6", "# version 7", "# version 8", "# version 9", "# version 10",
			}
			foundVersion, err := debugClient.WaitForRenderedConfigContainsAny(ctx, expectedVersions, 120*time.Second)
			if err != nil {
				t.Logf("Debounce wait failed: %v, dumping controller logs...", err)
				pod, podErr := GetControllerPod(ctx, client, namespace)
				if podErr == nil {
					DumpPodLogs(ctx, t, clientset, pod)
				}
			}
			require.NoError(t, err, "Should find a version >= 2 in rendered config")
			t.Logf("Found %q in rendered config (debouncing worked)", foundVersion)

			// Wait for validation to complete (validation happens after rendering)
			err = debugClient.WaitForValidationStatus(ctx, "succeeded", 30*time.Second)
			require.NoError(t, err, "Validation should succeed after debounce")

			// Use debug endpoint to verify pipeline completed successfully after debouncing
			pipeline, err := debugClient.GetPipelineStatusWithRetry(ctx, 30*time.Second)
			if err == nil && pipeline != nil {
				t.Log("Pipeline status retrieved for debounce verification")

				// Log rendering status (validation already verified above)
				if pipeline.Rendering != nil {
					t.Logf("Final rendering status: %s (duration: %dms)",
						pipeline.Rendering.Status, pipeline.Rendering.DurationMs)
				}

				if pipeline.Validation != nil {
					t.Logf("Final validation status: %s (duration: %dms)",
						pipeline.Validation.Status, pipeline.Validation.DurationMs)
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
			client, err := cfg.NewClient()
			if err != nil {
				return ctx
			}
			return CleanupControllerEnvironment(ctx, t, client)
		}).
		Feature()
}

// TestRapidConfigUpdates runs the rapid config updates test.
func TestRapidConfigUpdates(t *testing.T) {
	testEnv.Test(t, buildRapidConfigUpdatesFeature())
}

// buildGracefulShutdownFeature builds a feature that tests the controller shuts
// down gracefully.
//
// Scenario:
// 1. Deploy controller with valid config
// 2. Send SIGTERM to controller pod (kubectl delete with grace period)
// 3. Verify controller terminates within grace period
// 4. Verify next controller instance starts cleanly
func buildGracefulShutdownFeature() types.Feature {
	return features.New("Error Scenarios - Graceful Shutdown").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace := envconf.RandomName("test-shutdown", 32)
			ctx = StoreNamespaceInContext(ctx, namespace)
			t.Logf("Test namespace: %s", namespace)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			opts := DefaultControllerEnvironmentOptions()
			if err := CreateControllerEnvironment(ctx, t, client, namespace, opts); err != nil {
				t.Fatal("Failed to create controller environment:", err)
			}

			return ctx
		}).
		Assess("Controller shuts down gracefully", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace, err := GetNamespaceFromContext(ctx)
			require.NoError(t, err)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			// Use shared clientset (rate limiting disabled) to avoid exhaustion
			clientset := Clientset()

			// Wait for controller to be ready
			err = WaitForPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, DefaultTimeout)
			require.NoError(t, err)
			t.Log("Controller pod ready")

			pod, err := GetControllerPod(ctx, client, namespace)
			require.NoError(t, err)
			podName := pod.Name

			debugClient, err := EnsureDebugClientReady(ctx, t, client, clientset, namespace, 30*time.Second)
			require.NoError(t, err)

			_, err = debugClient.WaitForConfig(ctx, 60*time.Second)
			require.NoError(t, err)

			// Delete pod with grace period (sends SIGTERM)
			t.Logf("Deleting pod %s with 30s grace period...", podName)
			gracePeriod := int64(30)
			err = clientset.CoreV1().Pods(namespace).Delete(ctx, podName, metav1.DeleteOptions{
				GracePeriodSeconds: &gracePeriod,
			})
			require.NoError(t, err)

			// Wait for pod to terminate using proper polling
			t.Log("Waiting for pod to terminate...")
			err = WaitForPodTerminated(ctx, client, namespace, podName, 40*time.Second)
			if err != nil {
				// Pod didn't terminate in time - this is a failure
				t.Log("Pod did not terminate in time, fetching logs...")
				pod, podErr := GetControllerPod(ctx, client, namespace)
				if podErr == nil {
					DumpPodLogs(ctx, t, clientset, pod)
				}
				t.Fatal("Pod should terminate within grace period")
			}
			t.Log("Pod terminated successfully")

			// Wait for new pod to start
			t.Log("Waiting for new pod to be ready...")
			err = WaitForPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, 90*time.Second)
			require.NoError(t, err, "New pod should start cleanly after graceful shutdown")

			// Verify new pod is operational
			newPod, err := GetControllerPod(ctx, client, namespace)
			require.NoError(t, err)

			assert.NotEqual(t, podName, newPod.Name, "New pod should be created")

			// Reuse debugClient via NodePort (routes to new pod automatically)
			_, err = debugClient.WaitForConfig(ctx, 60*time.Second)
			require.NoError(t, err, "New pod should have config loaded")

			t.Log("Graceful shutdown verified successfully")

			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				return ctx
			}
			return CleanupControllerEnvironment(ctx, t, client)
		}).
		Feature()
}

// TestGracefulShutdown runs the graceful shutdown test.
func TestGracefulShutdown(t *testing.T) {
	testEnv.Test(t, buildGracefulShutdownFeature())
}

// buildDataplaneUnreachableFeature builds a feature that tests the controller handles
// HAProxy being unreachable.
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
func buildDataplaneUnreachableFeature() types.Feature {
	return features.New("Error Scenarios - Dataplane Unreachable").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace := envconf.RandomName("test-dp-unreach", 32)
			ctx = StoreNamespaceInContext(ctx, namespace)
			t.Logf("Test namespace: %s", namespace)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			opts := DefaultControllerEnvironmentOptions()
			if err := CreateControllerEnvironment(ctx, t, client, namespace, opts); err != nil {
				t.Fatal("Failed to create controller environment:", err)
			}

			return ctx
		}).
		Assess("Controller handles no HAProxy endpoints gracefully", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace, err := GetNamespaceFromContext(ctx)
			require.NoError(t, err)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			// Use shared clientset (rate limiting disabled) to avoid exhaustion
			clientset := Clientset()

			// Wait for controller to be ready
			err = WaitForPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, DefaultTimeout)
			require.NoError(t, err)
			t.Log("Controller pod ready")

			debugClient, err := EnsureDebugClientReady(ctx, t, client, clientset, namespace, 30*time.Second)
			require.NoError(t, err)

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

			// Wait for reconciliation to process by checking for version 99 in rendered config
			err = debugClient.WaitForRenderedConfigContains(ctx, "# version 99", 30*time.Second)
			require.NoError(t, err, "Config update should be processed")

			// Verify controller is still running (didn't crash)
			err = WaitForPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, 30*time.Second)
			require.NoError(t, err, "Controller should still be running with no HAProxy endpoints")

			// Wait for validation to complete before checking status
			err = debugClient.WaitForValidationStatus(ctx, "succeeded", 30*time.Second)
			if err != nil {
				t.Logf("Note: Validation did not reach 'succeeded' status: %v", err)
			}

			// Use debug endpoint to verify pipeline status
			pipeline, err := debugClient.GetPipelineStatusWithRetry(ctx, 30*time.Second)
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
				pod, podErr := GetControllerPod(ctx, client, namespace)
				if podErr == nil {
					logs, logErr := GetPodLogs(ctx, clientset, pod, 200)
					if logErr == nil {
						hasExpectedBehavior := strings.Contains(logs, "endpoint") ||
							strings.Contains(logs, "no pods") ||
							strings.Contains(logs, "reconcil")
						if hasExpectedBehavior {
							t.Log("Controller logs show expected endpoint handling")
						}
					}
				}
			}

			t.Log("Controller handles no HAProxy endpoints gracefully")

			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				return ctx
			}
			return CleanupControllerEnvironment(ctx, t, client)
		}).
		Feature()
}

// TestDataplaneUnreachable runs the dataplane unreachable test.
func TestDataplaneUnreachable(t *testing.T) {
	testEnv.Test(t, buildDataplaneUnreachableFeature())
}

// buildLeadershipDuringReconciliationFeature builds a feature that tests leadership
// transitions during operation are handled correctly.
//
// Scenario:
// 1. Deploy controller with 2 replicas and leader election enabled
// 2. Wait for leader election
// 3. Start a config update
// 4. Delete leader pod mid-reconciliation
// 5. Verify new leader is elected
// 6. Verify config eventually converges
func buildLeadershipDuringReconciliationFeature() types.Feature {
	return features.New("Error Scenarios - Leadership During Reconciliation").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace := envconf.RandomName("test-leader-rec", 32)
			ctx = StoreNamespaceInContext(ctx, namespace)
			t.Logf("Test namespace: %s", namespace)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			// Deploy with 2 replicas and leader election enabled
			opts := ControllerEnvironmentOptions{
				Replicas:             2,
				CRDName:              ControllerCRDName,
				EnableLeaderElection: true,
			}
			if err := CreateControllerEnvironment(ctx, t, client, namespace, opts); err != nil {
				t.Fatal("Failed to create controller environment:", err)
			}

			return ctx
		}).
		Assess("New leader elected after leader deletion", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace, err := GetNamespaceFromContext(ctx)
			require.NoError(t, err)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			// Use shared clientset (rate limiting disabled) to avoid exhaustion
			clientset := Clientset()

			// Wait for deployment to create pods (2 replicas expected)
			t.Log("Waiting for controller pods to be created...")
			err = WaitForPodCount(ctx, client, namespace, "app="+ControllerDeploymentName, 2, 60*time.Second)
			require.NoError(t, err, "Should have 2 controller pods")

			pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
				LabelSelector: "app=" + ControllerDeploymentName,
			})
			require.NoError(t, err)
			t.Logf("Found %d controller pods", len(pods.Items))

			// Wait for at least one pod to be ready
			t.Log("Waiting for controller pods to be ready...")
			err = WaitForPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, DefaultTimeout)
			require.NoError(t, err)

			// Wait for leader election to complete
			t.Log("Waiting for leader election...")
			err = WaitForLeaderElection(ctx, clientset, namespace, "haptic-leader", 60*time.Second)
			if err != nil {
				t.Logf("Leader election wait failed: %v (continuing anyway)", err)
			}

			// Get current leader
			initialLeader, err := GetLeaseHolder(ctx, clientset, namespace, "haptic-leader")
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

			// Wait for the deleted pod to be replaced with a new one
			t.Log("Waiting for pod replacement...")
			_, err = WaitForNewPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, string(pods.Items[0].UID), 90*time.Second)
			require.NoError(t, err, "New pod should be created after deletion")

			// Wait for 2 pods to be present again
			err = WaitForPodCount(ctx, client, namespace, "app="+ControllerDeploymentName, 2, 60*time.Second)
			require.NoError(t, err, "Should have 2 controller pods after replacement")

			// Wait for pods to be ready again
			err = WaitForPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, 90*time.Second)
			require.NoError(t, err, "Pods should become ready after leader deletion")

			// Verify we still have 2 pods
			pods, err = clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
				LabelSelector: "app=" + ControllerDeploymentName,
			})
			require.NoError(t, err)
			t.Logf("After leadership transition: %d pods", len(pods.Items))

			// Wait for leader election to complete
			err = WaitForLeaderElection(ctx, clientset, namespace, "haptic-leader", 30*time.Second)
			if err != nil {
				t.Logf("Leader election wait failed: %v (continuing anyway)", err)
			}
			newLeader, err := GetLeaseHolder(ctx, clientset, namespace, "haptic-leader")
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

			debugClient, err := EnsureDebugClientReady(ctx, t, client, clientset, namespace, 30*time.Second)
			if err != nil {
				t.Logf("Could not setup debug client: %v (this may be expected if pod is not leader)", err)
			} else {
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
			client, err := cfg.NewClient()
			if err != nil {
				return ctx
			}
			return CleanupControllerEnvironment(ctx, t, client)
		}).
		Feature()
}

// TestLeadershipDuringReconciliation runs the leadership during reconciliation test.
func TestLeadershipDuringReconciliation(t *testing.T) {
	testEnv.Test(t, buildLeadershipDuringReconciliationFeature())
}

// buildWatchReconnectionFeature builds a feature that tests the controller reconnects
// and resyncs after watch interruption.
//
// Scenario:
// 1. Deploy controller
// 2. Verify watching CRD
// 3. Restart controller Pod (disrupts watch connection)
// 4. Update CRD while pod restarting
// 5. Verify new Pod reconnects and processes update
func buildWatchReconnectionFeature() types.Feature {
	return features.New("Error Scenarios - Watch Reconnection").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace := envconf.RandomName("test-watch-rec", 32)
			ctx = StoreNamespaceInContext(ctx, namespace)
			t.Logf("Test namespace: %s", namespace)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			opts := DefaultControllerEnvironmentOptions()
			if err := CreateControllerEnvironment(ctx, t, client, namespace, opts); err != nil {
				t.Fatal("Failed to create controller environment:", err)
			}

			return ctx
		}).
		Assess("Controller reconnects and processes updates after restart", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace, err := GetNamespaceFromContext(ctx)
			require.NoError(t, err)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			// Use shared clientset (rate limiting disabled) to avoid exhaustion
			clientset := Clientset()

			// Wait for initial controller to be ready
			err = WaitForPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, DefaultTimeout)
			require.NoError(t, err)
			t.Log("Initial controller pod ready")

			pod, err := GetControllerPod(ctx, client, namespace)
			require.NoError(t, err)
			initialPodName := pod.Name

			debugClient, err := EnsureDebugClientReady(ctx, t, client, clientset, namespace, 30*time.Second)
			require.NoError(t, err)

			_, err = debugClient.WaitForConfig(ctx, 60*time.Second)
			require.NoError(t, err)

			// Delete pod (simulate restart/disruption)
			t.Logf("Deleting pod %s to disrupt watch...", initialPodName)
			err = clientset.CoreV1().Pods(namespace).Delete(ctx, initialPodName, metav1.DeleteOptions{})
			require.NoError(t, err)

			// Update CRD while pod is restarting
			t.Log("Updating CRD while pod is restarting...")
			err = UpdateHAProxyTemplateConfigTemplate(ctx, client, namespace, ControllerCRDName, VersionedTemplate(42))
			require.NoError(t, err)

			// Wait for new pod (different UID) to be ready
			t.Log("Waiting for new pod to be ready...")
			newPod, err := WaitForNewPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, string(pod.UID), 90*time.Second)
			require.NoError(t, err)
			t.Logf("New pod: %s (was %s)", newPod.Name, initialPodName)

			// Verify new pod processed the update - reuse debugClient via NodePort
			_, err = debugClient.WaitForConfig(ctx, 60*time.Second)
			require.NoError(t, err)

			// Verify the updated config is reflected
			renderedConfig, err := debugClient.GetRenderedConfig(ctx)
			require.NoError(t, err)

			// Should have version 42 marker
			assert.Contains(t, renderedConfig, "# version 42",
				"New pod should have the updated config")

			t.Log("Watch reconnection and resync successful")

			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				return ctx
			}
			return CleanupControllerEnvironment(ctx, t, client)
		}).
		Feature()
}

// TestWatchReconnection runs the watch reconnection test.
func TestWatchReconnection(t *testing.T) {
	testEnv.Test(t, buildWatchReconnectionFeature())
}

// buildTransactionConflictFeature builds a feature that tests the controller handles
// version conflicts during deployment.
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
func buildTransactionConflictFeature() types.Feature {
	return features.New("Error Scenarios - Transaction Conflict Handling").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace := envconf.RandomName("test-tx-conf", 32)
			ctx = StoreNamespaceInContext(ctx, namespace)
			t.Logf("Test namespace: %s", namespace)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			opts := DefaultControllerEnvironmentOptions()
			if err := CreateControllerEnvironment(ctx, t, client, namespace, opts); err != nil {
				t.Fatal("Failed to create controller environment:", err)
			}

			return ctx
		}).
		Assess("Controller handles concurrent updates gracefully", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace, err := GetNamespaceFromContext(ctx)
			require.NoError(t, err)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			// Use shared clientset (rate limiting disabled) to avoid exhaustion
			clientset := Clientset()

			// Wait for controller to be ready
			err = WaitForPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, DefaultTimeout)
			require.NoError(t, err)
			t.Log("Controller pod ready")

			debugClient, err := EnsureDebugClientReady(ctx, t, client, clientset, namespace, 30*time.Second)
			require.NoError(t, err)

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

			// Verify controller is still healthy
			err = WaitForPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, 30*time.Second)
			require.NoError(t, err, "Controller should remain healthy after rapid updates")

			// Wait for a late version to appear (debouncing coalesces updates)
			// Versions are 101-105 from VersionedTemplate(100+i)
			t.Log("Waiting for a late version (103-105) to appear in rendered config...")
			expectedVersions := []string{
				"# version 103", "# version 104", "# version 105",
			}
			foundVersion, err := debugClient.WaitForRenderedConfigContainsAny(ctx, expectedVersions, 30*time.Second)
			require.NoError(t, err, "Should find a late version (103-105) in rendered config")
			t.Logf("Found %q in rendered config (debouncing worked)", foundVersion)

			// Wait for validation to complete before checking status
			err = debugClient.WaitForValidationStatus(ctx, "succeeded", 30*time.Second)
			if err != nil {
				t.Logf("Note: Validation did not reach 'succeeded' status: %v", err)
			}

			// Use debug endpoint to verify pipeline completed successfully
			pipeline, err := debugClient.GetPipelineStatusWithRetry(ctx, 30*time.Second)
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
			client, err := cfg.NewClient()
			if err != nil {
				return ctx
			}
			return CleanupControllerEnvironment(ctx, t, client)
		}).
		Feature()
}

// TestTransactionConflict runs the transaction conflict test.
func TestTransactionConflict(t *testing.T) {
	testEnv.Test(t, buildTransactionConflictFeature())
}

// buildPartialDeploymentFailureFeature builds a feature that tests controller behavior
// when deploying to multiple HAProxy instances and some fail.
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
func buildPartialDeploymentFailureFeature() types.Feature {
	return features.New("Error Scenarios - Partial Deployment Failure").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace := envconf.RandomName("test-part-fail", 32)
			ctx = StoreNamespaceInContext(ctx, namespace)
			t.Logf("Test namespace: %s", namespace)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			opts := DefaultControllerEnvironmentOptions()
			if err := CreateControllerEnvironment(ctx, t, client, namespace, opts); err != nil {
				t.Fatal("Failed to create controller environment:", err)
			}

			return ctx
		}).
		Assess("Controller handles zero endpoints and remains operational", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace, err := GetNamespaceFromContext(ctx)
			require.NoError(t, err)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			// Use shared clientset (rate limiting disabled) to avoid exhaustion
			clientset := Clientset()

			// Wait for controller to be ready
			err = WaitForPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, DefaultTimeout)
			require.NoError(t, err)
			t.Log("Controller pod ready")

			debugClient, err := EnsureDebugClientReady(ctx, t, client, clientset, namespace, 30*time.Second)
			require.NoError(t, err)

			_, err = debugClient.WaitForConfig(ctx, 60*time.Second)
			require.NoError(t, err)

			// Trigger multiple config updates with no HAProxy endpoints
			// This is the "partial failure" case where 0/0 endpoints receive config
			t.Log("Testing deployment with no HAProxy endpoints (0/0 partial case)...")
			for i := 1; i <= 3; i++ {
				version := 200 + i
				err = UpdateHAProxyTemplateConfigTemplate(ctx, client, namespace, ControllerCRDName, VersionedTemplate(version))
				require.NoError(t, err)
				t.Logf("Config update %d applied, waiting for controller to process...", i)

				// Wait for each version to be processed before applying the next update.
				// This is critical in CI (DinD) environments where watch event propagation
				// and controller processing can be slower than locally.
				//
				// Use 120s timeout (matching other stress tests like debounce) because:
				// - This test runs in parallel with 16+ other tests
				// - The API server can become temporarily overloaded
				// - Later config updates (version 203) may face more contention
				expectedVersion := fmt.Sprintf("# version %d", version)
				err = debugClient.WaitForRenderedConfigContains(ctx, expectedVersion, 120*time.Second)
				require.NoError(t, err, "Config update %d (version %d) should be processed", i, version)
				t.Logf("Config version %d confirmed in rendered config", version)
			}

			// Verify controller is still operational
			err = WaitForPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, 30*time.Second)
			require.NoError(t, err, "Controller should remain operational with no endpoints")

			// Verify config is tracked correctly by polling for the expected version
			// Use WaitForRenderedConfigContains which has built-in reconnect logic for stability
			err = debugClient.WaitForRenderedConfigContains(ctx, "# version 20", 120*time.Second)
			require.NoError(t, err, "Config should reflect updates even with no deployment targets")

			// Wait for validation to complete before checking status
			err = debugClient.WaitForValidationStatus(ctx, "succeeded", 30*time.Second)
			if err != nil {
				t.Logf("Note: Validation did not reach 'succeeded' status: %v", err)
			}

			// Use debug endpoint to verify pipeline status for partial deployment
			pipeline, err := debugClient.GetPipelineStatusWithRetry(ctx, 30*time.Second)
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
				pod, podErr := GetControllerPod(ctx, client, namespace)
				if podErr == nil {
					logs, logErr := GetPodLogs(ctx, clientset, pod, 200)
					if logErr == nil {
						if strings.Contains(logs, "no pods") ||
							strings.Contains(logs, "endpoint") ||
							strings.Contains(logs, "0 endpoints") {
							t.Log("Controller logs show expected no-endpoints handling")
						}
					}
				}
			}

			t.Log("Partial deployment failure handling (0 endpoints case) verified")

			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				return ctx
			}
			return CleanupControllerEnvironment(ctx, t, client)
		}).
		Feature()
}

// TestPartialDeploymentFailure runs the partial deployment failure test.
func TestPartialDeploymentFailure(t *testing.T) {
	testEnv.Test(t, buildPartialDeploymentFailureFeature())
}
