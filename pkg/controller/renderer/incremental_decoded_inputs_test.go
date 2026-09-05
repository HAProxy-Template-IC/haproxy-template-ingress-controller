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
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestDecodedInputCacheSharesOneAuthenticatedSnapshot(t *testing.T) {
	renderSession := &incrementalRenderSession{}
	inputKey := incremental.NewInputKey("resource/routes/default/route")
	definition := func(key string) incremental.Definition {
		return incremental.Definition{
			Key: incremental.NewQueryKey(key),
			Run: func(_ context.Context, reader incremental.Reader) ([]byte, error) {
				object, _, _, found, err := renderSession.decodeComponentInputWithEncoding(
					reader, inputKey, "routes", "source object", true,
				)
				if err != nil || !found {
					return nil, err
				}
				metadata, ok := object["metadata"].(map[string]any)
				if !ok {
					return nil, fmt.Errorf("metadata is %T", object["metadata"])
				}
				return []byte(metadata["name"].(string)), nil
			},
		}
	}
	graph, err := incremental.New(definition("a"), definition("b"))
	require.NoError(t, err)
	session, err := graph.Begin()
	require.NoError(t, err)
	require.NoError(t, session.ApplyInputs(incremental.Input{
		Key:      inputKey,
		Revision: incremental.NewRevision("revision-1"),
		Found:    true,
		Value:    []byte(`{"metadata":{"name":"route"}}`),
	}))

	results, err := session.EvaluateAll(t.Context(),
		incremental.NewQueryKey("a"), incremental.NewQueryKey("b"),
	)
	require.NoError(t, err)
	require.Len(t, results, 2)
	assert.Equal(t, "route", string(results[0].Value))
	assert.Equal(t, "route", string(results[1].Value))
	assert.Equal(t, 1, renderSession.decodedInputs.len())
	session.Abort()
}

func TestDecodedInputCacheFallbackReauthenticatesBytes(t *testing.T) {
	inputKey := incremental.NewInputKey("resource/routes/default/route")
	reader := &decodedInputFallbackReader{input: incremental.Input{
		Key:      inputKey,
		Revision: incremental.NewRevision("revision-1"),
		Found:    true,
		Value:    []byte(`{"metadata":{"name":"route"}}`),
	}}
	renderSession := &incrementalRenderSession{}
	object, _, _, found, err := renderSession.decodeComponentInputWithEncoding(
		reader, inputKey, "routes", "source object", true,
	)
	require.NoError(t, err)
	require.True(t, found)
	require.NotNil(t, object)

	reader.input.Value = []byte(`{"metadata":{"name":"poison"}}`)
	object, _, _, found, err = renderSession.decodeComponentInputWithEncoding(
		reader, inputKey, "routes", "source object", true,
	)
	require.ErrorIs(t, err, incremental.ErrRevisionConflict)
	require.False(t, found)
	require.Nil(t, object)
}

func TestCachedInputLegacyObserverCannotMutateCachedString(t *testing.T) {
	key := incremental.NewInputKey("input")
	revision := incremental.NewRevision("revision")
	const encoded = "immutable\x00\xff"
	first := &decodedInputMutatingLegacyObserver{}
	observed, err := observeCachedIncrementalInput(first, key, revision, true, encoded)
	require.NoError(t, err)
	require.True(t, observed)
	require.Equal(t, byte('X'), first.value[0])

	second := &decodedInputMutatingLegacyObserver{}
	observed, err = observeCachedIncrementalInput(second, key, revision, true, encoded)
	require.NoError(t, err)
	require.True(t, observed)
	require.Equal(t, "X"+encoded[1:], string(second.value))
	require.Equal(t, "immutable\x00\xff", encoded)
}

