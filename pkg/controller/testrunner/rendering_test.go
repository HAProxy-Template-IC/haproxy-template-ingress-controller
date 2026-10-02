package testrunner

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
)

// TestTestExtraContextLayers pins both layerings: assertions render with
// testExtraContext < _global < per-test and never see the deployment's
// extraContext; the deployment check renders with extraContext < _global <
// per-test.
func TestTestExtraContextLayers(t *testing.T) {
	cfg := &config.Config{
		TemplatingSettings: config.TemplatingSettings{
			ExtraContext:     map[string]any{"marker": "deployment", "deploymentOnly": true},
			TestExtraContext: map[string]any{"marker": "default", "defaultOnly": true},
		},
		ValidationTests: map[string]config.ValidationTest{
			"_global": {ExtraContext: map[string]any{"pinned": "global"}},
		},
	}

	tests := []struct {
		name           string
		testExtra      map[string]any
		wantAssertion  map[string]any
		wantDeployment map[string]any
	}{
		{
			name:           "no per-test values",
			wantAssertion:  map[string]any{"marker": "default", "defaultOnly": true, "pinned": "global"},
			wantDeployment: map[string]any{"marker": "deployment", "deploymentOnly": true, "pinned": "global"},
		},
		{
			name:           "per-test values win in both",
			testExtra:      map[string]any{"marker": "test", "pinned": "test"},
			wantAssertion:  map[string]any{"marker": "test", "defaultOnly": true, "pinned": "test"},
			wantDeployment: map[string]any{"marker": "test", "deploymentOnly": true, "pinned": "test"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			test := &config.ValidationTest{ExtraContext: tt.testExtra}
			assert.Equal(t, tt.wantAssertion, AssertionExtraContext(cfg, test))
			assert.Equal(t, tt.wantDeployment, DeploymentExtraContext(cfg, test))
		})
	}
}

func TestDeepMergeMaps(t *testing.T) {
	tests := []struct {
		name       string
		base       map[string]any
		override   map[string]any
		wantMerged map[string]any
	}{
		{
			name:       "nil override keeps base",
			base:       map[string]any{"a": "1"},
			wantMerged: map[string]any{"a": "1"},
		},
		{
			name:       "scalar override replaces value",
			base:       map[string]any{"a": "1", "b": "2"},
			override:   map[string]any{"b": "3"},
			wantMerged: map[string]any{"a": "1", "b": "3"},
		},
		{
			name: "nested subtree merges instead of clobbering siblings",
			base: map[string]any{
				"tls": map[string]any{
					"defaultCertificate": map[string]any{"namespace": "haptic", "name": "default-ssl-cert"},
					"hsts":               map[string]any{"enabled": false},
				},
			},
			override: map[string]any{
				"tls": map[string]any{
					"hsts": map[string]any{"enabled": true, "preload": true},
				},
			},
			wantMerged: map[string]any{
				"tls": map[string]any{
					"defaultCertificate": map[string]any{"namespace": "haptic", "name": "default-ssl-cert"},
					"hsts":               map[string]any{"enabled": true, "preload": true},
				},
			},
		},
		{
			name:       "map replaces scalar and scalar replaces map",
			base:       map[string]any{"a": "scalar", "b": map[string]any{"k": "v"}},
			override:   map[string]any{"a": map[string]any{"k": "v"}, "b": "scalar"},
			wantMerged: map[string]any{"a": map[string]any{"k": "v"}, "b": "scalar"},
		},
		{
			name: "__replace__ sentinel swaps the subtree wholesale",
			base: map[string]any{
				"waf": map[string]any{
					"policies": map[string]any{
						"inline": map[string]any{"deployment-policy": map[string]any{}},
					},
				},
			},
			override: map[string]any{
				"waf": map[string]any{
					"policies": map[string]any{
						"inline": map[string]any{"__replace__": true, "approved-policy": map[string]any{}},
					},
				},
			},
			wantMerged: map[string]any{
				"waf": map[string]any{
					"policies": map[string]any{
						"inline": map[string]any{"approved-policy": map[string]any{}},
					},
				},
			},
		},
		{
			name:       "__replace__ sentinel is stripped from nested maps too",
			base:       map[string]any{"reg": map[string]any{"old": "x"}},
			override:   map[string]any{"reg": map[string]any{"__replace__": true, "sub": map[string]any{"__replace__": true, "k": "v"}}},
			wantMerged: map[string]any{"reg": map[string]any{"sub": map[string]any{"k": "v"}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseBefore := deepMergeMaps(tt.base, nil)

			assert.Equal(t, tt.wantMerged, deepMergeMaps(tt.base, tt.override))
			// The shared base map must never be mutated (parallel workers).
			assert.Equal(t, baseBefore, tt.base)
		})
	}
}
