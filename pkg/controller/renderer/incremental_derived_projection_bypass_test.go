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
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
)

type derivedProjectionProofReader struct {
	input      incremental.Input
	exactKey   incremental.InputKey
	exactReads int
	queries    int
}

var derivedProjectionProofSink []any

func (r *derivedProjectionProofReader) Input(
	key incremental.InputKey,
) (value []byte, found bool, err error) {
	input, err := r.ExactInput(key)
	return input.Value, input.Found, err
}

func (r *derivedProjectionProofReader) ExactInput(key incremental.InputKey) (incremental.Input, error) {
	r.exactKey = key
	r.exactReads++
	input := r.input
	input.Value = slices.Clone(input.Value)
	return input, nil
}

func (r *derivedProjectionProofReader) Query(context.Context, incremental.QueryKey) ([]byte, error) {
	r.queries++
	return nil, nil
}

func TestIncrementalDerivedProjectionBypassObservesExactOwnerAbsence(t *testing.T) {
	session := derivedProjectionProofSession("widgets", nil)
	expected := deriveOwnerInput("widgets", nil, false)
	reader := &derivedProjectionProofReader{input: expected}
	resolver := &incrementalQueryDerivedResourceResolver{
		ctx: t.Context(), reader: reader, session: session,
	}
	items := []any{derivedProjectionProofItem()}

	first, err := resolver.project("widgets", items)
	require.NoError(t, err)
	assert.Equal(t, reflect.ValueOf(items).Pointer(), reflect.ValueOf(first).Pointer())
	assert.Equal(t, 1, reader.exactReads)
	assert.Equal(t, expected.Key, reader.exactKey)
	assert.Zero(t, reader.queries)
	assert.Nil(t, resolver.view)

	second, err := resolver.project("widgets", items)
	require.NoError(t, err)
	assert.Equal(t, reflect.ValueOf(items).Pointer(), reflect.ValueOf(second).Pointer())
	assert.Equal(t, 1, reader.exactReads)
	assert.Nil(t, resolver.view)
}

func TestIncrementalDerivedProjectionBypassDoesNotObserveUnwarrantedOwner(t *testing.T) {
	tests := []struct {
		name     string
		session  *incrementalRenderSession
		resource string
		items    []any
	}{
		{
			name:     "empty result",
			session:  derivedProjectionProofSession("widgets", nil),
			resource: "widgets",
			items:    []any{},
		},
		{
			name:     "resource cannot be derived",
			session:  derivedProjectionProofSession("", nil),
			resource: "widgets",
			items:    []any{derivedProjectionProofItem()},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader := &derivedProjectionProofReader{}
			resolver := &incrementalQueryDerivedResourceResolver{
				ctx: t.Context(), reader: reader, session: test.session,
			}

			projected, err := resolver.project(test.resource, test.items)
			require.NoError(t, err)
			assert.Equal(t, test.items, projected)
			assert.Zero(t, reader.exactReads)
			assert.Zero(t, reader.queries)
			assert.Nil(t, resolver.view)
		})
	}
}

func TestIncrementalDerivedProjectionBuildsViewForExactOwner(t *testing.T) {
	owner := &incrementalComponent{name: "governance", deriveResource: true}
	session := derivedProjectionProofSession("widgets", owner)
	reader := &derivedProjectionProofReader{input: deriveOwnerInput("widgets", owner, true)}
	resolver := &incrementalQueryDerivedResourceResolver{
		ctx: t.Context(), reader: reader, session: session,
	}
	items := []any{derivedProjectionProofItem()}

	projected, err := resolver.project("widgets", items)
	require.NoError(t, err)
	assert.Equal(t, items, projected)
	assert.Equal(t, 1, reader.exactReads)
	assert.Equal(t, 1, reader.queries)
	assert.NotNil(t, resolver.view)
}