func TestDecodedInputCacheObserverRejectsSameRevisionWithDifferentBytes(t *testing.T) {
	renderSession := &incrementalRenderSession{}
	inputKey := incremental.NewInputKey("resource/routes/default/route")
	definition := func(key string) incremental.Definition {
		return incremental.Definition{
			Key: incremental.NewQueryKey(key),
			Run: func(_ context.Context, reader incremental.Reader) ([]byte, error) {
				object, _, _, _, err := renderSession.decodeComponentInputWithEncoding(
					reader, inputKey, "routes", "source object", true,
				)
				if err != nil {
					return nil, err
				}
				return []byte(object["metadata"].(map[string]any)["name"].(string)), nil
			},
		}
	}
	graph, err := incremental.New(definition("a"), definition("b"))
	require.NoError(t, err)
	session, err := graph.Begin()
	require.NoError(t, err)
	require.NoError(t, session.ApplyInputs(incremental.Input{
		Key: inputKey, Revision: incremental.NewRevision("same-revision"), Found: true,
		Value: []byte(`{"metadata":{"name":"route"}}`),
	}))
	_, err = session.Evaluate(t.Context(), incremental.NewQueryKey("a"))
	require.NoError(t, err)
	requireDecodedInput(t, renderSession, inputKey).encoded = `{"metadata":{"name":"poison"}}`

	_, err = session.Evaluate(t.Context(), incremental.NewQueryKey("b"))
	require.ErrorIs(t, err, incremental.ErrRevisionConflict)
	session.Abort()
}

func TestDecodedResourceInputCacheObserverRejectsSameRevisionWithDifferentBytes(t *testing.T) {
	spec := resourceInputSpec{resourceType: "routes", scope: resourceInputList}
	inputKey := resourceInputKey(&spec)
	renderSession := &incrementalRenderSession{
		resourceProofs: map[incremental.InputKey]incremental.Input{},
	}
	definition := func(key string) incremental.Definition {
		return incremental.Definition{
			Key: incremental.NewQueryKey(key),
			Run: func(_ context.Context, reader incremental.Reader) ([]byte, error) {
				items, _, err := renderSession.decodeResourceInput(reader, &spec)
				if err != nil {
					return nil, err
				}
				return []byte(items[0].(map[string]any)["name"].(string)), nil
			},
		}
	}
	graph, err := incremental.New(definition("a"), definition("b"))
	require.NoError(t, err)
	session, err := graph.Begin()
	require.NoError(t, err)
	require.NoError(t, session.ApplyInputs(incremental.Input{
		Key: inputKey, Revision: incremental.NewRevision("same-revision"), Found: true,
		Value: []byte(`[{"name":"route"}]`),
	}))
	_, err = session.Evaluate(t.Context(), incremental.NewQueryKey("a"))
	require.NoError(t, err)
	requireDecodedResourceInput(t, renderSession, inputKey).encoded = `[{"name":"poison"}]`

	_, err = session.Evaluate(t.Context(), incremental.NewQueryKey("b"))
	require.ErrorIs(t, err, incremental.ErrRevisionConflict)
	session.Abort()
}

func TestDecodedPublicationInputCacheReusesAuthenticatedValue(t *testing.T) {
	inputKey := incrementalSelectorInputKey("policies", "targets", "service")
	reader := &decodedInputFallbackReader{input: incremental.Input{
		Key:      inputKey,
		Revision: incremental.NewRevision("revision-1"),
		Found:    true,
		Value:    []byte(`{"name":"policy"}`),
	}}
	renderSession := &incrementalRenderSession{}

	first, firstCertificate, found, err := renderSession.decodePublicationInput(reader, inputKey)
	require.NoError(t, err)
	require.True(t, found)
	second, secondCertificate, found, err := renderSession.decodePublicationInput(reader, inputKey)
	require.NoError(t, err)
	require.True(t, found)

	assert.Equal(t, reflect.ValueOf(first).Pointer(), reflect.ValueOf(second).Pointer())
	assert.Same(t, firstCertificate, secondCertificate)
	assert.Equal(t, 2, reader.exactCalls)
}

