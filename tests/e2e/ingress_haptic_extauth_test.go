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

// TestHapticExternalAuth exercises the SPOA hub external-auth path driven by
// the haptic-native annotation haproxy-haptic.org/auth-url. The
// haptic-annotations library (50-auth-spoe.yaml) wires that annotation into
// auth-url.map, and the SPOA hub's external-auth plugin calls the auth-server
// fixture (deployed once into the shared `echo` namespace by TestMain).
//
// This mirrors the nginx-ingress vendor test TestIngressExternalAuth, adapted
// to haptic's canonical key. Two paths in one test:
//   - /allow returns 200 → request passes through to the backend
//   - /deny returns 401  → SPOA blocks before reaching the backend
func TestHapticExternalAuth(t *testing.T) {
	t.Parallel()

	const (
		hostAllow = "ingress-haptic-extauth.localdev.me"
		hostDeny  = "ingress-haptic-extauth-deny.localdev.me"
	)

	feature := features.New("Ingress: haptic external-auth (haproxy-haptic.org/auth-url)").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			backend := NewEchoServerBackend(ctx, t, client, ns)

			// auth-server lives in SharedFixturesNamespace; the SPOA hub
			// fetches it as plain HTTP across namespaces, not via an Ingress
			// backend (which would require same-namespace).
			authBase := "http://auth-server." + SharedFixturesNamespace + ".svc:80"

			NewIngress(ctx, t, client, ns, IngressSpec{
				Name:           "echo-haptic-auth-allow",
				Host:           hostAllow,
				Path:           "/",
				BackendService: backend.Service,
				BackendPort:    backend.Port,
				Annotations: map[string]string{
					"haproxy-haptic.org/auth-url": authBase + "/allow",
				},
			})
			NewIngress(ctx, t, client, ns, IngressSpec{
				Name:           "echo-haptic-auth-deny",
				Host:           hostDeny,
				Path:           "/",
				BackendService: backend.Service,
				BackendPort:    backend.Port,
				Annotations: map[string]string{
					"haproxy-haptic.org/auth-url": authBase + "/deny",
				},
			})
			return ctx
		}).
		Assess("allow host passes through to the backend", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.New(t).GET(hostAllow, "/").ExpectOK(t)
			return ctx
		}).
		Assess("deny host is blocked with 401", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.New(t).GET(hostDeny, "/").ExpectStatus(t, http.StatusUnauthorized)
			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}
