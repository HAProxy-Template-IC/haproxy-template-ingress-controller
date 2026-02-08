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

import "time"

// HTTPResourceUpdatedEvent is published when HTTP resource content has changed.
// This triggers a reconciliation cycle with the new content as "pending".
// The content must pass validation before being promoted to "accepted".
// This event is always coalescible since it represents content state where only
// the latest content for a URL matters.
type HTTPResourceUpdatedEvent struct {
	URL             string // The URL that was refreshed
	ContentChecksum string // SHA256 checksum of new content
	ContentSize     int    // Size of new content in bytes
	timestamp       time.Time
}

// NewHTTPResourceUpdatedEvent creates a new HTTPResourceUpdatedEvent.
func NewHTTPResourceUpdatedEvent(url, checksum string, size int) *HTTPResourceUpdatedEvent {
	return &HTTPResourceUpdatedEvent{
		URL:             url,
		ContentChecksum: checksum,
		ContentSize:     size,
		timestamp:       time.Now(),
	}
}

func (e *HTTPResourceUpdatedEvent) EventType() string    { return EventTypeHTTPResourceUpdated }
func (e *HTTPResourceUpdatedEvent) Timestamp() time.Time { return e.timestamp }

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
	timestamp       time.Time
}

// NewHTTPResourceAcceptedEvent creates a new HTTPResourceAcceptedEvent.
func NewHTTPResourceAcceptedEvent(url, checksum string, size int) *HTTPResourceAcceptedEvent {
	return &HTTPResourceAcceptedEvent{
		URL:             url,
		ContentChecksum: checksum,
		ContentSize:     size,
		timestamp:       time.Now(),
	}
}

func (e *HTTPResourceAcceptedEvent) EventType() string    { return EventTypeHTTPResourceAccepted }
func (e *HTTPResourceAcceptedEvent) Timestamp() time.Time { return e.timestamp }

// HTTPResourceRejectedEvent is published when pending HTTP content fails validation.
// The old accepted content remains in use.
type HTTPResourceRejectedEvent struct {
	URL             string // The URL whose content was rejected
	ContentChecksum string // SHA256 checksum of rejected content
	Reason          string // Why the content was rejected
	timestamp       time.Time
}

// NewHTTPResourceRejectedEvent creates a new HTTPResourceRejectedEvent.
func NewHTTPResourceRejectedEvent(url, checksum, reason string) *HTTPResourceRejectedEvent {
	return &HTTPResourceRejectedEvent{
		URL:             url,
		ContentChecksum: checksum,
		Reason:          reason,
		timestamp:       time.Now(),
	}
}

func (e *HTTPResourceRejectedEvent) EventType() string    { return EventTypeHTTPResourceRejected }
func (e *HTTPResourceRejectedEvent) Timestamp() time.Time { return e.timestamp }
