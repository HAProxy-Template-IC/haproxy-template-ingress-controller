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
	"sigs.k8s.io/e2e-framework/pkg/types"
)

// buildHTTPStoreValidUpdateFeature builds a feature that tests valid HTTP content
// updates are applied correctly.
// Steps:
// 1. Deploy blocklist server with valid IP content
// 2. Deploy controller with HTTP Store template
// 3. Verify initial blocklist content is fetched and used
// 4. Update blocklist content with new valid IPs
// 5. Wait for refresh interval
// 6. Verify HAProxy config updated with new IPs
func buildHTTPStoreValidUpdateFeature() types.Feature {
	return features.New("HTTP Store - Valid Update").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace := envconf.RandomName("test-http-valid", 32)
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

			// Create webhook certificate secret (required for controller startup)
			webhookSecret := NewWebhookCertSecret(namespace, "haproxy-webhook-certs")
			err = client.Resources().Create(ctx, webhookSecret)
			require.NoError(t, err)

			// Create blocklist server resources
			blocklistCM := NewBlocklistContentConfigMap(namespace, ValidBlocklistContent)
			err = client.Resources().Create(ctx, blocklistCM)
			require.NoError(t, err)

			blocklistDeploy := NewBlocklistServerDeployment(namespace)
			err = client.Resources().Create(ctx, blocklistDeploy)
			require.NoError(t, err)

			blocklistSvc := NewBlocklistService(namespace)
			err = client.Resources().Create(ctx, blocklistSvc)
			require.NoError(t, err)

			// Wait for blocklist server to be ready
			err = WaitForPodReady(ctx, client, namespace, "app="+BlocklistServerName, DefaultTimeout)
			require.NoError(t, err)
			t.Log("Blocklist server ready")

			// Create HAProxyTemplateConfig with HTTP Store
			crd := NewHTTPStoreHAProxyTemplateConfig(namespace, ControllerCRDName, ControllerSecretName, false)
			err = client.Resources().Create(ctx, crd)
			require.NoError(t, err)

			// Deploy controller
			deployment := NewControllerDeployment(namespace, ControllerCRDName, ControllerSecretName, ControllerServiceAccountName, DebugPort, 1)
			err = client.Resources().Create(ctx, deployment)
			require.NoError(t, err)

			return ctx
		}).
		Assess("Valid HTTP update is applied", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
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

			// Wait for config to be available first (controller needs to start up)
			t.Log("Waiting for controller config to be ready...")
			_, err = debugClient.WaitForConfig(ctx, 60*time.Second)
			if err != nil {
				t.Logf("Config not ready, dumping pod logs...")
				DumpPodLogs(ctx, t, cfg.Client().RESTConfig(), pod)
				require.NoError(t, err, "Controller config should be available")
			}

			// Wait for initial config to be loaded and auxiliary files to contain the initial blocklist
			t.Log("Waiting for initial blocklist content...")
			err = debugClient.WaitForAuxFileContains(ctx, "blocked-ips.acl", "192.168.1.0/24", 60*time.Second)
			if err != nil {
				t.Logf("Blocklist not found, dumping pod logs...")
				DumpPodLogs(ctx, t, cfg.Client().RESTConfig(), pod)
			}
			require.NoError(t, err)
			t.Log("Initial blocklist content verified")

			// Verify the content also contains the other IP
			content, err := debugClient.GetGeneralFileContent(ctx, "blocked-ips.acl")
			require.NoError(t, err)
			if !strings.Contains(content, "10.0.0.0/8") {
				t.Logf("FAILURE: Initial blocklist content missing expected IP\nActual content:\n%s", content)
			}
			assert.Contains(t, content, "10.0.0.0/8", "Initial content should contain both IPs")

			// Update the blocklist content with new valid IPs
			t.Log("Updating blocklist content with new valid IPs...")
			clientset, err := kubernetes.NewForConfig(cfg.Client().RESTConfig())
			require.NoError(t, err)

			blocklistCM, err := clientset.CoreV1().ConfigMaps(namespace).Get(ctx, BlocklistContentConfigMapName, metav1.GetOptions{})
			require.NoError(t, err)

			blocklistCM.Data["blocklist.txt"] = UpdatedBlocklistContent
			_, err = clientset.CoreV1().ConfigMaps(namespace).Update(ctx, blocklistCM, metav1.UpdateOptions{})
			require.NoError(t, err)

			// Delete the blocklist server pod to trigger immediate reconnection and re-fetch
			// (alternatively, wait for the refresh interval, but deleting is faster)
			pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
				LabelSelector: "app=" + BlocklistServerName,
			})
			require.NoError(t, err)
			require.NotEmpty(t, pods.Items, "Blocklist server pod should exist")

			err = clientset.CoreV1().Pods(namespace).Delete(ctx, pods.Items[0].Name, metav1.DeleteOptions{})
			require.NoError(t, err)

			// Wait for new blocklist server pod to be ready
			err = WaitForPodReady(ctx, client, namespace, "app="+BlocklistServerName, 60*time.Second)
			require.NoError(t, err)
			t.Log("Blocklist server restarted with new content")

			// Wait for the updated content to appear in auxiliary files
			t.Log("Waiting for updated blocklist content...")
			err = debugClient.WaitForAuxFileContains(ctx, "blocked-ips.acl", "172.16.0.0/12", 60*time.Second)
			if err != nil {
				t.Logf("Updated content wait failed, dumping controller logs...")
				DumpPodLogs(ctx, t, cfg.Client().RESTConfig(), pod)
			}
			require.NoError(t, err)
			t.Log("Updated blocklist content verified")

			// Verify the new content contains both new IPs
			content, err = debugClient.GetGeneralFileContent(ctx, "blocked-ips.acl")
			require.NoError(t, err)
			if !strings.Contains(content, "192.168.0.0/16") {
				t.Logf("FAILURE: Updated blocklist content missing expected IP\nActual content:\n%s", content)
			}
			assert.Contains(t, content, "192.168.0.0/16", "Updated content should contain both new IPs")

			// Verify old content is gone
			assert.NotContains(t, content, "192.168.1.0/24", "Old content should be replaced")
			assert.NotContains(t, content, "10.0.0.0/8", "Old content should be replaced")

			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace, err := GetNamespaceFromContext(ctx)
			if err != nil {
				t.Logf("Failed to get namespace from context: %v", err)
				return ctx
			}

			client, err := cfg.NewClient()
			if err != nil {
				t.Logf("Failed to create client for cleanup: %v", err)
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

			// Delete namespace (which deletes all namespace-scoped resources)
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
			_ = client.Resources().Delete(ctx, ns)

			return ctx
		}).
		Feature()
}

