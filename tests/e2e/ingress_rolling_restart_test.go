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
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/e2e-framework/klient"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"github.com/haproxy-haptic/HAPTIC/tests/e2e/httpclient"
)

// TestIngressRollingRestartZeroDowntime pins the zero-downtime contract of the
// pod readiness gate (ADR-0034): a single-replica backend whose pods hold
// haproxy-haptic.org/load-balancer-ready rolls without a single failed request.
//
// One replica is the worst case. RollingUpdate (maxSurge 1, maxUnavailable 0)
// deletes the old pod as soon as the new one is Ready, and the echo server
// stops on SIGTERM. Without the gate, Ready precedes HAProxy routing to the new
// pod by the controller's propagation delay, and every request in that window
// fails (TestIngressRollingRestartUngatedRecovers bounds that window). With the
// gate, Ready means every HAProxy pod already routes to the new pod, so the old
// one's exit is absorbed by `option redispatch`.
//
// This test runs in parallel like every other e2e test. HAPTIC must route
// correctly through the reload churn sibling tests produce; a reload-induced
// failure here is a real bug, not a reason to serialize the test.
func TestIngressRollingRestartZeroDowntime(t *testing.T) {
	t.Parallel()
	testRollingRestart(t, "ingress-rolling-restart.localdev.me",
		"Ingress: rolling restart of a gated single-replica backend is zero-downtime",
		NewGatedEchoServerBackend, requireNoFailures)
}

// TestIngressRollingRestartUngatedRecovers covers the abrupt-termination path:
// a single-replica backend without the readiness gate. Requests can fail while
// the new endpoint propagates, but only as fast 503s and only for one window no
// longer than the scale suite's single-change convergence budget.
func TestIngressRollingRestartUngatedRecovers(t *testing.T) {
	t.Parallel()
	testRollingRestart(t, "ingress-rolling-restart-ungated.localdev.me",
		"Ingress: rolling restart of an ungated single-replica backend recovers within the convergence budget",
		NewEchoServerBackend, requireBoundedRecovery)
}

// rolloutVerdict judges a probe run's failures; it is only called with at least one.
type rolloutVerdict func(failures []probeFailure) error

func testRollingRestart(
	t *testing.T,
	host, title string,
	newBackend func(context.Context, *testing.T, klient.Client, string) BackendRef,
	verdict rolloutVerdict,
) {
	t.Helper()
	// Assigned in Setup, read in Assess; e2e-framework runs them sequentially.
	var (
		namespace      string
		deploymentName string
	)

	feature := features.New(title).
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			t.Helper()
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			namespace = NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, namespace)
			backend := newBackend(ctx, t, client, namespace)
			deploymentName = backend.Service

			// NewIngress waits until every HAProxy replica reports the Ingress
			// deployed, so initial provisioning stays out of the probe window.
			NewIngress(ctx, t, client, namespace, &IngressSpec{
				Name:           "echo",
				Host:           host,
				BackendService: backend.Service,
				BackendPort:    backend.Port,
			})
			httpclient.New(t).GET(host, "/").ExpectOK(t)
			// Continuous tailers capture the moment of failure, which the
			// point-in-time snapshot taken after a failure is too late for.
			newContinuousTailer(t, namespace)
			return ctx
		}).
		Assess("rollout restart under continuous probing", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			t.Helper()
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}

			// A controller rebuild stops endpoint propagation for its whole
			// ~50s duration (issue #189), so a rollout overlapping one loses
			// traffic for a reason this test does not pin. Retry the window
			// instead of skipping it: with rebuilds landing every ~60s in this
			// suite, a skip would surrender the assertion on most runs.
			var (
				lastVerdict  error
				lastRebuilds float64
			)
			for attempt := 1; attempt <= rolloutProbeAttempts; attempt++ {
				rebuilds, result := probeOneRollout(ctx, t, client, namespace, deploymentName, host, verdict)
				if result == nil {
					return ctx
				}
				if rebuilds == 0 {
					t.Fatal(result)
				}
				lastVerdict, lastRebuilds = result, rebuilds
				t.Logf("attempt %d/%d: the controller rebuilt %.0f time(s) inside the window, "+
					"which stops endpoint propagation for its duration (#189); retrying on a quiet window. Verdict was: %v",
					attempt, rolloutProbeAttempts, rebuilds, result)
			}
			t.Skipf("all %d rollout windows overlapped a controller rebuild (last: %.0f), so the "+
				"failures cannot be attributed to the rollout (#189). Last verdict: %v",
				rolloutProbeAttempts, lastRebuilds, lastVerdict)
			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}

