// Copyright 2026 Philipp Hossner
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
	"strconv"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/e2e-framework/klient"

	hapticclient "gitlab.com/haproxy-haptic/haptic/pkg/generated/clientset/versioned"
	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

// reloadFreeRespHeader is the response header the reload-free suites set on
// every route. It stays constant across cycles and the anchor so the static
// http-response line it produces is identical, leaving only the per-route map
// entry (its value) to change on the runtime lane.
const reloadFreeRespHeader = "X-Rf-Resp"

// dynamicBackendsSupported reports whether the HAProxy the suite installed can
// create and delete backends at runtime (3.4+). Below it, a route add/remove is
// a file-defined backend that only a reload installs, so the reload-free
// assertions relax to "functional plus a bounded reload count".
func dynamicBackendsSupported() bool {
	return haproxyVersionAtLeast(ChartHAProxyVersion, "3.4")
}

// haproxyVersionAtLeast compares two "major.minor" strings numerically, so 3.10
// would still rank above 3.4 (string comparison would not).
func haproxyVersionAtLeast(have, want string) bool {
	hMajor, hMinor := splitVersion(have)
	wMajor, wMinor := splitVersion(want)
	if hMajor != wMajor {
		return hMajor > wMajor
	}
	return hMinor >= wMinor
}

func splitVersion(v string) (major, minor int) {
	parts := strings.SplitN(v, ".", 3)
	major, _ = strconv.Atoi(parts[0])
	if len(parts) > 1 {
		minor, _ = strconv.Atoi(parts[1])
	}
	return major, minor
}

// reloadFingerprint is the evidence a reload did or did not happen: the fleet's
// reload counter and every HAProxy worker's start time. A reload re-executes the
// worker, which moves its start time — the hard signal — while the counter is
// the leader's own tally, kept as corroboration.
type reloadFingerprint struct {
	reloads    float64
	startTimes map[string]float64
}

func captureReloadFingerprint(ctx context.Context, t *testing.T, cs kubernetes.Interface) reloadFingerprint {
	t.Helper()
	fp := reloadFingerprint{startTimes: haproxyWorkerStartTimes(ctx, t, cs)}
	for _, v := range snapshotReloadCounters(ctx, t, cs) {
		fp.reloads += v
	}
	return fp
}

// assertReloadFree fails when anything between two fingerprints reveals a
// reload: a moved worker start time, a changed pod set, or a bumped reload
// counter. It is the ≥3.4 contract for a route add or remove.
func assertReloadFree(t *testing.T, before, after reloadFingerprint, what string) {
	t.Helper()
	if len(before.startTimes) != len(after.startTimes) {
		t.Fatalf("%s: the HAProxy pod set changed (%d→%d), which a reload-free change never does",
			what, len(before.startTimes), len(after.startTimes))
	}
	for pod, was := range before.startTimes {
		now, present := after.startTimes[pod]
		if !present {
			t.Fatalf("%s: HAProxy pod %s disappeared during the change", what, pod)
		}
		if now != was {
			t.Fatalf("%s: HAProxy pod %s re-executed its worker (start time %.0f→%.0f): the change reloaded",
				what, pod, was, now)
		}
	}
	if after.reloads > before.reloads {
		t.Fatalf("%s: the fleet reload counter advanced by %.0f", what, after.reloads-before.reloads)
	}
}

const (
	dynamicReactionTimeout     = 30 * time.Second  // >=3.4: reaction is a runtime op (~ms).
	reloadStallCeiling         = 120 * time.Second // <3.4: no reload for this long with the reaction unmet ⇒ wedge, not slowness.
	reloadReactionBackstop     = 5 * time.Minute   // Absolute backstop; the primary bound is reload progress.
	reloadReactionPollInterval = 2 * time.Second
	reloadScrapeCadence        = 10 * time.Second // Worker-restart scrape cadence: ample for the stall ceiling, light on the shared apiserver.
)

// reloadFreeReaction waits for reached (the assertion, unchanged), bounded by fleet
// reload progress on <3.4 and a flat budget on >=3.4 — issue #174.
func reloadFreeReaction(
	ctx context.Context, t *testing.T, cs kubernetes.Interface, desc string,
	reached func(context.Context) (bool, error),
) {
	t.Helper()
	if dynamicBackendsSupported() {
		if err := testutil.WaitForConditionWithDescription(ctx, testutil.WaitConfig{
			InitialInterval: 100 * time.Millisecond,
			MaxInterval:     time.Second,
			Timeout:         dynamicReactionTimeout,
			Multiplier:      1.5,
		}, desc, reached); err != nil {
			t.Fatalf("%s: %v", desc, err)
		}
		return
	}
	waitReloadPacedReaction(ctx, t, cs, desc, reached)
}

