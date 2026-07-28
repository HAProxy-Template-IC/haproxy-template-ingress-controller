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
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/validation"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/validator"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
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

func TestValidationTestsAdmissionBudget(t *testing.T) {
	t.Run("uses suite-size-scaled load-gate budget without parent deadline", func(t *testing.T) {
		assert.Equal(t, validator.SuiteRunBudget(316), validationTestsAdmissionBudget(context.Background(), 316))
	})

	t.Run("caps suite budget to remaining configurable admission deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		budget := validationTestsAdmissionBudget(ctx, 316)
		assert.Positive(t, budget)
		assert.LessOrEqual(t, budget, 5*time.Second)
		assert.Less(t, budget, validator.SuiteRunBudget(316))
	})

	t.Run("expired admission deadline produces an immediately expired budget", func(t *testing.T) {
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()

		assert.LessOrEqual(t, validationTestsAdmissionBudget(ctx, 1), time.Duration(0))
	})
}

// TestConfigValidator_resolveSchemas pins the per-admission bootstrap parity:
// when a live bootstrapper is wired, admission derives its declarations/types
// from the PROSPECTIVE config (matching the daemon load gate); a bootstrap
// failure degrades to the startup-fixed wiring rather than blinding the gate.
func TestConfigValidator_resolveSchemas(t *testing.T) {
	cfg := &coreconfig.Config{HAProxyConfig: coreconfig.HAProxyConfig{Template: "frontend x\n  bind *:80\n"}}

	newValidator := func(c *ConfigValidatorConfig) *ConfigValidator {
		c.Logger = testutil.NewTestLogger()
		c.StrictValidator = validation.NewValidationService(&validation.ValidationServiceConfig{Logger: testutil.NewTestLogger()})
		c.StoreProvider = stubProvider{}
		return NewConfigValidator(c)
	}

	t.Run("nil bootstrap returns startup-fixed wiring", func(t *testing.T) {
		startupDecls := map[string]any{"sentinel": 1}
		startupTypes := map[string]reflect.Type{"svc": reflect.TypeOf(struct{}{})}
		v := newValidator(&ConfigValidatorConfig{Declarations: startupDecls, TypedResourceTypes: startupTypes})

		decls, types := v.resolveSchemas(context.Background(), cfg, "haptic", "haptic-config")
		assert.Equal(t, startupDecls, decls)
		assert.Equal(t, startupTypes, types)
	})

	t.Run("bootstrap success uses prospective-config schemas", func(t *testing.T) {
		var gotCfg *coreconfig.Config
		sentinelType := reflect.TypeOf(0)
		v := newValidator(&ConfigValidatorConfig{
			Declarations: map[string]any{"stale": true}, // must NOT be returned on success
			Bootstrap: func(_ context.Context, c *coreconfig.Config) (*typebootstrap.Result, error) {
				gotCfg = c
				return &typebootstrap.Result{
					Types:  map[string]reflect.Type{"widget": sentinelType},
					Kinds:  map[string]string{},
					Errors: map[string]error{},
				}, nil
			},
		})

		decls, types := v.resolveSchemas(context.Background(), cfg, "haptic", "haptic-config")
		assert.Same(t, cfg, gotCfg, "bootstrap must receive the prospective config")
		assert.Equal(t, sentinelType, types["widget"], "must use the bootstrapped types")
		assert.NotContains(t, decls, "stale", "must not fall back to startup declarations on success")
	})

	t.Run("bootstrap error falls back to startup-fixed wiring", func(t *testing.T) {
		startupDecls := map[string]any{"sentinel": 1}
		startupTypes := map[string]reflect.Type{"svc": reflect.TypeOf(struct{}{})}
		called := false
		v := newValidator(&ConfigValidatorConfig{
			Declarations:       startupDecls,
			TypedResourceTypes: startupTypes,
			Bootstrap: func(_ context.Context, _ *coreconfig.Config) (*typebootstrap.Result, error) {
				called = true
				return nil, errors.New("schema fetch failed")
			},
		})

		decls, types := v.resolveSchemas(context.Background(), cfg, "haptic", "haptic-config")
		assert.True(t, called, "bootstrap must be attempted")
		assert.Equal(t, startupDecls, decls, "must fall back to startup declarations on bootstrap error")
		assert.Equal(t, startupTypes, types)
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

// TestConfigValidator_deferTemplateFailureOnSkew pins the version-skew decision:
// admit-with-warning ONLY when both versions are present and differ, using plain
// inequality so it works for snapshot builds. Deny (no defer) otherwise.
func TestConfigValidator_deferTemplateFailureOnSkew(t *testing.T) {
	tests := []struct {
		name                 string
		runningConfigVersion string
		crVersion            string
		wantDeferred         bool
	}{
		{"skew: differing release versions defer", "0.2.0", "0.3.0", true},
		{"steady state: matching versions deny", "0.2.0", "0.2.0", false},
		{"no running config version denies", "", "0.3.0", false},
		{"no config label (hand-authored CR) denies", "0.2.0", "", false},
		{"snapshot skew: differing shas defer", "0.0.0-main.gaaaaaaa", "0.0.0-main.gbbbbbbb", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &ConfigValidator{logger: testutil.NewTestLogger(), runningConfigVersion: tt.runningConfigVersion}
			warnings, deferred := v.deferTemplateFailureOnSkew(tt.crVersion, "boom", "haptic", "cfg")
			assert.Equal(t, tt.wantDeferred, deferred)
			if tt.wantDeferred {
				if assert.Len(t, warnings, 1) {
					assert.Contains(t, warnings[0], tt.crVersion)
					assert.Contains(t, warnings[0], tt.runningConfigVersion)
				}
			} else {
				assert.Nil(t, warnings)
			}
		})
	}
}

// TestConfigValidator_SkewDefersTemplateFailure proves the wiring at BOTH
// deferral sites — engine compile (undefined function) and render (fail() at
// render time): a template failure is admitted-with-warning during a rolling
// upgrade (version-skewed CR) but still denied in steady state, so early typo
// detection is preserved while an upgrade adding a new builtin isn't blocked.
func TestConfigValidator_SkewDefersTemplateFailure(t *testing.T) {
	newValidator := func(version string) *ConfigValidator {
		return NewConfigValidator(&ConfigValidatorConfig{
			Logger:               testutil.NewTestLogger(),
			StrictValidator:      validation.NewValidationService(&validation.ValidationServiceConfig{Logger: testutil.NewTestLogger()}),
			StoreProvider:        stubProvider{},
			RunningConfigVersion: version,
		})
	}
	crWithTemplate := func(template, versionLabel string) *unstructured.Unstructured {
		obj := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "haproxy-haptic.org/v1alpha1",
			"kind":       "HAProxyTemplateConfig",
			"metadata": map[string]any{
				"namespace": "haptic",
				"name":      "haptic-config",
			},
			// A real prospective config carries these; the validator now runs
			// the structural completeness gate (the requirements the CRD schema
			// gave up when a single object of a merged set became legitimately
			// incomplete) before it ever compiles a template.
			"spec": map[string]any{
				"podSelector": map[string]any{"matchLabels": map[string]any{"app": "haproxy"}},
				"watchedResources": map[string]any{
					"namespaces": map[string]any{"apiVersion": "v1", "resources": "namespaces"},
				},
				"haproxyConfig": map[string]any{"template": template},
			},
		}}
		if versionLabel != "" {
			obj.SetLabels(map[string]string{AppVersionLabel: versionLabel})
		}
		return obj
	}

	// Compile failure: an undefined function fails Scriggo compilation at engine
	// construction — the same error class as the real `undefined: randBytes`
	// rolling-upgrade incident. Render failure: fail() compiles but aborts the
	// render, exercising the second (PhaseRender) deferral site.
	templates := map[string]string{
		"compile": "frontend x\n  bind *:80\n{{ definitelyUndefinedFn() }}\n",
		"render":  "frontend x\n  bind *:80\n{{ fail(\"render-time boom\") }}\n",
	}

	for kind, template := range templates {
		t.Run(kind+": version skew admits the failure with a warning", func(t *testing.T) {
			v := newValidator("0.2.0")
			allowed, reason, warnings := v.ValidateDirect(context.Background(),
				HAProxyTemplateConfigGVK, "haptic", "haptic-config", crWithTemplate(template, "0.3.0"), "CREATE")
			assert.True(t, allowed, "a %s failure during version skew must be admitted (deferred to load gate)", kind)
			assert.Empty(t, reason)
			if assert.Len(t, warnings, 1) {
				assert.Contains(t, warnings[0], "rolling upgrade")
			}
		})

		t.Run(kind+": matching version still denies the failure", func(t *testing.T) {
			v := newValidator("0.2.0")
			allowed, reason, _ := v.ValidateDirect(context.Background(),
				HAProxyTemplateConfigGVK, "haptic", "haptic-config", crWithTemplate(template, "0.2.0"), "CREATE")
			assert.False(t, allowed, "in steady state a %s failure must still deny (early detection preserved)", kind)
			assert.NotEmpty(t, reason)
		})

		t.Run(kind+": no version label denies (hand-authored CR)", func(t *testing.T) {
			v := newValidator("0.2.0")
			allowed, _, _ := v.ValidateDirect(context.Background(),
				HAProxyTemplateConfigGVK, "haptic", "haptic-config", crWithTemplate(template, ""), "CREATE")
			assert.False(t, allowed)
		})
	}
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
