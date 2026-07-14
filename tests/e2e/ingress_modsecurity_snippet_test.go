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
	"bytes"
	"context"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

// TestIngressModSecuritySnippetEnforced is the "works as intended" test
// for ADR-0007's per-Ingress WAF rule path: a Coraza directive set via
// `nginx.ingress.kubernetes.io/modsecurity-snippet` on an Ingress lands
// in the rendered SPOA hub config, the hub reloads gracefully on the
// file-watch event (haproxy-spoa-hub MR1), and the rule is enforced at
// runtime.
//
// We exercise three distinct rule shapes against the same Ingress so
// the test surfaces both rule-emission AND rule-matching, and so a
// regression in any one HAProxy frontend dispatch path (URI predicate,
// header predicate, query-string predicate) shows up cleanly:
//
//  1. URI prefix match — `SecRule REQUEST_URI "@beginsWith /admin"`
//     blocks /admin*; everything else passes through to the backend.
//  2. Header match — `SecRule REQUEST_HEADERS:X-Block-Me "@streq yes"`
//     blocks requests carrying that exact header value.
//  3. Query-string match — `SecRule ARGS:debug "@streq dump"` blocks
//     requests with `?debug=dump`.
//
// All three rules share one Ingress so the rendered hub TOML carries
// all of them at once, which also pins the multi-rule append behaviour
// of features-spoa-hub's snippet inlining.
func TestIngressModSecuritySnippetEnforced(t *testing.T) {
	RequireVendorLibrary(t, "nginxIngress")
	const host = "ingress-modsec.localdev.me"

	feature := features.New("Ingress: nginx.ingress.kubernetes.io/modsecurity-snippet enforced").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			backend := NewEchoServerBackend(ctx, t, client, ns)

			// Three rules in one snippet — Coraza accepts multi-line directives,
			// each line is a SecRule. IDs are kept distinct so log output and
			// the rendered TOML are easy to grep.
			snippet := strings.Join([]string{
				`SecRule REQUEST_URI "@beginsWith /admin" "id:9101,phase:1,deny,status:403"`,
				`SecRule REQUEST_HEADERS:X-Block-Me "@streq yes" "id:9102,phase:1,deny,status:403"`,
				`SecRule ARGS:debug "@streq dump" "id:9103,phase:2,deny,status:403"`,
			}, "\n")

			NewIngress(ctx, t, client, ns, IngressSpec{
				Name:           "echo",
				Host:           host,
				BackendService: backend.Service,
				BackendPort:    backend.Port,
				Annotations: map[string]string{
					"nginx.ingress.kubernetes.io/modsecurity-snippet": snippet,
				},
			})

			// Wait for the rendered TOML to land on a haproxy pod with all
			// three rule-ids before any assertion runs the HTTP probe — if
			// we hit /admin before the hub reloaded, the request would
			// reach the backend and the test would race.
			waitForSnippetOnPod(ctx, t, []string{"id:9101", "id:9102", "id:9103"})

			return ctx
		}).
		Assess("GET /admin is blocked with 403 (URI prefix rule fires)", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.New(t).GET(host, "/admin").ExpectStatus(t, http.StatusForbidden)
			return ctx
		}).
		Assess("GET /api passes through to the backend (URI predicate did not match)", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.New(t).GET(host, "/api").ExpectOK(t)
			return ctx
		}).
		Assess("X-Block-Me: yes header is blocked with 403 (header rule fires)", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.New(t).GET(host, "/").
				WithHeader("X-Block-Me", "yes").
				ExpectStatus(t, http.StatusForbidden)
			return ctx
		}).
		Assess("X-Block-Me: no header passes through (header predicate did not match)", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.New(t).GET(host, "/").
				WithHeader("X-Block-Me", "no").
				ExpectOK(t)
			return ctx
		}).
		Assess("?debug=dump query-string is blocked with 403 (args rule fires)", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.New(t).GET(host, "/?debug=dump").ExpectStatus(t, http.StatusForbidden)
			return ctx
		}).
		Assess("?debug=stats query-string passes through (args predicate did not match)", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.New(t).GET(host, "/?debug=stats").ExpectOK(t)
			return ctx
		})

	testEnv.Test(t, feature.Feature())
}

