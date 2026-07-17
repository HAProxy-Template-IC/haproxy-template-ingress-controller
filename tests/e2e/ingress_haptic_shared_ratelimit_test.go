// Copyright 2026 Philipp Hossner
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

//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/e2e-framework/klient"
	"sigs.k8s.io/e2e-framework/klient/k8s/resources"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

const (
	rateLimitStoreName          = HelmReleaseName + "-rl-store"
	labelSelectorRateLimitStore = "app.kubernetes.io/name=haptic-rate-limit-store,app.kubernetes.io/instance=" + rateLimitStoreName
)

// TestHapticSharedRateLimit exercises the Phase-4 shared rate-limit path:
// native haproxy-haptic.org/rate-limit-* annotations, SPOE dispatch to the
// bundled rate-limit plugin, and a chart-deployed Valkey store. The core proof
// hits two HAProxy pod IPs directly from one in-cluster curl pod. A per-pod
// limiter with the same limit would allow all direct requests; the shared
// Valkey-backed budget returns 429 once the fleet-wide limit is exhausted. It
// also proves source-IP rejection runs before Coraza, consumer keys are applied
// after native authentication, and Sentinel failover leaves limiting usable.
func TestHapticSharedRateLimit(t *testing.T) {
	RequireRateLimitProfile(t)
	t.Parallel()

	runID := time.Now().UnixNano()
	host := fmt.Sprintf("rl-%d.localdev.me", runID)
	leaseHost := fmt.Sprintf("rl-lease-%d.localdev.me", runID)
	failoverHost := fmt.Sprintf("rl-failover-%d.localdev.me", runID)
	consumerHost := fmt.Sprintf("rl-consumer-%d.localdev.me", runID)
	readinessHost := fmt.Sprintf("rl-ready-%d.localdev.me", runID)
	warmupHost := fmt.Sprintf("rl-warmup-%d.localdev.me", runID)
	consumerSecretName := fmt.Sprintf("rl-consumers-%d", runID)

	const (
		limit         = 5
		leaseLimit    = 2
		failoverLimit = 3
		warmupLimit   = 1000
		burstTotal    = 8
		leaseBurst    = 12
		failoverBurst = 5
	)

	feature := features.New("Ingress: HAPTIC shared rate-limit annotations").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			backend := NewEchoServerBackend(ctx, t, client, ns)
			consumerSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: consumerSecretName, Namespace: ns},
				Type:       corev1.SecretTypeOpaque,
				StringData: map[string]string{
					"keys": "key-alice:alice\nkey-bob:bob\n",
				},
			}
			if err := client.Resources(ns).Create(ctx, consumerSecret); err != nil {
				t.Fatalf("create consumer API-key Secret: %v", err)
			}
			ingresses := createSharedRateLimitIngresses(ctx, t, client, ns,
				IngressSpec{
					Name:           fmt.Sprintf("echo-shared-ratelimit-%d", time.Now().UnixNano()),
					Host:           host,
					Path:           "/",
					BackendService: backend.Service,
					BackendPort:    backend.Port,
					Annotations: map[string]string{
						"haproxy-haptic.org/rate-limit-requests":  fmt.Sprintf("%d", limit),
						"haproxy-haptic.org/rate-limit-period":    "60s",
						"haproxy-haptic.org/rate-limit-burst":     fmt.Sprintf("%d", limit),
						"haproxy-haptic.org/rate-limit-algorithm": "gcra",
					},
				},
				IngressSpec{
					Name:           fmt.Sprintf("echo-shared-ratelimit-lease-%d", time.Now().UnixNano()),
					Host:           leaseHost,
					Path:           "/",
					BackendService: backend.Service,
					BackendPort:    backend.Port,
					Annotations: map[string]string{
						"haproxy-haptic.org/rate-limit-requests": fmt.Sprintf("%d", leaseLimit),
						"haproxy-haptic.org/rate-limit-period":   "60s",
						"haproxy-haptic.org/rate-limit-burst":    fmt.Sprintf("%d", leaseLimit),
						"haproxy-haptic.org/waf":                 "modsecurity",
						"haproxy-haptic.org/waf-mode":            "deny",
					},
				},
				IngressSpec{
					Name:           fmt.Sprintf("echo-shared-ratelimit-failover-%d", time.Now().UnixNano()),
					Host:           failoverHost,
					Path:           "/",
					BackendService: backend.Service,
					BackendPort:    backend.Port,
					Annotations: map[string]string{
						"haproxy-haptic.org/rate-limit-requests":  fmt.Sprintf("%d", failoverLimit),
						"haproxy-haptic.org/rate-limit-period":    "60s",
						"haproxy-haptic.org/rate-limit-burst":     fmt.Sprintf("%d", failoverLimit),
						"haproxy-haptic.org/rate-limit-algorithm": "gcra",
					},
				},
				IngressSpec{
					Name:           fmt.Sprintf("echo-shared-ratelimit-consumer-%d", time.Now().UnixNano()),
					Host:           consumerHost,
					Path:           "/",
					BackendService: backend.Service,
					BackendPort:    backend.Port,
					Annotations: map[string]string{
						"haproxy-haptic.org/api-key-secret":       consumerSecretName,
						"haproxy-haptic.org/rate-limit-requests":  "1",
						"haproxy-haptic.org/rate-limit-period":    "60s",
						"haproxy-haptic.org/rate-limit-burst":     "1",
						"haproxy-haptic.org/rate-limit-key":       "consumer",
						"haproxy-haptic.org/rate-limit-algorithm": "gcra",
					},
				},
				IngressSpec{
					Name:           fmt.Sprintf("echo-shared-ratelimit-ready-%d", time.Now().UnixNano()),
					Host:           readinessHost,
					Path:           "/",
					BackendService: backend.Service,
					BackendPort:    backend.Port,
				},
				IngressSpec{
					Name:           fmt.Sprintf("echo-shared-ratelimit-warmup-%d", time.Now().UnixNano()),
					Host:           warmupHost,
					Path:           "/",
					BackendService: backend.Service,
					BackendPort:    backend.Port,
					Annotations: map[string]string{
						"haproxy-haptic.org/rate-limit-requests":  fmt.Sprintf("%d", warmupLimit),
						"haproxy-haptic.org/rate-limit-period":    "60s",
						"haproxy-haptic.org/rate-limit-burst":     fmt.Sprintf("%d", warmupLimit),
						"haproxy-haptic.org/rate-limit-algorithm": "gcra",
					},
				},
			)
			cleanupSharedRateLimitIngresses(t, client, ns, ingresses)
			// Wait until the rate-limited route is deployed without spending
			// request budget. HTTP polling is wrong here: the poll itself can
			// exhaust the shared limiter before the actual assertion runs.
			waitForControllerDeployed(ctx, t, client, ns)
			return StoreNamespaceInContext(ctx, ns)
		}).
		Assess("exact and lease budgets are shared across HAProxy pods", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			ns, err := GetNamespaceFromContext(ctx)
			if err != nil {
				t.Fatalf("get namespace: %v", err)
			}
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			if err := waitForDeploymentRolloutComplete(ctx, client, ControllerNamespace, HAProxyDeploymentName, 2*time.Minute); err != nil {
				t.Fatalf("wait for HAProxy rollout after rate-limit profile helm upgrade: %v", err)
			}
			podIPs := routableHAProxyPodIPs(ctx, t, client, ns, readinessHost, 2)
			waitForSharedRateLimitReadyOnPods(ctx, t, ns, warmupHost, podIPs[:2])
			result := sharedRateLimitBurstAcrossPods(ctx, t, ns, host, podIPs[:2], burstTotal, false)
			t.Logf("shared rate-limit direct-pod burst: %s", result.String())
			if result.byTarget["A"] == 0 || result.byTarget["B"] == 0 {
				t.Fatalf("expected burst to hit both HAProxy pods; got %s", result.String())
			}
			if result.byCode["429"] == 0 {
				t.Fatalf("expected at least one 429 from %d requests split across two HAProxy pods with one shared %d-request budget; got %s",
					burstTotal, limit, result.String())
			}
			if result.headerProbeCode != "429" || !result.headerRetryAfter ||
				!result.headerLimit || !result.headerRemaining || !result.headerReset {
				t.Fatalf("expected exhausted same-source probe to return 429 with rate-limit headers; got %s", result.String())
			}
			leaseResult := sharedRateLimitBurstAcrossPods(ctx, t, ns, leaseHost, podIPs[:2], leaseBurst, true)
			t.Logf("shared rate-limit lease-mode direct-pod burst: %s", leaseResult.String())
			if leaseResult.byTarget["A"] == 0 || leaseResult.byTarget["B"] == 0 {
				t.Fatalf("expected lease-mode burst to hit both HAProxy pods; got %s", leaseResult.String())
			}
			if leaseResult.byCode["429"] == 0 {
				t.Fatalf("expected at least one lease-mode 429 from %d requests split across two HAProxy pods with one shared %d-request budget; got %s",
					leaseBurst, leaseLimit, leaseResult.String())
			}
			if leaseResult.headerProbeCode != "429" || !leaseResult.headerRetryAfter ||
				!leaseResult.headerLimit || !leaseResult.headerRemaining || !leaseResult.headerReset {
				t.Fatalf("expected exhausted malicious lease-mode probe to return 429 with rate-limit headers before Coraza could return 403; got %s", leaseResult.String())
			}

			consumerCodes := probeSharedConsumerRateLimits(ctx, t, ns, consumerHost, podIPs[:2])
			t.Logf("shared rate-limit authenticated-consumer probes: %v", consumerCodes)
			if consumerCodes["alice-1"] != "200" || consumerCodes["alice-2"] != "429" ||
				consumerCodes["bob-1"] != "200" || consumerCodes["bob-2"] != "429" {
				t.Fatalf("expected independent one-request budgets for authenticated consumers alice and bob; got %v", consumerCodes)
			}

			deleteManagedRateLimitPrimary(ctx, t, client)
			waitForSharedRateLimitReadyOnPods(ctx, t, ns, warmupHost, podIPs[:2])
			failoverResult := sharedRateLimitBurstAcrossPods(ctx, t, ns, failoverHost, podIPs[:2], failoverBurst, false)
			t.Logf("shared rate-limit after Valkey primary failover: %s", failoverResult.String())
			if failoverResult.byCode["200"] == 0 {
				t.Fatalf("expected at least one 200 after deleting the managed Valkey primary; got %s", failoverResult.String())
			}
			if failoverResult.byCode["429"] == 0 {
				t.Fatalf("expected at least one 429 after deleting the managed Valkey primary; got %s", failoverResult.String())
			}
			if failoverResult.headerProbeCode != "429" || !failoverResult.headerRetryAfter ||
				!failoverResult.headerLimit || !failoverResult.headerRemaining || !failoverResult.headerReset {
				t.Fatalf("expected exhausted post-failover probe to return 429 with rate-limit headers; got %s", failoverResult.String())
			}
			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}

