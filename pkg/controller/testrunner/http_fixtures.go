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

package testrunner

import (
	"fmt"
	"log/slog"

	"haproxy-template-ic/pkg/core/config"
	"haproxy-template-ic/pkg/httpstore"
)

// FixtureHTTPStoreWrapper wraps an HTTPStore pre-populated with fixtures for test execution.
//
// Unlike the production HTTPStoreWrapper, this wrapper:
//   - Returns content only for URLs that have fixtures loaded
//   - Returns an error for URLs without fixtures (ensuring all HTTP dependencies are mocked)
//   - Does NOT make actual HTTP requests
//
// Template usage:
//
//	{{ http.Fetch("http://blocklist.example.com/list.txt") }}
//
// If the URL is not in fixtures, the template fails with a clear error message.
type FixtureHTTPStoreWrapper struct {
	store  *httpstore.HTTPStore
	logger *slog.Logger
}

// NewFixtureHTTPStoreWrapper creates a new fixture-only HTTP wrapper.
//
// Parameters:
//   - store: HTTPStore pre-populated with fixtures via LoadFixture()
//   - logger: Logger for debug messages
func NewFixtureHTTPStoreWrapper(store *httpstore.HTTPStore, logger *slog.Logger) *FixtureHTTPStoreWrapper {
	return &FixtureHTTPStoreWrapper{
		store:  store,
		logger: logger.With("component", "fixture-http-wrapper"),
	}
}

// Fetch returns fixture content for a URL.
//
// Template usage (same as production wrapper):
//
//	{{ http.Fetch("http://example.com/data.txt") }}
//	{{ http.Fetch("http://example.com/data.txt", {"delay": "5m"}) }}
//
// In fixture mode:
//   - Options (delay, timeout, etc.) are ignored
//   - Authentication is ignored
//   - Only the URL is used to look up fixture content
//   - Returns error if URL is not in fixtures
//
// Returns:
//   - Content string if URL has fixture
//   - Error if URL is not in fixtures
func (w *FixtureHTTPStoreWrapper) Fetch(args ...interface{}) (interface{}, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("http.Fetch requires at least 1 argument (url)")
	}

	// Extract URL from first argument
	url, err := fixtureToString(args[0])
	if err != nil {
		return nil, fmt.Errorf("http.Fetch: url must be a string, got %T", args[0])
	}

	// Look up fixture content
	content, ok := w.store.Get(url)
	if !ok {
		return nil, fmt.Errorf("http.Fetch: no fixture defined for URL: %s (add an httpResources fixture for this URL)", url)
	}

	w.logger.Debug("returning fixture content",
		"url", url,
		"size", len(content))

	return content, nil
}

// fixtureToString converts an interface to string for fixture lookup.
func fixtureToString(v interface{}) (string, error) {
	switch val := v.(type) {
	case string:
		return val, nil
	case fmt.Stringer:
		return val.String(), nil
	default:
		return "", fmt.Errorf("expected string, got %T", v)
	}
}

// createHTTPStoreFromFixtures creates an HTTPStore pre-populated with fixture content.
//
// Parameters:
//   - fixtures: HTTP fixtures from test definition
//   - logger: Logger for debug messages
//
// Returns:
//   - HTTPStore with fixtures loaded as accepted content
func createHTTPStoreFromFixtures(fixtures []config.HTTPResourceFixture, logger *slog.Logger) *httpstore.HTTPStore {
	store := httpstore.New(logger)

	for _, fixture := range fixtures {
		store.LoadFixture(fixture.URL, fixture.Content)
		logger.Debug("loaded HTTP fixture",
			"url", fixture.URL,
			"size", len(fixture.Content))
	}

	return store
}

// mergeHTTPFixtures merges global and test-specific HTTP fixtures.
//
// Test-specific fixtures override global fixtures for the same URL.
//
// Parameters:
//   - globalFixtures: HTTP fixtures from _global test
//   - testFixtures: HTTP fixtures from specific test
//
// Returns:
//   - Merged HTTP fixtures list
func mergeHTTPFixtures(globalFixtures, testFixtures []config.HTTPResourceFixture) []config.HTTPResourceFixture {
	// Use map to deduplicate by URL (test fixtures override global)
	fixtureMap := make(map[string]config.HTTPResourceFixture)

	// Add global fixtures first
	for _, f := range globalFixtures {
		fixtureMap[f.URL] = f
	}

	// Add test fixtures (override global for same URL)
	for _, f := range testFixtures {
		fixtureMap[f.URL] = f
	}

	// Convert back to slice
	result := make([]config.HTTPResourceFixture, 0, len(fixtureMap))
	for _, f := range fixtureMap {
		result = append(result, f)
	}

	return result
}
