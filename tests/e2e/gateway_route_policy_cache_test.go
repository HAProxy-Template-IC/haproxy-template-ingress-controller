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
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

func TestGatewayRoutePolicyCacheIsolation(t *testing.T) {
	RequireCacheProfile(t)
	t.Parallel()
	feature := features.New("Gateway private cache identity and rule isolation").
		Assess("distinct consumer components and rewritten rules never share a response", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			t.Helper()
			client, err := cfg.NewClient()
			require.NoError(t, err)
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			const host = "gateway-policy-cache.localdev.me"
			mustCreateImmutableSecret(ctx, t, client, ns, "keys", map[string][]byte{"keys": []byte("first:a|b\nsecond:a\n")})
			policy := gatewayRoutePolicy(ns, "private-cache", map[string]any{
				"authentication": map[string]any{"apiKey": map[string]any{"secretRef": map[string]any{"name": "keys"}, "consumerHeader": "X-Consumer"}},
				"cache":          map[string]any{"ttlSeconds": int64(60), "varyHeaders": []any{"X-Partition"}},
			})
			require.NoError(t, client.Resources().Create(ctx, policy))
			backend := NewEchoServerBackend(ctx, t, client, ns)
			NewGateway(ctx, t, ns, "policy")
			rules := make([]any, 0, 2)
			for _, marker := range []string{"first-rule", "second-rule"} {
				rule := gatewayPolicyHTTPRule(backend, "/"+marker, policy.GetName())
				rule["filters"] = append(rule["filters"].([]any), map[string]any{
					"type": "URLRewrite", "urlRewrite": map[string]any{"path": map[string]any{"type": "ReplaceFullPath", "replaceFullPath": "/origin"}},
				}, map[string]any{
					"type": "RequestHeaderModifier", "requestHeaderModifier": map[string]any{"set": []any{map[string]any{"name": "X-Rule", "value": marker}}},
				})
				rules = append(rules, rule)
			}
			route := gatewayPolicyRoute("HTTPRoute", ns, host, rules)
			require.NoError(t, client.Resources().Create(ctx, route))
			waitForRouteDeployed(ctx, t, client, httpRouteGVR, ns, route.GetName())
			forward := ForwardGateway(ctx, t, ns, "policy", 80)
			requests := httpclient.ForForwarded(t, forward.HTTPPort, 0)
			first := requests.GET(host, "/first-rule").WithHeader("X-API-Key", "first").WithHeader("X-Partition", "c")
			response := first.ExpectMatching(t, "first consumer's cache hit", func(response *httpclient.Response) bool {
				return response.Status == http.StatusOK && response.Header.Get("X-Cache") == "HIT"
			})
			assertGatewayCachedIdentity(t, response, "a|b", "first-rule")
			second := requests.GET(host, "/first-rule").WithHeader("X-API-Key", "second").WithHeader("X-Partition", "b|c").
				WithHeader("X-Haptic-Cache-Vary", "forged").WithHeader("X-Haptic-Cache-Auth", "1")
			response, err = second.Do(ctx)
			require.NoError(t, err)
			assertGatewayCachedIdentity(t, response, "a", "first-rule")
			response = second.ExpectMatching(t, "second consumer's cache hit", func(response *httpclient.Response) bool {
				return response.Status == http.StatusOK && response.Header.Get("X-Cache") == "HIT"
			})
			assertGatewayCachedIdentity(t, response, "a", "first-rule")
			response, err = requests.GET(host, "/second-rule").WithHeader("X-API-Key", "first").WithHeader("X-Partition", "c").Do(ctx)
			require.NoError(t, err)
			assertGatewayCachedIdentity(t, response, "a|b", "second-rule")
			response, err = requests.GET(host, "/first-rule").WithHeader("X-Partition", "c").
				WithHeader("X-Consumer", "a|b").WithHeader("X-Haptic-Cache-Auth", "1").Do(ctx)
			require.NoError(t, err)
			require.Equal(t, http.StatusUnauthorized, response.Status)
			return ctx
		}).Feature()
	testEnv.Test(t, feature)
}

func assertGatewayCachedIdentity(t *testing.T, response *httpclient.Response, consumer, rule string) {
	t.Helper()
	require.Equal(t, http.StatusOK, response.Status)
	require.NotNil(t, response.Echo)
	require.Equal(t, consumer, response.Echo.Headers["x-consumer"])
	require.Equal(t, rule, response.Echo.Headers["x-rule"])
	require.Equal(t, "/origin", response.Echo.Path)
	require.Contains(t, response.Header.Get("Cache-Control"), "private")
}
