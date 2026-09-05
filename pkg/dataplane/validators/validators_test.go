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

//go:build playground

package validators

import (
	"errors"
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

func TestValidatorSetMissingFunctionsFailClosed(t *testing.T) {
	validator := &ValidatorSet{}

	assert.ErrorIs(t, validator.ValidateServer(&models.Server{}), errValidatorUnavailable)
	assert.ErrorIs(t, validator.ValidateServerTemplate(&models.ServerTemplate{}), errValidatorUnavailable)
	assert.ErrorIs(t, validator.ValidateBind(&models.Bind{}), errValidatorUnavailable)
	assert.ErrorIs(t, validator.ValidateHTTPRequestRule(&models.HTTPRequestRule{}), errValidatorUnavailable)
	assert.ErrorIs(t, validator.ValidateHTTPResponseRule(&models.HTTPResponseRule{}), errValidatorUnavailable)
	assert.ErrorIs(t, validator.ValidateTCPRequestRule(&models.TCPRequestRule{}), errValidatorUnavailable)
	assert.ErrorIs(t, validator.ValidateTCPResponseRule(&models.TCPResponseRule{}), errValidatorUnavailable)
	assert.ErrorIs(t, validator.ValidateHTTPAfterResponseRule(&models.HTTPAfterResponseRule{}), errValidatorUnavailable)
	assert.ErrorIs(t, validator.ValidateHTTPErrorRule(&models.HTTPErrorRule{}), errValidatorUnavailable)
	assert.ErrorIs(t, validator.ValidateServerSwitchingRule(&models.ServerSwitchingRule{}), errValidatorUnavailable)
	assert.ErrorIs(t, validator.ValidateBackendSwitchingRule(&models.BackendSwitchingRule{}), errValidatorUnavailable)
	assert.ErrorIs(t, validator.ValidateStickRule(&models.StickRule{}), errValidatorUnavailable)
	assert.ErrorIs(t, validator.ValidateACL(&models.ACL{}), errValidatorUnavailable)
	assert.ErrorIs(t, validator.ValidateFilter(&models.Filter{}), errValidatorUnavailable)
	assert.ErrorIs(t, validator.ValidateLogTarget(&models.LogTarget{}), errValidatorUnavailable)
	assert.ErrorIs(t, validator.ValidateHTTPCheck(&models.HTTPCheck{}), errValidatorUnavailable)
	assert.ErrorIs(t, validator.ValidateTCPCheck(&models.TCPCheck{}), errValidatorUnavailable)
	assert.ErrorIs(t, validator.ValidateCapture(&models.Capture{}), errValidatorUnavailable)
}

func TestValidatorSetValidatesEveryOccurrence(t *testing.T) {
	calls := 0
	validator := &ValidatorSet{validateServer: func(*models.Server) error {
		calls++
		if calls == 2 {
			return errors.New("second occurrence refused")
		}
		return nil
	}}
	model := &models.Server{Name: "same-model"}

	assert.NoError(t, validator.ValidateServer(model))
	assert.EqualError(t, validator.ValidateServer(model), "second occurrence refused")
	assert.NoError(t, validator.ValidateServer(model))
	assert.Equal(t, 3, calls)
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
			validator := ForVersion(v.major, v.minor)
			require.NotNil(t, validator)

			// Valid server should pass
			server := &models.Server{
				Name:    "srv1",
				Address: "127.0.0.1",
				Port:    func() *int64 { p := int64(8080); return &p }(),
			}
			assert.NoError(t, validator.ValidateServer(server))

			// Valid ACL should pass
			acl := &models.ACL{
				ACLName:   "is_api",
				Criterion: "path_beg",
				Value:     "/api",
			}
			assert.NoError(t, validator.ValidateACL(acl))
		})
	}
}

func TestValidatorSet_AllModelTypes(t *testing.T) {
	validator := ForVersion(3, 2)
	tests := []struct {
		name     string
		validate func() error
	}{
		{"Server", func() error { return validator.ValidateServer(&models.Server{Name: "s", Address: "1.2.3.4"}) }},
		{"ServerTemplate", func() error { return validator.ValidateServerTemplate(&models.ServerTemplate{}) }},
		{"Bind", func() error { return validator.ValidateBind(&models.Bind{}) }},
		{"HTTPRequestRule", func() error { return validator.ValidateHTTPRequestRule(&models.HTTPRequestRule{}) }},
		{"HTTPResponseRule", func() error { return validator.ValidateHTTPResponseRule(&models.HTTPResponseRule{}) }},
		{"TCPRequestRule", func() error { return validator.ValidateTCPRequestRule(&models.TCPRequestRule{}) }},
		{"TCPResponseRule", func() error { return validator.ValidateTCPResponseRule(&models.TCPResponseRule{}) }},
		{"HTTPAfterResponseRule", func() error { return validator.ValidateHTTPAfterResponseRule(&models.HTTPAfterResponseRule{}) }},
		{"HTTPErrorRule", func() error { return validator.ValidateHTTPErrorRule(&models.HTTPErrorRule{}) }},
		{"ServerSwitchingRule", func() error { return validator.ValidateServerSwitchingRule(&models.ServerSwitchingRule{}) }},
		{"BackendSwitchingRule", func() error { return validator.ValidateBackendSwitchingRule(&models.BackendSwitchingRule{}) }},
		{"StickRule", func() error { return validator.ValidateStickRule(&models.StickRule{}) }},
		{"ACL", func() error {
			return validator.ValidateACL(&models.ACL{ACLName: "test", Criterion: "path", Value: "/test"})
		}},
		{"Filter", func() error { return validator.ValidateFilter(&models.Filter{}) }},
		{"LogTarget", func() error { return validator.ValidateLogTarget(&models.LogTarget{}) }},
		{"HTTPCheck", func() error { return validator.ValidateHTTPCheck(&models.HTTPCheck{}) }},
		{"TCPCheck", func() error { return validator.ValidateTCPCheck(&models.TCPCheck{}) }},
		{"Capture", func() error { return validator.ValidateCapture(&models.Capture{}) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NotPanics(t, func() { _ = tt.validate() })
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
