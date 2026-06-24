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

	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

// TestIngressHostRenameMapOnly verifies the full deployed stack — the chart with
// its relative DataPlane storage dirs — correctly applies a *pure map change*.
//
// Renaming an Ingress's host keeps the same backend (HAPTIC derives the backend
// name from the Ingress name + Service + port, not the host), so only host/path
// map entries change. That makes it the reload-free runtime-map path: the
// renamed host must start routing AND the old host must stop being served by
// echo-server. The old-host check is the load-bearing one — a bulk-append bug
// (adding the new entry without removing the stale one) would leave the old host
// still echoing.
//
// This guards full-stack correctness of the runtime-map path in the real chart
// deployment. The *no-reload* property itself is pinned by the isolated
// integration test TestSyncAuxiliary/update-map-only-no-config-change, where a
// per-test HAProxy makes the reload assertion deterministic (the shared e2e
// fleet reloads continuously as other tests churn fixtures, so a global
// reload-count assertion here would be flaky).
func TestIngressHostRenameMapOnly(t *testing.T) {
	t.Parallel()

	const ingressName = "echo"
	hostA := "ingress-rename-a.localdev.me"
	hostB := "ingress-rename-b.localdev.me"
	var ns string

	feature := features.New("Ingress: host rename is a reload-free map replace").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns = NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			backend := NewEchoServerBackend(ctx, t, client, ns)
			NewIngress(ctx, t, client, ns, IngressSpec{
				Name:           ingressName,
				Host:           hostA,
				BackendService: backend.Service,
				BackendPort:    backend.Port,
			})
			return ctx
		}).
		Assess("original host routes to echo-server", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			resp := httpclient.New(t).GET(hostA, "/").ExpectOK(t)
			if resp.Echo == nil {
				t.Fatalf("expected echo-server body on %s, got %d bytes", hostA, len(resp.Body))
			}
			return ctx
		}).
		Assess("rename host: new host routes, old host no longer served (map replace, not append)", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}

			// Retry on optimistic-concurrency conflict: the controller updates the
			// Ingress status (load-balancer IP) concurrently, so a bare Get→Update
			// can race with "the object has been modified".
			if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				ing := &networkingv1.Ingress{}
				if err := client.Resources(ns).Get(ctx, ingressName, ns, ing); err != nil {
					return err
				}
				ing.Spec.Rules[0].Host = hostB
				return client.Resources(ns).Update(ctx, ing)
			}); err != nil {
				t.Fatalf("update ingress host: %v", err)
			}

			// The renamed host must route (retries until the runtime map change
			// propagates to the workers).
			resp := httpclient.New(t).GET(hostB, "/").ExpectOK(t)
			if resp.Echo == nil {
				t.Fatalf("expected echo-server body on renamed host %s, got %d bytes", hostB, len(resp.Body))
			}

			// The old host must no longer be served by echo-server. A bulk-append
			// runtime apply would leave the stale host.map entry and keep echoing.
			httpclient.New(t).GET(hostA, "/").ExpectMatching(t,
				"old host no longer served by echo-server",
				func(r *httpclient.Response) bool {
					return r.Status != 200 || r.Echo == nil
				})
			return ctx
		})

	testEnv.Test(t, feature.Feature())
}
