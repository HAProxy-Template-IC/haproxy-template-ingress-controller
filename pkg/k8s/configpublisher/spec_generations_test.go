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
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	haproxyv1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/generated/clientset/versioned/fake"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

// TestSpecGenerations_RecordAndLookup covers the core correlation: a checksum
// published as generation N resolves back to N, and an unpublished one resolves
// to 0 (unknown) rather than to some other spec's generation.
func TestSpecGenerations_RecordAndLookup(t *testing.T) {
	s := newSpecGenerations()
	s.record("haptic", "cfg", "aaa", 7)
	s.record("haptic", "cfg", "bbb", 8)

	assert.Equal(t, int64(7), s.lookup("haptic", "cfg", "aaa"))
	assert.Equal(t, int64(8), s.lookup("haptic", "cfg", "bbb"))
	assert.Equal(t, int64(0), s.lookup("haptic", "cfg", "never-published"),
		"an unknown checksum must read as unknown, never as another spec's generation")
	assert.Equal(t, int64(0), s.lookup("haptic", "cfg", ""))
}

// TestSpecGenerations_ScopedToObject pins that the key includes object identity:
// two HAProxyCfgs can legitimately publish identical content (same checksum) at
// unrelated generations, and one must not answer for the other.
func TestSpecGenerations_ScopedToObject(t *testing.T) {
	s := newSpecGenerations()
	s.record("haptic", "cfg-a", "same-content", 3)
	s.record("haptic", "cfg-b", "same-content", 99)

	assert.Equal(t, int64(3), s.lookup("haptic", "cfg-a", "same-content"))
	assert.Equal(t, int64(99), s.lookup("haptic", "cfg-b", "same-content"))
	assert.Equal(t, int64(0), s.lookup("other-ns", "cfg-a", "same-content"),
		"namespace is part of the identity")
}

// TestSpecGenerations_RepublishKeepsFirstGeneration pins that re-recording a
// known checksum keeps the earliest generation. An unchanged republish does not
// bump metadata.generation, and the first one is when the content went live —
// taking a later value would claim the pod is further ahead than it is.
func TestSpecGenerations_RepublishKeepsFirstGeneration(t *testing.T) {
	s := newSpecGenerations()
	s.record("haptic", "cfg", "aaa", 5)
	s.record("haptic", "cfg", "aaa", 12)

	assert.Equal(t, int64(5), s.lookup("haptic", "cfg", "aaa"))
}

// TestSpecGenerations_IgnoresUnusableInput pins that an empty checksum or a
// non-positive generation is never stored — both would otherwise turn the
// "0 means unknown" contract into a real-looking answer.
func TestSpecGenerations_IgnoresUnusableInput(t *testing.T) {
	s := newSpecGenerations()
	s.record("haptic", "cfg", "", 4)
	s.record("haptic", "cfg", "zero-gen", 0)
	s.record("haptic", "cfg", "neg-gen", -1)

	assert.Equal(t, int64(0), s.lookup("haptic", "cfg", "zero-gen"))
	assert.Equal(t, int64(0), s.lookup("haptic", "cfg", "neg-gen"))
	assert.Empty(t, s.byKey, "nothing unusable is stored")
}

// TestSpecGenerations_EvictsOldestPastBound pins the bound: the newest entries —
// the only ones a pending status update can need — survive, and overflow costs
// an unknown (safe) answer rather than unbounded growth.
func TestSpecGenerations_EvictsOldestPastBound(t *testing.T) {
	s := newSpecGenerations()
	for i := 1; i <= specGenerationHistory+10; i++ {
		s.record("haptic", "cfg", fmt.Sprintf("sum-%d", i), int64(i))
	}

	assert.Len(t, s.byKey, specGenerationHistory, "the map stays bounded")
	assert.Equal(t, int64(0), s.lookup("haptic", "cfg", "sum-1"), "the oldest aged out")
	newest := specGenerationHistory + 10
	assert.Equal(t, int64(newest), s.lookup("haptic", "cfg", fmt.Sprintf("sum-%d", newest)),
		"the newest — what a pending status update needs — is retained")
}

