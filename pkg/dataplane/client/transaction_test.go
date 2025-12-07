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

// makeTransactionHandler creates an HTTP handler for transaction tests.
func makeTransactionHandler(path, method string, statusCode int, headers map[string]string, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v3/info" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
			return
		}

		if r.URL.Path == path && r.Method == method {
			for k, v := range headers {
				w.Header().Set(k, v)
			}
			w.WriteHeader(statusCode)
			if body != "" {
				fmt.Fprint(w, body)
			}
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}
}

// createTestClientWithServer creates a test client and server for transaction tests.
func createTestClientWithServer(t *testing.T, handler http.HandlerFunc) (client *DataplaneClient, cleanup func()) {
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

// assertTransactionCreateResult validates transaction creation test results.
func assertTransactionCreateResult(t *testing.T, tx *Transaction, err error, expectErr bool, errType string, expectedVersion int64) {
	t.Helper()
	if expectErr {
		require.Error(t, err)
		if errType == "conflict" {
			var conflictErr *VersionConflictError
			assert.ErrorAs(t, err, &conflictErr)
		}
		assert.Nil(t, tx)
		return
	}
	require.NoError(t, err)
	require.NotNil(t, tx)
	assert.Equal(t, "tx-12345", tx.ID)
	assert.Equal(t, expectedVersion, tx.Version)
}

func TestCreateTransaction(t *testing.T) {
	tests := []struct {
		name       string
		version    int64
		statusCode int
		headers    map[string]string
		body       string
		expectErr  bool
		errType    string
	}{
		{
			name:       "successful transaction creation",
			version:    42,
			statusCode: http.StatusCreated,
			body:       `{"id":"tx-12345","version":42}`,
			expectErr:  false,
		},
		{
			name:       "version conflict 409",
			version:    42,
			statusCode: http.StatusConflict,
			headers:    map[string]string{"Configuration-Version": "45"},
			expectErr:  true,
			errType:    "conflict",
		},
		{
			name:       "version conflict 406",
			version:    42,
			statusCode: http.StatusNotAcceptable,
			headers:    map[string]string{"Configuration-Version": "45"},
			expectErr:  true,
			errType:    "conflict",
		},
		{
			name:       "version conflict without header",
			version:    42,
			statusCode: http.StatusConflict,
			expectErr:  true,
			errType:    "conflict",
		},
		{
			name:       "server error",
			version:    42,
			statusCode: http.StatusInternalServerError,
			body:       "internal error",
			expectErr:  true,
			errType:    "other",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := makeTransactionHandler("/services/haproxy/transactions", "POST", tt.statusCode, tt.headers, tt.body)
			client, cleanup := createTestClientWithServer(t, handler)
			defer cleanup()

			tx, err := client.CreateTransaction(context.Background(), tt.version)
			assertTransactionCreateResult(t, tx, err, tt.expectErr, tt.errType, tt.version)
		})
	}
}

// assertCommitResult validates commit test results.
func assertCommitResult(t *testing.T, result *CommitResult, err error, expectErr bool, errType string, wantStatus int, wantReload string) {
	t.Helper()
	if expectErr {
		require.Error(t, err)
		if errType == "conflict" {
			var conflictErr *VersionConflictError
			assert.ErrorAs(t, err, &conflictErr)
		}
		assert.Nil(t, result)
		return
	}
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, wantStatus, result.StatusCode)
	assert.Equal(t, wantReload, result.ReloadID)
}