func createSharedRateLimitIngresses(
	ctx context.Context,
	t *testing.T,
	client klient.Client,
	namespace string,
	specs ...IngressSpec,
) []*networkingv1.Ingress {
	t.Helper()
	ingresses := make([]*networkingv1.Ingress, 0, len(specs))
	for _, spec := range specs {
		ing := buildIngress(namespace, spec)
		if err := client.Resources(namespace).Create(ctx, ing); err != nil {
			t.Fatalf("create Ingress %s/%s: %v", namespace, spec.Name, err)
		}
		ingresses = append(ingresses, ing)
	}
	return ingresses
}

func cleanupSharedRateLimitIngresses(t *testing.T, client klient.Client, namespace string, ingresses []*networkingv1.Ingress) {
	t.Helper()
	t.Cleanup(func() {
		bg := context.Background()
		for i := len(ingresses) - 1; i >= 0; i-- {
			ing := ingresses[i]
			if err := client.Resources(namespace).Delete(bg, ing); err != nil && !apierrors.IsNotFound(err) {
				t.Logf("delete Ingress %s/%s: %v (best-effort)", namespace, ing.Name, err)
			}
		}
		waitForControllerForgetNamespace(bg, t, client, namespace)
	})
}

func listReadyHAProxyPodIPs(ctx context.Context, client klient.Client) ([]string, error) {
	var pods corev1.PodList
	if err := client.Resources(ControllerNamespace).List(ctx, &pods, resources.WithLabelSelector(LabelSelectorHAProxy)); err != nil {
		return nil, err
	}
	ips := make([]string, 0, len(pods.Items))
	for i := range pods.Items {
		pod := pods.Items[i]
		if pod.DeletionTimestamp != nil || pod.Status.PodIP == "" || !podReady(pod) {
			continue
		}
		ips = append(ips, pod.Status.PodIP)
	}
	sort.Strings(ips)
	return ips, nil
}

