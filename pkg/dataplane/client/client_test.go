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

func TestNew_Validation(t *testing.T) {
	tests := []struct {
		name      string
		cfg       *Config
		expectErr bool
		errMsg    string
	}{
		{
			name:      "empty baseURL",
			cfg:       &Config{Username: "admin", Password: "pass"},
			expectErr: true,
			errMsg:    "baseURL is required",
		},
		{
			name:      "empty username",
			cfg:       &Config{BaseURL: "http://localhost:5555", Password: "pass"},
			expectErr: true,
			errMsg:    "username is required",
		},
		{
			name:      "empty password",
			cfg:       &Config{BaseURL: "http://localhost:5555", Username: "admin"},
			expectErr: true,
			errMsg:    "password is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := New(context.Background(), tt.cfg)

			if tt.expectErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				assert.Nil(t, client)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, client)
			}
		})
	}
}

func TestNew_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v3/info" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client, err := New(context.Background(), &Config{
		BaseURL:  server.URL,
		Username: "admin",
		Password: "password",
		PodName:  "haproxy-0",
	})

	require.NoError(t, err)
	require.NotNil(t, client)

	// Verify endpoint info
	assert.Equal(t, server.URL, client.Endpoint.URL)
	assert.Equal(t, "admin", client.Endpoint.Username)
	assert.Equal(t, "password", client.Endpoint.Password)
	assert.Equal(t, "haproxy-0", client.Endpoint.PodName)

	// Verify clientset is set
	assert.NotNil(t, client.Clientset())

	// Verify version detection
	assert.Equal(t, "v3.2.6 87ad0bcf", client.DetectedVersion())

	// Verify capabilities
	caps := client.Capabilities()
	assert.True(t, caps.SupportsCrtList)

	// Verify BaseURL
	assert.Equal(t, server.URL, client.BaseURL())
}

func TestNewFromEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v3/info" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"api":{"version":"v3.1.11 def456gh"}}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	endpoint := Endpoint{
		URL:      server.URL,
		Username: "admin",
		Password: "password",
		PodName:  "haproxy-pod",
	}

	client, err := NewFromEndpoint(context.Background(), &endpoint, nil)

	require.NoError(t, err)
	require.NotNil(t, client)

	// Verify endpoint was passed correctly
	assert.Equal(t, server.URL, client.Endpoint.URL)
	assert.Equal(t, "admin", client.Endpoint.Username)
	assert.Equal(t, "haproxy-pod", client.Endpoint.PodName)

	// Verify version detection for v3.1
	assert.Equal(t, "v3.1.11 def456gh", client.DetectedVersion())

	// Verify v3.1 capabilities
	caps := client.Capabilities()
	assert.True(t, caps.SupportsMapStorage)
	assert.False(t, caps.SupportsCrtList) // Only v3.2+
}

func TestDataplaneClient_Clientset(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v3/info" {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, `{"api":{"version":"v3.2.6 87ad0bcf"}}`)
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

	// Verify Clientset() returns non-nil clientset
	clientset := client.Clientset()
	require.NotNil(t, clientset)

	// Verify all version-specific clients are available
	assert.NotNil(t, clientset.V30())
	assert.NotNil(t, clientset.V31())
	assert.NotNil(t, clientset.V32())
}

func TestDataplaneClient_PreferredClient(t *testing.T) {
	tests := []struct {
		name        string
		apiVersion  string
		minorExpect int
	}{
		{
			name:        "v3.2 prefers V32",
			apiVersion:  "v3.2.6 87ad0bcf",
			minorExpect: 2,
		},
		{
			name:        "v3.1 prefers V31",
			apiVersion:  "v3.1.11 def456gh",
			minorExpect: 1,
		},
		{
			name:        "v3.0 prefers V30",
			apiVersion:  "v3.0.15 abc123de",
			minorExpect: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v3/info" {
					w.WriteHeader(http.StatusOK)
					fmt.Fprintf(w, `{"api":{"version":"%s"}}`, tt.apiVersion)
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

			// Verify the correct preferred client is returned
			preferred := client.PreferredClient()
			require.NotNil(t, preferred)

			// Verify minor version matches expected
			assert.Equal(t, tt.minorExpect, client.Clientset().MinorVersion())
		})
	}
}

func TestNew_ServerNotReachable(t *testing.T) {
	// Use an invalid URL that won't connect
	client, err := New(context.Background(), &Config{
		BaseURL:  "http://localhost:1", // Port 1 is unlikely to be listening
		Username: "admin",
		Password: "password",
	})

	require.Error(t, err)
	assert.Nil(t, client)
	assert.Contains(t, err.Error(), "creating clientset")
}

func TestNew_InvalidVersionResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v3/info" {
			w.WriteHeader(http.StatusOK)
			// Empty version - should fail validation
			fmt.Fprintln(w, `{"api":{"version":""}}`)
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

	require.Error(t, err)
	assert.Nil(t, client)
}

func TestNew_UnsupportedMajorVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v3/info" {
			w.WriteHeader(http.StatusOK)
			// v4.x is not supported
			fmt.Fprintln(w, `{"api":{"version":"v4.0.0 abc123de"}}`)
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

	require.Error(t, err)
	assert.Nil(t, client)
	assert.Contains(t, err.Error(), "unsupported DataPlane API major version")
}
