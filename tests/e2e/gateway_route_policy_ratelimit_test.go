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

//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/e2e-framework/klient/k8s/resources"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

func TestGatewayRoutePolicySharedRateLimit(t *testing.T) {
	RequireRateLimitProfile(t)
	feature := features.New("Gateway policy quota spans route rules and HAProxy replicas").
		Assess("one consumer budget is shared across the fleet", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			t.Helper()
			client, err := cfg.NewClient()
			require.NoError(t, err)
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			waitForManagedRateLimitStoreReady(ctx, t, client)
			const host = "gateway-policy-quota.localdev.me"
			mustCreateImmutableSecret(ctx, t, client, ns, "keys", map[string][]byte{"keys": []byte("first:consumer-a\nsecond:consumer-b\nprobe:warmup\n")})
			for _, name := range []string{"shared", "independent"} {
				policy := gatewayRoutePolicy(ns, name, map[string]any{
					"authentication": map[string]any{"apiKey": map[string]any{"secretRef": map[string]any{"name": "keys"}}},
					"rateLimit":      map[string]any{"requests": int64(5), "period": "10m", "burst": int64(5), "algorithm": "gcra", "key": "consumer"},
				})
				require.NoError(t, client.Resources().Create(ctx, policy))
			}
			backend := NewEchoServerBackend(ctx, t, client, ns)
			NewGateway(ctx, t, ns, "policy")
			route := gatewayPolicyRoute("HTTPRoute", ns, host, []any{
				gatewayPolicyHTTPRule(backend, "/first", "shared"),
				gatewayPolicyHTTPRule(backend, "/second", "shared"),
				gatewayPolicyHTTPRule(backend, "/independent", "independent"),
			})
			require.NoError(t, client.Resources().Create(ctx, route))
			waitForRouteDeployed(ctx, t, client, httpRouteGVR, ns, route.GetName())
			var service corev1.Service
			require.NoError(t, client.Resources().Get(ctx, waitForGatewayService(ctx, t, ns, "policy"), ControllerNamespace, &service))
			targetPort := targetPortForServicePort(&service, 80)
			require.Positive(t, targetPort)
			var pods corev1.PodList
			require.NoError(t, client.Resources(ControllerNamespace).List(ctx, &pods, resources.WithLabelSelector(LabelSelectorHAProxy)))
			targets := []corev1.Pod{}
			for _, pod := range pods.Items {
				if podReady(&pod) && pod.DeletionTimestamp == nil {
					targets = append(targets, pod)
				}
			}
			require.GreaterOrEqual(t, len(targets), 2)
			waitForGatewayQuotaHeaders(ctx, t, targets[:2], host, targetPort)
			var script strings.Builder
			for i := range 8 {
				path := "/first"
				if i/2%2 == 1 {
					path = "/second"
				}
				script.WriteString(gatewayQuotaProbe(targets[i%2].Status.PodIP, targetPort, host, path, "first"))
			}
			script.WriteString(gatewayQuotaProbe(targets[0].Status.PodIP, targetPort, host, "/first", "second"))
			script.WriteString(gatewayQuotaProbe(targets[1].Status.PodIP, targetPort, host, "/independent", "first"))
			output, err := execInHAProxyPod(ctx, targets[0].Name, "haproxy", "sh", "-c", script.String())
			require.NoError(t, err)
			require.Equal(t, []string{"200", "200", "200", "200", "200", "429", "429", "429", "200", "200"}, strings.Fields(output))
			return ctx
		}).Feature()
	testEnv.Test(t, feature)
}

func gatewayQuotaProbe(ip string, port int, host, path, key string) string {
	return fmt.Sprintf("curl -sS --connect-timeout 2 --max-time 5 -o /dev/null -H 'Host: %s' -H 'X-API-Key: %s' -w '%%{http_code}\\n' 'http://%s:%d%s'; ", host, key, ip, port, path)
}

func waitForGatewayQuotaHeaders(ctx context.Context, t *testing.T, pods []corev1.Pod, host string, port int) {
	t.Helper()
	err := testutil.WaitForConditionWithDescription(ctx, testutil.FastWaitConfig(), "shared quota headers on both Gateway replicas", func(ctx context.Context) (bool, error) {
		for i := range pods {
			pod := &pods[i]
			before, err := authoritativeRateLimitRequests(ctx, pod.Name)
			if err != nil {
				return false, err
			}
			output, err := execInHAProxyPod(ctx, pods[0].Name, "haproxy", "curl", "-sS", "--connect-timeout", "2", "--max-time", "5",
				"-o", "/dev/null", "-D", "-", "-H", "Host: "+host, "-H", "X-API-Key: probe", fmt.Sprintf("http://%s:%d/first", pod.Status.PodIP, port))
			if err != nil {
				return false, err
			}
			if !strings.Contains(strings.ToLower(output), "x-ratelimit-limit: 5") {
				return false, fmt.Errorf("replica %s has no shared quota headers: %s", pod.Name, output)
			}
			after, err := authoritativeRateLimitRequests(ctx, pod.Name)
			if err != nil {
				return false, err
			}
			if after <= before {
				return false, fmt.Errorf("replica %s has no authoritative shared quota decision", pod.Name)
			}
		}
		return true, nil
	})
	require.NoError(t, err)
}

func authoritativeRateLimitRequests(ctx context.Context, pod string) (float64, error) {
	metrics, err := execInHAProxyPod(ctx, pod, "haproxy", "curl", "-fsS", rateLimitHubMetricsURL)
	if err != nil {
		return 0, err
	}
	var total float64
	for _, outcome := range []string{"exact_allowed", "exact_limited"} {
		value, _, err := rateLimitOutcomeMetricValue(metrics, outcome)
		if err != nil {
			return 0, err
		}
		total += value
	}
	return total, nil
}
