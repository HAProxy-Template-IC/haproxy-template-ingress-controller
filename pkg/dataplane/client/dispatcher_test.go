package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	v30 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v30"
	v31 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v31"
	v32 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32"
)

// mockOKResponse creates a mock HTTP 200 OK response with a closeable body.
func mockOKResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("")),
	}
}

func TestDispatch_RoutesToCorrectVersion(t *testing.T) {
	tests := []struct {
		name          string
		apiVersion    string
		expectV32Call bool
		expectV31Call bool
		expectV30Call bool
	}{
		{
			name:          "v3.2 routes to V32",
			apiVersion:    "v3.2.6 87ad0bcf",
			expectV32Call: true,
		},
		{
			name:          "v3.1 routes to V31",
			apiVersion:    "v3.1.11 def456gh",
			expectV31Call: true,
		},
		{
			name:          "v3.0 routes to V30",
			apiVersion:    "v3.0.15 abc123de",
			expectV30Call: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create test server
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				fmt.Fprintf(w, `{"api":{"version":"%s"}}`, tt.apiVersion)
			}))
			defer server.Close()

			// Create client
			client, err := New(context.Background(), &Config{
				BaseURL:  server.URL,
				Username: "admin",
				Password: "password",
			})
			require.NoError(t, err)

			// Track which version was called
			var v32Called, v31Called, v30Called bool

			resp, err := client.Dispatch(context.Background(), CallFunc[*http.Response]{
				V32: func(c *v32.Client) (*http.Response, error) {
					v32Called = true
					return mockOKResponse(), nil
				},
				V31: func(c *v31.Client) (*http.Response, error) {
					v31Called = true
					return mockOKResponse(), nil
				},
				V30: func(c *v30.Client) (*http.Response, error) {
					v30Called = true
					return mockOKResponse(), nil
				},
			})

			require.NoError(t, err)
			require.NotNil(t, resp)
			defer resp.Body.Close()
			assert.Equal(t, tt.expectV32Call, v32Called, "V32 call mismatch")
			assert.Equal(t, tt.expectV31Call, v31Called, "V31 call mismatch")
			assert.Equal(t, tt.expectV30Call, v30Called, "V30 call mismatch")
		})
	}
}

func TestDispatch_NilFunctionReturnsError(t *testing.T) {
	// Create test server (v3.2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
	}))
	defer server.Close()

	client, err := New(context.Background(), &Config{
		BaseURL:  server.URL,
		Username: "admin",
		Password: "password",
	})
	require.NoError(t, err)

	// Test with nil v3.2 function
	resp, err := client.Dispatch(context.Background(), CallFunc[*http.Response]{
		V32: nil, // Nil function for v3.2
		V31: func(c *v31.Client) (*http.Response, error) {
			return mockOKResponse(), nil
		},
		V30: func(c *v30.Client) (*http.Response, error) {
			return mockOKResponse(), nil
		},
	})
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "not supported by DataPlane API v3.2")
}

func TestDispatchWithCapability_Success(t *testing.T) {
	// Create test server (v3.2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
	}))
	defer server.Close()

	client, err := New(context.Background(), &Config{
		BaseURL:  server.URL,
		Username: "admin",
		Password: "password",
	})
	require.NoError(t, err)

	// Test with capability check that passes
	var capabilityCheckCalled, v32Called bool
	resp, err := client.DispatchWithCapability(context.Background(), CallFunc[*http.Response]{
		V32: func(c *v32.Client) (*http.Response, error) {
			v32Called = true
			return mockOKResponse(), nil
		},
	}, func(caps Capabilities) error {
		capabilityCheckCalled = true
		// Check v3.2 capability
		if !caps.SupportsCrtList {
			return fmt.Errorf("crt-list not supported")
		}
		return nil
	})

	require.NoError(t, err)
	require.NotNil(t, resp)
	defer resp.Body.Close()
	assert.True(t, capabilityCheckCalled, "capability check should have been called")
	assert.True(t, v32Called, "v3.2 function should have been called")
}

