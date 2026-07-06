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
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"sigs.k8s.io/e2e-framework/klient"

	hapticv1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	hapticclient "gitlab.com/haproxy-haptic/haptic/pkg/generated/clientset/versioned"
	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

// controllerForgetTimeout caps how long the test cleanup waits for the
// controller's rendered config to no longer mention a deleted Ingress's
// namespace. The controller's informer typically catches up in well under
// 1s, but we allow a generous budget so transient API-server slowness
// during parallel teardown doesn't cause spurious cleanup-warning logs.
const controllerForgetTimeout = 10 * time.Second

// controllerDeployedTimeout caps the post-apply wait for the HAProxyCfg
// status to report every HAProxy pod at a render containing the marker.
// Convergence is bounded by the controller's own pacing: reconcile debounce
// (≤2s) + one deploy interval (minDeploymentInterval, 5s chart default) + the
// per-pod Sync/reload (~1-2s). Latest-wins coalescing means a freshly applied
// resource rides the NEXT deploy regardless of how many sibling tests churn
// concurrently, so ~7s is the realistic worst case. 12s is the 2x-headroom
// cap: generous enough to never flake on a healthy controller, tight enough
// that a genuine convergence regression fails the test loudly instead of
// hiding behind a 90s budget (a wait that legitimately needs >12s here would
// itself be the bug).
const controllerDeployedTimeout = 12 * time.Second

