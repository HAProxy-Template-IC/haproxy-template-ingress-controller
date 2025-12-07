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
	store := New(logger, 0)

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
	store := New(logger, 0)

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
	store := New(logger, 0)

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
	store := New(logger, 0)

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
	store := New(logger, 0)

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
	store := New(logger, 0)

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
	store := New(logger, 0)

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
	store := New(logger, 0)

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
	store := New(logger, 0)

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
	store := New(logger, 0)

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
	store := New(logger, 0)

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
	store := New(logger, 0)

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

func TestHTTPStore_EvictUnused(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Create store with 100ms maxAge for fast testing
	store := New(logger, 100*time.Millisecond)

	// Load two fixtures
	store.LoadFixture("http://example.com/old", "old content")
	store.LoadFixture("http://example.com/new", "new content")

	assert.Equal(t, 2, store.Size())

	// Access one of them to update its LastAccessTime
	time.Sleep(50 * time.Millisecond)
	_, ok := store.Get("http://example.com/new")
	require.True(t, ok)

	// Wait for old entry to expire
	time.Sleep(60 * time.Millisecond)

	// Evict - should remove old, keep new
	evictedURLs := store.EvictUnused()

	assert.Equal(t, 1, len(evictedURLs))
	assert.Equal(t, "http://example.com/old", evictedURLs[0])
	assert.Equal(t, 1, store.Size())

	// Verify correct entry was evicted
	_, ok = store.Get("http://example.com/old")
	assert.False(t, ok)

	_, ok = store.Get("http://example.com/new")
	assert.True(t, ok)
}

func TestHTTPStore_EvictUnused_NeverEvictsPending(t *testing.T) {
	// Create test server that returns changing content
	content := "original"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(content))
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Create store with very short maxAge
	store := New(logger, 1*time.Millisecond)

	ctx := context.Background()

	// Initial fetch
	_, err := store.Fetch(ctx, server.URL, FetchOptions{Delay: time.Minute}, nil)
	require.NoError(t, err)

	// Update content and refresh to create pending
	content = "updated"
	changed, err := store.RefreshURL(ctx, server.URL)
	require.NoError(t, err)
	require.True(t, changed)

	// Entry now has pending content
	assert.True(t, store.HasPendingValidation())

	// Wait for maxAge to expire
	time.Sleep(10 * time.Millisecond)

	// Evict - should not evict entry with pending content
	evictedURLs := store.EvictUnused()
	assert.Empty(t, evictedURLs)
	assert.Equal(t, 1, store.Size())
}

func TestHTTPStore_EvictUnused_DisabledWithZeroMaxAge(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Create store with 0 maxAge (eviction disabled)
	store := New(logger, 0)

	store.LoadFixture("http://example.com/test", "content")

	// Evict returns nil when disabled
	evictedURLs := store.EvictUnused()
	assert.Nil(t, evictedURLs)
	assert.Equal(t, 1, store.Size())
}

func TestHTTPStore_AccessResetsEvictionTime(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Create store with 80ms maxAge
	store := New(logger, 80*time.Millisecond)

	store.LoadFixture("http://example.com/test", "content")

	// Wait 50ms, then access
	time.Sleep(50 * time.Millisecond)
	_, ok := store.Get("http://example.com/test")
	require.True(t, ok)

	// Wait another 50ms (total 100ms since load, but only 50ms since access)
	time.Sleep(50 * time.Millisecond)

	// Should not be evicted because access reset the timer
	evictedURLs := store.EvictUnused()
	assert.Empty(t, evictedURLs)
	assert.Equal(t, 1, store.Size())

	// Wait for the full maxAge from last access
	time.Sleep(40 * time.Millisecond)

	// Now it should be evicted
	evictedURLs = store.EvictUnused()
	assert.Equal(t, 1, len(evictedURLs))
	assert.Equal(t, "http://example.com/test", evictedURLs[0])
	assert.Equal(t, 0, store.Size())
}

func TestHTTPStore_GetPending(t *testing.T) {
	// Create test server
	content := "original"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(content))
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	store := New(logger, 0)

	ctx := context.Background()

	// Initial fetch
	_, err := store.Fetch(ctx, server.URL, FetchOptions{Delay: time.Minute}, nil)
	require.NoError(t, err)

	// No pending content yet
	pending, ok := store.GetPending(server.URL)
	assert.False(t, ok)
	assert.Empty(t, pending)

	// Unknown URL returns false
	pending, ok = store.GetPending("http://unknown")
	assert.False(t, ok)
	assert.Empty(t, pending)

	// Update content and refresh to create pending
	content = "updated"
	changed, err := store.RefreshURL(ctx, server.URL)
	require.NoError(t, err)
	require.True(t, changed)

	// Now GetPending should return the pending content
	pending, ok = store.GetPending(server.URL)
	assert.True(t, ok)
	assert.Equal(t, "updated", pending)
}

