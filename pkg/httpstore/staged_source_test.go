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
	"sync"
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

func TestCandidateSurvivesUnchangedSourcePublication(t *testing.T) {
	for _, staged := range []bool{true, false} {
		name := "initial"
		if staged {
			name = "staged"
		}
		t.Run(name, func(t *testing.T) {
			for _, duringFetch := range []bool{true, false} {
				phase := "before acceptance"
				if duringFetch {
					phase = "during fetch"
				}
				t.Run(phase, func(t *testing.T) {
					candidateAcrossUnchangedPublication(t, staged, duringFetch)
				})
			}
		})
	}
}

func candidateAcrossUnchangedPublication(t *testing.T, staged, duringFetch bool) {
	t.Helper()
	entered := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		_, _ = w.Write([]byte("candidate"))
	}))
	t.Cleanup(server.Close)
	unblock := sync.OnceFunc(func() { close(release) })
	t.Cleanup(unblock)
	store := New(slog.Default(), 0)
	options := FetchOptions{Critical: true}
	publishUnchangedSource(t, store, server.URL, options)
	source, err := store.StageSource(server.URL, options, nil)
	require.NoError(t, err)
	var candidate *InitialCandidate
	var fetchErr error
	done := make(chan struct{})
	go func() {
		if staged {
			_, candidate, fetchErr = store.PrepareStagedSnapshot(t.Context(), source)
		} else {
			_, candidate, fetchErr = store.PrepareInitialSnapshot(t.Context(), server.URL, source.State())
		}
		close(done)
	}()
	<-entered
	if !duringFetch {
		unblock()
		<-done
	}
	for range 4 {
		publishUnchangedSource(t, store, server.URL, options)
	}
	unblock()
	<-done
	require.NoError(t, fetchErr)
	require.NotNil(t, candidate)
	require.True(t, store.VerifyStagedSource(source))
	var sources []*StagedSource
	if staged {
		sources = []*StagedSource{source}
	}
	prepared, err := store.PrepareStagedSourcesAndVerifyObservations(
		t.Context(), sources, []*InitialCandidate{candidate}, nil,
	)
	require.NoError(t, err)
	prepared.Publish()
	prepared.Release()
	accepted := store.AcceptedSnapshot(server.URL, source.Descriptor())
	require.True(t, accepted.Found)
	assert.Equal(t, "candidate", accepted.Content)
}

func publishUnchangedSource(t *testing.T, store *HTTPStore, url string, options FetchOptions) {
	t.Helper()
	source, err := store.StageSource(url, options, nil)
	require.NoError(t, err)
	prepared, err := store.PrepareStagedSourcesAndVerifyObservations(t.Context(), []*StagedSource{source}, nil, nil)
	require.NoError(t, err)
	prepared.Publish()
	prepared.Release()
}

func TestRefreshSurvivesUnchangedSourcePublication(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			_, _ = w.Write([]byte("accepted"))
			return
		}
		close(entered)
		<-release
		_, _ = w.Write([]byte("refreshed"))
	}))
	t.Cleanup(server.Close)
	unblock := sync.OnceFunc(func() { close(release) })
	t.Cleanup(unblock)
	store := New(slog.Default(), 0)
	options := FetchOptions{Critical: true}
	_, err := store.Fetch(t.Context(), server.URL, options, nil)
	require.NoError(t, err)
	var version *PendingVersion
	var refreshErr error
	done := make(chan struct{})
	go func() {
		version, refreshErr = store.RefreshURLVersion(t.Context(), server.URL)
		close(done)
	}()
	<-entered
	for range 4 {
		publishUnchangedSource(t, store, server.URL, options)
	}
	unblock()
	<-done
	require.NoError(t, refreshErr)
	require.NotNil(t, version)
	assert.True(t, store.PromotePendingVersion(server.URL, version.Checksum, version.Revision))
	content, found := store.Get(server.URL)
	require.True(t, found)
	assert.Equal(t, "refreshed", content)
}
