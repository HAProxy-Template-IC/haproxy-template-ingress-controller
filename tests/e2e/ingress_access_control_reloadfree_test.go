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
	"net/http"
	"testing"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/e2e-framework/klient"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

// TestAccessControlRouteAddRemoveIsReloadFree proves a route with a source-IP
// allowlist is dynamic: the list rides the shared map lane, so the frontend
// keeps no per-route ACL and a second allowlisted route is added and removed
// at runtime, admitting the client while it exists. Serial like the other
// reload-free suites: a reload count is attributable only on a quiet fleet.
func TestAccessControlRouteAddRemoveIsReloadFree(t *testing.T) {
	const (
		anchorHost = "ingress-haptic-allowlist-rf-anchor.localdev.me"
		anchorName = "echo-allowlist-rf-anchor"
		cycleHost  = "ingress-haptic-allowlist-rf-cycle.localdev.me"
		cycleName  = "echo-allowlist-rf-cycle"
	)
	var (
		client klient.Client
		cs     kubernetes.Interface
		dyn    dynamic.Interface
		ns     string
		echo   BackendRef
	)
	route := func(name, host string) *IngressSpec {
		return &IngressSpec{
			Name:           name,
			Host:           host,
			Path:           "/",
			BackendService: echo.Service,
			BackendPort:    echo.Port,
			Annotations: map[string]string{
				"haproxy-haptic.org/allowlist-source-range": "0.0.0.0/0",
			},
		}
	}
	feature := features.New("Ingress: haptic allowlist route add/remove is reload-free on 3.4").
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
			NewIngress(ctx, t, client, ns, route(anchorName, anchorHost))
			httpclient.New(t).GET(anchorHost, "/").ExpectOK(t)
			return ctx
		}).
		Assess("a second allowlisted route is added and removed at runtime and, on 3.4, never reloads",
			func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
				t.Helper()
				hc := httpclient.New(t)
				waitFleetQuiescent(ctx, t, client, cs)
				before := captureReloadFingerprint(ctx, t, cs)

				// Created directly: NewIngress would register a forget-namespace
				// wait on this sub-test, which blocks while the anchor still exists.
				// The cycle deletes the route itself.
				cycle := buildIngress(ns, route(cycleName, cycleHost))
				if err := client.Resources(ns).Create(ctx, cycle); err != nil {
					t.Fatalf("create Ingress %s/%s: %v", ns, cycleName, err)
				}
				reloadFreeReaction(ctx, t, cs, fmt.Sprintf("%s/ admits the client with 200", cycleHost),
					func(ctx context.Context) (bool, error) {
						resp, err := hc.GET(cycleHost, "/").Do(ctx)
						if err != nil {
							return false, err
						}
						if resp.Status != http.StatusOK {
							return false, fmt.Errorf("status %d", resp.Status)
						}
						if resp.Echo == nil {
							return false, fmt.Errorf("no echo JSON in %d bytes", len(resp.Body))
						}
						return true, nil
					})
				deleteRouteByName(ctx, t, dyn, ingressGVR, ns, cycleName)
				waitForRouteGone(ctx, t, cs, hc, cycleHost, "/")

				after := captureReloadFingerprint(ctx, t, cs)
				if dynamicBackendsSupported() {
					assertReloadFree(t, before, after, "allowlist route create+delete")
				} else {
					t.Logf("allowlist route cycle: %.0f reloads on %s (dynamic backends need 3.4)",
						after.reloads-before.reloads, ChartHAProxyVersion)
				}
				return ctx
			}).
		Feature()
	testEnv.Test(t, feature)
}
