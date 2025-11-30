// Copyright 2025 Philipp Hossner
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
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPStore_FetchAndGet(t *testing.T) {
	// Create test server
	content := "test content"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(content))
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	store := New(logger)

	ctx := context.Background()

	// First fetch - should return content
	result, err := store.Fetch(ctx, server.URL, FetchOptions{}, nil)
	require.NoError(t, err)
	assert.Equal(t, content, result)

	// Get should return accepted content
	cached, ok := store.Get(server.URL)
	require.True(t, ok)
	assert.Equal(t, content, cached)
}

func TestHTTPStore_FetchWithRetries(t *testing.T) {
	// Server that fails twice then succeeds
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	store := New(logger)

	ctx := context.Background()

	// Fetch with retries
	result, err := store.Fetch(ctx, server.URL, FetchOptions{
		Retries:    3,
		RetryDelay: 10 * time.Millisecond,
	}, nil)

	require.NoError(t, err)
	assert.Equal(t, "success", result)
	assert.Equal(t, 3, attempts)
}

func TestHTTPStore_FetchTimeout(t *testing.T) {
	// Server that delays response longer than our timeout
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("too late"))
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	store := New(logger)

	ctx := context.Background()

	// Fetch with short timeout and Critical=true should return error
	_, err := store.Fetch(ctx, server.URL, FetchOptions{
		Timeout:  50 * time.Millisecond,
		Critical: true,
	}, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "context deadline exceeded")
}

func TestHTTPStore_PendingContent(t *testing.T) {
	// Server that returns different content on each call
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusOK)
		if callCount == 1 {
			w.Write([]byte("initial"))
		} else {
			w.Write([]byte("updated"))
		}
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	store := New(logger)

	ctx := context.Background()

	// Initial fetch - content goes to accepted
	result, err := store.Fetch(ctx, server.URL, FetchOptions{Delay: time.Minute}, nil)
	require.NoError(t, err)
	assert.Equal(t, "initial", result)

	// Refresh - content goes to pending
	changed, err := store.RefreshURL(ctx, server.URL)
	require.NoError(t, err)
	assert.True(t, changed)

	// Get returns accepted content
	accepted, ok := store.Get(server.URL)
	require.True(t, ok)
	assert.Equal(t, "initial", accepted)

	// GetForValidation returns pending content
	pending, ok := store.GetForValidation(server.URL)
	require.True(t, ok)
	assert.Equal(t, "updated", pending)
}

func TestHTTPStore_PromotePending(t *testing.T) {
	// Server returning different content
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusOK)
		if callCount == 1 {
			w.Write([]byte("v1"))
		} else {
			w.Write([]byte("v2"))
		}
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	store := New(logger)

	ctx := context.Background()

	// Initial fetch
	_, err := store.Fetch(ctx, server.URL, FetchOptions{Delay: time.Minute}, nil)
	require.NoError(t, err)

	// Refresh creates pending
	_, err = store.RefreshURL(ctx, server.URL)
	require.NoError(t, err)

	// Promote pending to accepted
	promoted := store.PromotePending(server.URL)
	assert.True(t, promoted)

	// Now Get returns the new content
	content, ok := store.Get(server.URL)
	require.True(t, ok)
	assert.Equal(t, "v2", content)
}

func TestHTTPStore_RejectPending(t *testing.T) {
	// Server returning different content
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusOK)
		if callCount == 1 {
			w.Write([]byte("good"))
		} else {
			w.Write([]byte("bad"))
		}
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	store := New(logger)

	ctx := context.Background()

	// Initial fetch
	_, err := store.Fetch(ctx, server.URL, FetchOptions{Delay: time.Minute}, nil)
	require.NoError(t, err)

	// Refresh creates pending
	_, err = store.RefreshURL(ctx, server.URL)
	require.NoError(t, err)

	// Reject pending
	rejected := store.RejectPending(server.URL)
	assert.True(t, rejected)

	// Get still returns original content
	content, ok := store.Get(server.URL)
	require.True(t, ok)
	assert.Equal(t, "good", content)

	// No more pending content
	_, ok = store.GetForValidation(server.URL)
	assert.True(t, ok)
	// After rejection, GetForValidation falls back to accepted
}

