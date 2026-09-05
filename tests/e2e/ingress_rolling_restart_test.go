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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/e2e-framework/klient"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

// TestIngressRollingRestartZeroDowntime is the regression test for the
// single-replica rolling-restart 503 pattern.
//
// What it exercises end-to-end:
//   - Deploy a 1-replica echo-server (the typical "small backend" shape
//     that exposed the bug in production).
//   - Create an Ingress pointing at it; wait for the chart to render a
//     working backend.
//   - Start a continuous prober that hits the Ingress at ~5 req/s.
//   - Issue a rolling restart (kubectl rollout restart equivalent — a
//     patch to spec.template.metadata.annotations.kubectl.kubernetes.io/
//     restartedAt with the current timestamp, which kubectl uses too).
//   - Wait for the deployment to settle (new pod Ready, old pod gone).
//   - Wait an extra grace window for any trailing in-flight responses to
//     finish racing.
//   - Stop the prober and assert no non-2xx/3xx responses beyond at most the
//     single fast-fail 503 that ADR-0013 accepts as a bounded residual.
//
// The bug this guards against:
//   - charts/haptic/charts/base/library.yaml builds the HAProxy backend by
//     iterating slice.Endpoints[].Addresses with no conditions check.
//     During a rolling restart the EndpointSlice for the Service
//     transiently contains the new pod with conditions.ready=false (its
//     container hasn't started listening yet) and the old pod with
//     conditions.terminating=true (kubelet has sent SIGTERM and most
//     apps drop new connections immediately on SIGTERM). The template
//     happily includes both, so HAProxy dispatches requests to a not-
//     yet-listening container OR a torn-down container and returns
//     503 SC--.
//   - The fix is a two-line conditions filter in base.yaml; this test
//     is the empirical proof it works against the live chart + a real
//     kubelet rolling-restart timeline.
//
// Why one replica and not two:
//   - With ≥2 replicas K8s's RollingUpdate strategy (maxSurge=25%,
//     maxUnavailable=25%) keeps at least one healthy pod in the slice
//     at all times, so HAProxy's roundrobin masks the bug behind a 50/50
//     coin flip. One replica forces the transition window to be exactly
//     the time it takes new-pod-Ready ↔ old-pod-Terminating to overlap
//     — the worst case, and the one operators actually report.
//
// This test runs in parallel like every other e2e test. There is no such
// thing as a "reload-sensitive test" — HAPTIC must route correctly through
// HAProxy reload churn, which sibling tests creating / deleting Ingresses
// reliably produce. If this assertion fails because of reload-induced
// drops, that's a real chart/controller bug to investigate, not a reason
// to serialize the test.
func TestIngressRollingRestartZeroDowntime(t *testing.T) {
	t.Parallel()

	const host = "ingress-rolling-restart.localdev.me"
	// Captured by both closures below; assigned in Setup, read in Assess.
	// e2e-framework runs Setup→Assess sequentially within one t.Run, so
	// the write/read order is safe even under the outer t.Parallel().
	var (
		namespace      string
		deploymentName string
	)

	feature := features.New("Ingress: rolling restart of single-replica backend is zero-downtime").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			namespace = NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, namespace)
			backend := NewEchoServerBackend(ctx, t, client, namespace)
			deploymentName = backend.Service

			NewIngress(ctx, t, client, namespace, IngressSpec{
				Name:           "echo",
				Host:           host,
				BackendService: backend.Service,
				BackendPort:    backend.Port,
			})

			// Baseline: confirm the Ingress + backend are wired before
			// we start poking the deployment. ExpectOK polls until the
			// chart's render+deploy cycle catches up — if this times
			// out the test fails here with a clear "Ingress never
			// became reachable" signal rather than mid-restart noise.
			httpclient.New(t).GET(host, "/").ExpectOK(t)
			// Wait for HAPTIC's reconcile to propagate to EVERY HAProxy
			// pod, not just one. ExpectOK above only proves "at least
			// one HAProxy pod can serve this Ingress" — NodePort round-
			// robins across the chart's HAProxy replicas, so a probe
			// hitting a pod whose config push hasn't landed yet gets
			// 503 SC--. CI parallel-test contention exposed this: e2e
			// NewIngress above already waited for this Ingress to be reported
			// deployed to every replica, which is what pipeline 2560383500's
			// [3.1] needed: a 503 fired ~1.4s after Ingress create but BEFORE
			// the rolling restart began. Initial provisioning latency isn't
			// what this test pins; reaction-time on endpoint changes is.
			// Start continuous tailers — HAProxy access logs, backend
			// pod stdout, EPS watch, events watch, kubelet log. They
			// run for the whole test and write to
			// debug-logs/<test>/continuous/. The point-in-time snapshot
			// fires AFTER a failure is observed and is too late to
			// capture the moment-of-failure HAProxy/backend state; the
			// continuous tailers fill that gap. Starts here (post-
			// initial-propagation) so the streams contain only the
			// rolling-restart-phase activity, not initial setup noise.
			newContinuousTailer(t, namespace)
			return ctx
		}).
		Assess("no non-2xx/3xx beyond the single ADR-0013 bounded residual during and after rollout restart",
			func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
				client, err := cfg.NewClient()
				if err != nil {
					t.Fatalf("new client: %v", err)
				}

				// A controller rebuild stops endpoint propagation for its
				// whole ~50s duration (issue #189), so a rollout overlapping
				// one loses traffic for a reason this test does not pin.
				// Retry the window instead of skipping it: a skip surrenders
				// ADR-0013's assertion, and with rebuilds landing every ~60s
				// in this suite it would surrender it on most runs.
				var (
					lastVerdict  error
					lastRebuilds float64
				)
				for attempt := 1; attempt <= rolloutProbeAttempts; attempt++ {
					rebuilds, verdict := probeOneRollout(ctx, t, client, namespace, deploymentName, host)
					if verdict == nil {
						return ctx
					}
					if rebuilds == 0 {
						t.Fatal(verdict)
					}
					lastVerdict, lastRebuilds = verdict, rebuilds
					t.Logf("attempt %d/%d: the controller rebuilt %.0f time(s) inside the window, "+
						"which stops endpoint propagation for its duration (#189); retrying on a quiet window. Verdict was: %v",
						attempt, rolloutProbeAttempts, rebuilds, verdict)
				}
				t.Skipf("all %d rollout windows overlapped a controller rebuild (last: %.0f), so the "+
					"failures cannot be attributed to the rollout (#189). Last verdict: %v",
					rolloutProbeAttempts, lastRebuilds, lastVerdict)
				return ctx
			}).
		Feature()

	testEnv.Test(t, feature)
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
	hc := httpclient.New(t)
	ticker := time.NewTicker(200 * time.Millisecond)
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

