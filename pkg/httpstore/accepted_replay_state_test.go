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

package httpstore

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAcceptedReplayStateRebasesUnrelatedABAWithoutReadingEntries(t *testing.T) {
	store := newRevisionTestStore(0)
	const target = "https://target.example.test/value"
	const unrelated = "https://unrelated.example.test/value"
	store.LoadFixture(target, "target")
	state := captureFixtureReplayState(t, store, target)
	originalRoot := state.root

	store.LoadFixture(unrelated, "A")
	store.LoadFixture(unrelated, "B")
	store.LoadFixture(unrelated, "A")
	advanced, ok := store.AdvanceAcceptedReplayState(state)
	require.True(t, ok)
	require.NotSame(t, state, advanced)
	require.Same(t, originalRoot, advanced.root)
	require.Equal(t, store.ReplayWatermark(), advanced.ReplayWatermark())
	require.Equal(t, []ContentSnapshot{store.AcceptedSnapshot(target, SourceDescriptor{})}, advanced.Snapshots())
}

func TestAcceptedReplayStateRejectsRelevantContentSourceAndPendingTransitions(t *testing.T) {
	t.Run("content", func(t *testing.T) {
		store := newRevisionTestStore(0)
		const url = "https://target.example.test/value"
		store.LoadFixture(url, "A")
		state := captureFixtureReplayState(t, store, url)
		store.LoadFixture(url, "B")
		_, ok := store.AdvanceAcceptedReplayState(state)
		require.False(t, ok)
	})

	t.Run("source", func(t *testing.T) {
		store := newRevisionTestStore(0)
		const url = "https://target.example.test/value"
		store.LoadFixture(url, "A")
		state := captureFixtureReplayState(t, store, url)
		_, err := store.ReconcileSource(url, FetchOptions{Critical: true}, nil)
		require.NoError(t, err)
		_, ok := store.AdvanceAcceptedReplayState(state)
		require.False(t, ok)
	})

	t.Run("pending", func(t *testing.T) {
		var body atomic.Value
		body.Store("A")
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(body.Load().(string)))
		}))
		defer server.Close()
		store := newRevisionTestStore(0)
		options := FetchOptions{Critical: true}
		_, err := store.Fetch(t.Context(), server.URL, options, nil)
		require.NoError(t, err)
		descriptor, err := DescribeSource(options, nil)
		require.NoError(t, err)
		snapshot := store.AcceptedSnapshot(server.URL, descriptor)
		state, ok := store.CaptureAcceptedReplayState([]ContentSnapshot{snapshot})
		require.True(t, ok)

		body.Store("B")
		version, err := store.RefreshURLVersion(t.Context(), server.URL)
		require.NoError(t, err)
		require.NotNil(t, version)
		_, ok = store.AdvanceAcceptedReplayState(state)
		require.False(t, ok)
	})
}

func TestAcceptedReplayStateFailsClosedAcrossReplayJournalGap(t *testing.T) {
	store := newRevisionTestStore(0)
	store.replayJournalCapacity = 2
	const target = "https://target.example.test/value"
	store.LoadFixture(target, "target")
	state := captureFixtureReplayState(t, store, target)
	for revision := 0; revision < 3; revision++ {
		store.LoadFixture("https://unrelated.example.test/value", string(rune('A'+revision)))
	}
	_, ok := store.AdvanceAcceptedReplayState(state)
	require.False(t, ok)
}

func TestAcceptedReplayStateRejectsForgedReplayJournalURL(t *testing.T) {
	store := newRevisionTestStore(0)
	const target = "https://target.example.test/value"
	store.LoadFixture(target, "A")
	state := captureFixtureReplayState(t, store, target)
	store.LoadFixture(target, "B")
	store.replayJournal[len(store.replayJournal)-1].URL = "https://unrelated.example.test/value"

	_, ok := store.AdvanceAcceptedReplayState(state)
	require.False(t, ok)
}

func captureFixtureReplayState(
	t *testing.T,
	store *HTTPStore,
	url string,
) *AcceptedReplayState {
	t.Helper()
	snapshot := store.AcceptedSnapshot(url, SourceDescriptor{})
	require.True(t, snapshot.Found)
	state, ok := store.CaptureAcceptedReplayState([]ContentSnapshot{snapshot})
	require.True(t, ok)
	require.NoError(t, state.ValidateAuthentication())
	return state
}