func routableHAProxyPodIPs(ctx context.Context, t *testing.T, client klient.Client, namespace, host string, minPods int) []string {
	t.Helper()
	waitCfg := testutil.WaitConfig{
		InitialInterval: 1 * time.Second,
		MaxInterval:     5 * time.Second,
		Timeout:         2 * time.Minute,
		Multiplier:      1.5,
	}
	var ips []string
	err := testutil.WaitForConditionWithDescription(ctx, waitCfg, "at least two HAProxy pod IPs serving readiness route",
		func(ctx context.Context) (bool, error) {
			candidates, err := listReadyHAProxyPodIPs(ctx, client)
			if err != nil {
				return false, err
			}
			if len(candidates) < minPods {
				return false, fmt.Errorf("need %d Ready HAProxy pod IPs, have %d", minPods, len(candidates))
			}
			routable, err := probeHAProxyPodRoute(ctx, namespace, host, candidates)
			if err != nil {
				return false, err
			}
			ips = routable
			if len(ips) >= minPods {
				return true, nil
			}
			return false, fmt.Errorf("need %d HAProxy pod IPs serving host %q, have %d from candidates %v",
				minPods, host, len(ips), candidates)
		})
	if err != nil {
		t.Fatalf("wait for routable HAProxy pod IPs: %v", err)
	}
	return ips
}

