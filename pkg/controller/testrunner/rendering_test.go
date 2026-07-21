package testrunner

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
)

// TestApplyTestExtraContext pins the helper the benchmark path (cmd/controller)
// uses to render each test with the same isolated baseline as the load gate:
// production < _global < per-test, applied to an already-built render context.
func TestApplyTestExtraContext(t *testing.T) {
	cfgWithGlobal := &config.Config{
		ValidationTests: map[string]config.ValidationTest{
			"_global": {ExtraContext: map[string]any{"marker": "global"}},
		},
	}
	cfgNoGlobal := &config.Config{ValidationTests: map[string]config.ValidationTest{}}

	tests := []struct {
		name      string
		cfg       *config.Config
		testExtra map[string]any
		want      string
	}{
		{"no _global and no per-test keeps production", cfgNoGlobal, nil, "production"},
		{"_global baseline overrides production", cfgWithGlobal, nil, "global"},
		{"per-test overrides the _global baseline", cfgWithGlobal, map[string]any{"marker": "pertest"}, "pertest"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			renderCtx := map[string]any{"extraContext": map[string]any{"marker": "production"}}
			ApplyTestExtraContext(renderCtx, tt.cfg, tt.testExtra)
			got := renderCtx["extraContext"].(map[string]any)["marker"]
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMergeTestExtraContext(t *testing.T) {
	tests := []struct {
		name       string
		global     map[string]any
		test       map[string]any
		wantMerged map[string]any
	}{
		{
			name:       "nil test context leaves global untouched",
			global:     map[string]any{"a": "1"},
			test:       nil,
			wantMerged: map[string]any{"a": "1"},
		},
		{
			name:       "scalar override replaces value",
			global:     map[string]any{"a": "1", "b": "2"},
			test:       map[string]any{"b": "3"},
			wantMerged: map[string]any{"a": "1", "b": "3"},
		},
		{
			name: "nested subtree merges instead of clobbering siblings",
			global: map[string]any{
				"tls": map[string]any{
					"defaultCertificate": map[string]any{"namespace": "haptic", "name": "default-ssl-cert"},
					"hsts":               map[string]any{"enabled": false},
				},
			},
			test: map[string]any{
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
			global:     map[string]any{"a": "scalar", "b": map[string]any{"k": "v"}},
			test:       map[string]any{"a": map[string]any{"k": "v"}, "b": "scalar"},
			wantMerged: map[string]any{"a": map[string]any{"k": "v"}, "b": "scalar"},
		},
		{
			name: "__replace__ sentinel swaps the subtree wholesale",
			global: map[string]any{
				"waf": map[string]any{
					"policies": map[string]any{
						"inline": map[string]any{"deployment-policy": map[string]any{}},
					},
				},
			},
			test: map[string]any{
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
			global:     map[string]any{"reg": map[string]any{"old": "x"}},
			test:       map[string]any{"reg": map[string]any{"__replace__": true, "sub": map[string]any{"__replace__": true, "k": "v"}}},
			wantMerged: map[string]any{"reg": map[string]any{"sub": map[string]any{"k": "v"}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			renderCtx := map[string]any{"extraContext": tt.global}
			globalBefore := deepMergeMaps(tt.global, nil) // snapshot copy

			mergeTestExtraContext(renderCtx, tt.test)

			assert.Equal(t, tt.wantMerged, renderCtx["extraContext"])
			// Per-test keys are mirrored at the top level with their merged values.
			for key := range tt.test {
				assert.Equal(t, tt.wantMerged[key], renderCtx[key])
			}
			// The shared global map must never be mutated (parallel workers).
			assert.Equal(t, globalBefore, tt.global)
		})
	}
}
