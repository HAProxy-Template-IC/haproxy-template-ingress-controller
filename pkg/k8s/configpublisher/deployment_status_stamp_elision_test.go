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

package configpublisher

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/generated/clientset/versioned/fake"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8stesting "k8s.io/client-go/testing"
)

// ssaStatusPatchCounter counts Server-Side Apply patches on the /status
// subresource, keyed by resource plural (e.g. "haproxymapfiles"). This is the
// exact write issue #163 elides: each per-pod aux-file re-stamp is one throttled
// SSA status PATCH.
type ssaStatusPatchCounter struct {
	mu     sync.Mutex
	counts map[string]int
}

const (
	resHAProxyCfgs         = "haproxycfgs"
	resHAProxyMapFiles     = "haproxymapfiles"
	resHAProxyGeneralFiles = "haproxygeneralfiles"
	resHAProxyCRTListFiles = "haproxycrtlistfiles"
)

// installSSAStatusPatchCounter wires a counting reactor that sits in front of
// the merge reactor. It only observes SSA status patches and always falls
// through (returns false), so it never changes behaviour.
func installSSAStatusPatchCounter(c *fake.Clientset) *ssaStatusPatchCounter {
	counter := &ssaStatusPatchCounter{counts: make(map[string]int)}
	c.PrependReactor("patch", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		pa, ok := action.(k8stesting.PatchAction)
		if !ok || pa.GetPatchType() != types.ApplyPatchType || pa.GetSubresource() != statusSubresource {
			return false, nil, nil
		}
		counter.mu.Lock()
		counter.counts[pa.GetResource().Resource]++
		counter.mu.Unlock()
		return false, nil, nil
	})
	return counter
}

func (c *ssaStatusPatchCounter) get(resource string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[resource]
}

// aux returns the total SSA status patches across all three auxiliary-file kinds.
func (c *ssaStatusPatchCounter) aux() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[resHAProxyMapFiles] + c.counts[resHAProxyGeneralFiles] + c.counts[resHAProxyCRTListFiles]
}

// auxPublishRequest publishes one map, one general, and one crt-list file so a
// pod-status update stamps three auxiliary-file CRs.
func auxPublishRequest() PublishRequest {
	req := basePublishRequest()
	req.AuxiliaryFiles = &AuxiliaryFiles{
		MapFiles: []auxiliaryfiles.MapFile{
			{Path: "/etc/haproxy/maps/host.map", Content: "example.com be_a\n"},
		},
		GeneralFiles: []auxiliaryfiles.GeneralFile{
			{Filename: "503.http", Path: "/etc/haproxy/general/503.http", Content: "HTTP/1.0 503\r\n\r\n"},
		},
		CRTListFiles: []auxiliaryfiles.CRTListFile{
			{Path: "/etc/haproxy/ssl/crt-list.txt", Content: "/etc/haproxy/ssl/x.pem example.com\n"},
		},
	}
	return req
}

// newAuxPublisherWithCounter publishes the aux-file set, then installs the
// counter so only the pod-status stamps under test are counted (not the
// PublishConfig setup writes).
func newAuxPublisherWithCounter(t *testing.T) (context.Context, *Publisher, *ssaStatusPatchCounter) {
	t.Helper()
	ctx, _, crdClient, publisher := newTestPublisher(t)
	req := auxPublishRequest()
	_, err := publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)
	return ctx, publisher, installSSAStatusPatchCounter(crdClient)
}

func statusUpdate(podName, podUID, podRuntimeID, checksum string) *DeploymentStatusUpdate {
	return &DeploymentStatusUpdate{
		RuntimeConfigName:      "test-config-haproxycfg",
		RuntimeConfigNamespace: "default",
		PodName:                podName,
		PodUID:                 podUID,
		PodRuntimeID:           podRuntimeID,
		Checksum:               checksum,
	}
}

func driftStatusUpdate(podName, podUID, podRuntimeID, checksum string) *DeploymentStatusUpdate {
	u := statusUpdate(podName, podUID, podRuntimeID, checksum)
	u.IsDriftCheck = true
	return u
}

