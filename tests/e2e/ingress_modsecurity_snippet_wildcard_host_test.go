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

// TestIngressModSecuritySnippetWildcardHost is the regression test for
// the wildcard-host path through the per-app WAF dispatch. An Ingress
// declared with `host: '*.localdev.me'` must enforce its
// modsec-snippet on requests to any concrete subdomain (e.g.
// `wild-modsec-test.localdev.me`).
//
// Background. The chart's host.map (base.yaml) strips `*` from
// wildcard Ingress hosts (`*.example.com` → `.example.com`) so
// HAProxy's regsub-based fallback in frontend-routing-logic can match
// concrete subdomains via the suffix form. Without matching
// normalisation in the per-path maps, the runtime lookup
// `var(txn.host_match),concat(,txn.path,),map_beg(<...>.map)` would
// produce a key `.example.com<path>` while the map was populated with
// `*.example.com<path>` — no match, no dispatch, no enforcement.
//
// All `<host><path>`-keyed map populations now go through
// `MapKeyForHost` (charts/haptic/libraries/ingress.yaml's
// util-ingress-host-key snippet) so wildcard hosts produce the same
// suffix-form key as host.map. This test pins that contract by
// exercising it end-to-end through the per-app WAF path.
func TestIngressModSecuritySnippetWildcardHost(t *testing.T) {
	RequireVendorLibrary(t, "nginxIngress")
	const (
		wildcardHost = "*.localdev.me"
		concreteHost = "wild-modsec-test.localdev.me"
		ruleID       = "id:9301"
	)

	feature := features.New("Ingress: nginx.ingress.kubernetes.io/modsecurity-snippet on a wildcard host enforces on concrete subdomains").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			backend := NewEchoServerBackend(ctx, t, client, ns)

			NewIngress(ctx, t, client, ns, IngressSpec{
				Name:           "echo-wildcard",
				Host:           wildcardHost,
				BackendService: backend.Service,
				BackendPort:    backend.Port,
				Annotations: map[string]string{
					"nginx.ingress.kubernetes.io/modsecurity-snippet": `SecRule REQUEST_HEADERS:X-Wild-Block "@streq yes" "id:9301,phase:1,deny,status:403"`,
				},
			})
			waitForSnippetOnPod(ctx, t, []string{ruleID})
			return ctx
		}).
		Assess("X-Wild-Block: yes on a concrete subdomain matching the wildcard is blocked with 403", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.New(t).GET(concreteHost, "/").
				WithHeader("X-Wild-Block", "yes").
				ExpectStatus(t, http.StatusForbidden)
			return ctx
		}).
		Assess("X-Wild-Block: no on a concrete subdomain passes through to the backend", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.New(t).GET(concreteHost, "/").
				WithHeader("X-Wild-Block", "no").
				ExpectOK(t)
			return ctx
		})

	testEnv.Test(t, feature.Feature())
}
