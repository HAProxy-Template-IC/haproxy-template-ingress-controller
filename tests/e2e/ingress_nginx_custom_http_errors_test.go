// Copyright 2026 Philipp Hossner
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
	"strconv"
	"strings"
	"testing"

	"sigs.k8s.io/e2e-framework/klient"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

const customErrorPagesService = "error-pages"

// TestIngressNginxCustomHTTPErrors covers nginx.ingress.kubernetes.io/custom-http-errors:
// an upstream 404 or 503 is answered with the default backend's page for that
// code, in the format the client's Accept header asks for, and with the
// response headers the route would otherwise carry.
//
// The default backend is an echo-server, so each page is the echo of the
// controller's fetch: its X-Code and X-Format headers prove which page the
// client got. ingress-nginx's custom-error-pages image can't stand in: its
// mime table maps text/html to .ehtml, so it has no HTML pages.
func TestIngressNginxCustomHTTPErrors(t *testing.T) {
	t.Parallel()
	RunSimpleIngressTest(t, &SimpleIngressTest{
		Description: "Ingress: nginx custom-http-errors serves the default backend's error pages",
		Host:        "ingress-nginx-custom-http-errors.localdev.me",
		Annotations: map[string]string{
			"nginx.ingress.kubernetes.io/default-backend":         customErrorPagesService,
			"nginx.ingress.kubernetes.io/custom-http-errors":      "404,503",
			"nginx.ingress.kubernetes.io/hsts":                    "true",
			"nginx.ingress.kubernetes.io/enable-cors":             "true",
			"nginx.ingress.kubernetes.io/custom-response-headers": "X-Frame-Options:DENY",
		},
		TLSSecretName: "custom-http-errors-tls",
		PreSetup: func(ctx context.Context, t *testing.T, client klient.Client, namespace string) {
			t.Helper()
			NewNamedEchoServerBackend(ctx, t, client, namespace, customErrorPagesService)
		},
		Assess: []SimpleIngressAssertion{
			{
				Name: "upstream 404 returns the default backend's HTML 404 page",
				Check: func(t *testing.T, host string) {
					t.Helper()
					expectErrorPage(t, httpclient.New(t).GET(host, "/?echo_code=404"), 404, "text/html", nil)
				},
			},
			{
				Name: "upstream 503 returns the default backend's HTML 503 page",
				Check: func(t *testing.T, host string) {
					t.Helper()
					expectErrorPage(t, httpclient.New(t).GET(host, "/?echo_code=503"), 503, "text/html", nil)
				},
			},
			{
				Name: "a client preferring JSON gets the JSON 404 page",
				Check: func(t *testing.T, host string) {
					t.Helper()
					request := httpclient.New(t).GET(host, "/?echo_code=404").WithHeader("Accept", "application/json, text/html")
					expectErrorPage(t, request, 404, "application/json", nil)
				},
			},
			{
				Name: "the replaced page keeps HSTS, CORS, Server and custom response headers",
				Check: func(t *testing.T, host string) {
					t.Helper()
					request := httpclient.New(t).HTTPS(host, "/?echo_code=404").WithHeader("Origin", "https://app.example.com")
					expectErrorPage(t, request, 404, "text/html", func(resp *httpclient.Response) bool {
						return strings.HasPrefix(resp.Header.Get("Strict-Transport-Security"), "max-age=") &&
							resp.Header.Get("Access-Control-Allow-Origin") != "" &&
							resp.Header.Get("X-Frame-Options") == "DENY" &&
							resp.Header.Get("Server") == "haptic"
					})
				},
			},
			{
				Name: "an unlisted status reaches the client unchanged",
				Check: func(t *testing.T, host string) {
					t.Helper()
					httpclient.New(t).GET(host, "/?echo_code=500").ExpectMatching(t, "the upstream's own 500",
						func(resp *httpclient.Response) bool {
							return resp.Status == 500 && resp.Echo != nil && resp.Echo.Headers["x-code"] == ""
						})
				},
			},
		},
	})
}

// expectErrorPage waits for a response with status whose body is the default
// backend's page for that status and format, and that satisfies extra if set.
func expectErrorPage(t *testing.T, request *httpclient.Request, status int, format string, extra func(*httpclient.Response) bool) {
	t.Helper()
	request.ExpectMatching(t, "the default backend's error page", func(resp *httpclient.Response) bool {
		return resp.Status == status &&
			strings.HasPrefix(resp.Header.Get("Content-Type"), format) &&
			resp.Echo != nil &&
			resp.Echo.Headers["x-code"] == strconv.Itoa(status) &&
			resp.Echo.Headers["x-format"] == format &&
			resp.Echo.Headers["x-ingress-name"] == "echo" &&
			(extra == nil || extra(resp))
	})
}
