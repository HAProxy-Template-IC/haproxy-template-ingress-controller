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

// vendorBasicAuthCase is one vendor annotation family's basic auth: the
// annotations that enable it and the Secret shape its userlist reads.
type vendorBasicAuthCase struct {
	name        string
	annotations map[string]string
	secret      func(ctx context.Context, t *testing.T, client klient.Client, namespace string)
}

// TestVendorBasicAuthRouteAddRemoveIsReloadFree proves a vendor basic-auth
// route on an existing credentials Secret is dynamic in each vendor library:
// the challenge is a frontend rule fed by a per-route map, so the backend
// stays plain and a second route is added and removed at runtime. Serial like
// the other reload-free suites: a reload count is attributable only on a quiet
// fleet. Each library's case is skipped where the e2e profile has it off.
func TestVendorBasicAuthRouteAddRemoveIsReloadFree(t *testing.T) {
	cases := []vendorBasicAuthCase{
		{
			name: "nginx-ingress",
			annotations: map[string]string{
				"nginx.ingress.kubernetes.io/auth-type":   "basic",
				"nginx.ingress.kubernetes.io/auth-secret": "echo-auth-secret",
				"nginx.ingress.kubernetes.io/auth-realm":  "Echo-Server-Protected",
			},
			secret: createNginxAuthSecret,
		},
		{
			name: "haproxy-ingress",
			annotations: map[string]string{
				"haproxy-ingress.github.io/auth-type":   "basic",
				"haproxy-ingress.github.io/auth-secret": "echo-auth-secret",
				"haproxy-ingress.github.io/auth-realm":  "Echo-Server-Protected",
			},
			secret: createBasicAuthSecret,
		},
		{
			name: "haproxytech",
			annotations: map[string]string{
				"haproxy.org/auth-type":   "basic-auth",
				"haproxy.org/auth-secret": "echo-auth-secret",
				"haproxy.org/auth-realm":  "Echo-Server-Protected",
			},
			secret: createBasicAuthSecret,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			skipIfVendorDisabled(t, c.annotations)
			vendorBasicAuthCycle(t, c)
		})
	}
}

// vendorBasicAuthCycle is the anchor-plus-cycle shape every reload-free suite
// uses: an anchor route holds the userlist and the block, the cycled route is
// created directly (not through NewIngress, whose cleanups would wait for the
// controller to forget the namespace while the anchor still exists), proven
// challenged, deleted, and the fleet's reload count compared.
func vendorBasicAuthCycle(t *testing.T, c vendorBasicAuthCase) {
	t.Helper()
	anchorHost := fmt.Sprintf("ingress-%s-basicauth-rf-anchor.localdev.me", c.name)
	cycleHost := fmt.Sprintf("ingress-%s-basicauth-rf-cycle.localdev.me", c.name)
	const (
		anchorName = "echo-basicauth-rf-anchor"
		cycleName  = "echo-basicauth-rf-cycle"
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
			Annotations:    c.annotations,
		}
	}
	feature := features.New(fmt.Sprintf("Ingress: %s basic-auth route add/remove is reload-free on 3.4", c.name)).
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
			c.secret(ctx, t, client, ns)
			NewIngress(ctx, t, client, ns, route(anchorName, anchorHost))
			httpclient.New(t).GET(anchorHost, "/").WithBasicAuth("admin", "admin").ExpectOK(t)
			return ctx
		}).
		Assess("a second route on the same Secret is added and removed at runtime and, on 3.4, never reloads",
			func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
				t.Helper()
				hc := httpclient.New(t)
				waitFleetQuiescent(ctx, t, client, cs)
				before := captureReloadFingerprint(ctx, t, cs)

				cycle := buildIngress(ns, route(cycleName, cycleHost))
				if err := client.Resources(ns).Create(ctx, cycle); err != nil {
					t.Fatalf("create Ingress %s/%s: %v", ns, cycleName, err)
				}
				reloadFreeReaction(ctx, t, cs, fmt.Sprintf("%s/ answers 200 with credentials", cycleHost),
					func(ctx context.Context) (bool, error) {
						resp, err := hc.GET(cycleHost, "/").WithBasicAuth("admin", "admin").Do(ctx)
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
				// The runtime-added route is challenged too: its map row, not a
				// backend rule, names the userlist.
				hc.GET(cycleHost, "/").ExpectStatus(t, http.StatusUnauthorized)
				deleteRouteByName(ctx, t, dyn, ingressGVR, ns, cycleName)
				waitForRouteGone(ctx, t, cs, hc, cycleHost, "/")

				after := captureReloadFingerprint(ctx, t, cs)
				if dynamicBackendsSupported() {
					assertReloadFree(t, before, after, c.name+" basic-auth route create+delete")
				} else {
					t.Logf("%s basic-auth route cycle: %.0f reloads on %s (dynamic backends need 3.4)",
						c.name, after.reloads-before.reloads, ChartHAProxyVersion)
				}
				return ctx
			}).
		Feature()
	testEnv.Test(t, feature)
}
