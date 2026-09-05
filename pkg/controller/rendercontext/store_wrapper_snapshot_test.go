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

package rendercontext

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// mutatingStore returns a different snapshot on each List() / Get() call,
// simulating an informer landing an Add between two reads in one render.
// This is the live-store mutation that StoreWrapper's per-render snapshot
// needs to immunize templates against.
type mutatingStore struct {
	mu        sync.Mutex
	snapshots [][]any // returned in order; last snapshot reused once exhausted
	calls     int
}

func (s *mutatingStore) snapshot() []any {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.calls
	if idx >= len(s.snapshots) {
		idx = len(s.snapshots) - 1
	}
	s.calls++
	return s.snapshots[idx]
}

func (s *mutatingStore) List() ([]any, error)                 { return s.snapshot(), nil }
func (s *mutatingStore) Get(_ ...string) ([]any, error)       { return s.snapshot(), nil }
func (s *mutatingStore) Add(_ any, _ []string) error          { return nil }
func (s *mutatingStore) Update(_ any, _ []string) error       { return nil }
func (s *mutatingStore) Delete(_, _ string, _ []string) error { return nil }
func (s *mutatingStore) Clear() error                         { return nil }

var _ stores.Store = (*mutatingStore)(nil)

func ingress(ns, name string) map[string]any {
	return map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "Ingress",
		"metadata": map[string]any{
			"namespace": ns,
			"name":      name,
		},
	}
}

// TestStoreWrapper_ListIsStableAcrossCalls pins the regression: when the
// underlying store mutates between two List() calls (informer Add lands
// during admission validation), StoreWrapper must keep returning the
// first snapshot.
//
// Concretely the chart's nginx-ingress / haproxytech / haproxy-ingress
// auth pattern reads `resources.ingresses.List()` from one snippet
// (global-top, emits userlists) and again from another (backend-directives,
// emits http_auth refs). If the second read sees an extra Ingress, that
// Ingress's http_auth(...) ref points at a userlist no snippet emitted,
// HAProxy rejects the rendered config, and admission denies. The fix at
// the wrapper boundary makes the second read return the same set as the
// first.
func TestStoreWrapper_ListIsStableAcrossCalls(t *testing.T) {
	ingA := ingress("ns-a", "ing-a")
	ingB := ingress("ns-b", "ing-b")

	store := &mutatingStore{
		snapshots: [][]any{
			{ingA},       // first List(): one ingress
			{ingA, ingB}, // second List(): an Add landed in between
		},
	}
	wrapper := &StoreWrapper{
		readContext:  templating.WithImmutableResourceInputs(t.Context()),
		Store:        store,
		ResourceType: "ingresses",
		Logger:       testutil.NewTestLogger(),
		IndexBy:      []string{"metadata.namespace", "metadata.name"},
	}

	first := wrapper.List()
	second := wrapper.List()

	require.Len(t, first, 1, "first call returns the snapshot at first access")
	require.Len(t, second, 1, "second call must return the same snapshot, not the live store")
	assert.Equal(t, first, second, "wrapper must pin the snapshot for the render's lifetime")
}

// TestStoreWrapper_ListAndGetSingleAgree closes the cross-method hole:
// a List() in one snippet and a GetSingle() in another (on the same
// resource type, same render) must observe the same store state. With
// per-method memoization, List() and GetSingle() each take their own
// snapshot at first access — and a store mutation between them lets a
// template see an Ingress via GetSingle that wasn't in the List(),
// reproducing the orphan-userlist failure mode at a different angle.
// The snapshot-on-first-access design eliminates that hole.
func TestStoreWrapper_ListAndGetSingleAgree(t *testing.T) {
	ingA := ingress("ns-a", "ing-a")
	ingB := ingress("ns-b", "ing-b")

	store := &mutatingStore{
		snapshots: [][]any{
			{ingA},       // first read: only ing-a present
			{ingA, ingB}, // second read: ing-b appears (informer caught up)
		},
	}
	wrapper := &StoreWrapper{
		readContext:  templating.WithImmutableResourceInputs(t.Context()),
		Store:        store,
		ResourceType: "ingresses",
		Logger:       testutil.NewTestLogger(),
		IndexBy:      []string{"metadata.namespace", "metadata.name"},
	}

	// First access: List() pins the snapshot to the first store state.
	listed := wrapper.List()
	require.Len(t, listed, 1)

	// A subsequent GetSingle for ing-b must NOT find it: ing-b wasn't
	// in the snapshot pinned at first access, so the wrapper must agree
	// that "ing-b doesn't exist for this render".
	got := wrapper.GetSingle("ns-b", "ing-b")
	assert.Nil(t, got, "GetSingle must agree with List() that ing-b is not in this render's view")

	// And ing-a, which IS in the snapshot, must be findable.
	got = wrapper.GetSingle("ns-a", "ing-a")
	require.NotNil(t, got, "GetSingle must find ing-a in the snapshot")
	m, ok := got.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "ing-a", m["metadata"].(map[string]any)["name"])
}

