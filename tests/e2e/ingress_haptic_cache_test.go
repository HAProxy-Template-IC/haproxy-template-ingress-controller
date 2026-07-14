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
	"testing"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

// TestHapticVarnishCache exercises the shared Varnish cache tier (70-caching.yaml)
// end-to-end: the chart-deployed Varnish StatefulSet, the loopback origin, and
// per-route caching. Runs only in the cache shard (HAPTIC_E2E_PROFILE=cache),
// which installs with controller.cache.varnish.enabled=true.
//
// The VCL tags every response with X-Cache: HIT|MISS (vcl_deliver). A HIT proves
// the full path: the tier is deployed and reachable, the first request's MISS
// fetched from the app through the HAProxy loopback, and the object was then
// served from Varnish's cache.
func TestHapticVarnishCache(t *testing.T) {
	RequireCacheProfile(t)
	t.Parallel()
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: HAPTIC shared Varnish cache tier",
		Host:        "ingress-haptic-cache.localdev.me",
		Annotations: map[string]string{
			"haproxy-haptic.org/cache-enable":        "true",
			"haproxy-haptic.org/cache-ttl":           "60",
			"haproxy-haptic.org/cache-exclude-paths": "/nocache",
		},
		Assess: []SimpleIngressAssertion{
			{
				Name: "cacheable GET is served from Varnish (X-Cache: HIT after warm-up)",
				Check: func(t *testing.T, host string) {
					// Polling absorbs Varnish startup and the MISS→HIT transition:
					// the first request(s) MISS (fetched from the app via the
					// HAProxy loopback), then the object is served from the cache.
					httpclient.New(t).GET(host, "/cache-probe").ExpectMatching(t,
						"response served from the Varnish cache (X-Cache: HIT)",
						func(resp *httpclient.Response) bool {
							return resp.Status == 200 && resp.Header.Get("X-Cache") == "HIT"
						})
				},
			},
			{
				Name: "excluded path bypasses the cache (no X-Cache header)",
				Check: func(t *testing.T, host string) {
					// cache-exclude-paths routes /nocache straight to the app, so
					// Varnish never sees it and adds no X-Cache header.
					httpclient.New(t).GET(host, "/nocache").ExpectMatching(t,
						"excluded path reaches the app directly, no X-Cache header",
						func(resp *httpclient.Response) bool {
							return resp.Status == 200 && resp.Echo != nil && resp.Header.Get("X-Cache") == ""
						})
				},
			},
		},
	})
}