// waitReloadPacedReaction waits for reached while any HAProxy worker keeps
// reloading; it fails only on a reload stall (wedge) or the absolute backstop, so
// an unbounded reload backlog never times it out. A scrape blip is "not yet
// observed", never an abort: it keeps the prior start-times and keeps polling.
func waitReloadPacedReaction(
	ctx context.Context, t *testing.T, cs kubernetes.Interface, desc string,
	reached func(context.Context) (bool, error),
) {
	t.Helper()
	start := time.Now()
	lastStartTimes, _ := haproxyWorkerStartTimesE(ctx, cs)
	lastReload := start
	lastScrape := start
	lastErr := fmt.Errorf("no observation yet")

	for {
		if ok, err := reached(ctx); ok {
			return
		} else if err != nil {
			lastErr = err
		}

		if time.Since(lastScrape) >= reloadScrapeCadence {
			lastScrape = time.Now()
			if now, err := haproxyWorkerStartTimesE(ctx, cs); err != nil {
				lastErr = err
			} else if !sameStartTimes(lastStartTimes, now) {
				lastStartTimes = now
				lastReload = time.Now()
			}
		}
		if stalled := time.Since(lastReload); stalled >= reloadStallCeiling {
			t.Fatalf("%s: no HAProxy worker reloaded for %s and the change never landed (last: %v); "+
				"a reload-gated fleet applies a route add/remove only via a reload, so a fleet that "+
				"stopped reloading will not apply it", desc, stalled.Round(time.Second), lastErr)
		}
		if time.Since(start) >= reloadReactionBackstop {
			t.Fatalf("%s: not reached within the %s backstop while the fleet kept reloading (last: %v); "+
				"the change appears never to be rendered into the config", desc, reloadReactionBackstop, lastErr)
		}

		select {
		case <-ctx.Done():
			t.Fatalf("%s: %v (last: %v)", desc, ctx.Err(), lastErr)
		case <-time.After(reloadReactionPollInterval):
		}
	}
}

// waitFleetQuiescent blocks until the fleet is a safe baseline for a reload-free
// measurement: the render gate has accepted the published render (it is not
// holding after a refusal) AND no HAProxy worker has re-executed for a sustained
// window. The suites share one fleet: a sibling test's teardown (deleting a
// Gateway is structural), the anchor route's own first-appearance reload, a
// capabilities change, or — the case issue #170 hit — apply_rollback driving the
// render gate PESSIMISTIC all leave work in flight that would be charged to a
// cycle it did not cause. Draining both the reloads and the gate first is what
// makes the per-cycle assertion about the cycle, not the neighbourhood.
//
// The gate check reads the HAProxyCfg conditions rather than a port-forwarded
// debug endpoint, so it needs no tunnel of its own — the very thing whose stalls
// #170 also hardens.
func waitFleetQuiescent(ctx context.Context, t *testing.T, client klient.Client, cs kubernetes.Interface) {
	t.Helper()
	const stableFor = 8 * time.Second
	hc, err := hapticclient.NewForConfig(client.RESTConfig())
	if err != nil {
		t.Fatalf("build haptic clientset: %v", err)
	}
	var last map[string]float64
	stableSince := time.Now()
	err = testutil.WaitForConditionWithDescription(ctx, testutil.WaitConfig{
		InitialInterval: time.Second,
		MaxInterval:     time.Second,
		Timeout:         2 * time.Minute,
		Multiplier:      1,
	}, fmt.Sprintf("render gate open and HAProxy worker start times stable for %s", stableFor), func(ctx context.Context) (bool, error) {
		if err := renderGateOpen(ctx, hc); err != nil {
			last = nil
			stableSince = time.Now()
			return false, err
		}
		now := haproxyWorkerStartTimes(ctx, t, cs)
		if last == nil || !sameStartTimes(last, now) {
			last = now
			stableSince = time.Now()
			return false, fmt.Errorf("fleet still reloading")
		}
		return time.Since(stableSince) >= stableFor, nil
	})
	if err != nil {
		t.Fatalf("fleet never reached a gate-open, quiescent baseline before the reload-free cycles: %v", err)
	}
}

