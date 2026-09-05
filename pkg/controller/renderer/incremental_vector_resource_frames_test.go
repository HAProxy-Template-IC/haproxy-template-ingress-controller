// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package renderer

import (
	"context"
	"reflect"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

type directResourceProjectionFixture struct {
	session         *incrementalRenderSession
	materialization *incrementalResourceMaterialization
	reader          *derivedProjectionProofReader
	resolver        *incrementalQueryDerivedResourceResolver
}

var benchmarkDirectBoundResourceInputSpec resourceInputSpec

func newDirectResourceProjectionFixture(t *testing.T) *directResourceProjectionFixture {
	t.Helper()
	_, snapshot := newResourceMaterializationStore(t, "value")
	session := newResourceMaterializationSession(snapshot)
	derived := derivedProjectionProofSession("routes", nil)
	session.state = derived.state
	session.bindingPlan = derived.bindingPlan
	spec := resourceInputSpec{resourceType: "routes", scope: resourceInputList}
	materialization, supported, err := session.resourceMaterializations.ensure(
		t.Context(), snapshot, &spec,
	)
	require.NoError(t, err)
	require.True(t, supported)
	reader := &derivedProjectionProofReader{input: deriveOwnerInput("routes", nil, false)}
	resolver := &incrementalQueryDerivedResourceResolver{
		ctx: t.Context(), reader: reader, session: session,
	}
	return &directResourceProjectionFixture{
		session: session, materialization: materialization, reader: reader, resolver: resolver,
	}
}

func TestIncrementalDirectResourceProjectionReobservesOwnerAbsence(t *testing.T) {
	fixture := newDirectResourceProjectionFixture(t)
	owner, err := fixture.resolver.resolveOwnerForProjection("routes")
	require.NoError(t, err)
	projection, err := fixture.materialization.directProjection(
		fixture.session, "routes", &owner, true,
	)
	require.NoError(t, err)
	require.NoError(t, projection.AuthenticateDirectBoundResourceProjection("routes"))
	assert.Equal(t, 1, fixture.reader.exactReads)

	owner, err = fixture.resolver.resolveOwnerForProjection("routes")
	require.NoError(t, err)
	reused, err := fixture.materialization.directProjection(
		fixture.session, "routes", &owner, true,
	)
	require.NoError(t, err)
	assert.Same(t, projection, reused)
	assert.Equal(t, 2, fixture.reader.exactReads)
}

func TestIncrementalDirectResourceProjectionRejectsOwnerPublication(t *testing.T) {
	fixture := newDirectResourceProjectionFixture(t)
	owner, err := fixture.resolver.resolveOwnerForProjection("routes")
	require.NoError(t, err)
	_, err = fixture.materialization.directProjection(fixture.session, "routes", &owner, true)
	require.NoError(t, err)
	fixture.reader.input = deriveOwnerInput(
		"routes", &incrementalComponent{name: "governance", deriveResource: true}, true,
	)

	_, err = fixture.resolver.resolveOwnerForProjection("routes")
	require.ErrorIs(t, err, incremental.ErrRevisionConflict)
	assert.Equal(t, 2, fixture.reader.exactReads)
	assert.NotNil(t, fixture.materialization.projection.Load())
}

func TestIncrementalDirectResourceProjectionKeepsEmptyReadOwnerFree(t *testing.T) {
	store := k8sstore.NewMemoryStore(2)
	snapshot, err := store.Pin()
	require.NoError(t, err)
	session := newResourceMaterializationSession(snapshot)
	derived := derivedProjectionProofSession("routes", nil)
	session.state = derived.state
	session.bindingPlan = derived.bindingPlan
	spec := resourceInputSpec{resourceType: "routes", scope: resourceInputList}
	materialization, supported, err := session.resourceMaterializations.ensure(t.Context(), snapshot, &spec)
	require.NoError(t, err)
	require.True(t, supported)
	require.Zero(t, materialization.itemCount)
	require.Nil(t, materialization.raw.value.Load())

	projection, err := materialization.directProjection(
		session, "routes", &incrementalDerivedOwnerResolution{}, false,
	)
	require.NoError(t, err)
	require.NoError(t, projection.AuthenticateDirectBoundResourceProjection("routes"))
}

func TestIncrementalDirectResourceProjectionFallsBackForIncompatibleResultShape(t *testing.T) {
	_, snapshot := newResourceMaterializationStore(t, "value")
	session := newResourceMaterializationSession(snapshot)
	derived := derivedProjectionProofSession("routes", nil)
	session.state = derived.state
	session.bindingPlan = derived.bindingPlan
	spec := resourceInputSpec{
		resourceType: "routes",
		scope:        resourceInputGet,
		keys:         []string{"default", "route"},
	}
	materialization, supported, err := session.resourceMaterializations.ensure(t.Context(), snapshot, &spec)
	require.NoError(t, err)
	require.True(t, supported)
	reader := &derivedProjectionProofReader{input: deriveOwnerInput("routes", nil, false)}
	resolver := &incrementalQueryDerivedResourceResolver{
		ctx: t.Context(), reader: reader, session: session,
	}
	owner, err := resolver.resolveOwnerForProjection("routes")
	require.NoError(t, err)
	projection, err := materialization.directProjection(session, "routes", &owner, true)
	require.NoError(t, err)

	tests := map[string]struct {
		elementType reflect.Type
		returnType  reflect.Type
	}{
		"untyped": {
			returnType: reflect.TypeFor[any](),
		},
		"different return type": {
			elementType: reflect.TypeFor[string](),
			returnType:  reflect.TypeFor[*int](),
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			value, certificate, projected, err := projection.ProjectDirectBoundResourceResult(
				t.Context(),
				"routes",
				rendercontext.DirectBoundResourceGetSingle,
				test.elementType,
				test.returnType,
			)
			require.NoError(t, err)
			assert.False(t, projected)
			assert.False(t, value.IsValid())
			assert.Nil(t, certificate)
		})
	}
}

