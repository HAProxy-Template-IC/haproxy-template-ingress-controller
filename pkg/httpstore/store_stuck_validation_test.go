// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

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

// An entry leaves StateValidating only via PromotePending/RejectPending, and both
// are driven by a ProposalValidationCompletedEvent. The bus drops that event when
// the subscriber buffer is full, and a panic in the proposal validator publishes
// no verdict at all — so without a deadline a single lost verdict freezes the URL
// at its accepted content for the rest of the process lifetime, invisible except
// at trace level. Eviction cannot clear it either: entries with pending content
// are never evicted.

func newStuckValidationStore(t *testing.T, content string) (store *HTTPStore, url string, closeServer func()) {
	t.Helper()

	served := content
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(served))
	}))

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	store = New(logger, 0)

	_, err := store.Fetch(context.Background(), server.URL, FetchOptions{}, nil)
	require.NoError(t, err, "initial fetch must succeed")

	return store, server.URL, server.Close
}

// rewindValidationStart moves an entry's validation start into the past so the
// deadline is exceeded without the test waiting for wall-clock time.
func rewindValidationStart(s *HTTPStore, url string, by time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache[url].ValidationStartedAt = time.Now().Add(-by)
}

func TestRefreshURL_SkipsWhileValidationIsInFlight(t *testing.T) {
	store, url, closeServer := newStuckValidationStore(t, "v1")
	defer closeServer()

	ctx := context.Background()

	// Serve new content so the refresh stores a pending version.
	store.mu.Lock()
	store.cache[url].AcceptedChecksum = "different-so-content-counts-as-changed"
	store.mu.Unlock()

	changed, err := store.RefreshURL(ctx, url)
	require.NoError(t, err)
	require.True(t, changed, "content differs from accepted, so it must be stored as pending")

	store.mu.RLock()
	state := store.cache[url].ValidationState
	store.mu.RUnlock()
	require.Equal(t, StateValidating, state)

	// A verdict is legitimately still in flight: the refresh must not disturb it.
	changed, err = store.RefreshURL(ctx, url)
	require.NoError(t, err)
	assert.False(t, changed,
		"a refresh during an in-flight validation must not replace the pending content — "+
			"the render that is being validated reads it")

	store.mu.RLock()
	defer store.mu.RUnlock()
	assert.True(t, store.cache[url].HasPending,
		"the pending content must survive a refresh inside the deadline")
}

func TestRefreshURL_AbandonsValidationStuckPastDeadline(t *testing.T) {
	store, url, closeServer := newStuckValidationStore(t, "v1")
	defer closeServer()

	ctx := context.Background()

	store.mu.Lock()
	store.cache[url].AcceptedChecksum = "different-so-content-counts-as-changed"
	store.mu.Unlock()

	_, err := store.RefreshURL(ctx, url)
	require.NoError(t, err)

	// The verdict never arrives (dropped event, or a panicking validator that
	// publishes nothing).
	rewindValidationStart(store, url, 2*store.validationStuckAfter)

	changed, err := store.RefreshURL(ctx, url)
	require.NoError(t, err,
		"a refresh past the deadline must proceed, not keep short-circuiting")

	store.mu.RLock()
	defer store.mu.RUnlock()
	entry := store.cache[url]

	assert.True(t, changed,
		"once the stuck pending content is abandoned the refresh must fetch again, "+
			"otherwise the URL is frozen at its accepted content for the process lifetime")
	assert.Equal(t, StateValidating, entry.ValidationState,
		"the fresh fetch starts a new validation cycle")
	assert.Equal(t, "v1", entry.PendingContent,
		"the pending content must be the newly fetched body, not the abandoned one")
	assert.WithinDuration(t, time.Now(), entry.ValidationStartedAt, time.Minute,
		"the new cycle must restart the deadline clock, otherwise the next refresh "+
			"would abandon it immediately")
}

func TestRefreshURL_StuckEntryStaysServingAcceptedContent(t *testing.T) {
	store, url, closeServer := newStuckValidationStore(t, "v1")
	defer closeServer()

	store.mu.Lock()
	store.cache[url].AcceptedContent = "accepted-blocklist"
	store.cache[url].AcceptedChecksum = "different-so-content-counts-as-changed"
	store.mu.Unlock()

	_, err := store.RefreshURL(context.Background(), url)
	require.NoError(t, err)
	rewindValidationStart(store, url, 2*store.validationStuckAfter)
	_, err = store.RefreshURL(context.Background(), url)
	require.NoError(t, err)

	content, ok := store.Get(url)
	require.True(t, ok)
	assert.Equal(t, "accepted-blocklist", content,
		"abandoning an unvalidated pending version must never promote it — the accepted "+
			"content stays in production until a verdict actually accepts a replacement")
}