func deleteManagedRateLimitPrimary(ctx context.Context, t *testing.T, client klient.Client) {
	t.Helper()
	waitForManagedRateLimitStoreReady(ctx, t, client)
	primaryPod := currentManagedRateLimitPrimaryPod(ctx, t, client)
	primary := primaryPod.Name
	primaryUID := string(primaryPod.UID)
	t.Logf("deleting managed Valkey primary pod %s (uid %s) to exercise Sentinel failover", primary, primaryUID)

	blockManagedRateLimitStoreReplacementScheduling(ctx, t)
	t.Cleanup(func() {
		restoreManagedRateLimitStoreScheduling(context.Background(), t)
	})
	deleteCmd := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", kubeconfigPath,
		"-n", ControllerNamespace,
		"delete", "pod", primary,
		"--grace-period=0",
		"--force",
		"--wait=false")
	var deleteErr bytes.Buffer
	deleteCmd.Stderr = &deleteErr
	if err := deleteCmd.Run(); err != nil {
		t.Fatalf("delete managed Valkey primary pod %s: %v\nstderr: %s", primary, err, deleteErr.String())
	}
	waitForManagedRateLimitPrimaryDeletionObserved(ctx, t, client, primary, primaryUID)
	waitForManagedRateLimitFailover(ctx, t, client, primary)
	restoreManagedRateLimitStoreScheduling(ctx, t)
	waitForManagedRateLimitStoreReady(ctx, t, client)
	newPrimary := currentManagedRateLimitPrimary(ctx, t, client)
	if newPrimary == primary {
		t.Fatalf("managed Valkey primary is still %s after forced deletion; Sentinel failover did not move the primary", primary)
	}
	t.Logf("managed Valkey primary after failover: %s", newPrimary)
}

func blockManagedRateLimitStoreReplacementScheduling(ctx context.Context, t *testing.T) {
	t.Helper()
	patch := `{"spec":{"updateStrategy":{"type":"OnDelete","rollingUpdate":null},"template":{"spec":{"nodeSelector":{"haproxy-haptic.org/e2e-unschedulable":"true"}}}}}`
	cmd := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", kubeconfigPath,
		"-n", ControllerNamespace,
		"patch", "statefulset", rateLimitStoreName,
		"--type=merge",
		"-p", patch)
	var patchErr bytes.Buffer
	cmd.Stderr = &patchErr
	if err := cmd.Run(); err != nil {
		t.Fatalf("patch managed Valkey StatefulSet to block replacement scheduling without rolling existing pods: %v\nstderr: %s", err, patchErr.String())
	}
}

func restoreManagedRateLimitStoreScheduling(ctx context.Context, t *testing.T) {
	t.Helper()
	patch := `{"spec":{"updateStrategy":{"type":"RollingUpdate","rollingUpdate":{}},"template":{"spec":{"nodeSelector":null}}}}`
	cmd := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", kubeconfigPath,
		"-n", ControllerNamespace,
		"patch", "statefulset", rateLimitStoreName,
		"--type=merge",
		"-p", patch)
	var patchErr bytes.Buffer
	cmd.Stderr = &patchErr
	if err := cmd.Run(); err != nil {
		t.Fatalf("restore managed Valkey StatefulSet scheduling: %v\nstderr: %s", err, patchErr.String())
	}
}

func waitForManagedRateLimitPrimaryDeletionObserved(ctx context.Context, t *testing.T, client klient.Client, name, uid string) {
	t.Helper()
	waitCfg := testutil.WaitConfig{
		InitialInterval: 500 * time.Millisecond,
		MaxInterval:     2 * time.Second,
		Timeout:         30 * time.Second,
		Multiplier:      1.5,
	}
	err := testutil.WaitForConditionWithDescription(ctx, waitCfg, "managed rate-limit primary pod deletion observed",
		func(ctx context.Context) (bool, error) {
			pods, err := listManagedRateLimitStorePods(ctx, client)
			if err != nil {
				return false, err
			}
			for i := range pods {
				pod := pods[i]
				if pod.Name != name || string(pod.UID) != uid {
					continue
				}
				if pod.DeletionTimestamp != nil || !podReady(pod) {
					return true, nil
				}
				return false, fmt.Errorf("pod %s uid %s still Ready and not deleting", name, uid)
			}
			return true, nil
		})
	if err != nil {
		t.Fatalf("wait for managed Valkey primary pod deletion: %v", err)
	}
}

func waitForManagedRateLimitFailover(ctx context.Context, t *testing.T, client klient.Client, oldPrimary string) {
	t.Helper()
	waitCfg := testutil.WaitConfig{
		InitialInterval: 1 * time.Second,
		MaxInterval:     5 * time.Second,
		Timeout:         2 * time.Minute,
		Multiplier:      1.5,
	}
	err := testutil.WaitForConditionWithDescription(ctx, waitCfg, "managed rate-limit Valkey Sentinel failover",
		func(ctx context.Context) (bool, error) {
			pods, err := listManagedRateLimitStorePods(ctx, client)
			if err != nil {
				return false, err
			}
			ready := 0
			for i := range pods {
				if podReady(pods[i]) {
					ready++
				}
			}
			if ready < 2 {
				return false, fmt.Errorf("need at least 2 Ready managed Valkey pods during failover, have %d", ready)
			}
			primary, err := findManagedRateLimitPrimary(ctx, pods)
			if err != nil {
				return false, err
			}
			if primary == oldPrimary {
				return false, fmt.Errorf("primary is still %s", oldPrimary)
			}
			return true, nil
		})
	if err != nil {
		t.Fatalf("wait for managed Valkey Sentinel failover away from %s: %v", oldPrimary, err)
	}
}

