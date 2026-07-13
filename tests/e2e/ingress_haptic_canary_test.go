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

	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

// TestHapticCanary covers haproxy-haptic.org/canary* (the native superset of
// nginx.ingress.kubernetes.io/canary*). Two Ingresses share one host: a main
// Ingress owns the base route (the default backend, ENVIRONMENT="") and a
// canary Ingress (canary: "true") overlays a header-based use_backend split
// onto a distinct canary backend (ENVIRONMENT="v2"). The
// features-800-haptic-canary-colocation snippet keeps the canary out of
// base-route ownership so normal traffic lands on the main; the
// frontend-filters-810-haptic-canary snippet emits
// `use_backend <canary> if { req.hdr(X-Canary) -m str true }` so a request
// carrying the canary header is split off to the canary backend.
//
// The assertions poll on the echoed ENVIRONMENT (via ExpectEchoEnvironment)
// so they close the route-readiness race deterministically without sleeps:
// header-carrying traffic must reach v2, header-less traffic must reach the
// default backend.
func TestHapticCanary(t *testing.T) {
	t.Parallel()
	const host = "ingress-haptic-canary.localdev.me"

	feature := features.New("Ingress: haproxy-haptic.org/canary header-based split").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)

			// Two distinguishable backends: the default (v1) echo-server and
			// the v2-tagged echo-server. resp.Echo.Environment tells them apart.
			mainBackend := NewEchoServerBackend(ctx, t, client, ns)
			canaryBackend := NewEchoServerV2Backend(ctx, t, client, ns)

			// Main Ingress owns the base route for the shared host → v1 backend.
			NewIngress(ctx, t, client, ns, IngressSpec{
				Name:           "echo-main",
				Host:           host,
				BackendService: mainBackend.Service,
				BackendPort:    mainBackend.Port,
			})

			// Canary Ingress shares the host and splits X-Canary: true off to
			// the v2 backend. It does NOT own the base route (colocation marker).
			NewIngress(ctx, t, client, ns, IngressSpec{
				Name:           "echo-canary",
				Host:           host,
				BackendService: canaryBackend.Service,
				BackendPort:    canaryBackend.Port,
				Annotations: map[string]string{
					"haproxy-haptic.org/canary":                 "true",
					"haproxy-haptic.org/canary-by-header":       "X-Canary",
					"haproxy-haptic.org/canary-by-header-value": "true",
				},
			})
			return ctx
		}).
		Assess("canary header routes to the canary (v2) backend", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.New(t).GET(host, "/").WithHeader("X-Canary", "true").ExpectEchoEnvironment(t, "v2")
			return ctx
		}).
		Assess("normal traffic hits the main (default) backend", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.New(t).GET(host, "/").ExpectEchoEnvironment(t, "")
			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}
