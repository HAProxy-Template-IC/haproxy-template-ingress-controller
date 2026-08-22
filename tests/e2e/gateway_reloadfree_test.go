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
	"sort"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/e2e-framework/klient"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

// reloadFreeCycles is how many create/delete rounds each reload-free suite
// runs. The first round is allowed to reload (it introduces the profile and the
// per-header static lines the map-driven filters read); every round after it
// must be reload-free on 3.4.
const reloadFreeCycles = 6

// TestGatewayRouteAddRemoveIsReloadFree is workstream D's gateway proof for the
// dynamic-backend plan: adding and removing an HTTPRoute whose backend is
// profile-shaped and whose filters are map-driven changes no structural config,
// so on HAProxy 3.4 the fleet never reloads for it. An anchor route with the
// identical shape stays up for the whole test, holding the shared profile and
// the filters' static lines alive, so only per-route map entries and the
// backend itself come and go — exactly the runtime lane deployplan composes.
//
// On 3.0–3.3 there is no `add backend`, so each add/remove reloads; the suite
// then only asserts the route works and the reload count stays bounded.
func TestGatewayRouteAddRemoveIsReloadFree(t *testing.T) {
	const (
		gatewayName = "reloadfree"
		anchorHost  = "gw-anchor.localdev.me"
		cycleHost   = "gw-cycle.localdev.me"
		cycleRoute  = "gw-cycle-route"
	)
	respHeader := reloadFreeRespHeader

	var (
		client    klient.Client
		cs        kubernetes.Interface
		dyn       dynamic.Interface
		namespace string
		cycleSvc  BackendRef
		fwd       GatewayForward
	)

	feature := features.New("Gateway route add/remove is reload-free on 3.4").
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
			NewGateway(ctx, t, namespace, gatewayName)

			// The anchor carries the exact shape the cycled route does — same
			// filters, same timeouts — so the profile and every static filter
			// line exist for the whole test and only the cycled backend and its
			// map entries move.
			applyGatewayFilteredRoute(ctx, t, namespace, "gw-anchor-route", gatewayName, anchorHost, anchorSvc, "rf-anchor")
			fwd = ForwardGateway(ctx, t, namespace, gatewayName, 80)
			waitForRouteServing(ctx, t, httpclient.ForForwarded(t, fwd.HTTPPort, 0), anchorHost, "/app/ping", respHeader, "rf-anchor")
			return ctx
		}).
		Assess("each cycle serves the route and, on 3.4, never reloads", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			dynamicBE := dynamicBackendsSupported()
			http := httpclient.ForForwarded(t, fwd.HTTPPort, 0)
			// Drain the anchor's first-appearance reload and any sibling-test
			// teardown before measuring, so a cycle is charged only its own work.
			waitFleetQuiescent(ctx, t, cs)
			startReloads := captureReloadFingerprint(ctx, t, cs)
			var latencies []time.Duration

			for cycle := 1; cycle <= reloadFreeCycles; cycle++ {
				want := fmt.Sprintf("rf-%d", cycle)
				before := captureReloadFingerprint(ctx, t, cs)
				divBefore := mapDivergenceTotal(ctx, t, cs)

				applyGatewayFilteredRoute(ctx, t, namespace, cycleRoute, gatewayName, cycleHost, cycleSvc, want)
				latency := waitForRouteServing(ctx, t, http, cycleHost, "/app/ping", respHeader, want)
				latencies = append(latencies, latency)

				// The response-header value the route just set must be in the
				// runtime map, which is what a reload-free filter update means.
				entries := mapEntriesFrom(showMap(ctx, t, cs, "maps/gw-reshdr.map"))
				assertMapHasValue(t, entries, want, "maps/gw-reshdr.map")

				deleteRouteByName(ctx, t, dyn, httpRouteGVR, namespace, cycleRoute)
				waitForRouteGone(ctx, t, http, cycleHost, "/app/ping")

				after := captureReloadFingerprint(ctx, t, cs)
				if dynamicBE {
					assertReloadFree(t, before, after, fmt.Sprintf("gateway cycle %d create+delete", cycle))
					if div := mapDivergenceTotal(ctx, t, cs); div != divBefore {
						t.Fatalf("gateway cycle %d: map divergence advanced %.0f→%.0f", cycle, divBefore, div)
					}
				}
				t.Logf("gateway cycle %d: create→200 in %s", cycle, latency.Round(time.Millisecond))
			}

			endReloads := captureReloadFingerprint(ctx, t, cs)
			reloads := endReloads.reloads - startReloads.reloads
			t.Logf("gateway reload-free suite: p50 create→200 %s, %.0f reloads over %d cycles (dynamic backends: %t)",
				median(latencies).Round(time.Millisecond), reloads, reloadFreeCycles, dynamicBE)
			if !dynamicBE && reloads > float64(2*reloadFreeCycles) {
				t.Fatalf("on %s a route add/remove reloads, but %.0f reloads over %d cycles exceeds the 2N bound",
					ChartHAProxyVersion, reloads, reloadFreeCycles)
			}
			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}