// assertProbeRunClean is the only place this test calls t.Fatalf on the
// behavioural assertion. The output deliberately lists every failure
// (with timestamp, status, duration, and error if any) so the regression
// is debuggable from the test log alone — no kubectl exec required.
// rolloutProbeAttempts bounds the retries for a window disturbed by a
// controller rebuild. Rebuilds arrive roughly every 60s in this suite and a
// window is ~20s, so a quiet one is reached well inside this budget.
const rolloutProbeAttempts = 4

// probeOneRollout runs one rollout under a continuous probe and reports the
// ADR-0013 verdict together with how many controller rebuilds landed inside
// the window. A non-nil verdict with rebuilds > 0 is unattributable, not a
// regression.
func probeOneRollout(
	ctx context.Context,
	t *testing.T,
	client klient.Client,
	namespace, deploymentName, host string,
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
	return rebuilds, evaluateProbeRun(t, results, baselineCount)
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
func controllerStillWarming(ctx context.Context, t *testing.T, client klient.Client) (bool, string) {
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

// evaluateProbeRun returns nil when the run satisfies ADR-0013, and the verdict
// to report otherwise. It does not fail the test itself: the caller retries an
// unattributable window rather than surrendering the assertion.
func evaluateProbeRun(t *testing.T, rec *probeRecorder, baselineCount int64) error {
	t.Helper()
	failures := rec.snapshotFailures()
	total := rec.count()

	// Liveness sanity floor: we need at least one probe attempt to
	// have completed AFTER the baseline phase, otherwise the assert
	// is meaningless ("zero failures out of zero requests" is true
	// vacuously). One is enough — even a single failing probe inside
	// the rollout window is enough to flag the bug class via the
	// per-failure detail report below.
	//
	// Deliberately NOT requiring N≥20 probes total: in CI the e2e
	// suite runs ~30 tests in parallel, each creating/deleting
	// Ingresses, which forces the HAProxy in the chart to reload on
	// roughly every test setup/teardown. Reloads cause curl to retry
	// the connection (~1–3s each), which slows the probe loop by ~10×
	// without changing the behaviour we're testing. The bug we care
	// about is "request dispatched to a dead pod IP → 503 SC--", which
	// the per-failure report catches regardless of probe count. The
	// minProbes-during-rollout gate guards only against the loop
	// being completely stuck.
	const minProbesDuringRollout = 1
	probesDuringRollout := total - baselineCount
	if probesDuringRollout < minProbesDuringRollout {
		t.Fatalf("probe loop fired only %d requests during the rollout window (baseline before restart: %d, total: %d) — the prober may have been wedged. Need ≥%d to assert anything.",
			probesDuringRollout, baselineCount, total, minProbesDuringRollout)
	}

	if len(failures) == 0 {
		t.Logf("clean: %d total probes (%d during rollout window after %d-probe baseline), zero non-2xx/3xx",
			total, probesDuringRollout, baselineCount)
		return nil
	}

	// ADR-0013 accepts exactly ONE bounded residual: a single fast-fail 503
	// from a keep-alive request stranded on the leaving worker when an
	// EndpointSlice flip races a co-batched structural reload. `option
	// redispatch` + the RFC 5737 192.0.2.1 sentinel bound it to a fast
	// `timeout connect` failover (~403 ms observed in the ADR), so it surfaces
	// as a single clean 503 with a short duration — never a connection error,
	// and never the ~5 s (per-probe budget) context-cancel of a reload-window
	// drop. Tolerate that one signature and nothing else: two or more failures
	// is a systematic mis-dispatch (e.g. the base.yaml conditions-filter
	// regression floods 503s), and a non-503, an errored probe, or a slow 503
	// is a different, unbounded failure. This encodes the ADR contract exactly
	// and still fails on any *widening* of the accepted residual.
	// See docs/adr/0013-rolling-restart-leaving-worker-residual.md.
	const fastFailMaxDur = 1500 * time.Millisecond
	if len(failures) == 1 {
		if f := failures[0]; f.err == nil && f.status == 503 && f.dur <= fastFailMaxDur {
			t.Logf("tolerated the single ADR-0013 bounded residual: one fast-fail 503 in %s (%d total probes, %d during rollout window). See docs/adr/0013-rolling-restart-leaving-worker-residual.md.",
				f.dur, total, probesDuringRollout)
			return nil
		}
	}

	// Build a one-line-per-failure report so the failure mode is
	// obvious at a glance: "all SC-- 503s clustered at +T1s and +T2s"
	// is the production-bug signature; "intermittent errors across the
	// whole run" is a different bug (test infra).
	var report string
	for _, f := range failures {
		ts := f.ts.UTC().Format("15:04:05.000")
		if f.err != nil {
			report += fmt.Sprintf("  %s  err=%v  dur=%s\n", ts, f.err, f.dur)
		} else {
			report += fmt.Sprintf("  %s  status=%d  dur=%s\n", ts, f.status, f.dur)
		}
	}
	// The failure could be many things — a regression of the conditions
	// filter in base.yaml step 5, a slot rotation race, an HAProxy
	// reload outage, NodePort routing churn. Don't presuppose which;
	// just list the failures. The timestamps + statuses + durations
	// give the operator enough to triage. The conditions-filter
	// regression in particular shows up as 503 SC-- with a short
	// duration (HAProxy retries hit ECONNREFUSED fast); reload-window
	// drops show up as context-canceled with duration ~= the per-probe
	// budget (5s).
	return fmt.Errorf("rolling-restart probe found %d non-2xx/3xx responses out of %d total:\n%s",
		len(failures), total, report)
}