// TestUpdateDeploymentStatus_ElidesUnchangedAuxStamps is the headline win for
// issue #163: the first update stamps every aux file once, and byte-identical
// repeats issue ZERO aux-file SSA patches — while the HAProxyCfg is still
// patched every time (its checksum/plan genuinely changes per deploy).
func TestUpdateDeploymentStatus_ElidesUnchangedAuxStamps(t *testing.T) {
	ctx, publisher, counter := newAuxPublisherWithCounter(t)

	require.NoError(t, publisher.UpdateDeploymentStatus(ctx, statusUpdate("haproxy-0", "uid-0", "rt-0", "abc123")))
	assert.Equal(t, 3, counter.aux(), "first update stamps all three aux files")
	assert.Equal(t, 1, counter.get(resHAProxyCfgs), "HAProxyCfg patched on first update")

	const repeats = 5
	for range repeats {
		require.NoError(t, publisher.UpdateDeploymentStatus(ctx, statusUpdate("haproxy-0", "uid-0", "rt-0", "abc123")))
	}

	assert.Equal(t, 3, counter.aux(),
		"identical repeats must issue no further aux-file patches (before this fix: %d)", 3+3*repeats)
	assert.Equal(t, 1+repeats, counter.get(resHAProxyCfgs),
		"HAProxyCfg is patched on every update, unchanged by the elision")
}

// TestUpdateDeploymentStatus_RestampsOnChangedPodIdentity verifies that a
// changed podUID or podRuntimeID (and a new pod) re-stamps, while a changed
// deploy checksum alone does not — the aux entry records the aux file's OWN
// immutable content checksum, not the main-config deploy checksum, so a new
// deploy over an unchanged aux set is exactly the redundant write #163 elides.
func TestUpdateDeploymentStatus_RestampsOnChangedPodIdentity(t *testing.T) {
	ctx, publisher, counter := newAuxPublisherWithCounter(t)

	require.NoError(t, publisher.UpdateDeploymentStatus(ctx, statusUpdate("haproxy-0", "uid-0", "rt-0", "abc123")))
	require.Equal(t, 3, counter.aux())

	// A new deploy checksum over the same aux set leaves aux stamps unchanged.
	require.NoError(t, publisher.UpdateDeploymentStatus(ctx, statusUpdate("haproxy-0", "uid-0", "rt-0", "def456")))
	assert.Equal(t, 3, counter.aux(), "a new deploy checksum alone does not re-stamp aux files")

	// Changed podUID re-stamps.
	require.NoError(t, publisher.UpdateDeploymentStatus(ctx, statusUpdate("haproxy-0", "uid-1", "rt-0", "abc123")))
	assert.Equal(t, 6, counter.aux(), "changed podUID re-stamps all aux files")

	// Changed podRuntimeID re-stamps.
	require.NoError(t, publisher.UpdateDeploymentStatus(ctx, statusUpdate("haproxy-0", "uid-1", "rt-1", "abc123")))
	assert.Equal(t, 9, counter.aux(), "changed podRuntimeID re-stamps all aux files")

	// A different pod re-stamps (its own keys).
	require.NoError(t, publisher.UpdateDeploymentStatus(ctx, statusUpdate("haproxy-1", "uid-9", "rt-9", "abc123")))
	assert.Equal(t, 12, counter.aux(), "a new pod stamps its own aux entries")
}

// TestUpdateDeploymentStatus_RestampsOnNewAuxSet verifies that a new
// content-hashed aux-file set (new set-id → new CR names) re-stamps: the changed
// files are new keys, so nothing is wrongly skipped.
func TestUpdateDeploymentStatus_RestampsOnNewAuxSet(t *testing.T) {
	ctx, publisher, counter := newAuxPublisherWithCounter(t)

	require.NoError(t, publisher.UpdateDeploymentStatus(ctx, statusUpdate("haproxy-0", "uid-0", "rt-0", "abc123")))
	require.Equal(t, 3, counter.aux())
	require.NoError(t, publisher.UpdateDeploymentStatus(ctx, statusUpdate("haproxy-0", "uid-0", "rt-0", "abc123")))
	require.Equal(t, 3, counter.aux(), "unchanged repeat elided")

	// Republish with changed map content: the set-id rotates, so all aux CR
	// names change and the pod re-stamps the new set.
	req := auxPublishRequest()
	req.AuxiliaryFiles.MapFiles[0].Content = "example.com be_b\n"
	_, err := publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)

	require.NoError(t, publisher.UpdateDeploymentStatus(ctx, statusUpdate("haproxy-0", "uid-0", "rt-0", "abc123")))
	assert.Equal(t, 6, counter.aux(), "a new aux-file set re-stamps every file")
}

