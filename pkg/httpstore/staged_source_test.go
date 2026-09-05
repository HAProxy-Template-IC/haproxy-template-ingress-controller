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
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStagedSourceAbortLeavesAuthorityAndContentUnchanged(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		_, _ = w.Write([]byte(request.Header.Get("Authorization")))
	}))
	defer server.Close()

	store := New(slog.Default(), 0)
	initialOptions := FetchOptions{Critical: true, Delay: time.Hour}
	initialAuth := &AuthConfig{Type: AuthTypeBearer, Token: "accepted"}
	content, err := store.Fetch(t.Context(), server.URL, initialOptions, initialAuth)
	require.NoError(t, err)
	require.Equal(t, "Bearer accepted", content)

	stateBefore, exists := store.GetSourceState(server.URL)
	require.True(t, exists)
	entryBefore := store.GetEntry(server.URL)
	watermarkBefore := store.Watermark()
	generationBefore := store.nextSourceGeneration

	replacement, err := store.StageSource(
		server.URL,
		FetchOptions{Critical: true, Delay: 2 * time.Hour},
		&AuthConfig{Type: AuthTypeBearer, Token: "replacement"},
	)
	require.NoError(t, err)
	require.True(t, replacement.Changed())
	snapshot, candidate, err := store.PrepareStagedSnapshot(t.Context(), replacement)
	require.NoError(t, err)
	require.NotNil(t, candidate)
	assert.Equal(t, "Bearer replacement", snapshot.Content)

	assert.Equal(t, stateBefore, mustSourceState(t, store, server.URL))
	assert.Equal(t, entryBefore, store.GetEntry(server.URL))
	assert.Equal(t, watermarkBefore, store.Watermark())
	assert.Equal(t, generationBefore, store.nextSourceGeneration)

	prepared, err := store.PrepareStagedSourcesAndVerifyObservations(
		t.Context(),
		[]*StagedSource{replacement},
		[]*InitialCandidate{candidate},
		nil,
	)
	require.NoError(t, err)
	prepared.Abort()

	assert.Equal(t, stateBefore, mustSourceState(t, store, server.URL))
	assert.Equal(t, entryBefore, store.GetEntry(server.URL))
	assert.Equal(t, watermarkBefore, store.Watermark())
	assert.Equal(t, generationBefore, store.nextSourceGeneration)
}

func TestStagedSourcePublishInstallsExactAuthorityAndCandidate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		_, _ = w.Write([]byte(request.Header.Get("Authorization")))
	}))
	defer server.Close()

	store := New(slog.Default(), 0)
	options := FetchOptions{Critical: true, Delay: time.Hour}
	auth := &AuthConfig{Type: AuthTypeBearer, Token: "candidate"}
	source, err := store.StageSource(server.URL, options, auth)
	require.NoError(t, err)
	snapshot, candidate, err := store.PrepareStagedSnapshot(t.Context(), source)
	require.NoError(t, err)
	require.NotNil(t, candidate)
	assert.Nil(t, store.GetEntry(server.URL))

	prepared, err := store.PrepareStagedSourcesAndVerifyObservations(
		t.Context(),
		[]*StagedSource{source},
		[]*InitialCandidate{candidate},
		nil,
	)
	require.NoError(t, err)
	commits, watermark := prepared.Planned()
	require.Len(t, commits, 1)
	prepared.Publish()
	prepared.Release()

	state := mustSourceState(t, store, server.URL)
	assert.Equal(t, source.Descriptor(), state.Descriptor)
	assert.Equal(t, time.Hour, state.Delay)
	assert.True(t, state.HasAccepted)
	accepted := store.AcceptedSnapshot(server.URL, source.Descriptor())
	require.True(t, accepted.Found)
	assert.Equal(t, snapshot.Content, accepted.Content)
	assert.Equal(t, commits[0].Accepted, accepted.Token)
	assert.Equal(t, watermark, store.Watermark())
	assert.True(t, store.VerifySnapshots([]SnapshotToken{accepted.Token}))
}

func TestPrepareStagedSnapshotUsesAcceptedBytesWithoutSharedMutation(t *testing.T) {
	requests := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte("accepted"))
	}))
	defer server.Close()

	store := New(slog.Default(), 0)
	options := FetchOptions{Critical: true, Delay: time.Hour}
	content, err := store.Fetch(t.Context(), server.URL, options, nil)
	require.NoError(t, err)
	require.Equal(t, "accepted", content)
	entryBefore := store.GetEntry(server.URL)
	watermarkBefore := store.Watermark()

	source, err := store.StageSource(server.URL, options, nil)
	require.NoError(t, err)
	require.False(t, source.Changed())
	snapshot, candidate, err := store.PrepareStagedSnapshot(t.Context(), source)
	require.NoError(t, err)

	assert.Equal(t, "accepted", snapshot.Content)
	assert.Equal(t, SnapshotAccepted, snapshot.Token.Kind())
	assert.Nil(t, candidate)
	assert.Equal(t, int32(1), requests.Load())
	assert.Equal(t, entryBefore, store.GetEntry(server.URL))
	assert.Equal(t, watermarkBefore, store.Watermark())
}

func mustSourceState(t *testing.T, store *HTTPStore, url string) SourceState {
	t.Helper()
	state, exists := store.GetSourceState(url)
	require.True(t, exists)
	return state
}
