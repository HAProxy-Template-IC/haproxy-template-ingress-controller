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
	"time"

	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

// TestHTTPRouteSplit covers test_httproute_split: 70/30 weighted traffic
// split between two backends. The chart uses HAProxy's rand() for the
// split, so the observed distribution converges to the configured weights
// only with a non-trivial sample.
//
// Reload-resilient sampling. Concurrent e2e tests create/delete fixtures
// during the sampling window, triggering HAProxy reloads roughly every
// ~300ms. Each reload drops in-flight connections for ~50-150ms. A
// single-shot sampling loop loses ~50% of samples to those windows
// (issue #54), which fails the `total < samples/2` floor. The
// per-sample retry below recovers samples that hit a reload — a fresh
// connection to the post-reload HAProxy succeeds — so the counted
// distribution stays accurate.
//
// Statistical bound: the 70/30 assertion accepts the observed v2 share
// within a five-sigma confidence band whose half-width is derived from the
// ACTUAL successful-sample count, not a fixed tolerance. Its false-failure
// probability under correct weights is P(|Z| > 5) = 5.7e-7 (normal
// approximation; see the Assess block). Deriving the band from the real
// count means churn that thins the sample widens the band rather than
// flaking it — the retry above keeps the count near the 200 target, and the
// band absorbs whatever count remains.
func TestHTTPRouteSplit(t *testing.T) {
	t.Parallel()
	host := "httproute-split.localdev.me"
	var fwd GatewayForward

	const (
		samples = 200
		// zFiveSigma is the standard-normal quantile for a five-sigma two-sided
		// bound. Under correct 70/30 weights the observed v2 share is
		// Binomial(total, 0.30)/total, and P(|Z| > 5) = 5.7e-7 by the normal
		// approximation — so the 70/30 assertion below falsely fails on
		// well-formed traffic less than once per ~1.7 million runs. The band's
		// half-width is derived from the ACTUAL successful-sample count, not a
		// fixed magic tolerance (see the Assess block).
		zFiveSigma = 5.0
		// maxTolerance caps the 5-sigma band's half-width. The band widens as
		// the sample thins toward the samples/2 floor (±22.9pp at total=100),
		// which would accept a genuinely broken split — e.g. ignored weights
		// producing ~50/50 (v2≈0.50, 0.20 from p0). Capping at 0.18 keeps a
		// ~50/50 regression always failing (0.30+0.18 = 0.48 < 0.50) while the
		// cap only binds below ~124 samples; at the nominal 200 the 5-sigma
		// band (±16.2pp) is already tighter, so de-flaking is unaffected. At
		// the floor the capped false-failure rate is P(|Z| > 0.18/0.0458) =
		// P(|Z| > 3.93) ≈ 8.5e-5. Minimum detectable effect at the floor: 18pp.
		maxTolerance        = 0.18
		v2Weight            = 30
		defaultWeight       = 70
		warmupConsecutiveOK = 5
		// A newly-created Gateway HTTPRoute is a STRUCTURAL change (new backends +
		// host/path map entries → reload), so it can't take the runtime fast path
		// and its deploy is rate-limited by minDeploymentInterval (chart default
		// 5s). Under the heavily-parallel suite, the route's deploy is commonly
		// gated behind an unrelated tenant's in-flight structural deploy that just
		// armed the interval, so the host→route map entry can take up to ~one
		// interval + a reload (~5-8s observed) to actually go live on the workers —
		// during which requests to the route 404 (default_backend, no map entry).
		// The warmup budget must cover that legitimate deploy-under-churn window;
		// 50 attempts × 50ms (~2.5s) did not, and the warmup timed out before the
		// route was even deployed. (This is a readiness precondition, not the
		// 70/30 assertion — that is unchanged.)
		warmupBudget       = 12 * time.Second
		warmupBackoff      = 50 * time.Millisecond
		sampleMaxAttempts  = 4
		sampleRetryBackoff = 50 * time.Millisecond
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
			fwd = ForwardGateway(ctx, t, ns, "test-gateway", 80)

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

			// Wait until the controller has rendered the route AND deployed it
			// to EVERY HAProxy pod before sampling. With minDeploymentInterval
			// throttling reloads, the initial structural deploy can take a couple
			// seconds; the 50-attempt warmup alone races it (the b48f3c9d CI run
			// failed here with "warmup: failed to achieve 5 consecutive 200s").
			// The marker is route-gated (issue #71): gating on the bare
			// namespace passes off the Gateway's route-independent
			// typed-access-smoke comment (rendered before this HTTPRoute) and
			// still races the route's own deploy. The backend-name fragment
			// "gtw_<ns>_echo-split_" appears only once this route's backends
			// render, and <ns> is unique per test. Same convergence wait the
			// rolling-restart test uses.
			waitForControllerDeployed(ctx, t, client, "gtw_"+ns+"_echo-split_")
			return ctx
		}).
		Assess("traffic split converges to the configured 70/30 within a five-sigma band over ~200 samples", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client := httpclient.ForForwarded(t, fwd.HTTPPort, 0)

			// Warmup: wait for N consecutive 200s before starting the
			// sampling loop. A single warmup request can lull the test
			// into starting right before a reload window — requiring
			// several in a row ensures HAProxy is in steady state with
			// both backends healthy.
			waitWarmedUp(ctx, t, client, host, warmupConsecutiveOK, warmupBudget, warmupBackoff)

			counts := map[string]int{"default": 0, "v2": 0}
			fails := 0
			for i := 0; i < samples; i++ {
				resp := sampleWithRetry(ctx, client, host, sampleMaxAttempts, sampleRetryBackoff)
				if resp == nil {
					fails++
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
				t.Fatalf("only %d/%d samples succeeded after retries (fails=%d) — HAProxy backends genuinely unreachable",
					total, samples, fails)
			}

			// Expected share of the v2 (minority) backend under the configured weights.
			p0 := float64(v2Weight) / float64(v2Weight+defaultWeight) // 0.30

			// Accept the split iff the observed v2 share lies within a five-sigma
			// confidence band of p0. Each sample is Bernoulli(p0), so the observed
			// share has standard error sqrt(p0(1-p0)/total). Deriving the band's
			// half-width from the ACTUAL `total` (not a fixed 200) is the fix for
			// issue #69: when concurrent-test churn thins the sample toward the
			// samples/2 floor, the band WIDENS to hold the false-failure rate flat
			// instead of tightening into a spurious failure. The normal
			// approximation is valid because total >= samples/2 = 100 keeps both
			// total*p0 = 30 and total*(1-p0) = 70 well above 5. False-failure
			// probability P(|Z| > 5) = 5.7e-7 (see zFiveSigma). The band is
			// capped at maxTolerance so it never widens enough to accept a
			// broken (~50/50) split when the sample thins toward the floor.
			se := math.Sqrt(p0 * (1 - p0) / float64(total))
			tolerance := math.Min(zFiveSigma*se, maxTolerance)
			observedV2 := float64(counts["v2"]) / float64(total)

			// Checking the v2 share alone also covers the default share: the two are
			// complementary (observedDefault-0.70 = -(observedV2-0.30)), so their
			// absolute deviations from the configured weights are identical.
			if math.Abs(observedV2-p0) > tolerance {
				t.Fatalf("v2 share %.1f%% drifted >%.1fpp (5σ over %d samples) from configured %d%% (counts: %v, fails: %d)",
					observedV2*100, tolerance*100, total, v2Weight, counts, fails)
			}
			t.Logf("split converged: default=%.1f%% v2=%.1f%% (configured %d/%d, total=%d, 5σ tolerance=±%.1fpp, retried_fails=%d)",
				(1-observedV2)*100, observedV2*100, defaultWeight, v2Weight, total, tolerance*100, fails)
			return ctx
		}).
		Feature()
	testEnv.Test(t, feature)
}

