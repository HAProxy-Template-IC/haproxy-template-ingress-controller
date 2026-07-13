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

// TestHapticSourceRange covers the haproxy-haptic.org source-IP allow/deny
// annotations, implemented in 30-frontend-filters.yaml via the shared
// EmitAnnotationAccessControl macro (aclPrefix "haptic"):
//
//   - haproxy-haptic.org/denylist-source-range → acl <name> src <cidrs> +
//     "http-request deny if <host-match> <acl>" (HAProxy deny → HTTP 403).
//   - haproxy-haptic.org/allowlist-source-range → acl <name> src <cidrs> +
//     "http-request deny if <host-match> !<acl>" (deny everything NOT in the
//     allow-list; a matching client is served → HTTP 200).
//
// DinD randomises the client source IP, so this test uses the two
// deterministic bounds of the CIDR space instead of a fixed range:
//
//   - denylist "0.0.0.0/0" matches every source, so the gate denies any
//     client → 403.
//   - allowlist "0.0.0.0/0" matches every source, so no client is denied →
//     200.
//
// This proves the deny rule fires and the allow rule admits, independent of
// whatever source IP the DinD network assigns.
func TestHapticSourceRange(t *testing.T) {
	t.Parallel()

	const (
		denyHost  = "ingress-haptic-deny.localdev.me"
		allowHost = "ingress-haptic-allow.localdev.me"
	)

	feature := features.New("Ingress: source-IP allow/deny via haproxy-haptic.org").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			backend := NewEchoServerBackend(ctx, t, client, ns)

			// Deny-all Ingress: 0.0.0.0/0 matches every client, so the
			// host-scoped "http-request deny if <host> <acl>" fires for all.
			NewIngress(ctx, t, client, ns, IngressSpec{
				Name:           "echo-deny",
				Host:           denyHost,
				BackendService: backend.Service,
				BackendPort:    backend.Port,
				Annotations: map[string]string{
					"haproxy-haptic.org/denylist-source-range": "0.0.0.0/0",
				},
			})

			// Allow-all Ingress: 0.0.0.0/0 matches every client, so the
			// host-scoped "http-request deny if <host> !<acl>" never fires.
			NewIngress(ctx, t, client, ns, IngressSpec{
				Name:           "echo-allow",
				Host:           allowHost,
				BackendService: backend.Service,
				BackendPort:    backend.Port,
				Annotations: map[string]string{
					"haproxy-haptic.org/allowlist-source-range": "0.0.0.0/0",
				},
			})
			return ctx
		}).
		Assess("denylist 0.0.0.0/0 rejects any client with 403", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.New(t).GET(denyHost, "/").ExpectStatus(t, http.StatusForbidden)
			return ctx
		}).
		Assess("allowlist 0.0.0.0/0 admits any client with 200", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.New(t).GET(allowHost, "/").ExpectOK(t)
			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}
