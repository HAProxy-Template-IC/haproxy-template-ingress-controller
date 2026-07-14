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

	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

// TestIngressExternalAuth exercises the SPOA hub external-auth path. The
// chart's nginx-ingress library wires nginx.ingress.kubernetes.io/auth-url
// to the SPOA hub's external-auth plugin, which calls into the auth-server
// fixture (deployed once into the shared `echo` namespace by TestMain).
//
// Two paths exercised in one test:
//   - /allow returns 200 → request passes through to the backend
//   - /deny returns 401  → SPOA blocks before reaching the backend
//
// Third smoke test; if this passes, the SPOA hub deployment, the chart's
// auth-url annotation wiring, and the httpclient retry-with-401-tolerance
// are all working.
func TestIngressExternalAuth(t *testing.T) {
	RequireVendorLibrary(t, "nginxIngress")
	t.Parallel()

	const (
		hostAllow = "auth-allowed.localdev.me"
		hostDeny  = "auth-denied.localdev.me"
	)

	feature := features.New("Ingress: SPOA hub external-auth").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			backend := NewEchoServerBackend(ctx, t, client, ns)

			// auth-server is in the SharedFixturesNamespace; its in-cluster
			// URL crosses namespaces because the SPOA hub fetches it as
			// plain HTTP, not via an Ingress backend (which would require
			// same-namespace).
			authBase := "http://auth-server." + SharedFixturesNamespace + ".svc:80"

			NewIngress(ctx, t, client, ns, IngressSpec{
				Name:           "echo-auth-allow",
				Host:           hostAllow,
				Path:           "/",
				BackendService: backend.Service,
				BackendPort:    backend.Port,
				Annotations: map[string]string{
					"nginx.ingress.kubernetes.io/auth-url": authBase + "/allow",
				},
			})
			NewIngress(ctx, t, client, ns, IngressSpec{
				Name:           "echo-auth-deny",
				Host:           hostDeny,
				Path:           "/",
				BackendService: backend.Service,
				BackendPort:    backend.Port,
				Annotations: map[string]string{
					"nginx.ingress.kubernetes.io/auth-url": authBase + "/deny",
				},
			})
			return ctx
		}).
		Assess("auth-allowed.localdev.me passes through to the backend", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.New(t).GET(hostAllow, "/").ExpectOK(t)
			return ctx
		}).
		Assess("auth-denied.localdev.me is blocked with 401", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.New(t).GET(hostDeny, "/").ExpectStatus(t, http.StatusUnauthorized)
			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}
