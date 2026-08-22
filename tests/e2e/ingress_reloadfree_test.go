// Copyright 2026 Philipp Hossner
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
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/e2e-framework/klient"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

// routeGVR is the custom-crd-example library's Route kind, watched when the
// library is enabled in the e2e install.
var routeGVR = schema.GroupVersionResource{Group: "haptic-example.org", Version: "v1", Resource: "routes"}

// TestIngressRouteAddRemoveIsReloadFree is workstream D's ingress proof: the
// same reload-free contract the gateway suite asserts, but for an Ingress whose
// per-route logic comes from the haproxytech annotation library — the C13 work
// that moved header manipulation into a backend-keyed map and the timeout into
// the shared profile. An anchor Ingress of the identical shape holds the profile
// and the static header line alive, so each cycled Ingress add/remove is only a
// dynamic backend plus a map entry: reload-free on 3.4, a reload apiece below.
//
// The custom-CRD reload-free cycle the plan lists (custom-crd-example library)
// is deliberately NOT here: that library is disabled by default and ships no
// CRD, so exercising it end to end would enable a suite-wide library and apply a
// hand-written CRD, destabilising every other e2e test. RULE #1 — that the
// runtime lane is resource-agnostic — is already proven by the custom-crd-example
// chart validationTest fixture (dynamic shape recorded, one profile for N
// objects, map entries present) and by deployplan diffing plans, not resources.
func TestIngressRouteAddRemoveIsReloadFree(t *testing.T) {
	const (
		anchorHost = "ing-anchor.localdev.me"
		cycleHost  = "ing-cycle.localdev.me"
		cycleName  = "ing-cycle"
	)
	respHeader := reloadFreeRespHeader

	var (
		client    klient.Client
		cs        kubernetes.Interface
		dyn       dynamic.Interface
		namespace string
		cycleSvc  BackendRef
	)

	feature := features.New("Ingress route add/remove is reload-free on 3.4").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
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
			namespace = NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, namespace)

			anchorSvc := NewNamedEchoServerBackend(ctx, t, client, namespace, "echo-anchor")
			cycleSvc = NewNamedEchoServerBackend(ctx, t, client, namespace, "echo-cycle")

			// A stable Ingress of the same shape holds the profile and the static
			// response-header line, so cycling the second Ingress moves only a
			// dynamic backend and a map entry.
			NewIngress(ctx, t, client, namespace, IngressSpec{
				Name:           "ing-anchor",
				Host:           anchorHost,
				BackendService: anchorSvc.Service,
				BackendPort:    anchorSvc.Port,
				Annotations:    ingressFilterAnnotations("rf-anchor"),
			})
			waitForRouteServing(ctx, t, httpclient.New(t), anchorHost, "/", respHeader, "rf-anchor")
			return ctx
		}).
		Assess("each cycle serves the Ingress and, on 3.4, never reloads", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			dynamicBE := dynamicBackendsSupported()
			http := httpclient.New(t)
			// Drain the anchor's first-appearance reload and any sibling-test
			// teardown before measuring, so a cycle is charged only its own work.
			waitFleetQuiescent(ctx, t, cs)
			startReloads := captureReloadFingerprint(ctx, t, cs)

			for cycle := 1; cycle <= reloadFreeCycles; cycle++ {
				want := fmt.Sprintf("rf-%d", cycle)
				before := captureReloadFingerprint(ctx, t, cs)
				divBefore := mapDivergenceTotal(ctx, t, cs)

				applyIngressFilteredRoute(ctx, t, namespace, cycleName, cycleHost, cycleSvc, want)
				latency := waitForRouteServing(ctx, t, http, cycleHost, "/", respHeader, want)

				entries := mapEntriesFrom(showMap(ctx, t, cs, "maps/ing-reshdr.map"))
				assertMapHasValue(t, entries, want, "maps/ing-reshdr.map")

				deleteRouteByName(ctx, t, dyn, ingressGVR, namespace, cycleName)
				waitForRouteGone(ctx, t, http, cycleHost, "/")

				after := captureReloadFingerprint(ctx, t, cs)
				if dynamicBE {
					assertReloadFree(t, before, after, fmt.Sprintf("ingress cycle %d create+delete", cycle))
					if div := mapDivergenceTotal(ctx, t, cs); div != divBefore {
						t.Fatalf("ingress cycle %d: map divergence advanced %.0f→%.0f", cycle, divBefore, div)
					}
				}
				t.Logf("ingress cycle %d: create→200 in %s", cycle, latency.Round(time.Millisecond))
			}

			reloads := captureReloadFingerprint(ctx, t, cs).reloads - startReloads.reloads
			t.Logf("ingress reload-free suite: %.0f reloads over %d cycles (dynamic backends: %t)",
				reloads, reloadFreeCycles, dynamicBE)
			if !dynamicBE && reloads > float64(2*reloadFreeCycles) {
				t.Fatalf("on %s an Ingress add/remove reloads, but %.0f reloads over %d cycles exceeds the 2N bound",
					ChartHAProxyVersion, reloads, reloadFreeCycles)
			}
			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}

