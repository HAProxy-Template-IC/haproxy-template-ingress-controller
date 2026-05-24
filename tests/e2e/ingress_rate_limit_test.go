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
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

// TestIngressRateLimit covers test_ingress_rate_limit: HAProxy's
// stick-table-backed rate-limit annotations gate requests by source IP.
// Verification has to come from *inside* the cluster — DinD NAT
// randomises the source IP for connections originating from the test
// host, so a parallel burst from the host hits HAProxy with N different
// src IPs and never trips the limit. We use kubectl exec into a
// per-test alpine/curl pod (xargs -P10 from inside the pod), where
// every request shares one cluster-IP and the limit reliably fires.
func TestIngressRateLimit(t *testing.T) {
	t.Parallel()
	host := "ingress-ratelimit.localdev.me"

	// Conservative numbers: rate-limit at 5/period, send 20 requests.
	// Even with some racing, well above the limit.
	const (
		rateLimit  = 5
		burstTotal = 20
	)

	feature := features.New("Ingress: rate-limit annotation enforces from inside cluster").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			backend := NewEchoServerBackend(ctx, t, client, ns)
			NewIngress(ctx, t, client, ns, IngressSpec{
				Name:           "echo-ratelimit",
				Host:           host,
				Path:           "/",
				BackendService: backend.Service,
				BackendPort:    backend.Port,
				Annotations: map[string]string{
					"haproxy.org/rate-limit-requests":    fmt.Sprintf("%d", rateLimit),
					"haproxy.org/rate-limit-period":      "10s",
					"haproxy.org/rate-limit-size":        "100k",
					"haproxy.org/rate-limit-status-code": "429",
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
			result := rateLimitBurstFromCluster(ctx, t, ns, host, burstTotal)
			t.Logf("rate-limit burst: %s", result)
			if result.byCode["429"] == 0 {
				// Expand the failure message with the full status-code
				// distribution so the next flake tells us *what* happened
				// (e.g. 5×200 / 0×429 / 15×000 = connection drops, vs.
				// 20×200 = stick-table reset, vs. 5×200 / 15×5xx = a
				// reload-window backend transition). The previous failure
				// message silently dropped non-200/non-429 codes which
				// hid the real cause across multiple investigations.
				t.Fatalf("expected at least one 429 from a burst of %d requests; got %s",
					burstTotal, result)
			}
			return ctx
		}).
		Feature()
	testEnv.Test(t, feature)
}

// StoreNamespaceInContext / GetNamespaceFromContext let Setup pass the
// per-test namespace name through to Assess. e2e-framework features
// share *envconf.Config across phases, but envconf doesn't carry
// arbitrary values — context does.
type namespaceKey struct{}

// StoreNamespaceInContext returns ctx with the namespace name attached
// under a private key, retrievable via GetNamespaceFromContext.
func StoreNamespaceInContext(ctx context.Context, namespace string) context.Context {
	return context.WithValue(ctx, namespaceKey{}, namespace)
}

// GetNamespaceFromContext retrieves the namespace name stored by
// StoreNamespaceInContext. Returns an error if not set.
func GetNamespaceFromContext(ctx context.Context) (string, error) {
	v, ok := ctx.Value(namespaceKey{}).(string)
	if !ok || v == "" {
		return "", fmt.Errorf("namespace not in context")
	}
	return v, nil
}

// rateLimitBurstResult captures the full outcome of a burst — every
// status code seen, with counts, plus wall-clock duration. The earlier
// (ok, blocked int) signature silently dropped non-200/non-429 codes,
// which made flakes look like "stick-table reset" when they could have
// been any of: connection drops (000), backend transition (5xx),
// routing race (404), or admission webhook errors (4xx). The full
// distribution makes the next flake self-diagnosing.
//
// `requested` is what the test asked for; `parsed` is the sum of
// `byCode`. They can diverge if a curl instance is killed before
// writing its status to stdout (e.g. xargs -P10 SIGKILL'd by the pod
// terminating). The failure message shows both so a missing-line
// gap doesn't masquerade as a status mismatch.
type rateLimitBurstResult struct {
	requested int
	duration  time.Duration
	byCode    map[string]int
}

// parsed returns the total number of status codes actually captured
// from curl's stdout — i.e. the sum of byCode values.
func (r rateLimitBurstResult) parsed() int {
	n := 0
	for _, c := range r.byCode {
		n += c
	}
	return n
}

// String renders the result as `20 reqs over 0.42s: 5×200 / 0×429 /
// 15×000`. Codes are sorted by count desc so the dominant bucket
// appears first.
func (r rateLimitBurstResult) String() string {
	type cc struct {
		code  string
		count int
	}
	var pairs []cc
	for k, v := range r.byCode {
		pairs = append(pairs, cc{k, v})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].count != pairs[j].count {
			return pairs[i].count > pairs[j].count
		}
		return pairs[i].code < pairs[j].code
	})
	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		parts = append(parts, fmt.Sprintf("%d×%s", p.count, p.code))
	}
	parsed := r.parsed()
	if parsed == r.requested {
		return fmt.Sprintf("%d reqs over %.2fs: %s", r.requested, r.duration.Seconds(), strings.Join(parts, " / "))
	}
	// Missing lines means curls were killed before writing their
	// status. Report both numbers so the gap is visible.
	return fmt.Sprintf("%d requested / %d parsed over %.2fs: %s",
		r.requested, parsed, r.duration.Seconds(), strings.Join(parts, " / "))
}