func TestDecodedPublicationInputCacheRejectsPoison(t *testing.T) {
	inputKey := incrementalSelectorInputKey("policies", "targets", "service")
	newFixture := func(t *testing.T) (*incrementalRenderSession, *decodedInputFallbackReader) {
		t.Helper()
		reader := &decodedInputFallbackReader{input: incremental.Input{
			Key:      inputKey,
			Revision: incremental.NewRevision("revision-1"),
			Found:    true,
			Value:    []byte(`{"name":"policy"}`),
		}}
		renderSession := &incrementalRenderSession{}
		_, _, _, err := renderSession.decodePublicationInput(reader, inputKey)
		require.NoError(t, err)
		return renderSession, reader
	}

	t.Run("encoded identity", func(t *testing.T) {
		renderSession, reader := newFixture(t)
		requireDecodedInput(t, renderSession, inputKey).encoded = `{"name":"poison"}`

		_, _, _, err := renderSession.decodePublicationInput(reader, inputKey)
		require.ErrorIs(t, err, incremental.ErrRevisionConflict)
	})

	t.Run("decoded provenance", func(t *testing.T) {
		renderSession, reader := newFixture(t)
		requireDecodedInput(t, renderSession, inputKey).value.seal = nil

		_, _, _, err := renderSession.decodePublicationInput(reader, inputKey)
		require.ErrorContains(t, err, "invalid provenance")
		assert.Equal(t, 2, reader.exactCalls)
	})
}

func TestDecodedPublicationInputCacheRejectsCrossRevisionReuse(t *testing.T) {
	inputKey := incrementalSelectorValuesInputKey("policies", "targets")
	reader := &decodedInputFallbackReader{input: incremental.Input{
		Key:      inputKey,
		Revision: incremental.NewRevision("revision-1"),
		Found:    true,
		Value:    []byte(`[{"name":"policy"}]`),
	}}
	renderSession := &incrementalRenderSession{}
	value, _, found, err := renderSession.decodePublicationInput(reader, inputKey)
	require.NoError(t, err)
	require.True(t, found)
	require.NotNil(t, value)

	reader.input.Revision = incremental.NewRevision("revision-2")
	value, _, found, err = renderSession.decodePublicationInput(reader, inputKey)
	require.ErrorIs(t, err, incremental.ErrRevisionConflict)
	require.False(t, found)
	require.Nil(t, value)
}

func TestDecodedInputCachesOwnCanonicalBytes(t *testing.T) {
	inputKey := incremental.NewInputKey("resource/routes/default/route")
	inputBytes := []byte(`{"metadata":{"name":"route"}}`)
	reader := &decodedInputFallbackReader{input: incremental.Input{
		Key: inputKey, Revision: incremental.NewRevision("revision-1"), Found: true, Value: inputBytes,
	}}
	renderSession := &incrementalRenderSession{}

	_, returned, _, found, err := renderSession.decodeComponentInputWithEncoding(reader, inputKey, "routes", "source", true)
	require.NoError(t, err)
	require.True(t, found)
	inputBytes[len(inputBytes)-3] = 'X'
	returned[0] = 'X'
	assert.Equal(t, `{"metadata":{"name":"route"}}`, requireDecodedInput(t, renderSession, inputKey).encoded)

	reader.input.Value = []byte(`{"metadata":{"name":"route"}}`)
	_, returned, _, found, err = renderSession.decodeComponentInputWithEncoding(reader, inputKey, "routes", "source", true)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, `{"metadata":{"name":"route"}}`, string(returned))
}

