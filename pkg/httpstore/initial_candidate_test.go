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
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitialCandidatesCommitAtomically(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.URL.Path))
	}))
	defer server.Close()

	store := New(slog.New(slog.NewTextHandler(io.Discard, nil)), 0)
	aURL := server.URL + "/a"
	bURL := server.URL + "/b"
	aSource, err := store.ReconcileSource(aURL, FetchOptions{Critical: true}, nil)
	require.NoError(t, err)
	bSource, err := store.ReconcileSource(bURL, FetchOptions{Critical: true}, nil)
	require.NoError(t, err)

	aContent, aCandidate, err := store.PrepareInitial(t.Context(), aURL, aSource.State)
	require.NoError(t, err)
	bContent, bCandidate, err := store.PrepareInitial(t.Context(), bURL, bSource.State)
	require.NoError(t, err)
	assert.Equal(t, "/a", aContent)
	assert.Equal(t, "/b", bContent)
	_, aAccepted := store.GetSource(aURL, aSource.State.Identity)
	_, bAccepted := store.GetSource(bURL, bSource.State.Identity)
	assert.False(t, aAccepted)
	assert.False(t, bAccepted)

	_, err = store.ReconcileSource(bURL, FetchOptions{Critical: true}, &AuthConfig{
		Type:  AuthTypeBearer,
		Token: "replacement",
	})
	require.NoError(t, err)
	require.Error(t, store.CommitInitialCandidates(t.Context(), []*InitialCandidate{aCandidate, bCandidate}))
	_, aAccepted = store.GetSource(aURL, aSource.State.Identity)
	_, bAccepted = store.GetSource(bURL, bSource.State.Identity)
	assert.False(t, aAccepted)
	assert.False(t, bAccepted)
}

func TestInitialCandidateBecomesVisibleOnlyAfterCommit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()

	store := New(slog.New(slog.NewTextHandler(io.Discard, nil)), 0)
	reconciled, err := store.ReconcileSource(server.URL, FetchOptions{Critical: true}, nil)
	require.NoError(t, err)
	content, candidate, err := store.PrepareInitial(t.Context(), server.URL, reconciled.State)
	require.NoError(t, err)
	assert.Empty(t, content)
	require.NotNil(t, candidate)
	_, accepted := store.GetSource(server.URL, reconciled.State.Identity)
	assert.False(t, accepted)
	assert.Empty(t, store.GetPendingURLs())

	require.NoError(t, store.CommitInitialCandidates(t.Context(), []*InitialCandidate{candidate}))
	acceptedContent, accepted := store.GetSource(server.URL, reconciled.State.Identity)
	require.True(t, accepted)
	assert.Empty(t, acceptedContent)
}

func TestInitialCandidateDoesNotCommitAfterCancellationWhileWaitingForStore(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("candidate"))
	}))
	defer server.Close()

	store := New(slog.New(slog.NewTextHandler(io.Discard, nil)), 0)
	reconciled, err := store.ReconcileSource(server.URL, FetchOptions{Critical: true}, nil)
	require.NoError(t, err)
	_, candidate, err := store.PrepareInitial(t.Context(), server.URL, reconciled.State)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	started := make(chan struct{})
	result := make(chan error, 1)
	store.mu.Lock()
	go func() {
		close(started)
		result <- store.CommitInitialCandidates(ctx, []*InitialCandidate{candidate})
	}()
	<-started
	cancel()
	store.mu.Unlock()

	require.ErrorIs(t, <-result, context.Canceled)
	_, accepted := store.GetSource(server.URL, reconciled.State.Identity)
	assert.False(t, accepted)
}