func TestHTTPStore_GetURLsWithDelay(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("content"))
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	store := New(logger, 0)

	ctx := context.Background()

	// Initially empty
	urls := store.GetURLsWithDelay()
	assert.Empty(t, urls)

	// Fetch with delay
	_, err := store.Fetch(ctx, server.URL, FetchOptions{Delay: 5 * time.Minute}, nil)
	require.NoError(t, err)

	// Fetch without delay using fixture
	store.LoadFixture("http://example.com/no-delay", "content")

	// Only URL with delay should be returned
	urls = store.GetURLsWithDelay()
	assert.Equal(t, 1, len(urls))
	assert.Equal(t, server.URL, urls[0])
}

func TestHTTPStore_GetEntry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"test-etag"`)
		w.Write([]byte("content"))
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	store := New(logger, 0)

	ctx := context.Background()

	// Unknown URL returns nil
	entry := store.GetEntry("http://unknown")
	assert.Nil(t, entry)

	// Fetch with auth headers
	auth := &AuthConfig{
		Type: "header",
		Headers: map[string]string{
			"X-API-Key": "secret",
		},
	}
	_, err := store.Fetch(ctx, server.URL, FetchOptions{Delay: time.Minute}, auth)
	require.NoError(t, err)

	// GetEntry should return a copy with all fields
	entry = store.GetEntry(server.URL)
	require.NotNil(t, entry)
	assert.Equal(t, "content", entry.AcceptedContent)
	assert.Equal(t, time.Minute, entry.Options.Delay)
	assert.NotNil(t, entry.Auth)
	assert.Equal(t, "secret", entry.Auth.Headers["X-API-Key"])

	// Verify it's a copy - modifying returned entry shouldn't affect store
	entry.Auth.Headers["X-API-Key"] = "modified"
	entry2 := store.GetEntry(server.URL)
	assert.Equal(t, "secret", entry2.Auth.Headers["X-API-Key"])
}

func TestHTTPStore_Clear(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	store := New(logger, 0)

	// Load fixtures
	store.LoadFixture("http://example.com/1", "content1")
	store.LoadFixture("http://example.com/2", "content2")
	store.LoadFixture("http://example.com/3", "content3")

	assert.Equal(t, 3, store.Size())

	// Clear store
	store.Clear()

	assert.Equal(t, 0, store.Size())

	// Verify entries are gone
	_, ok := store.Get("http://example.com/1")
	assert.False(t, ok)
}

func TestHTTPStore_PromoteAllPending(t *testing.T) {
	// Create test server
	content := "original"
	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(content))
	}))
	defer server1.Close()

	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(content))
	}))
	defer server2.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	store := New(logger, 0)

	ctx := context.Background()

	// Initial fetch for both
	_, err := store.Fetch(ctx, server1.URL, FetchOptions{Delay: time.Minute}, nil)
	require.NoError(t, err)
	_, err = store.Fetch(ctx, server2.URL, FetchOptions{Delay: time.Minute}, nil)
	require.NoError(t, err)

	// Update content and refresh both
	content = "updated"
	_, err = store.RefreshURL(ctx, server1.URL)
	require.NoError(t, err)
	_, err = store.RefreshURL(ctx, server2.URL)
	require.NoError(t, err)

	// Both should have pending
	urls := store.GetPendingURLs()
	assert.Equal(t, 2, len(urls))

	// Promote all
	promoted := store.PromoteAllPending()
	assert.Equal(t, 2, promoted)

	// No more pending
	assert.False(t, store.HasPendingValidation())

	// Both should have updated content as accepted
	content1, ok := store.Get(server1.URL)
	assert.True(t, ok)
	assert.Equal(t, "updated", content1)

	content2, ok := store.Get(server2.URL)
	assert.True(t, ok)
	assert.Equal(t, "updated", content2)
}

func TestHTTPStore_RejectAllPending(t *testing.T) {
	// Create test server
	content := "original"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(content))
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	store := New(logger, 0)

	ctx := context.Background()

	// Initial fetch
	_, err := store.Fetch(ctx, server.URL, FetchOptions{Delay: time.Minute}, nil)
	require.NoError(t, err)

	// Update content and refresh
	content = "bad content"
	_, err = store.RefreshURL(ctx, server.URL)
	require.NoError(t, err)

	// Should have pending
	assert.True(t, store.HasPendingValidation())

	// Reject all
	rejected := store.RejectAllPending()
	assert.Equal(t, 1, rejected)

	// No more pending
	assert.False(t, store.HasPendingValidation())

	// Accepted content should still be original
	accepted, ok := store.Get(server.URL)
	assert.True(t, ok)
	assert.Equal(t, "original", accepted)
}

func TestValidationState_String(t *testing.T) {
	tests := []struct {
		state    ValidationState
		expected string
	}{
		{StateAccepted, "accepted"},
		{StateValidating, "validating"},
		{StateRejected, "rejected"},
		{ValidationState(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.state.String())
		})
	}
}

func TestHTTPStore_NewWithNilLogger(t *testing.T) {
	// Should not panic and should use default logger
	store := New(nil, 0)
	require.NotNil(t, store)
}

func TestHTTPStore_FetchHTTPErrors(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantErr    string
	}{
		{"401 unauthorized", http.StatusUnauthorized, "authentication failed (401 Unauthorized)"},
		{"403 forbidden", http.StatusForbidden, "access denied (403 Forbidden)"},
		{"404 not found", http.StatusNotFound, "resource not found (404 Not Found)"},
		{"400 bad request", http.StatusBadRequest, "client error: 400 Bad Request"},
		{"500 internal error", http.StatusInternalServerError, "server error: 500 Internal Server Error"},
		{"201 created", http.StatusCreated, "unexpected status: 201 Created"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
			}))
			defer server.Close()

			logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
			store := New(logger, 0)

			ctx := context.Background()
			_, err := store.Fetch(ctx, server.URL, FetchOptions{Critical: true}, nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestHTTPStore_FetchContentTooLarge(t *testing.T) {
	// Create content larger than MaxContentSize
	largeContent := make([]byte, MaxContentSize+100)
	for i := range largeContent {
		largeContent[i] = 'x'
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(largeContent)
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	store := New(logger, 0)

	ctx := context.Background()
	_, err := store.Fetch(ctx, server.URL, FetchOptions{Critical: true}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum size")
}

func TestHTTPStore_FetchWithHeaderAuth(t *testing.T) {
	var receivedHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header.Clone()
		w.Write([]byte("content"))
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	store := New(logger, 0)

	ctx := context.Background()
	auth := &AuthConfig{
		Type: "header",
		Headers: map[string]string{
			"X-API-Key":    "my-api-key",
			"X-Custom-Key": "custom-value",
		},
	}
	content, err := store.Fetch(ctx, server.URL, FetchOptions{}, auth)
	require.NoError(t, err)
	assert.Equal(t, "content", content)
	assert.Equal(t, "my-api-key", receivedHeaders.Get("X-API-Key"))
	assert.Equal(t, "custom-value", receivedHeaders.Get("X-Custom-Key"))
}

func TestHTTPStore_FetchWithUnknownAuthType(t *testing.T) {
	var receivedHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header.Clone()
		w.Write([]byte("content"))
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	store := New(logger, 0)

	ctx := context.Background()
	// Unknown auth type falls through to default which uses Headers
	auth := &AuthConfig{
		Type: "unknown",
		Headers: map[string]string{
			"X-Custom-Auth": "some-token",
		},
	}
	content, err := store.Fetch(ctx, server.URL, FetchOptions{}, auth)
	require.NoError(t, err)
	assert.Equal(t, "content", content)
	assert.Equal(t, "some-token", receivedHeaders.Get("X-Custom-Auth"))
}

func TestHTTPStore_RefreshURLNotInCache(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	store := New(logger, 0)

	ctx := context.Background()
	_, err := store.RefreshURL(ctx, "http://not-in-cache.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "URL not in cache")
}

func TestHTTPStore_RefreshURLSkipsValidating(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("content"))
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	store := New(logger, 0)

	ctx := context.Background()
	// Initial fetch
	_, err := store.Fetch(ctx, server.URL, FetchOptions{Delay: time.Minute}, nil)
	require.NoError(t, err)

	// Manually set state to validating (simulating pending validation)
	store.mu.Lock()
	entry := store.cache[server.URL]
	entry.ValidationState = StateValidating
	store.mu.Unlock()

	// Refresh should be skipped
	changed, err := store.RefreshURL(ctx, server.URL)
	require.NoError(t, err)
	assert.False(t, changed)
}

func TestHTTPStore_RefreshURLError(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			w.Write([]byte("initial"))
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	store := New(logger, 0)

	ctx := context.Background()
	// Initial fetch succeeds
	_, err := store.Fetch(ctx, server.URL, FetchOptions{Delay: time.Minute, Retries: 0}, nil)
	require.NoError(t, err)

	// Refresh fails
	_, err = store.RefreshURL(ctx, server.URL)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "server error")
}

func TestHTTPStore_FetchNonCriticalError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	store := New(logger, 0)

	ctx := context.Background()
	// Non-critical fetch returns empty string without error
	content, err := store.Fetch(ctx, server.URL, FetchOptions{Critical: false, Retries: 0}, nil)
	require.NoError(t, err)
	assert.Equal(t, "", content)
}

func TestHTTPStore_GetForValidationEmpty(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	store := New(logger, 0)

	// URL not in cache
	content, ok := store.GetForValidation("http://not-cached.example.com")
	assert.False(t, ok)
	assert.Equal(t, "", content)
}
