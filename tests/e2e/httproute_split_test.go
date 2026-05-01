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
	"math"
	"testing"

	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

// TestHTTPRouteSplit covers test_httproute_split: 70/30 weighted traffic
// split between two backends. The chart uses HAProxy's rand() for the
// split, so the observed distribution converges to the configured weights
// only with a non-trivial sample. Bash uses 200 samples / 15 percentage-point
// tolerance; we keep the same numbers.
//
// Statistical flakiness floor by binomial math is ~1% (pass-rate >99%
// at 200 samples / ±15 pp). Consistent flakes would indicate the chart's
// weight rendering broke, not random variance.
func TestHTTPRouteSplit(t *testing.T) {
	t.Parallel()
	host := "httproute-split.localdev.me"

	const (
		samples         = 200
		tolerancePoints = 15
		v2Weight        = 30
		defaultWeight   = 70
	)

	feature := features.New("HTTPRoute: 70/30 weighted backend split").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			defaultBackend := NewEchoServerBackend(ctx, t, client, ns)
			v2Backend := NewEchoServerV2Backend(ctx, t, client, ns)
			NewGateway(ctx, t, ns, "test-gateway")

			NewHTTPRoute(ctx, t, ns, HTTPRouteSpec{
				Name:        "echo-split",
				GatewayName: "test-gateway",
				Hostnames:   []string{host},
				Rules: []HTTPRouteRule{{
					PathType: "PathPrefix",
					Path:     "/",
					BackendRefs: []HTTPRouteBackendRef{
						{Service: defaultBackend.Service, Port: defaultBackend.Port, Weight: defaultWeight},
						{Service: v2Backend.Service, Port: v2Backend.Port, Weight: v2Weight},
					},
				}},
			})
			return ctx
		}).
		Assess("traffic split converges to the configured 70/30 within ±15pp over 200 samples", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client := httpclient.New(t)

			// Drive a single request through ExpectOK so we wait for HAProxy to
			// have the route ready before we start counting samples (otherwise
			// the first ~5 samples can be 503s and skew the ratio).
			client.GET(host, "/").ExpectOK(t)

			counts := map[string]int{"default": 0, "v2": 0}
			for i := 0; i < samples; i++ {
				resp, err := client.GET(host, "/").Do(ctx)
				if err != nil || resp.Status != 200 || resp.Echo == nil {
					// Don't fail the test on a single bad sample (the
					// retry-loop wait above handles initial readiness);
					// just skip.
					continue
				}
				if resp.Echo.Environment == "v2" {
					counts["v2"]++
				} else {
					counts["default"]++
				}
			}

			total := counts["default"] + counts["v2"]
			if total < samples/2 {
				t.Fatalf("only %d/%d samples succeeded", total, samples)
			}

			pctV2 := float64(counts["v2"]) / float64(total) * 100
			pctDefault := float64(counts["default"]) / float64(total) * 100
			if math.Abs(pctV2-float64(v2Weight)) > tolerancePoints {
				t.Fatalf("v2 share %.1f%% drifted >%dpp from configured %d%% (counts: %v)",
					pctV2, tolerancePoints, v2Weight, counts)
			}
			if math.Abs(pctDefault-float64(defaultWeight)) > tolerancePoints {
				t.Fatalf("default share %.1f%% drifted >%dpp from configured %d%% (counts: %v)",
					pctDefault, tolerancePoints, defaultWeight, counts)
			}
			t.Logf("split converged: default=%.1f%% v2=%.1f%% (configured %d/%d, samples=%d)",
				pctDefault, pctV2, defaultWeight, v2Weight, total)
			return ctx
		}).
		Feature()
	testEnv.Test(t, feature)
}
