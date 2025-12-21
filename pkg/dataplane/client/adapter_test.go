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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewVersionAdapter(t *testing.T) {
	tests := []struct {
		name           string
		maxRetries     int
		wantMaxRetries int
	}{
		{
			name:           "positive max retries",
			maxRetries:     5,
			wantMaxRetries: 5,
		},
		{
			name:           "zero max retries defaults to 3",
			maxRetries:     0,
			wantMaxRetries: 3,
		},
		{
			name:           "negative max retries defaults to 3",
			maxRetries:     -1,
			wantMaxRetries: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a minimal client (nil is fine for this test)
			adapter := NewVersionAdapter(nil, tt.maxRetries)

			require.NotNil(t, adapter)
			assert.Equal(t, tt.wantMaxRetries, adapter.maxRetries)
		})
	}
}

func TestParseVersionFromHeader(t *testing.T) {
	tests := []struct {
		name        string
		header      string
		wantVersion int64
		wantErr     bool
		errContains string
	}{
		{
			name:        "valid version",
			header:      "123",
			wantVersion: 123,
			wantErr:     false,
		},
		{
			name:        "zero version",
			header:      "0",
			wantVersion: 0,
			wantErr:     false,
		},
		{
			name:        "large version",
			header:      "9223372036854775807", // max int64
			wantVersion: 9223372036854775807,
			wantErr:     false,
		},
		{
			name:        "negative version",
			header:      "-1",
			wantVersion: -1,
			wantErr:     false,
		},
		{
			name:        "empty header",
			header:      "",
			wantVersion: 0,
			wantErr:     true,
			errContains: "empty version header",
		},
		{
			name:        "invalid format - letters",
			header:      "abc",
			wantVersion: 0,
			wantErr:     true,
			errContains: "invalid version header",
		},
		{
			name:        "invalid format - mixed",
			header:      "123abc",
			wantVersion: 0,
			wantErr:     true,
			errContains: "invalid version header",
		},
		{
			name:        "invalid format - float",
			header:      "123.45",
			wantVersion: 0,
			wantErr:     true,
			errContains: "invalid version header",
		},
		{
			name:        "invalid format - spaces",
			header:      " 123 ",
			wantVersion: 0,
			wantErr:     true,
			errContains: "invalid version header",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version, err := ParseVersionFromHeader(tt.header)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantVersion, version)
		})
	}
}

func TestExecuteTransaction_Success(t *testing.T) {
	transactionStarted := int32(0)
	transactionCommitted := int32(0)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/info":
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
		case "/services/haproxy/configuration/version":
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, "42")
		case "/services/haproxy/transactions":
			if r.Method == http.MethodPost {
				atomic.AddInt32(&transactionStarted, 1)
				w.WriteHeader(http.StatusCreated)
				fmt.Fprintln(w, `{"id": "tx-123", "status": "in_progress", "_version": 42}`)
				return
			}
			w.WriteHeader(http.StatusMethodNotAllowed)
		case "/services/haproxy/transactions/tx-123":
			if r.Method == http.MethodPut {
				atomic.AddInt32(&transactionCommitted, 1)
				w.Header().Set("Configuration-Version", "43")
				w.WriteHeader(http.StatusAccepted)
				return
			}
			w.WriteHeader(http.StatusMethodNotAllowed)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := New(context.Background(), &Config{
		BaseURL:  server.URL,
		Username: "admin",
		Password: "password",
	})
	require.NoError(t, err)

	adapter := NewVersionAdapter(client, 3)
	result, err := adapter.ExecuteTransaction(context.Background(), func(ctx context.Context, tx *Transaction) error {
		// Simulate a successful operation
		return nil
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int32(1), atomic.LoadInt32(&transactionStarted))
	assert.Equal(t, int32(1), atomic.LoadInt32(&transactionCommitted))
}