func TestIncrementalDirectResourceProjectionConcurrentPublication(t *testing.T) {
	fixture := newDirectResourceProjectionFixture(t)
	owner, err := fixture.resolver.resolveOwnerForProjection("routes")
	require.NoError(t, err)
	const workerCount = 64
	projections := make(chan *incrementalDirectResourceProjection, workerCount)
	errs := make(chan error, workerCount)
	start := make(chan struct{})
	var group sync.WaitGroup
	for range workerCount {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			projection, err := fixture.materialization.directProjection(
				fixture.session, "routes", &owner, true,
			)
			if err != nil {
				errs <- err
				return
			}
			projections <- projection
		}()
	}
	close(start)
	group.Wait()
	close(projections)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var expected *incrementalDirectResourceProjection
	for projection := range projections {
		if expected == nil {
			expected = projection
		}
		assert.Same(t, expected, projection)
	}
}

func TestIncrementalDirectResourceProjectionRejectsPoison(t *testing.T) {
	tests := map[string]func(*incrementalDirectResourceProjection){
		"seal": func(projection *incrementalDirectResourceProjection) {
			projection.seal = nil
		},
		"proof seal": func(projection *incrementalDirectResourceProjection) {
			projection.proof.seal = nil
		},
		"authority": func(projection *incrementalDirectResourceProjection) {
			projection.authority = &incrementalResourceMaterializationAuthority{}
		},
		"materialization": func(projection *incrementalDirectResourceProjection) {
			projection.materialization = &incrementalResourceMaterialization{}
		},
		"resource": func(projection *incrementalDirectResourceProjection) {
			projection.resourceType = "other"
		},
		"owner": func(projection *incrementalDirectResourceProjection) {
			projection.owner.source = "other"
		},
		"owner mode": func(projection *incrementalDirectResourceProjection) {
			projection.ownerObserved = false
		},
	}
	for name, poison := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newDirectResourceProjectionFixture(t)
			owner, err := fixture.resolver.resolveOwnerForProjection("routes")
			require.NoError(t, err)
			projection, err := fixture.materialization.directProjection(
				fixture.session, "routes", &owner, true,
			)
			require.NoError(t, err)
			poison(projection)

			err = projection.AuthenticateDirectBoundResourceProjection("routes")
			require.Error(t, err)
		})
	}
}

func TestIncrementalDirectResourceProjectionRejectsInternallyConsistentPoison(t *testing.T) {
	tests := map[string]func(*incrementalDirectResourceProjection){
		"authority": func(projection *incrementalDirectResourceProjection) {
			authority := &incrementalResourceMaterializationAuthority{}
			authority.seal.Store(authority)
			projection.authority = authority
			projection.proof.authority = authority
		},
		"resource": func(projection *incrementalDirectResourceProjection) {
			projection.resourceType = "other"
			projection.proof.resourceType = "other"
		},
		"owner input": func(projection *incrementalDirectResourceProjection) {
			projection.owner.input.Revision = incremental.NewRevision("poison")
			projection.proof.owner = projection.owner
		},
		"owner support": func(projection *incrementalDirectResourceProjection) {
			projection.owner.supported = false
			projection.proof.owner = projection.owner
		},
		"owner mode": func(projection *incrementalDirectResourceProjection) {
			projection.owner = incrementalDerivedOwnerResolution{}
			projection.ownerObserved = false
			projection.proof.owner = projection.owner
			projection.proof.ownerObserved = false
		},
	}
	for name, poison := range tests {
		t.Run(name, func(t *testing.T) {
			fixture := newDirectResourceProjectionFixture(t)
			owner, err := fixture.resolver.resolveOwnerForProjection("routes")
			require.NoError(t, err)
			projection, err := fixture.materialization.directProjection(
				fixture.session, "routes", &owner, true,
			)
			require.NoError(t, err)
			poison(projection)

			err = projection.AuthenticateDirectBoundResourceProjection("routes")
			require.Error(t, err)
		})
	}
}

