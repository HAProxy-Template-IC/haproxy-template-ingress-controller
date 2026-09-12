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

func TestIngressAuthURLPrefixSubpath(t *testing.T) {
	RequireVendorLibrary(t, nginxIngressLibrary)
	const (
		host    = "auth-prefix-subpath.localdev.me"
		subpath = "/api/users"
	)

	feature := features.New("Ingress: auth-url must fire on subpath of Prefix path").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			t.Helper()
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			backend := NewEchoServerBackend(ctx, t, client, ns)

			NewIngress(ctx, t, client, ns, &IngressSpec{
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
			t.Helper()
			httpclient.New(t).GET(host, subpath).ExpectStatus(t, http.StatusUnauthorized)
			return ctx
		})

	testEnv.Test(t, feature.Feature())
}

func TestIngressAuthURLWildcardHostSubpath(t *testing.T) {
	RequireVendorLibrary(t, nginxIngressLibrary)
	const (
		wildcardHost = "*.auth-wild.localdev.me"
		concreteHost = "api.auth-wild.localdev.me"
		subpath      = "/users"
	)

	feature := features.New("Ingress: auth-url must fire on subpath of wildcard host").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			t.Helper()
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			backend := NewEchoServerBackend(ctx, t, client, ns)

			NewIngress(ctx, t, client, ns, &IngressSpec{
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
			t.Helper()
			httpclient.New(t).GET(concreteHost, subpath).ExpectStatus(t, http.StatusUnauthorized)
			return ctx
		})

	testEnv.Test(t, feature.Feature())
}

func TestIngressAuthURLRegexPath(t *testing.T) {
	// This test needs both vendor libraries at once: the
	// nginx.ingress.kubernetes.io/auth-url annotation (nginxIngress) to make
	// the auth check fire, and the haproxy-ingress.github.io/path-type=regex
	// annotation (haproxyIngress) to route the request via the regex path.
	// Under the old single-vendor sharding no shard enabled both, so this test
	// skipped everywhere and never actually ran. The core profile now enables
	// all three, so it executes; the guards stay for the conformance profile,
	// which enables only nginx-ingress.
	RequireVendorLibrary(t, nginxIngressLibrary)
	RequireVendorLibrary(t, "haproxyIngress")
	const (
		host        = "auth-regex.localdev.me"
		regexPath   = "/api/v[0-9]+/.*"
		requestPath = "/api/v2/users"
	)

	feature := features.New("Ingress: auth-url must fire on haproxy-ingress regex path").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			t.Helper()
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			backend := NewEchoServerBackend(ctx, t, client, ns)

			NewIngress(ctx, t, client, ns, &IngressSpec{
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
			t.Helper()
			httpclient.New(t).GET(host, requestPath).ExpectStatus(t, http.StatusUnauthorized)
			return ctx
		})

	testEnv.Test(t, feature.Feature())
}

func denyAuthURL() string {
	return "http://auth-server." + SharedFixturesNamespace + ".svc:80/deny"
}