func TestExecuteTransaction_OperationFails(t *testing.T) {
	transactionAborted := int32(0)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/info":
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
		case "/services/haproxy/configuration/version":
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, "42")
		case "/services/haproxy/transactions":
			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusCreated)
				fmt.Fprintln(w, `{"id": "tx-123", "status": "in_progress", "_version": 42}`)
				return
			}
			w.WriteHeader(http.StatusMethodNotAllowed)
		case "/services/haproxy/transactions/tx-123":
			if r.Method == http.MethodDelete {
				atomic.AddInt32(&transactionAborted, 1)
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.WriteHeader(http.StatusMethodNotAllowed)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := New(context.Background(), &Config{
		BaseURL:  server.URL,
		Username: "admin",
		Password: "password",
	})
	require.NoError(t, err)

	adapter := NewVersionAdapter(client, 3)
	_, err = adapter.ExecuteTransaction(context.Background(), func(ctx context.Context, tx *Transaction) error {
		return fmt.Errorf("operation failed")
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "operation failed")
	assert.Equal(t, int32(1), atomic.LoadInt32(&transactionAborted))
}

func TestExecuteTransaction_GetVersionFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/info":
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
		case "/services/haproxy/configuration/version":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := New(context.Background(), &Config{
		BaseURL:  server.URL,
		Username: "admin",
		Password: "password",
	})
	require.NoError(t, err)

	adapter := NewVersionAdapter(client, 3)
	_, err = adapter.ExecuteTransaction(context.Background(), func(ctx context.Context, tx *Transaction) error {
		return nil
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get version")
}

func TestExecuteTransactionWithVersion_Success(t *testing.T) {
	transactionStarted := int32(0)
	transactionCommitted := int32(0)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/info":
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
		case "/services/haproxy/transactions":
			if r.Method == http.MethodPost {
				atomic.AddInt32(&transactionStarted, 1)
				w.WriteHeader(http.StatusCreated)
				fmt.Fprintln(w, `{"id": "tx-123", "status": "in_progress", "_version": 42}`)
				return
			}
			w.WriteHeader(http.StatusMethodNotAllowed)
		case "/services/haproxy/transactions/tx-123":
			if r.Method == http.MethodPut {
				atomic.AddInt32(&transactionCommitted, 1)
				w.Header().Set("Configuration-Version", "43")
				w.WriteHeader(http.StatusAccepted)
				return
			}
			w.WriteHeader(http.StatusMethodNotAllowed)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := New(context.Background(), &Config{
		BaseURL:  server.URL,
		Username: "admin",
		Password: "password",
	})
	require.NoError(t, err)

	adapter := NewVersionAdapter(client, 3)
	err = adapter.ExecuteTransactionWithVersion(context.Background(), 42, func(ctx context.Context, tx *Transaction) error {
		// Simulate a successful operation
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, int32(1), atomic.LoadInt32(&transactionStarted))
	assert.Equal(t, int32(1), atomic.LoadInt32(&transactionCommitted))
}

func TestExecuteTransactionWithVersion_OperationFails(t *testing.T) {
	transactionAborted := int32(0)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/info":
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
		case "/services/haproxy/transactions":
			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusCreated)
				fmt.Fprintln(w, `{"id": "tx-123", "status": "in_progress", "_version": 42}`)
				return
			}
			w.WriteHeader(http.StatusMethodNotAllowed)
		case "/services/haproxy/transactions/tx-123":
			if r.Method == http.MethodDelete {
				atomic.AddInt32(&transactionAborted, 1)
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.WriteHeader(http.StatusMethodNotAllowed)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := New(context.Background(), &Config{
		BaseURL:  server.URL,
		Username: "admin",
		Password: "password",
	})
	require.NoError(t, err)

	adapter := NewVersionAdapter(client, 3)
	err = adapter.ExecuteTransactionWithVersion(context.Background(), 42, func(ctx context.Context, tx *Transaction) error {
		return fmt.Errorf("operation failed")
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "operation failed")
	assert.Equal(t, int32(1), atomic.LoadInt32(&transactionAborted))
}

func TestExecuteTransaction_VersionConflictOnCreateTransaction(t *testing.T) {
	transactionAttempts := int32(0)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/info":
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
		case "/services/haproxy/configuration/version":
			w.WriteHeader(http.StatusOK)
			// Return different version each time
			fmt.Fprintf(w, "%d", 42+atomic.LoadInt32(&transactionAttempts))
		case "/services/haproxy/transactions":
			if r.Method == http.MethodPost {
				attempt := atomic.AddInt32(&transactionAttempts, 1)
				if attempt < 3 {
					// Version conflict on first two attempts
					w.Header().Set("Configuration-Version", fmt.Sprintf("%d", 42+attempt))
					w.WriteHeader(http.StatusConflict)
					return
				}
				// Success on third attempt
				w.WriteHeader(http.StatusCreated)
				fmt.Fprintln(w, `{"id": "tx-123", "status": "in_progress", "_version": 45}`)
				return
			}
			w.WriteHeader(http.StatusMethodNotAllowed)
		case "/services/haproxy/transactions/tx-123":
			if r.Method == http.MethodPut {
				w.Header().Set("Configuration-Version", "46")
				w.WriteHeader(http.StatusAccepted)
				return
			}
			w.WriteHeader(http.StatusMethodNotAllowed)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := New(context.Background(), &Config{
		BaseURL:  server.URL,
		Username: "admin",
		Password: "password",
	})
	require.NoError(t, err)

	adapter := NewVersionAdapter(client, 3)
	result, err := adapter.ExecuteTransaction(context.Background(), func(ctx context.Context, tx *Transaction) error {
		return nil
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int32(3), atomic.LoadInt32(&transactionAttempts))
}

func TestExecuteTransaction_VersionConflictOnCommit(t *testing.T) {
	transactionAttempts := int32(0)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/info":
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
		case "/services/haproxy/configuration/version":
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "%d", 42+atomic.LoadInt32(&transactionAttempts))
		case "/services/haproxy/transactions":
			if r.Method == http.MethodPost {
				atomic.AddInt32(&transactionAttempts, 1)
				w.WriteHeader(http.StatusCreated)
				fmt.Fprintf(w, `{"id": "tx-%d", "status": "in_progress"}`, atomic.LoadInt32(&transactionAttempts))
				return
			}
			w.WriteHeader(http.StatusMethodNotAllowed)
		default:
			// Handle commit for any transaction ID
			if r.Method == http.MethodPut {
				attempt := atomic.LoadInt32(&transactionAttempts)
				if attempt < 3 {
					// Version conflict on first two commits
					w.Header().Set("Configuration-Version", fmt.Sprintf("%d", 42+attempt))
					w.WriteHeader(http.StatusConflict)
					return
				}
				// Success on third commit
				w.Header().Set("Configuration-Version", "46")
				w.WriteHeader(http.StatusAccepted)
				return
			}
			if r.Method == http.MethodDelete {
				// Abort - always succeeds
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := New(context.Background(), &Config{
		BaseURL:  server.URL,
		Username: "admin",
		Password: "password",
	})
	require.NoError(t, err)

	adapter := NewVersionAdapter(client, 3)
	result, err := adapter.ExecuteTransaction(context.Background(), func(ctx context.Context, tx *Transaction) error {
		return nil
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, int32(3), atomic.LoadInt32(&transactionAttempts))
}

func TestExecuteTransaction_MaxRetriesExceeded(t *testing.T) {
	transactionAttempts := int32(0)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/info":
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
		case "/services/haproxy/configuration/version":
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "%d", 42+atomic.LoadInt32(&transactionAttempts))
		case "/services/haproxy/transactions":
			if r.Method == http.MethodPost {
				atomic.AddInt32(&transactionAttempts, 1)
				// Always conflict
				w.Header().Set("Configuration-Version", "99")
				w.WriteHeader(http.StatusConflict)
				return
			}
			w.WriteHeader(http.StatusMethodNotAllowed)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := New(context.Background(), &Config{
		BaseURL:  server.URL,
		Username: "admin",
		Password: "password",
	})
	require.NoError(t, err)

	adapter := NewVersionAdapter(client, 2) // Only 2 retries
	_, err = adapter.ExecuteTransaction(context.Background(), func(ctx context.Context, tx *Transaction) error {
		return nil
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "retries")
	assert.Equal(t, int32(3), atomic.LoadInt32(&transactionAttempts)) // Initial + 2 retries
}

func TestExecuteTransaction_CreateTransactionNonVersionError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/info":
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
		case "/services/haproxy/configuration/version":
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, "42")
		case "/services/haproxy/transactions":
			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprintln(w, "internal server error")
				return
			}
			w.WriteHeader(http.StatusMethodNotAllowed)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := New(context.Background(), &Config{
		BaseURL:  server.URL,
		Username: "admin",
		Password: "password",
	})
	require.NoError(t, err)

	adapter := NewVersionAdapter(client, 3)
	_, err = adapter.ExecuteTransaction(context.Background(), func(ctx context.Context, tx *Transaction) error {
		return nil
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create transaction")
}

func TestExecuteTransaction_CommitNonVersionError(t *testing.T) {
	transactionAborted := int32(0)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/info":
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
		case "/services/haproxy/configuration/version":
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, "42")
		case "/services/haproxy/transactions":
			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusCreated)
				fmt.Fprintln(w, `{"id": "tx-123", "status": "in_progress", "_version": 42}`)
				return
			}
			w.WriteHeader(http.StatusMethodNotAllowed)
		case "/services/haproxy/transactions/tx-123":
			if r.Method == http.MethodPut {
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprintln(w, "internal server error")
				return
			}
			if r.Method == http.MethodDelete {
				atomic.AddInt32(&transactionAborted, 1)
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.WriteHeader(http.StatusMethodNotAllowed)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := New(context.Background(), &Config{
		BaseURL:  server.URL,
		Username: "admin",
		Password: "password",
	})
	require.NoError(t, err)

	adapter := NewVersionAdapter(client, 3)
	_, err = adapter.ExecuteTransaction(context.Background(), func(ctx context.Context, tx *Transaction) error {
		return nil
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to commit transaction")
	assert.Equal(t, int32(1), atomic.LoadInt32(&transactionAborted))
}

func TestExecuteTransactionWithVersion_RetryWithNewVersion(t *testing.T) {
	transactionAttempts := int32(0)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/info":
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
		case "/services/haproxy/configuration/version":
			w.WriteHeader(http.StatusOK)
			// Return fresh version on retry
			fmt.Fprintf(w, "%d", 50+atomic.LoadInt32(&transactionAttempts))
		case "/services/haproxy/transactions":
			if r.Method == http.MethodPost {
				attempt := atomic.AddInt32(&transactionAttempts, 1)
				if attempt < 3 {
					w.Header().Set("Configuration-Version", fmt.Sprintf("%d", 50+attempt))
					w.WriteHeader(http.StatusConflict)
					return
				}
				w.WriteHeader(http.StatusCreated)
				fmt.Fprintln(w, `{"id": "tx-123", "status": "in_progress"}`)
				return
			}
			w.WriteHeader(http.StatusMethodNotAllowed)
		case "/services/haproxy/transactions/tx-123":
			if r.Method == http.MethodPut {
				w.Header().Set("Configuration-Version", "55")
				w.WriteHeader(http.StatusAccepted)
				return
			}
			w.WriteHeader(http.StatusMethodNotAllowed)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := New(context.Background(), &Config{
		BaseURL:  server.URL,
		Username: "admin",
		Password: "password",
	})
	require.NoError(t, err)

	adapter := NewVersionAdapter(client, 3)
	err = adapter.ExecuteTransactionWithVersion(context.Background(), 42, func(ctx context.Context, tx *Transaction) error {
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, int32(3), atomic.LoadInt32(&transactionAttempts))
}

func TestExecuteTransactionWithVersion_MaxRetriesExceeded(t *testing.T) {
	transactionAttempts := int32(0)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/info":
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
		case "/services/haproxy/configuration/version":
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "%d", 42+atomic.LoadInt32(&transactionAttempts))
		case "/services/haproxy/transactions":
			if r.Method == http.MethodPost {
				atomic.AddInt32(&transactionAttempts, 1)
				w.Header().Set("Configuration-Version", "99")
				w.WriteHeader(http.StatusConflict)
				return
			}
			w.WriteHeader(http.StatusMethodNotAllowed)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := New(context.Background(), &Config{
		BaseURL:  server.URL,
		Username: "admin",
		Password: "password",
	})
	require.NoError(t, err)

	adapter := NewVersionAdapter(client, 2) // Only 2 retries
	err = adapter.ExecuteTransactionWithVersion(context.Background(), 42, func(ctx context.Context, tx *Transaction) error {
		return nil
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "retries")
}

func TestExecuteTransactionWithVersion_GetVersionFailsOnRetry(t *testing.T) {
	transactionAttempts := int32(0)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/info":
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
		case "/services/haproxy/configuration/version":
			attempt := atomic.LoadInt32(&transactionAttempts)
			if attempt > 0 {
				// Fail on retry
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, "42")
		case "/services/haproxy/transactions":
			if r.Method == http.MethodPost {
				atomic.AddInt32(&transactionAttempts, 1)
				// First attempt fails with version conflict
				w.Header().Set("Configuration-Version", "99")
				w.WriteHeader(http.StatusConflict)
				return
			}
			w.WriteHeader(http.StatusMethodNotAllowed)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := New(context.Background(), &Config{
		BaseURL:  server.URL,
		Username: "admin",
		Password: "password",
	})
	require.NoError(t, err)

	adapter := NewVersionAdapter(client, 3)
	err = adapter.ExecuteTransactionWithVersion(context.Background(), 42, func(ctx context.Context, tx *Transaction) error {
		return nil
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get version on retry")
}

func TestExecuteTransactionWithVersion_CommitVersionConflict(t *testing.T) {
	transactionAttempts := int32(0)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/info":
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
		case "/services/haproxy/configuration/version":
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "%d", 42+atomic.LoadInt32(&transactionAttempts))
		case "/services/haproxy/transactions":
			if r.Method == http.MethodPost {
				atomic.AddInt32(&transactionAttempts, 1)
				w.WriteHeader(http.StatusCreated)
				fmt.Fprintf(w, `{"id": "tx-%d", "status": "in_progress"}`, atomic.LoadInt32(&transactionAttempts))
				return
			}
			w.WriteHeader(http.StatusMethodNotAllowed)
		default:
			if r.Method == http.MethodPut {
				attempt := atomic.LoadInt32(&transactionAttempts)
				if attempt < 3 {
					w.Header().Set("Configuration-Version", fmt.Sprintf("%d", 42+attempt))
					w.WriteHeader(http.StatusConflict)
					return
				}
				w.Header().Set("Configuration-Version", "46")
				w.WriteHeader(http.StatusAccepted)
				return
			}
			if r.Method == http.MethodDelete {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := New(context.Background(), &Config{
		BaseURL:  server.URL,
		Username: "admin",
		Password: "password",
	})
	require.NoError(t, err)

	adapter := NewVersionAdapter(client, 3)
	err = adapter.ExecuteTransactionWithVersion(context.Background(), 42, func(ctx context.Context, tx *Transaction) error {
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, int32(3), atomic.LoadInt32(&transactionAttempts))
}

func TestExecuteTransactionWithVersion_CommitNonVersionError(t *testing.T) {
	transactionAborted := int32(0)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/info":
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
		case "/services/haproxy/configuration/version":
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, "42")
		case "/services/haproxy/transactions":
			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusCreated)
				fmt.Fprintln(w, `{"id": "tx-123", "status": "in_progress"}`)
				return
			}
			w.WriteHeader(http.StatusMethodNotAllowed)
		case "/services/haproxy/transactions/tx-123":
			if r.Method == http.MethodPut {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			if r.Method == http.MethodDelete {
				atomic.AddInt32(&transactionAborted, 1)
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.WriteHeader(http.StatusMethodNotAllowed)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := New(context.Background(), &Config{
		BaseURL:  server.URL,
		Username: "admin",
		Password: "password",
	})
	require.NoError(t, err)

	adapter := NewVersionAdapter(client, 3)
	err = adapter.ExecuteTransactionWithVersion(context.Background(), 42, func(ctx context.Context, tx *Transaction) error {
		return nil
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to commit transaction")
	assert.Equal(t, int32(1), atomic.LoadInt32(&transactionAborted))
}

func TestExecuteTransactionWithVersion_CreateTransactionNonVersionError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/info":
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
		case "/services/haproxy/transactions":
			if r.Method == http.MethodPost {
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprintln(w, "internal server error")
				return
			}
			w.WriteHeader(http.StatusMethodNotAllowed)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := New(context.Background(), &Config{
		BaseURL:  server.URL,
		Username: "admin",
		Password: "password",
	})
	require.NoError(t, err)

	adapter := NewVersionAdapter(client, 3)
	err = adapter.ExecuteTransactionWithVersion(context.Background(), 42, func(ctx context.Context, tx *Transaction) error {
		return nil
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create transaction")
}
