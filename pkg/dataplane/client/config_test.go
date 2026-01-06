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
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createTestClient creates a test client with a mock server.
// Returns the client and a cleanup function.
func createTestClient(t *testing.T, handler http.HandlerFunc) (client *DataplaneClient, cleanup func()) {
	t.Helper()

	server := httptest.NewServer(handler)

	client, err := New(context.Background(), &Config{
		BaseURL:  server.URL,
		Username: "admin",
		Password: "password",
	})
	require.NoError(t, err)

	return client, server.Close
}

func TestGetVersion(t *testing.T) {
	tests := []struct {
		name        string
		versionResp string
		statusCode  int
		expectErr   bool
		wantVersion int64
	}{
		{
			name:        "valid version",
			versionResp: "42",
			statusCode:  http.StatusOK,
			expectErr:   false,
			wantVersion: 42,
		},
		{
			name:        "version with whitespace",
			versionResp: "  123\n",
			statusCode:  http.StatusOK,
			expectErr:   false,
			wantVersion: 123,
		},
		{
			name:        "large version number",
			versionResp: "999999999",
			statusCode:  http.StatusOK,
			expectErr:   false,
			wantVersion: 999999999,
		},
		{
			name:        "server error",
			versionResp: "error",
			statusCode:  http.StatusInternalServerError,
			expectErr:   true,
		},
		{
			name:        "invalid version format",
			versionResp: "not-a-number",
			statusCode:  http.StatusOK,
			expectErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, cleanup := createTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				// Handle version detection for client initialization
				if r.URL.Path == "/v3/info" {
					w.WriteHeader(http.StatusOK)
					fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
					return
				}

				// Handle configuration version request (no /v3/ prefix in generated client paths)
				if r.URL.Path == "/services/haproxy/configuration/version" {
					w.WriteHeader(tt.statusCode)
					fmt.Fprint(w, tt.versionResp)
					return
				}

				w.WriteHeader(http.StatusNotFound)
			})
			defer cleanup()

			version, err := client.GetVersion(context.Background())

			if tt.expectErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantVersion, version)
			}
		})
	}
}

func TestGetRawConfiguration(t *testing.T) {
	tests := []struct {
		name       string
		configResp string
		statusCode int
		expectErr  bool
	}{
		{
			name: "valid configuration",
			configResp: `global
  daemon

defaults
  mode http

frontend http
  bind :80
`,
			statusCode: http.StatusOK,
			expectErr:  false,
		},
		{
			name:       "empty configuration",
			configResp: "",
			statusCode: http.StatusOK,
			expectErr:  false,
		},
		{
			name:       "server error",
			configResp: "error",
			statusCode: http.StatusInternalServerError,
			expectErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, cleanup := createTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				// Handle version detection
				if r.URL.Path == "/v3/info" {
					w.WriteHeader(http.StatusOK)
					fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
					return
				}

				// Handle configuration request (no /v3/ prefix in generated client paths)
				if r.URL.Path == "/services/haproxy/configuration/raw" {
					w.WriteHeader(tt.statusCode)
					fmt.Fprint(w, tt.configResp)
					return
				}

				w.WriteHeader(http.StatusNotFound)
			})
			defer cleanup()

			config, err := client.GetRawConfiguration(context.Background())

			if tt.expectErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.configResp, config)
			}
		})
	}
}

// makePushConfigHandler creates an HTTP handler for push configuration tests.
func makePushConfigHandler(statusCode int, reloadID string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v3/info" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
			return
		}

		if r.URL.Path == "/services/haproxy/configuration/raw" && r.Method == "POST" {
			if reloadID != "" {
				w.Header().Set("Reload-ID", reloadID)
			}
			w.WriteHeader(statusCode)
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}
}

// assertPushConfigResult validates push configuration test results.
func assertPushConfigResult(t *testing.T, expectErr bool, wantReloadID, gotReloadID string, err error) {
	t.Helper()
	if expectErr {
		require.Error(t, err)
		return
	}
	require.NoError(t, err)
	assert.Equal(t, wantReloadID, gotReloadID)
}

