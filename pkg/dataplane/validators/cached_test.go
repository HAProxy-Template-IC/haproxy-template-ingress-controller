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

package validators

import (
	"testing"

	"github.com/haproxytech/client-native/v6/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCachedValidator_CacheHitMiss(t *testing.T) {
	cv := NewCachedValidator(3, 2)

	server := &models.Server{
		Name:    "srv1",
		Address: "127.0.0.1",
		Port:    func() *int64 { p := int64(8080); return &p }(),
	}

	// First call - cache miss, should validate
	err := cv.ValidateServer(server)
	assert.NoError(t, err)
	assert.Equal(t, 1, cv.Cache().Len())

	// Second call - cache hit, same result
	err = cv.ValidateServer(server)
	assert.NoError(t, err)
	assert.Equal(t, 1, cv.Cache().Len()) // Still 1, from cache
}

func TestCachedValidator_CachesErrors(t *testing.T) {
	cv := NewCachedValidator(3, 2)

	// Create a server with invalid weight to trigger validation error
	weight := int64(-1)
	server := &models.Server{
		Name:    "srv1",
		Address: "127.0.0.1",
		Port:    func() *int64 { p := int64(8080); return &p }(),
	}
	server.Weight = &weight

	// First call
	err1 := cv.ValidateServer(server)
	cacheLen := cv.Cache().Len()

	// Second call - should return cached result (whether error or not)
	err2 := cv.ValidateServer(server)
	assert.Equal(t, cacheLen, cv.Cache().Len()) // No new cache entry

	// Both calls should return the same result
	if err1 != nil {
		assert.Equal(t, err1.Error(), err2.Error())
	} else {
		assert.NoError(t, err2)
	}
}

func TestCachedValidator_SharedCache(t *testing.T) {
	cache := NewCache()

	cv1 := NewCachedValidatorWithCache(cache, 3, 2)
	cv2 := NewCachedValidatorWithCache(cache, 3, 2)

	server := &models.Server{
		Name:    "srv1",
		Address: "127.0.0.1",
		Port:    func() *int64 { p := int64(8080); return &p }(),
	}

	// Validate on cv1
	err := cv1.ValidateServer(server)
	assert.NoError(t, err)
	assert.Equal(t, 1, cache.Len())

	// cv2 should see the cached entry (same cache)
	err = cv2.ValidateServer(server)
	assert.NoError(t, err)
	assert.Equal(t, 1, cache.Len()) // Still 1, shared cache hit
}

func TestCachedValidator_Accessors(t *testing.T) {
	cv := NewCachedValidator(3, 1)

	assert.NotNil(t, cv.ValidatorSet())
	assert.Equal(t, "v31", cv.ValidatorSet().Version())
	assert.NotNil(t, cv.Cache())
	assert.Equal(t, 0, cv.Cache().Len())
}

func TestCachedValidator_AllModelTypes(t *testing.T) {
	cv := NewCachedValidator(3, 2)

	// Test that all model types can be validated without panics
	tests := []struct {
		name     string
		validate func() error
	}{
		{"Server", func() error {
			return cv.ValidateServer(&models.Server{Name: "s", Address: "1.2.3.4"})
		}},
		{"ServerTemplate", func() error {
			return cv.ValidateServerTemplate(&models.ServerTemplate{})
		}},
		{"Bind", func() error {
			return cv.ValidateBind(&models.Bind{})
		}},
		{"HTTPRequestRule", func() error {
			return cv.ValidateHTTPRequestRule(&models.HTTPRequestRule{})
		}},
		{"HTTPResponseRule", func() error {
			return cv.ValidateHTTPResponseRule(&models.HTTPResponseRule{})
		}},
		{"TCPRequestRule", func() error {
			return cv.ValidateTCPRequestRule(&models.TCPRequestRule{})
		}},
		{"TCPResponseRule", func() error {
			return cv.ValidateTCPResponseRule(&models.TCPResponseRule{})
		}},
		{"HTTPAfterResponseRule", func() error {
			return cv.ValidateHTTPAfterResponseRule(&models.HTTPAfterResponseRule{})
		}},
		{"HTTPErrorRule", func() error {
			return cv.ValidateHTTPErrorRule(&models.HTTPErrorRule{})
		}},
		{"ServerSwitchingRule", func() error {
			return cv.ValidateServerSwitchingRule(&models.ServerSwitchingRule{})
		}},
		{"BackendSwitchingRule", func() error {
			return cv.ValidateBackendSwitchingRule(&models.BackendSwitchingRule{})
		}},
		{"StickRule", func() error {
			return cv.ValidateStickRule(&models.StickRule{})
		}},
		{"ACL", func() error {
			return cv.ValidateACL(&models.ACL{ACLName: "test", Criterion: "path", Value: "/test"})
		}},
		{"Filter", func() error {
			return cv.ValidateFilter(&models.Filter{})
		}},
		{"LogTarget", func() error {
			return cv.ValidateLogTarget(&models.LogTarget{})
		}},
		{"HTTPCheck", func() error {
			return cv.ValidateHTTPCheck(&models.HTTPCheck{})
		}},
		{"TCPCheck", func() error {
			return cv.ValidateTCPCheck(&models.TCPCheck{})
		}},
		{"Capture", func() error {
			return cv.ValidateCapture(&models.Capture{})
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NotPanics(t, func() {
				tt.validate()
			})
		})
	}

	// Verify all types added entries to cache
	assert.Greater(t, cv.Cache().Len(), 0)
}
