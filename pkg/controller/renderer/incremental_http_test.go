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
	"fmt"
	"testing"

	iradix "github.com/hashicorp/go-immutable-radix/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/httpstore"
)

func TestHTTPInputChangeLookupUsesExactURLDescriptorIndex(t *testing.T) {
	state := newHTTPRegistryTestState()
	retained := map[uint64]struct{}{}
	for index := range 1000 {
		descriptor, err := httpstore.DescribeSource(httpstore.FetchOptions{}, &httpstore.AuthConfig{
			Type:  httpstore.AuthTypeBearer,
			Token: fmt.Sprintf("credential-%04d", index),
		})
		require.NoError(t, err)
		spec, _, err := state.acquireHTTPInput(httpInputIdentity{
			url:        fmt.Sprintf("https://example.test/%04d", index),
			descriptor: descriptor,
		})
		require.NoError(t, err)
		retained[spec.id] = struct{}{}
	}
	previous, err := httpstore.DescribeSource(httpstore.FetchOptions{}, &httpstore.AuthConfig{
		Type:  httpstore.AuthTypeBearer,
		Token: "previous",
	})
	require.NoError(t, err)
	current, err := httpstore.DescribeSource(httpstore.FetchOptions{}, &httpstore.AuthConfig{
		Type:  httpstore.AuthTypeBearer,
		Token: "current",
	})
	require.NoError(t, err)
	previousSpec, _, err := state.acquireHTTPInput(httpInputIdentity{url: "https://target.test", descriptor: previous})
	require.NoError(t, err)
	currentSpec, _, err := state.acquireHTTPInput(httpInputIdentity{url: "https://target.test", descriptor: current})
	require.NoError(t, err)
	retained[previousSpec.id] = struct{}{}
	retained[currentSpec.id] = struct{}{}

	change := httpstore.SemanticChange{
		URL:                "https://target.test",
		PreviousDescriptor: previous,
		Descriptor:         current,
	}
	affected := state.httpInputsForChange(&change)
	require.Len(t, affected, 2)
	assert.Equal(t, previousSpec.id, affected[0].id)
	assert.Equal(t, currentSpec.id, affected[1].id)

	state.finishHTTPInputs(retained, nil, iradix.New[*iradix.Tree[incrementalHTTPEffect]](), true)
	assert.Empty(t, state.httpIDs)
	assert.Empty(t, state.httpSpecs)
	assert.Empty(t, state.httpByURL)
}

func TestHTTPInputReferencesRetireCredentialsAfterLastCommittedConsumer(t *testing.T) {
	state := newHTTPRegistryTestState()
	descriptor, err := httpstore.DescribeSource(httpstore.FetchOptions{}, &httpstore.AuthConfig{
		Type:  httpstore.AuthTypeBearer,
		Token: "retire-me",
	})
	require.NoError(t, err)
	identity := httpInputIdentity{url: "https://example.test/secret", descriptor: descriptor}
	spec, _, err := state.acquireHTTPInput(identity)
	require.NoError(t, err)

	txn := iradix.New[*iradix.Tree[incrementalHTTPEffect]]().Txn()
	txn.Insert([]byte("consumer"), mustIndexedHTTPEffects(t, incrementalHTTPEffect{inputID: spec.id}))
	state.finishHTTPInputs(map[uint64]struct{}{spec.id: {}}, nil, txn.Commit(), true)
	assert.Equal(t, uint64(1), state.httpRefs[spec.id])
	assert.Contains(t, state.httpIDs, identity)

	state.finishHTTPInputs(nil, map[uint64]httpRefDelta{
		spec.id: {removed: 1},
	}, nil, true)
	assert.NotContains(t, state.httpIDs, identity)
	assert.NotContains(t, state.httpSpecs, spec.id)
	assert.NotContains(t, state.httpByURL, identity.url)

	reacquired, _, err := state.acquireHTTPInput(identity)
	require.NoError(t, err)
	assert.Greater(t, reacquired.id, spec.id)
	state.finishHTTPInputs(map[uint64]struct{}{reacquired.id: {}}, nil, nil, false)
}

