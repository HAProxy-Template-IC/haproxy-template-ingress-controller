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
	"fmt"
	"testing"
	"time"

	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

// TestHapticRateLimit is the haproxy-haptic.org/* adaptation of
// TestIngressRateLimit: it proves HAPTIC's native rate-limiting
// annotations gate requests by source IP end-to-end through a deployed
// HAProxy, using the same stick-table-backed http_req_rate mechanism.
//
// The haptic 24-rate-limiting.yaml fragment renders, for
// rate-limit-rps=5 with a rate-limit-period=10s override:
//
//	stick-table type ip size 100k expire 10s store http_req_rate(10s) peers localinstance
//	http-request track-sc0 src
//	http-request deny deny_status 429 if { sc_http_req_rate(0) gt 5 }
//
// so the 6th request from one source IP inside a 10s window is denied —
// identical runtime behaviour to the haproxy.org/* vendor test.
//
// Verification must come from *inside* the cluster: DinD NAT randomises
// the source IP of connections from the test host, so a host-side burst
// hits HAProxy with N different src IPs and never trips the per-source
// limit. rateLimitBurstFromCluster fires the burst from a per-test
// alpine/curl pod as ONE curl invocation, so every request shares one
// pod IP and the limit reliably fires.
func TestHapticRateLimit(t *testing.T) {
	t.Parallel()
	host := "ingress-haptic-ratelimit.localdev.me"

	// Rate-limit at 5 per 10s, send 20 requests. rate-limit-rps sets the
	// per-source cap; rate-limit-period widens the http_req_rate window
	// from the 1s rps default to 10s so a fresh burst pod's requests all
	// land in one sliding window and the 6th trips the deny.
	// burstTotal = 4×rateLimit leaves room for a few requests lost to
	// reload churn while still exceeding the limit.
	const (
		rateLimit  = 5
		ratePeriod = 10 * time.Second
		burstTotal = 20
	)

	feature := features.New("Ingress: haptic rate-limit annotation enforces from inside cluster").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			backend := NewEchoServerBackend(ctx, t, client, ns)
			NewIngress(ctx, t, client, ns, IngressSpec{
				Name:           "echo-haptic-ratelimit",
				Host:           host,
				Path:           "/",
				BackendService: backend.Service,
				BackendPort:    backend.Port,
				Annotations: map[string]string{
					"haproxy-haptic.org/rate-limit-rps":         fmt.Sprintf("%d", rateLimit),
					"haproxy-haptic.org/rate-limit-period":      ratePeriod.String(),
					"haproxy-haptic.org/rate-limit-size":        "100k",
					"haproxy-haptic.org/rate-limit-status-code": "429",
				},
			})
			// Wait for HAProxy to pick up the new Ingress before bursting.
			httpclient.New(t).GET(host, "/").ExpectOK(t)
			return StoreNamespaceInContext(ctx, ns)
		}).
		Assess("burst from in-cluster pod trips the limit (≥1 × 429)", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			ns, err := GetNamespaceFromContext(ctx)
			if err != nil {
				t.Fatalf("get namespace: %v", err)
			}
			result := rateLimitBurstFromCluster(ctx, t, ns, host, burstTotal, rateLimit, ratePeriod)
			t.Logf("haptic rate-limit burst: %s", result)
			if result.byCode["429"] == 0 {
				// Full status-code distribution makes the next flake
				// self-diagnosing (5×200 / 0×429 / 15×000 = connection
				// drops, 20×200 = stick-table reset, 5×200 / 15×5xx =
				// reload-window backend transition).
				t.Fatalf("expected at least one 429 from a burst of %d requests; got %s",
					burstTotal, result)
			}
			return ctx
		}).
		Feature()
	testEnv.Test(t, feature)
}
