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

package deployer

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
)

// TestHandleDeploymentCompleted_UsesEventChecksumNotLatestRender pins
// the load-bearing invariant that handleDeploymentCompleted records the
// checksum of the deployment that JUST COMPLETED, not whatever render
// happens to be the most recent in s.lastContentChecksum at completion
// time.
//
// Without this contract, the following race silently drops deployments:
//
//  1. Render A produces checksum X. handleTemplateRendered caches X.
//  2. DeploymentScheduler schedules deploy A with ContentChecksum=X.
//  3. Deploy A's data-plane push begins (slow — many endpoints, many
//     backends).
//  4. Reconcile triggered by an unrelated resource change. Render B
//     produces checksum Y. handleTemplateRendered OVERWRITES the
//     cached checksum to Y. (Both handlers serialize on s.mu, but
//     semantically the cache now reflects render B, not deploy A.)
//  5. Deploy A finishes. handleDeploymentCompleted fires.
//  6. Bug: lastDeployedConfigHash = s.lastContentChecksum = Y.
//     We just deployed X, but the cache now claims Y is deployed.
//  7. A subsequent reconcile renders Y again (extremely common —
//     Y might be a "steady-state" hash). canSkip predicate sees
//     configHash (Y) == lastDeployedConfigHash (Y) → SKIP.
//     Render Y's content never reaches HAProxy.
//
// Real-world symptom (CI pipeline 2551671212, TestIngressHaproxyRedirectTo
// on HAProxy 3.3): a fresh Ingress's `http-request redirect` directive
// was present in the HAProxyCfg CR but absent from the live haproxy.cfg
// inside the pod; the test asserted 302 and got 200 from the backend
// echo server for the full 3-minute retry budget.
//
// Fix: the scheduler reads the deployed checksum from the event payload
// (DeploymentCompletedEvent.ContentChecksum, forwarded unchanged from
// DeploymentScheduledEvent), not from the live cache.
func TestHandleDeploymentCompleted_UsesEventChecksumNotLatestRender(t *testing.T) {
	bus := testutil.NewTestBus()
	bus.Start()
	scheduler := newDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)

	// Simulate the race window:
	//  - The deployment that just finished was for content X.
	//  - A newer render Y arrived during the deployment, overwriting
	//    the live cache.
	const (
		deployedChecksum = "checksum-X-of-the-deployment-that-just-completed"
		laterRender      = "checksum-Y-from-a-reconcile-that-arrived-mid-deploy"
	)

	scheduler.mu.Lock()
	scheduler.lastContentChecksum = laterRender // newer render overwrote the cache
	scheduler.mu.Unlock()

	event := completionForActiveDeployment(scheduler, &events.DeploymentResult{
		Total:           1,
		Succeeded:       1,
		ContentChecksum: deployedChecksum,
		PodSetHash:      "pod-set-X",
	})

	scheduler.handleDeploymentCompleted(event)

	scheduler.mu.RLock()
	recorded := scheduler.lastDeployedConfigHash
	scheduler.mu.RUnlock()

	require.Equal(t, deployedChecksum, recorded,
		"lastDeployedConfigHash must reflect the deployment that just completed "+
			"(event.ContentChecksum=%q), not the latest cached render "+
			"(s.lastContentChecksum=%q). When these diverge, the next reconcile "+
			"that produces the cached value will incorrectly hit the skip branch "+
			"and the real intervening render never reaches HAProxy.",
		deployedChecksum, laterRender)
}

func TestHandleDeploymentCompleted_UsesEventPodSetHashNotCurrentEndpoints(t *testing.T) {
	bus := testutil.NewTestBus()
	bus.Start()
	scheduler := newDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)

	oldEndpoints := []dataplane.Endpoint{{
		URL:          "https://haproxy.default.svc:5555",
		PodName:      "haproxy-0",
		PodNamespace: "default",
		PodUID:       "uid-old",
	}}
	replacementEndpoints := []dataplane.Endpoint{{
		URL:          oldEndpoints[0].URL,
		PodName:      oldEndpoints[0].PodName,
		PodNamespace: oldEndpoints[0].PodNamespace,
		PodUID:       "uid-new",
	}}
	oldPodSetHash := computePodSetHash(oldEndpoints)
	replacementPodSetHash := computePodSetHash(replacementEndpoints)
	require.NotEqual(t, oldPodSetHash, replacementPodSetHash)

	scheduler.mu.Lock()
	scheduler.currentEndpoints = replacementEndpoints
	scheduler.mu.Unlock()

	scheduler.handleDeploymentCompleted(completionForActiveDeployment(scheduler, &events.DeploymentResult{
		Total:           1,
		Succeeded:       1,
		ContentChecksum: "deployed-checksum",
		PodSetHash:      oldPodSetHash,
	}))

	scheduler.mu.RLock()
	recorded := scheduler.lastDeployedPodSetHash
	scheduler.mu.RUnlock()

	require.Equal(t, oldPodSetHash, recorded)
	require.NotEqual(t, replacementPodSetHash, recorded,
		"an old deployment completion must not certify the replacement endpoint set")
}

