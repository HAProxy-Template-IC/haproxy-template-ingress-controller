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

// TestIngressSSLRedirect covers test_ingress_ssl_redirect: the
// haproxy.org/ssl-redirect annotation makes HAProxy answer plain HTTP
// requests with a 301 to https://. Verifies the status code and that
// the Location header carries the https:// scheme.
func TestIngressSSLRedirect(t *testing.T) {
	t.Parallel()
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: ssl-redirect annotation",
		Host:        "ingress-ssl-redirect.localdev.me",
		Annotations: map[string]string{
			"haproxy.org/ssl-redirect":      "true",
			"haproxy.org/ssl-redirect-code": "301",
			"haproxy.org/ssl-redirect-port": "443",
		},
		Assess: []SimpleIngressAssertion{{
			Name: "HTTP request returns 301 with https:// Location",
			Check: func(t *testing.T, host string) {
				resp := httpclient.New(t).GET(host, "/").ExpectStatus(t, 301)
				loc := resp.Header.Get("Location")
				if !strings.HasPrefix(loc, "https://") {
					t.Fatalf("expected https:// scheme in Location, got %q", loc)
				}
			},
		}},
	})
}
