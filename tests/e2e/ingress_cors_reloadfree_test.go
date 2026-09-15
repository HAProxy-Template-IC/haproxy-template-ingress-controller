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

// corsRouteCase is one annotation family's CORS: the HAPTIC-native origin
// allow-list and the haproxytech origin regex share the lane but not the
// publisher.
type corsRouteCase struct {
	name        string
	annotations map[string]string
}

// TestCORSRouteAddRemoveIsReloadFree proves a CORS route is dynamic: the
// headers come from a frontend map lane, so the backend stays plain and a
// second route is added and removed at runtime. Serial like the other
// reload-free suites: a reload count is attributable only on a quiet fleet.
func TestCORSRouteAddRemoveIsReloadFree(t *testing.T) {
	const origin = "https://app.example.com"
	cases := []corsRouteCase{
		{
			name: "haptic",
			annotations: map[string]string{
				"haproxy-haptic.org/cors-enable":       "true",
				"haproxy-haptic.org/cors-allow-origin": origin,
			},
		},
		{
			name: "haproxytech",
			annotations: map[string]string{
				"haproxy.org/cors-enable":             "true",
				"haproxy.org/cors-allow-origin":       "^https://app\\.example\\.com$",
				"haproxy.org/cors-respond-to-options": "true",
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			skipIfVendorDisabled(t, c.annotations)
			corsRouteCycle(t, c, origin)
		})
	}
}

// corsRouteCycle is the anchor-plus-cycle shape every reload-free suite uses:
// the anchor route holds the block, the cycled route is created directly (not
// through NewIngress, whose cleanups would wait for the controller to forget
// the namespace while the anchor still exists), proven to echo the allowed
// Origin, deleted, and the fleet's reload count compared.
func corsRouteCycle(t *testing.T, c corsRouteCase, origin string) {
	t.Helper()
	anchorHost := fmt.Sprintf("ingress-%s-cors-rf-anchor.localdev.me", c.name)
	cycleHost := fmt.Sprintf("ingress-%s-cors-rf-cycle.localdev.me", c.name)
	const (
		anchorName = "echo-cors-rf-anchor"
		cycleName  = "echo-cors-rf-cycle"
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
	feature := features.New(fmt.Sprintf("Ingress: %s CORS route add/remove is reload-free on 3.4", c.name)).
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
			httpclient.New(t).GET(anchorHost, "/").WithHeader("Origin", origin).ExpectHeader(t, "Access-Control-Allow-Origin", origin)
			return ctx
		}).
		Assess("a second CORS route is added and removed at runtime and, on 3.4, never reloads",
			func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
				t.Helper()
				hc := httpclient.New(t)
				waitFleetQuiescent(ctx, t, client, cs)
				before := captureReloadFingerprint(ctx, t, cs)

				cycle := buildIngress(ns, route(cycleName, cycleHost))
				if err := client.Resources(ns).Create(ctx, cycle); err != nil {
					t.Fatalf("create Ingress %s/%s: %v", ns, cycleName, err)
				}
				reloadFreeReaction(ctx, t, cs, fmt.Sprintf("%s/ echoes the allowed Origin", cycleHost),
					corsEchoes(hc, cycleHost, origin))
				// A disallowed Origin gets no CORS headers: the row is per route,
				// not per host.
				resp := hc.GET(cycleHost, "/").WithHeader("Origin", "https://evil.example.net").ExpectOK(t)
				if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "" {
					t.Fatalf("disallowed Origin got Access-Control-Allow-Origin %q", got)
				}
				deleteRouteByName(ctx, t, dyn, ingressGVR, ns, cycleName)
				waitForRouteGone(ctx, t, cs, hc, cycleHost, "/")

				after := captureReloadFingerprint(ctx, t, cs)
				if dynamicBackendsSupported() {
					assertReloadFree(t, before, after, c.name+" CORS route create+delete")
				} else {
					t.Logf("%s CORS route cycle: %.0f reloads on %s (dynamic backends need 3.4)",
						c.name, after.reloads-before.reloads, ChartHAProxyVersion)
				}
				return ctx
			}).
		Feature()
	testEnv.Test(t, feature)
}

// corsEchoes reports whether host answers a request from origin with 200 and
// Access-Control-Allow-Origin echoing it.
func corsEchoes(hc *httpclient.Client, host, origin string) func(ctx context.Context) (bool, error) {
	return func(ctx context.Context) (bool, error) {
		resp, err := hc.GET(host, "/").WithHeader("Origin", origin).Do(ctx)
		if err != nil {
			return false, err
		}
		if resp.Status != http.StatusOK {
			return false, fmt.Errorf("status %d", resp.Status)
		}
		if got := resp.Header.Get("Access-Control-Allow-Origin"); got != origin {
			return false, fmt.Errorf("Access-Control-Allow-Origin %q, want %q", got, origin)
		}
		return true, nil
	}
}
