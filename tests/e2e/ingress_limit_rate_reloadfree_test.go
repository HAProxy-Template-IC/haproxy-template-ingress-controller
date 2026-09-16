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

// TestIngressLimitRateRouteAddRemoveIsReloadFree proves a throttled route is
// dynamic: the bandwidth filters are declared on the frontends and the route's
// rate comes from a map, so a second route reusing an existing limit-rate-after
// is added and removed at runtime with its backend left plain.
//
// The throttle itself is not measured here. A rate low enough to time reliably
// would make the test slow and its timing sensitive to the runner's load, which
// is the shape that produces flaky suites; the chart tests pin the rendered
// filter and rule instead. What this asserts is that a throttled route still
// serves, and that adding and removing one costs no reload.
//
// Serial like the other reload-free suites: a reload count is attributable only
// on a quiet fleet.
func TestIngressLimitRateRouteAddRemoveIsReloadFree(t *testing.T) {
	skipIfVendorDisabled(t, limitRateAnnotations("512k"))
	const (
		anchorHost = "ingress-limitrate-rf-anchor.localdev.me"
		anchorName = "echo-limitrate-rf-anchor"
		cycleHost  = "ingress-limitrate-rf-cycle.localdev.me"
		cycleName  = "echo-limitrate-rf-cycle"
	)
	var (
		client klient.Client
		cs     kubernetes.Interface
		dyn    dynamic.Interface
		ns     string
		echo   BackendRef
	)
	// The cycled route keeps the anchor's limit-rate-after and takes a rate of
	// its own: the filter is shared, the rate is a map value, so neither is new.
	route := func(name, host, rate string) *IngressSpec {
		return &IngressSpec{
			Name:           name,
			Host:           host,
			Path:           "/",
			BackendService: echo.Service,
			BackendPort:    echo.Port,
			Annotations:    limitRateAnnotations(rate),
		}
	}
	feature := features.New("Ingress: nginx-ingress limit-rate route add/remove is reload-free on 3.4").
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
			NewIngress(ctx, t, client, ns, route(anchorName, anchorHost, "512k"))
			httpclient.New(t).GET(anchorHost, "/").ExpectStatus(t, http.StatusOK)
			return ctx
		}).
		Assess("a second throttled route is added and removed at runtime and, on 3.4, never reloads",
			func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
				t.Helper()
				hc := httpclient.New(t)
				waitFleetQuiescent(ctx, t, client, cs)
				before := captureReloadFingerprint(ctx, t, cs)

				cycle := buildIngress(ns, route(cycleName, cycleHost, "256k"))
				if err := client.Resources(ns).Create(ctx, cycle); err != nil {
					t.Fatalf("create Ingress %s/%s: %v", ns, cycleName, err)
				}
				reloadFreeReaction(ctx, t, cs, fmt.Sprintf("%s/ answers 200", cycleHost),
					limitRateEchoes(hc, cycleHost))
				deleteRouteByName(ctx, t, dyn, ingressGVR, ns, cycleName)
				waitForRouteGone(ctx, t, cs, hc, cycleHost, "/")

				after := captureReloadFingerprint(ctx, t, cs)
				if dynamicBackendsSupported() {
					assertReloadFree(t, before, after, "limit-rate route create+delete")
				} else {
					t.Logf("limit-rate route cycle: %.0f reloads on %s (dynamic backends need 3.4)",
						after.reloads-before.reloads, ChartHAProxyVersion)
				}
				return ctx
			}).
		Feature()
	testEnv.Test(t, feature)
}

// limitRateAnnotations is the throttle at one rate. Every route in this suite
// keeps the same limit-rate-after, so they share a filter and only their map
// rows differ — which is what makes the cycle reload-free.
func limitRateAnnotations(rate string) map[string]string {
	return map[string]string{
		"nginx.ingress.kubernetes.io/limit-rate":       rate,
		"nginx.ingress.kubernetes.io/limit-rate-after": "1m",
	}
}

// limitRateEchoes reports whether the throttled route serves the echo backend.
func limitRateEchoes(hc *httpclient.Client, host string) func(ctx context.Context) (bool, error) {
	return func(ctx context.Context) (bool, error) {
		resp, err := hc.GET(host, "/").Do(ctx)
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
	}
}