// TestCleanupPodReferences_EvictsAuxStamps proves the pod-departure invalidation:
// after a pod is cleaned up, a same-named pod re-stamps rather than being elided
// against the departed pod's cached entry.
func TestCleanupPodReferences_EvictsAuxStamps(t *testing.T) {
	ctx, publisher, counter := newAuxPublisherWithCounter(t)

	require.NoError(t, publisher.UpdateDeploymentStatus(ctx, statusUpdate("haproxy-0", "uid-0", "rt-0", "abc123")))
	require.Equal(t, 3, counter.aux())

	require.NoError(t, publisher.CleanupPodReferences(ctx, &PodCleanupRequest{PodName: "haproxy-0", Namespace: "default"}))

	// Same identity returns: without eviction the value would match the cache
	// and be wrongly skipped. It must re-stamp.
	require.NoError(t, publisher.UpdateDeploymentStatus(ctx, statusUpdate("haproxy-0", "uid-0", "rt-0", "abc123")))
	assert.Equal(t, 6, counter.aux(), "a departed-then-returned pod re-stamps all aux files")
}

// TestReconcileDeployedToPods_EvictsDepartedAuxStamps proves the reconcile-path
// invalidation: a pod dropped from the running set (e.g. a transient discovery
// blip) re-stamps if it returns with the same identity.
func TestReconcileDeployedToPods_EvictsDepartedAuxStamps(t *testing.T) {
	ctx, publisher, counter := newAuxPublisherWithCounter(t)

	require.NoError(t, publisher.UpdateDeploymentStatus(ctx, statusUpdate("haproxy-0", "uid-0", "rt-0", "abc123")))
	require.Equal(t, 3, counter.aux())

	// Reconcile with haproxy-0 absent from the running fleet — it is evicted.
	require.NoError(t, publisher.ReconcileDeployedToPods(ctx, "default", []PodIdentity{{PodName: "haproxy-9", PodUID: "uid-9", PodRuntimeID: "rt-9"}}))

	// The same pod returns with the same identity and must re-stamp.
	require.NoError(t, publisher.UpdateDeploymentStatus(ctx, statusUpdate("haproxy-0", "uid-0", "rt-0", "abc123")))
	assert.Equal(t, 6, counter.aux(), "a pod dropped by reconcile re-stamps on return")
}

// TestReconcileDeployedToPods_KeepsRunningPodStamps verifies a pod still in the
// running set keeps its cache entry — an unchanged repeat after reconcile is
// still elided.
func TestReconcileDeployedToPods_KeepsRunningPodStamps(t *testing.T) {
	ctx, publisher, counter := newAuxPublisherWithCounter(t)

	require.NoError(t, publisher.UpdateDeploymentStatus(ctx, statusUpdate("haproxy-0", "uid-0", "rt-0", "abc123")))
	require.Equal(t, 3, counter.aux())

	require.NoError(t, publisher.ReconcileDeployedToPods(ctx, "default", []PodIdentity{{PodName: "haproxy-0", PodUID: "uid-0", PodRuntimeID: "rt-0"}}))

	require.NoError(t, publisher.UpdateDeploymentStatus(ctx, statusUpdate("haproxy-0", "uid-0", "rt-0", "abc123")))
	assert.Equal(t, 3, counter.aux(), "a still-running pod's unchanged repeat stays elided")
}

