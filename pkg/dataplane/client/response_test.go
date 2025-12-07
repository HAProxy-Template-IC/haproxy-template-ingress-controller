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

package client

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckResponse_Success(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
	}{
		{"200 OK", http.StatusOK},
		{"201 Created", http.StatusCreated},
		{"202 Accepted", http.StatusAccepted},
		{"204 No Content", http.StatusNoContent},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{
				StatusCode: tc.statusCode,
				Body:       io.NopCloser(strings.NewReader("")),
			}

			err := CheckResponse(resp, "test operation")
			require.NoError(t, err)
		})
	}
}

func TestCheckResponse_ClientError(t *testing.T) {
	tests := []struct {
		name         string
		statusCode   int
		body         string
		wantContains string
	}{
		{
			name:         "400 Bad Request",
			statusCode:   http.StatusBadRequest,
			body:         `{"message": "invalid request"}`,
			wantContains: "create backend failed with status 400",
		},
		{
			name:         "401 Unauthorized",
			statusCode:   http.StatusUnauthorized,
			body:         `{"message": "authentication required"}`,
			wantContains: "create backend failed with status 401",
		},
		{
			name:         "403 Forbidden",
			statusCode:   http.StatusForbidden,
			body:         `{"message": "access denied"}`,
			wantContains: "create backend failed with status 403",
		},
		{
			name:         "404 Not Found",
			statusCode:   http.StatusNotFound,
			body:         `{"message": "resource not found"}`,
			wantContains: "create backend failed with status 404",
		},
		{
			name:         "409 Conflict",
			statusCode:   http.StatusConflict,
			body:         `{"message": "version conflict"}`,
			wantContains: "create backend failed with status 409",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{
				StatusCode: tc.statusCode,
				Body:       io.NopCloser(strings.NewReader(tc.body)),
			}

			err := CheckResponse(resp, "create backend")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantContains)
		})
	}
}

func TestCheckResponse_ServerError(t *testing.T) {
	tests := []struct {
		name         string
		statusCode   int
		body         string
		wantContains string
	}{
		{
			name:         "500 Internal Server Error",
			statusCode:   http.StatusInternalServerError,
			body:         `{"message": "internal error"}`,
			wantContains: "sync config failed with status 500",
		},
		{
			name:         "502 Bad Gateway",
			statusCode:   http.StatusBadGateway,
			body:         `{"message": "bad gateway"}`,
			wantContains: "sync config failed with status 502",
		},
		{
			name:         "503 Service Unavailable",
			statusCode:   http.StatusServiceUnavailable,
			body:         `{"message": "service unavailable"}`,
			wantContains: "sync config failed with status 503",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{
				StatusCode: tc.statusCode,
				Body:       io.NopCloser(strings.NewReader(tc.body)),
			}

			err := CheckResponse(resp, "sync config")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantContains)
		})
	}
}

func TestCheckResponse_EmptyBody(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusInternalServerError,
		Body:       io.NopCloser(strings.NewReader("")),
	}

	err := CheckResponse(resp, "test operation")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "test operation failed with status 500")
}

// errorReader is a reader that always returns an error.
type errorReader struct{}

func (errorReader) Read(_ []byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

func TestCheckResponse_BodyReadError(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Body:       io.NopCloser(errorReader{}),
	}

	err := CheckResponse(resp, "test operation")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "test operation failed with status 400")
}