func TestPushRawConfiguration(t *testing.T) {
	tests := []struct {
		name         string
		statusCode   int
		reloadID     string
		expectErr    bool
		wantReloadID string
	}{
		{
			name:         "success with reload",
			statusCode:   http.StatusAccepted,
			reloadID:     "reload-123",
			expectErr:    false,
			wantReloadID: "reload-123",
		},
		{
			name:         "success without reload",
			statusCode:   http.StatusOK,
			reloadID:     "",
			expectErr:    false,
			wantReloadID: "",
		},
		{
			name:       "bad request",
			statusCode: http.StatusBadRequest,
			expectErr:  true,
		},
		{
			name:       "server error",
			statusCode: http.StatusInternalServerError,
			expectErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, cleanup := createTestClient(t, makePushConfigHandler(tt.statusCode, tt.reloadID))
			defer cleanup()

			reloadID, err := client.PushRawConfiguration(context.Background(), "global\n  daemon\n", 1)
			assertPushConfigResult(t, tt.expectErr, tt.wantReloadID, reloadID, err)
		})
	}
}

func TestVersionConflictError(t *testing.T) {
	err := &VersionConflictError{
		ExpectedVersion: 42,
		ActualVersion:   "45",
	}

	assert.Contains(t, err.Error(), "42")
	assert.Contains(t, err.Error(), "45")
	assert.Contains(t, err.Error(), "version conflict")
}

// makeReloadStatusHandler creates an HTTP handler for reload status tests.
func makeReloadStatusHandler(reloadID string, statusCode int, responseBody string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v3/info" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
			return
		}

		expectedPath := fmt.Sprintf("/services/haproxy/reloads/%s", reloadID)
		if r.URL.Path == expectedPath && r.Method == "GET" {
			w.WriteHeader(statusCode)
			if responseBody != "" {
				fmt.Fprint(w, responseBody)
			}
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}
}

// reloadStatusTestCase defines test case data for reload status tests.
type reloadStatusTestCase struct {
	name          string
	reloadID      string
	statusCode    int
	responseBody  string
	expectErr     bool
	errContains   string
	wantStatus    ReloadStatus
	wantResponse  string
	wantTimestamp int64
}

// assertReloadStatusResult validates reload status test results.
func assertReloadStatusResult(t *testing.T, tc *reloadStatusTestCase, info *ReloadInfo, err error) {
	t.Helper()
	if tc.expectErr {
		require.Error(t, err)
		if tc.errContains != "" {
			assert.Contains(t, err.Error(), tc.errContains)
		}
		return
	}

	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, tc.reloadID, info.ID)
	assert.Equal(t, tc.wantStatus, info.Status)
	if tc.wantResponse != "" {
		assert.Equal(t, tc.wantResponse, info.Response)
	}
	if tc.wantTimestamp != 0 {
		assert.Equal(t, tc.wantTimestamp, info.ReloadTimestamp)
	}
}

func TestGetReloadStatus(t *testing.T) {
	tests := []reloadStatusTestCase{
		{
			name:       "reload succeeded",
			reloadID:   "abc123",
			statusCode: http.StatusOK,
			responseBody: `{
				"id": "abc123",
				"status": "succeeded",
				"reload_timestamp": 1640000000
			}`,
			wantStatus:    ReloadStatusSucceeded,
			wantTimestamp: 1640000000,
		},
		{
			name:       "reload in progress",
			reloadID:   "def456",
			statusCode: http.StatusOK,
			responseBody: `{
				"id": "def456",
				"status": "in_progress"
			}`,
			wantStatus: ReloadStatusInProgress,
		},
		{
			name:       "reload failed",
			reloadID:   "ghi789",
			statusCode: http.StatusOK,
			responseBody: `{
				"id": "ghi789",
				"status": "failed",
				"response": "configuration validation failed: missing backend"
			}`,
			wantStatus:   ReloadStatusFailed,
			wantResponse: "configuration validation failed: missing backend",
		},
		{
			name:        "reload not found",
			reloadID:    "nonexistent",
			statusCode:  http.StatusNotFound,
			expectErr:   true,
			errContains: "reload ID not found",
		},
		{
			name:        "server error",
			reloadID:    "error",
			statusCode:  http.StatusInternalServerError,
			expectErr:   true,
			errContains: "status 500",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, cleanup := createTestClient(t, makeReloadStatusHandler(tt.reloadID, tt.statusCode, tt.responseBody))
			defer cleanup()

			info, err := client.GetReloadStatus(context.Background(), tt.reloadID)
			assertReloadStatusResult(t, &tt, info, err)
		})
	}
}

func TestReloadStatusConstants(t *testing.T) {
	// Verify constants match HAProxy DataPlane API values
	assert.Equal(t, ReloadStatus("in_progress"), ReloadStatusInProgress)
	assert.Equal(t, ReloadStatus("succeeded"), ReloadStatusSucceeded)
	assert.Equal(t, ReloadStatus("failed"), ReloadStatusFailed)
}