func TestDecodedComponentInputOnlyReturnsDetachedEncodingWhenRequested(t *testing.T) {
	inputKey := incremental.NewInputKey("resource/routes/default/route")
	const encoded = `{"metadata":{"name":"route"}}`
	reader := &decodedInputFallbackReader{input: incremental.Input{
		Key: inputKey, Revision: incremental.NewRevision("revision-1"), Found: true,
		Value: []byte(encoded),
	}}
	renderSession := &incrementalRenderSession{}

	_, skipped, _, found, err := renderSession.decodeComponentInputWithEncoding(
		reader, inputKey, "routes", "source", false,
	)
	require.NoError(t, err)
	require.True(t, found)
	require.Nil(t, skipped)

	_, detached, _, found, err := renderSession.decodeComponentInputWithEncoding(
		reader, inputKey, "routes", "source", true,
	)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, encoded, string(detached))
	detached[0] = 'X'
	require.Equal(t, encoded, requireDecodedInput(t, renderSession, inputKey).encoded)
}

func TestAuthenticateComponentProjectionPreservesExactCertificateOrFailsClosed(t *testing.T) {
	original := map[string]any{
		"metadata": map[string]any{"name": "route", "annotations": map[string]any{}},
	}
	originalCertificate := templating.CertifyIncrementalImmutableInputs(original)
	renderSession := &incrementalRenderSession{}

	unchanged, unchangedCertificate, err := renderSession.authenticateComponentProjection(
		"routes", original, nil, originalCertificate, false,
	)
	require.NoError(t, err)
	require.Equal(t, reflect.ValueOf(original).Pointer(), reflect.ValueOf(unchanged).Pointer())
	require.Same(t, originalCertificate, unchangedCertificate)

	projected := map[string]any{
		"metadata": map[string]any{
			"name": "route", "annotations": map[string]any{"governed": "yes"},
		},
	}
	projectedBytes, err := encodeResourceValue(projected)
	require.NoError(t, err)
	require.False(t, originalCertificate.Guards(projected))

	_, _, err = renderSession.authenticateComponentProjection(
		"routes", projected, projectedBytes, originalCertificate, false,
	)
	require.ErrorContains(t, err, "invalid immutable provenance")

	authenticated, projectedCertificate, err := renderSession.authenticateComponentProjection(
		"routes", projected, projectedBytes, originalCertificate, true,
	)
	require.NoError(t, err)
	require.Equal(t, reflect.ValueOf(projected).Pointer(), reflect.ValueOf(authenticated).Pointer())
	require.NotSame(t, originalCertificate, projectedCertificate)
	require.True(t, projectedCertificate.Guards(projected))
}

func TestDecodedValueCachesReauthenticateSeals(t *testing.T) {
	t.Run("component object", func(t *testing.T) {
		inputKey := incremental.NewInputKey("resource/routes/default/route")
		reader := &decodedInputFallbackReader{input: incremental.Input{
			Key: inputKey, Revision: incremental.NewRevision("revision-1"), Found: true,
			Value: []byte(`{"metadata":{"name":"route"}}`),
		}}
		renderSession := &incrementalRenderSession{}
		_, _, _, _, err := renderSession.decodeComponentInputWithEncoding(reader, inputKey, "routes", "source", true)
		require.NoError(t, err)
		requireDecodedInput(t, renderSession, inputKey).value.seal = nil

		_, _, _, _, err = renderSession.decodeComponentInputWithEncoding(reader, inputKey, "routes", "source", true)
		require.ErrorContains(t, err, "invalid provenance")
		assert.Equal(t, 2, reader.exactCalls)
	})

	t.Run("resource items", func(t *testing.T) {
		spec := resourceInputSpec{resourceType: "routes", scope: resourceInputList}
		inputKey := resourceInputKey(&spec)
		reader := &decodedInputFallbackReader{input: incremental.Input{
			Key: inputKey, Revision: incremental.NewRevision("revision-1"), Found: true,
			Value: []byte(`[{"metadata":{"name":"route"}}]`),
		}}
		renderSession := &incrementalRenderSession{resourceProofs: map[incremental.InputKey]incremental.Input{}}
		_, _, err := renderSession.decodeResourceInput(reader, &spec)
		require.NoError(t, err)
		requireDecodedResourceInput(t, renderSession, inputKey).value.seal = nil

		_, _, err = renderSession.decodeResourceInput(reader, &spec)
		require.ErrorContains(t, err, "invalid provenance")
		assert.Equal(t, 2, reader.exactCalls)
	})
}

