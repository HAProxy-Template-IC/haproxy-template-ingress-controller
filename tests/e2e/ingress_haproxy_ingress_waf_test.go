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
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

// TestIngressHaproxyIngressWafDeny exercises the deny-mode half of the
// `haproxy-ingress.github.io/waf` annotation path: an Ingress carrying
// `waf: modsecurity` (with the default `waf-mode: deny`) opts into the
// chart-default WAF dispatch on its host+path, and any request matching
// a SecRule is blocked with 403. Requests to the same path without the
// triggering input pass through to the backend.
//
// The trigger lives in the chart-level coraza directives (set by
// dev-values.yaml's `spoaHub.plugins.coraza.directives`), not in the
// per-Ingress annotation, because haproxy-ingress.github.io's
// `/waf` annotation is dispatch-scoped — it gates whether the WAF runs
// at all for the path, but doesn't carry rule text. The chart-level
// SecRule blocks any request whose `User-Agent` exactly equals
// `haptic-waf-block-probe`, so the test can flip an HTTP probe between
// "rule fires" and "rule does not fire" by toggling that header.
//
// The test also covers `waf-mode: detect` (shadow rollout): a second
// Ingress with `waf-mode: detect` still dispatches the WAF (so
// `txn.hub.coraza.*` vars are populated for observability), but the
// deny rule is skipped via the
// `!{ var(txn.coraza_mode) -m str detect }` predicate emitted by
// `frontend-spoe-filters-050-coraza` — traffic still passes even when
// the SecRule matches.
func TestIngressHaproxyIngressWafDeny(t *testing.T) {
	RequireVendorLibrary(t, "haproxyIngress")
	const (
		denyHost   = "ingress-haproxy-ingress-waf-deny.localdev.me"
		detectHost = "ingress-haproxy-ingress-waf-detect.localdev.me"
		triggerUA  = "haptic-waf-block-probe"
	)

	feature := features.New("Ingress: haproxy-ingress.github.io/waf opt-in dispatches Coraza, /waf-mode controls enforcement").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			backend := NewEchoServerBackend(ctx, t, client, ns)

			// Deny-mode Ingress: opts into the WAF, default mode (deny).
			NewIngress(ctx, t, client, ns, IngressSpec{
				Name:           "echo-deny",
				Host:           denyHost,
				BackendService: backend.Service,
				BackendPort:    backend.Port,
				Annotations: map[string]string{
					"haproxy-ingress.github.io/waf": "modsecurity",
				},
			})

			// Detect-mode Ingress: opts into the WAF in shadow mode.
			NewIngress(ctx, t, client, ns, IngressSpec{
				Name:           "echo-detect",
				Host:           detectHost,
				BackendService: backend.Service,
				BackendPort:    backend.Port,
				Annotations: map[string]string{
					"haproxy-ingress.github.io/waf":      "modsecurity",
					"haproxy-ingress.github.io/waf-mode": "detect",
				},
			})

			// Wait for the rendered haproxy-ingress-waf.map to land on
			// a haproxy pod containing both entries before running any
			// HTTP probe. Without this wait the requests can race the
			// apply and arrive while the map is still
			// empty (skip-WAF) — the test would silently see 200s for
			// the deny-mode probe and pass for the wrong reason.
			//
			// Map is keyed by `<ns>/<name>` (resource id), not host+path —
			// see the "Helm chart" subsection in CHANGELOG.md.
			waitForHaproxyIngressWafMap(ctx, t, []string{
				ns + "/echo-deny deny",
				ns + "/echo-detect detect",
			})

			return ctx
		}).
		Assess("deny mode: trigger UA is blocked with 403", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.New(t).GET(denyHost, "/").
				WithHeader("User-Agent", triggerUA).
				ExpectStatus(t, http.StatusForbidden)
			return ctx
		}).
		Assess("deny mode: non-trigger UA passes through to the backend", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.New(t).GET(denyHost, "/").
				WithHeader("User-Agent", "Mozilla/5.0 (haptic-e2e)").
				ExpectOK(t)
			return ctx
		}).
		Assess("detect mode: trigger UA passes through despite SecRule match (shadow rollout)", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.New(t).GET(detectHost, "/").
				WithHeader("User-Agent", triggerUA).
				ExpectOK(t)
			return ctx
		}).
		Assess("detect mode: non-trigger UA passes through (rule did not match)", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.New(t).GET(detectHost, "/").
				WithHeader("User-Agent", "Mozilla/5.0 (haptic-e2e)").
				ExpectOK(t)
			return ctx
		})

	testEnv.Test(t, feature.Feature())
}

// waitForHaproxyIngressWafMap blocks until the rendered
// haproxy-ingress-waf.map on a haproxy pod contains every supplied
// entry. Same shape as waitForSnippetOnPod (in
// ingress_modsecurity_snippet_test.go) but pointed at the per-path WAF
// map instead of the spoa-hub config TOML.
func waitForHaproxyIngressWafMap(ctx context.Context, t *testing.T, entries []string) {
	t.Helper()
	pod := firstHAProxyPodName(ctx, t)
	const path = "/etc/haproxy/maps/haproxy-ingress-waf.map"
	waitCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := testutil.WaitForCondition(waitCtx, testutil.DefaultWaitConfig(), func(c context.Context) (bool, error) {
		content, err := readFileFromHAProxyPod(c, pod, path)
		if err != nil {
			return false, err
		}
		for _, e := range entries {
			if !strings.Contains(content, e) {
				return false, nil
			}
		}
		return true, nil
	}); err != nil {
		t.Fatalf("timed out waiting for entries %v in %s on %s: %v", entries, path, pod, err)
	}
}