func waitForManagedRateLimitStoreReady(ctx context.Context, t *testing.T, client klient.Client) {
	t.Helper()
	waitCfg := testutil.WaitConfig{
		InitialInterval: 1 * time.Second,
		MaxInterval:     5 * time.Second,
		Timeout:         2 * time.Minute,
		Multiplier:      1.5,
	}
	err := testutil.WaitForConditionWithDescription(ctx, waitCfg, "managed rate-limit Valkey store ready",
		func(ctx context.Context) (bool, error) {
			pods, err := listManagedRateLimitStorePods(ctx, client)
			if err != nil {
				return false, err
			}
			if len(pods) < 3 {
				return false, fmt.Errorf("need 3 managed Valkey pods, have %d", len(pods))
			}
			ready := 0
			for i := range pods {
				if podReady(pods[i]) {
					ready++
				}
			}
			if ready < 3 {
				return false, fmt.Errorf("need 3 Ready managed Valkey pods, have %d", ready)
			}
			if _, err := findManagedRateLimitPrimary(ctx, pods); err != nil {
				return false, err
			}
			return true, nil
		})
	if err != nil {
		t.Fatalf("wait for managed rate-limit Valkey store: %v", err)
	}
}

func currentManagedRateLimitPrimary(ctx context.Context, t *testing.T, client klient.Client) string {
	t.Helper()
	return currentManagedRateLimitPrimaryPod(ctx, t, client).Name
}

func currentManagedRateLimitPrimaryPod(ctx context.Context, t *testing.T, client klient.Client) corev1.Pod {
	t.Helper()
	pods, err := listManagedRateLimitStorePods(ctx, client)
	if err != nil {
		t.Fatalf("list managed Valkey pods: %v", err)
	}
	primary, err := findManagedRateLimitPrimary(ctx, pods)
	if err != nil {
		t.Fatalf("find managed Valkey primary: %v", err)
	}
	for i := range pods {
		if pods[i].Name == primary {
			return pods[i]
		}
	}
	t.Fatalf("managed Valkey primary %s disappeared from listed pods", primary)
	return corev1.Pod{}
}

func listManagedRateLimitStorePods(ctx context.Context, client klient.Client) ([]corev1.Pod, error) {
	var pods corev1.PodList
	if err := client.Resources(ControllerNamespace).List(ctx, &pods, resources.WithLabelSelector(labelSelectorRateLimitStore)); err != nil {
		return nil, err
	}
	sort.Slice(pods.Items, func(i, j int) bool {
		return pods.Items[i].Name < pods.Items[j].Name
	})
	return pods.Items, nil
}

func findManagedRateLimitPrimary(ctx context.Context, pods []corev1.Pod) (string, error) {
	var checked []string
	for i := range pods {
		pod := pods[i]
		if pod.DeletionTimestamp != nil || !podReady(pod) {
			continue
		}
		checked = append(checked, pod.Name)
		roleCmd := exec.CommandContext(ctx, "kubectl",
			"--kubeconfig", kubeconfigPath,
			"-n", ControllerNamespace,
			"exec", pod.Name,
			"-c", "valkey",
			"--", "valkey-cli", "-p", "6379", "role")
		var out, roleErr bytes.Buffer
		roleCmd.Stdout = &out
		roleCmd.Stderr = &roleErr
		if err := roleCmd.Run(); err != nil {
			return "", fmt.Errorf("read Valkey role from pod %s: %w; stderr: %s", pod.Name, err, roleErr.String())
		}
		if strings.HasPrefix(strings.TrimSpace(out.String()), "master") {
			return pod.Name, nil
		}
	}
	return "", fmt.Errorf("no Ready managed Valkey primary among pods %v", checked)
}

func waitForSharedRateLimitReadyOnPods(ctx context.Context, t *testing.T, namespace, host string, podIPs []string) {
	t.Helper()
	waitCfg := testutil.WaitConfig{
		InitialInterval: 1 * time.Second,
		MaxInterval:     5 * time.Second,
		Timeout:         2 * time.Minute,
		Multiplier:      1.5,
	}
	err := testutil.WaitForConditionWithDescription(ctx, waitCfg, "rate-limit plugin active on HAProxy pods",
		func(ctx context.Context) (bool, error) {
			result, err := probeSharedRateLimitHeadersOnPods(ctx, namespace, host, podIPs)
			if err != nil {
				return false, err
			}
			if result.ready {
				return true, nil
			}
			return false, fmt.Errorf("rate-limit headers not ready: %s", result.String())
		})
	if err != nil {
		t.Fatalf("wait for shared rate-limit plugin readiness: %v", err)
	}
}

type sharedRateLimitReadinessProbeResult struct {
	ready bool
	lines []string
}

func (r sharedRateLimitReadinessProbeResult) String() string {
	if len(r.lines) == 0 {
		return "<no probe output>"
	}
	return strings.Join(r.lines, "; ")
}

