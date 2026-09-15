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

// TestHapticSatisfyAnyRouteAddRemoveIsReloadFree proves a satisfy=any route is
// dynamic: the combined address-or-authentication gate is a frontend rule per
// distinct (userlist, realm) pair selected by a per-route map row, so a second
// route on the same Secret and realm is added and removed at runtime.
//
// It also pins the OR semantics on the wire in both directions, which is the
// part a map row could get wrong without any rule looking wrong: a route whose
// allowlist excludes the client must challenge, and a route whose allowlist
// covers it must not.
//
// Serial like the other reload-free suites: a reload count is attributable only
// on a quiet fleet.
func TestHapticSatisfyAnyRouteAddRemoveIsReloadFree(t *testing.T) {
	const (
		anchorHost = "ingress-haptic-satisfy-rf-anchor.localdev.me"
		anchorName = "echo-haptic-satisfy-rf-anchor"
		exemptHost = "ingress-haptic-satisfy-rf-exempt.localdev.me"
		exemptName = "echo-haptic-satisfy-rf-exempt"
		cycleHost  = "ingress-haptic-satisfy-rf-cycle.localdev.me"
		cycleName  = "echo-haptic-satisfy-rf-cycle"
		realm      = "Echo-Server-Protected"
	)
	var (
		client klient.Client
		cs     kubernetes.Interface
		dyn    dynamic.Interface
		ns     string
		echo   BackendRef
	)
	// allowlist is the only difference between the routes: a range that cannot
	// contain the client's address challenges, and one that covers every address
	// exempts it. Same Secret and realm throughout, so all three share one pair.
	route := func(name, host, allowlist string) *IngressSpec {
		return &IngressSpec{
			Name:           name,
			Host:           host,
			Path:           "/",
			BackendService: echo.Service,
			BackendPort:    echo.Port,
			Annotations: map[string]string{
				"haproxy-haptic.org/auth-type":              "basic",
				"haproxy-haptic.org/auth-secret":            "echo-auth-secret",
				"haproxy-haptic.org/auth-realm":             realm,
				"haproxy-haptic.org/auth-secret-type":       "auth-file",
				"haproxy-haptic.org/allowlist-source-range": allowlist,
				"haproxy-haptic.org/satisfy":                "any",
			},
		}
	}
	feature := features.New("Ingress: HAPTIC-native satisfy=any route add/remove is reload-free on 3.4").
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
			newBasicAuthSecret(ctx, t, client, ns)
			NewIngress(ctx, t, client, ns, route(anchorName, anchorHost, "10.99.0.0/16"))
			NewIngress(ctx, t, client, ns, route(exemptName, exemptHost, "0.0.0.0/0"))
			httpclient.New(t).GET(anchorHost, "/").WithBasicAuth("admin", "admin").ExpectOK(t)
			return ctx
		}).
		Assess("the OR gate challenges a non-allowlisted client and exempts an allowlisted one",
			func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
				t.Helper()
				hc := httpclient.New(t)
				hc.GET(anchorHost, "/").ExpectStatus(t, http.StatusUnauthorized)
				hc.GET(anchorHost, "/").WithBasicAuth("admin", "admin").ExpectOK(t)
				// Same Secret, same realm, same rule — only the allowlist differs,
				// and it covers the client, so no credentials are asked for.
				hc.GET(exemptHost, "/").ExpectOK(t)
				return ctx
			}).
		Assess("a second satisfy=any route on the same Secret and realm is added and removed at runtime and, on 3.4, never reloads",
			func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
				t.Helper()
				hc := httpclient.New(t)
				waitFleetQuiescent(ctx, t, client, cs)
				before := captureReloadFingerprint(ctx, t, cs)

				// The cycled route reuses the anchor's allowlist as well as its
				// pair, so neither the challenge rule nor the address cover is new.
				cycle := buildIngress(ns, route(cycleName, cycleHost, "10.99.0.0/16"))
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
						return true, nil
					})
				// The runtime-added route is gated too: its map row, not a backend
				// rule, puts it behind the challenge.
				hc.GET(cycleHost, "/").ExpectStatus(t, http.StatusUnauthorized)
				deleteRouteByName(ctx, t, dyn, ingressGVR, ns, cycleName)
				waitForRouteGone(ctx, t, cs, hc, cycleHost, "/")

				after := captureReloadFingerprint(ctx, t, cs)
				if dynamicBackendsSupported() {
					assertReloadFree(t, before, after, "satisfy=any route create+delete")
				} else {
					t.Logf("satisfy=any route cycle: %.0f reloads on %s (dynamic backends need 3.4)",
						after.reloads-before.reloads, ChartHAProxyVersion)
				}
				return ctx
			}).
		Feature()
	testEnv.Test(t, feature)
}
