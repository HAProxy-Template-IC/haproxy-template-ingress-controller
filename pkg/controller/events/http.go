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

package events

// HTTPResourceUpdatedEvent is published when HTTP resource content has changed.
// This triggers a reconciliation cycle with the new content as "pending".
// The content must pass validation before being promoted to "accepted".
// This event is always coalescible since it represents content state where only
// the latest content for a URL matters.
type HTTPResourceUpdatedEvent struct {
	URL             string // The URL that was refreshed
	ContentChecksum string // SHA256 checksum of new content
	ContentSize     int    // Size of new content in bytes
	timestamped
}

// NewHTTPResourceUpdatedEvent creates a new HTTPResourceUpdatedEvent.
func NewHTTPResourceUpdatedEvent(url, checksum string, size int) *HTTPResourceUpdatedEvent {
	return &HTTPResourceUpdatedEvent{
		URL:             url,
		ContentChecksum: checksum,
		ContentSize:     size,
		timestamped:     newTimestamped(),
	}
}

func (e *HTTPResourceUpdatedEvent) EventType() string { return EventTypeHTTPResourceUpdated }

// Coalescible returns true because HTTP resource update events represent state
// where only the latest content matters. If the same URL updates multiple times
// before reconciliation completes, older updates can be safely skipped.
func (e *HTTPResourceUpdatedEvent) Coalescible() bool { return true }

// HTTPResourceAcceptedEvent is published when pending HTTP content passes validation.
// The content has been promoted from "pending" to "accepted" state.
type HTTPResourceAcceptedEvent struct {
	URL             string // The URL whose content was accepted
	ContentChecksum string // SHA256 checksum of accepted content
	ContentSize     int    // Size of accepted content in bytes
	timestamped
}

// NewHTTPResourceAcceptedEvent creates a new HTTPResourceAcceptedEvent.
func NewHTTPResourceAcceptedEvent(url, checksum string, size int) *HTTPResourceAcceptedEvent {
	return &HTTPResourceAcceptedEvent{
		URL:             url,
		ContentChecksum: checksum,
		ContentSize:     size,
		timestamped:     newTimestamped(),
	}
}

func (e *HTTPResourceAcceptedEvent) EventType() string { return EventTypeHTTPResourceAccepted }

// HTTPContentAcceptanceRequestedEvent is published when a deploying render left
// out HTTP content no render has accepted yet. The leader answers it with an
// acceptance attempt outside the reconcile loop.
type HTTPContentAcceptanceRequestedEvent struct {
	timestamped
}

// NewHTTPContentAcceptanceRequestedEvent creates a new HTTPContentAcceptanceRequestedEvent.
func NewHTTPContentAcceptanceRequestedEvent() *HTTPContentAcceptanceRequestedEvent {
	return &HTTPContentAcceptanceRequestedEvent{timestamped: newTimestamped()}
}

func (e *HTTPContentAcceptanceRequestedEvent) EventType() string {
	return EventTypeHTTPContentAcceptanceRequested
}

// HTTPContentRevokedEvent is published when accepted HTTP content was taken
// back because HAProxy refused a render containing it.
type HTTPContentRevokedEvent struct {
	URL             string // The URL whose content was revoked (credentials redacted)
	ContentChecksum string // SHA256 checksum of the revoked content
	Restored        bool   // The URL went back to its previous accepted content
	Critical        bool   // Renders fail until the URL has accepted content again
	timestamped
}

// NewHTTPContentRevokedEvent creates a new HTTPContentRevokedEvent.
func NewHTTPContentRevokedEvent(url, checksum string, restored, critical bool) *HTTPContentRevokedEvent {
	return &HTTPContentRevokedEvent{
		URL:             url,
		ContentChecksum: checksum,
		Restored:        restored,
		Critical:        critical,
		timestamped:     newTimestamped(),
	}
}

func (e *HTTPContentRevokedEvent) EventType() string { return EventTypeHTTPContentRevoked }

// HTTPContentRejectedEvent reports pending content refused before acceptance.
type HTTPContentRejectedEvent struct {
	URL string // Credentials, query, and fragment are redacted.
	timestamped
}

func NewHTTPContentRejectedEvent(url string) *HTTPContentRejectedEvent {
	return &HTTPContentRejectedEvent{URL: url, timestamped: newTimestamped()}
}

func (e *HTTPContentRejectedEvent) EventType() string { return EventTypeHTTPContentRejected }