// TestResetAuxiliaryStampCache_ForcesRestamp proves the leadership-transition
// invalidation: after a reset a new leader re-stamps once per (pod, file).
func TestResetAuxiliaryStampCache_ForcesRestamp(t *testing.T) {
	ctx, publisher, counter := newAuxPublisherWithCounter(t)

	require.NoError(t, publisher.UpdateDeploymentStatus(ctx, statusUpdate("haproxy-0", "uid-0", "rt-0", "abc123")))
	require.Equal(t, 3, counter.aux())
	require.NoError(t, publisher.UpdateDeploymentStatus(ctx, statusUpdate("haproxy-0", "uid-0", "rt-0", "abc123")))
	require.Equal(t, 3, counter.aux(), "unchanged repeat elided before reset")

	publisher.ResetAuxiliaryStampCache()

	require.NoError(t, publisher.UpdateDeploymentStatus(ctx, statusUpdate("haproxy-0", "uid-0", "rt-0", "abc123")))
	assert.Equal(t, 6, counter.aux(), "reset forces one re-stamp per aux file")
	require.NoError(t, publisher.UpdateDeploymentStatus(ctx, statusUpdate("haproxy-0", "uid-0", "rt-0", "abc123")))
	assert.Equal(t, 6, counter.aux(), "and elision resumes after the re-stamp")
}

// TestAuxStampCache_CommitSkippedWhenInvalidationRacesPatch is the direct unit
// exercise of the check-then-act race guard: an invalidation between beginStamp
// (compare) and commitStamp (record) must drop the record, so the value the CR
// may no longer carry is not cached and the next update re-stamps.
func TestAuxStampCache_CommitSkippedWhenInvalidationRacesPatch(t *testing.T) {
	var c auxStampCache
	key := stampKey{kind: kindMapFile, namespace: "default", name: "haproxy-map-x", podName: "haproxy-0"}
	value := stampedEntry{podUID: "uid-0", podRuntimeID: "rt-0", checksum: "c"}

	// Nothing cached yet: the stamp must be applied.
	skip, gen := c.beginStamp(key, value, false)
	require.False(t, skip)

	// A pod-departure invalidation races between the (unlocked) Patch and record.
	// It is a no-op on the entries (none yet) but must still bump the generation.
	c.forgetPod("haproxy-0")

	// The record is dropped, so the stale value is NOT cached.
	c.commitStamp(key, value, gen)
	skipAfter, _ := c.beginStamp(key, value, false)
	assert.False(t, skipAfter, "a stamp whose invalidation raced its Patch must not be cached; the next update re-stamps")

	// Sanity: an uncontested stamp IS cached and the next identical update elides.
	skip2, gen2 := c.beginStamp(key, value, false)
	require.False(t, skip2)
	c.commitStamp(key, value, gen2)
	skip3, _ := c.beginStamp(key, value, false)
	assert.True(t, skip3, "an uncontested stamp is cached and the next identical update is elided")
}

// TestUpdateDeploymentStatus_DriftCheckBypassesElision proves the drift-interval
// self-heal: a drift-check update re-stamps even when the value is unchanged
// (restoring the pre-fix authoritative re-stamp that heals an out-of-band strip),
// while ordinary inter-drift updates with the same value still elide.
func TestUpdateDeploymentStatus_DriftCheckBypassesElision(t *testing.T) {
	ctx, publisher, counter := newAuxPublisherWithCounter(t)

	require.NoError(t, publisher.UpdateDeploymentStatus(ctx, statusUpdate("haproxy-0", "uid-0", "rt-0", "abc123")))
	require.Equal(t, 3, counter.aux())

	// Ordinary repeat: elided.
	require.NoError(t, publisher.UpdateDeploymentStatus(ctx, statusUpdate("haproxy-0", "uid-0", "rt-0", "abc123")))
	assert.Equal(t, 3, counter.aux(), "a normal identical update is elided")

	// Drift check with the identical value: re-stamps every aux file.
	require.NoError(t, publisher.UpdateDeploymentStatus(ctx, driftStatusUpdate("haproxy-0", "uid-0", "rt-0", "abc123")))
	assert.Equal(t, 6, counter.aux(), "a drift-check update re-stamps even when the value is unchanged")

	// The drift re-stamp is recorded, so the next ordinary update elides again.
	require.NoError(t, publisher.UpdateDeploymentStatus(ctx, statusUpdate("haproxy-0", "uid-0", "rt-0", "abc123")))
	assert.Equal(t, 6, counter.aux(), "elision resumes after the drift re-stamp")
}