func TestIncrementalDirectResourceProjectionRejectsWrongResource(t *testing.T) {
	fixture := newDirectResourceProjectionFixture(t)
	owner, err := fixture.resolver.resolveOwnerForProjection("routes")
	require.NoError(t, err)
	projection, err := fixture.materialization.directProjection(
		fixture.session, "routes", &owner, true,
	)
	require.NoError(t, err)

	err = projection.AuthenticateDirectBoundResourceProjection("services")
	require.ErrorContains(t, err, "immutable provenance")
}

func TestIncrementalDirectResourceProjectionCannotCrossGeneration(t *testing.T) {
	fixture := newDirectResourceProjectionFixture(t)
	owner, err := fixture.resolver.resolveOwnerForProjection("routes")
	require.NoError(t, err)
	projection, err := fixture.materialization.directProjection(
		fixture.session, "routes", &owner, true,
	)
	require.NoError(t, err)
	fixture.session.resourceMaterializations.revoke()

	err = projection.AuthenticateDirectBoundResourceProjection("routes")
	require.ErrorContains(t, err, "invalid provenance")
}

func TestIncrementalResourceFramesReleaseIsIdempotent(t *testing.T) {
	fixture := newDirectResourceProjectionFixture(t)
	owner, err := fixture.resolver.resolveOwnerForProjection("routes")
	require.NoError(t, err)
	projection, err := fixture.materialization.directProjection(
		fixture.session, "routes", &owner, true,
	)
	require.NoError(t, err)
	require.Positive(t, fixture.session.resourceMaterializations.entries.len())

	fixture.session.releaseResourceFrames()
	fixture.session.releaseResourceFrames()

	assert.Zero(t, fixture.session.resourceMaterializations.entries.len())
	assert.False(t, fixture.session.resourceMaterializations.valid())
	err = projection.AuthenticateDirectBoundResourceProjection("routes")
	require.ErrorContains(t, err, "invalid provenance")
}

func TestIncrementalResourceFramePayloadHasNoRenderOwnerReachability(t *testing.T) {
	forbidden := map[reflect.Type]struct{}{
		reflect.TypeFor[*incrementalRenderSession]():                     {},
		reflect.TypeFor[*incrementalVectorExecution]():                   {},
		reflect.TypeFor[context.Context]():                               {},
		reflect.TypeFor[templating.IncrementalResourceInvocationLease](): {},
	}
	roots := []reflect.Type{
		reflect.TypeFor[incrementalDirectResourceProjection](),
		reflect.TypeFor[incrementalDirectResourceProjectionProof](),
		reflect.TypeFor[incrementalResourceMaterialization](),
		reflect.TypeFor[incrementalResourceMaterializationProof](),
		reflect.TypeFor[incrementalResourceMaterializationAuthority](),
	}
	for _, root := range roots {
		assertIncrementalResourceFrameTypeDetached(t, root, root.String(), forbidden, map[reflect.Type]bool{})
	}

	var _ rendercontext.DirectBoundResourceProjection = (*incrementalDirectResourceProjection)(nil)
}

func assertIncrementalResourceFrameTypeDetached(
	t *testing.T,
	typeOf reflect.Type,
	path string,
	forbidden map[reflect.Type]struct{},
	seen map[reflect.Type]bool,
) {
	t.Helper()
	if _, rejected := forbidden[typeOf]; rejected {
		t.Errorf("resource frame payload reaches %v through %s", typeOf, path)
		return
	}
	for typeOf.Kind() == reflect.Pointer || typeOf.Kind() == reflect.Array || typeOf.Kind() == reflect.Slice {
		typeOf = typeOf.Elem()
		if _, rejected := forbidden[typeOf]; rejected {
			t.Errorf("resource frame payload reaches %v through %s", typeOf, path)
			return
		}
	}
	if typeOf.Kind() != reflect.Struct || seen[typeOf] ||
		typeOf.PkgPath() != reflect.TypeFor[incrementalDirectResourceProjection]().PkgPath() {
		return
	}
	seen[typeOf] = true
	for index := range typeOf.NumField() {
		field := typeOf.Field(index)
		assertIncrementalResourceFrameTypeDetached(
			t, field.Type, path+"."+field.Name, forbidden, seen,
		)
	}
}

func BenchmarkDirectBoundResourceInputSpec(b *testing.B) {
	declaration := rendercontext.DirectBoundResourceMaterialization{
		ResourceType: "services",
		Operation:    rendercontext.DirectBoundResourceGetSingle,
	}
	keys := []string{"default", "service"}
	b.ReportAllocs()
	for range b.N {
		spec, err := directBoundResourceInputSpec(declaration, keys)
		if err != nil {
			b.Fatal(err)
		}
		benchmarkDirectBoundResourceInputSpec = spec
	}
}
