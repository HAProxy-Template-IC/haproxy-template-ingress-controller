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
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// HTTPStore provides HTTP resource fetching with caching and two-version validation.
//
// The store supports:
//   - Synchronous initial fetch (blocks until complete)
//   - Cached access to previously fetched content
//   - Two-version cache for safe validation (pending vs accepted)
//   - Conditional requests using ETag/If-Modified-Since
//
// Thread-safe for concurrent access.
type HTTPStore struct {
	mu         sync.RWMutex
	cache      map[string]*CacheEntry // URL -> CacheEntry
	httpClient *http.Client
	logger     *slog.Logger
}

// New creates a new HTTPStore with the given logger.
func New(logger *slog.Logger) *HTTPStore {
	if logger == nil {
		logger = slog.Default()
	}

	return &HTTPStore{
		cache: make(map[string]*CacheEntry),
		httpClient: &http.Client{
			Timeout: DefaultTimeout,
			// Don't follow redirects automatically - we want to handle them
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return fmt.Errorf("too many redirects")
				}
				return nil
			},
		},
		logger: logger.With("component", "httpstore"),
	}
}

// Fetch retrieves content from a URL, using cache if available.
//
// On first call for a URL, this performs a synchronous HTTP fetch and caches the result.
// Subsequent calls return cached content immediately.
//
// If the URL has a Delay > 0 in options, the caller is responsible for scheduling
// refreshes (typically done by the event adapter component).
//
// Parameters:
//   - ctx: Context for cancellation and timeout
//   - url: The HTTP(S) URL to fetch
//   - opts: Fetch options (timeout, retries, critical flag, delay for refresh)
//   - auth: Optional authentication configuration
//
// Returns:
//   - Content string (empty if fetch failed and not critical)
//   - Error if critical fetch fails
func (s *HTTPStore) Fetch(ctx context.Context, url string, opts FetchOptions, auth *AuthConfig) (string, error) {
	opts = opts.WithDefaults()

	// Check cache first
	s.mu.RLock()
	entry, exists := s.cache[url]
	if exists && entry.AcceptedContent != "" {
		content := entry.AcceptedContent
		s.mu.RUnlock()
		s.logger.Debug("returning cached content",
			"url", url,
			"size", len(content),
			"age", time.Since(entry.AcceptedTime).String())
		return content, nil
	}
	s.mu.RUnlock()

	// Cache miss - perform synchronous fetch
	s.logger.Info("performing initial HTTP fetch",
		"url", url,
		"timeout", opts.Timeout.String(),
		"retries", opts.Retries,
		"critical", opts.Critical)

	content, etag, lastModified, err := s.fetchWithRetry(ctx, url, opts, auth, "", "")
	if err != nil {
		if opts.Critical {
			return "", fmt.Errorf("critical HTTP fetch failed for %s: %w", url, err)
		}
		s.logger.Warn("HTTP fetch failed, returning empty content",
			"url", url,
			"error", err)
		return "", nil
	}

	// Store in cache
	checksum := Checksum(content)
	s.mu.Lock()
	s.cache[url] = &CacheEntry{
		URL:              url,
		AcceptedContent:  content,
		AcceptedChecksum: checksum,
		AcceptedTime:     time.Now(),
		ValidationState:  StateAccepted,
		ETag:             etag,
		LastModified:     lastModified,
		Options:          opts,
		Auth:             auth,
	}
	s.mu.Unlock()

	s.logger.Info("cached HTTP content",
		"url", url,
		"size", len(content),
		"checksum", checksum[:16]+"...")

	return content, nil
}

// Get returns the accepted content for a URL if it exists in cache.
// Returns empty string and false if not cached.
func (s *HTTPStore) Get(url string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, exists := s.cache[url]
	if !exists || entry.AcceptedContent == "" {
		return "", false
	}
	return entry.AcceptedContent, true
}

// GetPending returns the pending content for a URL if it exists.
// This is used during validation to render with pending content.
// Returns empty string and false if no pending content.
func (s *HTTPStore) GetPending(url string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, exists := s.cache[url]
	if !exists || !entry.HasPending {
		return "", false
	}
	return entry.PendingContent, true
}

// GetForValidation returns content for validation rendering.
// If pending content exists, returns pending; otherwise returns accepted.
func (s *HTTPStore) GetForValidation(url string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, exists := s.cache[url]
	if !exists {
		return "", false
	}

	if entry.HasPending {
		return entry.PendingContent, true
	}
	if entry.AcceptedContent != "" {
		return entry.AcceptedContent, true
	}
	return "", false
}