func TestDispatchWithCapability_FailsCheck(t *testing.T) {
	// Create test server (v3.0 - no crt-list support)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"api":{"version":"v3.0.15 abc123de"}}`)
	}))
	defer server.Close()

	client, err := New(context.Background(), &Config{
		BaseURL:  server.URL,
		Username: "admin",
		Password: "password",
	})
	require.NoError(t, err)

	// Test with capability check that fails
	var capabilityCheckCalled, v30Called bool
	resp, err := client.DispatchWithCapability(context.Background(), CallFunc[*http.Response]{
		V30: func(c *v30.Client) (*http.Response, error) {
			v30Called = true
			return mockOKResponse(), nil
		},
	}, func(caps Capabilities) error {
		capabilityCheckCalled = true
		// Check v3.2 capability (should fail on v3.0)
		if !caps.SupportsCrtList {
			return fmt.Errorf("crt-list requires DataPlane API v3.2+")
		}
		return nil
	})
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}

	require.Error(t, err)
	assert.True(t, capabilityCheckCalled, "capability check should have been called")
	assert.False(t, v30Called, "v3.0 function should NOT have been called")
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "crt-list requires DataPlane API v3.2+")
}

func TestDispatchGeneric_String(t *testing.T) {
	// Create test server (v3.2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
	}))
	defer server.Close()

	client, err := New(context.Background(), &Config{
		BaseURL:  server.URL,
		Username: "admin",
		Password: "password",
	})
	require.NoError(t, err)

	// Test with string return type
	result, err := DispatchGeneric[string](context.Background(), client.Clientset(), CallFunc[string]{
		V32: func(c *v32.Client) (string, error) {
			return "v3.2 result", nil
		},
		V31: func(c *v31.Client) (string, error) {
			return "v3.1 result", nil
		},
		V30: func(c *v30.Client) (string, error) {
			return "v3.0 result", nil
		},
	})

	require.NoError(t, err)
	assert.Equal(t, "v3.2 result", result)
}

func TestDispatch_NilV31FunctionReturnsError(t *testing.T) {
	// Create test server (v3.1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"api":{"version":"v3.1.11 def456gh"}}`)
	}))
	defer server.Close()

	client, err := New(context.Background(), &Config{
		BaseURL:  server.URL,
		Username: "admin",
		Password: "password",
	})
	require.NoError(t, err)

	// Test with nil v3.1 function
	resp, err := client.Dispatch(context.Background(), CallFunc[*http.Response]{
		V32: func(c *v32.Client) (*http.Response, error) {
			return mockOKResponse(), nil
		},
		V31: nil, // Nil function for v3.1
		V30: func(c *v30.Client) (*http.Response, error) {
			return mockOKResponse(), nil
		},
	})
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "not supported by DataPlane API v3.1")
}

func TestDispatch_NilV30FunctionReturnsError(t *testing.T) {
	// Create test server (v3.0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"api":{"version":"v3.0.15 abc123de"}}`)
	}))
	defer server.Close()

	client, err := New(context.Background(), &Config{
		BaseURL:  server.URL,
		Username: "admin",
		Password: "password",
	})
	require.NoError(t, err)

	// Test with nil v3.0 function
	resp, err := client.Dispatch(context.Background(), CallFunc[*http.Response]{
		V32: func(c *v32.Client) (*http.Response, error) {
			return mockOKResponse(), nil
		},
		V31: func(c *v31.Client) (*http.Response, error) {
			return mockOKResponse(), nil
		},
		V30: nil, // Nil function for v3.0
	})
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "not supported by DataPlane API v3.0")
}