// TestHTTPStoreValidUpdate runs the valid HTTP Store update test.
func TestHTTPStoreValidUpdate(t *testing.T) {
	testEnv.Test(t, buildHTTPStoreValidUpdateFeature())
}

// buildHTTPStoreInvalidUpdateFeature builds a feature that tests invalid HTTP content
// is rejected and old content is preserved.
// Steps:
// 1. Deploy blocklist server with valid IP content
// 2. Deploy controller with HTTP Store template
// 3. Verify initial blocklist content is fetched and used
// 4. Update blocklist content with invalid content (non-IP values)
// 5. Wait for refresh interval and validation attempt
// 6. Verify HAProxy config still contains old valid content
// 7. Verify invalid content was rejected
func buildHTTPStoreInvalidUpdateFeature() types.Feature {
	return features.New("HTTP Store - Invalid Update Cache Preservation").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace := envconf.RandomName("test-http-invalid", 32)
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

			// Create webhook certificate secret (required for controller startup)
			webhookSecret := NewWebhookCertSecret(namespace, "haproxy-webhook-certs")
			err = client.Resources().Create(ctx, webhookSecret)
			require.NoError(t, err)

			// Create blocklist server resources with valid content
			blocklistCM := NewBlocklistContentConfigMap(namespace, ValidBlocklistContent)
			err = client.Resources().Create(ctx, blocklistCM)
			require.NoError(t, err)

			blocklistDeploy := NewBlocklistServerDeployment(namespace)
			err = client.Resources().Create(ctx, blocklistDeploy)
			require.NoError(t, err)

			blocklistSvc := NewBlocklistService(namespace)
			err = client.Resources().Create(ctx, blocklistSvc)
			require.NoError(t, err)

			// Wait for blocklist server to be ready
			err = WaitForPodReady(ctx, client, namespace, "app="+BlocklistServerName, DefaultTimeout)
			require.NoError(t, err)
			t.Log("Blocklist server ready")

			// Create HAProxyTemplateConfig with HTTP Store
			crd := NewHTTPStoreHAProxyTemplateConfig(namespace, ControllerCRDName, ControllerSecretName, false)
			err = client.Resources().Create(ctx, crd)
			require.NoError(t, err)

			// Deploy controller
			deployment := NewControllerDeployment(namespace, ControllerCRDName, ControllerSecretName, ControllerServiceAccountName, DebugPort, 1)
			err = client.Resources().Create(ctx, deployment)
			require.NoError(t, err)

			return ctx
		}).
		Assess("Invalid HTTP update is rejected and old content preserved", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
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
			t.Log("Waiting for initial blocklist content...")
			err = debugClient.WaitForAuxFileContains(ctx, "blocked-ips.acl", "192.168.1.0/24", 60*time.Second)
			require.NoError(t, err)
			t.Log("Initial blocklist content verified")

			// Update the blocklist content with invalid content
			t.Log("Updating blocklist content with invalid content...")
			clientset, err := kubernetes.NewForConfig(cfg.Client().RESTConfig())
			require.NoError(t, err)

			blocklistCM, err := clientset.CoreV1().ConfigMaps(namespace).Get(ctx, BlocklistContentConfigMapName, metav1.GetOptions{})
			require.NoError(t, err)

			blocklistCM.Data["blocklist.txt"] = InvalidBlocklistContent
			_, err = clientset.CoreV1().ConfigMaps(namespace).Update(ctx, blocklistCM, metav1.UpdateOptions{})
			require.NoError(t, err)

			// Delete the blocklist server pod to trigger immediate reconnection and re-fetch
			pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
				LabelSelector: "app=" + BlocklistServerName,
			})
			require.NoError(t, err)
			require.NotEmpty(t, pods.Items, "Blocklist server pod should exist")

			err = clientset.CoreV1().Pods(namespace).Delete(ctx, pods.Items[0].Name, metav1.DeleteOptions{})
			require.NoError(t, err)

			// Wait for new blocklist server pod to be ready
			err = WaitForPodReady(ctx, client, namespace, "app="+BlocklistServerName, 60*time.Second)
			require.NoError(t, err)
			t.Log("Blocklist server restarted with invalid content")

			// Wait for the refresh interval and give time for the validation to happen
			// The invalid content should be rejected by HAProxy validation
			t.Log("Waiting for validation to reject invalid content...")
			time.Sleep(15 * time.Second) // Wait for refresh + validation

			// Dump controller logs to understand what happened
			t.Log("Dumping controller logs after wait...")
			DumpPodLogs(ctx, t, cfg.Client().RESTConfig(), pod)

			// Verify the old valid content is still present (not replaced with invalid content)
			content, err := debugClient.GetGeneralFileContent(ctx, "blocked-ips.acl")
			require.NoError(t, err)

			// Old content should still be present
			if !strings.Contains(content, "192.168.1.0/24") || !strings.Contains(content, "10.0.0.0/8") {
				t.Logf("FAILURE: Old valid content not preserved after invalid update rejection\nActual content:\n%s", content)
			}
			assert.Contains(t, content, "192.168.1.0/24", "Old valid content should be preserved")
			assert.Contains(t, content, "10.0.0.0/8", "Old valid content should be preserved")

			// Invalid content should NOT be present
			assert.NotContains(t, content, "not-an-ip-address", "Invalid content should not be applied")
			assert.NotContains(t, content, "invalid-cidr", "Invalid content should not be applied")

			// Verify content remains stable for a period (confirming rejection was permanent)
			t.Log("Verifying content remains stable...")
			err = debugClient.WaitForAuxFileContentStable(ctx, "blocked-ips.acl", "192.168.1.0/24", 10*time.Second)
			require.NoError(t, err)
			t.Log("Content stability verified - invalid update was rejected")

			// Check controller logs for validation failure message
			logs, err := GetPodLogs(ctx, client, pod, 100)
			if err == nil {
				if strings.Contains(logs, "validation failed") || strings.Contains(logs, "invalid") {
					t.Log("Controller logs show validation failure (expected)")
				}
			}

			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace, err := GetNamespaceFromContext(ctx)
			if err != nil {
				t.Logf("Failed to get namespace from context: %v", err)
				return ctx
			}

			client, err := cfg.NewClient()
			if err != nil {
				t.Logf("Failed to create client for cleanup: %v", err)
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
}