// TestHandleDeploymentCompleted_EmptyChecksumLeavesCacheUntouched pins
// the zero-endpoint behaviour. deployToEndpoints publishes a completion
// event with an empty ContentChecksum when there are no endpoints to
// deploy to (nothing actually shipped). The scheduler must not overwrite
// lastDeployedConfigHash in that case — otherwise we'd record "" as
// "last deployed" and either (a) force every subsequent deploy to run
// (annoying but safe) or (b) cause the canSkip predicate to compare
// against "" and behave undefinedly. Leaving the cache untouched keeps
// the previous real deployment's hash as the source of truth.
func TestHandleDeploymentCompleted_EmptyChecksumLeavesCacheUntouched(t *testing.T) {
	bus := testutil.NewTestBus()
	bus.Start()
	scheduler := newDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)

	const priorDeployedHash = "real-deploy-hash-from-the-last-actual-deploy"

	scheduler.mu.Lock()
	scheduler.lastDeployedConfigHash = priorDeployedHash
	scheduler.lastContentChecksum = "something-newer"
	scheduler.mu.Unlock()

	// Zero-endpoint deployment-completed event (no actual deploy happened).
	event := completionForActiveDeployment(scheduler, &events.DeploymentResult{
		Total: 0,
		// ContentChecksum intentionally empty
	})

	scheduler.handleDeploymentCompleted(event)

	scheduler.mu.RLock()
	got := scheduler.lastDeployedConfigHash
	scheduler.mu.RUnlock()

	require.Equal(t, priorDeployedHash, got,
		"empty ContentChecksum (zero-endpoint code path) must leave "+
			"lastDeployedConfigHash untouched — nothing was deployed, so the "+
			"previous real deploy's hash stays authoritative")
}

// TestHandleDeploymentCompleted_FailedDeployLeavesCacheUntouched pins that a
// deployment that did not fully succeed (event.Failed > 0) must NOT be recorded
// as the last-deployed hash. lastDeployedConfigHash is the "last SUCCESSFULLY
// deployed" hash the skip-unchanged gate compares against; recording a
// partial/failed deploy would make the gate refuse to re-push to the still-stale
// pods until the config changes or the drift timer fires, delaying self-heal.
// Leaving the cache at the prior good hash lets the next reconcile re-attempt
// the same config immediately.
func TestHandleDeploymentCompleted_FailedDeployLeavesCacheUntouched(t *testing.T) {
	bus := testutil.NewTestBus()
	bus.Start()
	scheduler := newDeploymentScheduler(bus, testutil.NewTestLogger(), 0, 30*time.Second)

	const priorDeployedHash = "real-deploy-hash-from-the-last-successful-deploy"

	scheduler.mu.Lock()
	scheduler.lastDeployedConfigHash = priorDeployedHash
	scheduler.mu.Unlock()

	// Partial-failure completion: 2 endpoints, 1 succeeded, 1 failed. The new
	// checksum must NOT become the last-deployed hash.
	event := completionForActiveDeployment(scheduler, &events.DeploymentResult{
		Total:           2,
		Succeeded:       1,
		Failed:          1,
		ContentChecksum: "checksum-of-the-partially-failed-deploy",
	})

	scheduler.handleDeploymentCompleted(event)

	scheduler.mu.RLock()
	got := scheduler.lastDeployedConfigHash
	scheduler.mu.RUnlock()

	require.Equal(t, priorDeployedHash, got,
		"a partial/failed deployment (event.Failed=%d) must leave "+
			"lastDeployedConfigHash untouched so the skip-unchanged gate keeps "+
			"re-attempting until every pod converges", event.Failed)
}
