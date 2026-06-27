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
	"testing"

	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

// TestIngressAnnotations is the smoke matrix for chart annotations whose
// behaviour doesn't get its own focused test file. Every row creates an
// Ingress with a representative annotation set and asserts that HAProxy
// still serves the request — the goal is "applying these annotations
// doesn't break the render", not behavioural verification.
//
// Annotations with rich behaviour (Basic auth, external auth, redirects,
// CORS, mTLS, SSL passthrough, rate limiting, sticky sessions) live in
// dedicated files (ingress_basic_auth_test.go, ingress_redirect_test.go, …).
//
// The matrix mixes the three annotation prefixes the chart understands —
// haproxy.org/*, haproxy-ingress.github.io/*, nginx.ingress.kubernetes.io/*
// — to exercise each library's wiring. We don't test every alias of every
// behaviour: the chart's library merge is deterministic, so a single
// representative row per behaviour surfaces render regressions; matrix
// growth past ~25 rows starts saturating the controller's reconcile
// pipeline and producing flake-by-throughput failures rather than real
// chart bugs.
func TestIngressAnnotations(t *testing.T) {
	t.Parallel()

	type assertFn func(t *testing.T, host string)

	// assertWebhookAdmittedOnly is a no-op extraAssert for rows whose
	// annotation set passes admission (the webhook just rendered the
	// merged config with the annotation in place — that's the actual
	// assertion) but whose runtime semantics need a backend the shared
	// echo-server doesn't provide (PROXY-protocol, mTLS, etc.) or whose
	// behaviour is fully covered by a sibling dedicated test. Used for
	// rows that exist purely to gate render-time annotation parsing.
	assertWebhookAdmittedOnly := func(_ *testing.T, _ string) {}
	_ = assertWebhookAdmittedOnly // avoid unused warning before first use below

	cases := []struct {
		// host is the request Host: header. Also used as the kebab-cased
		// sub-test name segment so it must be a valid DNS label.
		name        string
		host        string
		annotations map[string]string
		// extraAssert lets a row tighten the smoke check past "200 OK"
		// without extracting it to its own file. Keep these short.
		extraAssert assertFn
	}{
		// ── haproxy.org/* (haproxytech library) — 13 rows from bash ──
		{
			name: "allowlist",
			host: "ingress-allowlist.localdev.me",
			annotations: map[string]string{
				"haproxy.org/allowlist": "127.0.0.1, 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16",
			},
		},
		{
			name: "denylist",
			host: "ingress-denylist.localdev.me",
			annotations: map[string]string{
				"haproxy.org/denylist": "1.1.1.1, 8.8.8.8",
			},
		},
		{
			name: "sethost",
			host: "ingress-sethost.localdev.me",
			annotations: map[string]string{
				"haproxy.org/set-host": "echo-server.echo.svc.cluster.local",
			},
		},
		{
			name: "timeouts",
			host: "ingress-timeouts.localdev.me",
			annotations: map[string]string{
				// Cover all six haproxy.org/timeout-* aliases the chart wires.
				"haproxy.org/timeout-server":          "30s",
				"haproxy.org/timeout-client":          "60s",
				"haproxy.org/timeout-connect":         "10s",
				"haproxy.org/timeout-http-request":    "5s",
				"haproxy.org/timeout-http-keep-alive": "2m",
				"haproxy.org/timeout-queue":           "15s",
				"haproxy.org/timeout-tunnel":          "1h",
				"haproxy.org/timeout-check":           "5s",
			},
		},
		{
			name: "forwardedfor",
			host: "ingress-forwardedfor.localdev.me",
			annotations: map[string]string{
				"haproxy.org/forwarded-for": "true",
			},
		},
		{
			name: "capture",
			host: "ingress-capture.localdev.me",
			annotations: map[string]string{
				"haproxy.org/request-capture":     "User-Agent\nReferer\nCookie",
				"haproxy.org/request-capture-len": "256",
			},
		},
		{
			name: "healthcheck",
			host: "ingress-healthcheck.localdev.me",
			annotations: map[string]string{
				"haproxy.org/check":          "true",
				"haproxy.org/check-http":     "GET /health",
				"haproxy.org/check-interval": "10s",
			},
		},
		{
			name: "maxconn",
			host: "ingress-maxconn.localdev.me",
			annotations: map[string]string{
				"haproxy.org/pod-maxconn": "100",
			},
		},
		{
			name: "srcip",
			host: "ingress-srcip.localdev.me",
			annotations: map[string]string{
				"haproxy.org/src-ip-header": "X-Real-IP",
			},
		},
		{
			name: "loadbalance",
			host: "ingress-loadbalance.localdev.me",
			annotations: map[string]string{
				"haproxy.org/load-balance": "leastconn",
			},
		},
		{
			name: "headers-request",
			host: "ingress-headers-request.localdev.me",
			annotations: map[string]string{
				"haproxy.org/request-set-header": "X-Forwarded-Proto https\nX-Custom-Request custom-req-value\nX-Request-ID req-12345",
			},
		},
		{
			name: "backend-snippet",
			host: "ingress-backend-snippet.localdev.me",
			annotations: map[string]string{
				"haproxy.org/backend-config-snippet": "option httplog\noption http-keep-alive\nhttp-reuse safe",
			},
		},
		{
			name: "scale-slots",
			host: "ingress-scale-slots.localdev.me",
			annotations: map[string]string{
				"haproxy.org/scale-server-slots": "20",
			},
		},
		{
			name: "cookie-persistence-no-dynamic",
			host: "ingress-cookie-no-dynamic.localdev.me",
			annotations: map[string]string{
				// Static (no-dynamic) cookie persistence — the chart
				// treats this and `cookie-persistence` as mutually
				// exclusive, so set only the static variant here.
				"haproxy.org/cookie-persistence-no-dynamic": "MYSESSION",
			},
		},

		// ── haproxy-ingress.github.io/* (haproxy-ingress library) ──
		// These rows exercise the haproxy-ingress.github.io namespace,
		// which is a separate code path from haproxy.org/* in the chart.
		// Each row bundles related annotations so the matrix stays small.
		{
			name: "hi-cors",
			host: "ingress-hi-cors.localdev.me",
			annotations: map[string]string{
				"haproxy-ingress.github.io/cors-enable":            "true",
				"haproxy-ingress.github.io/cors-allow-origin":      "https://example.com",
				"haproxy-ingress.github.io/cors-allow-methods":     "GET, POST, OPTIONS",
				"haproxy-ingress.github.io/cors-allow-headers":     "X-Custom-Header",
				"haproxy-ingress.github.io/cors-allow-credentials": "true",
				"haproxy-ingress.github.io/cors-expose-headers":    "X-Response-Header",
				"haproxy-ingress.github.io/cors-max-age":           "3600",
			},
		},
		{
			name: "hi-session-cookie",
			host: "ingress-hi-session-cookie.localdev.me",
			annotations: map[string]string{
				"haproxy-ingress.github.io/affinity":                 "cookie",
				"haproxy-ingress.github.io/session-cookie-name":      "HISESSION",
				"haproxy-ingress.github.io/session-cookie-strategy":  "insert",
				"haproxy-ingress.github.io/session-cookie-domain":    "example.com",
				"haproxy-ingress.github.io/session-cookie-dynamic":   "false",
				"haproxy-ingress.github.io/session-cookie-keywords":  "nocache",
				"haproxy-ingress.github.io/session-cookie-preserve":  "false",
				"haproxy-ingress.github.io/session-cookie-same-site": "Lax",
			},
		},
		{
			name: "hi-healthcheck",
			host: "ingress-hi-healthcheck.localdev.me",
			annotations: map[string]string{
				"haproxy-ingress.github.io/backend-check-interval": "5s",
				"haproxy-ingress.github.io/health-check-uri":       "/healthz",
				// NOTE: do not set health-check-port here. The shared echo-server
				// backend listens only on port 80, so a health check against any
				// other port can never succeed — the server settles DOWN after
				// fall*interval and the route returns 503 (flaky on slow runners).
				// The health-check-port annotation's rendering is covered by the
				// chart validationTests instead.
				"haproxy-ingress.github.io/health-check-rise-count": "2",
				"haproxy-ingress.github.io/health-check-fall-count": "3",
			},
		},
		{
			name: "hi-allowlist",
			host: "ingress-hi-allowlist.localdev.me",
			annotations: map[string]string{
				"haproxy-ingress.github.io/allowlist-source-range": "10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 127.0.0.1/32",
			},
		},
		{
			name: "hi-denylist",
			host: "ingress-hi-denylist.localdev.me",
			annotations: map[string]string{
				"haproxy-ingress.github.io/denylist-source-range": "1.1.1.1, 8.8.8.8",
			},
		},
		{
			name: "hi-misc",
			host: "ingress-hi-misc.localdev.me",
			annotations: map[string]string{
				"haproxy-ingress.github.io/balance-algorithm": "leastconn",
				"haproxy-ingress.github.io/backend-protocol":  "h1",
				"haproxy-ingress.github.io/forwardfor":        "add",
				"haproxy-ingress.github.io/headers":           "X-Custom-Header custom-value",
				"haproxy-ingress.github.io/initial-weight":    "50",
				"haproxy-ingress.github.io/limit-connections": "200",
				"haproxy-ingress.github.io/maxconn-server":    "100",
				"haproxy-ingress.github.io/maxqueue-server":   "30",
				"haproxy-ingress.github.io/path-type":         "begin",
			},
		},
		{
			name: "hi-config-backend",
			host: "ingress-hi-config-backend.localdev.me",
			annotations: map[string]string{
				"haproxy-ingress.github.io/config-backend": "option httpchk\noption http-keep-alive",
			},
		},
		{
			name: "hi-proxy-protocol",
			host: "ingress-hi-proxy-protocol.localdev.me",
			annotations: map[string]string{
				"haproxy-ingress.github.io/proxy-protocol": "v2",
			},
			// proxy-protocol makes HAProxy send PROXY-protocol packets to
			// the upstream; the shared echo-server only speaks plain HTTP,
			// so a "200 OK" assertion fails by design. We rely on the
			// admission webhook to prove the chart renders the annotation
			// correctly (it ran against the merged config); no runtime
			// probe needed. TestIngressProxyProtocol covers the equivalent
			// `haproxy.org/send-proxy-protocol` against haproxy-demo-backend
			// (which speaks PROXY).
			extraAssert: assertWebhookAdmittedOnly,
		},
		{
			name: "hi-ssl-redirect",
			host: "ingress-hi-ssl-redirect.localdev.me",
			annotations: map[string]string{
				"haproxy-ingress.github.io/ssl-redirect":      "false",
				"haproxy-ingress.github.io/ssl-redirect-code": "302",
			},
		},

		// ── nginx.ingress.kubernetes.io/* (nginx-ingress library) ──
		// Third independent code path. Same bundling strategy.
		{
			name: "nginx-cors",
			host: "ingress-nginx-cors.localdev.me",
			annotations: map[string]string{
				"nginx.ingress.kubernetes.io/enable-cors":            "true",
				"nginx.ingress.kubernetes.io/cors-allow-origin":      "https://example.com",
				"nginx.ingress.kubernetes.io/cors-allow-methods":     "GET, POST, OPTIONS",
				"nginx.ingress.kubernetes.io/cors-allow-headers":     "X-Custom-Header",
				"nginx.ingress.kubernetes.io/cors-allow-credentials": "true",
				"nginx.ingress.kubernetes.io/cors-expose-headers":    "X-Response-Header",
				"nginx.ingress.kubernetes.io/cors-max-age":           "3600",
			},
		},
		{
			name: "nginx-hsts",
			host: "ingress-nginx-hsts.localdev.me",
			annotations: map[string]string{
				"nginx.ingress.kubernetes.io/hsts":                    "true",
				"nginx.ingress.kubernetes.io/hsts-max-age":            "31536000",
				"nginx.ingress.kubernetes.io/hsts-include-subdomains": "true",
				"nginx.ingress.kubernetes.io/hsts-preload":            "true",
			},
		},
		{
			name: "nginx-rate-limit",
			host: "ingress-nginx-rate-limit.localdev.me",
			annotations: map[string]string{
				"nginx.ingress.kubernetes.io/limit-rps":         "10",
				"nginx.ingress.kubernetes.io/limit-connections": "100",
			},
		},
		{
			name: "nginx-misc",
			host: "ingress-nginx-misc.localdev.me",
			annotations: map[string]string{
				"nginx.ingress.kubernetes.io/affinity":               "cookie",
				"nginx.ingress.kubernetes.io/backend-protocol":       "HTTP",
				"nginx.ingress.kubernetes.io/load-balance":           "round_robin",
				"nginx.ingress.kubernetes.io/upstream-hash-by":       "$remote_addr",
				"nginx.ingress.kubernetes.io/use-proxy-protocol":     "false",
				"nginx.ingress.kubernetes.io/whitelist-source-range": "10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 127.0.0.1/32",
				"nginx.ingress.kubernetes.io/denylist-source-range":  "1.1.1.1",
				// configuration-snippet is HAProxy-syntax pass-through;
				// the chart does NOT translate nginx config-snippet
				// directives. Use a benign HAProxy comment so the chart
				// emits valid syntax in the rendered backend block.
				"nginx.ingress.kubernetes.io/configuration-snippet": "# nginx-misc-test snippet",
			},
		},
		{
			name: "nginx-headers",
			host: "ingress-nginx-headers.localdev.me",
			annotations: map[string]string{
				// The chart's nginx-ingress library accepts pipe-separated
				// headers (`name:value|name:value`), not newline-separated
				// (some upstream nginx-ingress builds use \n; haptic chose
				// `|` for parse simplicity). See the
				// `frontend-filters-750-nginx-ingress-custom-headers`
				// snippet in nginx-ingress.yaml.
				"nginx.ingress.kubernetes.io/custom-request-headers":  "X-Forwarded-Proto: https|X-Request-ID: nginx-test",
				"nginx.ingress.kubernetes.io/custom-response-headers": "X-Frame-Options: DENY|X-Content-Type-Options: nosniff",
			},
		},
		{
			name: "nginx-redirect",
			host: "ingress-nginx-redirect.localdev.me",
			annotations: map[string]string{
				"nginx.ingress.kubernetes.io/ssl-redirect":       "false",
				"nginx.ingress.kubernetes.io/force-ssl-redirect": "false",
			},
		},
		{
			name: "nginx-rewrite",
			host: "ingress-nginx-rewrite.localdev.me",
			annotations: map[string]string{
				"nginx.ingress.kubernetes.io/rewrite-target": "/v2",
			},
		},
		{
			name: "nginx-proxy-tuning",
			host: "ingress-nginx-proxy-tuning.localdev.me",
			annotations: map[string]string{
				"nginx.ingress.kubernetes.io/proxy-body-size":       "10m",
				"nginx.ingress.kubernetes.io/proxy-connect-timeout": "10",
				"nginx.ingress.kubernetes.io/proxy-read-timeout":    "30",
				"nginx.ingress.kubernetes.io/proxy-send-timeout":    "30",
			},
		},
		{
			name: "nginx-session-cookie",
			host: "ingress-nginx-session-cookie.localdev.me",
			annotations: map[string]string{
				"nginx.ingress.kubernetes.io/affinity":            "cookie",
				"nginx.ingress.kubernetes.io/session-cookie-name": "NGSESSION",
				"nginx.ingress.kubernetes.io/session-cookie-path": "/",
				"nginx.ingress.kubernetes.io/session-cookie-hash": "sha1",
			},
		},
		{
			name: "nginx-auth-redirect",
			host: "ingress-nginx-auth-redirect.localdev.me",
			annotations: map[string]string{
				// auth-signin renders an http-request redirect on auth deny.
				// We don't have an auth-server pointing at this ingress, so
				// the chart's deny-with-redirect path just renders without
				// triggering — sufficient as a "covered" check.
				"nginx.ingress.kubernetes.io/auth-url":    "http://auth-server." + SharedFixturesNamespace + ".svc:80/deny",
				"nginx.ingress.kubernetes.io/auth-signin": "https://login.example.com/oauth/start",
			},
			extraAssert: func(t *testing.T, host string) {
				// The deny path issues a 302 redirect to auth-signin; verify
				// the chart wired the redirect rule.
				resp := httpclient.New(t).GET(host, "/").ExpectStatus(t, 302)
				if got := resp.Header.Get("Location"); got != "https://login.example.com/oauth/start" {
					t.Fatalf("expected Location: https://login.example.com/oauth/start; got %q", got)
				}
			},
		},
		{
			name: "nginx-canary-header-pattern",
			host: "ingress-nginx-canary-header-pattern.localdev.me",
			annotations: map[string]string{
				"nginx.ingress.kubernetes.io/canary":                   "true",
				"nginx.ingress.kubernetes.io/canary-by-header":         "X-Canary",
				"nginx.ingress.kubernetes.io/canary-by-header-pattern": "^v[0-9]+$",
			},
		},
		{
			name: "nginx-ssl-passthrough",
			host: "ingress-nginx-ssl-passthrough.localdev.me",
			annotations: map[string]string{
				// Don't actually flip ssl-passthrough on for an HTTP ingress
				// (it would change frontend wiring). Setting to "false" still
				// exercises the chart's parsing of the annotation key.
				"nginx.ingress.kubernetes.io/ssl-passthrough": "false",
			},
		},
		{
			name: "hi-auth-tls-error",
			host: "ingress-hi-auth-tls-error.localdev.me",
			annotations: map[string]string{
				"haproxy-ingress.github.io/auth-tls-error-page": "https://login.example.com/tls-error",
			},
		},
		{
			name: "hi-auth-extras",
			host: "ingress-hi-auth-extras.localdev.me",
			annotations: map[string]string{
				// Auth annotation suite from haproxy-ingress.github.io that
				// doesn't get its own behavioural test (auth-headers-fail /
				// auth-tls-secret have dedicated tests). auth-headers-request
				// / auth-headers-succeed / auth-method / auth-realm /
				// auth-secret / auth-signin all flow through the chart's
				// auth pipeline and need a render gate.
				"haproxy-ingress.github.io/auth-url":             "http://auth-server." + SharedFixturesNamespace + ".svc:80/allow",
				"haproxy-ingress.github.io/auth-method":          "GET",
				"haproxy-ingress.github.io/auth-realm":           "hi-realm",
				"haproxy-ingress.github.io/auth-headers-request": "X-Forwarded-Method, X-Forwarded-URI",
				"haproxy-ingress.github.io/auth-headers-succeed": "X-Auth-User",
				"haproxy-ingress.github.io/auth-signin":          "https://login.example.com/oauth/start",
			},
			// auth-headers-request lists headers (X-Forwarded-Method etc)
			// that aren't in the chart's hardcoded SPOE message-body
			// capture set, so the per-route forward_headers narrowing in
			// the plugin can't actually receive them. The full plumbing
			// is exercised by TestIngressBackendMTLSHaproxyIngress and
			// the auth-headers-fail test; here we only assert the chart
			// renders the annotation set without rejecting at admission.
			extraAssert: assertWebhookAdmittedOnly,
		},
		{
			name: "hi-ssl-passthrough",
			host: "ingress-hi-ssl-passthrough.localdev.me",
			annotations: map[string]string{
				"haproxy-ingress.github.io/ssl-passthrough": "false",
			},
		},
	}

	for _, tc := range cases {
		feature := features.New("Ingress: "+tc.name).
			Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
				client, err := cfg.NewClient()
				if err != nil {
					t.Fatalf("new client: %v", err)
				}
				ns := NamespaceForTest(ctx, t, client)
				DumpLogsOnFailure(t, ns)
				backend := NewEchoServerBackend(ctx, t, client, ns)
				NewIngress(ctx, t, client, ns, IngressSpec{
					Name:           "echo-" + tc.name,
					Host:           tc.host,
					Path:           "/",
					BackendService: backend.Service,
					BackendPort:    backend.Port,
					Annotations:    tc.annotations,
				})
				return ctx
			}).
			Assess(tc.host+" passes through to backend", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
				if tc.extraAssert != nil {
					tc.extraAssert(t, tc.host)
					return ctx
				}
				resp := httpclient.New(t).GET(tc.host, "/").ExpectOK(t)
				if resp.Echo == nil {
					t.Fatalf("expected echo-server JSON, got %d bytes: %s", len(resp.Body), string(resp.Body))
				}
				return ctx
			}).
			Feature()

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			testEnv.Test(t, feature)
		})
	}
}
