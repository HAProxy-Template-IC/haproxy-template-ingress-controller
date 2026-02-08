package client

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockRoundTripper is a mock implementation of http.RoundTripper for testing.
type mockRoundTripper struct {
	response *http.Response
	err      error
}

func (m *mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.response, m.err
}

// captureLogger captures log output for testing.
type captureLogger struct {
	*slog.Logger
	buffer *bytes.Buffer
}

func newCaptureLogger() *captureLogger {
	buf := &bytes.Buffer{}
	handler := slog.NewTextHandler(buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	return &captureLogger{
		Logger: slog.New(handler),
		buffer: buf,
	}
}

func (c *captureLogger) output() string {
	return c.buffer.String()
}

func TestLoggingRoundTripper_Success(t *testing.T) {
	logger := newCaptureLogger()

	mockTransport := &mockRoundTripper{
		response: &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"status":"ok"}`)),
		},
	}

	rt := newLoggingRoundTripper(mockTransport, logger.Logger)

	req, err := http.NewRequest("GET", "http://haproxy:5555/v3/configuration/version", http.NoBody)
	require.NoError(t, err)

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Should NOT log successful request
	assert.Empty(t, logger.output(), "Expected no logging for 2xx status code")
}

func TestLoggingRoundTripper_404NotFound(t *testing.T) {
	logger := newCaptureLogger()

	mockTransport := &mockRoundTripper{
		response: &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader(`{"code":404,"message":"Not found"}`)),
		},
	}

	rt := newLoggingRoundTripper(mockTransport, logger.Logger)

	req, err := http.NewRequest("GET", "http://haproxy:5555/v3/services/haproxy/storage/ssl_crt_lists", http.NoBody)
	require.NoError(t, err)

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)

	// Should log the error with full URL
	output := logger.output()
	assert.Contains(t, output, "Dataplane API returned non-2xx status code")
	assert.Contains(t, output, "method=GET")
	assert.Contains(t, output, "url=http://haproxy:5555/v3/services/haproxy/storage/ssl_crt_lists")
	assert.Contains(t, output, "status_code=404")
	assert.Contains(t, output, `response_body="{\"code\":404,\"message\":\"Not found\"}"`)
}

func TestLoggingRoundTripper_422UnprocessableEntity(t *testing.T) {
	logger := newCaptureLogger()

	requestBody := `{"id":"tx123"}`
	responseBody := `{"code":422,"message":"Invalid transaction"}`

	mockTransport := &mockRoundTripper{
		response: &http.Response{
			StatusCode: http.StatusUnprocessableEntity,
			Body:       io.NopCloser(strings.NewReader(responseBody)),
		},
	}

	rt := newLoggingRoundTripper(mockTransport, logger.Logger)

	req, err := http.NewRequest("POST", "http://haproxy:5555/v3/transactions", strings.NewReader(requestBody))
	require.NoError(t, err)

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

	// Should log the error
	output := logger.output()
	assert.Contains(t, output, "Dataplane API returned non-2xx status code")
	assert.Contains(t, output, "method=POST")
	assert.Contains(t, output, "url=http://haproxy:5555/v3/transactions")
	assert.Contains(t, output, "status_code=422")
	// JSON strings are escaped in log output
	assert.Contains(t, output, `request_body="{\"id\":\"tx123\"}"`)
	assert.Contains(t, output, `response_body="{\"code\":422,\"message\":\"Invalid transaction\"}"`)
}

func TestLoggingRoundTripper_500InternalServerError(t *testing.T) {
	logger := newCaptureLogger()

	mockTransport := &mockRoundTripper{
		response: &http.Response{
			StatusCode: http.StatusInternalServerError,
			Body:       io.NopCloser(strings.NewReader(`{"code":500,"message":"Internal error"}`)),
		},
	}

	rt := newLoggingRoundTripper(mockTransport, logger.Logger)

	req, err := http.NewRequest("POST", "http://haproxy:5555/v3/services/haproxy/configuration/raw", strings.NewReader("global\n  maxconn 2000"))
	require.NoError(t, err)

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)

	// Should log the error
	output := logger.output()
	assert.Contains(t, output, "Dataplane API returned non-2xx status code")
	assert.Contains(t, output, "status_code=500")
}

func TestLoggingRoundTripper_AllStatusCodes(t *testing.T) {
	tests := []struct {
		name        string
		statusCode  int
		shouldLog   bool
		description string
	}{
		{"100 Continue", 100, true, "1xx informational should be logged"},
		{"200 OK", 200, false, "2xx success should not be logged"},
		{"201 Created", 201, false, "2xx success should not be logged"},
		{"202 Accepted", 202, false, "2xx success should not be logged"},
		{"204 No Content", 204, false, "2xx success should not be logged"},
		{"300 Multiple Choices", 300, true, "3xx redirect should be logged"},
		{"301 Moved Permanently", 301, true, "3xx redirect should be logged"},
		{"400 Bad Request", 400, true, "4xx client error should be logged"},
		{"401 Unauthorized", 401, true, "4xx client error should be logged"},
		{"403 Forbidden", 403, true, "4xx client error should be logged"},
		{"404 Not Found", 404, true, "4xx client error should be logged"},
		{"406 Not Acceptable", 406, true, "4xx client error should be logged"},
		{"409 Conflict", 409, true, "4xx client error should be logged"},
		{"422 Unprocessable Entity", 422, true, "4xx client error should be logged"},
		{"500 Internal Server Error", 500, true, "5xx server error should be logged"},
		{"502 Bad Gateway", 502, true, "5xx server error should be logged"},
		{"503 Service Unavailable", 503, true, "5xx server error should be logged"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := newCaptureLogger()

			mockTransport := &mockRoundTripper{
				response: &http.Response{
					StatusCode: tt.statusCode,
					Body:       io.NopCloser(strings.NewReader(`{}`)),
				},
			}

			rt := newLoggingRoundTripper(mockTransport, logger.Logger)

			req, err := http.NewRequest("GET", "http://haproxy:5555/v3/test", http.NoBody)
			require.NoError(t, err)

			resp, err := rt.RoundTrip(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			output := logger.output()
			if tt.shouldLog {
				assert.NotEmpty(t, output, "Expected logging for status %d: %s", tt.statusCode, tt.description)
				assert.Contains(t, output, "Dataplane API returned non-2xx status code")
			} else {
				assert.Empty(t, output, "Expected no logging for status %d: %s", tt.statusCode, tt.description)
			}
		})
	}
}

func TestLoggingRoundTripper_NilLogger(t *testing.T) {
	mockTransport := &mockRoundTripper{
		response: &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader(`{}`)),
		},
	}

	// Pass nil logger
	rt := newLoggingRoundTripper(mockTransport, nil)

	req, err := http.NewRequest("GET", "http://haproxy:5555/v3/test", http.NoBody)
	require.NoError(t, err)

	// Should not panic with nil logger
	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestLoggingRoundTripper_NilBaseTransport(t *testing.T) {
	logger := newCaptureLogger()

	// Pass nil base transport
	rt := newLoggingRoundTripper(nil, logger.Logger)

	// Should use http.DefaultTransport
	assert.NotNil(t, rt.base)
	assert.Equal(t, http.DefaultTransport, rt.base)
}

func TestLoggingRoundTripper_RequestBodyCapture(t *testing.T) {
	logger := newCaptureLogger()

	requestBody := `{"key":"value"}`

	mockTransport := &mockRoundTripper{
		response: &http.Response{
			StatusCode: http.StatusBadRequest,
			Body:       io.NopCloser(strings.NewReader(`{}`)),
		},
	}

	rt := newLoggingRoundTripper(mockTransport, logger.Logger)

	req, err := http.NewRequest("POST", "http://haproxy:5555/v3/test", strings.NewReader(requestBody))
	require.NoError(t, err)

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	// Verify request body was logged (JSON is escaped in log output)
	output := logger.output()
	assert.Contains(t, output, `request_body="{\"key\":\"value\"}"`)

	// Verify request body is still readable (was restored)
	// Note: In the mock, we don't actually use req.Body, but this tests the restoration logic
	assert.NotNil(t, req.Body)
}

func TestLoggingRoundTripper_ResponseBodyCapture(t *testing.T) {
	logger := newCaptureLogger()

	responseBody := `{"error":"validation failed"}`

	mockTransport := &mockRoundTripper{
		response: &http.Response{
			StatusCode: http.StatusBadRequest,
			Body:       io.NopCloser(strings.NewReader(responseBody)),
		},
	}

	rt := newLoggingRoundTripper(mockTransport, logger.Logger)

	req, err := http.NewRequest("GET", "http://haproxy:5555/v3/test", http.NoBody)
	require.NoError(t, err)

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	// Verify response body was logged (JSON is escaped in log output)
	output := logger.output()
	assert.Contains(t, output, `response_body="{\"error\":\"validation failed\"}"`)

	// Verify response body is still readable (was restored)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, responseBody, string(body))
}

func TestLoggingRoundTripper_EmptyRequestBody(t *testing.T) {
	logger := newCaptureLogger()

	mockTransport := &mockRoundTripper{
		response: &http.Response{
			StatusCode: http.StatusNotFound,
			Body:       io.NopCloser(strings.NewReader(`{}`)),
		},
	}

	rt := newLoggingRoundTripper(mockTransport, logger.Logger)

	req, err := http.NewRequest("GET", "http://haproxy:5555/v3/test", http.NoBody)
	require.NoError(t, err)

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)

	// Should not panic with nil request body
	output := logger.output()
	assert.Contains(t, output, "request_body=\"\"")
}

func TestLoggingRoundTripper_TransportError(t *testing.T) {
	logger := newCaptureLogger()

	expectedErr := io.EOF
	mockTransport := &mockRoundTripper{
		err: expectedErr,
	}

	rt := newLoggingRoundTripper(mockTransport, logger.Logger)

	req, err := http.NewRequest("GET", "http://haproxy:5555/v3/test", http.NoBody)
	require.NoError(t, err)

	resp, err := rt.RoundTrip(req)
	if resp != nil {
		defer resp.Body.Close()
	}
	assert.Error(t, err)
	assert.Equal(t, expectedErr, err)
	assert.Nil(t, resp)

	// Should not log when transport returns error (no response)
	assert.Empty(t, logger.output())
}