// TestSpecGenerations_ConcurrentAccess exercises the lock: publishes and per-pod
// status lookups run on different goroutines in production.
func TestSpecGenerations_ConcurrentAccess(t *testing.T) {
	s := newSpecGenerations()
	var wg sync.WaitGroup
	for i := 1; i <= 50; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); s.record("haptic", "cfg", fmt.Sprintf("sum-%d", i), int64(i)) }()
		go func() { defer wg.Done(); _ = s.lookup("haptic", "cfg", fmt.Sprintf("sum-%d", i)) }()
	}
	wg.Wait()
	assert.NotEmpty(t, s.byKey)
}

// TestPublisher_SpecGenerationHelpers_NilSafe pins that a Publisher built
// without the constructor (as some tests do) degrades to "unknown" instead of
// panicking on a nil map.
func TestPublisher_SpecGenerationHelpers_NilSafe(t *testing.T) {
	p := &Publisher{}
	assert.NotPanics(t, func() { p.recordSpecGeneration(nil) })
	assert.Equal(t, int64(0), p.specGenerationFor("haptic", "cfg", "aaa"))
}

// TestUpdateDeploymentStatus_RecordsObservedGeneration is the wiring test for
// issue #122: a pod's status entry must carry the generation its checksum was
// published as, so an observer can decide "at or past generation N" from a
// single object instead of having to have witnessed the intermediate spec
// versions its watch may never deliver.
func TestUpdateDeploymentStatus_RecordsObservedGeneration(t *testing.T) {
	ctx := context.Background()
	crdClient := fake.NewSimpleClientset()
	installSSAListMapMergeReactor(crdClient)

	publisher := NewWithListers(k8sfake.NewClientset(), crdClient, nil, testLogger())

	req := PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("uid-1"),
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "sum-one",
	}
	_, err := publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)

	// The fake clientset's tracker deep-copies on write, so it cannot stamp
	// metadata.generation the way a real API server does. Seed the correlation
	// the publish would have recorded; that recording is covered by the
	// specGenerations unit tests above, and end-to-end against a real API server
	// by the e2e ground-truth test.
	publisher.specGens.record("default", "test-config-haproxycfg", "sum-one", 4)

	require.NoError(t, publisher.UpdateDeploymentStatus(ctx, &DeploymentStatusUpdate{
		RuntimeConfigName:      "test-config-haproxycfg",
		RuntimeConfigNamespace: "default",
		PodName:                "haproxy-0",
		Checksum:               "sum-one",
	}))

	cfg, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyCfgs("default").
		Get(ctx, "test-config-haproxycfg", metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, cfg.Status.DeployedToPods, 1)
	assert.Equal(t, "sum-one", cfg.Status.DeployedToPods[0].Checksum)
	assert.Equal(t, int64(4), cfg.Status.DeployedToPods[0].ObservedGeneration,
		"the pod's entry must carry the generation its checksum was published as")
}

// TestUpdateDeploymentStatus_UnknownChecksumLeavesGenerationUnset pins the
// failure direction: a checksum this publisher never published (a previous
// leader's, or one aged out of the bound) must read as unknown — 0 — so an
// observer treats the pod as not-yet-converged. Guessing a generation here
// would turn a loud false negative into a silent false positive.
func TestUpdateDeploymentStatus_UnknownChecksumLeavesGenerationUnset(t *testing.T) {
	ctx := context.Background()
	crdClient := fake.NewSimpleClientset()
	installSSAListMapMergeReactor(crdClient)

	publisher := NewWithListers(k8sfake.NewClientset(), crdClient, nil, testLogger())

	_, err := publisher.PublishConfig(ctx, &PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("uid-1"),
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "sum-one",
	})
	require.NoError(t, err)

	// The pod reports a checksum this publisher never wrote.
	require.NoError(t, publisher.UpdateDeploymentStatus(ctx, &DeploymentStatusUpdate{
		RuntimeConfigName:      "test-config-haproxycfg",
		RuntimeConfigNamespace: "default",
		PodName:                "haproxy-0",
		Checksum:               "sum-from-a-previous-leader",
	}))

	cfg, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyCfgs("default").
		Get(ctx, "test-config-haproxycfg", metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, cfg.Status.DeployedToPods, 1)
	assert.Zero(t, cfg.Status.DeployedToPods[0].ObservedGeneration,
		"an unknown checksum must stay unknown, never be guessed")
}

