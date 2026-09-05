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

package rendercontext

import (
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores/storetest"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

type derivedTestMetadata struct {
	Namespace   string            `json:"namespace"`
	Name        string            `json:"name"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

type derivedTestResource struct {
	Metadata derivedTestMetadata `json:"metadata"`
	Spec     map[string]any      `json:"spec,omitempty"`
}

type derivedExpandedResource struct {
	Metadata derivedTestMetadata `json:"metadata"`
	Status   string              `json:"status"`
}

func TestDerivedResourceViewProjectsWithoutMutatingSource(t *testing.T) {
	source := map[string]any{
		"metadata": map[string]any{
			"namespace":   "default",
			"name":        "app",
			"annotations": map[string]any{"existing": "kept"},
		},
	}
	store := &storetest.MockStore{Items: []any{source}}
	view := NewDerivedResourceView()
	derived, err := view.DeriveResource("objects", source, "metadata.annotations['injected']", "yes")
	require.NoError(t, err)
	assert.Equal(t, "yes", derived.(map[string]any)["metadata"].(map[string]any)["annotations"].(map[string]any)["injected"])

	wrapper := &StoreWrapper{
		readContext:    templating.WithImmutableResourceInputs(t.Context()),
		Store:          store,
		ResourceType:   "objects",
		Logger:         testutil.NewTestLogger(),
		IndexBy:        []string{"metadata.namespace", "metadata.name"},
		resourceErrors: NewResourceErrorCollector(),
		DerivedView:    view,
	}
	for operation, items := range map[string][]any{
		"List":      wrapper.List(),
		"Fetch":     wrapper.Fetch("default", "app"),
		"GetSingle": {wrapper.GetSingle("default", "app")},
	} {
		require.Len(t, items, 1, operation)
		annotations := items[0].(map[string]any)["metadata"].(map[string]any)["annotations"].(map[string]any)
		assert.Equal(t, "yes", annotations["injected"], operation)
	}
	assert.NotContains(t, source["metadata"].(map[string]any)["annotations"].(map[string]any), "injected")

	projected := wrapper.List()[0].(map[string]any)
	projected["metadata"].(map[string]any)["annotations"].(map[string]any)["injected"] = "poison"
	next := wrapper.List()[0].(map[string]any)
	assert.Equal(t, "yes", next["metadata"].(map[string]any)["annotations"].(map[string]any)["injected"])
}

func TestDerivedResourceViewTypedValuesAndChainedDerivations(t *testing.T) {
	source := &derivedTestResource{
		Metadata: derivedTestMetadata{Namespace: "default", Name: "typed"},
		Spec:     map[string]any{"replicas": int64(3)},
	}
	view := NewDerivedResourceView()
	first, err := view.DeriveResource("widgets", source, "metadata.annotations['a']", "1")
	require.NoError(t, err)
	second, err := view.DeriveResource("widgets", first, "metadata.annotations['b']", "2")
	require.NoError(t, err)
	assert.Equal(t, "2", second.(map[string]any)["metadata"].(map[string]any)["annotations"].(map[string]any)["b"])

	projected, err := view.Project("widgets", []any{source})
	require.NoError(t, err)
	annotations := projected[0].(map[string]any)["metadata"].(map[string]any)["annotations"].(map[string]any)
	assert.Equal(t, map[string]any{"a": "1", "b": "2"}, annotations)
	assert.Nil(t, source.Metadata.Annotations)
	assert.IsType(t, int64(0), projected[0].(map[string]any)["spec"].(map[string]any)["replicas"])
}

func TestDerivedResourceViewUsesRawOriginForTypedMaterialization(t *testing.T) {
	source := map[string]any{
		"metadata": map[string]any{"namespace": "default", "name": "typed"},
	}
	exposed := &derivedExpandedResource{
		Metadata: derivedTestMetadata{Namespace: "default", Name: "typed"},
	}
	view := NewDerivedResourceView()
	require.NoError(t, view.Bind("widgets", exposed, source))

	_, err := view.DeriveResource("widgets", exposed, "metadata.annotations['derived']", "yes")
	require.NoError(t, err)
	projected, err := view.Project("widgets", []any{source})
	require.NoError(t, err)
	object := projected[0].(map[string]any)
	assert.NotContains(t, object, "status")
	assert.Equal(t, "yes", object["metadata"].(map[string]any)["annotations"].(map[string]any)["derived"])
}

func TestDerivedResourceViewReplayIsExactAndIsolated(t *testing.T) {
	objects := []map[string]any{
		{"metadata": map[string]any{"namespace": "z", "name": "b"}},
		{"metadata": map[string]any{"namespace": "a", "name": "a"}},
	}
	view := NewDerivedResourceView()
	for index, source := range objects {
		_, err := view.DeriveResource("objects", source, "spec.value", index)
		require.NoError(t, err)
	}
	derivations := view.Derivations()
	require.Len(t, derivations, 2)
	assert.Equal(t, "a", derivations[0].Identity.Namespace)
	assert.Equal(t, "z", derivations[1].Identity.Namespace)

	replayed := NewDerivedResourceView()
	for index := range derivations {
		require.NoError(t, replayed.Replay(&derivations[index]))
	}
	projected, err := replayed.Project("objects", []any{objects[0]})
	require.NoError(t, err)
	assert.Equal(t, int64(0), projected[0].(map[string]any)["spec"].(map[string]any)["value"])

	changed := deepCopyDerivedTestMap(t, objects[0])
	changed["metadata"].(map[string]any)["labels"] = map[string]any{"revision": "new"}
	_, err = replayed.Project("objects", []any{changed})
	require.ErrorIs(t, err, ErrDerivedResourceStale)

	scratch := NewDerivedResourceView()
	untouched, err := scratch.Project("objects", []any{objects[0]})
	require.NoError(t, err)
	assert.Nil(t, untouched[0].(map[string]any)["spec"])
}

func TestDerivedResourceViewAdmissionScratchIsIsolated(t *testing.T) {
	source := map[string]any{
		"metadata": map[string]any{"namespace": "default", "name": "object"},
	}
	reconcile := NewDerivedResourceView()
	_, err := reconcile.DeriveResource("objects", source, "spec.mode", "live")
	require.NoError(t, err)
	admission := NewDerivedResourceView()
	_, err = admission.DeriveResource("objects", source, "spec.mode", "candidate")
	require.NoError(t, err)

	live, err := reconcile.Project("objects", []any{source})
	require.NoError(t, err)
	candidate, err := admission.Project("objects", []any{source})
	require.NoError(t, err)
	assert.Equal(t, "live", live[0].(map[string]any)["spec"].(map[string]any)["mode"])
	assert.Equal(t, "candidate", candidate[0].(map[string]any)["spec"].(map[string]any)["mode"])
	assert.Nil(t, source["spec"])
}

func TestDerivedResourceViewSkipsUnrelatedResources(t *testing.T) {
	source := map[string]any{
		"metadata": map[string]any{"namespace": "default", "name": "object"},
	}
	view := NewDerivedResourceView()
	_, err := view.DeriveResource("objects", source, "spec.mode", "derived")
	require.NoError(t, err)

	items := []any{"not a resource"}
	projected, err := view.Project("other", items)
	require.NoError(t, err)
	assert.Equal(t, items, projected)
}

func TestDerivedResourceViewFreezeRejectsEntryMutations(t *testing.T) {
	source := map[string]any{
		"metadata": map[string]any{"namespace": "default", "name": "object"},
	}
	view := NewDerivedResourceView()
	_, err := view.DeriveResource("objects", source, "spec.mode", "derived")
	require.NoError(t, err)
	frozen := view.Freeze()
	require.Len(t, frozen, 1)

	projected, err := view.Project("objects", []any{source})
	require.NoError(t, err)
	_, err = view.DeriveResource("objects", projected[0], "spec.other", "rejected")
	require.ErrorIs(t, err, ErrDerivedResourceViewFrozen)
	require.ErrorIs(t, view.Replay(&frozen[0]), ErrDerivedResourceViewFrozen)
	assert.Equal(t, frozen, view.Derivations())
}

func TestDerivedResourceViewFreezeSnapshotIsStableAndDetached(t *testing.T) {
	sources := []map[string]any{
		{"metadata": map[string]any{"namespace": "z", "name": "second"}},
		{"metadata": map[string]any{"namespace": "a", "name": "first"}},
	}
	view := NewDerivedResourceView()
	for _, source := range sources {
		_, err := view.DeriveResource("objects", source, "spec.mode", "derived")
		require.NoError(t, err)
	}
	want := view.Freeze()
	require.Len(t, want, 2)
	assert.Equal(t, "a", want[0].Identity.Namespace)

	mutable := view.Freeze()
	mutable[0].Identity.Name = "poison"
	mutable[0].Source[0] = '!'
	mutable[0].Value[0] = '!'

	projected, err := view.Project("objects", []any{sources[0]})
	require.NoError(t, err)
	assert.Equal(t, "derived", projected[0].(map[string]any)["spec"].(map[string]any)["mode"])
	exposed := &derivedExpandedResource{
		Metadata: derivedTestMetadata{Namespace: "z", Name: "second"},
	}
	require.NoError(t, view.Bind("objects", exposed, sources[0]))
	assert.Equal(t, want, view.Freeze())
}

func TestDerivedResourceViewResolverIsLazyAndLocalEntriesTakePrecedence(t *testing.T) {
	localSource := map[string]any{
		"metadata": map[string]any{"namespace": "default", "name": "local"},
	}
	resolvedSource := map[string]any{
		"metadata": map[string]any{"namespace": "default", "name": "resolved"},
	}
	resolvedValue := deepCopyDerivedTestMap(t, resolvedSource)
	resolvedValue["spec"] = map[string]any{"mode": "resolver"}
	resolvedEntry := newDerivedTestEntry(t, resolvedSource, resolvedValue)

	view := NewDerivedResourceView()
	_, err := view.DeriveResource("objects", localSource, "spec.mode", "local")
	require.NoError(t, err)
	var lookups []DerivedResourceIdentity
	require.NoError(t, view.SetResolver(DerivedResourceResolverFunc(func(
		identity DerivedResourceIdentity,
	) (DerivedResource, bool, error) {
		lookups = append(lookups, identity)
		return resolvedEntry, true, nil
	})))

	projected, err := view.Project("objects", []any{localSource, resolvedSource})
	require.NoError(t, err)
	assert.Equal(t, "local", projected[0].(map[string]any)["spec"].(map[string]any)["mode"])
	assert.Equal(t, "resolver", projected[1].(map[string]any)["spec"].(map[string]any)["mode"])
	assert.Equal(t, []DerivedResourceIdentity{resolvedEntry.Identity}, lookups)
	derivations := view.Derivations()
	require.Len(t, derivations, 1)
	assert.Equal(t, DerivedResourceIdentity{Resource: "objects", Namespace: "default", Name: "local"},
		derivations[0].Identity)
}

func TestDerivedResourceViewResolverAbsenceAndErrorsAreExact(t *testing.T) {
	source := map[string]any{
		"metadata": map[string]any{"namespace": "default", "name": "object"},
	}
	var absentLookups atomic.Int64
	view := NewDerivedResourceViewWithResolver(DerivedResourceResolverFunc(func(
		DerivedResourceIdentity,
	) (DerivedResource, bool, error) {
		absentLookups.Add(1)
		return DerivedResource{}, false, nil
	}))
	for range 2 {
		projected, err := view.Project("objects", []any{source})
		require.NoError(t, err)
		assert.Equal(t, source, projected[0])
	}
	assert.Equal(t, int64(2), absentLookups.Load())

	wantErr := errors.New("lookup failed")
	failing := NewDerivedResourceViewWithResolver(DerivedResourceResolverFunc(func(
		DerivedResourceIdentity,
	) (DerivedResource, bool, error) {
		return DerivedResource{}, false, wantErr
	}))
	projected, err := failing.Project("objects", []any{source})
	assert.Nil(t, projected)
	require.ErrorIs(t, err, wantErr)
}

type selectiveDerivedTestResolver struct {
	supported map[string]bool
	lookups   atomic.Int64
}

func (r *selectiveDerivedTestResolver) ResolveDerivedResource(
	DerivedResourceIdentity,
) (DerivedResource, bool, error) {
	r.lookups.Add(1)
	return DerivedResource{}, false, nil
}

func (r *selectiveDerivedTestResolver) DerivedResourceSupported(resource string) bool {
	return r.supported[resource]
}

func TestDerivedResourceViewSkipsUnsupportedResolverResources(t *testing.T) {
	resolver := &selectiveDerivedTestResolver{supported: map[string]bool{"owned": true}}
	view := NewDerivedResourceViewWithResolver(resolver)
	unsupported := map[string]any{
		"metadata": map[string]any{"namespace": "default", "name": "opaque"},
		"opaque":   make(chan struct{}),
	}

	projected, err := view.Project("unowned", []any{unsupported})
	require.NoError(t, err)
	require.Len(t, projected, 1)
	assert.Equal(t, unsupported, projected[0])
	assert.Zero(t, resolver.lookups.Load())

	owned := map[string]any{"metadata": map[string]any{"namespace": "default", "name": "owned"}}
	projected, err = view.Project("owned", []any{owned})
	require.NoError(t, err)
	assert.Equal(t, owned, projected[0])
	assert.Equal(t, int64(1), resolver.lookups.Load())
}

func TestDerivedResourceViewResolverRejectsInvalidResults(t *testing.T) {
	source := map[string]any{
		"metadata": map[string]any{"namespace": "default", "name": "object"},
		"spec":     map[string]any{"generation": "current"},
	}
	value := deepCopyDerivedTestMap(t, source)
	value["spec"].(map[string]any)["mode"] = "derived"
	valid := newDerivedTestEntry(t, source, value)

	tests := []struct {
		name    string
		mutate  func(DerivedResource) DerivedResource
		wantErr string
	}{
		{
			name: "lookup identity",
			mutate: func(entry DerivedResource) DerivedResource {
				entry.Identity.Name = "other"
				return entry
			},
			wantErr: "identity does not match its lookup",
		},
		{
			name: "stale source bytes",
			mutate: func(entry DerivedResource) DerivedResource {
				changed := deepCopyDerivedTestMap(t, source)
				changed["spec"].(map[string]any)["generation"] = "changed"
				entry.Source = encodeDerivedTestValue(t, changed)
				return entry
			},
			wantErr: ErrDerivedResourceStale.Error(),
		},
		{
			name: "invalid source JSON",
			mutate: func(entry DerivedResource) DerivedResource {
				entry.Source = []byte(`{"metadata":`)
				return entry
			},
			wantErr: "source is not valid JSON",
		},
		{
			name: "source owner",
			mutate: func(entry DerivedResource) DerivedResource {
				other := deepCopyDerivedTestMap(t, source)
				other["metadata"].(map[string]any)["name"] = "other"
				entry.Source = encodeDerivedTestValue(t, other)
				return entry
			},
			wantErr: "source identity does not match its owner",
		},
		{
			name: "invalid value JSON",
			mutate: func(entry DerivedResource) DerivedResource {
				entry.Value = []byte(`[] trailing`)
				return entry
			},
			wantErr: "value is not valid JSON",
		},
		{
			name: "value owner",
			mutate: func(entry DerivedResource) DerivedResource {
				other := deepCopyDerivedTestMap(t, value)
				other["metadata"].(map[string]any)["namespace"] = "other"
				entry.Value = encodeDerivedTestValue(t, other)
				return entry
			},
			wantErr: "value identity does not match its owner",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entry := test.mutate(valid)
			view := NewDerivedResourceViewWithResolver(DerivedResourceResolverFunc(func(
				DerivedResourceIdentity,
			) (DerivedResource, bool, error) {
				return entry, true, nil
			}))
			projected, err := view.Project("objects", []any{source})
			assert.Nil(t, projected)
			require.ErrorContains(t, err, test.wantErr)
		})
	}
}

func TestDerivedResourceViewResolverBytesAreDetachedAndProjectDoesNotFreeze(t *testing.T) {
	source := map[string]any{
		"metadata": map[string]any{"namespace": "default", "name": "object"},
	}
	value := deepCopyDerivedTestMap(t, source)
	value["spec"] = map[string]any{"mode": "resolved"}
	entry := newDerivedTestEntry(t, source, value)
	view := NewDerivedResourceViewWithResolver(DerivedResourceResolverFunc(func(
		DerivedResourceIdentity,
	) (DerivedResource, bool, error) {
		return entry, true, nil
	}))

	projected, err := view.Project("objects", []any{source})
	require.NoError(t, err)
	for index := range entry.Source {
		entry.Source[index] = '!'
	}
	for index := range entry.Value {
		entry.Value[index] = '!'
	}
	derived, err := view.DeriveResource("objects", projected[0], "spec.next", "ok")
	require.NoError(t, err)
	spec := derived.(map[string]any)["spec"].(map[string]any)
	assert.Equal(t, "resolved", spec["mode"])
	assert.Equal(t, "ok", spec["next"])
	assert.Len(t, view.Freeze(), 1)
}

func TestDerivedResourceViewFrozenViewStillProjectsResolverResults(t *testing.T) {
	source := map[string]any{
		"metadata": map[string]any{"namespace": "default", "name": "object"},
	}
	value := deepCopyDerivedTestMap(t, source)
	value["spec"] = map[string]any{"mode": "resolved"}
	entry := newDerivedTestEntry(t, source, value)
	view := NewDerivedResourceView()
	assert.Empty(t, view.Freeze())
	require.NoError(t, view.SetResolver(DerivedResourceResolverFunc(func(
		DerivedResourceIdentity,
	) (DerivedResource, bool, error) {
		return entry, true, nil
	})))

	projected, err := view.Project("objects", []any{source})
	require.NoError(t, err)
	assert.Equal(t, "resolved", projected[0].(map[string]any)["spec"].(map[string]any)["mode"])
	assert.Empty(t, view.origins)
	require.NoError(t, view.Bind("objects", map[string]any{
		"metadata": map[string]any{"namespace": "default", "name": "opaque"},
		"opaque":   make(chan struct{}),
	}, source))
	assert.Empty(t, view.origins)
	_, err = view.DeriveResource("objects", projected[0], "spec.next", "rejected")
	require.ErrorIs(t, err, ErrDerivedResourceViewFrozen)
	assert.Empty(t, view.Derivations())
}

func TestDerivedResourceViewSetResolverIsOneTimeAndPreservesState(t *testing.T) {
	source := map[string]any{
		"metadata": map[string]any{"namespace": "default", "name": "object"},
	}
	view := NewDerivedResourceView()
	projected, err := view.Project("objects", []any{source})
	require.NoError(t, err)
	assert.Equal(t, source, projected[0])
	_, err = view.DeriveResource("objects", source, "spec.local", "kept")
	require.NoError(t, err)

	resolver := DerivedResourceResolverFunc(func(DerivedResourceIdentity) (DerivedResource, bool, error) {
		return DerivedResource{}, false, nil
	})
	require.NoError(t, view.SetResolver(resolver))
	require.ErrorIs(t, view.SetResolver(resolver), ErrDerivedResourceResolverConfigured)
	require.Error(t, view.SetResolver(DerivedResourceResolverFunc(nil)))
	assert.Len(t, view.Derivations(), 1)
}

func TestDerivedResourceViewResolverConcurrentProjection(t *testing.T) {
	source := map[string]any{
		"metadata": map[string]any{"namespace": "default", "name": "object"},
	}
	value := deepCopyDerivedTestMap(t, source)
	value["spec"] = map[string]any{"mode": "derived"}
	entry := newDerivedTestEntry(t, source, value)
	var lookups atomic.Int64
	view := NewDerivedResourceViewWithResolver(DerivedResourceResolverFunc(func(
		DerivedResourceIdentity,
	) (DerivedResource, bool, error) {
		lookups.Add(1)
		return entry, true, nil
	}))

	const workers = 32
	var wait sync.WaitGroup
	wait.Add(workers)
	errorsByWorker := make(chan error, workers)
	for range workers {
		go func() {
			defer wait.Done()
			projected, err := view.Project("objects", []any{source})
			if err == nil && projected[0].(map[string]any)["spec"].(map[string]any)["mode"] != "derived" {
				err = errors.New("resolver result was not projected")
			}
			errorsByWorker <- err
		}()
	}
	wait.Wait()
	close(errorsByWorker)
	for err := range errorsByWorker {
		require.NoError(t, err)
	}
	assert.Equal(t, int64(workers), lookups.Load())
}

func newDerivedTestEntry(t *testing.T, source, value any) DerivedResource {
	t.Helper()
	identity, err := derivedResourceIdentity("objects", source)
	require.NoError(t, err)
	return DerivedResource{
		Identity: identity,
		Source:   encodeDerivedTestValue(t, source),
		Value:    encodeDerivedTestValue(t, value),
	}
}

func encodeDerivedTestValue(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := encodeDerivedResource(value)
	require.NoError(t, err)
	return encoded
}

func deepCopyDerivedTestMap(t *testing.T, source map[string]any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(source)
	require.NoError(t, err)
	var copied map[string]any
	require.NoError(t, json.Unmarshal(encoded, &copied))
	return copied
}
