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
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

// Compile-time assertion: HTTPOverlay implements stores.HTTPContentOverlay.
var _ stores.HTTPContentOverlay = (*HTTPOverlay)(nil)

// HTTPOverlay represents pending HTTP content changes awaiting validation.
//
// Unlike K8s overlays which are constructed explicitly with additions/modifications/deletions,
// HTTP overlays are derived from the HTTPStore's pending content state. When content changes
// during refresh, it's stored as pending in the HTTPStore. The overlay provides access to
// this pending content during validation rendering.
//
// This type implements the stores.ContentOverlay interface, enabling unified handling
// of both K8s and HTTP overlays in the validation pipeline.
type HTTPOverlay struct {
	// pendingURLs contains URLs with pending content at overlay creation time.
	// This is a snapshot - changes to HTTPStore after creation are not reflected.
	pendingURLs []string
	pending     map[string]overlayPendingContent

	// store provides accepted fallback content for URLs outside this snapshot.
	store *HTTPStore
}

type overlayPendingContent struct {
	content  string
	checksum string
	revision uint64
}

// NewHTTPOverlay creates an overlay from the store's current pending state.
//
// The overlay captures a snapshot of which URLs have pending content at creation time.
// This ensures consistent behavior even if the store's state changes during validation.
//
// Parameters:
//   - store: The HTTPStore to derive pending state from
//
// Returns:
//   - An HTTPOverlay with the current pending URLs snapshot
func NewHTTPOverlay(store *HTTPStore) *HTTPOverlay {
	store.mu.RLock()
	pendingURLs := make([]string, 0)
	pending := make(map[string]overlayPendingContent)
	for url, entry := range store.cache {
		if !entry.HasPending {
			continue
		}
		pendingURLs = append(pendingURLs, url)
		pending[url] = overlayPendingContent{
			content:  entry.PendingContent,
			checksum: entry.PendingChecksum,
			revision: entry.PendingRevision,
		}
	}
	store.mu.RUnlock()

	return &HTTPOverlay{
		pendingURLs: pendingURLs,
		pending:     pending,
		store:       store,
	}
}

// IsEmpty returns true if the overlay contains no pending content.
// Implements the stores.ContentOverlay interface.
func (o *HTTPOverlay) IsEmpty() bool {
	return len(o.pendingURLs) == 0
}

// GetContent returns content for the given URL.
//
// If the URL has pending content, returns the pending content.
// Otherwise, returns the accepted content if available.
// This behavior matches what templates should see during validation rendering.
//
// Parameters:
//   - url: The URL to get content for
//
// Returns:
//   - content: The content string (pending preferred, otherwise accepted)
//   - ok: True if content was found
func (o *HTTPOverlay) GetContent(url string) (string, bool) {
	if pending, ok := o.pending[url]; ok {
		return pending.content, true
	}
	return o.store.Get(url)
}

// PendingURLs returns the list of URLs with pending content.
// This is the snapshot captured at overlay creation time.
func (o *HTTPOverlay) PendingURLs() []string {
	// Return a copy to prevent external modification
	result := make([]string, len(o.pendingURLs))
	copy(result, o.pendingURLs)
	return result
}

// HasPendingURL returns true if the given URL has pending content.
func (o *HTTPOverlay) HasPendingURL(url string) bool {
	_, ok := o.pending[url]
	return ok
}

// PendingVersion returns the checksum and revision captured for url.
func (o *HTTPOverlay) PendingVersion(url string) (checksum string, revision uint64, found bool) {
	pending, ok := o.pending[url]
	if !ok {
		return "", 0, false
	}
	return pending.checksum, pending.revision, true
}
