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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecuteWithVersion_Success(t *testing.T) {
	versionCallCount := int32(0)
	operationCallCount := int32(0)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handle version detection
		if r.URL.Path == "/v3/info" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
			return
		}

		// Handle configuration version
		if r.URL.Path == "/services/haproxy/configuration/version" {
			atomic.AddInt32(&versionCallCount, 1)
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, "42")
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client, err := New(context.Background(), &Config{
		BaseURL:  server.URL,
		Username: "admin",
		Password: "password",
	})
	require.NoError(t, err)

	result, err := ExecuteWithVersion(context.Background(), client, func(ctx context.Context, version int) (string, error) {
		atomic.AddInt32(&operationCallCount, 1)
		return fmt.Sprintf("success with version %d", version), nil
	})

	require.NoError(t, err)
	assert.Equal(t, "success with version 42", result)
	assert.Equal(t, int32(1), atomic.LoadInt32(&versionCallCount))
	assert.Equal(t, int32(1), atomic.LoadInt32(&operationCallCount))
}

func TestExecuteWithVersion_VersionConflictRetry(t *testing.T) {
	versionCallCount := int32(0)
	operationCallCount := int32(0)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handle version detection
		if r.URL.Path == "/v3/info" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
			return
		}

		// Handle configuration version
		if r.URL.Path == "/services/haproxy/configuration/version" {
			count := atomic.AddInt32(&versionCallCount, 1)
			w.WriteHeader(http.StatusOK)
			// Return incrementing versions
			fmt.Fprintf(w, "%d", 40+count)
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client, err := New(context.Background(), &Config{
		BaseURL:  server.URL,
		Username: "admin",
		Password: "password",
	})
	require.NoError(t, err)

	// Fail first two times with version conflict, succeed on third
	result, err := ExecuteWithVersion(context.Background(), client, func(ctx context.Context, version int) (string, error) {
		count := atomic.AddInt32(&operationCallCount, 1)
		if count < 3 {
			return "", &VersionConflictError{
				ExpectedVersion: int64(version),
				ActualVersion:   fmt.Sprintf("%d", version+1),
			}
		}
		return fmt.Sprintf("success with version %d", version), nil
	})

	require.NoError(t, err)
	assert.Contains(t, result, "success with version")
	assert.Equal(t, int32(3), atomic.LoadInt32(&operationCallCount))
}

func TestExecuteWithVersion_GetVersionFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handle version detection
		if r.URL.Path == "/v3/info" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
			return
		}

		// Return error for version request
		if r.URL.Path == "/services/haproxy/configuration/version" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client, err := New(context.Background(), &Config{
		BaseURL:  server.URL,
		Username: "admin",
		Password: "password",
	})
	require.NoError(t, err)

	_, err = ExecuteWithVersion(context.Background(), client, func(ctx context.Context, version int) (string, error) {
		return "success", nil
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get version")
}

func TestExecuteWithVersionCustom(t *testing.T) {
	versionCallCount := int32(0)
	operationCallCount := int32(0)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handle version detection
		if r.URL.Path == "/v3/info" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
			return
		}

		// Handle configuration version
		if r.URL.Path == "/services/haproxy/configuration/version" {
			count := atomic.AddInt32(&versionCallCount, 1)
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "%d", 40+count)
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client, err := New(context.Background(), &Config{
		BaseURL:  server.URL,
		Username: "admin",
		Password: "password",
	})
	require.NoError(t, err)

	// Use custom config with more attempts
	config := RetryConfig{
		MaxAttempts: 5, // More attempts
		Backoff:     BackoffNone,
		// Note: RetryIf will be set to IsVersionConflict() by ExecuteWithVersionCustom
	}

	// Fail first 4 times, succeed on 5th
	result, err := ExecuteWithVersionCustom(context.Background(), client, config, func(ctx context.Context, version int) (string, error) {
		count := atomic.AddInt32(&operationCallCount, 1)
		if count < 5 {
			return "", &VersionConflictError{
				ExpectedVersion: int64(version),
				ActualVersion:   fmt.Sprintf("%d", version+1),
			}
		}
		return fmt.Sprintf("success with version %d", version), nil
	})

	require.NoError(t, err)
	assert.Contains(t, result, "success with version")
	assert.Equal(t, int32(5), atomic.LoadInt32(&operationCallCount))
}

func TestExecuteWithVersionCustom_DefaultRetryIf(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handle version detection
		if r.URL.Path == "/v3/info" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
			return
		}

		// Handle configuration version
		if r.URL.Path == "/services/haproxy/configuration/version" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, "42")
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client, err := New(context.Background(), &Config{
		BaseURL:  server.URL,
		Username: "admin",
		Password: "password",
	})
	require.NoError(t, err)

	// Empty config - should use defaults
	config := RetryConfig{}

	result, err := ExecuteWithVersionCustom(context.Background(), client, config, func(ctx context.Context, version int) (string, error) {
		return fmt.Sprintf("success with version %d", version), nil
	})

	require.NoError(t, err)
	assert.Equal(t, "success with version 42", result)
}

func TestExecuteWithExponentialBackoff(t *testing.T) {
	operationCallCount := int32(0)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handle version detection
		if r.URL.Path == "/v3/info" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
			return
		}

		// Handle configuration version
		if r.URL.Path == "/services/haproxy/configuration/version" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, "42")
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client, err := New(context.Background(), &Config{
		BaseURL:  server.URL,
		Username: "admin",
		Password: "password",
	})
	require.NoError(t, err)

	start := time.Now()

	// Fail first two times, succeed on third
	result, err := ExecuteWithExponentialBackoff(context.Background(), client, 10*time.Millisecond, func(ctx context.Context, version int) (string, error) {
		count := atomic.AddInt32(&operationCallCount, 1)
		if count < 3 {
			return "", &VersionConflictError{
				ExpectedVersion: int64(version),
				ActualVersion:   fmt.Sprintf("%d", version+1),
			}
		}
		return "success", nil
	})

	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Equal(t, "success", result)
	assert.Equal(t, int32(3), atomic.LoadInt32(&operationCallCount))

	// Should have delays: 10ms + 20ms = 30ms (exponential backoff)
	assert.GreaterOrEqual(t, elapsed, 30*time.Millisecond, "should have exponential backoff delays")
}
