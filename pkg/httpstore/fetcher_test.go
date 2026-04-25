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
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAddAuthHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com", nil)

	tests := []struct {
		name        string
		auth        *AuthConfig
		wantHeaders map[string]string
		wantMissing []string
	}{
		{
			name: "basic with username and password",
			auth: &AuthConfig{Type: "basic", Username: "alice", Password: "s3cret"},
			wantHeaders: map[string]string{
				"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:s3cret")),
			},
		},
		{
			name: "basic with username only",
			auth: &AuthConfig{Type: "basic", Username: "alice"},
			wantHeaders: map[string]string{
				"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:")),
			},
		},
		{
			name: "basic with password only",
			auth: &AuthConfig{Type: "basic", Password: "pw"},
			wantHeaders: map[string]string{
				"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(":pw")),
			},
		},
		{
			name:        "basic with neither sets nothing",
			auth:        &AuthConfig{Type: "basic"},
			wantMissing: []string{"Authorization"},
		},
		{
			name: "bearer with token",
			auth: &AuthConfig{Type: "bearer", Token: "mytoken"},
			wantHeaders: map[string]string{
				"Authorization": "Bearer mytoken",
			},
		},
		{
			name:        "bearer without token sets nothing",
			auth:        &AuthConfig{Type: "bearer"},
			wantMissing: []string{"Authorization"},
		},
		{
			name: "header type sets all headers",
			auth: &AuthConfig{
				Type: "header",
				Headers: map[string]string{
					"X-API-Key":  "abc123",
					"X-Tenant":   "acme",
				},
			},
			wantHeaders: map[string]string{
				"X-Api-Key": "abc123",
				"X-Tenant":  "acme",
			},
		},
		{
			name: "unknown type falls back to custom headers",
			auth: &AuthConfig{
				Type:    "custom",
				Headers: map[string]string{"X-API-Key": "abc"},
			},
			wantHeaders: map[string]string{"X-Api-Key": "abc"},
		},
		{
			name: "unknown type with no headers does nothing",
			auth: &AuthConfig{Type: "unknown"},
			wantMissing: []string{"Authorization", "X-Api-Key"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := req.Clone(req.Context())
			addAuthHeaders(r, tt.auth)
			for k, want := range tt.wantHeaders {
				assert.Equal(t, want, r.Header.Get(k), "header %s", k)
			}
			for _, k := range tt.wantMissing {
				assert.Equal(t, "", r.Header.Get(k), "header %s should be absent", k)
			}
		})
	}
}

// TestAddAuthHeaders_DoesNotOverwriteCustomHeaderForBasicAndBearer verifies
// that the basic/bearer paths don't accidentally write to a header conflict.
func TestAddAuthHeaders_OverwriteSameHeaderTwice(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
	req.Header.Set("Authorization", "Original")

	addAuthHeaders(req, &AuthConfig{Type: "bearer", Token: "newtoken"})

	require.Equal(t, "Bearer newtoken", req.Header.Get("Authorization"))
}
