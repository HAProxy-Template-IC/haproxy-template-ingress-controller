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
		name            string
		major, minor    int
		expectedVersion string
	}{
		{"v2.9 maps to v30", 2, 9, "v30"},
		{"v3.0 maps to v30", 3, 0, "v30"},
		{"v3.1 maps to v31", 3, 1, "v31"},
		{"v3.2 maps to v32", 3, 2, "v32"},
		{"v3.3 maps to v32", 3, 3, "v32"},
		{"v4.0 maps to v32", 4, 0, "v32"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vs := ForVersion(tt.major, tt.minor)
			require.NotNil(t, vs)
			assert.Equal(t, tt.expectedVersion, vs.Version())
		})
	}
}

func TestValidatorSet_Version(t *testing.T) {
	assert.Equal(t, "v30", ForVersion(3, 0).Version())
	assert.Equal(t, "v31", ForVersion(3, 1).Version())
	assert.Equal(t, "v32", ForVersion(3, 2).Version())
}

func TestValidatorSet_NilFunctions(t *testing.T) {
	// A ValidatorSet with all nil functions should return nil/0 for all operations
	vs := &ValidatorSet{version: "test"}

	assert.NoError(t, vs.ValidateServer(&models.Server{}))
	assert.NoError(t, vs.ValidateServerTemplate(&models.ServerTemplate{}))
	assert.NoError(t, vs.ValidateBind(&models.Bind{}))
	assert.NoError(t, vs.ValidateHTTPRequestRule(&models.HTTPRequestRule{}))
	assert.NoError(t, vs.ValidateHTTPResponseRule(&models.HTTPResponseRule{}))
	assert.NoError(t, vs.ValidateTCPRequestRule(&models.TCPRequestRule{}))
	assert.NoError(t, vs.ValidateTCPResponseRule(&models.TCPResponseRule{}))
	assert.NoError(t, vs.ValidateHTTPAfterResponseRule(&models.HTTPAfterResponseRule{}))
	assert.NoError(t, vs.ValidateHTTPErrorRule(&models.HTTPErrorRule{}))
	assert.NoError(t, vs.ValidateServerSwitchingRule(&models.ServerSwitchingRule{}))
	assert.NoError(t, vs.ValidateBackendSwitchingRule(&models.BackendSwitchingRule{}))
	assert.NoError(t, vs.ValidateStickRule(&models.StickRule{}))
	assert.NoError(t, vs.ValidateACL(&models.ACL{}))
	assert.NoError(t, vs.ValidateFilter(&models.Filter{}))
	assert.NoError(t, vs.ValidateLogTarget(&models.LogTarget{}))
	assert.NoError(t, vs.ValidateHTTPCheck(&models.HTTPCheck{}))
	assert.NoError(t, vs.ValidateTCPCheck(&models.TCPCheck{}))
	assert.NoError(t, vs.ValidateCapture(&models.Capture{}))

	assert.Equal(t, uint64(0), vs.HashServer(&models.Server{}))
	assert.Equal(t, uint64(0), vs.HashBind(&models.Bind{}))
	assert.Equal(t, uint64(0), vs.HashACL(&models.ACL{}))
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
			vs := ForVersion(v.major, v.minor)
			require.NotNil(t, vs)

			// Valid server should pass
			server := &models.Server{
				Name:    "srv1",
				Address: "127.0.0.1",
				Port:    func() *int64 { p := int64(8080); return &p }(),
			}
			assert.NoError(t, vs.ValidateServer(server))

			// Valid ACL should pass
			acl := &models.ACL{
				ACLName:   "is_api",
				Criterion: "path_beg",
				Value:     "/api",
			}
			assert.NoError(t, vs.ValidateACL(acl))
		})
	}
}

func TestValidatorSet_Hash(t *testing.T) {
	vs := ForVersion(3, 2)

	// Same content should produce same hash
	server1 := &models.Server{Name: "srv1", Address: "10.0.0.1"}
	server2 := &models.Server{Name: "srv1", Address: "10.0.0.1"}
	assert.Equal(t, vs.HashServer(server1), vs.HashServer(server2))

	// Different content should produce different hash
	server3 := &models.Server{Name: "srv2", Address: "10.0.0.2"}
	assert.NotEqual(t, vs.HashServer(server1), vs.HashServer(server3))

	// Hash should be non-zero for real validators
	assert.NotEqual(t, uint64(0), vs.HashServer(server1))
}

func TestValidationError(t *testing.T) {
	err := &ValidationError{
		Field:   "maxconn",
		Message: "must be >= 1",
	}

	assert.Equal(t, "maxconn: must be >= 1", err.Error())
	assert.Contains(t, err.Error(), "maxconn")
	assert.Contains(t, err.Error(), "must be >= 1")
}