func TestDecodedComponentInterningKeepsDistinctDependencyRoots(t *testing.T) {
	leftInput := incremental.NewInputKey("resource/routes/default/left")
	rightInput := incremental.NewInputKey("resource/routes/default/right")
	renderSession := &incrementalRenderSession{}
	definition := func(query string, inputKey incremental.InputKey) incremental.Definition {
		return incremental.Definition{
			Key: incremental.NewQueryKey(query),
			Run: func(_ context.Context, reader incremental.Reader) ([]byte, error) {
				object, _, _, _, err := renderSession.decodeComponentInputWithEncoding(reader, inputKey, "routes", "source", true)
				if err != nil {
					return nil, err
				}
				return []byte(object["metadata"].(map[string]any)["name"].(string)), nil
			},
		}
	}
	graph, err := incremental.New(definition("left", leftInput), definition("right", rightInput))
	require.NoError(t, err)
	session, err := graph.Begin()
	require.NoError(t, err)
	value := []byte(`{"metadata":{"name":"route"}}`)
	require.NoError(t, session.ApplyInputs(
		incremental.Input{Key: leftInput, Revision: incremental.NewRevision("left-1"), Found: true, Value: value},
		incremental.Input{Key: rightInput, Revision: incremental.NewRevision("right-1"), Found: true, Value: value},
	))
	_, err = session.EvaluateAll(t.Context(), incremental.NewQueryKey("left"), incremental.NewQueryKey("right"))
	require.NoError(t, err)
	assert.Same(
		t,
		requireDecodedInput(t, renderSession, leftInput).value,
		requireDecodedInput(t, renderSession, rightInput).value,
	)
	require.NoError(t, session.Commit(t.Context(), acceptDecodedInputRevisions))
	assert.True(t, graph.HasInputDependents(leftInput))
	assert.True(t, graph.HasInputDependents(rightInput))
}

func TestDecodedPublicationInterningKeepsDistinctDependencyRoots(t *testing.T) {
	leftInput := incrementalSelectorInputKey("policies", "targets", "left")
	rightInput := incrementalSelectorInputKey("policies", "targets", "right")
	renderSession := &incrementalRenderSession{}
	definition := func(query string, inputKey incremental.InputKey) incremental.Definition {
		return incremental.Definition{
			Key: incremental.NewQueryKey(query),
			Run: func(_ context.Context, reader incremental.Reader) ([]byte, error) {
				value, _, _, err := renderSession.decodePublicationInput(reader, inputKey)
				if err != nil {
					return nil, err
				}
				return []byte(value.(map[string]any)["name"].(string)), nil
			},
		}
	}
	graph, err := incremental.New(definition("left", leftInput), definition("right", rightInput))
	require.NoError(t, err)
	session, err := graph.Begin()
	require.NoError(t, err)
	value := []byte(`{"name":"policy"}`)
	require.NoError(t, session.ApplyInputs(
		incremental.Input{Key: leftInput, Revision: incremental.NewRevision("left-1"), Found: true, Value: value},
		incremental.Input{Key: rightInput, Revision: incremental.NewRevision("right-1"), Found: true, Value: value},
	))
	_, err = session.EvaluateAll(t.Context(), incremental.NewQueryKey("left"), incremental.NewQueryKey("right"))
	require.NoError(t, err)
	assert.Same(
		t,
		requireDecodedInput(t, renderSession, leftInput).value,
		requireDecodedInput(t, renderSession, rightInput).value,
	)
	require.NoError(t, session.Commit(t.Context(), acceptDecodedInputRevisions))
	assert.True(t, graph.HasInputDependents(leftInput))
	assert.True(t, graph.HasInputDependents(rightInput))
}

