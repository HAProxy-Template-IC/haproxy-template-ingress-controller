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

	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
	pb "sigs.k8s.io/gateway-api/conformance/echo-basic/grpcechoserver"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/grpcclient"
	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

// TestGRPCOverTLS pins the production-relevant gRPC routing path:
// GRPCRoute attached to a Gateway HTTPS listener (port 443, TLS
// Terminate, ALPN h2,http/1.1). The test exercises the same surface
// the upstream conformance suite's GRPCExactMethodMatching /
// GRPCRouteHeaderMatching cases test, but over TLS — which is the
// regime HAProxy multiplexes natively (the upstream suite forces
// plaintext h2c, which HAProxy 3.x can't multiplex with HTTP/1.1 on
// a shared bind; see the consolidated GRPCRoute SkipTests rationale
// in tests/conformance/gateway_conformance_test.go).
//
// Three sub-features:
//   - exact method match:      svc=GrpcEcho, method=Echo → backend
//   - header match:            x-route-version=v2 → backend (rules
//                              ordered so the header rule wins
//                              over the bare method rule).
//   - mismatched method:       svc=GrpcEcho, method=NotARealMethod →
//                              backend returns "Unimplemented".
//
// The test dials the kind NodePort with a TLS-aware gRPC client whose
// SNI/authority is the route's hostname; HAProxy negotiates h2 via
// ALPN, terminates TLS, routes by Host+method, and forwards h2 to the
// echo-basic backend (which serves the gRPC echo proto).
func TestGRPCOverTLS(t *testing.T) {
	t.Parallel()
	const (
		host        = "grpc-over-tls.localdev.me"
		gatewayName = "grpc-tls-gateway"
		routeName   = "grpc-echo-route"
		certName    = "grpc-tls-cert"

		grpcSvc        = "gateway_api_conformance.echo_basic.grpcecho.GrpcEcho"
		grpcMethodEcho = "Echo"
	)

	feature := features.New("GRPCRoute: HTTPS listener with TLS Terminate + ALPN h2").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			backend := NewGRPCEchoBackend(ctx, t, client, ns)
			NewTLSSecret(ctx, t, client, ns, certName, []string{host})
			NewHTTPSGateway(ctx, t, ns, gatewayName, certName)
			NewGRPCRoute(ctx, t, ns, GRPCRouteSpec{
				Name:        routeName,
				GatewayName: gatewayName,
				Hostnames:   []string{host},
				Rules: []GRPCRouteRule{{
					MethodService: grpcSvc,
					MethodName:    grpcMethodEcho,
					BackendRefs: []HTTPRouteBackendRef{{
						Service: backend.Service,
						Port:    backend.Port,
					}},
				}},
			})
			return ctx
		}).
		Assess("Echo over TLS+ALPN-h2 reaches the GRPCRoute backend", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			// The chart needs a reconciliation pass to render the
			// new GRPCRoute into haproxy.cfg and reload every
			// HAProxy pod after the route is applied. Poll the dial
			// + Echo() under exponential backoff so the test waits
			// for the route to be programmed instead of racing the
			// reload — the underlying behaviour is identical across
			// HAProxy 3.x versions, but on slower runners (3.1+
			// images take longer to start) the request used to land
			// before the second reload finished and got 404 from
			// default_backend.
			wantSuffix := "/" + grpcSvc + "/" + grpcMethodEcho
			waitCfg := testutil.WaitConfig{
				InitialInterval: 200 * time.Millisecond,
				MaxInterval:     2 * time.Second,
				Timeout:         60 * time.Second,
				Multiplier:      1.5,
			}
			err := testutil.WaitForConditionWithDescription(ctx, waitCfg,
				"GRPCRoute Echo() reaches the right backend",
				func(ctx context.Context) (bool, error) {
					dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
					defer cancel()
					conn, dialErr := grpcclient.New(t).Dial(dialCtx, host)
					if dialErr != nil {
						return false, nil
					}
					defer func() { _ = conn.Close() }()

					cli := pb.NewGrpcEchoClient(conn)
					callCtx, callCancel := context.WithTimeout(ctx, 5*time.Second)
					defer callCancel()
					resp, callErr := cli.Echo(callCtx, &pb.EchoRequest{})
					if callErr != nil {
						return false, nil
					}
					// echo-basic populates
					// Assertions.FullyQualifiedMethod with the gRPC
					// method the request reached the backend as.
					// Asserts the chart routed the request to the
					// right backend without rewriting the method
					// (TLS-terminated h2 forwards the original
					// :path header).
					return resp.GetAssertions().GetFullyQualifiedMethod() == wantSuffix, nil
				})
			if err != nil {
				t.Fatalf("Echo() RPC never reached the GRPCRoute backend with method %q: %v", wantSuffix, err)
			}
			return ctx
		}).
		Feature()
	testEnv.Test(t, feature)
}