// renderGateOpen reports nil when the render gate has accepted the published
// render — ConfigValidated=True and not ConfigPinned — meaning it is OPTIMISTIC
// and will not hold the next render for a synchronous verdict. A refusal (a
// sibling test's broken input) leaves ConfigValidated=False until a passing
// render reopens it, which is exactly what a reload-free measurement must wait
// out. It reads the same conditions `kubectl describe haproxycfg` shows, so no
// port-forward is involved.
func renderGateOpen(ctx context.Context, hc hapticclient.Interface) error {
	cfgName := HAProxyConfigName + "-haproxycfg"
	obj, err := hc.HaproxyTemplateICV1alpha1().HAProxyCfgs(ControllerNamespace).Get(ctx, cfgName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get HAProxyCfg %s: %w", cfgName, err)
	}
	validated := meta.FindStatusCondition(obj.Status.Conditions, "ConfigValidated")
	if validated == nil || validated.Status != metav1.ConditionTrue {
		status := "absent"
		if validated != nil {
			status = string(validated.Status)
		}
		return fmt.Errorf("render gate has not accepted the published render (ConfigValidated=%s)", status)
	}
	if pinned := meta.FindStatusCondition(obj.Status.Conditions, "ConfigPinned"); pinned != nil && pinned.Status == metav1.ConditionTrue {
		return fmt.Errorf("render gate is pinned, holding renders")
	}
	return nil
}

// firstHAProxyPod names one HAProxy pod, for the socat runtime queries that need
// a concrete pod to exec into.
func firstHAProxyPod(ctx context.Context, t *testing.T, cs kubernetes.Interface) string {
	t.Helper()
	pods, err := cs.CoreV1().Pods(ControllerNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: LabelSelectorHAProxy,
	})
	if err != nil {
		t.Fatalf("list HAProxy pods: %v", err)
	}
	for i := range pods.Items {
		return pods.Items[i].Name
	}
	t.Fatalf("no HAProxy pods match %q", LabelSelectorHAProxy)
	return ""
}

// showMap runs `show map <path>` on one pod's worker socket and returns the raw
// output. The path is the base-relative name HAProxy prints and accepts, e.g.
// maps/host.map, which is the manifest path with no translation.
func showMap(ctx context.Context, t *testing.T, cs kubernetes.Interface, path string) string {
	t.Helper()
	pod := firstHAProxyPod(ctx, t, cs)
	command := "printf 'show map " + path + "\\n' | socat -t5 stdio unix-connect:" + haproxyWorkerSocketPath
	out, err := execInHAProxyPod(ctx, pod, "haproxy", "sh", "-c", command)
	if err != nil {
		t.Fatalf("show map %s on %s: %v\n%s", path, pod, err, out)
	}
	return out
}

// haproxyWorkerSocketPath is where the chart's rendered and bootstrap global put
// the worker stats socket the agent (and these queries) talk to.
const haproxyWorkerSocketPath = "/etc/haproxy/haproxy-worker.sock"

// backendInRuntime reports whether the running worker has a backend of this
// name, read from `show stat` over the worker socket — the runtime truth, not
// the on-disk file (which carries the section whether or not it was applied at
// runtime). It is how the custom-CRD cycle proves a Route's backend was added
// and removed at runtime.
func backendInRuntime(ctx context.Context, t *testing.T, cs kubernetes.Interface, backend string) bool {
	t.Helper()
	pod := firstHAProxyPod(ctx, t, cs)
	command := "printf 'show stat\\n' | socat -t5 stdio unix-connect:" + haproxyWorkerSocketPath
	out, err := execInHAProxyPod(ctx, pod, "haproxy", "sh", "-c", command)
	if err != nil {
		t.Fatalf("show stat on %s: %v\n%s", pod, err, out)
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(line, ",")
		if len(fields) > 1 && fields[0] == backend && fields[1] == "BACKEND" {
			return true
		}
	}
	return false
}

// mapDivergenceTotal sums haptic_runtime_map_divergence_total across the
// controller pods. A runtime map that failed its read-back and forced a reload
// fallback moves it, so a zero delta across a cycle is what proves the map ops
// stayed on the runtime lane.
func mapDivergenceTotal(ctx context.Context, t *testing.T, cs kubernetes.Interface) float64 {
	t.Helper()
	var total float64
	scraped := 0
	for pod := range controllerPodNames(ctx, t, cs) {
		value, err := labelledMetricSum(ctx, cs, pod, "haptic_runtime_map_divergence_total")
		if err != nil {
			t.Logf("map-divergence scrape: %v (tolerated)", err)
			continue
		}
		scraped++
		total += value
	}
	if scraped == 0 {
		t.Fatal("map-divergence scrape: no controller pod's /metrics was reachable")
	}
	return total
}