// fastFailMaxDur separates HAProxy's fast 503 (no usable server; `timeout
// connect` is 100ms) from a hung connect that ran into the 5s probe budget.
const fastFailMaxDur = 1500 * time.Millisecond

func requireNoFailures(failures []probeFailure) error {
	return fmt.Errorf("rolling restart of a gated backend failed %d request(s):\n%s",
		len(failures), formatProbeFailures(failures))
}

func requireBoundedRecovery(failures []probeFailure) error {
	budget := time.Duration(scaleDefaultBudgetChangeP95Seconds) * time.Second
	for _, f := range failures {
		if f.err != nil || f.status != 503 || f.dur > fastFailMaxDur {
			return fmt.Errorf("ungated rolling restart produced a failure other than a fast 503:\n%s",
				formatProbeFailures(failures))
		}
	}
	if window := failures[len(failures)-1].ts.Sub(failures[0].ts); window > budget {
		return fmt.Errorf("ungated rolling restart failed requests for %s, over the %s convergence budget:\n%s",
			window.Round(time.Millisecond), budget, formatProbeFailures(failures))
	}
	return nil
}

func formatProbeFailures(failures []probeFailure) string {
	var report strings.Builder
	for _, f := range failures {
		ts := f.ts.UTC().Format("15:04:05.000")
		if f.err != nil {
			fmt.Fprintf(&report, "  %s  err=%v  dur=%s\n", ts, f.err, f.dur)
		} else {
			fmt.Fprintf(&report, "  %s  status=%d  dur=%s\n", ts, f.status, f.dur)
		}
	}
	return report.String()
}

// probeRecorder accumulates probe outcomes with enough detail for the
// failure message to point at the exact request that 503'd. Concurrent
// from the prober goroutine; total + failures use atomic counters so
// the assertion side can read a consistent snapshot without holding the
// mutex.
type probeRecorder struct {
	mu          sync.Mutex
	failures    []probeFailure
	total       atomic.Int64
	snapshotter *proberSnapshotter
	snapshots   sync.WaitGroup
}

type probeFailure struct {
	ts     time.Time
	status int
	dur    time.Duration
	err    error
}

func (r *probeRecorder) recordSuccess() { r.total.Add(1) }

func (r *probeRecorder) recordFailure(f probeFailure) {
	r.mu.Lock()
	r.failures = append(r.failures, f)
	r.total.Add(1)
	snap := r.snapshotter
	r.mu.Unlock()
	// Capture HAProxy + controller state immediately, off the probe loop:
	// the kubectl exec fan-out takes 5–10 s, longer than a whole rollout,
	// and a loop blocked on it records nothing of the window it exists to
	// observe. The capture still starts at the failure's instant.
	if snap != nil {
		r.snapshots.Add(1)
		go func() {
			defer r.snapshots.Done()
			snap.snapshot(f)
		}()
	}
}

// waitForSnapshots blocks until every failure capture has landed, so the
// diagnostics are on disk before the test reports.
func (r *probeRecorder) waitForSnapshots() { r.snapshots.Wait() }

func (r *probeRecorder) count() int64 { return r.total.Load() }

func (r *probeRecorder) snapshotFailures() []probeFailure {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]probeFailure, len(r.failures))
	copy(out, r.failures)
	return out
}

// runProbeLoop fires HTTP GETs at the host every ~200 ms until ctx is
// done. Successful responses (2xx, 3xx) increment the success counter;
// anything else (including connection errors with no status at all) is
// recorded as a failure with full detail.
//
// 200 ms is a sweet spot: dense enough to hit the ~3 s
// "connect-failed-and-retry" windows that produced the 503s in
// production (~15 probes per window — easy to see), sparse enough that
// the test doesn't hammer the kind NodePort gratuitously.
func runProbeLoop(ctx context.Context, t *testing.T, host string, rec *probeRecorder) {
	t.Helper()
	runProbeLoopEvery(ctx, t, host, rec, 200*time.Millisecond)
}

