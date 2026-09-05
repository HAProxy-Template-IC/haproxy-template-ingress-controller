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

package renderer

import (
	"testing"

	iradix "github.com/hashicorp/go-immutable-radix/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/httpstore"
)

func TestIncrementalHTTPEffectCacheOwnsCallerValues(t *testing.T) {
	effects := []incrementalHTTPEffect{{
		inputID: 17,
		snapshot: httpstore.ContentSnapshot{
			URL: "https://example.test/config", Content: "original", Found: true,
		},
	}}
	runtime := &incrementalRenderSession{
		httpEffects:   iradix.New[*iradix.Tree[incrementalHTTPEffect]]().Txn(),
		httpRefDeltas: map[uint64]httpRefDelta{},
	}
	changed, err := runtime.replaceHTTPEffects([]byte("result"), effects)
	require.NoError(t, err)
	require.True(t, changed)

	effects[0].inputID = 99
	effects[0].snapshot.Content = "poison"
	stored, found := runtime.httpEffects.Get([]byte("result"))
	require.True(t, found)
	value, found := stored.Root().Get(incrementalHTTPIdentityKey(17))
	require.True(t, found)
	assert.Equal(t, "original", value.snapshot.Content)
	assert.Equal(t, map[uint64]httpRefDelta{17: {added: 1}}, runtime.httpRefDeltas)

	fork := stored.Txn()
	fork.Insert(incrementalHTTPIdentityKey(17), incrementalHTTPEffect{
		inputID: 17,
		snapshot: httpstore.ContentSnapshot{
			URL: "https://example.test/config", Content: "fork", Found: true,
		},
	})
	value, found = stored.Root().Get(incrementalHTTPIdentityKey(17))
	require.True(t, found)
	assert.Equal(t, "original", value.snapshot.Content)
}

func TestIncrementalHTTPEffectDuplicateFailsAtomically(t *testing.T) {
	runtime := &incrementalRenderSession{
		httpEffects:   iradix.New[*iradix.Tree[incrementalHTTPEffect]]().Txn(),
		httpRefDeltas: map[uint64]httpRefDelta{},
	}
	root := runtime.httpEffects.Root()
	changed, err := runtime.replaceHTTPEffects([]byte("result"), []incrementalHTTPEffect{
		{inputID: 17},
		{inputID: 17},
	})
	require.ErrorContains(t, err, "repeat input 17")
	assert.False(t, changed)
	assert.Same(t, root, runtime.httpEffects.Root())
	assert.Empty(t, runtime.httpRefDeltas)
}

func TestIncrementalHTTPEffectSnapshotRejectsLeafSubstitution(t *testing.T) {
	snapshot := newIncrementalStateSnapshot()
	key := []byte("result")
	effects := mustIndexedHTTPEffects(t, incrementalHTTPEffect{inputID: 17})
	txn := snapshot.httpEffects.Txn()
	txn.Insert(key, effects)
	snapshot.httpEffects = txn.Commit()
	authenticateIncrementalStateSnapshot(snapshot)
	require.NoError(t, validateIncrementalStateSnapshotAuthentication(snapshot))

	poisoned := effects.Txn()
	poisoned.Insert(incrementalHTTPIdentityKey(17), incrementalHTTPEffect{
		inputID: 17, snapshot: httpstore.ContentSnapshot{Content: "poison"},
	})
	outer := snapshot.httpEffects.Txn()
	outer.Insert(key, poisoned.Commit())
	snapshot.httpEffects = outer.Commit()
	require.ErrorContains(t, validateIncrementalStateSnapshotAuthentication(snapshot), "persistent root changed")
}