// TestIngressModSecuritySnippetHotReload covers the dynamic side of the
// path: an Ingress's modsecurity-snippet annotation is updated, the
// controller re-renders, the dataplane API pushes, the hub reloads, and
// the new rule is enforced on subsequent requests — without restarting
// any pod.
//
// First rule blocks /v1, second rule blocks /v2 (annotation update). We
// verify /v1 transitions from 403 → 200 and /v2 transitions from 200 →
// 403 across the update.
func TestIngressModSecuritySnippetHotReload(t *testing.T) {
	RequireVendorLibrary(t, "nginxIngress")
	const host = "ingress-modsec-hot-reload.localdev.me"

	feature := features.New("Ingress: modsecurity-snippet hot-reload (annotation update)").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			backend := NewEchoServerBackend(ctx, t, client, ns)

			// Phase 1: /v1 blocked.
			NewIngress(ctx, t, client, ns, IngressSpec{
				Name:           "echo",
				Host:           host,
				BackendService: backend.Service,
				BackendPort:    backend.Port,
				Annotations: map[string]string{
					"nginx.ingress.kubernetes.io/modsecurity-snippet": `SecRule REQUEST_URI "@beginsWith /v1" "id:9201,phase:1,deny,status:403"`,
				},
			})
			waitForSnippetOnPod(ctx, t, []string{"id:9201"})
			httpclient.New(t).GET(host, "/v1").ExpectStatus(t, http.StatusForbidden)
			httpclient.New(t).GET(host, "/v2").ExpectOK(t)

			// Phase 2: update the annotation so /v2 is the blocked one.
			updateIngressAnnotation(ctx, t, ns, "echo",
				"nginx.ingress.kubernetes.io/modsecurity-snippet",
				`SecRule REQUEST_URI "@beginsWith /v2" "id:9202,phase:1,deny,status:403"`)
			waitForSnippetOnPod(ctx, t, []string{"id:9202"})
			waitForSnippetGoneFromPod(ctx, t, "id:9201")

			return ctx
		}).
		Assess("after annotation update, the new rule is enforced (hot reload)", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.New(t).GET(host, "/v2").ExpectStatus(t, http.StatusForbidden)
			httpclient.New(t).GET(host, "/v1").ExpectOK(t)
			return ctx
		})

	testEnv.Test(t, feature.Feature())
}

// waitForSnippetOnPod blocks until the rendered hub TOML on a haproxy
// pod contains every supplied marker string. Used by setup blocks that
// need to defer HTTP probes until the hub has reloaded with the new
// rules. Polls a single arbitrarily-chosen pod (every replica gets the
// same dataplane API push, so checking one is representative).
func waitForSnippetOnPod(ctx context.Context, t *testing.T, markers []string) {
	t.Helper()
	pod := firstHAProxyPodName(ctx, t)
	waitCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := testutil.WaitForCondition(waitCtx, testutil.DefaultWaitConfig(), func(c context.Context) (bool, error) {
		content, err := readFileFromHAProxyPod(c, pod, "/etc/haproxy/general/spoa-hub-config.toml")
		if err != nil {
			return false, err
		}
		for _, m := range markers {
			if !strings.Contains(content, m) {
				return false, nil
			}
		}
		return true, nil
	}); err != nil {
		t.Fatalf("timed out waiting for markers %v in /etc/haproxy/general/spoa-hub-config.toml on %s: %v", markers, pod, err)
	}
}

// waitForSnippetGoneFromPod is the inverse of waitForSnippetOnPod, used
// after an annotation update so the test confirms the OLD rule was
// removed (not just that the NEW rule was added).
func waitForSnippetGoneFromPod(ctx context.Context, t *testing.T, marker string) {
	t.Helper()
	pod := firstHAProxyPodName(ctx, t)
	waitCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := testutil.WaitForCondition(waitCtx, testutil.DefaultWaitConfig(), func(c context.Context) (bool, error) {
		content, err := readFileFromHAProxyPod(c, pod, "/etc/haproxy/general/spoa-hub-config.toml")
		if err != nil {
			return false, err
		}
		return !strings.Contains(content, marker), nil
	}); err != nil {
		t.Fatalf("timed out waiting for marker %q to disappear from /etc/haproxy/general/spoa-hub-config.toml on %s: %v", marker, pod, err)
	}
}

// updateIngressAnnotation overwrites a single annotation on an existing
// Ingress via `kubectl annotate --overwrite`. Mutating in place (rather
// than delete-and-recreate) is what the hot-reload path exercises in
// production: the operator typo-fixes a SecRule and expects HAPTIC to
// re-render + the hub to reload without a pod bounce.
func updateIngressAnnotation(ctx context.Context, t *testing.T, namespace, name, key, value string) {
	t.Helper()
	cmd := exec.CommandContext(ctx, "kubectl",
		"--kubeconfig", kubeconfigPath,
		"-n", namespace,
		"annotate", "ingress", name,
		"--overwrite",
		key+"="+value,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("kubectl annotate ingress %s/%s: %v\nstderr: %s", namespace, name, err, stderr.String())
	}
}
