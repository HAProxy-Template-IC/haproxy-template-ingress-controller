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

// These tests pin the auth-url request-time enforcement contract for
// scenarios that today silently bypass authentication: subpaths of a
// Prefix-typed ingress, requests to concrete subdomains of a wildcard
// host, and requests matched by a haproxy-ingress regex path.
//
// Bug shape. The auth-* feature maps (auth-url.map and friends) are
// keyed on `<host_match><path>` (the literal ingress path string) and
// consumed by HAProxy via plain `map(...)` — exact-match lookup. The
// runtime lookup key is `<host_match><actual_request_path>`. So:
//
//   - Prefix path `/api`, request `/api/users` → lookup key
//     `<host>/api/users` does not equal map key `<host>/api`. Auth
//     never fires; the request reaches the backend unauthenticated.
//   - Regex path `/api/v[0-9]+/.*`, request `/api/v2/users` → map key
//     contains literal regex metacharacters; exact-match never hits.
//   - Wildcard host `*.example.com` + Prefix `/`, request to
//     `api.example.com/users` → host normalisation works (both render
//     and regsub-fallback collapse to `.example.com`) but the path
//     dimension of the bug bites again.
//
// Each test below points auth-url at the shared auth-server's `/deny`
// endpoint (returns 401). With the bug, silent auth-skip means the
// request reaches the echo-server backend and gets 200. With the fix
// (auth-url.map keyed by `<ns>/<name>`, consumed via
// `var(txn.resource_id),map(...)`), the auth-check fires and the
// request is denied with 401. The expected status is therefore 401;
// red→green.

// TestIngressAuthURLPrefixSubpath fails today because auth-url.map's
// key for a Prefix-typed ingress is the literal `<host>/api` ingress
// path, while the lookup key for the request `/api/users` is
// `<host>/api/users`. Exact-match map() can never hit. After the fix,
// keying by `<ns>/<name>` makes the path dimension irrelevant.
func TestIngressAuthURLPrefixSubpath(t *testing.T) {
	RequireVendorLibrary(t, "nginxIngress")
	const (
		host    = "auth-prefix-subpath.localdev.me"
		subpath = "/api/users"
	)

	feature := features.New("Ingress: auth-url must fire on subpath of Prefix path").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			backend := NewEchoServerBackend(ctx, t, client, ns)

			NewIngress(ctx, t, client, ns, IngressSpec{
				Name:           "echo-prefix",
				Host:           host,
				Path:           "/api",
				BackendService: backend.Service,
				BackendPort:    backend.Port,
				Annotations: map[string]string{
					"nginx.ingress.kubernetes.io/auth-url": denyAuthURL(),
				},
			})
			return ctx
		}).
		Assess("subpath request denied by auth-server returns 401", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.New(t).GET(host, subpath).ExpectStatus(t, http.StatusUnauthorized)
			return ctx
		})

	testEnv.Test(t, feature.Feature())
}

// TestIngressAuthURLWildcardHostSubpath combines the wildcard-host and
// prefix-subpath bug shapes. The wildcard host already works for the
// routing layer thanks to MapKeyForHost + regsub fallback; the auth-url
// map is broken on the same axis as the prefix-subpath case. After the
// fix, the test should pass because the route's owning resource id is
// stable regardless of which concrete subdomain the request used.
func TestIngressAuthURLWildcardHostSubpath(t *testing.T) {
	RequireVendorLibrary(t, "nginxIngress")
	const (
		wildcardHost = "*.auth-wild.localdev.me"
		concreteHost = "api.auth-wild.localdev.me"
		subpath      = "/users"
	)

	feature := features.New("Ingress: auth-url must fire on subpath of wildcard host").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			backend := NewEchoServerBackend(ctx, t, client, ns)

			NewIngress(ctx, t, client, ns, IngressSpec{
				Name:           "echo-wild",
				Host:           wildcardHost,
				Path:           "/",
				BackendService: backend.Service,
				BackendPort:    backend.Port,
				Annotations: map[string]string{
					"nginx.ingress.kubernetes.io/auth-url": denyAuthURL(),
				},
			})
			return ctx
		}).
		Assess("concrete subdomain subpath denied by auth-server returns 401", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.New(t).GET(concreteHost, subpath).ExpectStatus(t, http.StatusUnauthorized)
			return ctx
		})

	testEnv.Test(t, feature.Feature())
}

// TestIngressAuthURLRegexPath uses the haproxy-ingress regex-path
// flavour: pathType=ImplementationSpecific plus the
// haproxy-ingress.github.io/path-type=regex annotation routes the path
// into path-regex.map (consumed via map_reg()). auth-url.map is keyed
// by `<ns>/<name>` (resource id), so the auth lookup is decoupled from
// the path entirely — wildcard hosts, regex paths, prefix subpaths all
// resolve to the same resource_id and the auth-url lookup hits.
func TestIngressAuthURLRegexPath(t *testing.T) {
	// This test needs both vendor libraries at once: the
	// nginx.ingress.kubernetes.io/auth-url annotation (nginxIngress) to make
	// the auth check fire, and the haproxy-ingress.github.io/path-type=regex
	// annotation (haproxyIngress) to route the request via the regex path.
	// Under single-vendor sharding no shard enables both, so gating on each
	// makes it skip unless both are present rather than fail with one off.
	RequireVendorLibrary(t, "nginxIngress")
	RequireVendorLibrary(t, "haproxyIngress")
	const (
		host        = "auth-regex.localdev.me"
		regexPath   = "/api/v[0-9]+/.*"
		requestPath = "/api/v2/users"
	)

	feature := features.New("Ingress: auth-url must fire on haproxy-ingress regex path").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			backend := NewEchoServerBackend(ctx, t, client, ns)

			NewIngress(ctx, t, client, ns, IngressSpec{
				Name:           "echo-regex",
				Host:           host,
				Path:           regexPath,
				PathType:       "ImplementationSpecific",
				BackendService: backend.Service,
				BackendPort:    backend.Port,
				Annotations: map[string]string{
					"nginx.ingress.kubernetes.io/auth-url": denyAuthURL(),
					"haproxy-ingress.github.io/path-type":  "regex",
				},
			})
			return ctx
		}).
		Assess("regex-matched request denied by auth-server returns 401", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.New(t).GET(host, requestPath).ExpectStatus(t, http.StatusUnauthorized)
			return ctx
		})

	testEnv.Test(t, feature.Feature())
}

// denyAuthURL is the in-cluster URL of the shared auth-server's /deny
// endpoint. auth-server returns 401 for paths under /deny. Used by all
// three red→green tests to make silent auth-skip observable: with the
// bug the request bypasses auth and reaches the echo-server (200);
// with the fix the auth check fires and returns 401.
func denyAuthURL() string {
	return "http://auth-server." + SharedFixturesNamespace + ".svc:80/deny"
}
