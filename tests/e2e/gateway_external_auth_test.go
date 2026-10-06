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
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/e2e-framework/klient"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

// TestGatewayExternalAuth drives the HTTPRoute ExternalAuth filter against the
// shared auth-server fixture, whose /gw-check location answers 200 only when
// exactly Authorization and X-Api-Key arrive (no Cookie) with the client's Host.
func TestGatewayExternalAuth(t *testing.T) {
	t.Parallel()

	const host = "gw-extauth.localdev.me"

	feature := features.New("Gateway: HTTPRoute ExternalAuth filter").
		Assess("auth server decides, headers are scoped, failures stay closed", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			t.Helper()
			client, err := cfg.NewClient()
			require.NoError(t, err)
			requireExperimentalGatewayAPI(ctx, t, client)
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			backend := NewEchoServerBackend(ctx, t, client, ns)
			grantExternalAuthServer(ctx, t, client, ns)
			unreachable := newServiceRefusingConnections(ctx, t, client, ns, backend)
			NewGateway(ctx, t, ns, "extauth")

			authServer := map[string]any{"name": "auth-server", "namespace": SharedFixturesNamespace, "port": int64(80)}
			route := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "gateway.networking.k8s.io/v1", "kind": "HTTPRoute",
				"metadata": map[string]any{"namespace": ns, "name": "extauth"},
				"spec": map[string]any{
					"parentRefs": []any{map[string]any{"name": "extauth"}},
					"hostnames":  []any{host},
					"rules": []any{
						externalAuthRule(backend, "/check", authServer, "/gw-check", []any{"X-Auth-User"}),
						externalAuthRule(backend, "/deny", authServer, "/deny", []any{"X-Auth-User"}),
						externalAuthRule(backend, "/down",
							map[string]any{"name": unreachable, "port": int64(80)}, "", []any{"X-Auth-User"}),
						externalAuthRule(backend, "/unenforceable", authServer, "/gw-check", nil),
						map[string]any{
							"matches":     []any{map[string]any{"path": map[string]any{"type": "PathPrefix", "value": "/public"}}},
							"backendRefs": []any{map[string]any{"name": backend.Service, "port": int64(backend.Port)}},
						},
					},
				},
			}}
			require.NoError(t, client.Resources().Create(ctx, route))
			waitForRouteDeployed(ctx, t, client, httpRouteGVR, ns, "extauth")
			forward := ForwardGateway(ctx, t, ns, "extauth", 80)
			requests := httpclient.ForForwarded(t, forward.HTTPPort, 0)
			authorized := func(path string) *httpclient.Request {
				return requests.GET(host, path).
					WithHeader("Authorization", "Bearer good").
					WithHeader("X-Api-Key", "k1").
					WithHeader("Cookie", "session=never-forwarded")
			}

			requests.GET(host, "/public").ExpectOK(t)
			// The auth server's X-Auth-User replaces the client's forged copy.
			authorized("/check").WithHeader("X-Auth-User", "mallory").ExpectEchoHeader(t, "X-Auth-User", "alice")

			once := func(request *httpclient.Request) *httpclient.Response {
				t.Helper()
				callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
				defer cancel()
				response, err := request.Do(callCtx)
				require.NoError(t, err)
				return response
			}
			checked := once(authorized("/check").WithHeader("X-Auth-User", "mallory"))
			require.Equal(t, http.StatusOK, checked.Status)
			require.NotNil(t, checked.Echo)
			require.Equal(t, "alice", checked.Echo.Headers["x-auth-user"])
			require.Empty(t, checked.Echo.Headers["x-auth-roles"], "a response header outside allowedResponseHeaders reached the backend")

			require.Equal(t, http.StatusUnauthorized, once(requests.GET(host, "/check")).Status)
			require.Equal(t, http.StatusUnauthorized, once(requests.GET(host, "/check").WithHeader("Authorization", "Bearer good")).Status)
			require.Equal(t, http.StatusUnauthorized, once(authorized("/deny")).Status)
			require.Equal(t, http.StatusUnauthorized, once(authorized("/down")).Status, "an unreachable auth service must deny")
			require.Equal(t, http.StatusInternalServerError, once(authorized("/unenforceable")).Status)
			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}

func externalAuthRule(backend BackendRef, path string, authBackend map[string]any, authPath string, responseHeaders []any) map[string]any {
	httpConfig := map[string]any{"allowedHeaders": []any{"X-Api-Key"}}
	if authPath != "" {
		httpConfig["path"] = authPath
	}
	if responseHeaders != nil {
		httpConfig["allowedResponseHeaders"] = responseHeaders
	}
	return map[string]any{
		"matches": []any{map[string]any{"path": map[string]any{"type": "PathPrefix", "value": path}}},
		"filters": []any{map[string]any{
			"type": "ExternalAuth",
			"externalAuth": map[string]any{
				"protocol":   "HTTP",
				"backendRef": authBackend,
				"http":       httpConfig,
			},
		}},
		"backendRefs": []any{map[string]any{"name": backend.Service, "port": int64(backend.Port)}},
	}
}

// grantExternalAuthServer lets the test namespace's HTTPRoutes reference the
// shared auth-server Service, which lives outside every test namespace.
func grantExternalAuthServer(ctx context.Context, t *testing.T, client klient.Client, namespace string) {
	t.Helper()
	grant := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1", "kind": "ReferenceGrant",
		"metadata": map[string]any{"name": "extauth-" + namespace, "namespace": SharedFixturesNamespace},
		"spec": map[string]any{
			"from": []any{map[string]any{"group": "gateway.networking.k8s.io", "kind": "HTTPRoute", "namespace": namespace}},
			"to":   []any{map[string]any{"group": "", "kind": "Service", "name": "auth-server"}},
		},
	}}
	require.NoError(t, client.Resources().Create(ctx, grant))
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		if err := client.Resources().Delete(cleanupCtx, grant); err != nil && !apierrors.IsNotFound(err) {
			t.Errorf("delete ReferenceGrant %s/%s: %v", SharedFixturesNamespace, grant.GetName(), err)
		}
	})
}

// newServiceRefusingConnections selects the echo pods on a port they don't
// listen on, so the auth request is refused at once rather than timing out.
func newServiceRefusingConnections(ctx context.Context, t *testing.T, client klient.Client, namespace string, backend BackendRef) string {
	t.Helper()
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "auth-unreachable", Namespace: namespace},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": backend.Service},
			Ports:    []corev1.ServicePort{{Port: 80, TargetPort: intstr.FromInt32(9)}},
		},
	}
	require.NoError(t, client.Resources().Create(ctx, service))
	return service.Name
}