func TestHTTPStore_GetPendingURLs(t *testing.T) {
	// Servers that return different content on second request
	callCount1 := 0
	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount1++
		if callCount1 == 1 {
			w.Write([]byte("content1-v1"))
		} else {
			w.Write([]byte("content1-v2"))
		}
	}))
	defer server1.Close()

	callCount2 := 0
	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount2++
		if callCount2 == 1 {
			w.Write([]byte("content2-v1"))
		} else {
			w.Write([]byte("content2-v2"))
		}
	}))
	defer server2.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	store := New(logger)

	ctx := context.Background()

	// Fetch both URLs
	_, _ = store.Fetch(ctx, server1.URL, FetchOptions{Delay: time.Minute}, nil)
	_, _ = store.Fetch(ctx, server2.URL, FetchOptions{Delay: time.Minute}, nil)

	// Initially no pending
	assert.Empty(t, store.GetPendingURLs())

	// After refresh, both have pending (content changed)
	changed1, _ := store.RefreshURL(ctx, server1.URL)
	changed2, _ := store.RefreshURL(ctx, server2.URL)

	assert.True(t, changed1, "server1 content should have changed")
	assert.True(t, changed2, "server2 content should have changed")

	pendingURLs := store.GetPendingURLs()
	assert.Len(t, pendingURLs, 2)
	assert.Contains(t, pendingURLs, server1.URL)
	assert.Contains(t, pendingURLs, server2.URL)
}

func TestHTTPStore_BasicAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || username != "user" || password != "pass" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte("authenticated"))
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	store := New(logger)

	ctx := context.Background()

	// Without auth and with Critical=true - should return error
	_, err := store.Fetch(ctx, server.URL, FetchOptions{Critical: true}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401 Unauthorized")

	// With auth - should succeed
	// Use a different URL to avoid cached empty result from failed fetch
	result, err := store.Fetch(ctx, server.URL+"/auth", FetchOptions{}, &AuthConfig{
		Type:     "basic",
		Username: "user",
		Password: "pass",
	})
	require.NoError(t, err)
	assert.Equal(t, "authenticated", result)
}

func TestHTTPStore_BearerAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer mytoken" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte("authenticated"))
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	store := New(logger)

	ctx := context.Background()

	// With bearer token
	result, err := store.Fetch(ctx, server.URL, FetchOptions{}, &AuthConfig{
		Type:  "bearer",
		Token: "mytoken",
	})
	require.NoError(t, err)
	assert.Equal(t, "authenticated", result)
}

func TestHTTPStore_CustomHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey := r.Header.Get("X-API-Key")
		if apiKey != "secret123" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte("authenticated"))
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	store := New(logger)

	ctx := context.Background()

	// With custom headers
	result, err := store.Fetch(ctx, server.URL, FetchOptions{}, &AuthConfig{
		Type: "header",
		Headers: map[string]string{
			"X-API-Key": "secret123",
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "authenticated", result)
}

func TestHTTPStore_ConditionalRequest(t *testing.T) {
	etag := `"abc123"`
	requestCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		ifNoneMatch := r.Header.Get("If-None-Match")
		if ifNoneMatch == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		w.Write([]byte("content"))
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	store := New(logger)

	ctx := context.Background()

	// Initial fetch
	result, err := store.Fetch(ctx, server.URL, FetchOptions{Delay: time.Minute}, nil)
	require.NoError(t, err)
	assert.Equal(t, "content", result)
	assert.Equal(t, 1, requestCount)

	// Refresh - should get 304 Not Modified
	changed, err := store.RefreshURL(ctx, server.URL)
	require.NoError(t, err)
	assert.False(t, changed) // Content unchanged
	assert.Equal(t, 2, requestCount)
}

func TestHTTPStore_GetDelay(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("content"))
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	store := New(logger)

	ctx := context.Background()

	// Unknown URL returns 0
	assert.Equal(t, time.Duration(0), store.GetDelay("http://unknown"))

	// Fetch with delay
	expectedDelay := 5 * time.Minute
	_, err := store.Fetch(ctx, server.URL, FetchOptions{Delay: expectedDelay}, nil)
	require.NoError(t, err)

	// Get delay returns configured value
	assert.Equal(t, expectedDelay, store.GetDelay(server.URL))
}