func runProbeLoopEvery(ctx context.Context, t *testing.T, host string, rec *probeRecorder, interval time.Duration) {
	t.Helper()
	hc := httpclient.New(t)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		start := time.Now()
		// Per-probe budget so a hung connect doesn't stretch the
		// iteration past the next tick.
		reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		resp, err := hc.GET(host, "/").Do(reqCtx)
		cancel()
		dur := time.Since(start)

		if err != nil {
			// Cleanup-race guard: when stopProber() runs, reqCtx
			// inherits the canceled parent and the request fails
			// instantly with context.Canceled. That's shutdown
			// bookkeeping, not a real probe failure — skip it.
			if ctx.Err() != nil {
				return
			}
			rec.recordFailure(probeFailure{ts: start, status: 0, dur: dur, err: err})
			continue
		}
		if resp.Status >= 200 && resp.Status < 400 {
			rec.recordSuccess()
			continue
		}
		rec.recordFailure(probeFailure{ts: start, status: resp.Status, dur: dur})
	}
}

// triggerRollingRestart issues the exact patch `kubectl rollout
// restart deployment/<name>` issues — adding (or refreshing) the
// kubectl.kubernetes.io/restartedAt annotation on the pod template.
// The kubelet treats any change to pod template as "spin up a new
// ReplicaSet" so this triggers the same EndpointSlice transitions the
// production restart did.
func triggerRollingRestart(ctx context.Context, client klient.Client, namespace, deploymentName string) error {
	dep := &appsv1.Deployment{}
	if err := client.Resources(namespace).Get(ctx, deploymentName, namespace, dep); err != nil {
		return fmt.Errorf("get deployment %s/%s: %w", namespace, deploymentName, err)
	}

	if dep.Spec.Template.Annotations == nil {
		dep.Spec.Template.Annotations = map[string]string{}
	}
	dep.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().UTC().Format(time.RFC3339)

	if err := client.Resources(namespace).Update(ctx, dep); err != nil {
		return fmt.Errorf("patch deployment %s/%s with restartedAt: %w", namespace, deploymentName, err)
	}
	return nil
}

