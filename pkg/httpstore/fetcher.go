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
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"time"
)

// fetchWithRetry performs an HTTP GET with retry logic and conditional request headers.
//
// Parameters:
//   - ctx: Context for cancellation
//   - url: The URL to fetch
//   - opts: Fetch options (timeout, retries)
//   - auth: Optional authentication config
//   - etag: Previous ETag for conditional request (empty to skip)
//   - lastModified: Previous Last-Modified for conditional request (empty to skip)
//
// Returns:
//   - content: The response body (empty string for 304 Not Modified)
//   - newEtag: The ETag header from response
//   - newLastModified: The Last-Modified header from response
//   - err: Error if all retries failed
func (s *HTTPStore) fetchWithRetry(
	ctx context.Context,
	url string,
	opts FetchOptions,
	auth *AuthConfig,
	etag string,
	lastModified string,
) (content, newEtag, newLastModified string, err error) {
	opts = opts.WithDefaults()

	var lastErr error
	for attempt := 0; attempt <= opts.Retries; attempt++ {
		if attempt > 0 {
			// Wait before retry with exponential backoff
			// Cap the exponent to prevent overflow (max ~32x multiplier)
			exp := attempt - 1
			if exp > 5 {
				exp = 5
			}
			delay := opts.RetryDelay * time.Duration(1<<exp)
			s.logger.Debug("retrying HTTP fetch",
				"url", url,
				"attempt", attempt+1,
				"delay", delay.String())

			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return "", "", "", ctx.Err()
			}
		}

		content, newEtag, newLastModified, err = s.doFetch(ctx, url, opts, auth, etag, lastModified)
		if err == nil {
			return content, newEtag, newLastModified, nil
		}

		lastErr = err
		s.logger.Debug("HTTP fetch attempt failed",
			"url", url,
			"attempt", attempt+1,
			"error", err)
	}

	return "", "", "", fmt.Errorf("all %d retry attempts failed: %w", opts.Retries+1, lastErr)
}

// doFetch performs a single HTTP GET request and returns the response content
// along with cache headers (ETag, Last-Modified) for conditional requests.
func (s *HTTPStore) doFetch(
	ctx context.Context,
	url string,
	opts FetchOptions,
	auth *AuthConfig,
	etag string,
	lastModified string,
) (content, etagResult, lastModifiedResult string, err error) {
	// Create request with timeout
	reqCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to create request: %w", err)
	}

	// Add conditional request headers
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if lastModified != "" {
		req.Header.Set("If-Modified-Since", lastModified)
	}

	// Add authentication headers
	if auth != nil {
		addAuthHeaders(req, auth)
	}

	// Add user agent
	req.Header.Set("User-Agent", "haproxy-template-ic/1.0")

	// Perform request
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", "", "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Extract cache headers
	etagResult = resp.Header.Get("ETag")
	lastModifiedResult = resp.Header.Get("Last-Modified")

	// Handle response status
	switch resp.StatusCode {
	case http.StatusOK:
		// Read body with size limit
		limitedReader := io.LimitReader(resp.Body, MaxContentSize+1)
		var body []byte
		body, err = io.ReadAll(limitedReader)
		if err != nil {
			return "", "", "", fmt.Errorf("failed to read response body: %w", err)
		}

		if len(body) > MaxContentSize {
			return "", "", "", fmt.Errorf("response body exceeds maximum size of %d bytes", MaxContentSize)
		}

		return string(body), etagResult, lastModifiedResult, nil

	case http.StatusNotModified:
		// Content unchanged - return empty content but preserve original etag
		return "", etag, lastModifiedResult, nil

	case http.StatusUnauthorized:
		return "", "", "", fmt.Errorf("authentication failed (401 Unauthorized)")

	case http.StatusForbidden:
		return "", "", "", fmt.Errorf("access denied (403 Forbidden)")

	case http.StatusNotFound:
		return "", "", "", fmt.Errorf("resource not found (404 Not Found)")

	default:
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return "", "", "", fmt.Errorf("client error: %s", resp.Status)
		}
		if resp.StatusCode >= 500 {
			return "", "", "", fmt.Errorf("server error: %s", resp.Status)
		}
		return "", "", "", fmt.Errorf("unexpected status: %s", resp.Status)
	}
}

// addAuthHeaders adds authentication headers to the request.
func addAuthHeaders(req *http.Request, auth *AuthConfig) {
	switch auth.Type {
	case "basic":
		if auth.Username != "" || auth.Password != "" {
			credentials := base64.StdEncoding.EncodeToString(
				[]byte(auth.Username + ":" + auth.Password))
			req.Header.Set("Authorization", "Basic "+credentials)
		}

	case "bearer":
		if auth.Token != "" {
			req.Header.Set("Authorization", "Bearer "+auth.Token)
		}

	case "header":
		// Custom headers for API key authentication etc.
		for key, value := range auth.Headers {
			req.Header.Set(key, value)
		}

	default:
		// Unknown auth type - add custom headers if present
		for key, value := range auth.Headers {
			req.Header.Set(key, value)
		}
	}
}