// TestPublishConfig_AuxFileDeleteRecreateReStamps reproduces the content
// oscillation A→B→A: the aux-file CR is deleted (during the B publish) and
// recreated fresh under the SAME content-hashed name (during the revert to A).
// The delete evicts the cache, so the recreated CR is re-stamped and a reader
// observes the pod on it — rather than the pre-delete cache eliding the re-stamp
// and leaving the recreated CR permanently empty.
func TestPublishConfig_AuxFileDeleteRecreateReStamps(t *testing.T) {
	ctx, _, crdClient, publisher := newTestPublisher(t)

	reqA := auxPublishRequest() // content A
	reqB := auxPublishRequest() // content B (rotates the whole set-id)
	reqB.AuxiliaryFiles.MapFiles[0].Content = "example.com be_b\n"

	// Publish A and stamp pod P onto A's map file.
	resA, err := publisher.PublishConfig(ctx, &reqA)
	require.NoError(t, err)
	mapNameA := resA.MapFileNames[0]
	require.NoError(t, publisher.UpdateDeploymentStatus(ctx, statusUpdate("haproxy-0", "uid-0", "rt-0", "abc123")))

	// Publish B: A's map CR is pruned (deleted) — this must evict the stamp.
	_, err = publisher.PublishConfig(ctx, &reqB)
	require.NoError(t, err)

	// Revert to A: the map CR is recreated fresh (same content-hashed name,
	// empty status.deployedToPods).
	resA2, err := publisher.PublishConfig(ctx, &reqA)
	require.NoError(t, err)
	require.Equal(t, mapNameA, resA2.MapFileNames[0], "content A must reproduce the same content-hashed name")

	// The recreated CR really is empty before the re-stamp.
	recreated, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyMapFiles("default").Get(ctx, mapNameA, metav1.GetOptions{})
	require.NoError(t, err)
	require.Empty(t, recreated.Status.DeployedToPods, "recreated CR starts with empty status")

	// The next status update must re-stamp the recreated CR.
	require.NoError(t, publisher.UpdateDeploymentStatus(ctx, statusUpdate("haproxy-0", "uid-0", "rt-0", "abc123")))

	got, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyMapFiles("default").Get(ctx, mapNameA, metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, got.Status.DeployedToPods, 1, "recreated CR must be re-stamped, not elided against the pre-delete cache")
	assert.Equal(t, "haproxy-0", got.Status.DeployedToPods[0].PodName)
}

// TestPublishConfig_AuxFileGCCascadeRecreateReStamps proves the create-path
// eviction: an aux-file CR deleted OUT OF BAND — an apiserver owner-GC cascade
// from `kubectl delete haproxycfg` (or a delete+recreate of the parent
// HAProxyTemplateConfig), which never runs pruneAuxiliaryFiles — and recreated
// fresh on the next publish is re-stamped. The delete here bypasses the prune
// path entirely, so only the create-branch eviction can save it.
func TestPublishConfig_AuxFileGCCascadeRecreateReStamps(t *testing.T) {
	ctx, _, crdClient, publisher := newTestPublisher(t)

	req := auxPublishRequest()
	res, err := publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)
	mapName := res.MapFileNames[0]

	require.NoError(t, publisher.UpdateDeploymentStatus(ctx, statusUpdate("haproxy-0", "uid-0", "rt-0", "abc123")))

	// Simulate the owner-GC cascade: delete the CR directly, bypassing
	// pruneAuxiliaryFiles (so the delete-path eviction never runs).
	require.NoError(t, crdClient.HaproxyTemplateICV1alpha1().HAProxyMapFiles("default").
		Delete(ctx, mapName, metav1.DeleteOptions{}))

	// Republish the identical config: the map CR is recreated fresh (same
	// content-hashed name, empty status). The create-path eviction must drop the
	// stale stamp.
	res2, err := publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)
	require.Equal(t, mapName, res2.MapFileNames[0])

	recreated, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyMapFiles("default").Get(ctx, mapName, metav1.GetOptions{})
	require.NoError(t, err)
	require.Empty(t, recreated.Status.DeployedToPods, "GC-recreated CR starts empty")

	require.NoError(t, publisher.UpdateDeploymentStatus(ctx, statusUpdate("haproxy-0", "uid-0", "rt-0", "abc123")))

	got, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyMapFiles("default").Get(ctx, mapName, metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, got.Status.DeployedToPods, 1, "GC-recreated CR must be re-stamped via the create-path eviction")
	assert.Equal(t, "haproxy-0", got.Status.DeployedToPods[0].PodName)
}