func TestTransaction_Commit(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		headers    map[string]string
		expectErr  bool
		errType    string
		wantStatus int
		wantReload string
	}{
		{
			name:       "commit without reload (200)",
			statusCode: http.StatusOK,
			expectErr:  false,
			wantStatus: http.StatusOK,
		},
		{
			name:       "commit with reload (202)",
			statusCode: http.StatusAccepted,
			headers:    map[string]string{"Reload-ID": "reload-123"},
			expectErr:  false,
			wantStatus: http.StatusAccepted,
			wantReload: "reload-123",
		},
		{
			name:       "version conflict 409",
			statusCode: http.StatusConflict,
			headers:    map[string]string{"Configuration-Version": "50"},
			expectErr:  true,
			errType:    "conflict",
		},
		{
			name:       "version conflict 406",
			statusCode: http.StatusNotAcceptable,
			expectErr:  true,
			errType:    "conflict",
		},
		{
			name:       "server error",
			statusCode: http.StatusInternalServerError,
			expectErr:  true,
			errType:    "other",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := makeTransactionHandler("/services/haproxy/transactions/tx-123", "PUT", tt.statusCode, tt.headers, "")
			client, cleanup := createTestClientWithServer(t, handler)
			defer cleanup()

			tx := &Transaction{
				ID:      "tx-123",
				Version: 42,
				client:  client,
			}

			result, err := tx.Commit(context.Background())
			assertCommitResult(t, result, err, tt.expectErr, tt.errType, tt.wantStatus, tt.wantReload)
		})
	}
}

func TestTransaction_CommitIdempotent(t *testing.T) {
	callCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handle version detection
		if r.URL.Path == "/v3/info" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
			return
		}

		// Handle commit - track calls
		if r.URL.Path == "/services/haproxy/transactions/tx-123" && r.Method == "PUT" {
			callCount++
			w.WriteHeader(http.StatusOK)
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

	tx := &Transaction{
		ID:      "tx-123",
		Version: 42,
		client:  client,
	}

	// First commit
	result1, err := tx.Commit(context.Background())
	require.NoError(t, err)
	require.NotNil(t, result1)
	assert.Equal(t, 1, callCount)

	// Second commit - should return cached result, not call server
	result2, err := tx.Commit(context.Background())
	require.NoError(t, err)
	require.NotNil(t, result2)
	assert.Equal(t, 1, callCount) // Still 1 - didn't call server again

	// Results should be the same
	assert.Equal(t, result1.StatusCode, result2.StatusCode)
}

func TestTransaction_Abort(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		expectErr  bool
	}{
		{
			name:       "successful abort (204)",
			statusCode: http.StatusNoContent,
			expectErr:  false,
		},
		{
			name:       "successful abort (200)",
			statusCode: http.StatusOK,
			expectErr:  false,
		},
		{
			name:       "transaction not found (already gone)",
			statusCode: http.StatusNotFound,
			expectErr:  false, // 404 is OK for abort
		},
		{
			name:       "server error",
			statusCode: http.StatusInternalServerError,
			expectErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Handle version detection
				if r.URL.Path == "/v3/info" {
					w.WriteHeader(http.StatusOK)
					fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
					return
				}

				// Handle abort (DELETE)
				if r.URL.Path == "/services/haproxy/transactions/tx-123" && r.Method == "DELETE" {
					w.WriteHeader(tt.statusCode)
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

			tx := &Transaction{
				ID:      "tx-123",
				Version: 42,
				client:  client,
			}

			err = tx.Abort(context.Background())

			if tt.expectErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.True(t, tx.IsAborted())
			}
		})
	}
}

func TestTransaction_AbortAfterCommit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handle version detection
		if r.URL.Path == "/v3/info" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
			return
		}

		// Handle commit
		if r.URL.Path == "/services/haproxy/transactions/tx-123" && r.Method == "PUT" {
			w.WriteHeader(http.StatusOK)
			return
		}

		// Handle abort - should not be called
		if r.URL.Path == "/services/haproxy/transactions/tx-123" && r.Method == "DELETE" {
			t.Error("Abort should not call server after commit")
			w.WriteHeader(http.StatusOK)
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

	tx := &Transaction{
		ID:      "tx-123",
		Version: 42,
		client:  client,
	}

	// Commit first
	_, err = tx.Commit(context.Background())
	require.NoError(t, err)
	assert.True(t, tx.IsCommitted())

	// Abort after commit - should be no-op
	err = tx.Abort(context.Background())
	require.NoError(t, err)
}

