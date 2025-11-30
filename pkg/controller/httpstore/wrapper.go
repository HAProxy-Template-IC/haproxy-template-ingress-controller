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
	"time"

	"haproxy-template-ic/pkg/httpstore"

	"github.com/nikolalohinski/gonja/v2/exec"
)

// HTTPStoreWrapper wraps HTTPStore for template access.
//
// It provides the Fetch method callable from templates:
//
//	{{ http.Fetch("https://example.com/data.txt", {"delay": "60s"}) }}
//	{{ http.Fetch("https://api.example.com/data", {"delay": "5m"}, {"type": "bearer", "token": token}) }}
//
// During validation renders, it returns pending content if available.
// During production renders, it returns accepted content only.
type HTTPStoreWrapper struct {
	component    *Component
	logger       *slog.Logger
	isValidation bool // True during validation render
	ctx          context.Context
}

// NewHTTPStoreWrapper creates a new HTTPStoreWrapper.
//
// Parameters:
//   - component: The httpstore component for store access and URL registration
//   - logger: Logger for debug messages
//   - isValidation: If true, return pending content; if false, return accepted only
//   - ctx: Context for HTTP requests
func NewHTTPStoreWrapper(component *Component, logger *slog.Logger, isValidation bool, ctx context.Context) *HTTPStoreWrapper {
	return &HTTPStoreWrapper{
		component:    component,
		logger:       logger.With("component", "http-wrapper"),
		isValidation: isValidation,
		ctx:          ctx,
	}
}

// Fetch fetches content from a URL.
//
// Template usage:
//
//	Basic fetch (no refresh):
//	  {{ http.Fetch("https://example.com/data.txt") }}
//
//	With refresh interval:
//	  {{ http.Fetch("https://example.com/data.txt", {"delay": "60s"}) }}
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
func (w *HTTPStoreWrapper) Fetch(args ...interface{}) (interface{}, error) {
	// Parse all arguments
	url, opts, auth, err := w.parseArgs(args)
	if err != nil {
		return nil, err
	}

	// Try to get cached content first
	if content, ok := w.getCachedContent(url); ok {
		return content, nil
	}

	// No cached content - perform synchronous fetch
	store := w.component.GetStore()
	content, err := store.Fetch(w.ctx, url, opts, auth)
	if err != nil {
		return nil, err
	}

	// Register URL for periodic refresh if delay is configured
	if opts.Delay > 0 {
		w.component.RegisterURL(url)
	}

	return content, nil
}

// parseArgs extracts and validates URL, options, and auth from variadic arguments.
func (w *HTTPStoreWrapper) parseArgs(args []interface{}) (string, httpstore.FetchOptions, *httpstore.AuthConfig, error) {
	if len(args) < 1 {
		return "", httpstore.FetchOptions{}, nil, fmt.Errorf("http.Fetch requires at least 1 argument (url)")
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
func parseOptionsArg(args []interface{}) (httpstore.FetchOptions, error) {
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
func parseAuthFromArg(arg interface{}) (*httpstore.AuthConfig, error) {
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

// getCachedContent returns cached content based on render mode.
func (w *HTTPStoreWrapper) getCachedContent(url string) (string, bool) {
	store := w.component.GetStore()

	if w.isValidation {
		if content, ok := store.GetForValidation(url); ok {
			w.logger.Debug("returning content for validation",
				"url", url,
				"size", len(content))
			return content, true
		}
		return "", false
	}

	// Production render - only return accepted content
	if content, ok := store.Get(url); ok {
		w.logger.Debug("returning accepted content",
			"url", url,
			"size", len(content))
		return content, true
	}
	return "", false
}

// parseFetchOptions parses a map into FetchOptions.
func parseFetchOptions(m map[string]interface{}) (httpstore.FetchOptions, error) {
	opts := httpstore.FetchOptions{}

	if v, ok := m["delay"]; ok {
		d, err := parseDuration(v)
		if err != nil {
			return opts, fmt.Errorf("invalid delay: %w", err)
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
func parseAuthConfig(m map[string]interface{}) (*httpstore.AuthConfig, error) {
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
			return nil, fmt.Errorf("invalid headers: expected map")
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

// toString converts an interface to string, handling Gonja's PyString type.
func toString(v interface{}) (string, error) {
	switch val := v.(type) {
	case string:
		return val, nil
	case fmt.Stringer:
		return val.String(), nil
	default:
		return "", fmt.Errorf("expected string, got %T", v)
	}
}

// toMap converts an interface to map[string]interface{}.
func toMap(v interface{}) (map[string]interface{}, bool) {
	switch val := v.(type) {
	case map[string]interface{}:
		return val, true
	case map[interface{}]interface{}:
		// Convert to string keys
		result := make(map[string]interface{})
		for k, v := range val {
			switch key := k.(type) {
			case string:
				result[key] = v
			case fmt.Stringer:
				result[key.String()] = v
			}
		}
		return result, true
	case *exec.Dict:
		// Convert Gonja's Dict type to map[string]interface{}
		result := make(map[string]interface{})
		for _, pair := range val.Pairs {
			result[pair.Key.String()] = pair.Value.Interface()
		}
		return result, true
	default:
		return nil, false
	}
}

// toInt converts an interface to int.
func toInt(v interface{}) (int, error) {
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
func toBool(v interface{}) (bool, error) {
	switch val := v.(type) {
	case bool:
		return val, nil
	default:
		return false, fmt.Errorf("expected bool, got %T", v)
	}
}

// parseDuration parses a duration from string or time.Duration.
func parseDuration(v interface{}) (time.Duration, error) {
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