// rateLimitBurstFromCluster runs `total` concurrent curls against
// http://<host>/ from inside a kubectl-run alpine/curl pod, returning
// the full status-code distribution + duration. curl is invoked with
// `--max-time 5` so connection failures surface as `000` (curl's
// own convention) rather than hanging.
//
// The pod is created and deleted per call. We use --restart=Never +
// --rm so the pod tears down even if the test fails.
func rateLimitBurstFromCluster(ctx context.Context, t *testing.T, namespace, host string, total int) rateLimitBurstResult {
	t.Helper()

	// alpine/curl includes both `curl` and `xargs`. The HAProxy NodePort
	// is also reachable via the in-cluster Service (port 80, host
	// header sets the route), so we go via the chart's haptic-haproxy
	// Service rather than the host-side NodePort to keep the source
	// IP consistent.
	//
	// `--max-time 5` bounds each curl so reload-window connection
	// drops produce `000` quickly rather than hanging the whole
	// burst on the slowest one.
	runOnce := func() rateLimitBurstResult {
		cmdScript := fmt.Sprintf(
			`seq 1 %d | xargs -P 10 -I{} curl -s --max-time 5 -o /dev/null -w "%%{http_code}\n" `+
				`-H "Host: %s" http://haptic-haproxy.haptic.svc/`,
			total, host)

		podName := fmt.Sprintf("ratelimit-burst-%d", time.Now().UnixNano())
		cmd := exec.CommandContext(ctx, "kubectl",
			"--kubeconfig", kubeconfigPath,
			"-n", namespace,
			"run", podName,
			"--rm", "-i",
			"--restart=Never",
			"--image=alpine/curl:latest",
			"--quiet",
			"--command", "--",
			"sh", "-c", cmdScript,
		)
		var out, errBuf bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &errBuf
		start := time.Now()
		runErr := cmd.Run()
		elapsed := time.Since(start)
		// Don't fatal on non-zero exit. xargs returns 123 whenever any
		// curl invocation exited non-zero (e.g. 7 on connection refused
		// or 28 on --max-time hit) — both of which the test wants to
		// observe as `000` in the byCode distribution, not as a fatal
		// pod-run error. The byCode-based check in the caller is what
		// distinguishes "rate-limit not engaging" from "reload-window
		// connection drops".
		if runErr != nil {
			t.Logf("rate-limit burst pod returned non-zero (individual curls failed): %v\nstderr: %s", runErr, errBuf.String())
		}

		result := rateLimitBurstResult{
			requested: total,
			duration:  elapsed,
			byCode:    map[string]int{},
		}
		for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
			code := strings.TrimSpace(line)
			if code == "" {
				continue
			}
			result.byCode[code]++
		}
		return result
	}

	// Burst once; retry once after a 1s gap if the result looks like
	// reload-window churn (no 429s, fewer than half the curls landed).
	// The parallel-test e2e suite drives haproxy reloads every ~1-2s as
	// other tests create / delete ingresses; a single burst that races a
	// reload window produces all-000 codes (each curl `--max-time 5`'s
	// out before the new worker binds the socket). Two bursts with a 1s
	// gap clear that race for the vast majority of cases without
	// changing the chart's reload cadence. A sustained 0×429 across both
	// bursts is a real test failure the caller fatals on.
	result := runOnce()
	if result.byCode["429"] > 0 {
		return result
	}
	landed := result.byCode["200"] + result.byCode["429"]
	if landed > total/2 {
		// Burst landed cleanly but no 429 — real failure, don't retry.
		return result
	}
	t.Logf("rate-limit burst attempt 1 looks like reload-window churn (%s); retrying once after 1s", result)
	select {
	case <-time.After(1 * time.Second):
	case <-ctx.Done():
		return result
	}
	return runOnce()
}
