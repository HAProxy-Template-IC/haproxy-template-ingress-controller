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
	"slices"

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
	accepted    map[string]overlayAcceptedContent
	source      SourceID
	watermark   Revision
}

type overlayPendingContent struct {
	content        string
	checksum       string
	revision       uint64
	sourceIdentity string
	descriptor     SourceDescriptor
	token          SnapshotToken
	cacheable      bool
}

type overlayAcceptedContent struct {
	content        string
	sourceIdentity string
	descriptor     SourceDescriptor
	token          SnapshotToken
	cacheable      bool
	observation    Revision
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
	return newHTTPOverlay(store, true)
}

// NewAcceptedHTTPOverlay freezes accepted content without selecting pending versions.
func NewAcceptedHTTPOverlay(store *HTTPStore) *HTTPOverlay {
	return newHTTPOverlay(store, false)
}

func newHTTPOverlay(store *HTTPStore, includePending bool) *HTTPOverlay {
	store.mu.RLock()
	overlay := newHTTPOverlayFromState(
		store.cache, store.revisionSource, store.semanticRevision, includePending,
	)
	store.mu.RUnlock()
	return overlay
}

func newHTTPOverlayFromState(
	cache map[string]*CacheEntry,
	source SourceID,
	watermark Revision,
	includePending bool,
) *HTTPOverlay {
	pendingURLs := make([]string, 0)
	pending := make(map[string]overlayPendingContent)
	accepted := make(map[string]overlayAcceptedContent)
	for url, entry := range cache {
		if entry.AcceptedChecksum != "" {
			token := SnapshotToken{
				source: source, url: entry.URL, descriptor: entry.sourceDescriptor,
				kind: SnapshotAccepted, revision: entry.acceptedRevision,
			}
			accepted[url] = overlayAcceptedContent{
				content:        entry.AcceptedContent,
				sourceIdentity: entry.sourceIdentity,
				descriptor:     entry.sourceDescriptor,
				token:          token,
				cacheable:      token.Valid(),
				observation:    entry.acceptedRevision,
			}
		}
		if !includePending || !entry.HasPending {
			continue
		}
		pendingURLs = append(pendingURLs, url)
		token := SnapshotToken{
			source:     source,
			url:        url,
			descriptor: entry.sourceDescriptor,
			kind:       SnapshotPending,
			revision:   Revision(entry.PendingRevision),
		}
		pending[url] = overlayPendingContent{
			content:        entry.PendingContent,
			checksum:       entry.PendingChecksum,
			revision:       entry.PendingRevision,
			sourceIdentity: entry.sourceIdentity,
			descriptor:     entry.sourceDescriptor,
			token:          token,
			cacheable:      token.Valid(),
		}
	}
	slices.Sort(pendingURLs)
	return &HTTPOverlay{
		pendingURLs: pendingURLs,
		pending:     pending,
		accepted:    accepted,
		source:      source,
		watermark:   watermark,
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
	accepted, ok := o.accepted[url]
	return accepted.content, ok
}

// GetContentForSource returns overlay content only for its fetch authority.
func (o *HTTPOverlay) GetContentForSource(url, sourceIdentity string) (string, bool) {
	if pending, ok := o.pending[url]; ok {
		if pending.sourceIdentity != sourceIdentity {
			return "", false
		}
		return pending.content, true
	}
	accepted, ok := o.accepted[url]
	if !ok || accepted.sourceIdentity != sourceIdentity {
		return "", false
	}
	return accepted.content, true
}

// GetContentForDescriptor returns content only for the exact fetch declaration.
func (o *HTTPOverlay) GetContentForDescriptor(url string, descriptor SourceDescriptor) (string, bool) {
	if pending, ok := o.pending[url]; ok {
		if pending.descriptor != descriptor {
			return "", false
		}
		return pending.content, true
	}
	accepted, ok := o.accepted[url]
	if !ok || accepted.descriptor != descriptor {
		return "", false
	}
	return accepted.content, true
}

// Snapshot returns frozen pending or accepted bytes for one exact source.
func (o *HTTPOverlay) Snapshot(url string, descriptor SourceDescriptor) ContentSnapshot {
	if pending, ok := o.pending[url]; ok {
		if pending.descriptor != descriptor {
			return ContentSnapshot{URL: url, Descriptor: descriptor, StoreSource: o.source, Watermark: o.watermark}
		}
		return ContentSnapshot{
			URL:         url,
			Descriptor:  descriptor,
			Content:     pending.content,
			Found:       true,
			Cacheable:   pending.cacheable,
			Token:       pending.token,
			StoreSource: o.source,
			Observation: pending.token.revision,
			Watermark:   o.watermark,
		}
	}
	accepted, ok := o.accepted[url]
	if !ok || accepted.descriptor != descriptor {
		return ContentSnapshot{URL: url, Descriptor: descriptor, StoreSource: o.source, Watermark: o.watermark}
	}
	return ContentSnapshot{
		URL:         url,
		Descriptor:  descriptor,
		Content:     accepted.content,
		Found:       true,
		Cacheable:   accepted.cacheable,
		Token:       accepted.token,
		StoreSource: o.source,
		Observation: accepted.observation,
		Watermark:   o.watermark,
	}
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