// waitWarmedUp blocks until `consecutive` requests in a row succeed, or fails the
// test if that streak isn't reached within `budget`. Reaching a streak proves the
// route's host→backend mapping is live on the serving workers and both backends
// are healthy enough to count from.
//
// It closes idle connections before each attempt so every probe dials a fresh
// connection: while the route's deploy is still gated (its host-map entry not yet
// on the workers), a request 404s and pins a keepalive connection to the
// pre-route (draining) worker, which would keep 404ing on that connection until it
// closes. Forcing a fresh dial each attempt lets the streak form the moment the
// route-bearing config reloads, rather than waiting out the stale worker's drain.
func waitWarmedUp(ctx context.Context, t *testing.T, client *httpclient.Client, host string, consecutive int, budget, backoff time.Duration) {
	t.Helper()
	deadline := time.Now().Add(budget)
	streak, attempts := 0, 0
	for time.Now().Before(deadline) {
		client.CloseIdleConnections() // escape a stale worker pinned by a prior 404
		attempts++
		resp, err := client.GET(host, "/").Do(ctx)
		if err == nil && resp.Status == 200 && resp.Echo != nil {
			streak++
			if streak >= consecutive {
				return
			}
			continue
		}
		streak = 0
		time.Sleep(backoff)
	}
	t.Fatalf("warmup: failed to achieve %d consecutive 200s within %s (%d attempts) — the route never became live", consecutive, budget, attempts)
}

// sampleWithRetry returns a successful response or nil if every retry
// failed. A nil return means even sequential retries (well past any
// single reload window) couldn't get through — i.e., not a transient
// reload race but real backend unavailability.
func sampleWithRetry(ctx context.Context, client *httpclient.Client, host string, maxAttempts int, backoff time.Duration) *httpclient.Response {
	for attempt := 0; attempt < maxAttempts; attempt++ {
		resp, err := client.GET(host, "/").Do(ctx)
		if err == nil && resp.Status == 200 && resp.Echo != nil {
			return resp
		}
		if attempt < maxAttempts-1 {
			time.Sleep(backoff)
		}
	}
	return nil
}
