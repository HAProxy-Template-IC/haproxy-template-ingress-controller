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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/validation"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
)

// stubServedChecker satisfies coreconfig.ServedVersionChecker with a fixed
// predicate — the availability signal ResolveEffective strips against.
type stubServedChecker struct {
	servedFn func(apiVersion, resources string) bool
}

func (s stubServedChecker) IsServed(apiVersion, resources string) bool {
	return s.servedFn(apiVersion, resources)
}

// effectiveTestConfigCRD builds an unstructured HAProxyTemplateConfig whose
// spec mirrors the issue #79 shape: an OPTIONAL gateways watched resource and
// a snippet gated on it whose body only compiles if the requires-stripping
// ran (the identifier is undefined otherwise). haproxy.cfg pulls the snippet
// the way base.yaml does — via render_glob, which renders empty when the
// snippet was stripped.
func effectiveTestConfigCRD() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "haproxy-haptic.org/v1alpha1",
		"kind":       "HAProxyTemplateConfig",
		"metadata":   map[string]any{"name": "haptic-config", "namespace": "haptic"},
		"spec": map[string]any{
			"credentialsSecretRef": map[string]any{"name": "creds"},
			"podSelector":          map[string]any{"matchLabels": map[string]any{"app": "haproxy"}},
			"watchedResources": map[string]any{
				"gateways": map[string]any{
					"apiVersion": "gateway.networking.k8s.io/v1",
					"resources":  "gateways",
					"optional":   true,
					"indexBy":    []any{"metadata.namespace", "metadata.name"},
				},
			},
			"templateSnippets": map[string]any{
				"gw-smoke": map[string]any{
					"requires": []any{"gateways"},
					"template": "{{ definitely_undefined_identifier }}",
				},
			},
			"haproxyConfig": map[string]any{
				"template": "global\n  daemon\n\ndefaults\n  mode http\n  timeout connect 5s\n  timeout client 30s\n  timeout server 30s\n{{ render_glob \"gw-*\" }}\n",
			},
		},
	}}
}

// TestConfigValidator_ValidateDirect_EffectiveResolution pins issue #79: the
// admission gate must judge the EFFECTIVE config (requires-unsatisfied
// features stripped against live availability, exactly like the load gate),
// not the raw proposed spec. Pre-fix, a cluster without Gateway API CRDs
// denied every config update because the chart's gateway snippets failed
// compilation.
func TestConfigValidator_ValidateDirect_EffectiveResolution(t *testing.T) {
	newValidator := func(resolver func(ctx context.Context, cfg *coreconfig.Config) (*coreconfig.Config, error)) *ConfigValidator {
		return NewConfigValidator(&ConfigValidatorConfig{
			Logger: testutil.NewTestLogger(),
			StrictValidator: validation.NewValidationService(&validation.ValidationServiceConfig{
				Logger: testutil.NewTestLogger(),
			}),
			StoreProvider:     stubProvider{},
			EffectiveResolver: resolver,
		})
	}

	resolverWith := func(served func(apiVersion, resources string) bool) func(ctx context.Context, cfg *coreconfig.Config) (*coreconfig.Config, error) {
		return func(_ context.Context, cfg *coreconfig.Config) (*coreconfig.Config, error) {
			effective, _, err := coreconfig.ResolveEffective(cfg, stubServedChecker{servedFn: served}, nil)
			return effective, err
		}
	}

	t.Run("nil resolver compiles the raw config (pre-fix behavior pin)", func(t *testing.T) {
		v := newValidator(nil)
		allowed, reason, _ := v.ValidateDirect(context.Background(), "haproxy-haptic.org/v1alpha1/HAProxyTemplateConfig",
			"haptic", "haptic-config", effectiveTestConfigCRD(), "UPDATE")
		require.False(t, allowed, "raw config must fail compilation (the requires-gated snippet is undefined)")
		assert.Contains(t, reason, "gw-smoke", "the denial must name the un-stripped snippet")
	})

	t.Run("gateways unserved strips the snippet and admits (the #79 fix)", func(t *testing.T) {
		v := newValidator(resolverWith(func(apiVersion, _ string) bool {
			return !strings.HasPrefix(apiVersion, "gateway.networking.k8s.io/")
		}))
		allowed, reason, _ := v.ValidateDirect(context.Background(), "haproxy-haptic.org/v1alpha1/HAProxyTemplateConfig",
			"haptic", "haptic-config", effectiveTestConfigCRD(), "UPDATE")
		assert.True(t, allowed, "stripped config must validate cleanly, got denial: %s", reason)
	})

	t.Run("gateways served keeps the snippet and denies", func(t *testing.T) {
		v := newValidator(resolverWith(func(_, _ string) bool { return true }))
		allowed, reason, _ := v.ValidateDirect(context.Background(), "haproxy-haptic.org/v1alpha1/HAProxyTemplateConfig",
			"haptic", "haptic-config", effectiveTestConfigCRD(), "UPDATE")
		require.False(t, allowed, "with gateways served the snippet is kept and must fail compilation")
		assert.Contains(t, reason, "gw-smoke", "stripping must be availability-gated, not unconditional")
	})

	t.Run("resolver error admits with a warning, never compiles raw", func(t *testing.T) {
		v := newValidator(func(_ context.Context, _ *coreconfig.Config) (*coreconfig.Config, error) {
			return nil, errors.New("discovery blip")
		})
		allowed, reason, warnings := v.ValidateDirect(context.Background(), "haproxy-haptic.org/v1alpha1/HAProxyTemplateConfig",
			"haptic", "haptic-config", effectiveTestConfigCRD(), "UPDATE")
		assert.True(t, allowed, "resolver failure must fail open, got denial: %s", reason)
		require.Len(t, warnings, 1)
		assert.Contains(t, warnings[0], "load gate", "the warning must point at the enforcing gate")
	})
}
