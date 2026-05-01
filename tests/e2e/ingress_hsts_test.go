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
	"strings"
	"testing"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

// TestIngressHSTS covers haproxy-ingress.github.io/hsts and the
// associated hsts-* sub-annotations: the chart must surface a
// Strict-Transport-Security header on responses with the configured
// max-age plus optional includeSubdomains and preload directives.
//
// HSTS is only meaningful — and the chart only emits the header — on
// HTTPS responses (the `{ ssl_fc }` ACL gates the directive). Hence the
// test creates a TLS Ingress and probes via HTTPS.
func TestIngressHSTS(t *testing.T) {
	t.Parallel()
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description:   "Ingress: HSTS via haproxy-ingress.github.io",
		Host:          "ingress-hsts.localdev.me",
		TLSSecretName: "hsts-tls",
		Annotations: map[string]string{
			"haproxy-ingress.github.io/hsts":                    "true",
			"haproxy-ingress.github.io/hsts-max-age":            "31536000",
			"haproxy-ingress.github.io/hsts-include-subdomains": "true",
			"haproxy-ingress.github.io/hsts-preload":            "true",
		},
		Assess: []SimpleIngressAssertion{{
			Name: "HTTPS response carries Strict-Transport-Security with all three HSTS components",
			Check: func(t *testing.T, host string) {
				resp := httpclient.New(t).HTTPS(host, "/").ExpectOK(t)
				sts := resp.Header.Get("Strict-Transport-Security")
				if sts == "" {
					t.Fatalf("expected Strict-Transport-Security header, got headers: %v", resp.Header)
				}
				if !strings.Contains(sts, "max-age=31536000") {
					t.Fatalf("expected max-age=31536000 in STS header, got %q", sts)
				}
				// HAProxy emits "includeSubDomains" (capital D) per RFC 6797.
				if !strings.Contains(sts, "includeSubDomains") {
					t.Fatalf("expected includeSubDomains in STS header, got %q", sts)
				}
				if !strings.Contains(sts, "preload") {
					t.Fatalf("expected preload in STS header, got %q", sts)
				}
			},
		}},
	})
}
