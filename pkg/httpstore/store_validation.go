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
	"time"
)

// PromotePending promotes pending content to accepted for a URL.
// This should be called after successful validation.
func (s *HTTPStore) PromotePending(url string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, exists := s.cache[url]
	if !exists || !entry.HasPending {
		return false
	}

	s.logger.Info("Promoting pending content to accepted",
		"url", url,
		"old_checksum", entry.AcceptedChecksum[:min(16, len(entry.AcceptedChecksum))]+"...",
		"new_checksum", entry.PendingChecksum[:min(16, len(entry.PendingChecksum))]+"...")

	// Promote pending to accepted
	entry.AcceptedContent = entry.PendingContent
	entry.AcceptedChecksum = entry.PendingChecksum
	entry.AcceptedTime = time.Now()

	// Clear pending
	entry.PendingContent = ""
	entry.PendingChecksum = ""
	entry.HasPending = false
	entry.ValidationState = StateAccepted

	return true
}

// RejectPending discards pending content for a URL.
// This should be called when validation fails.
func (s *HTTPStore) RejectPending(url string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, exists := s.cache[url]
	if !exists || !entry.HasPending {
		return false
	}

	s.logger.Warn("Rejecting pending content, keeping accepted version",
		"url", url,
		"rejected_checksum", entry.PendingChecksum[:min(16, len(entry.PendingChecksum))]+"...",
		"keeping_checksum", entry.AcceptedChecksum[:min(16, len(entry.AcceptedChecksum))]+"...")

	// Discard pending, keep accepted
	entry.PendingContent = ""
	entry.PendingChecksum = ""
	entry.HasPending = false
	entry.ValidationState = StateRejected

	return true
}

// PromoteAllPending promotes all pending content to accepted.
// This is used when validation succeeds for the entire config.
func (s *HTTPStore) PromoteAllPending() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	count := 0
	for url, entry := range s.cache {
		if !entry.HasPending {
			continue
		}

		s.logger.Info("Promoting pending content to accepted",
			"url", url,
			"new_checksum", entry.PendingChecksum[:min(16, len(entry.PendingChecksum))]+"...")

		entry.AcceptedContent = entry.PendingContent
		entry.AcceptedChecksum = entry.PendingChecksum
		entry.AcceptedTime = time.Now()
		entry.PendingContent = ""
		entry.PendingChecksum = ""
		entry.HasPending = false
		entry.ValidationState = StateAccepted
		count++
	}
	return count
}

// RejectAllPending rejects all pending content.
// This is used when validation fails for the entire config.
func (s *HTTPStore) RejectAllPending() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	count := 0
	for url, entry := range s.cache {
		if !entry.HasPending {
			continue
		}

		s.logger.Warn("Rejecting pending content",
			"url", url,
			"rejected_checksum", entry.PendingChecksum[:min(16, len(entry.PendingChecksum))]+"...")

		entry.PendingContent = ""
		entry.PendingChecksum = ""
		entry.HasPending = false
		entry.ValidationState = StateRejected
		count++
	}
	return count
}

// HasPendingValidation returns true if any URL has pending content awaiting validation.
func (s *HTTPStore) HasPendingValidation() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, entry := range s.cache {
		if entry.HasPending {
			return true
		}
	}
	return false
}

// GetPendingURLs returns all URLs with pending content awaiting validation.
func (s *HTTPStore) GetPendingURLs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var urls []string
	for url, entry := range s.cache {
		if entry.HasPending {
			urls = append(urls, url)
		}
	}
	return urls
}

// EvictUnused removes cache entries that haven't been accessed within maxAge.
// Entries with pending validation are never evicted to protect the two-version cache.
// Returns the list of evicted URLs (empty if none evicted or eviction disabled).
//
// This method is called periodically by the event adapter to prevent unbounded
// memory growth when templates change and old URLs are no longer used.
func (s *HTTPStore) EvictUnused() []string {
	if s.maxAge == 0 {
		return nil // Eviction disabled
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-s.maxAge)
	var evictedURLs []string

	for url, entry := range s.cache {
		// Never evict entries with pending validation
		if entry.HasPending {
			continue
		}

		// Evict if last access time is before cutoff
		if entry.LastAccessTime.Before(cutoff) {
			s.logger.Info("Evicting unused HTTP cache entry",
				"url", url,
				"last_access", entry.LastAccessTime,
				"age", now.Sub(entry.LastAccessTime))
			delete(s.cache, url)
			evictedURLs = append(evictedURLs, url)
		}
	}

	if len(evictedURLs) > 0 {
		s.logger.Info("HTTP store eviction complete",
			"evicted", len(evictedURLs),
			"remaining", len(s.cache))
	}

	return evictedURLs
}
