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
	"sync"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/httpstore"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

// SourceMode controls whether a render may replace the shared HTTP source.
type SourceMode uint8

const (
	// SourceModeReadOnly keeps source declarations and fetched content local to one render.
	SourceModeReadOnly SourceMode = iota
	// SourceModeAuthoritative reconciles accepted sources and their refresh timers.
	SourceModeAuthoritative
)

// HTTPStoreWrapper exposes HTTPStore content to one template render.
type HTTPStoreWrapper struct {
	component      *Component
	logger         *slog.Logger
	overlay        stores.HTTPContentOverlay
	ctx            context.Context
	sourceMode     SourceMode
	transientStore *httpstore.HTTPStore
	transaction    *InputTransaction
	mu             sync.Mutex
	declared       map[string]string
}

// NewHTTPStoreWrapper creates a new HTTPStoreWrapper.
func NewHTTPStoreWrapper(
	ctx context.Context,
	component *Component,
	logger *slog.Logger,
	overlay stores.HTTPContentOverlay,
	sourceMode SourceMode,
) *HTTPStoreWrapper {
	wrapper := &HTTPStoreWrapper{
		component:      component,
		logger:         logger.With("component", "http-wrapper"),
		overlay:        overlay,
		ctx:            ctx,
		sourceMode:     sourceMode,
		transientStore: httpstore.New(logger, 0),
		declared:       make(map[string]string),
	}
	if sourceMode == SourceModeAuthoritative {
		wrapper.transaction = newInputTransaction(component)
	}
	return wrapper
}

// InputTransaction returns the authoritative render's candidate transaction.
func (w *HTTPStoreWrapper) InputTransaction() *InputTransaction {
	return w.transaction
}

// Fetch fetches content from a URL.
//
// Template usage:
//
//	Basic fetch (no refresh):
//	  {{ http.Fetch("https://example.com/data.txt") }}
//
//	With refresh interval — the first fetch is synchronous either way; this
//	only sets how often the content is re-checked afterwards:
//	  {{ http.Fetch("https://example.com/data.txt", {"interval": "60s"}) }}
//
//	With options:
//	  {{ http.Fetch("https://example.com/data.txt", {"delay": "5m", "timeout": "30s", "retries": 3, "critical": true}) }}
//
//	With authentication:
//	  {{ http.Fetch("https://api.example.com/data", {"delay": "5m"}, {"type": "bearer", "token": token}) }}
//	  {{ http.Fetch("https://api.example.com/data", {"delay": "5m"}, {"type": "basic", "username": user, "password": pass}) }}
//
// Parameters (variadic):
//   - url (string, required): The HTTP(S) URL to fetch
//   - options (map, optional): {"delay": "60s", "timeout": "30s", "retries": 3, "critical": true}
//   - auth (map, optional): {"type": "bearer"|"basic"|"header", "token": "...", "username": "...", "password": "..."}
//
// Returns:
//   - Content string (empty if fetch failed and not critical)
//   - Error if critical fetch fails
func (w *HTTPStoreWrapper) Fetch(args ...any) (any, error) {
	// Parse all arguments
	url, opts, auth, err := w.parseArgs(args)
	if err != nil {
		return nil, err
	}
	identity, err := httpstore.SourceIdentity(opts, auth)
	if err != nil {
		return nil, fmt.Errorf("http.Fetch: %w", err)
	}
	if err := w.declare(url, identity); err != nil {
		return nil, err
	}
	if err := w.rejectOverlaySourceConflict(url, identity); err != nil {
		return nil, err
	}

	if w.sourceMode != SourceModeAuthoritative {
		return w.fetchReadOnly(url, identity, opts, auth)
	}
	return w.fetchAuthoritative(url, opts, auth)
}

func (w *HTTPStoreWrapper) fetchReadOnly(
	url string,
	identity string,
	opts httpstore.FetchOptions,
	auth *httpstore.AuthConfig,
) (any, error) {
	content, ok, err := w.getCachedContent(url, identity)
	if err != nil {
		return nil, err
	}
	if ok {
		return content, nil
	}

	content, err = w.transientStore.Fetch(w.ctx, url, opts, auth)
	if err != nil {
		return nil, err
	}
	return content, nil
}

func (w *HTTPStoreWrapper) fetchAuthoritative(
	url string,
	opts httpstore.FetchOptions,
	auth *httpstore.AuthConfig,
) (any, error) {
	state, err := w.component.ReconcileSource(url, opts, auth)
	if err != nil {
		return nil, fmt.Errorf("http.Fetch: %w", err)
	}
	content, err := w.transaction.fetch(w.ctx, url, state)
	if err != nil {
		return nil, err
	}

	return content, nil
}