// TestCustomCRDRouteAddRemoveIsReloadFree is the RULE #1 runtime proof: a
// resource-agnostic library watching a user-defined `Route` CRD (no bundled
// schema, read untyped) drives the same Backend() macro, so adding and removing
// a Route object adds and removes a dynamic backend at runtime — no reload on
// 3.4 — exactly like Ingress and Gateway. The library builds no host routing
// map (it exists to exercise the macros, not to serve traffic), so the proof is
// the backend's runtime lifecycle in `show stat`, not a 200. Render-time
// resource-agnosticism is separately proven by the library's own validationTest
// (test-custom-crd-route-reload-free) and the differential test.
//
// The library is enabled and its Route CRD installed in the e2e setup
// (main_test.go). With no Route objects it renders an empty map and no static
// lines, so other suites are unaffected.
func TestCustomCRDRouteAddRemoveIsReloadFree(t *testing.T) {
	const cycleName = "rf-cycle"

	var (
		client    klient.Client
		cs        kubernetes.Interface
		dyn       dynamic.Interface
		namespace string
	)

	feature := features.New("Custom-CRD route add/remove is reload-free on 3.4").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
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
			namespace = NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, namespace)

			// An anchor Route of the same shape holds the shared profile so the
			// cycled Route's backend is a pure runtime add/remove.
			applyCustomRoute(ctx, t, namespace, "rf-anchor")
			waitBackendRuntime(ctx, t, cs, namespace+"_rf-anchor", true)
			return ctx
		}).
		Assess("each cycle adds and removes the Route's backend, reload-free on 3.4", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			dynamicBE := dynamicBackendsSupported()
			backend := namespace + "_" + cycleName
			waitFleetQuiescent(ctx, t, cs)
			startReloads := captureReloadFingerprint(ctx, t, cs)

			for cycle := 1; cycle <= reloadFreeCycles; cycle++ {
				before := captureReloadFingerprint(ctx, t, cs)

				applyCustomRoute(ctx, t, namespace, cycleName)
				waitBackendRuntime(ctx, t, cs, backend, true)

				deleteRouteByName(ctx, t, dyn, routeGVR, namespace, cycleName)
				waitBackendRuntime(ctx, t, cs, backend, false)

				after := captureReloadFingerprint(ctx, t, cs)
				if dynamicBE {
					assertReloadFree(t, before, after, fmt.Sprintf("custom-CRD cycle %d add+remove", cycle))
				}
				t.Logf("custom-CRD cycle %d: backend added and removed at runtime", cycle)
			}

			reloads := captureReloadFingerprint(ctx, t, cs).reloads - startReloads.reloads
			t.Logf("custom-CRD reload-free suite: %.0f reloads over %d cycles (dynamic backends: %t)",
				reloads, reloadFreeCycles, dynamicBE)
			if !dynamicBE && reloads > float64(2*reloadFreeCycles) {
				t.Fatalf("on %s a custom Route add/remove reloads, but %.0f reloads over %d cycles exceeds the 2N bound",
					ChartHAProxyVersion, reloads, reloadFreeCycles)
			}
			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}