// TestStoreWrapper_GetSingleFirstThenListAgree is the symmetric case:
// the first access is GetSingle. The wrapper still snapshots the full
// store at that moment, so a later List() returns the same set.
func TestStoreWrapper_GetSingleFirstThenListAgree(t *testing.T) {
	ingA := ingress("ns-a", "ing-a")
	ingB := ingress("ns-b", "ing-b")

	store := &mutatingStore{
		snapshots: [][]any{
			{ingA},
			{ingA, ingB},
		},
	}
	wrapper := &StoreWrapper{
		readContext:  templating.WithImmutableResourceInputs(t.Context()),
		Store:        store,
		ResourceType: "ingresses",
		Logger:       testutil.NewTestLogger(),
		IndexBy:      []string{"metadata.namespace", "metadata.name"},
	}

	// First access: GetSingle.
	got := wrapper.GetSingle("ns-a", "ing-a")
	require.NotNil(t, got)

	// Now List(). It must reflect the snapshot loaded at first access,
	// NOT a fresh call to the live store (which would include ing-b).
	listed := wrapper.List()
	require.Len(t, listed, 1, "List() must use the same snapshot the first GetSingle pinned")
}

// TestStoreWrapper_FetchPartialMatchUsesSnapshot verifies the prefix-scan
// path of the snapshot index: Fetch with fewer keys than IndexBy returns
// every snapshot item whose composite key starts with the supplied
// prefix.
func TestStoreWrapper_FetchPartialMatchUsesSnapshot(t *testing.T) {
	ingA1 := ingress("ns-a", "ing-1")
	ingA2 := ingress("ns-a", "ing-2")
	ingB1 := ingress("ns-b", "ing-1")

	store := &mutatingStore{
		snapshots: [][]any{
			{ingA1, ingA2, ingB1},
		},
	}
	wrapper := &StoreWrapper{
		readContext:  templating.WithImmutableResourceInputs(t.Context()),
		Store:        store,
		ResourceType: "ingresses",
		Logger:       testutil.NewTestLogger(),
		IndexBy:      []string{"metadata.namespace", "metadata.name"},
	}

	results := wrapper.Fetch("ns-a")
	require.Len(t, results, 2, "Fetch with namespace prefix should return both ns-a ingresses")
}

func TestStoreWrapper_IndexComponentsAreUnambiguous(t *testing.T) {
	fixtures := []snapshotIndexFixture{
		{name: "slash-first", first: "a/b", second: "c"},
		{name: "slash-second", first: "a", second: "b/c"},
		{name: "empty", first: "", second: "tail"},
		{name: "unicode-slash-first", first: "領域/一", second: "雪"},
		{name: "unicode-slash-second", first: "領域", second: "一/雪"},
	}
	items := make([]any, len(fixtures))
	for i, fixture := range fixtures {
		items[i] = snapshotIndexedResource(fixture.name, fixture.first, fixture.second)
	}

	wrapper := &StoreWrapper{
		readContext:  templating.WithImmutableResourceInputs(t.Context()),
		Store:        &mutatingStore{snapshots: [][]any{items}},
		ResourceType: "custom-resources",
		Logger:       testutil.NewTestLogger(),
		IndexBy:      []string{"spec.first", "spec.second"},
	}

	for _, fixture := range fixtures {
		assertWrapperResourceNames(t, wrapper.Fetch(fixture.first, fixture.second), fixture.name)
	}
	assertWrapperResourceNames(t, wrapper.Fetch("a"), "slash-second")
	assertWrapperResourceNames(t, wrapper.Fetch("a/b"), "slash-first")
	assertWrapperResourceNames(t, wrapper.Fetch(""), "empty")
	assertWrapperResourceNames(t, wrapper.Fetch("領域"), "unicode-slash-second")
	assertWrapperResourceNames(t, wrapper.Fetch("領域/一"), "unicode-slash-first")
	assert.Empty(t, wrapper.Fetch())
	assert.Empty(t, wrapper.Fetch("a", "b", "c"))
}

type snapshotIndexFixture struct {
	name   string
	first  string
	second string
}

func snapshotIndexedResource(name, first, second string) map[string]any {
	return map[string]any{
		"metadata": map[string]any{"name": name},
		"spec": map[string]any{
			"first":  first,
			"second": second,
		},
	}
}

func assertWrapperResourceNames(t *testing.T, resources []any, want ...string) {
	t.Helper()

	names := make([]string, len(resources))
	for i, resource := range resources {
		metadata := resource.(map[string]any)["metadata"].(map[string]any)
		names[i] = metadata["name"].(string)
	}
	assert.Equal(t, want, names)
}