// TestPublish_BackfillsObservedGenerationWrittenBeforeThePublish pins the race
// that would otherwise strand a converged cluster: the per-pod status write and
// the deploy-driven publish are independent paths, so the status can land while
// the checksum's generation is still unknown, recording 0. Under churn the next
// deploy corrects it — at QUIESCENCE there is no next deploy, so the final entry
// would say "unknown" forever and every consumer ordering pods against the spec
// would wait out its budget on a cluster that had in fact converged.
//
// Publishing is when the missing fact arrives, so the publish must repair the
// entries that were written too early.
func TestPublish_BackfillsObservedGenerationWrittenBeforeThePublish(t *testing.T) {
	ctx := context.Background()
	crdClient := fake.NewSimpleClientset()
	installSSAListMapMergeReactor(crdClient)
	publisher := NewWithListers(k8sfake.NewClientset(), crdClient, nil, testLogger())

	req := PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("uid-1"),
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "sum-one",
	}
	_, err := publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)

	// A pod reports the deployed checksum BEFORE that checksum's generation is
	// known — the ordering this test exists for.
	require.NoError(t, publisher.UpdateDeploymentStatus(ctx, &DeploymentStatusUpdate{
		RuntimeConfigName:      "test-config-haproxycfg",
		RuntimeConfigNamespace: "default",
		PodName:                "haproxy-0",
		Checksum:               "sum-one",
	}))
	cfg, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyCfgs("default").
		Get(ctx, "test-config-haproxycfg", metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, cfg.Status.DeployedToPods, 1)
	require.Zero(t, cfg.Status.DeployedToPods[0].ObservedGeneration,
		"premise: the entry was written before the generation was known")

	// The publish now learns the generation. (The fake does not stamp
	// metadata.generation, so set it the way the API server would.)
	cfg.Generation = 9
	_, err = crdClient.HaproxyTemplateICV1alpha1().HAProxyCfgs("default").
		Update(ctx, cfg, metav1.UpdateOptions{})
	require.NoError(t, err)
	publisher.specGens.record("default", "test-config-haproxycfg", "sum-one", 9)
	publisher.backfillObservedGeneration(ctx, cfg)

	cfg, err = crdClient.HaproxyTemplateICV1alpha1().HAProxyCfgs("default").
		Get(ctx, "test-config-haproxycfg", metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, cfg.Status.DeployedToPods, 1)
	assert.Equal(t, int64(9), cfg.Status.DeployedToPods[0].ObservedGeneration,
		"the publish must repair an entry written before its generation was known")
}

// TestBackfill_LeavesOlderEntriesAlone pins that the backfill only repairs
// entries for the checksum being published. A pod still running an older config
// must keep reading as behind — stamping the new generation onto it would claim
// it converged, which is the silent false positive this whole field must never
// produce.
func TestBackfill_LeavesOlderEntriesAlone(t *testing.T) {
	ctx := context.Background()
	crdClient := fake.NewSimpleClientset()
	installSSAListMapMergeReactor(crdClient)
	publisher := NewWithListers(k8sfake.NewClientset(), crdClient, nil, testLogger())

	cfg := &haproxyv1alpha1.HAProxyCfg{
		ObjectMeta: metav1.ObjectMeta{Name: "cfg", Namespace: "default", Generation: 12},
		Spec:       haproxyv1alpha1.HAProxyCfgSpec{Checksum: "sum-new"},
		Status: haproxyv1alpha1.HAProxyCfgStatus{
			DeployedToPods: []haproxyv1alpha1.PodDeploymentStatus{
				{PodName: "haproxy-0", Checksum: "sum-old"},
			},
		},
	}
	_, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyCfgs("default").
		Create(ctx, cfg, metav1.CreateOptions{})
	require.NoError(t, err)

	publisher.backfillObservedGeneration(ctx, cfg)

	got, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyCfgs("default").
		Get(ctx, "cfg", metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, got.Status.DeployedToPods, 1)
	assert.Zero(t, got.Status.DeployedToPods[0].ObservedGeneration,
		"a pod on an older checksum must not be stamped with the new generation")
}