// applyCustomRoute applies a Route CR of the custom-crd-example library's kind.
// Its backend address is a placeholder — the test asserts the backend's runtime
// presence, never traffic.
func applyCustomRoute(ctx context.Context, t *testing.T, namespace, name string) {
	t.Helper()
	manifest := fmt.Sprintf(`apiVersion: haptic-example.org/v1
kind: Route
metadata:
  name: %s
  namespace: %s
spec:
  backend: {address: 10.0.0.9, port: 8080}
  requestHeaders:
    - {name: X-Rf-Resp, value: rf-custom}
`, name, namespace)
	if err := kubectlApplyStdin(ctx, []byte(manifest)); err != nil {
		t.Fatalf("apply Route %s/%s: %v", namespace, name, err)
	}
}

// waitBackendRuntime blocks until the running worker has, or no longer has, the
// named backend — the reload-free lifecycle signal for the custom-CRD cycle.
func waitBackendRuntime(ctx context.Context, t *testing.T, cs kubernetes.Interface, backend string, want bool) {
	t.Helper()
	err := testutil.WaitForConditionWithDescription(ctx, testutil.WaitConfig{
		InitialInterval: 200 * time.Millisecond,
		MaxInterval:     2 * time.Second,
		Timeout:         convergenceTimeout(),
		Multiplier:      1.5,
	}, fmt.Sprintf("backend %s present=%t at runtime", backend, want), func(ctx context.Context) (bool, error) {
		if backendInRuntime(ctx, t, cs, backend) == want {
			return true, nil
		}
		return false, fmt.Errorf("backend %s present=%t, want %t", backend, !want, want)
	})
	if err != nil {
		t.Fatalf("backend %s never reached present=%t: %v", backend, want, err)
	}
}

// ingressFilterAnnotations are the haproxytech per-route directives C13 moved
// onto the runtime lane: a response header that lands in a backend-keyed map,
// and a server timeout that lands in the shared profile. Compression is turned
// off: haptic-annotations enables it by default, and a compression `filter`
// cannot live in a named defaults, so it makes the backend structural — a route
// that carries it reloads on add/remove by design (appendix §E). The dynamic
// lane this suite proves is for backends whose body is empty.
func ingressFilterAnnotations(respValue string) map[string]string {
	return map[string]string{
		"haproxy.org/response-set-header":    reloadFreeRespHeader + " " + respValue,
		"haproxy.org/timeout-server":         "30s",
		"haproxy-haptic.org/compress-enable": "false",
	}
}

// applyIngressFilteredRoute applies an Ingress with the haproxytech filter
// annotations, carrying respValue in the response header so a test can tell the
// fresh route from a stale one.
func applyIngressFilteredRoute(
	ctx context.Context, t *testing.T, namespace, name, host string, backend BackendRef, respValue string,
) {
	t.Helper()
	manifest := fmt.Sprintf(`apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: %s
  namespace: %s
  annotations:
    haproxy.org/response-set-header: "%s %s"
    haproxy.org/timeout-server: "30s"
    haproxy-haptic.org/compress-enable: "false"
spec:
  ingressClassName: haptic
  rules:
    - host: "%s"
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: %s
                port:
                  number: %d
`, name, namespace, reloadFreeRespHeader, respValue, host, backend.Service, backend.Port)
	if err := kubectlApplyStdin(ctx, []byte(manifest)); err != nil {
		t.Fatalf("apply Ingress %s/%s: %v", namespace, name, err)
	}
}