func TestDecodedResourceInterningKeepsDistinctDependencyRoots(t *testing.T) {
	leftSpec := resourceInputSpec{resourceType: "routes", scope: resourceInputGet, keys: []string{"left"}}
	rightSpec := resourceInputSpec{resourceType: "routes", scope: resourceInputGet, keys: []string{"right"}}
	leftInput := resourceInputKey(&leftSpec)
	rightInput := resourceInputKey(&rightSpec)
	renderSession := &incrementalRenderSession{resourceProofs: map[incremental.InputKey]incremental.Input{}}
	definition := func(query string, spec *resourceInputSpec) incremental.Definition {
		return incremental.Definition{
			Key: incremental.NewQueryKey(query),
			Run: func(_ context.Context, reader incremental.Reader) ([]byte, error) {
				items, _, err := renderSession.decodeResourceInput(reader, spec)
				if err != nil {
					return nil, err
				}
				return []byte(items[0].(map[string]any)["name"].(string)), nil
			},
		}
	}
	graph, err := incremental.New(definition("left", &leftSpec), definition("right", &rightSpec))
	require.NoError(t, err)
	session, err := graph.Begin()
	require.NoError(t, err)
	value := []byte(`[{"name":"route"}]`)
	require.NoError(t, session.ApplyInputs(
		incremental.Input{Key: leftInput, Revision: incremental.NewRevision("left-1"), Found: true, Value: value},
		incremental.Input{Key: rightInput, Revision: incremental.NewRevision("right-1"), Found: true, Value: value},
	))
	_, err = session.EvaluateAll(t.Context(), incremental.NewQueryKey("left"), incremental.NewQueryKey("right"))
	require.NoError(t, err)
	assert.Same(
		t,
		requireDecodedResourceInput(t, renderSession, leftInput).value,
		requireDecodedResourceInput(t, renderSession, rightInput).value,
	)
	require.NoError(t, session.Commit(t.Context(), acceptDecodedInputRevisions))
	assert.True(t, graph.HasInputDependents(leftInput))
	assert.True(t, graph.HasInputDependents(rightInput))
}

func acceptDecodedInputRevisions(context.Context, []incremental.InputRevision) (bool, error) {
	return true, nil
}

func requireDecodedInput(
	t *testing.T,
	renderSession *incrementalRenderSession,
	key incremental.InputKey,
) *incrementalDecodedInput {
	t.Helper()
	value, found, err := renderSession.decodedInputs.load(key, incrementalDecodedCacheStringHash(key.Opaque()))
	require.NoError(t, err)
	require.True(t, found)
	return value
}

func requireDecodedResourceInput(
	t *testing.T,
	renderSession *incrementalRenderSession,
	key incremental.InputKey,
) *incrementalDecodedResourceInput {
	t.Helper()
	value, found, err := renderSession.decodedResourceInputs.load(
		key,
		incrementalDecodedCacheStringHash(key.Opaque()),
	)
	require.NoError(t, err)
	require.True(t, found)
	return value
}

type decodedInputFallbackReader struct {
	input      incremental.Input
	exactCalls int
}

func (r *decodedInputFallbackReader) Input(key incremental.InputKey) (value []byte, found bool, err error) {
	input, err := r.ExactInput(key)
	return input.Value, input.Found, err
}

func (r *decodedInputFallbackReader) ExactInput(key incremental.InputKey) (incremental.Input, error) {
	r.exactCalls++
	if key != r.input.Key {
		return incremental.Input{}, errors.New("unknown input")
	}
	input := r.input
	input.Value = slices.Clone(input.Value)
	return input, nil
}

func (*decodedInputFallbackReader) Query(context.Context, incremental.QueryKey) ([]byte, error) {
	return nil, errors.New("queries are unsupported")
}

type decodedInputMutatingLegacyObserver struct {
	incremental.ExactInputValueObserver
	value []byte
}

func (r *decodedInputMutatingLegacyObserver) ObserveExactInputValue(input incremental.Input) error {
	r.value = input.Value
	r.value[0] = 'X'
	return nil
}