func TestDispatchGeneric_RoutesToAllVersions(t *testing.T) {
	tests := []struct {
		name       string
		apiVersion string
		expected   string
	}{
		{
			name:       "routes to v3.2",
			apiVersion: "v3.2.6 87ad0bcf",
			expected:   "v3.2 result",
		},
		{
			name:       "routes to v3.1",
			apiVersion: "v3.1.11 def456gh",
			expected:   "v3.1 result",
		},
		{
			name:       "routes to v3.0",
			apiVersion: "v3.0.15 abc123de",
			expected:   "v3.0 result",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				fmt.Fprintf(w, `{"api":{"version":"%s"}}`, tt.apiVersion)
			}))
			defer server.Close()

			client, err := New(context.Background(), &Config{
				BaseURL:  server.URL,
				Username: "admin",
				Password: "password",
			})
			require.NoError(t, err)

			result, err := DispatchGeneric[string](context.Background(), client.Clientset(), CallFunc[string]{
				V32: func(c *v32.Client) (string, error) {
					return "v3.2 result", nil
				},
				V31: func(c *v31.Client) (string, error) {
					return "v3.1 result", nil
				},
				V30: func(c *v30.Client) (string, error) {
					return "v3.0 result", nil
				},
			})

			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDispatchGeneric_NilFunctionReturnsError(t *testing.T) {
	tests := []struct {
		name           string
		apiVersion     string
		expectedErrMsg string
	}{
		{
			name:           "nil V32 function",
			apiVersion:     "v3.2.6 87ad0bcf",
			expectedErrMsg: "not supported by DataPlane API v3.2",
		},
		{
			name:           "nil V31 function",
			apiVersion:     "v3.1.11 def456gh",
			expectedErrMsg: "not supported by DataPlane API v3.1",
		},
		{
			name:           "nil V30 function",
			apiVersion:     "v3.0.15 abc123de",
			expectedErrMsg: "not supported by DataPlane API v3.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				fmt.Fprintf(w, `{"api":{"version":"%s"}}`, tt.apiVersion)
			}))
			defer server.Close()

			client, err := New(context.Background(), &Config{
				BaseURL:  server.URL,
				Username: "admin",
				Password: "password",
			})
			require.NoError(t, err)

			// All functions are nil
			result, err := DispatchGeneric[string](context.Background(), client.Clientset(), CallFunc[string]{
				V32: nil,
				V31: nil,
				V30: nil,
			})

			require.Error(t, err)
			assert.Equal(t, "", result)
			assert.Contains(t, err.Error(), tt.expectedErrMsg)
		})
	}
}

func TestDispatchWithCapability_NilCapabilityCheck(t *testing.T) {
	// Create test server (v3.2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
	}))
	defer server.Close()

	client, err := New(context.Background(), &Config{
		BaseURL:  server.URL,
		Username: "admin",
		Password: "password",
	})
	require.NoError(t, err)

	// Test with nil capability check - should proceed without error
	var v32Called bool
	resp, err := client.DispatchWithCapability(context.Background(), CallFunc[*http.Response]{
		V32: func(c *v32.Client) (*http.Response, error) {
			v32Called = true
			return mockOKResponse(), nil
		},
	}, nil) // nil capability check

	require.NoError(t, err)
	require.NotNil(t, resp)
	defer resp.Body.Close()
	assert.True(t, v32Called, "v3.2 function should have been called")
}

func TestDispatchGeneric_WithError(t *testing.T) {
	// Create test server (v3.2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
	}))
	defer server.Close()

	client, err := New(context.Background(), &Config{
		BaseURL:  server.URL,
		Username: "admin",
		Password: "password",
	})
	require.NoError(t, err)

	// Test when the version function returns an error
	expectedErr := fmt.Errorf("test error from v3.2")
	result, err := DispatchGeneric[int64](context.Background(), client.Clientset(), CallFunc[int64]{
		V32: func(c *v32.Client) (int64, error) {
			return 0, expectedErr
		},
	})

	require.Error(t, err)
	assert.Equal(t, int64(0), result)
	assert.Equal(t, expectedErr, err)
}