func (w *HTTPStoreWrapper) declare(url, identity string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	previous, exists := w.declared[url]
	if exists && previous != identity {
		return fmt.Errorf(
			"http.Fetch: URL %s uses conflicting authentication or options in one render; use one declaration per URL",
			url,
		)
	}
	w.declared[url] = identity
	return nil
}

func (w *HTTPStoreWrapper) rejectOverlaySourceConflict(url, sourceIdentity string) error {
	if w.overlay == nil || !w.overlay.HasPendingURL(url) {
		return nil
	}
	if _, ok := w.overlay.GetContentForSource(url, sourceIdentity); ok {
		return nil
	}
	return fmt.Errorf(
		"http.Fetch: URL %s has pending content from different authentication or options; retry after the source change settles",
		url,
	)
}

// parseArgs extracts and validates URL, options, and auth from variadic arguments.
func (w *HTTPStoreWrapper) parseArgs(args []any) (string, httpstore.FetchOptions, *httpstore.AuthConfig, error) {
	if len(args) < 1 {
		return "", httpstore.FetchOptions{}, nil, errors.New("http.Fetch requires at least 1 argument (url)")
	}

	// Extract URL
	url, err := toString(args[0])
	if err != nil {
		return "", httpstore.FetchOptions{}, nil, fmt.Errorf("http.Fetch: url must be a string, got %T", args[0])
	}

	// Parse options (optional second argument)
	opts, err := parseOptionsArg(args)
	if err != nil {
		return "", httpstore.FetchOptions{}, nil, err
	}

	// Parse auth (optional third argument)
	var auth *httpstore.AuthConfig
	if len(args) >= 3 && args[2] != nil {
		auth, err = parseAuthFromArg(args[2])
		if err != nil {
			return "", httpstore.FetchOptions{}, nil, err
		}
	}

	return url, opts, auth, nil
}

// parseOptionsArg extracts FetchOptions from the second argument if present.
func parseOptionsArg(args []any) (httpstore.FetchOptions, error) {
	if len(args) < 2 || args[1] == nil {
		return httpstore.FetchOptions{}, nil
	}

	optMap, ok := toMap(args[1])
	if !ok {
		return httpstore.FetchOptions{}, fmt.Errorf("http.Fetch: options must be a map, got %T", args[1])
	}

	opts, err := parseFetchOptions(optMap)
	if err != nil {
		return httpstore.FetchOptions{}, fmt.Errorf("http.Fetch: %w", err)
	}
	return opts, nil
}

// parseAuthFromArg extracts AuthConfig from a non-nil argument.
func parseAuthFromArg(arg any) (*httpstore.AuthConfig, error) {
	authMap, ok := toMap(arg)
	if !ok {
		return nil, fmt.Errorf("http.Fetch: auth must be a map, got %T", arg)
	}

	auth, err := parseAuthConfig(authMap)
	if err != nil {
		return nil, fmt.Errorf("http.Fetch: %w", err)
	}
	return auth, nil
}

// getCachedContent returns matching overlay content or accepted shared content.
func (w *HTTPStoreWrapper) getCachedContent(url, sourceIdentity string) (content string, found bool, err error) {
	if w.overlay != nil {
		if content, ok := w.overlay.GetContentForSource(url, sourceIdentity); ok {
			w.logger.Debug("Returning content via overlay",
				"url", url,
				"size", len(content),
				"has_pending", w.overlay.HasPendingURL(url))
			return content, true, nil
		}
		if w.overlay.HasPendingURL(url) {
			return "", false, fmt.Errorf(
				"http.Fetch: URL %s has pending content from different authentication or options; retry after the source change settles",
				url,
			)
		}
		return "", false, nil
	}

	store := w.component.GetStore()
	if content, ok := store.GetSource(url, sourceIdentity); ok {
		w.logger.Debug("Returning accepted content",
			"url", url,
			"size", len(content))
		return content, true, nil
	}
	return "", false, nil
}

// parseFetchOptions parses a map into FetchOptions.
// Option keys for the refresh cadence. optDelay is the original spelling: it
// reads like a wait before the first fetch, which it never was — that fetch is
// synchronous — so optInterval is the name and optDelay is kept working.
const (
	optInterval = "interval"
	optDelay    = "delay"
)