func TestTransaction_CommitAfterAbort(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handle version detection
		if r.URL.Path == "/v3/info" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
			return
		}

		// Handle abort
		if r.URL.Path == "/services/haproxy/transactions/tx-123" && r.Method == "DELETE" {
			w.WriteHeader(http.StatusNoContent)
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

	tx := &Transaction{
		ID:      "tx-123",
		Version: 42,
		client:  client,
	}

	// Abort first
	err = tx.Abort(context.Background())
	require.NoError(t, err)
	assert.True(t, tx.IsAborted())

	// Commit after abort - should fail
	result, err := tx.Commit(context.Background())
	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "cannot commit aborted transaction")
}

func TestTransaction_IsCommittedIsAborted(t *testing.T) {
	tx := &Transaction{
		ID:      "tx-123",
		Version: 42,
	}

	// Initially neither committed nor aborted
	assert.False(t, tx.IsCommitted())
	assert.False(t, tx.IsAborted())

	// Set committed manually for testing
	tx.committed = true
	assert.True(t, tx.IsCommitted())
	assert.False(t, tx.IsAborted())

	// Reset and set aborted
	tx.committed = false
	tx.aborted = true
	assert.False(t, tx.IsCommitted())
	assert.True(t, tx.IsAborted())
}

func TestCreateTransaction_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/info":
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
			return
		case "/services/haproxy/transactions":
			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusCreated)
				fmt.Fprintln(w, `{invalid json}`)
				return
			}
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

	tx, err := client.CreateTransaction(context.Background(), 42)
	require.Error(t, err)
	assert.Nil(t, tx)
	assert.Contains(t, err.Error(), "failed to parse transaction response")
}

func TestTransaction_AbortIdempotent(t *testing.T) {
	callCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v3/info" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
			return
		}

		if r.URL.Path == "/services/haproxy/transactions/tx-123" && r.Method == "DELETE" {
			callCount++
			w.WriteHeader(http.StatusNoContent)
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

	tx := &Transaction{
		ID:      "tx-123",
		Version: 42,
		client:  client,
	}

	// First abort
	err = tx.Abort(context.Background())
	require.NoError(t, err)
	assert.True(t, tx.IsAborted())
	assert.Equal(t, 1, callCount)

	// Second abort - should be idempotent (not call server again)
	err = tx.Abort(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, callCount) // Still 1 - didn't call server again
}

func TestTransaction_CommitVersionConflict406WithHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v3/info" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
			return
		}

		if r.URL.Path == "/services/haproxy/transactions/tx-123" && r.Method == "PUT" {
			w.Header().Set("Configuration-Version", "55")
			w.WriteHeader(http.StatusNotAcceptable) // 406
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

	tx := &Transaction{
		ID:      "tx-123",
		Version: 42,
		client:  client,
	}

	result, err := tx.Commit(context.Background())
	require.Error(t, err)
	assert.Nil(t, result)

	var conflictErr *VersionConflictError
	require.ErrorAs(t, err, &conflictErr)
	assert.Equal(t, "55", conflictErr.ActualVersion)
}

func TestTransaction_CommitVersionConflict409WithoutHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v3/info" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
			return
		}

		if r.URL.Path == "/services/haproxy/transactions/tx-123" && r.Method == "PUT" {
			// 409 without Configuration-Version header
			w.WriteHeader(http.StatusConflict)
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

	tx := &Transaction{
		ID:      "tx-123",
		Version: 42,
		client:  client,
	}

	result, err := tx.Commit(context.Background())
	require.Error(t, err)
	assert.Nil(t, result)

	var conflictErr *VersionConflictError
	require.ErrorAs(t, err, &conflictErr)
	assert.Equal(t, "unknown", conflictErr.ActualVersion)
}
