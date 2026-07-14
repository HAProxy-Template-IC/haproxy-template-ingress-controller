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
	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

// TestIngressRateLimit covers test_ingress_rate_limit: HAProxy's
// stick-table-backed rate-limit annotations gate requests by source IP.
// Verification has to come from *inside* the cluster — DinD NAT
// randomises the source IP for connections originating from the test
// host, so a parallel burst from the host hits HAProxy with N different
// src IPs and never trips the limit. We run the burst in a per-test
// alpine/curl pod as ONE curl invocation carrying all burstTotal URLs
// (a single process firing back-to-back keep-alive requests), where
// every request shares one pod IP and the limit reliably fires.
func TestIngressRateLimit(t *testing.T) {
	RequireVendorLibrary(t, "haproxytech")
	t.Parallel()
	host := "ingress-ratelimit.localdev.me"

	// Conservative numbers: rate-limit at 5 per 10s, send 20 requests.
	// The chart's haproxytech rate-limit snippet emits
	// `stick-table ... store http_req_rate(<period>)` plus
	// `http-request deny deny_status 429 if { sc_http_req_rate(0) gt <limit> }`,
	// so the 6th request from one source IP inside a 10s window must be
	// denied. burstTotal = 4×rateLimit leaves room for a few requests
	// lost to reload churn while still tripping the limit.
	const (
		rateLimit  = 5
		ratePeriod = 10 * time.Second
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
					"haproxy.org/rate-limit-period":      ratePeriod.String(),
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
			result := rateLimitBurstFromCluster(ctx, t, ns, host, burstTotal, rateLimit, ratePeriod)
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
// status code seen, with counts, plus two durations: podElapsed is the
// in-pod window from just before the first request to just after the
// last (measured via /proc/uptime inside the pod, so pod scheduling and
// image pull don't inflate it), and duration is the host-side
// wall-clock for the whole kubectl run. The earlier (ok, blocked int)
// signature silently dropped non-200/non-429 codes, which made flakes
// look like "stick-table reset" when they could have been any of:
// connection drops (000), backend transition (5xx), routing race (404),
// or admission webhook errors (4xx). The full distribution makes the
// next flake self-diagnosing.
//
// `requested` is what the test asked for; `parsed` is the sum of
// `byCode`. These no longer diverge in practice now that the status lines
// are read from the terminated pod's `kubectl logs` (a single,
// fully-written stream) instead of the `kubectl run -i` attach stream,
// which under parallel-suite load tore down against the fast-exiting pod
// before flushing all lines (issue #74). The failure message still shows
// both so any residual gap stays visible rather than masquerading as a
// status mismatch.
type rateLimitBurstResult struct {
	requested  int
	duration   time.Duration // host-side: includes pod scheduling + image pull
	podElapsed time.Duration // in-pod burst window; < 0 when the marker line is missing
	byCode     map[string]int
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

// landed returns the number of requests that provably reached the
// rate-limited backend and moved its stick-table counter: 200 (passed)
// or 429 (denied). 000 never reached HAProxy; 404/5xx don't prove the
// backend's rate-limit rules ran.
func (r rateLimitBurstResult) landed() int {
	return r.byCode["200"] + r.byCode["429"]
}

// rateExceeded reports whether the burst provably pushed this source
// IP's request rate above limit-per-period, making a 0×429 outcome
// judgeable as a product failure instead of scheduler starvation
// (issue #60). Two conditions, both with margin:
//
//   - at least 2×limit requests landed (the deny fires at limit+1), and
//   - the whole in-pod burst window fit inside period/2, so every
//     landed request falls within one sliding stick-table window.
//
// Each burst pod is fresh (new source IP → stick-table entry created by
// the burst's own first request), so "window ≤ period" already puts all
// landed requests in the entry's first http_req_rate bucket; period/2
// doubles the margin.
func (r rateLimitBurstResult) rateExceeded(limit int, period time.Duration) bool {
	return r.podElapsed >= 0 && r.podElapsed <= period/2 && r.landed() >= 2*limit
}

// String renders the result as `20 reqs in 0.15s in-pod (4.20s wall):
// 14×429 / 6×200`. Codes are sorted by count desc so the dominant
// bucket appears first. When curl died before emitting the timing
// marker the in-pod window shows as `?`.
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
	window := "?"
	if r.podElapsed >= 0 {
		window = fmt.Sprintf("%.2fs", r.podElapsed.Seconds())
	}
	reqs := fmt.Sprintf("%d reqs", r.requested)
	if parsed := r.parsed(); parsed != r.requested {
		// Missing lines means curl died before writing every status.
		// Report both numbers so the gap is visible.
		reqs = fmt.Sprintf("%d requested / %d parsed", r.requested, parsed)
	}
	return fmt.Sprintf("%s in %s in-pod (%.2fs wall): %s",
		reqs, window, r.duration.Seconds(), strings.Join(parts, " / "))
}

// rateLimitBurstFromCluster fires `total` back-to-back requests at
// http://<host>/ from inside a kubectl-run alpine/curl pod and returns
// the full status-code distribution + timing. The burst is ONE curl
// invocation carrying `total` URLs: a single process reusing one
// keep-alive connection, so the requests are as tight as the pod can
// physically issue them — no per-request process spawns to spread the
// burst out (issue #60: under PARALLEL=8 in-suite contention the old
// `seq | xargs -P10 curl` burst parsed 1-3 of 20 requests over >3s, a
// rate that legitimately never trips a 5-per-10s limit). curl runs with
// `--max-time 5` so per-request connection failures surface as `000`
// (curl's own convention) rather than hanging the burst, and the burst
// window is measured inside the pod via /proc/uptime so pod scheduling
// and image pull don't count against it.
//
// The verdict is gated on ACHIEVED rate: a result only settles the
// caller's ≥1×429 assertion when it contains a 429, or rateExceeded()
// proves the burst genuinely beat the configured limit and still saw
// none. Anything else (starved scheduler, reload-window churn) is
// retried with backoff until the WaitConfig budget runs out, at which
// point the test fails with an explicit starvation message instead of
// a misleading "no 429".
//
// A fresh pod is created per attempt (--restart=Never, explicitly
// deleted after each attempt), giving every attempt a clean source IP
// and therefore a fresh stick-table entry. The status codes are read
// from the terminated pod's log via `kubectl logs`, not the
// `kubectl run -i` attach stream — the attach stream lost lines against
// the fast-exiting pod under parallel-suite load (issue #74).
func rateLimitBurstFromCluster(ctx context.Context, t *testing.T, namespace, host string, total, limit int, period time.Duration) rateLimitBurstResult {
	t.Helper()

	runOnce := func(ctx context.Context) rateLimitBurstResult {
		// The HAProxy NodePort is also reachable via the in-cluster
		// Service (port 80, host header sets the route), so we go via
		// the chart's haptic-haproxy Service rather than the host-side
		// NodePort to keep the source IP consistent.
		//
		// Each URL needs its own `-o /dev/null` — a single -o only
		// applies to the first URL and later bodies would leak into
		// stdout between the status lines. /proc/uptime gives
		// 10ms-resolution wall-clock in busybox (which lacks
		// `date +%N`); the BURST_WINDOW marker carries both readings
		// back through stdout. The trailing echo also pins the script's
		// exit status to 0, so individual curl transfer failures show
		// up only as `000` lines, never as a pod-run error.
		urls := strings.TrimSpace(strings.Repeat(
			"-o /dev/null http://haptic-haproxy.haptic.svc/ ", total))
		cmdScript := fmt.Sprintf(
			`t0=$(cut -d" " -f1 /proc/uptime); `+
				`curl -s --max-time 5 -H "Host: %s" -w "%%{http_code}\n" %s; `+
				`t1=$(cut -d" " -f1 /proc/uptime); `+
				`echo "BURST_WINDOW $t0 $t1"`,
			host, urls)

		podName := fmt.Sprintf("ratelimit-burst-%d", time.Now().UnixNano())
		kubectlArgs := func(extra ...string) []string {
			return append([]string{"--kubeconfig", kubeconfigPath, "-n", namespace}, extra...)
		}
		start := time.Now()

		// Launch the burst pod DETACHED (no --rm/-i). curl fires all `total`
		// requests and exits in ~50ms; reading its status lines through the
		// `kubectl run -i` attach stream raced the pod's teardown and dropped
		// most lines under parallel-suite load, so the verdict was computed
		// from lost output and no attempt could ever settle (issue #74). Instead
		// we read the terminated pod's kubelet-buffered log via `kubectl logs`:
		// a single, fully-written stream with no concurrent producer.
		runCmd := exec.CommandContext(ctx, "kubectl", kubectlArgs(
			"run", podName,
			"--restart=Never",
			"--image=alpine/curl:latest",
			"--quiet",
			"--command", "--",
			"sh", "-c", cmdScript,
		)...)
		var runErrBuf bytes.Buffer
		runCmd.Stderr = &runErrBuf
		if err := runCmd.Run(); err != nil {
			t.Logf("rate-limit burst pod create failed: %v\nstderr: %s", err, runErrBuf.String())
		}
		// Explicit cleanup — no --rm to do it for us. A fresh background
		// context (independent of the possibly-cancelled attempt ctx) so the
		// pod is still removed after a cancelled attempt, but bounded so an
		// unreachable API server can't hang the test forever.
		defer func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_ = exec.CommandContext(cleanupCtx, "kubectl", kubectlArgs(
				"delete", "pod", podName, "--now", "--ignore-not-found")...).Run()
		}()

		// Block until the container has terminated, so its full stdout is
		// written before we read it. The script pins exit 0, so a healthy pod
		// reaches Succeeded within a few seconds (schedule + the ~50ms burst;
		// alpine/curl is ~10MB and cached after the first attempt on a node).
		// The 30s ceiling covers a cold pull with margin while keeping ~4
		// attempts inside the 2m retry budget below. A pod that never reaches
		// Succeeded (image-pull backoff, eviction, or the PARALLEL=8 starvation
		// the retry loop tolerates) burns this ceiling, then falls through to an
		// empty logs read and retries — the same infra transients the budget is
		// there to absorb.
		waitCmd := exec.CommandContext(ctx, "kubectl", kubectlArgs(
			"wait", "--for=jsonpath={.status.phase}=Succeeded",
			"pod/"+podName, "--timeout=30s")...)
		if err := waitCmd.Run(); err != nil {
			t.Logf("rate-limit burst pod did not reach Succeeded: %v", err)
		}

		var out, logsErrBuf bytes.Buffer
		logsCmd := exec.CommandContext(ctx, "kubectl", kubectlArgs("logs", podName)...)
		logsCmd.Stdout = &out
		logsCmd.Stderr = &logsErrBuf
		if err := logsCmd.Run(); err != nil {
			t.Logf("rate-limit burst logs failed: %v\nstderr: %s", err, logsErrBuf.String())
		}
		elapsed := time.Since(start)

		result := rateLimitBurstResult{
			requested:  total,
			duration:   elapsed,
			podElapsed: -1,
			byCode:     map[string]int{},
		}
		for _, raw := range strings.Split(strings.TrimSpace(out.String()), "\n") {
			line := strings.TrimSpace(raw)
			if line == "" {
				continue
			}
			if rest, ok := strings.CutPrefix(line, "BURST_WINDOW "); ok {
				var t0, t1 float64
				if n, _ := fmt.Sscanf(rest, "%f %f", &t0, &t1); n == 2 && t1 >= t0 {
					result.podElapsed = time.Duration((t1 - t0) * float64(time.Second))
				}
				continue
			}
			result.byCode[line]++
		}
		return result
	}

	// Retry budget: the parallel-test e2e suite drives haproxy reloads
	// every ~1-2s as other tests create/delete ingresses, and under
	// PARALLEL=8 the runner can starve a pod hard enough to spread even
	// a single-process burst out (issue #60 saw the old single retry
	// exhausted). Each attempt costs a full pod create/run/delete cycle
	// (several seconds when healthy), so the budget is sized in
	// attempts-worth of minutes, not convergence-style seconds. On a
	// healthy run the first attempt settles the verdict with no waiting.
	waitCfg := testutil.WaitConfig{
		InitialInterval: 1 * time.Second,
		MaxInterval:     10 * time.Second,
		Timeout:         2 * time.Minute,
		Multiplier:      2.0,
	}
	var result rateLimitBurstResult
	attempt := 0
	err := testutil.WaitForConditionWithDescription(ctx, waitCfg,
		"rate-limit burst to reach a judgeable request rate",
		func(ctx context.Context) (bool, error) {
			attempt++
			result = runOnce(ctx)
			t.Logf("rate-limit burst attempt %d: %s", attempt, result)
			if result.byCode["429"] > 0 {
				return true, nil // limit tripped — verdict settled
			}
			if result.rateExceeded(limit, period) {
				// The burst provably beat the limit and still saw no
				// 429 — judgeable; the caller fatals on the result.
				return true, nil
			}
			return false, fmt.Errorf(
				"burst under-achieved the configured rate (%s): need ≥%d×(200|429) inside %v to judge",
				result, 2*limit, period/2)
		})
	if err != nil {
		// The wait error already carries the last attempt's distribution
		// (codes/landed/elapsed). Spell out BOTH readings: chronic
		// under-achievement is usually infrastructure (issue #60), but a
		// last attempt with substantial landed 200s and zero 429s can also
		// mean the rate limit genuinely stopped firing — don't let the
		// starvation framing send that investigation the wrong way.
		t.Fatalf("rate-limit burst never achieved a judgeable rate above the %d-per-%v limit "+
			"(last attempt: %s). Chronic under-achievement = in-suite starvation or sustained "+
			"reload churn (issue #60); but if landed traffic was substantial with 0×429 across "+
			"attempts, suspect a real rate-limit regression instead: %v", limit, period, result, err)
	}
	return result
}
