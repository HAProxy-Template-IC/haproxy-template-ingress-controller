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

import "gitlab.com/haproxy-haptic/haptic/pkg/stores"

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

	// store is the underlying HTTPStore providing access to pending content.
	store *HTTPStore
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
	return &HTTPOverlay{
		pendingURLs: store.GetPendingURLs(),
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
	return o.store.GetForValidation(url)
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
	for _, u := range o.pendingURLs {
		if u == url {
			return true
		}
	}
	return false
}
