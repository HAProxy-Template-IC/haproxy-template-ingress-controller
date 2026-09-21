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

// TestHapticBackendTLS covers the HAPTIC-native backend-TLS annotation family
// (haproxy-haptic.org/backend-*) end-to-end. It is the haptic-prefixed
// counterpart of TestIngressBackendMTLS (haproxy.org/server-*) and
// TestIngressBackendSSL / TestIngressServerProtoH2 (haproxy.org/server-ssl,
// server-proto): all prove HAProxy speaks TLS to the upstream, verifying and
// presenting certificates.
//
// The fixture is NewHAProxyMTLSBackend — a TLS-terminating upstream configured
// with `verify required` against a private CA. Reaching it therefore forces the
// full backend-TLS leg to succeed:
//   - haproxy-haptic.org/backend-protocol: https  → TLS to the upstream (h1-ssl)
//   - haproxy-haptic.org/backend-verify:   on     → verify the upstream cert
//     (fail-closed without a CA)
//   - haproxy-haptic.org/backend-ca-secret        → ca.crt used to verify it
//   - haproxy-haptic.org/backend-crt-secret       → client cert+key presented
//     to the upstream (mTLS)
//   - haproxy-haptic.org/backend-sni:      host   → forward Host as SNI
//     (sni req.hdr(host))
//
// If any one of these mis-wires, the upstream's `verify required` rejects the
// connection and the request fails — a 200 with echo JSON proves all five
// annotations combined to establish a verified mTLS connection to the backend.
//
// The mTLS fixture's TLS frontend advertises no `alpn h2`, so this test drives
// the h1-ssl (`https`) protocol; the h2/grpcs variants of backend-protocol
// share the identical render path (both add `proto h2` on top of the same TLS
// flags) and are covered by the chart's render-time validationTests.
func TestHapticBackendTLS(t *testing.T) {
	t.Parallel()
	host := "ingress-haptic-backendtls.localdev.me"

	feature := features.New("Ingress: haptic backend-TLS (backend-protocol + verify + ca/crt-secret + sni)").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			t.Helper()
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			echo := NewEchoServerBackend(ctx, t, client, ns)
			mtls := NewHAProxyMTLSBackend(ctx, t, client, ns, echo, host)

			NewIngress(ctx, t, client, ns, backendTLSIngress("echo-haptic-backendtls", host, "host", mtls))
			return ctx
		}).
		Assess("haptic backend-* annotations establish a verified mTLS connection to the upstream → 200 from echo",
			func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
				t.Helper()
				resp := httpclient.New(t).GET(host, "/").ExpectOK(t)
				if resp.Echo == nil {
					t.Fatalf("expected echo-server JSON via verified backend TLS, got %d bytes", len(resp.Body))
				}
				return ctx
			}).
		Feature()
	testEnv.Test(t, feature)
}

// TestHapticBackendTLSRouteAddRemoveIsReloadFree proves a client-cert backend
// is dynamic: the certificate is referenced bare under crt-base, which add
// server resolves against the runtime store, so a second route on the same
// certificate is added and removed at runtime. Serial like the other
// reload-free suites: a reload count is attributable only on a quiet fleet.
// Both routes send the anchor host as SNI, the name the fixture's upstream
// certificate is issued for.
func TestHapticBackendTLSRouteAddRemoveIsReloadFree(t *testing.T) {
	const (
		anchorHost = "ingress-haptic-backendtls-rf-anchor.localdev.me"
		anchorName = "echo-haptic-backendtls-rf-anchor"
		cycleHost  = "ingress-haptic-backendtls-rf-cycle.localdev.me"
		cycleName  = "echo-haptic-backendtls-rf-cycle"
	)
	var (
		client klient.Client
		cs     kubernetes.Interface
		dyn    dynamic.Interface
		ns     string
		mtls   HAProxyTLSBackend
	)
	feature := features.New("Ingress: haptic backend-TLS client-cert route add/remove is reload-free on 3.4").
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
			echo := NewEchoServerBackend(ctx, t, client, ns)
			mtls = NewHAProxyMTLSBackend(ctx, t, client, ns, echo, anchorHost)
			NewIngress(ctx, t, client, ns, backendTLSIngress(anchorName, anchorHost, anchorHost, mtls))
			httpclient.New(t).GET(anchorHost, "/").ExpectOK(t)
			return ctx
		}).
		Assess("a second client-cert route is added and removed at runtime and, on 3.4, never reloads",
			func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
				t.Helper()
				hc := httpclient.New(t)
				waitFleetQuiescent(ctx, t, client, cs)
				before := captureReloadFingerprint(ctx, t, cs)

				// Created directly: NewIngress would register a forget-namespace
				// wait on this sub-test, which blocks while the anchor still exists.
				// The cycle deletes the route itself.
				cycle := buildIngress(ns, backendTLSIngress(cycleName, cycleHost, anchorHost, mtls))
				if err := client.Resources(ns).Create(ctx, cycle); err != nil {
					t.Fatalf("create Ingress %s/%s: %v", ns, cycleName, err)
				}
				reloadFreeReaction(ctx, t, cs, fmt.Sprintf("%s/ answers 200 through the verified mTLS upstream", cycleHost),
					func(ctx context.Context) (bool, error) {
						resp, err := hc.GET(cycleHost, "/").Do(ctx)
						if err != nil {
							return false, err
						}
						if resp.Status != 200 {
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
					assertReloadFree(t, before, after, "client-cert route create+delete")
				} else {
					t.Logf("client-cert route cycle: %.0f reloads on %s (dynamic backends need 3.4)",
						after.reloads-before.reloads, ChartHAProxyVersion)
				}
				return ctx
			}).
		Feature()
	testEnv.Test(t, feature)
}

// backendTLSIngress is the verified-mTLS route shape; sni is the backend-sni
// annotation value ("host", or a literal name the upstream certificate covers).
func backendTLSIngress(name, host, sni string, mtls HAProxyTLSBackend) *IngressSpec {
	return &IngressSpec{
		Name:           name,
		Host:           host,
		Path:           "/",
		BackendService: mtls.HTTPS.Service,
		BackendPort:    mtls.HTTPS.Port,
		Annotations: map[string]string{
			"haproxy-haptic.org/backend-protocol":   "https",
			"haproxy-haptic.org/backend-verify":     "on",
			"haproxy-haptic.org/backend-ca-secret":  mtls.CASecretName,
			"haproxy-haptic.org/backend-crt-secret": mtls.ClientCertSecretName,
			"haproxy-haptic.org/backend-sni":        sni,
		},
	}
}
