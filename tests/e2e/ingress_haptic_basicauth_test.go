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

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/e2e-framework/klient"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

// TestHapticBasicAuth verifies that the haptic-annotations library's
// haproxy-haptic.org/auth-type=basic annotation gates the Ingress behind
// HTTP Basic auth, mirroring the haproxytech-library test
// (TestIngressBasicAuth) against haptic's canonical keys.
//
// Two checks:
//   - no credentials → 401
//   - admin:admin     → 200 (echo-server reached)
//
// The haptic fragment (50-auth-spoe.yaml) defaults auth-secret-type to
// "auth-file": the Secret carries a single `auth` data key holding an
// htpasswd file (one `username:hash` line each). The auth Secret is
// per-test (deleted with the namespace).
func TestHapticBasicAuth(t *testing.T) {
	t.Parallel()
	RunSimpleIngressTest(t, &SimpleIngressTest{
		Description: "Ingress: HAPTIC-native HTTP Basic auth",
		Host:        "ingress-haptic-basicauth.localdev.me",
		Annotations: map[string]string{
			"haproxy-haptic.org/auth-type":        "basic",
			"haproxy-haptic.org/auth-secret":      "echo-auth-secret",
			"haproxy-haptic.org/auth-realm":       "Echo-Server-Protected",
			"haproxy-haptic.org/auth-secret-type": "auth-file",
		},
		PreSetup: func(ctx context.Context, t *testing.T, client klient.Client, namespace string) {
			t.Helper()
			newBasicAuthSecret(ctx, t, client, namespace)
		},
		Assess: []SimpleIngressAssertion{
			{
				Name: "returns 401 without credentials",
				Check: func(t *testing.T, host string) {
					t.Helper()
					httpclient.New(t).GET(host, "/").ExpectStatus(t, http.StatusUnauthorized)
				},
			},
			{
				Name: "returns 200 with admin:admin credentials",
				Check: func(t *testing.T, host string) {
					t.Helper()
					resp := httpclient.New(t).GET(host, "/").WithBasicAuth("admin", "admin").ExpectOK(t)
					if resp.Echo == nil {
						t.Fatalf("expected echo-server JSON after auth, got status=%d", resp.Status)
					}
				},
			},
		},
	})
}

// newBasicAuthSecret creates the auth-file Secret both basic-auth tests use:
// one `username:hash` htpasswd line in the `auth` key. The bcrypt hash is for
// "admin" (admin/admin matches the dev-env secret); regenerate with
// `htpasswd -nbB admin admin | cut -d: -f2`.
func newBasicAuthSecret(ctx context.Context, t *testing.T, client klient.Client, namespace string) {
	t.Helper()
	adminBcrypt := "$2y$05$mN1WVk5Qnbg4QwdAdXbfz.8b3ceH6Q5KOVCKxR2IkNAfJgLi5pIKW"
	authSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "echo-auth-secret", Namespace: namespace},
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{"auth": []byte("admin:" + adminBcrypt + "\n")},
	}
	if err := client.Resources(namespace).Create(ctx, authSecret); err != nil {
		t.Fatalf("create auth secret: %v", err)
	}
}

// TestHapticBasicAuthRouteAddRemoveIsReloadFree proves a basic-auth route on an
// existing credentials Secret is dynamic: the challenge is a frontend rule fed
// by a per-route map, so the backend stays plain and a second route is added
// and removed at runtime. Serial like the other reload-free suites: a reload
// count is attributable only on a quiet fleet.
func TestHapticBasicAuthRouteAddRemoveIsReloadFree(t *testing.T) {
	const (
		anchorHost = "ingress-haptic-basicauth-rf-anchor.localdev.me"
		anchorName = "echo-haptic-basicauth-rf-anchor"
		cycleHost  = "ingress-haptic-basicauth-rf-cycle.localdev.me"
		cycleName  = "echo-haptic-basicauth-rf-cycle"
	)
	var (
		client klient.Client
		cs     kubernetes.Interface
		dyn    dynamic.Interface
		ns     string
		echo   BackendRef
	)
	feature := features.New("Ingress: HAPTIC-native basic-auth route add/remove is reload-free on 3.4").
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
			NewIngress(ctx, t, client, ns, basicAuthIngress(anchorName, anchorHost, echo))
			httpclient.New(t).GET(anchorHost, "/").WithBasicAuth("admin", "admin").ExpectOK(t)
			return ctx
		}).
		Assess("a second route on the same Secret is added and removed at runtime and, on 3.4, never reloads",
			func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
				t.Helper()
				hc := httpclient.New(t)
				waitFleetQuiescent(ctx, t, client, cs)
				before := captureReloadFingerprint(ctx, t, cs)

				// Created directly: NewIngress would register a forget-namespace
				// wait on this sub-test, which blocks while the anchor still exists.
				// The cycle deletes the route itself.
				cycle := buildIngress(ns, basicAuthIngress(cycleName, cycleHost, echo))
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
					assertReloadFree(t, before, after, "basic-auth route create+delete")
				} else {
					t.Logf("basic-auth route cycle: %.0f reloads on %s (dynamic backends need 3.4)",
						after.reloads-before.reloads, ChartHAProxyVersion)
				}
				return ctx
			}).
		Feature()
	testEnv.Test(t, feature)
}

// basicAuthIngress is the basic-auth route shape the anchor and the cycled
// route share, Secret and userlist included.
func basicAuthIngress(name, host string, echo BackendRef) *IngressSpec {
	return &IngressSpec{
		Name:           name,
		Host:           host,
		Path:           "/",
		BackendService: echo.Service,
		BackendPort:    echo.Port,
		Annotations: map[string]string{
			"haproxy-haptic.org/auth-type":        "basic",
			"haproxy-haptic.org/auth-secret":      "echo-auth-secret",
			"haproxy-haptic.org/auth-realm":       "Echo-Server-Protected",
			"haproxy-haptic.org/auth-secret-type": "auth-file",
		},
	}
}