// TestUpdateDeploymentStatus_UnknownLookupPreservesRecordedGeneration pins the
// SSA-delete-on-omit hazard. The per-pod field manager applies with Force, so a
// field left out of the payload is DELETED, not left alone. A checksum that
// aged out of the bounded history (or came from a previous leader) resolves to
// unknown — and writing that as "omit" would regress a pod that already had a
// generation back to not-converged, which backfill cannot repair once the
// entry's checksum no longer matches the current spec. That is the same hang
// this field was introduced to remove.
func TestUpdateDeploymentStatus_UnknownLookupPreservesRecordedGeneration(t *testing.T) {
	ctx := context.Background()
	crdClient := fake.NewSimpleClientset()
	installSSAListMapMergeReactor(crdClient)
	publisher := NewWithListers(k8sfake.NewClientset(), crdClient, nil, testLogger())

	_, err := publisher.PublishConfig(ctx, &PublishRequest{
		TemplateConfigName:      "test-config",
		TemplateConfigNamespace: "default",
		TemplateConfigUID:       types.UID("uid-1"),
		Config:                  "global\n  daemon\n",
		ConfigPath:              "/etc/haproxy/haproxy.cfg",
		Checksum:                "sum-one",
	})
	require.NoError(t, err)

	// The pod is recorded at a known generation.
	publisher.specGens.record("default", "test-config-haproxycfg", "sum-one", 6)
	require.NoError(t, publisher.UpdateDeploymentStatus(ctx, &DeploymentStatusUpdate{
		RuntimeConfigName:      "test-config-haproxycfg",
		RuntimeConfigNamespace: "default",
		PodName:                "haproxy-0",
		Checksum:               "sum-one",
	}))

	// It then advances to a checksum whose generation this publisher never saw.
	require.NoError(t, publisher.UpdateDeploymentStatus(ctx, &DeploymentStatusUpdate{
		RuntimeConfigName:      "test-config-haproxycfg",
		RuntimeConfigNamespace: "default",
		PodName:                "haproxy-0",
		Checksum:               "sum-aged-out",
	}))

	cfg, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyCfgs("default").
		Get(ctx, "test-config-haproxycfg", metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, cfg.Status.DeployedToPods, 1)
	assert.Equal(t, int64(6), cfg.Status.DeployedToPods[0].ObservedGeneration,
		"an unknown lookup must preserve the recorded generation, never delete it")
}

// TestBackfill_ReadsFreshStatusNotTheWriteSnapshot pins that the backfill does
// not trust the caller's copy of the status. cfg is what the spec write
// returned, and its status was loaded BEFORE that write — so a pod entry that
// landed in between is absent from it. Repairing that stale list skips exactly
// the entries the backfill exists to fix, and the preserve-on-unknown rule then
// keeps re-emitting the old value, so the pod reports a generation it passed
// long ago (in CI: pinned at 53 while the spec was at 60, failing four tests).
func TestBackfill_ReadsFreshStatusNotTheWriteSnapshot(t *testing.T) {
	ctx := context.Background()
	crdClient := fake.NewSimpleClientset()
	installSSAListMapMergeReactor(crdClient)
	publisher := NewWithListers(k8sfake.NewClientset(), crdClient, nil, testLogger())

	// Stored object: the pod entry IS present, awaiting its generation.
	stored := &haproxyv1alpha1.HAProxyCfg{
		ObjectMeta: metav1.ObjectMeta{Name: "cfg", Namespace: "default", Generation: 21},
		Spec:       haproxyv1alpha1.HAProxyCfgSpec{Checksum: "sum-live"},
		Status: haproxyv1alpha1.HAProxyCfgStatus{
			DeployedToPods: []haproxyv1alpha1.PodDeploymentStatus{
				{PodName: "haproxy-0", Checksum: "sum-live"},
			},
		},
	}
	_, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyCfgs("default").
		Create(ctx, stored, metav1.CreateOptions{})
	require.NoError(t, err)

	// The caller's copy is the pre-write snapshot: same spec, EMPTY status.
	snapshot := stored.DeepCopy()
	snapshot.Status.DeployedToPods = nil

	publisher.backfillObservedGeneration(ctx, snapshot)

	got, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyCfgs("default").
		Get(ctx, "cfg", metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, got.Status.DeployedToPods, 1)
	assert.Equal(t, int64(21), got.Status.DeployedToPods[0].ObservedGeneration,
		"the backfill must repair the entry that exists NOW, not the one its stale snapshot showed")
}