// waitForControllerForgetNamespace polls the controller's /debug/vars/rendered
// endpoint until the rendered haproxy.cfg no longer contains the given
// namespace. This is used during test cleanup, after an Ingress has been
// explicitly deleted from the API server, to bound the window in which the
// controller's resource-store still contains the (now stale) Ingress.
//
// The race we close: apiserver Delete returns synchronously, but the
// controller's watcher has its own latency. While the watcher is catching
// up, the controller's render still includes the Ingress; if another
// parallel test's webhook validation fires during that window and the
// referenced Secret has already been cascade-deleted, the render fails
// with an [ALERT]-level "unable to find userlist" / "Secret does not
// exist" error and admission is denied for the unrelated resource.
//
// On timeout we log and return without failing the test — cleanup is
// best-effort, and the test that triggered the cleanup has already
// completed. The cap exists so a stuck controller can't wedge the suite.
func waitForControllerForgetNamespace(ctx context.Context, t *testing.T, client klient.Client, namespace string) {
	t.Helper()

	// Read the rendered config straight from the HAProxyCfg CR (the same source
	// waitForControllerDeployed uses) rather than the apiserver service-proxy to
	// the controller's debug endpoint. The proxy path (ProxyGet -> controller
	// :8080, fetching the full rendered config every poll) strains under
	// parallel-test cleanup churn and returns the opaque
	// `an error on the server ("unknown")`; a direct CR GET is served from the
	// apiserver's watch cache and never proxies to a pod.
	hc, err := hapticclient.NewForConfig(client.RESTConfig())
	if err != nil {
		t.Logf("waitForControllerForgetNamespace: build haptic clientset: %v (skipping wait)", err)
		return
	}
	cfgName := HAProxyConfigName + "-haproxycfg"

	cfg := testutil.FastWaitConfig()
	cfg.Timeout = controllerForgetTimeout

	err = testutil.WaitForConditionWithDescription(ctx, cfg,
		"controller forgot namespace "+namespace,
		func(ctx context.Context) (bool, error) {
			obj, err := hc.HaproxyTemplateICV1alpha1().HAProxyCfgs(ControllerNamespace).Get(ctx, cfgName, metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			if obj.Spec.Compressed {
				return false, fmt.Errorf("HAProxyCfg.spec.content is compressed; forget-namespace wait does not decompress")
			}
			return !strings.Contains(obj.Spec.Content, namespace), nil
		})
	if err != nil {
		t.Logf("waitForControllerForgetNamespace %q: %v (cleanup proceeding anyway)", namespace, err)
	}
}

// waitForControllerDeployed blocks until the controller's HAProxyCfg reports
// that EVERY HAProxy pod has applied a render whose spec.Content contains the
// supplied marker — i.e. the resource our caller just applied is live on all
// pods.
//
// This is the controller's authoritative post-convergence signal, surviving
// across reconciliation cycles (unlike /debug/vars/pipeline, whose phase status
// is wiped on every new trigger).
//
// Membership, not "== latest spec.Checksum": under the full parallel suite the
// controller re-renders constantly — every sibling test's Ingress create bumps
// the whole-config checksum — so the pods run ~1 render behind a target that
// never stops moving, and an equality check against the latest spec.Checksum
// can never converge. Instead we accumulate the set of spec.Checksums OBSERVED
// to contain the marker and pass when every pod is at one of them. A content
// checksum uniquely identifies the rendered config, so a pod reporting a
// checksum in that set has provably deployed a config containing the marker.
// Membership never yields false-positives (a pod cannot be in the set without
// the marker); its only failure mode is failing to OBSERVE the marker-bearing
// checksum a pod settled on — so the observed set must be COMPLETE and the read
// must be FRESH.
//
// We WATCH the HAProxyCfg rather than poll it, which delivers both guarantees a
// poll could not:
//   - Completeness: every spec.Checksum transition arrives as an event, so we
//     never miss the one a pod sits at. (A poll backing off to seconds sampled
//     only a fraction — the controller republishes spec.Checksum several times
//     faster than it deploys — so a pod could settle on a checksum never
//     sampled-while-live, hanging the wait though the cluster had converged.)
//   - Freshness: each event carries the current object, eliminating the
//     stale-read window seen on CI where repeated GETs returned a ~16s-old
//     checksum while the CR was already correct.
//
// Completeness now rests on the apiserver watch contract (ordered, no missed
// events within the history window); when our resourceVersion expires (410
// Gone) we resync via a Get and re-fold current state before re-watching, so an
// expiry can never drop the transition we needed.
//
//   - spec.Checksum is what the publisher wrote for a render (config +
//     auxiliary file content fed through dataplane.ComputeContentChecksum, the
//     same function the deployer stamps onto each ConfigAppliedToPodEvent.Checksum).
//   - status.deployedToPods[].Checksum carries that per-pod checksum, updated
//     when the deployer's per-endpoint Sync returns success (the dataplane API
//     confirmed reload via VerifyReload polling).
//
// Choosing the marker. It must appear in spec.Content ONLY once the resource
// the caller just applied has rendered — otherwise the gate reports
// convergence off an unrelated, earlier render and the caller races the real
// deploy. For Ingress tests the namespace is a fine marker: it enters
// spec.Content only via the Ingress's own backend, so it can't appear before
// the Ingress renders. For Gateway/HTTPRoute/GRPCRoute tests the namespace is
// NOT sufficient: the Gateway injects the namespace into spec.Content via the
// route-independent `# typed-access-smoke: ns=<ns> ...` global comment
// (charts/haptic/charts/gateway/05-typed-access-smoke.yaml), which renders when
// the Gateway is created — before the route exists. A namespace marker would
// then pass off that pre-route render while the route's own (throttled)
// structural deploy is still in flight (issue #71). Use a route-gated marker
// instead: the backend-name fragment "gtw_<ns>_<routeName>_" appears only once
// the route's backends render, and <ns> is unique per test so it can't
// cross-match a sibling test's route.
//
// An initial Get (so an already-converged CR passes instantly) plus a single
// long-lived watch — no service proxy, no per-tick GET storm — keeps this cheap
// under parallel-test load; earlier revisions that fetched /debug/vars/rendered
// hammered the API-server proxy and got "unknown" 5xx errors as it buckled.
//
// Fail on timeout: every assertion that follows assumes convergence;
// silently proceeding would just cascade into a misleading later
// failure.
func waitForControllerDeployed(ctx context.Context, t *testing.T, client klient.Client, marker string) {
	t.Helper()

	hc, err := hapticclient.NewForConfig(client.RESTConfig())
	if err != nil {
		t.Fatalf("waitForControllerDeployed: build haptic clientset: %v", err)
	}
	cfgName := HAProxyConfigName + "-haproxycfg"
	cfgs := hc.HaproxyTemplateICV1alpha1().HAProxyCfgs(ControllerNamespace)

	// Hard wall-clock budget. A watch carries every spec.Checksum transition and
	// every event reflects the freshest status, so controllerDeployedTimeout is
	// pure headroom (see the doc above).
	ctx, cancel := context.WithTimeout(ctx, controllerDeployedTimeout)
	defer cancel()

	// Accumulates every spec.Checksum observed (initial Get + every watch event +
	// across re-establishments) whose spec.Content contained the marker. See the
	// doc above for why membership — not equality against the latest checksum — is
	// the convergence test under parallel-suite churn.
	observedMarkerChecksums := make(map[string]struct{})

	// lastErr keeps the most recent "why not converged yet" reason so a timeout
	// fails with the same actionable detail the poll version produced.
	var lastErr error

	// evaluate folds one observed CR into the accumulator and reports whether the
	// cluster has fully converged. Identical predicate to the former poll closure.
	evaluate := func(obj *hapticv1alpha1.HAProxyCfg) (bool, error) {
		if obj.Spec.Compressed {
			// Defensive: e2e configs are well under the default 1 MiB
			// compression threshold (~60 KiB observed), so this branch should
			// never fire. If it does, raise the threshold or add a decompressor
			// here — falling through would cause a spurious "marker not in
			// rendered config" failure.
			return false, fmt.Errorf("HAProxyCfg.spec.content is compressed; e2e wait does not decompress")
		}
		if obj.Spec.Checksum != "" && strings.Contains(obj.Spec.Content, marker) {
			observedMarkerChecksums[obj.Spec.Checksum] = struct{}{}
		}
		if len(observedMarkerChecksums) == 0 {
			return false, fmt.Errorf("marker %q not yet in any observed HAProxyCfg.spec.content", marker)
		}
		deployed := obj.Status.DeployedToPods
		if len(deployed) == 0 {
			return false, fmt.Errorf("HAProxyCfg.status.deployedToPods empty (controller hasn't reported any pod yet)")
		}
		for _, p := range deployed {
			if _, ok := observedMarkerChecksums[p.Checksum]; !ok {
				return false, fmt.Errorf("pod %s at checksum %q not yet among %d observed marker-bearing checksums (latest spec %q)",
					p.PodName, p.Checksum, len(observedMarkerChecksums), obj.Spec.Checksum)
			}
		}
		return true, nil
	}

	// resourceVersion drives where each (re)watch starts: seeded by the initial
	// Get, advanced by every event so a re-established watch never replays, reset
	// to "" to force a resync after the apiserver expires our RV (410 Gone).
	var resourceVersion string

	// Phase 1: initial Get — an already-converged CR passes instantly, and the RV
	// anchors the watch so it doesn't replay history. A missing CR is NOT fatal
	// (it may not exist yet); the watch from RV "" catches its creation.
	if obj, gerr := cfgs.Get(ctx, cfgName, metav1.GetOptions{}); gerr != nil {
		lastErr = fmt.Errorf("initial get HAProxyCfg %s/%s: %w", ControllerNamespace, cfgName, gerr)
	} else {
		resourceVersion = obj.ResourceVersion
		done, reason := evaluate(obj)
		if done {
			return
		}
		lastErr = reason
	}

	// drain consumes one watch until the cluster converges (true, _), the stream
	// closes / RV expires so we must re-establish (_, true), or ctx is done
	// (false, false). It always Stop()s the watch via defer.
	drain := func(w watch.Interface) (converged, restart bool) {
		defer w.Stop()
		for {
			select {
			case <-ctx.Done():
				return false, false
			case ev, open := <-w.ResultChan():
				if !open {
					// Server closed the stream (idle timeout / rebalance):
					// re-watch from the last RV, no resync needed.
					return false, true
				}
				switch ev.Type {
				case watch.Added, watch.Modified:
					obj, ok := ev.Object.(*hapticv1alpha1.HAProxyCfg)
					if !ok || obj.Name != cfgName {
						continue
					}
					resourceVersion = obj.ResourceVersion
					done, reason := evaluate(obj)
					if done {
						return true, false
					}
					lastErr = reason
				case watch.Bookmark:
					// Carries only an advanced RV (cheap restarts), never state.
					if obj, ok := ev.Object.(*hapticv1alpha1.HAProxyCfg); ok {
						resourceVersion = obj.ResourceVersion
					}
				case watch.Deleted:
					// Only react to OUR object: the watch is namespace-scoped, so a
					// sibling HAProxyCfg's deletion must not reset our wait state
					// (mirrors the name guard on Added/Modified above).
					obj, ok := ev.Object.(*hapticv1alpha1.HAProxyCfg)
					if !ok || obj.Name != cfgName {
						continue
					}
					// Teardown race: a sibling cleanup removed the CR. It can't be
					// converged-and-gone; keep the accumulator (a marker-bearing
					// checksum already seen is still proof the marker rendered) and
					// resync on the next watch.
					resourceVersion = ""
					lastErr = fmt.Errorf("HAProxyCfg %s deleted during wait", cfgName)
				case watch.Error:
					// Our RV fell out of the apiserver's history window (410 Gone).
					// Resync via a fresh Get (which may itself reveal convergence),
					// then re-watch from the new RV.
					resourceVersion = ""
					if obj, gerr := cfgs.Get(ctx, cfgName, metav1.GetOptions{}); gerr == nil {
						resourceVersion = obj.ResourceVersion
						done, reason := evaluate(obj)
						if done {
							return true, false
						}
						lastErr = reason
					} else {
						// Record the resync failure itself so a timeout during a
						// persistent apiserver-error window reports the real blocker,
						// not a stale earlier reason.
						lastErr = fmt.Errorf("resync get on watch error (410 Gone): %w", gerr)
					}
					return false, true
				}
			}
		}
	}

	// Phase 2: watch from resourceVersion, re-establishing until convergence or
	// the deadline.
	for {
		if cerr := ctx.Err(); cerr != nil {
			t.Fatalf("waitForControllerDeployed %q: %v (last state: %v)", marker, cerr, lastErr)
		}
		w, werr := cfgs.Watch(ctx, metav1.ListOptions{
			ResourceVersion:     resourceVersion,
			AllowWatchBookmarks: true,
		})
		if werr != nil {
			if ctx.Err() != nil {
				t.Fatalf("waitForControllerDeployed %q: open watch: %v (last state: %v)", marker, werr, lastErr)
			}
			// Transient open failure: brief backoff so a persistently-failing
			// apiserver can't hot-spin this loop until the deadline.
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if converged, _ := drain(w); converged {
			return
		}
	}
}
