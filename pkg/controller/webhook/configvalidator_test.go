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

package webhook

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/validation"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

// stubProvider is the minimal stores.StoreProvider needed to construct a
// ConfigValidator. The full-pipeline cases are covered by acceptance tests;
// these unit tests exercise structural early-return paths only and never
// reach the renderer / validator, so the provider doesn't need real stores.
type stubProvider struct{}

func (stubProvider) GetStore(string) stores.Store { return nil }
func (stubProvider) StoreNames() []string         { return nil }

func newConfigValidatorForTest(t *testing.T) *ConfigValidator {
	t.Helper()
	return NewConfigValidator(&ConfigValidatorConfig{
		Logger: testutil.NewTestLogger(),
		StrictValidator: validation.NewValidationService(&validation.ValidationServiceConfig{
			Logger: testutil.NewTestLogger(),
		}),
		StoreProvider: stubProvider{},
	})
}

func TestConfigValidator_DELETE_Allowed(t *testing.T) {
	v := newConfigValidatorForTest(t)

	allowed, reason, warnings := v.ValidateDirect(
		context.Background(),
		HAProxyTemplateConfigGVK,
		"haptic",
		"haptic-config",
		nil, // DELETE has no body
		"DELETE",
	)

	assert.True(t, allowed, "DELETE must always be admitted")
	assert.Empty(t, reason)
	assert.Nil(t, warnings)
}

func TestConfigValidator_RejectsNonUnstructured(t *testing.T) {
	v := newConfigValidatorForTest(t)

	allowed, reason, _ := v.ValidateDirect(
		context.Background(),
		HAProxyTemplateConfigGVK,
		"haptic",
		"haptic-config",
		"not an unstructured object", // wrong type
		"CREATE",
	)

	assert.False(t, allowed)
	assert.Contains(t, reason, "expected *unstructured.Unstructured")
}

func TestConfigValidator_RejectsWrongKind(t *testing.T) {
	v := newConfigValidatorForTest(t)

	// Wrong kind — should be rejected by conversion.ParseCRD before any
	// rendering happens.
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion("haproxy-haptic.org/v1alpha1")
	obj.SetKind("HAProxyCfg") // not HAProxyTemplateConfig
	obj.SetNamespace("haptic")
	obj.SetName("haptic-config")

	allowed, reason, _ := v.ValidateDirect(
		context.Background(),
		HAProxyTemplateConfigGVK,
		"haptic",
		"haptic-config",
		obj,
		"CREATE",
	)

	assert.False(t, allowed)
	assert.Contains(t, reason, "parsing HAProxyTemplateConfig")
}

func TestConfigValidator_RejectsWrongAPIVersion(t *testing.T) {
	v := newConfigValidatorForTest(t)

	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion("haproxy-haptic.org/v1beta1") // not v1alpha1
	obj.SetKind("HAProxyTemplateConfig")
	obj.SetNamespace("haptic")
	obj.SetName("haptic-config")

	allowed, reason, _ := v.ValidateDirect(
		context.Background(),
		HAProxyTemplateConfigGVK,
		"haptic",
		"haptic-config",
		obj,
		"CREATE",
	)

	assert.False(t, allowed)
	assert.Contains(t, reason, "parsing HAProxyTemplateConfig")
}

func TestConfigValidator_Constructor_PanicsOnMissingDeps(t *testing.T) {
	t.Run("panics on missing StrictValidator", func(t *testing.T) {
		defer func() {
			r := recover()
			assert.NotNil(t, r, "expected panic on missing StrictValidator")
		}()
		NewConfigValidator(&ConfigValidatorConfig{
			Logger:        testutil.NewTestLogger(),
			StoreProvider: stubProvider{},
		})
	})

	t.Run("panics on missing StoreProvider", func(t *testing.T) {
		defer func() {
			r := recover()
			assert.NotNil(t, r, "expected panic on missing StoreProvider")
		}()
		NewConfigValidator(&ConfigValidatorConfig{
			Logger:          testutil.NewTestLogger(),
			StrictValidator: validation.NewValidationService(&validation.ValidationServiceConfig{}),
		})
	})
}