func probeSharedRateLimitHeadersOnPods(ctx context.Context, namespace, host string, podIPs []string) (sharedRateLimitReadinessProbeResult, error) {
	var script strings.Builder
	script.WriteString("set -u; ")
	for _, ip := range podIPs {
		fmt.Fprintf(&script, `
headers=$(mktemp);
code=$(curl -s --connect-timeout 2 --max-time 5 -o /dev/null -D "$headers" -H "Host: %s" -w "%%{http_code}" http://%s:80/ || true);
limit=$(awk 'BEGIN{v=0} tolower($1)=="x-ratelimit-limit:"{v=1} END{print v}' "$headers");
remaining=$(awk 'BEGIN{v=0} tolower($1)=="x-ratelimit-remaining:"{v=1} END{print v}' "$headers");
reset=$(awk 'BEGIN{v=0} tolower($1)=="x-ratelimit-reset:"{v=1} END{print v}' "$headers");
echo "%s $code $limit $remaining $reset";
`, host, ip, ip)
	}

	podName := fmt.Sprintf("shared-ratelimit-ready-%d", time.Now().UnixNano())
	kubectlArgs := func(extra ...string) []string {
		return append([]string{"--kubeconfig", kubeconfigPath, "-n", namespace}, extra...)
	}

	runCmd := exec.CommandContext(ctx, "kubectl", kubectlArgs(
		"run", podName,
		"--restart=Never",
		"--image=alpine/curl:latest",
		"--quiet",
		"--command", "--",
		"sh", "-c", script.String(),
	)...)
	var runErr bytes.Buffer
	runCmd.Stderr = &runErr
	if err := runCmd.Run(); err != nil {
		return sharedRateLimitReadinessProbeResult{}, fmt.Errorf("create rate-limit readiness probe pod: %w; stderr: %s", err, runErr.String())
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = exec.CommandContext(cleanupCtx, "kubectl", kubectlArgs("delete", "pod", podName, "--now", "--ignore-not-found")...).Run()
	}()

	waitCmd := exec.CommandContext(ctx, "kubectl", kubectlArgs(
		"wait", "--for=jsonpath={.status.phase}=Succeeded",
		"pod/"+podName, "--timeout=30s")...)
	var waitErr bytes.Buffer
	waitCmd.Stderr = &waitErr
	if err := waitCmd.Run(); err != nil {
		return sharedRateLimitReadinessProbeResult{}, fmt.Errorf("wait for rate-limit readiness probe pod: %w; stderr: %s", err, waitErr.String())
	}

	var out, logsErr bytes.Buffer
	logsCmd := exec.CommandContext(ctx, "kubectl", kubectlArgs("logs", podName)...)
	logsCmd.Stdout = &out
	logsCmd.Stderr = &logsErr
	if err := logsCmd.Run(); err != nil {
		return sharedRateLimitReadinessProbeResult{}, fmt.Errorf("read rate-limit readiness probe logs: %w; stderr: %s", err, logsErr.String())
	}

	result := sharedRateLimitReadinessProbeResult{ready: true}
	seen := 0
	for _, raw := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		result.lines = append(result.lines, line)
		seen++
		fields := strings.Fields(line)
		if len(fields) != 5 || fields[1] != "200" || fields[2] != "1" || fields[3] != "1" || fields[4] != "1" {
			result.ready = false
		}
	}
	if seen != len(podIPs) {
		result.ready = false
		result.lines = append(result.lines, fmt.Sprintf("expected %d pod probe lines, got %d", len(podIPs), seen))
	}
	return result, nil
}

func probeHAProxyPodRoute(ctx context.Context, namespace, host string, podIPs []string) ([]string, error) {
	var script strings.Builder
	script.WriteString("set -u; ")
	for _, ip := range podIPs {
		fmt.Fprintf(&script,
			`code=$(curl -s --connect-timeout 2 --max-time 5 -o /dev/null -H "Host: %s" -w "%%{http_code}" http://%s:80/ || true); echo "%s $code"; `,
			host, ip, ip)
	}

	podName := fmt.Sprintf("shared-ratelimit-route-probe-%d", time.Now().UnixNano())
	kubectlArgs := func(extra ...string) []string {
		return append([]string{"--kubeconfig", kubeconfigPath, "-n", namespace}, extra...)
	}

	runCmd := exec.CommandContext(ctx, "kubectl", kubectlArgs(
		"run", podName,
		"--restart=Never",
		"--image=alpine/curl:latest",
		"--quiet",
		"--command", "--",
		"sh", "-c", script.String(),
	)...)
	var runErr bytes.Buffer
	runCmd.Stderr = &runErr
	if err := runCmd.Run(); err != nil {
		return nil, fmt.Errorf("create route probe pod: %w; stderr: %s", err, runErr.String())
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = exec.CommandContext(cleanupCtx, "kubectl", kubectlArgs("delete", "pod", podName, "--now", "--ignore-not-found")...).Run()
	}()

	waitCmd := exec.CommandContext(ctx, "kubectl", kubectlArgs(
		"wait", "--for=jsonpath={.status.phase}=Succeeded",
		"pod/"+podName, "--timeout=30s")...)
	var waitErr bytes.Buffer
	waitCmd.Stderr = &waitErr
	if err := waitCmd.Run(); err != nil {
		return nil, fmt.Errorf("wait for route probe pod: %w; stderr: %s", err, waitErr.String())
	}

	var out, logsErr bytes.Buffer
	logsCmd := exec.CommandContext(ctx, "kubectl", kubectlArgs("logs", podName)...)
	logsCmd.Stdout = &out
	logsCmd.Stderr = &logsErr
	if err := logsCmd.Run(); err != nil {
		return nil, fmt.Errorf("read route probe logs: %w; stderr: %s", err, logsErr.String())
	}

	codes := map[string]string{}
	routable := make([]string, 0, len(podIPs))
	for _, raw := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		codes[fields[0]] = fields[1]
		if fields[1] == "200" {
			routable = append(routable, fields[0])
		}
	}
	sort.Strings(routable)
	if len(routable) == 0 {
		return nil, fmt.Errorf("no HAProxy pod IP served host %q; candidates=%v codes=%v", host, podIPs, codes)
	}
	return routable, nil
}