// TestHTTPStoreInvalidUpdate runs the invalid HTTP Store update test.
func TestHTTPStoreInvalidUpdate(t *testing.T) {
	testEnv.Test(t, buildHTTPStoreInvalidUpdateFeature())
}

// buildHTTPStoreFailoverFeature builds a feature that tests HTTP Store continues
// working after controller failover.
// Steps:
// 1. Deploy blocklist server with valid IP content
// 2. Deploy controller with 2 replicas and leader election
// 3. Wait for leader election
// 4. Verify initial blocklist content is fetched
// 5. Delete the leader pod
// 6. Wait for new leader election
// 7. Update blocklist content with new valid IPs
// 8. Verify new leader processes the update correctly
func buildHTTPStoreFailoverFeature() types.Feature {
	return features.New("HTTP Store - Controller Failover").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace := envconf.RandomName("test-http-failover", 32)
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

			// Create webhook certificate secret (required for controller startup)
			webhookSecret := NewWebhookCertSecret(namespace, "haproxy-webhook-certs")
			err = client.Resources().Create(ctx, webhookSecret)
			require.NoError(t, err)

			// Create blocklist server resources
			blocklistCM := NewBlocklistContentConfigMap(namespace, ValidBlocklistContent)
			err = client.Resources().Create(ctx, blocklistCM)
			require.NoError(t, err)

			blocklistDeploy := NewBlocklistServerDeployment(namespace)
			err = client.Resources().Create(ctx, blocklistDeploy)
			require.NoError(t, err)

			blocklistSvc := NewBlocklistService(namespace)
			err = client.Resources().Create(ctx, blocklistSvc)
			require.NoError(t, err)

			// Wait for blocklist server to be ready
			err = WaitForPodReady(ctx, client, namespace, "app="+BlocklistServerName, DefaultTimeout)
			require.NoError(t, err)
			t.Log("Blocklist server ready")

			// Create HAProxyTemplateConfig with HTTP Store AND leader election enabled
			crd := NewHTTPStoreHAProxyTemplateConfig(namespace, ControllerCRDName, ControllerSecretName, true)
			err = client.Resources().Create(ctx, crd)
			require.NoError(t, err)

			// Deploy controller with 2 replicas for failover testing
			deployment := NewControllerDeployment(namespace, ControllerCRDName, ControllerSecretName, ControllerServiceAccountName, DebugPort, 2)
			err = client.Resources().Create(ctx, deployment)
			require.NoError(t, err)

			return ctx
		}).
		Assess("HTTP Store works after controller failover", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace, err := GetNamespaceFromContext(ctx)
			require.NoError(t, err)

			client, err := cfg.NewClient()
			require.NoError(t, err)

			clientset, err := kubernetes.NewForConfig(cfg.Client().RESTConfig())
			require.NoError(t, err)

			// Wait for at least one controller pod to be ready
			err = WaitForPodReady(ctx, client, namespace, "app="+ControllerDeploymentName, DefaultTimeout)
			require.NoError(t, err)
			t.Log("At least one controller pod ready")

			// Wait for leader election
			leaseName := "haproxy-template-ic-leader"
			err = WaitForLeaderElection(ctx, cfg.Client().RESTConfig(), namespace, leaseName, 60*time.Second)
			require.NoError(t, err)
			t.Log("Leader election complete")

			// Get the current leader
			leaderIdentity, err := GetLeaseHolder(ctx, cfg.Client().RESTConfig(), namespace, leaseName)
			require.NoError(t, err)
			t.Logf("Current leader: %s", leaderIdentity)

			// Get leader pod for debug client
			allPods, err := GetAllControllerPods(ctx, client, namespace)
			require.NoError(t, err)

			var leaderPod *corev1.Pod
			for i := range allPods {
				pod := &allPods[i]
				if strings.Contains(leaderIdentity, pod.Name) {
					leaderPod = pod
					break
				}
			}
			require.NotNil(t, leaderPod, "Leader pod should be found")

			// Start debug client to leader
			debugClient := NewDebugClient(cfg.Client().RESTConfig(), leaderPod, DebugPort)
			err = debugClient.Start(ctx)
			require.NoError(t, err)

			// Verify initial content
			t.Log("Waiting for initial blocklist content on leader...")
			err = debugClient.WaitForAuxFileContains(ctx, "blocked-ips.acl", "192.168.1.0/24", 60*time.Second)
			require.NoError(t, err)
			t.Log("Initial blocklist content verified on leader")

			// Stop debug client before deleting pod
			debugClient.Stop()

			// Delete the leader pod to trigger failover
			t.Logf("Deleting leader pod %s to trigger failover...", leaderPod.Name)
			err = clientset.CoreV1().Pods(namespace).Delete(ctx, leaderPod.Name, metav1.DeleteOptions{})
			require.NoError(t, err)

			// Wait for new leader election (must be different from old leader)
			t.Log("Waiting for new leader election...")
			newLeaderIdentity, err := WaitForNewLeader(ctx, client, cfg.Client().RESTConfig(),
				namespace, leaseName, leaderIdentity, 90*time.Second)
			require.NoError(t, err)
			t.Logf("New leader: %s", newLeaderIdentity)

			// Get the new leader pod
			allPods, err = GetAllControllerPods(ctx, client, namespace)
			require.NoError(t, err)

			var newLeaderPod *corev1.Pod
			for i := range allPods {
				pod := &allPods[i]
				if strings.Contains(newLeaderIdentity, pod.Name) && pod.Status.Phase == corev1.PodRunning {
					newLeaderPod = pod
					break
				}
			}
			require.NotNil(t, newLeaderPod, "New leader pod should be found")

			// Start debug client to new leader
			newDebugClient := NewDebugClient(cfg.Client().RESTConfig(), newLeaderPod, DebugPort)
			err = newDebugClient.Start(ctx)
			require.NoError(t, err)
			defer newDebugClient.Stop()

			// Verify new leader has the blocklist content
			t.Log("Waiting for blocklist content on new leader...")
			err = newDebugClient.WaitForAuxFileContains(ctx, "blocked-ips.acl", "192.168.1.0/24", 60*time.Second)
			require.NoError(t, err)
			t.Log("Blocklist content verified on new leader")

			// Update blocklist content with new valid IPs
			t.Log("Updating blocklist content after failover...")
			blocklistCM, err := clientset.CoreV1().ConfigMaps(namespace).Get(ctx, BlocklistContentConfigMapName, metav1.GetOptions{})
			require.NoError(t, err)

			blocklistCM.Data["blocklist.txt"] = UpdatedBlocklistContent
			_, err = clientset.CoreV1().ConfigMaps(namespace).Update(ctx, blocklistCM, metav1.UpdateOptions{})
			require.NoError(t, err)

			// Delete the blocklist server pod to trigger immediate re-fetch
			pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
				LabelSelector: "app=" + BlocklistServerName,
			})
			require.NoError(t, err)
			require.NotEmpty(t, pods.Items, "Blocklist server pod should exist")

			err = clientset.CoreV1().Pods(namespace).Delete(ctx, pods.Items[0].Name, metav1.DeleteOptions{})
			require.NoError(t, err)

			// Wait for blocklist server to be ready again
			err = WaitForPodReady(ctx, client, namespace, "app="+BlocklistServerName, 60*time.Second)
			require.NoError(t, err)

			// Verify new leader processes the update correctly
			t.Log("Waiting for updated blocklist content on new leader after failover...")
			err = newDebugClient.WaitForAuxFileContains(ctx, "blocked-ips.acl", "172.16.0.0/12", 60*time.Second)
			require.NoError(t, err)
			t.Log("HTTP Store update successful after failover")

			// Verify full content
			content, err := newDebugClient.GetGeneralFileContent(ctx, "blocked-ips.acl")
			require.NoError(t, err)
			if !strings.Contains(content, "192.168.0.0/16") {
				t.Logf("FAILURE: Updated content not applied after failover\nActual content:\n%s", content)
			}
			assert.Contains(t, content, "192.168.0.0/16", "Updated content should be fully applied after failover")

			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			namespace, err := GetNamespaceFromContext(ctx)
			if err != nil {
				t.Logf("Failed to get namespace from context: %v", err)
				return ctx
			}

			client, err := cfg.NewClient()
			if err != nil {
				t.Logf("Failed to create client for cleanup: %v", err)
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
}

// TestHTTPStoreFailover runs the HTTP Store failover test.
func TestHTTPStoreFailover(t *testing.T) {
	testEnv.Test(t, buildHTTPStoreFailoverFeature())
}