func parseFetchOptions(m map[string]any) (httpstore.FetchOptions, error) {
	opts := httpstore.FetchOptions{}

	// "interval" is the name; "delay" is the original spelling, kept working.
	// Rejecting both together rather than letting one silently win: a config
	// setting each to a different value has no obvious right answer, and
	// picking one would leave the other looking effective when it is not.
	_, hasInterval := m[optInterval]
	_, hasDelay := m[optDelay]
	if hasInterval && hasDelay {
		return opts, errors.New(
			"http.Fetch: set either \"interval\" or its deprecated alias \"delay\", not both")
	}
	key := optInterval
	if hasDelay {
		key = optDelay
	}
	if v, ok := m[key]; ok {
		d, err := parseDuration(v)
		if err != nil {
			return opts, fmt.Errorf("invalid %s: %w", key, err)
		}
		opts.Delay = d
	}

	if v, ok := m["timeout"]; ok {
		d, err := parseDuration(v)
		if err != nil {
			return opts, fmt.Errorf("invalid timeout: %w", err)
		}
		opts.Timeout = d
	}

	if v, ok := m["retries"]; ok {
		n, err := toInt(v)
		if err != nil {
			return opts, fmt.Errorf("invalid retries: %w", err)
		}
		opts.Retries = n
	}

	if v, ok := m["critical"]; ok {
		b, err := toBool(v)
		if err != nil {
			return opts, fmt.Errorf("invalid critical: %w", err)
		}
		opts.Critical = b
	}

	return opts, nil
}

// parseAuthConfig parses a map into AuthConfig.
func parseAuthConfig(m map[string]any) (*httpstore.AuthConfig, error) {
	auth := &httpstore.AuthConfig{}

	if v, ok := m["type"]; ok {
		s, err := toString(v)
		if err != nil {
			return nil, fmt.Errorf("invalid auth type: %w", err)
		}
		auth.Type = s
	}

	if v, ok := m["username"]; ok {
		s, err := toString(v)
		if err != nil {
			return nil, fmt.Errorf("invalid username: %w", err)
		}
		auth.Username = s
	}

	if v, ok := m["password"]; ok {
		s, err := toString(v)
		if err != nil {
			return nil, fmt.Errorf("invalid password: %w", err)
		}
		auth.Password = s
	}

	if v, ok := m["token"]; ok {
		s, err := toString(v)
		if err != nil {
			return nil, fmt.Errorf("invalid token: %w", err)
		}
		auth.Token = s
	}

	if v, ok := m["headers"]; ok {
		headers, ok := toMap(v)
		if !ok {
			return nil, errors.New("invalid headers: expected map")
		}
		auth.Headers = make(map[string]string)
		for k, val := range headers {
			s, err := toString(val)
			if err != nil {
				return nil, fmt.Errorf("invalid header value for %s: %w", k, err)
			}
			auth.Headers[k] = s
		}
	}

	return auth, nil
}

// toString converts an interface to string.
func toString(v any) (string, error) {
	switch val := v.(type) {
	case string:
		return val, nil
	case fmt.Stringer:
		return val.String(), nil
	default:
		return "", fmt.Errorf("expected string, got %T", v)
	}
}

// toMap converts an interface to map[string]any.
func toMap(v any) (map[string]any, bool) {
	switch val := v.(type) {
	case map[string]any:
		return val, true
	case map[any]any:
		// Convert to string keys
		result := make(map[string]any)
		for k, v := range val {
			switch key := k.(type) {
			case string:
				result[key] = v
			case fmt.Stringer:
				result[key.String()] = v
			}
		}
		return result, true
	default:
		return nil, false
	}
}

// toInt converts an interface to int.
func toInt(v any) (int, error) {
	switch val := v.(type) {
	case int:
		return val, nil
	case int64:
		return int(val), nil
	case float64:
		return int(val), nil
	default:
		return 0, fmt.Errorf("expected number, got %T", v)
	}
}

// toBool converts an interface to bool.
func toBool(v any) (bool, error) {
	switch val := v.(type) {
	case bool:
		return val, nil
	default:
		return false, fmt.Errorf("expected bool, got %T", v)
	}
}

// parseDuration parses a duration from string or time.Duration.
func parseDuration(v any) (time.Duration, error) {
	switch val := v.(type) {
	case time.Duration:
		return val, nil
	case string:
		return time.ParseDuration(val)
	case fmt.Stringer:
		return time.ParseDuration(val.String())
	default:
		return 0, fmt.Errorf("expected duration string, got %T", v)
	}
}
