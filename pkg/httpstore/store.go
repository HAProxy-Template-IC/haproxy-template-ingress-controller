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
	"errors"
	"fmt"
	"log/slog"
	"maps"
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
//   - Automatic eviction of unused entries based on last access time
//
// Thread-safe for concurrent access.
type HTTPStore struct {
	mu         sync.RWMutex
	cache      map[string]*CacheEntry // URL -> CacheEntry
	httpClient *http.Client
	logger     *slog.Logger
	maxAge     time.Duration // Maximum time an entry can remain unused before eviction (0 = disabled)

	// validationStuckAfter bounds how long an entry may sit in StateValidating.
	// Only PromotePending/RejectPending leave that state, and both are driven by
	// a ProposalValidationCompletedEvent — so a lost event, or a panic in the
	// validator, would otherwise freeze the URL at its accepted content for the
	// process lifetime. Overridden in tests.
	validationStuckAfter time.Duration
}

// DefaultValidationStuckAfter bounds a pending validation. Render plus
// three-phase HAProxy validation takes seconds; an entry still validating
// minutes later is waiting for a verdict that is never coming.
const DefaultValidationStuckAfter = 5 * time.Minute

// New creates a new HTTPStore with the given logger and maximum cache age.
//
// maxAge is the maximum time an entry can remain unused before becoming eligible
// for eviction. If maxAge is 0, entries are never evicted based on access time.
func New(logger *slog.Logger, maxAge time.Duration) *HTTPStore {
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
					return errors.New("too many redirects")
				}
				return nil
			},
		},
		logger:               logger.With("component", "httpstore"),
		maxAge:               maxAge,
		validationStuckAfter: DefaultValidationStuckAfter,
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
	s.mu.Lock()
	entry, exists := s.cache[url]
	if exists && entry.AcceptedContent != "" {
		content := entry.AcceptedContent
		entry.LastAccessTime = time.Now() // Track access for eviction
		s.mu.Unlock()
		s.logger.Log(context.Background(), levelTrace, "returning cached content",
			"url", url,
			"size", len(content),
			"age", time.Since(entry.AcceptedTime).String())
		return content, nil
	}
	s.mu.Unlock()

	// Cache miss - perform synchronous fetch
	s.logger.Info("Performing initial HTTP fetch",
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
	checksum := checksum(content)
	now := time.Now()
	s.mu.Lock()
	s.cache[url] = &CacheEntry{
		URL:              url,
		AcceptedContent:  content,
		AcceptedChecksum: checksum,
		AcceptedTime:     now,
		LastAccessTime:   now,
		ValidationState:  StateAccepted,
		ETag:             etag,
		LastModified:     lastModified,
		Options:          opts,
		Auth:             auth,
	}
	s.mu.Unlock()

	s.logger.Debug("Cached HTTP content",
		"url", url,
		"size", len(content),
		"checksum", checksum[:16]+"...")

	return content, nil
}

// Get returns the accepted content for a URL if it exists in cache.
// Returns empty string and false if not cached.
// Updates LastAccessTime to track usage for cache eviction.
func (s *HTTPStore) Get(url string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, exists := s.cache[url]
	if !exists || entry.AcceptedContent == "" {
		return "", false
	}
	entry.LastAccessTime = time.Now()
	return entry.AcceptedContent, true
}

// GetForValidation returns content for validation rendering.
// If pending content exists, returns pending; otherwise returns accepted.
// Updates LastAccessTime to track usage for cache eviction.
func (s *HTTPStore) GetForValidation(url string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, exists := s.cache[url]
	if !exists {
		return "", false
	}

	entry.LastAccessTime = time.Now()

	if entry.HasPending {
		return entry.PendingContent, true
	}
	if entry.AcceptedContent != "" {
		return entry.AcceptedContent, true
	}
	return "", false
}

// abandonStuckValidation reports whether the caller should proceed with a
// refresh over an entry that is still validating. It returns false while the
// verdict may legitimately still arrive; past the deadline it discards the
// pending content and returns true.
//
// Without this the entry never leaves StateValidating: only a verdict clears
// that state, every later refresh short-circuits on it, and eviction skips
// entries with pending content — so one lost verdict freezes the URL at its
// accepted content for the process lifetime.
func (s *HTTPStore) abandonStuckValidation(url string, stuckFor time.Duration) bool {
	if stuckFor <= s.validationStuckAfter {
		s.logger.Log(context.Background(), levelTrace, "skipping refresh, validation in progress", "url", url)
		return false
	}

	s.logger.Warn("Abandoning stuck HTTP content validation, no verdict arrived",
		"url", url,
		"stuck_for", stuckFor.Round(time.Second),
		"timeout", s.validationStuckAfter)
	s.RejectPending(url)
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

	// Skip if already validating — unless the verdict is never coming.
	if entry.ValidationState == StateValidating {
		stuckFor := time.Since(entry.ValidationStartedAt)
		s.mu.RUnlock()

		if !s.abandonStuckValidation(url, stuckFor) {
			return false, nil
		}

		s.mu.RLock()
		entry, exists = s.cache[url]
		if !exists {
			s.mu.RUnlock()
			return false, fmt.Errorf("URL not in cache: %s", url)
		}
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
		// 304 Not Modified: the server confirmed our cached copy is current.
		// This is distinct from a 200 OK whose body happens to be empty.
		if errors.Is(err, errNotModified) {
			s.logger.Log(context.Background(), levelTrace, "content not modified (304)",
				"url", url,
				"etag", etag)
			return false, nil
		}
		s.logger.Warn("Refresh fetch failed",
			"url", url,
			"error", err)
		return false, err
	}

	// 200 OK — the body is fresh content (an empty body is a real change to
	// empty, no longer misread as a 304). Check if it actually changed.
	newChecksum := checksum(content)
	if newChecksum == acceptedChecksum {
		s.logger.Log(context.Background(), levelTrace, "content unchanged (same checksum)",
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
		e.ValidationStartedAt = time.Now()
		e.ETag = newEtag
		e.LastModified = newLastModified
	}
	s.mu.Unlock()

	s.logger.Debug("Content changed, stored as pending",
		"url", url,
		"old_checksum", acceptedChecksum[:min(16, len(acceptedChecksum))]+"...",
		"new_checksum", newChecksum[:16]+"...",
		"new_size", len(content))

	return true, nil
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
			maps.Copy(authCopy.Headers, entry.Auth.Headers)
		}
		entryCopy.Auth = &authCopy
	}
	return &entryCopy
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

	checksum := checksum(content)
	now := time.Now()
	s.cache[url] = &CacheEntry{
		URL:              url,
		AcceptedContent:  content,
		AcceptedChecksum: checksum,
		AcceptedTime:     now,
		LastAccessTime:   now,
		ValidationState:  StateAccepted,
		// No pending content, no ETag - fixtures are immediately accepted
	}

	s.logger.Debug("Loaded HTTP fixture",
		"url", url,
		"size", len(content),
		"checksum", checksum[:min(16, len(checksum))]+"...")
}