func TestIncrementalDerivedProjectionRejectsUnauthenticatedOwnerProof(t *testing.T) {
	tests := []struct {
		name   string
		poison func(*incremental.Input)
	}{
		{
			name: "key",
			poison: func(input *incremental.Input) {
				input.Key = incremental.NewInputKey("poisoned")
			},
		},
		{
			name: "revision",
			poison: func(input *incremental.Input) {
				input.Revision = incremental.NewRevision("poisoned")
			},
		},
		{
			name: "presence",
			poison: func(input *incremental.Input) {
				input.Found = true
			},
		},
		{
			name: "value",
			poison: func(input *incremental.Input) {
				input.Value = []byte("poisoned")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session := derivedProjectionProofSession("widgets", nil)
			input := deriveOwnerInput("widgets", nil, false)
			test.poison(&input)
			reader := &derivedProjectionProofReader{input: input}
			resolver := &incrementalQueryDerivedResourceResolver{
				ctx: t.Context(), reader: reader, session: session,
			}

			_, err := resolver.project("widgets", []any{derivedProjectionProofItem()})
			require.ErrorContains(t, err, "does not match its binding")
			assert.Equal(t, 1, reader.exactReads)
			assert.Nil(t, resolver.view)
		})
	}
}

func TestIncrementalDerivedProjectionRejectsStaleAbsenceProof(t *testing.T) {
	session := derivedProjectionProofSession("widgets", nil)
	reader := &derivedProjectionProofReader{input: deriveOwnerInput("widgets", nil, false)}
	resolver := &incrementalQueryDerivedResourceResolver{
		ctx: t.Context(), reader: reader, session: session,
	}
	items := []any{derivedProjectionProofItem()}

	_, err := resolver.project("widgets", items)
	require.NoError(t, err)
	session.bindingPlan.owners["widgets"] = incrementalComponent{name: "governance", deriveResource: true}

	_, err = resolver.project("widgets", items)
	require.ErrorContains(t, err, "owner proof for \"widgets\" is stale")
	assert.Nil(t, resolver.view)
}

func BenchmarkIncrementalDerivedProjectionOwnerAbsent(b *testing.B) {
	session := derivedProjectionProofSession("widgets", nil)
	reader := &derivedProjectionProofReader{input: deriveOwnerInput("widgets", nil, false)}
	items := []any{derivedProjectionProofItem()}
	b.Run("view", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			resolver := &incrementalQueryDerivedResourceResolver{
				ctx: b.Context(), reader: reader, session: session,
			}
			view := rendercontext.NewDerivedResourceViewWithResolver(resolver)
			projected, err := view.Project("widgets", items)
			if err != nil {
				b.Fatal(err)
			}
			derivedProjectionProofSink = projected
		}
	})
	b.Run("bypass", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			resolver := &incrementalQueryDerivedResourceResolver{
				ctx: b.Context(), reader: reader, session: session,
			}
			projected, err := resolver.project("widgets", items)
			if err != nil {
				b.Fatal(err)
			}
			derivedProjectionProofSink = projected
		}
	})
}

func derivedProjectionProofSession(
	source string,
	owner *incrementalComponent,
) *incrementalRenderSession {
	deriveSources := map[string]struct{}{}
	if source != "" {
		deriveSources[source] = struct{}{}
	}
	plan := newIncrementalBindingPlan()
	components := map[string]incrementalComponent{}
	if owner != nil {
		plan.owners[source] = *owner
		plan.props[string(bindingKey(owner.name, source))] = nil
		components[owner.name] = *owner
	}
	return &incrementalRenderSession{
		state: &incrementalRenderState{
			components: components, deriveSources: deriveSources,
		},
		bindingPlan: plan,
	}
}

func derivedProjectionProofItem() map[string]any {
	return map[string]any{
		"apiVersion": "example.test/v1",
		"kind":       "Widget",
		"metadata": map[string]any{
			"namespace": "default",
			"name":      "widget",
		},
	}
}
