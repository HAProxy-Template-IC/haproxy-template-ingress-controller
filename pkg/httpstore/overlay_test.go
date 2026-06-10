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
	"log/slog"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newPendingStore returns an HTTPStore preloaded with the given URLs as
// pending. Each URL is created with accepted content "old-<url>" and pending
// content "new-<url>".
func newPendingStore(t *testing.T, urls ...string) *HTTPStore {
	t.Helper()
	store := New(slog.Default(), 0)
	now := time.Now()
	for _, u := range urls {
		store.cache[u] = &CacheEntry{
			URL:              u,
			AcceptedContent:  "old-" + u,
			AcceptedChecksum: checksum("old-" + u),
			AcceptedTime:     now,
			LastAccessTime:   now,
			PendingContent:   "new-" + u,
			PendingChecksum:  checksum("new-" + u),
			HasPending:       true,
			ValidationState:  StateValidating,
		}
	}
	return store
}

func TestNewHTTPOverlay_SnapshotsPendingURLs(t *testing.T) {
	store := newPendingStore(t, "http://a", "http://b")

	overlay := NewHTTPOverlay(store)

	got := overlay.PendingURLs()
	sort.Strings(got)
	assert.Equal(t, []string{"http://a", "http://b"}, got)
}

func TestHTTPOverlay_IsEmpty(t *testing.T) {
	t.Run("no pending content", func(t *testing.T) {
		store := New(slog.Default(), 0)
		store.LoadFixture("http://no-pending", "content")
		overlay := NewHTTPOverlay(store)
		assert.True(t, overlay.IsEmpty())
	})

	t.Run("with pending content", func(t *testing.T) {
		store := newPendingStore(t, "http://a")
		overlay := NewHTTPOverlay(store)
		assert.False(t, overlay.IsEmpty())
	})
}

func TestHTTPOverlay_GetContent(t *testing.T) {
	store := newPendingStore(t, "http://pending")
	store.LoadFixture("http://accepted-only", "accepted-content")

	overlay := NewHTTPOverlay(store)

	tests := []struct {
		name   string
		url    string
		want   string
		wantOK bool
	}{
		{name: "pending URL returns pending content", url: "http://pending", want: "new-http://pending", wantOK: true},
		{name: "accepted-only URL returns accepted content", url: "http://accepted-only", want: "accepted-content", wantOK: true},
		{name: "missing URL returns empty", url: "http://missing", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := overlay.GetContent(tt.url)
			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestHTTPOverlay_PendingURLs_ReturnsCopy(t *testing.T) {
	store := newPendingStore(t, "http://a", "http://b")
	overlay := NewHTTPOverlay(store)

	first := overlay.PendingURLs()
	require.Len(t, first, 2)

	// Mutating the returned slice must not affect the overlay's snapshot.
	first[0] = "mutated"
	second := overlay.PendingURLs()
	assert.NotContains(t, second, "mutated")
}

func TestHTTPOverlay_HasPendingURL(t *testing.T) {
	store := newPendingStore(t, "http://pending-1", "http://pending-2")
	store.LoadFixture("http://accepted-only", "x")

	overlay := NewHTTPOverlay(store)

	assert.True(t, overlay.HasPendingURL("http://pending-1"))
	assert.True(t, overlay.HasPendingURL("http://pending-2"))
	assert.False(t, overlay.HasPendingURL("http://accepted-only"))
	assert.False(t, overlay.HasPendingURL("http://missing"))
}

// TestHTTPOverlay_SnapshotIsFrozen verifies that PendingURLs reflects the state
// at NewHTTPOverlay time, not changes afterwards. This is the documented
// behaviour ("snapshot — changes to HTTPStore after creation are not
// reflected").
func TestHTTPOverlay_SnapshotIsFrozen(t *testing.T) {
	store := newPendingStore(t, "http://a")
	overlay := NewHTTPOverlay(store)

	// Add a new pending URL after the overlay was built.
	now := time.Now()
	store.cache["http://b"] = &CacheEntry{
		URL:             "http://b",
		PendingContent:  "x",
		PendingChecksum: checksum("x"),
		HasPending:      true,
		LastAccessTime:  now,
	}

	// Snapshot must still report only the original URLs.
	got := overlay.PendingURLs()
	assert.Equal(t, []string{"http://a"}, got)
	assert.False(t, overlay.HasPendingURL("http://b"))
}
