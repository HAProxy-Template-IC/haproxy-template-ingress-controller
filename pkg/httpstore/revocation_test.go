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
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func acceptInitial(t *testing.T, store *HTTPStore, url string, options FetchOptions) SourceState {
	t.Helper()
	reconciled, err := store.ReconcileSource(url, options, nil)
	require.NoError(t, err)
	_, candidate, err := store.PrepareInitial(t.Context(), url, reconciled.State)
	require.NoError(t, err)
	require.NoError(t, store.CommitInitialCandidates(t.Context(), []*InitialCandidate{candidate}))
	return reconciled.State
}

func TestRevokingAnInitialAcceptanceLeavesTheSourceUnaccepted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("page"))
	}))
	defer server.Close()
	store := New(slog.New(slog.NewTextHandler(io.Discard, nil)), 0)
	state := acceptInitial(t, store, server.URL, FetchOptions{})
	watermark := store.Watermark()

	revoked := store.RevokeAcceptances(store.AcceptanceSequence())

	require.Len(t, revoked, 1)
	assert.Equal(t, server.URL, revoked[0].URL)
	assert.False(t, revoked[0].Restored)
	_, accepted := store.GetSource(server.URL, state.Descriptor)
	assert.False(t, accepted)
	assert.Greater(t, store.Watermark(), watermark, "a revocation is a change renders must see")

	acceptInitial(t, store, server.URL, FetchOptions{})
	content, accepted := store.GetSource(server.URL, state.Descriptor)
	require.True(t, accepted, "a revoked source can be accepted again")
	assert.Equal(t, "page", content)
}

func TestRevokingARefreshRestoresThePreviousVersion(t *testing.T) {
	var version atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if version.Load() == 0 {
			w.Header().Set("ETag", `"first"`)
			_, _ = w.Write([]byte("v1"))
			return
		}
		_, _ = w.Write([]byte("v2"))
	}))
	defer server.Close()
	store := New(slog.New(slog.NewTextHandler(io.Discard, nil)), 0)
	state := acceptInitial(t, store, server.URL, FetchOptions{Delay: time.Minute})
	snapshot := store.AcceptedSnapshot(server.URL, state.Descriptor)
	store.ConfirmAcceptances([]ObservationToken{snapshot.ObservationToken()})
	version.Store(1)
	pending, err := store.RefreshURLVersion(t.Context(), server.URL)
	require.NoError(t, err)
	require.True(t, store.PromotePendingVersion(server.URL, pending.Checksum, pending.Revision))

	revoked := store.RevokeAcceptances(store.AcceptanceSequence())

	require.Len(t, revoked, 1)
	assert.True(t, revoked[0].Restored)
	content, accepted := store.Get(server.URL)
	require.True(t, accepted)
	assert.Equal(t, "v1", content)
	entry := store.GetEntry(server.URL)
	assert.Empty(t, entry.ETag, "the next refresh fetches the revoked bytes again instead of a 304")
}

func TestRevocationReachesOnlyUnconfirmedAcceptancesUpToTheRender(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.URL.Path))
	}))
	defer server.Close()
	store := New(slog.New(slog.NewTextHandler(io.Discard, nil)), 0)
	state := acceptInitial(t, store, server.URL+"/confirmed", FetchOptions{})
	snapshot := store.AcceptedSnapshot(server.URL+"/confirmed", state.Descriptor)
	store.ConfirmAcceptances([]ObservationToken{snapshot.ObservationToken()})
	acceptInitial(t, store, server.URL+"/seen", FetchOptions{})
	seen := store.AcceptanceSequence()
	acceptInitial(t, store, server.URL+"/later", FetchOptions{})

	revoked := store.RevokeAcceptances(seen)

	require.Len(t, revoked, 1)
	assert.Equal(t, server.URL+"/seen", revoked[0].URL)
	for _, path := range []string{"/confirmed", "/later"} {
		_, accepted := store.Get(server.URL + path)
		assert.True(t, accepted, path)
	}
	assert.Empty(t, store.RevokeAcceptances(seen), "a revocation is not repeated")
}

func TestConfirmationOnlyCoversConsumedAcceptedVersions(t *testing.T) {
	var body atomic.Value
	body.Store("first")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body.Load().(string)))
	}))
	t.Cleanup(server.Close)
	store := New(slog.New(slog.NewTextHandler(io.Discard, nil)), 0)
	state := acceptInitial(t, store, server.URL, FetchOptions{})
	first := store.AcceptedSnapshot(server.URL, state.Descriptor)
	acceptInitial(t, store, server.URL+"/unused", FetchOptions{})
	for _, value := range []string{"second", "first"} {
		body.Store(value)
		pending, err := store.RefreshURLVersion(t.Context(), server.URL)
		require.NoError(t, err)
		require.NotNil(t, pending)
		require.True(t, store.PromotePendingVersion(server.URL, pending.Checksum, pending.Revision))
	}

	store.ConfirmAcceptances([]ObservationToken{first.ObservationToken()})
	revoked := store.RevokeAcceptances(store.AcceptanceSequence())

	require.Len(t, revoked, 2, "an old read cannot confirm reaccepted bytes or an unused source")
	_, accepted := store.Get(server.URL)
	require.False(t, accepted)
}

func TestRevocationDoesNotRestoreContentFromAnotherSourceDeclaration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("same bytes"))
	}))
	t.Cleanup(server.Close)
	store := New(slog.New(slog.NewTextHandler(io.Discard, nil)), 0)
	first := acceptInitial(t, store, server.URL, FetchOptions{})
	oldRead := store.AcceptedSnapshot(server.URL, first.Descriptor)
	store.ConfirmAcceptances([]ObservationToken{oldRead.ObservationToken()})
	second := acceptInitial(t, store, server.URL, FetchOptions{Critical: true})
	store.ConfirmAcceptances([]ObservationToken{oldRead.ObservationToken()})

	revoked := store.RevokeAcceptances(store.AcceptanceSequence())

	require.Len(t, revoked, 1)
	assert.True(t, revoked[0].Critical)
	assert.False(t, revoked[0].Restored)
	_, accepted := store.GetSource(server.URL, second.Descriptor)
	assert.False(t, accepted)
}
