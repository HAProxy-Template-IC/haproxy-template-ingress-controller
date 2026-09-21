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
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
	pb "sigs.k8s.io/gateway-api-conformance-images/echo-basic/grpcechoserver"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/grpcclient"
	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

func TestGatewayRouteKindIsolation(t *testing.T) {
	t.Parallel()
	feature := features.New("Same-name HTTP and gRPC routes have independent filters").
		Assess("changing one route leaves the other route unchanged", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			t.Helper()
			client, err := cfg.NewClient()
			require.NoError(t, err)
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			const httpHost = "http-route-identity.localdev.me"
			const grpcHost = "grpc-route-identity.localdev.me"
			httpBackend := NewEchoServerBackend(ctx, t, client, ns)
			grpcBackend := NewGRPCEchoBackend(ctx, t, client, ns)
			NewTLSSecret(ctx, t, client, ns, "identity-cert", []string{httpHost, grpcHost})
			NewHTTPSGateway(ctx, t, ns, "identity", "identity-cert")
			httpRoute := gatewayIdentityRoute("HTTPRoute", ns, httpHost, "http", httpBackend)
			grpcRoute := gatewayIdentityRoute("GRPCRoute", ns, grpcHost, "grpc", grpcBackend)
			require.NoError(t, client.Resources().Create(ctx, httpRoute))
			require.NoError(t, client.Resources().Create(ctx, grpcRoute))
			waitForRouteDeployed(ctx, t, client, httpRouteGVR, ns, "same-name")
			waitForRouteDeployed(ctx, t, client, grpcRouteGVR, ns, "same-name")
			forward := ForwardGateway(ctx, t, ns, "identity", 443)
			request := httpclient.ForForwarded(t, 0, forward.HTTPSPort).HTTPS(httpHost, "/")
			require.Equal(t, "http", request.ExpectOK(t).Header.Get("X-Route-Kind"))
			dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			connection, err := grpcclient.ForForwarded(t, forward.HTTPSPort).Dial(dialCtx, grpcHost)
			require.NoError(t, err)
			defer func() { _ = connection.Close() }()
			assertGatewayGRPCResponseHeader(ctx, t, connection, "grpc")

			require.NoError(t, client.Resources().Get(ctx, "same-name", ns, grpcRoute))
			updated := gatewayIdentityRoute("GRPCRoute", ns, grpcHost, "grpc-updated", grpcBackend)
			updated.SetResourceVersion(grpcRoute.GetResourceVersion())
			require.NoError(t, client.Resources().Update(ctx, updated))
			waitForRouteDeployed(ctx, t, client, grpcRouteGVR, ns, "same-name")
			assertGatewayGRPCResponseHeader(ctx, t, connection, "grpc-updated")
			require.Equal(t, "http", request.ExpectOK(t).Header.Get("X-Route-Kind"))
			return ctx
		}).Feature()
	testEnv.Test(t, feature)
}

func gatewayIdentityRoute(kind, namespace, host, headerValue string, backend BackendRef) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1", "kind": kind,
		"metadata": map[string]any{"name": "same-name", "namespace": namespace},
		"spec": map[string]any{
			"parentRefs": []any{map[string]any{"name": "identity"}},
			"hostnames":  []any{host},
			"rules": []any{map[string]any{
				"filters": []any{map[string]any{
					"type": "ResponseHeaderModifier",
					"responseHeaderModifier": map[string]any{"set": []any{
						map[string]any{"name": "X-Route-Kind", "value": headerValue},
					}},
				}},
				"backendRefs": []any{map[string]any{"name": backend.Service, "port": int64(backend.Port)}},
			}},
		},
	}}
}

func assertGatewayGRPCResponseHeader(ctx context.Context, t *testing.T, connection *grpc.ClientConn, want string) {
	t.Helper()
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var headers metadata.MD
	response, err := pb.NewGrpcEchoClient(connection).Echo(callCtx, &pb.EchoRequest{}, grpc.Header(&headers))
	require.NoError(t, err)
	require.NotNil(t, response.GetAssertions())
	require.Equal(t, []string{want}, headers.Get("x-route-kind"))
}

func TestGatewayBackendNamespaceIsolation(t *testing.T) {
	t.Parallel()
	feature := features.New("Same-name Services in different namespaces remain independent").
		Assess("each path reaches its referenced namespace", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			t.Helper()
			client, err := cfg.NewClient()
			require.NoError(t, err)
			ns := NamespaceForTest(ctx, t, client)
			remoteNS := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			DumpLogsOnFailure(t, remoteNS)
			backend := NewEchoServerBackend(ctx, t, client, ns)
			NewEchoServerBackend(ctx, t, client, remoteNS)
			NewGateway(ctx, t, ns, "identity")
			grant := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "gateway.networking.k8s.io/v1", "kind": "ReferenceGrant",
				"metadata": map[string]any{"name": "identity", "namespace": remoteNS},
				"spec": map[string]any{
					"from": []any{map[string]any{"group": "gateway.networking.k8s.io", "kind": "HTTPRoute", "namespace": ns}},
					"to":   []any{map[string]any{"group": "", "kind": "Service", "name": backend.Service}},
				},
			}}
			require.NoError(t, client.Resources().Create(ctx, grant))
			const host = "backend-identity.localdev.me"
			route := gatewayIdentityRoute("HTTPRoute", ns, host, "http", backend)
			rules := []any{
				gatewayNamespaceRule("/local", ns, backend),
				gatewayNamespaceRule("/remote", remoteNS, backend),
			}
			require.NoError(t, unstructured.SetNestedSlice(route.Object, rules, "spec", "rules"))
			require.NoError(t, client.Resources().Create(ctx, route))
			waitForRouteDeployed(ctx, t, client, httpRouteGVR, ns, "same-name")
			forward := ForwardGateway(ctx, t, ns, "identity", 80)
			requests := httpclient.ForForwarded(t, forward.HTTPPort, 0)
			for path, namespace := range map[string]string{"/local": ns, "/remote": remoteNS} {
				response := requests.GET(host, path).ExpectOK(t)
				require.NotNil(t, response.Echo)
				require.NotEmpty(t, response.Echo.PodHostname)
				var pod corev1.Pod
				require.NoError(t, client.Resources().Get(ctx, response.Echo.PodHostname, namespace, &pod),
					"%s must reach a pod in %s", path, namespace)
			}
			return ctx
		}).Feature()
	testEnv.Test(t, feature)
}

func gatewayNamespaceRule(path, namespace string, backend BackendRef) map[string]any {
	return map[string]any{
		"matches": []any{map[string]any{"path": map[string]any{"type": "PathPrefix", "value": path}}},
		"backendRefs": []any{map[string]any{
			"name": backend.Service, "namespace": namespace, "port": int64(backend.Port),
		}},
	}
}
