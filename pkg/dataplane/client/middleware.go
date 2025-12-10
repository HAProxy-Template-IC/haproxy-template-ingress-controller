package client

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
)

// loggingRoundTripper is an HTTP RoundTripper that logs request details when
// the Dataplane API returns a non-2xx status code.
//
// This helps debug API errors by capturing:
// - HTTP method (GET, POST, PUT, etc.)
// - Full request URL including query parameters
// - Request body payload
// - Response body payload
// - Status code
//
// The middleware is transparent for successful requests (2xx status codes) and
// only adds logging overhead when errors occur.
type loggingRoundTripper struct {
	base   http.RoundTripper
	logger *slog.Logger
}

// newLoggingRoundTripper creates a new logging middleware that wraps the provided
// base RoundTripper. If base is nil, http.DefaultTransport is used.
func newLoggingRoundTripper(base http.RoundTripper, logger *slog.Logger) *loggingRoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &loggingRoundTripper{
		base:   base,
		logger: logger,
	}
}

// RoundTrip implements the http.RoundTripper interface.
//
// It captures the request body, executes the request, and logs details if
// the response status is non-2xx (< 200 or >= 300).
func (t *loggingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Capture request body for potential logging
	// We need to read the body before the request is sent, then restore it
	var requestBody []byte
	if req.Body != nil {
		var err error
		requestBody, err = io.ReadAll(req.Body)
		if err != nil {
			t.logger.Warn("Failed to read request body for logging", "error", err)
		}
		// Restore the body for the actual request
		req.Body = io.NopCloser(bytes.NewBuffer(requestBody))
	}

	// Execute the actual HTTP request
	resp, err := t.base.RoundTrip(req)

	// Check for non-2xx status and log if found
	if err == nil && (resp.StatusCode < 200 || resp.StatusCode >= 300) {
		// Capture response body for logging
		var responseBody []byte
		if resp.Body != nil {
			responseBody, err = io.ReadAll(resp.Body)
			if err != nil {
				t.logger.Warn("Failed to read response body for logging", "error", err)
			}
			// Restore the response body for the caller
			resp.Body = io.NopCloser(bytes.NewBuffer(responseBody))
		}

		t.logger.Error("Dataplane API returned non-2xx status code",
			"method", req.Method,
			"url", req.URL.String(),
			"status_code", resp.StatusCode,
			"request_body", string(requestBody),
			"response_body", string(responseBody),
		)
	}

	return resp, err
}
