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
	"testing"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/e2e-framework/klient"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

// TestIngressPathRewrite covers test_ingress_rewrite: the
// haproxy.org/path-rewrite annotation strips a path prefix before
// forwarding to the backend. echo-server includes the (rewritten) path
// in its JSON response, so we verify by asserting on Echo.Path.
func TestIngressPathRewrite(t *testing.T) {
	t.Parallel()
	RunSimpleIngressTest(t, &SimpleIngressTest{
		Description: "Ingress: path-rewrite annotation",
		Host:        "ingress-rewrite.localdev.me",
		Annotations: map[string]string{
			// Strip /api/v1/ prefix.
			"haproxy.org/path-rewrite": `^/api/v1/(.*) /\1`,
		},
		Assess: []SimpleIngressAssertion{
			{
				Name: "/api/v1/test rewrites to /test at the backend",
				Check: func(t *testing.T, host string) {
					t.Helper()
					httpclient.New(t).GET(host, "/api/v1/test").ExpectEchoPath(t, "/test")
				},
			},
			{
				Name: "/api/v1/users rewrites to /users at the backend",
				Check: func(t *testing.T, host string) {
					t.Helper()
					httpclient.New(t).GET(host, "/api/v1/users").ExpectEchoPath(t, "/users")
				},
			},
		},
	})
}

// TestIngressPathRewriteRouteAddRemoveIsReloadFree proves a haproxytech
// prefix-strip route is dynamic: the rewrite is a frontend map lookup, so the
// backend stays plain and a second route is added and removed at runtime.
// Serial like the other reload-free suites: a reload count is attributable
// only on a quiet fleet.
func TestIngressPathRewriteRouteAddRemoveIsReloadFree(t *testing.T) {
	const (
		anchorHost = "ingress-rewrite-rf-anchor.localdev.me"
		anchorName = "echo-rewrite-rf-anchor"
		cycleHost  = "ingress-rewrite-rf-cycle.localdev.me"
		cycleName  = "echo-rewrite-rf-cycle"
	)
	skipIfVendorDisabled(t, map[string]string{haproxytechPathRewrite: ""})
	var (
		client klient.Client
		cs     kubernetes.Interface
		dyn    dynamic.Interface
		ns     string
		echo   BackendRef
	)
	feature := features.New("Ingress: haproxy.org/path-rewrite route add/remove is reload-free on 3.4").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			t.Helper()
			var err error
			if client, err = cfg.NewClient(); err != nil {
				t.Fatalf("new client: %v", err)
			}
			if cs, err = newClientsetForE2E(client.RESTConfig()); err != nil {
				t.Fatalf("build clientset: %v", err)
			}
			if dyn, err = newDynamicForE2E(client.RESTConfig()); err != nil {
				t.Fatalf("build dynamic client: %v", err)
			}
			ns = NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			echo = NewEchoServerBackend(ctx, t, client, ns)
			NewIngress(ctx, t, client, ns, pathRewriteIngress(anchorName, anchorHost, echo, haproxytechPathRewrite))
			httpclient.New(t).GET(anchorHost, "/api/v1/ping").ExpectEchoPath(t, "/ping")
			return ctx
		}).
		Assess("a second rewrite route is added and removed at runtime and, on 3.4, never reloads",
			func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
				t.Helper()
				hc := httpclient.New(t)
				waitFleetQuiescent(ctx, t, client, cs)
				before := captureReloadFingerprint(ctx, t, cs)

				// Created directly: NewIngress would register a forget-namespace
				// wait on this sub-test, which blocks while the anchor still exists.
				// The cycle deletes the route itself.
				cycle := buildIngress(ns, pathRewriteIngress(cycleName, cycleHost, echo, haproxytechPathRewrite))
				if err := client.Resources(ns).Create(ctx, cycle); err != nil {
					t.Fatalf("create Ingress %s/%s: %v", ns, cycleName, err)
				}
				reloadFreeReaction(ctx, t, cs, fmt.Sprintf("%s/api/v1/ping reaches the upstream as /ping", cycleHost),
					func(ctx context.Context) (bool, error) {
						resp, err := hc.GET(cycleHost, "/api/v1/ping").Do(ctx)
						if err != nil {
							return false, err
						}
						if resp.Status != 200 {
							return false, fmt.Errorf("status %d", resp.Status)
						}
						if got := echoPath(resp); got != "/ping" {
							return false, fmt.Errorf("upstream saw %q, want /ping", got)
						}
						return true, nil
					})
				deleteRouteByName(ctx, t, dyn, ingressGVR, ns, cycleName)
				waitForRouteGone(ctx, t, cs, hc, cycleHost, "/api/v1/ping")

				after := captureReloadFingerprint(ctx, t, cs)
				if dynamicBackendsSupported() {
					assertReloadFree(t, before, after, "haproxytech path-rewrite route create+delete")
				} else {
					t.Logf("haproxytech path-rewrite route cycle: %.0f reloads on %s (dynamic backends need 3.4)",
						after.reloads-before.reloads, ChartHAProxyVersion)
				}
				return ctx
			}).
		Feature()
	testEnv.Test(t, feature)
}