func TestHTTPReferenceDeltasScaleWithChangedEffects(t *testing.T) {
	tree := iradix.New[*iradix.Tree[incrementalHTTPEffect]]().Txn()
	for id := uint64(1); id <= 1000; id++ {
		tree.Insert([]byte(fmt.Sprintf("consumer-%04d", id)), mustIndexedHTTPEffects(t, incrementalHTTPEffect{inputID: id}))
	}
	runtime := &incrementalRenderSession{
		httpEffects:   tree.Commit().Txn(),
		httpRefDeltas: map[uint64]httpRefDelta{},
	}

	replaced, err := runtime.replaceHTTPEffects(
		[]byte("consumer-0500"),
		[]incrementalHTTPEffect{{inputID: 1001}},
	)
	require.NoError(t, err)

	require.True(t, replaced)
	assert.Equal(t, map[uint64]httpRefDelta{
		500:  {removed: 1},
		1001: {added: 1},
	}, runtime.httpRefDeltas)
}

func TestHTTPReferenceDeltasPublishOnlyWithSuccessfulSession(t *testing.T) {
	state := newHTTPRegistryTestState()
	oldIdentity := httpInputIdentity{url: "https://example.test/old"}
	newIdentity := httpInputIdentity{url: "https://example.test/new"}
	oldSpec, _, err := state.acquireHTTPInput(oldIdentity)
	require.NoError(t, err)
	baseTxn := iradix.New[*iradix.Tree[incrementalHTTPEffect]]().Txn()
	baseTxn.Insert([]byte("consumer"), mustIndexedHTTPEffects(t, incrementalHTTPEffect{inputID: oldSpec.id}))
	base := baseTxn.Commit()
	state.finishHTTPInputs(map[uint64]struct{}{oldSpec.id: {}}, nil, base, true)

	require.NoError(t, state.retainHTTPInputSpec(oldSpec.id))
	abortedNew, _, err := state.acquireHTTPInput(newIdentity)
	require.NoError(t, err)
	aborted := &incrementalRenderSession{
		state:         state,
		httpEffects:   base.Txn(),
		httpRefDeltas: map[uint64]httpRefDelta{},
		httpRetained: map[uint64]struct{}{
			oldSpec.id:    {},
			abortedNew.id: {},
		},
	}
	abortedChanged, err := aborted.replaceHTTPEffects(
		[]byte("consumer"),
		[]incrementalHTTPEffect{{inputID: abortedNew.id}},
	)
	require.NoError(t, err)
	require.True(t, abortedChanged)
	aborted.abort()
	assert.Equal(t, uint64(1), state.httpRefs[oldSpec.id])
	assert.Contains(t, state.httpSpecs, oldSpec.id)
	assert.NotContains(t, state.httpSpecs, abortedNew.id)

	require.NoError(t, state.retainHTTPInputSpec(oldSpec.id))
	committedNew, _, err := state.acquireHTTPInput(newIdentity)
	require.NoError(t, err)
	committed := &incrementalRenderSession{
		state:         state,
		httpEffects:   base.Txn(),
		httpRefDeltas: map[uint64]httpRefDelta{},
		httpRetained: map[uint64]struct{}{
			oldSpec.id:      {},
			committedNew.id: {},
		},
	}
	committedChanged, err := committed.replaceHTTPEffects(
		[]byte("consumer"),
		[]incrementalHTTPEffect{{inputID: committedNew.id}},
	)
	require.NoError(t, err)
	require.True(t, committedChanged)
	committed.finishHTTPInputs(true, committed.httpEffects.Commit())
	assert.NotContains(t, state.httpSpecs, oldSpec.id)
	assert.Equal(t, uint64(1), state.httpRefs[committedNew.id])
	assert.Contains(t, state.httpSpecs, committedNew.id)

	state.finishHTTPInputs(nil, map[uint64]httpRefDelta{
		committedNew.id: {removed: 1},
	}, nil, true)
}

func newHTTPRegistryTestState() *incrementalRenderState {
	return &incrementalRenderState{
		httpIDs:    map[httpInputIdentity]uint64{},
		httpSpecs:  map[uint64]httpInputSpec{},
		httpByURL:  map[string]map[httpstore.SourceDescriptor]uint64{},
		httpRefs:   map[uint64]uint64{},
		httpFlight: map[uint64]uint64{},
	}
}

func mustIndexedHTTPEffects(
	t *testing.T,
	effects ...incrementalHTTPEffect,
) *iradix.Tree[incrementalHTTPEffect] {
	t.Helper()
	indexed, err := newIncrementalIndexedHTTPEffects(effects)
	require.NoError(t, err)
	return indexed
}