// PromotePending promotes pending content to accepted for a URL.
// This should be called after successful validation.
func (s *HTTPStore) PromotePending(url string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, exists := s.cache[url]
	if !exists || !entry.HasPending {
		return false
	}

	s.logger.Info("promoting pending content to accepted",
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

	s.logger.Warn("rejecting pending content, keeping accepted version",
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

// RefreshURL fetches fresh content for a URL and stores it as pending.
//
// This does NOT replace accepted content immediately. The caller must:
// 1. Trigger re-render with pending content (using GetForValidation)
// 2. On successful validation, call PromotePending
// 3. On failed validation, call RejectPending
//
// Returns:
//   - changed: true if content changed from accepted version
//   - err: fetch error (nil if successful or 304 Not Modified)
func (s *HTTPStore) RefreshURL(ctx context.Context, url string) (changed bool, err error) {
	// Get current cache state
	s.mu.RLock()
	entry, exists := s.cache[url]
	if !exists {
		s.mu.RUnlock()
		return false, fmt.Errorf("URL not in cache: %s", url)
	}

	// Skip if already validating
	if entry.ValidationState == StateValidating {
		s.mu.RUnlock()
		s.logger.Debug("skipping refresh, validation in progress", "url", url)
		return false, nil
	}

	opts := entry.Options
	auth := entry.Auth
	etag := entry.ETag
	lastModified := entry.LastModified
	acceptedChecksum := entry.AcceptedChecksum
	s.mu.RUnlock()

	// Fetch with conditional headers
	content, newEtag, newLastModified, err := s.fetchWithRetry(ctx, url, opts, auth, etag, lastModified)
	if err != nil {
		// Check for 304 Not Modified (handled by fetchWithRetry returning empty content)
		s.logger.Warn("refresh fetch failed",
			"url", url,
			"error", err)
		return false, err
	}

	// Empty content with no error means 304 Not Modified
	if content == "" && newEtag == etag {
		s.logger.Debug("content not modified (304)",
			"url", url,
			"etag", etag)
		return false, nil
	}

	// Check if content actually changed
	newChecksum := Checksum(content)
	if newChecksum == acceptedChecksum {
		s.logger.Debug("content unchanged (same checksum)",
			"url", url,
			"checksum", newChecksum[:16]+"...")

		// Update cache headers even if content unchanged
		s.mu.Lock()
		if e, ok := s.cache[url]; ok {
			e.ETag = newEtag
			e.LastModified = newLastModified
		}
		s.mu.Unlock()

		return false, nil
	}

	// Content changed - store as pending for validation
	s.mu.Lock()
	if e, ok := s.cache[url]; ok {
		e.PendingContent = content
		e.PendingChecksum = newChecksum
		e.HasPending = true
		e.ValidationState = StateValidating
		e.ETag = newEtag
		e.LastModified = newLastModified
	}
	s.mu.Unlock()

	s.logger.Info("content changed, stored as pending",
		"url", url,
		"old_checksum", acceptedChecksum[:min(16, len(acceptedChecksum))]+"...",
		"new_checksum", newChecksum[:16]+"...",
		"new_size", len(content))

	return true, nil
}

// GetURLsWithDelay returns all cached URLs that have a refresh delay configured.
// This is used by the event adapter to schedule refresh timers.
func (s *HTTPStore) GetURLsWithDelay() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var urls []string
	for url, entry := range s.cache {
		if entry.Options.Delay > 0 {
			urls = append(urls, url)
		}
	}
	return urls
}

// GetDelay returns the configured delay for a URL.
// Returns 0 if URL not in cache or no delay configured.
func (s *HTTPStore) GetDelay(url string) time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if entry, exists := s.cache[url]; exists {
		return entry.Options.Delay
	}
	return 0
}

// GetEntry returns a copy of the cache entry for a URL.
// Returns nil if not cached.
func (s *HTTPStore) GetEntry(url string) *CacheEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, exists := s.cache[url]
	if !exists {
		return nil
	}

	// Return a copy to prevent external modification
	entryCopy := *entry
	if entry.Auth != nil {
		authCopy := *entry.Auth
		if entry.Auth.Headers != nil {
			authCopy.Headers = make(map[string]string, len(entry.Auth.Headers))
			for k, v := range entry.Auth.Headers {
				authCopy.Headers[k] = v
			}
		}
		entryCopy.Auth = &authCopy
	}
	return &entryCopy
}

// Clear removes all entries from the cache.
func (s *HTTPStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cache = make(map[string]*CacheEntry)
	s.logger.Info("cleared HTTP store cache")
}

// Size returns the number of cached URLs.
func (s *HTTPStore) Size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.cache)
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

		s.logger.Info("promoting pending content to accepted",
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

		s.logger.Warn("rejecting pending content",
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

// LoadFixture loads a single HTTP fixture directly into the store as accepted content.
// This is used by validation tests to provide mock HTTP responses without making
// actual HTTP requests.
//
// The fixture is stored directly as accepted content, bypassing the normal
// fetch and validation workflow.
func (s *HTTPStore) LoadFixture(url, content string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	checksum := Checksum(content)
	s.cache[url] = &CacheEntry{
		URL:              url,
		AcceptedContent:  content,
		AcceptedChecksum: checksum,
		AcceptedTime:     time.Now(),
		ValidationState:  StateAccepted,
		// No pending content, no ETag - fixtures are immediately accepted
	}

	s.logger.Debug("loaded HTTP fixture",
		"url", url,
		"size", len(content),
		"checksum", checksum[:min(16, len(checksum))]+"...")
}