func podReady(pod corev1.Pod) bool {
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

type sharedRateLimitBurstResult struct {
	requested int
	byTarget  map[string]int
	byCode    map[string]int
	lines     []string

	headerProbeCode  string
	headerRetryAfter bool
	headerLimit      bool
	headerRemaining  bool
	headerReset      bool
}

func (r sharedRateLimitBurstResult) String() string {
	type pair struct {
		k string
		v int
	}
	pairs := make([]pair, 0, len(r.byCode))
	for k, v := range r.byCode {
		pairs = append(pairs, pair{k: k, v: v})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].v != pairs[j].v {
			return pairs[i].v > pairs[j].v
		}
		return pairs[i].k < pairs[j].k
	})
	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		parts = append(parts, fmt.Sprintf("%d×%s", p.v, p.k))
	}
	return fmt.Sprintf("%d requests: %s; targets A=%d B=%d; headerProbe=%s retryAfter=%t limit=%t remaining=%t reset=%t; raw=%q",
		r.requested, strings.Join(parts, " / "), r.byTarget["A"], r.byTarget["B"],
		r.headerProbeCode, r.headerRetryAfter, r.headerLimit, r.headerRemaining, r.headerReset, r.lines)
}

func sharedRateLimitBurstAcrossPods(
	ctx context.Context,
	t *testing.T,
	namespace, host string,
	podIPs []string,
	total int,
	wafBlockProbe bool,
) sharedRateLimitBurstResult {
	t.Helper()
	if len(podIPs) < 2 {
		t.Fatalf("need at least two HAProxy pod IPs, got %v", podIPs)
	}

	var script strings.Builder
	script.WriteString("set -u; ")
	for i := 0; i < total; i++ {
		label := "A"
		ip := podIPs[0]
		if i%2 == 1 {
			label = "B"
			ip = podIPs[1]
		}
		fmt.Fprintf(&script,
			`code=$(curl -s --max-time 5 -o /dev/null -H "Host: %s" -w "%%{http_code}" http://%s:80/); echo "%s $code"; `,
			host, ip, label)
	}
	probeHeader := ""
	if wafBlockProbe {
		probeHeader = ` -H "User-Agent: haptic-waf-block-probe"`
	}
	fmt.Fprintf(&script, `
headers=$(mktemp);
code=$(curl -s --max-time 5 -o /dev/null -D "$headers" -H "Host: %s"%s -w "%%{http_code}" http://%s:80/);
retry_after=$(awk 'BEGIN{v=0} tolower($1)=="retry-after:"{v=1} END{print v}' "$headers");
limit=$(awk 'BEGIN{v=0} tolower($1)=="x-ratelimit-limit:"{v=1} END{print v}' "$headers");
remaining=$(awk 'BEGIN{v=0} tolower($1)=="x-ratelimit-remaining:"{v=1} END{print v}' "$headers");
reset=$(awk 'BEGIN{v=0} tolower($1)=="x-ratelimit-reset:"{v=1} END{print v}' "$headers");
echo "H $code $retry_after $limit $remaining $reset";
`,
		host, probeHeader, podIPs[0])

	podName := fmt.Sprintf("shared-ratelimit-burst-%d", time.Now().UnixNano())
	kubectlArgs := func(extra ...string) []string {
		return append([]string{"--kubeconfig", kubeconfigPath, "-n", namespace}, extra...)
	}

	runCmd := exec.CommandContext(ctx, "kubectl", kubectlArgs(
		"run", podName,
		"--restart=Never",
		"--image=alpine/curl:latest",
		"--quiet",
		"--command", "--",
		"sh", "-c", script.String(),
	)...)
	var runErr bytes.Buffer
	runCmd.Stderr = &runErr
	if err := runCmd.Run(); err != nil {
		t.Fatalf("create shared rate-limit burst pod: %v\nstderr: %s", err, runErr.String())
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = exec.CommandContext(cleanupCtx, "kubectl", kubectlArgs("delete", "pod", podName, "--now", "--ignore-not-found")...).Run()
	}()

	waitCmd := exec.CommandContext(ctx, "kubectl", kubectlArgs(
		"wait", "--for=jsonpath={.status.phase}=Succeeded",
		"pod/"+podName, "--timeout=45s")...)
	var waitErr bytes.Buffer
	waitCmd.Stderr = &waitErr
	if err := waitCmd.Run(); err != nil {
		t.Fatalf("wait for shared rate-limit burst pod: %v\nstderr: %s", err, waitErr.String())
	}

	var out, logsErr bytes.Buffer
	logsCmd := exec.CommandContext(ctx, "kubectl", kubectlArgs("logs", podName)...)
	logsCmd.Stdout = &out
	logsCmd.Stderr = &logsErr
	if err := logsCmd.Run(); err != nil {
		t.Fatalf("read shared rate-limit burst logs: %v\nstderr: %s", err, logsErr.String())
	}

	result := sharedRateLimitBurstResult{
		requested: total,
		byTarget:  map[string]int{},
		byCode:    map[string]int{},
	}
	for _, raw := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		result.lines = append(result.lines, line)
		fields := strings.Fields(line)
		if len(fields) == 6 && fields[0] == "H" {
			result.headerProbeCode = fields[1]
			result.headerRetryAfter = fields[2] == "1"
			result.headerLimit = fields[3] == "1"
			result.headerRemaining = fields[4] == "1"
			result.headerReset = fields[5] == "1"
			continue
		}
		if len(fields) != 2 {
			result.byCode["unparsed"]++
			continue
		}
		result.byTarget[fields[0]]++
		result.byCode[fields[1]]++
	}
	if len(result.lines) != total+1 {
		t.Fatalf("expected %d burst status lines plus one header probe from shared rate-limit burst pod, got %d: %s",
			total, len(result.lines), result.String())
	}
	return result
}