// waitForDeploymentRolloutComplete blocks until the Deployment's status
// shows the new generation fully observed AND zero unavailable
// replicas — the same condition `kubectl rollout status` waits for.
//
// We deliberately don't reach into ReplicaSets here. The Deployment
// status fields are the authoritative summary, and polling the
// Deployment alone keeps the test deterministic when the controller
// momentarily emits a ReplicaSet status update mid-transition.
func waitForDeploymentRolloutComplete(ctx context.Context, client klient.Client, namespace, deploymentName string, budget time.Duration) error {
	deadline := time.Now().Add(budget)
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()

	for {
		dep := &appsv1.Deployment{}
		if err := client.Resources(namespace).Get(ctx, deploymentName, namespace, dep); err != nil {
			return fmt.Errorf("get deployment %s/%s: %w", namespace, deploymentName, err)
		}
		desired := int32(1)
		if dep.Spec.Replicas != nil {
			desired = *dep.Spec.Replicas
		}
		if dep.Status.ObservedGeneration >= dep.Generation &&
			dep.Status.UpdatedReplicas == desired &&
			dep.Status.AvailableReplicas == desired &&
			dep.Status.UnavailableReplicas == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("rollout did not complete within %s: generation=%d/observed=%d updated=%d/%d available=%d/%d unavailable=%d",
				budget, dep.Generation, dep.Status.ObservedGeneration,
				dep.Status.UpdatedReplicas, desired,
				dep.Status.AvailableReplicas, desired,
				dep.Status.UnavailableReplicas)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

// rolloutProbeAttempts bounds the retries for a window disturbed by a
// controller rebuild. Rebuilds arrive roughly every 60s in this suite and a
// window is ~20s, so a quiet one is reached well inside this budget.
const rolloutProbeAttempts = 4

// probeOneRollout runs one rollout under a continuous probe and reports the
// verdict together with how many controller rebuilds landed inside the
// window. A non-nil verdict with rebuilds > 0 is unattributable, not a
// regression.
func probeOneRollout(
	ctx context.Context,
	t *testing.T,
	client klient.Client,
	namespace, deploymentName, host string,
	verdict rolloutVerdict,
) (float64, error) {
	t.Helper()

	proberCtx, stopProber := context.WithCancel(ctx)
	var probeWG sync.WaitGroup
	snapshotter := newProberSnapshotter(t, namespace)
	results := &probeRecorder{snapshotter: snapshotter}

	probeWG.Add(1)
	go func() {
		defer probeWG.Done()
		runProbeLoop(proberCtx, t, host, results)
	}()

	finish := func() {
		stopProber()
		probeWG.Wait()
		results.waitForSnapshots()
	}

	// Brief warm-up so the asserter has baseline samples to compare against
	// the restart-window samples.
	time.Sleep(2 * time.Second)
	baselineCount := results.count()
	rebuildsBefore := controllerReinitializationsFor(ctx, t, client)

	if err := triggerRollingRestart(ctx, client, namespace, deploymentName); err != nil {
		finish()
		t.Fatalf("trigger rollout restart: %v", err)
	}

	if err := waitForDeploymentRolloutComplete(ctx, client, namespace, deploymentName, 90*time.Second); err != nil {
		finish()
		t.Fatalf("rollout did not complete cleanly: %v", err)
	}

	// Trailing grace window: the kubelet may keep the old pod's EndpointSlice
	// entry briefly past Ready=true on the new one, and HAProxy reload latency
	// can stretch into this window. The bug fires here in the buggy build; the
	// fix should keep this window clean too.
	time.Sleep(5 * time.Second)
	finish()

	// A rebuild resets this counter, so compare magnitudes: a decrease means
	// the controller restarted mid-window, which disturbs it just as much.
	rebuilds := math.Abs(controllerReinitializationsFor(ctx, t, client) - rebuildsBefore)
	if rebuilds == 0 {
		if since, why := controllerStillWarming(ctx, t, client); since {
			t.Logf("controller is still warming: %s", why)
			rebuilds = 1
		}
	}
	return rebuilds, evaluateProbeRun(t, results, baselineCount, verdict)
}

// controllerWarmUpWindow is how long after a controller container starts its
// render graph is still cold enough to delay endpoint propagation.
//
// A failover leaves the new leader rendering from an empty incremental graph,
// and the observed CI failures sat 73s and 82s after one.
const controllerWarmUpWindow = 3 * time.Minute

// controllerStillWarming reports whether a controller container started recently
// enough that the fleet is not yet being served at steady-state latency.
//
// The reinitialization counter cannot answer this. A container restart resets
// that counter, so a restart that happened *before* the probe window leaves
// both samples equal and the delta reads zero — precisely when the disruption
// was largest. The restart's cost outlives the restart: the new leader renders
// from an empty graph for seconds afterwards.
func controllerStillWarming(ctx context.Context, t *testing.T, client klient.Client) (warming bool, podName string) {
	t.Helper()
	cs, err := newClientsetForE2E(client.RESTConfig())
	if err != nil {
		t.Logf("controller warm-up scrape: %v (tolerated)", err)
		return false, ""
	}
	pods, err := cs.CoreV1().Pods(ControllerNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: LabelSelectorController,
	})
	if err != nil {
		t.Logf("controller warm-up scrape: %v (tolerated)", err)
		return false, ""
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		for j := range pod.Status.ContainerStatuses {
			status := &pod.Status.ContainerStatuses[j]
			if status.Name != "controller" || status.State.Running == nil {
				continue
			}
			age := time.Since(status.State.Running.StartedAt.Time)
			if age < controllerWarmUpWindow {
				return true, fmt.Sprintf("%s restarted %s ago (restarts=%d)",
					pod.Name, age.Round(time.Second), status.RestartCount)
			}
		}
	}
	return false, ""
}

// evaluateProbeRun returns nil when the run satisfies verdict, and the verdict
// to report otherwise. It does not fail the test itself: the caller retries an
// unattributable window rather than surrendering the assertion.
func evaluateProbeRun(t *testing.T, rec *probeRecorder, baselineCount int64, verdict rolloutVerdict) error {
	t.Helper()
	failures := rec.snapshotFailures()
	total := rec.count()

	// One probe after the baseline is enough to make the assertion
	// non-vacuous. Parallel tests' reloads slow the loop ~10x, so a higher
	// floor would flag a slow prober rather than a stuck one.
	probesDuringRollout := total - baselineCount
	if probesDuringRollout < 1 {
		t.Fatalf("probe loop fired only %d requests during the rollout window (baseline before restart: %d, total: %d) — the prober may have been wedged.",
			probesDuringRollout, baselineCount, total)
	}

	if len(failures) == 0 {
		t.Logf("clean: %d total probes (%d during rollout window after %d-probe baseline), zero non-2xx/3xx",
			total, probesDuringRollout, baselineCount)
		return nil
	}
	if err := verdict(failures); err != nil {
		return fmt.Errorf("%w(%d total probes, %d during rollout window)", err, total, probesDuringRollout)
	}
	t.Logf("accepted %d failure(s) out of %d probes:\n%s", len(failures), total, formatProbeFailures(failures))
	return nil
}
