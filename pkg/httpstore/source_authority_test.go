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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFetchRefetchesWhenBearerTokenRotates(t *testing.T) {
	requests := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.Header.Get("Authorization") {
		case "Bearer old-token":
			_, _ = w.Write([]byte("old-content"))
		case "Bearer new-token":
			_, _ = w.Write([]byte("new-content"))
		default:
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer server.Close()

	store := New(slog.New(slog.NewTextHandler(io.Discard, nil)), 0)
	oldAuth := &AuthConfig{Type: AuthTypeBearer, Token: "old-token"}
	newAuth := &AuthConfig{Type: AuthTypeBearer, Token: "new-token"}

	oldContent, err := store.Fetch(t.Context(), server.URL, FetchOptions{Critical: true}, oldAuth)
	require.NoError(t, err)
	assert.Equal(t, "old-content", oldContent)

	newContent, err := store.Fetch(t.Context(), server.URL, FetchOptions{Critical: true}, newAuth)
	require.NoError(t, err)
	assert.Equal(t, "new-content", newContent)
	assert.Equal(t, int32(2), requests.Load())

	descriptor, err := DescribeSource(FetchOptions{Critical: true}, newAuth)
	require.NoError(t, err)
	cached, ok := store.GetSource(server.URL, descriptor)
	require.True(t, ok)
	assert.Equal(t, "new-content", cached)
}

func TestRefreshCannotCommitAfterSourceAuthorityChanges(t *testing.T) {
	refreshStarted := make(chan struct{})
	releaseRefresh := make(chan struct{})
	oldRequests := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "Bearer old-token":
			if oldRequests.Add(1) == 1 {
				_, _ = w.Write([]byte("old-content"))
				return
			}
			close(refreshStarted)
			<-releaseRefresh
			_, _ = w.Write([]byte("stale-refresh"))
		case "Bearer new-token":
			_, _ = w.Write([]byte("new-content"))
		default:
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer server.Close()

	store := New(slog.New(slog.NewTextHandler(io.Discard, nil)), 0)
	oldAuth := &AuthConfig{Type: AuthTypeBearer, Token: "old-token"}
	newAuth := &AuthConfig{Type: AuthTypeBearer, Token: "new-token"}
	_, err := store.Fetch(t.Context(), server.URL, FetchOptions{Critical: true}, oldAuth)
	require.NoError(t, err)

	type refreshResult struct {
		version *PendingVersion
		err     error
	}
	result := make(chan refreshResult, 1)
	go func() {
		version, refreshErr := store.RefreshURLVersion(t.Context(), server.URL)
		result <- refreshResult{version: version, err: refreshErr}
	}()
	<-refreshStarted

	content, err := store.Fetch(t.Context(), server.URL, FetchOptions{Critical: true}, newAuth)
	require.NoError(t, err)
	assert.Equal(t, "new-content", content)
	close(releaseRefresh)

	refresh := <-result
	require.NoError(t, refresh.err)
	assert.Nil(t, refresh.version)
	assert.Empty(t, store.GetPendingURLs())
	accepted, ok := store.Get(server.URL)
	require.True(t, ok)
	assert.Equal(t, "new-content", accepted)
}

func TestSourceIdentityRejectsConflictingHeaderSpellings(t *testing.T) {
	_, err := SourceIdentity(FetchOptions{}, &AuthConfig{
		Type: AuthTypeHeader,
		Headers: map[string]string{
			"X-API-Key": "first",
			"x-api-key": "second",
		},
	})

	require.ErrorContains(t, err, "conflicting values")
}

func TestRefreshUsesOwnedAuthenticationSnapshot(t *testing.T) {
	headers := make(chan string, 2)
	requests := atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers <- r.Header.Get("X-API-Key")
		if requests.Add(1) == 1 {
			_, _ = w.Write([]byte("initial"))
			return
		}
		_, _ = w.Write([]byte("refreshed"))
	}))
	defer server.Close()

	store := New(slog.New(slog.NewTextHandler(io.Discard, nil)), 0)
	auth := &AuthConfig{
		Type:    AuthTypeHeader,
		Headers: map[string]string{"X-API-Key": "owned"},
	}
	_, err := store.Fetch(t.Context(), server.URL, FetchOptions{Critical: true}, auth)
	require.NoError(t, err)
	auth.Headers["X-API-Key"] = "caller-mutated"

	changed, err := store.RefreshURL(t.Context(), server.URL)
	require.NoError(t, err)
	require.True(t, changed)
	assert.Equal(t, "owned", <-headers)
	assert.Equal(t, "owned", <-headers)
}
