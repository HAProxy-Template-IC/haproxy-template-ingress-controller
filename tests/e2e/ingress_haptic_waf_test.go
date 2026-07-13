// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"testing"

	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

// TestHapticWAF exercises the haptic-native Coraza WAF annotation path,
// the best-of-breed superset that merges haproxy-ingress.github.io's
// `waf` / `waf-mode` opt-in with nginx's per-app `modsecurity-snippet`
// under the `haproxy-haptic.org/` prefix.
//
// A single Ingress carries all three canonical keys at once:
//
//   - haproxy-haptic.org/waf: modsecurity  → opts the host into the WAF
//     dispatch (haproxy-ingress-waf.map, keyed by <ns>/<name>).
//   - haproxy-haptic.org/waf-mode: deny    → resolves the enforcement mode
//     to deny (block), pinning the `<ns>/<name> deny` map row.
//   - haproxy-haptic.org/modsecurity-snippet → carries the SecRule text
//     that defines what "malicious" means (scanned into the spoa-hub
//     config TOML and coraza-app.map).
//
// The SecRule mirrors the modsecurity-snippet vendor trigger — a
// `@beginsWith /admin` URI rule that denies with 403. With waf-mode deny
// the rule is enforced: a request to /admin is blocked (403) while a
// benign request to / passes through to the backend.
//
// All three keys share one Ingress so the test pins that the haptic
// library emits both the enforcement-mode map row (from waf/waf-mode) and
// the per-app SecRule (from modsecurity-snippet) for the same resource,
// and that the two cooperate at runtime through the coraza SPOE dispatch.
func TestHapticWAF(t *testing.T) {
	t.Parallel()

	const (
		host   = "ingress-haptic-waf.localdev.me"
		ruleID = "id:9301"
	)

	// @beginsWith /admin, deny 403 — the same shape the nginx
	// modsecurity-snippet vendor test uses, carried here via the haptic
	// canonical modsecurity-snippet key.
	snippet := `SecRule REQUEST_URI "@beginsWith /admin" "` + ruleID + `,phase:1,deny,status:403"`

	feature := features.New("Ingress: haproxy-haptic.org/waf + waf-mode + modsecurity-snippet enforce Coraza WAF").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			backend := NewEchoServerBackend(ctx, t, client, ns)

			NewIngress(ctx, t, client, ns, IngressSpec{
				Name:           "echo",
				Host:           host,
				BackendService: backend.Service,
				BackendPort:    backend.Port,
				Annotations: map[string]string{
					"haproxy-haptic.org/waf":                 "modsecurity",
					"haproxy-haptic.org/waf-mode":            "deny",
					"haproxy-haptic.org/modsecurity-snippet": snippet,
				},
			})

			// Defer HTTP probes until BOTH the enforcement-mode map row
			// (from waf/waf-mode) and the SecRule (from modsecurity-snippet)
			// have landed on a haproxy pod. Without these waits the probe can
			// race the dataplane push and see a 200 for the wrong reason —
			// either the WAF not yet dispatched, or the rule not yet loaded.
			waitForHaproxyIngressWafMap(ctx, t, []string{ns + "/echo deny"})
			waitForSnippetOnPod(ctx, t, []string{ruleID})

			return ctx
		}).
		Assess("malicious request to /admin is blocked with 403 (SecRule fires, waf-mode deny enforces)", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.New(t).GET(host, "/admin").ExpectStatus(t, http.StatusForbidden)
			return ctx
		}).
		Assess("benign request to / passes through to the backend (SecRule did not match)", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.New(t).GET(host, "/").ExpectOK(t)
			return ctx
		})

	testEnv.Test(t, feature.Feature())
}