// probeSharedConsumerRateLimits proves that consumer-keyed quotas dispatch
// after native API-key authentication. Both requests originate from one pod;
// an accidental source-IP fallback would make bob's first request a 429.
func probeSharedConsumerRateLimits(
	ctx context.Context,
	t *testing.T,
	namespace, host string,
	podIPs []string,
) map[string]string {
	t.Helper()
	if len(podIPs) < 2 {
		t.Fatalf("need at least two HAProxy pod IPs, got %v", podIPs)
	}

	var script strings.Builder
	script.WriteString("set -eu; ")
	probes := []struct {
		label, key, ip string
	}{
		{label: "alice-1", key: "key-alice", ip: podIPs[0]},
		{label: "alice-2", key: "key-alice", ip: podIPs[1]},
		{label: "bob-1", key: "key-bob", ip: podIPs[1]},
		{label: "bob-2", key: "key-bob", ip: podIPs[0]},
	}
	for _, probe := range probes {
		fmt.Fprintf(&script,
			`code=$(curl -s --max-time 5 -o /dev/null -H "Host: %s" -H "X-API-Key: %s" -w "%%{http_code}" http://%s:80/); echo "%s $code"; `,
			host, probe.key, probe.ip, probe.label)
	}

	podName := fmt.Sprintf("shared-ratelimit-consumers-%d", time.Now().UnixNano())
	kubectlArgs := func(extra ...string) []string {
		return append([]string{"--kubeconfig", kubeconfigPath, "-n", namespace}, extra...)
	}
	runCmd := exec.CommandContext(ctx, "kubectl", kubectlArgs(
		"run", podName,
		"--restart=Never",
		"--image=alpine/curl:latest",
		"--quiet",
		"--command", "--",
		"sh", "-c", script.String(),
	)...)
	var runErr bytes.Buffer
	runCmd.Stderr = &runErr
	if err := runCmd.Run(); err != nil {
		t.Fatalf("create shared consumer rate-limit probe pod: %v\nstderr: %s", err, runErr.String())
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = exec.CommandContext(cleanupCtx, "kubectl", kubectlArgs("delete", "pod", podName, "--now", "--ignore-not-found")...).Run()
	}()

	waitCmd := exec.CommandContext(ctx, "kubectl", kubectlArgs(
		"wait", "--for=jsonpath={.status.phase}=Succeeded",
		"pod/"+podName, "--timeout=45s")...)
	var waitErr bytes.Buffer
	waitCmd.Stderr = &waitErr
	if err := waitCmd.Run(); err != nil {
		t.Fatalf("wait for shared consumer rate-limit probe pod: %v\nstderr: %s", err, waitErr.String())
	}

	var out, logsErr bytes.Buffer
	logsCmd := exec.CommandContext(ctx, "kubectl", kubectlArgs("logs", podName)...)
	logsCmd.Stdout = &out
	logsCmd.Stderr = &logsErr
	if err := logsCmd.Run(); err != nil {
		t.Fatalf("read shared consumer rate-limit probe logs: %v\nstderr: %s", err, logsErr.String())
	}

	codes := map[string]string{}
	for _, raw := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		fields := strings.Fields(raw)
		if len(fields) != 2 {
			t.Fatalf("unexpected shared consumer rate-limit probe output %q", raw)
		}
		codes[fields[0]] = fields[1]
	}
	return codes
}
