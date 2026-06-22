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

func TestForVersion(t *testing.T) {
	tests := []struct {
		name         string
		major, minor int
		expectedSet  *ValidatorSet
	}{
		{"v2.9 maps to v30", 2, 9, validatorSetV30},
		{"v3.0 maps to v30", 3, 0, validatorSetV30},
		{"v3.1 maps to v31", 3, 1, validatorSetV31},
		{"v3.2 maps to v32", 3, 2, validatorSetV32},
		{"v3.3 maps to v33", 3, 3, validatorSetV33},
		{"v4.0 maps to v33", 4, 0, validatorSetV33},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vs := ForVersion(tt.major, tt.minor)
			require.NotNil(t, vs)
			assert.Same(t, tt.expectedSet, vs)
		})
	}
}

func TestValidatorSet_Version(t *testing.T) {
	assert.Same(t, validatorSetV30, ForVersion(3, 0))
	assert.Same(t, validatorSetV31, ForVersion(3, 1))
	assert.Same(t, validatorSetV32, ForVersion(3, 2))
	assert.Same(t, validatorSetV33, ForVersion(3, 3))
}

func TestValidatorSet_NilFunctions(t *testing.T) {
	// A ValidatorSet with all nil function fields means "no validator for this
	// version" — every validation must be treated as valid (no error, no panic).
	// The nil-tolerance is exercised through CachedValidator, whose validateCached
	// guards nil hasher/validator fields inline.
	cv := &CachedValidator{cache: NewCache(), set: &ValidatorSet{}}

	assert.NoError(t, cv.ValidateServer(&models.Server{}))
	assert.NoError(t, cv.ValidateServerTemplate(&models.ServerTemplate{}))
	assert.NoError(t, cv.ValidateBind(&models.Bind{}))
	assert.NoError(t, cv.ValidateHTTPRequestRule(&models.HTTPRequestRule{}))
	assert.NoError(t, cv.ValidateHTTPResponseRule(&models.HTTPResponseRule{}))
	assert.NoError(t, cv.ValidateTCPRequestRule(&models.TCPRequestRule{}))
	assert.NoError(t, cv.ValidateTCPResponseRule(&models.TCPResponseRule{}))
	assert.NoError(t, cv.ValidateHTTPAfterResponseRule(&models.HTTPAfterResponseRule{}))
	assert.NoError(t, cv.ValidateHTTPErrorRule(&models.HTTPErrorRule{}))
	assert.NoError(t, cv.ValidateServerSwitchingRule(&models.ServerSwitchingRule{}))
	assert.NoError(t, cv.ValidateBackendSwitchingRule(&models.BackendSwitchingRule{}))
	assert.NoError(t, cv.ValidateStickRule(&models.StickRule{}))
	assert.NoError(t, cv.ValidateACL(&models.ACL{}))
	assert.NoError(t, cv.ValidateFilter(&models.Filter{}))
	assert.NoError(t, cv.ValidateLogTarget(&models.LogTarget{}))
	assert.NoError(t, cv.ValidateHTTPCheck(&models.HTTPCheck{}))
	assert.NoError(t, cv.ValidateTCPCheck(&models.TCPCheck{}))
	assert.NoError(t, cv.ValidateCapture(&models.Capture{}))

	// Nil validator fields mean nothing is cached either.
	assert.Equal(t, 0, cacheLen(cv.cache))
}

func TestValidatorSet_ValidateWithGeneratedValidators(t *testing.T) {
	// Test that generated validators are properly wired up
	versions := []struct {
		name  string
		major int
		minor int
	}{
		{"v30", 3, 0},
		{"v31", 3, 1},
		{"v32", 3, 2},
	}

	for _, v := range versions {
		t.Run(v.name, func(t *testing.T) {
			cv := NewCachedValidator(v.major, v.minor)
			require.NotNil(t, cv)

			// Valid server should pass
			server := &models.Server{
				Name:    "srv1",
				Address: "127.0.0.1",
				Port:    func() *int64 { p := int64(8080); return &p }(),
			}
			assert.NoError(t, cv.ValidateServer(server))

			// Valid ACL should pass
			acl := &models.ACL{
				ACLName:   "is_api",
				Criterion: "path_beg",
				Value:     "/api",
			}
			assert.NoError(t, cv.ValidateACL(acl))
		})
	}
}

func TestFieldError(t *testing.T) {
	err := &FieldError{
		Field:   "maxconn",
		Message: "must be >= 1",
	}

	assert.Equal(t, "maxconn: must be >= 1", err.Error())
	assert.Contains(t, err.Error(), "maxconn")
	assert.Contains(t, err.Error(), "must be >= 1")
}