// applyGatewayFilteredRoute applies an HTTPRoute that exercises the map-driven
// filter surface 1a moved out of the backend section: request- and
// response-header modifiers, a prefix URL rewrite, a per-rule request timeout,
// and a redirect on a second path. The response header carries respValue so a
// test can tell the fresh route from a stale one.
func applyGatewayFilteredRoute(
	ctx context.Context, t *testing.T, namespace, name, gateway, host string, backend BackendRef, respValue string,
) {
	t.Helper()
	manifest := fmt.Sprintf(`apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: %s
  namespace: %s
spec:
  parentRefs:
    - name: %s
  hostnames:
    - "%s"
  rules:
    - matches:
        - path: {type: PathPrefix, value: /app}
      filters:
        - type: RequestHeaderModifier
          requestHeaderModifier:
            set:
              - {name: X-Rf-Req, value: rf-req}
        - type: ResponseHeaderModifier
          responseHeaderModifier:
            set:
              - {name: X-Rf-Resp, value: %s}
        - type: URLRewrite
          urlRewrite:
            path: {type: ReplacePrefixMatch, replacePrefixMatch: /}
      timeouts:
        request: 5s
      backendRefs:
        - name: %s
          port: %d
    - matches:
        - path: {type: PathPrefix, value: /go}
      filters:
        - type: RequestRedirect
          requestRedirect:
            statusCode: 301
            path: {type: ReplacePrefixMatch, replacePrefixMatch: /moved}
`, name, namespace, gateway, host, respValue, backend.Service, backend.Port)
	if err := kubectlApplyStdin(ctx, []byte(manifest)); err != nil {
		t.Fatalf("apply HTTPRoute %s/%s: %v", namespace, name, err)
	}
}

// waitForRouteServing polls until the host+path answers 200 with the expected
// response-header value and returns how long that took, which is the
// create→first-200 latency the plan budgets.
func waitForRouteServing(
	ctx context.Context, t *testing.T, client *httpclient.Client, host, path, header, want string,
) time.Duration {
	t.Helper()
	start := time.Now()
	err := testutil.WaitForConditionWithDescription(ctx, testutil.WaitConfig{
		InitialInterval: 100 * time.Millisecond,
		MaxInterval:     time.Second,
		Timeout:         convergenceTimeout(),
		Multiplier:      1.5,
	}, fmt.Sprintf("%s%s answers 200 with %s: %s", host, path, header, want), func(ctx context.Context) (bool, error) {
		resp, err := client.GET(host, path).Do(ctx)
		if err != nil {
			return false, err
		}
		if resp.Status != 200 {
			return false, fmt.Errorf("status %d", resp.Status)
		}
		if got := resp.Header.Get(header); got != want {
			return false, fmt.Errorf("%s is %q, want %q", header, got, want)
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("route never served %s%s with %s=%s: %v", host, path, header, want, err)
	}
	return time.Since(start)
}

// waitForRouteGone polls until the host+path stops answering 200, proving the
// route (and, on 3.4, its dynamic backend) is retired. A non-200 answer is the
// "gone" signal (delete → 404); a transport error is treated as not-yet-gone so
// a flaky port-forward never reads as a passed teardown.
func waitForRouteGone(ctx context.Context, t *testing.T, client *httpclient.Client, host, path string) {
	t.Helper()
	err := testutil.WaitForConditionWithDescription(ctx, testutil.WaitConfig{
		InitialInterval: 100 * time.Millisecond,
		MaxInterval:     time.Second,
		Timeout:         convergenceTimeout(),
		Multiplier:      1.5,
	}, fmt.Sprintf("%s%s stops answering 200", host, path), func(ctx context.Context) (bool, error) {
		resp, err := client.GET(host, path).Do(ctx)
		if err != nil {
			return false, err
		}
		if resp.Status == 200 {
			return false, fmt.Errorf("still 200")
		}
		return true, nil
	})
	if err != nil {
		t.Fatalf("route %s%s never stopped serving: %v", host, path, err)
	}
}

// deleteRouteByName removes one namespaced object through the dynamic client,
// tolerating a not-found (a prior cycle's delete that already landed).
func deleteRouteByName(
	ctx context.Context, t *testing.T, dyn dynamic.Interface, gvr schema.GroupVersionResource, namespace, name string,
) {
	t.Helper()
	err := dyn.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		t.Fatalf("delete %s %s/%s: %v", gvr.Resource, namespace, name, err)
	}
}

// convergenceTimeout bounds a single reload-free reaction. It is deliberately
// generous: the shared e2e suite runs these against a contended CI runner, and
// the plan's latency budget is asserted only as a logged p50, never as a
// per-request gate that a busy runner could trip.
func convergenceTimeout() time.Duration { return 30 * time.Second }

// mapEntriesFrom parses `show map` output into key→value, dropping the entry id
// HAProxy prefixes. Values keep every byte after the first space.
func mapEntriesFrom(output string) map[string]string {
	entries := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if !strings.HasPrefix(line, "0x") {
			continue
		}
		fields := strings.SplitN(line, " ", 3)
		switch len(fields) {
		case 2:
			entries[fields[1]] = ""
		case 3:
			entries[fields[1]] = fields[2]
		}
	}
	return entries
}

// assertMapHasValue fails when no entry carries want. The map stores the value
// URL-encoded (space→+), so the check is a substring of some value, which holds
// for the plain ASCII markers these tests use.
func assertMapHasValue(t *testing.T, entries map[string]string, want, mapPath string) {
	t.Helper()
	for _, v := range entries {
		if strings.Contains(v, want) {
			return
		}
	}
	t.Fatalf("%s has no entry carrying %q; entries: %v", mapPath, want, entries)
}

// median is the middle create→200 latency, the plan's reported figure.
func median(ds []time.Duration) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), ds...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[len(sorted)/2]
}
