package testrunner

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

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
