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

package rendercontext

import (
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/core/config"
	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/dataplane"
	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/k8s/types"
	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/templating"
)

func TestNewBuilder(t *testing.T) {
	cfg := &config.Config{}
	pathResolver := &templating.PathResolver{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	builder := NewBuilder(cfg, pathResolver, logger)

	require.NotNil(t, builder)
	assert.Equal(t, cfg, builder.config)
	assert.Equal(t, pathResolver, builder.pathResolver)
	assert.Equal(t, logger, builder.logger)
}

func TestBuilder_WithOptions(t *testing.T) {
	cfg := &config.Config{}
	pathResolver := &templating.PathResolver{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	stores := map[string]types.Store{
		"ingresses": &mockStore{},
	}
	haproxyPodStore := &mockStore{}
	capabilities := &dataplane.Capabilities{SupportsWAF: true}

	builder := NewBuilder(
		cfg,
		pathResolver,
		logger,
		WithStores(stores),
		WithHAProxyPodStore(haproxyPodStore),
		WithCapabilities(capabilities),
	)

	assert.NotNil(t, builder.stores)
	assert.Equal(t, 1, len(builder.stores))
	assert.NotNil(t, builder.haproxyPodStore)
	assert.NotNil(t, builder.capabilities)
}

func TestBuilder_Build_BasicContext(t *testing.T) {
	cfg := &config.Config{
		TemplateSnippets: map[string]config.TemplateSnippet{
			"snippet-b": {},
			"snippet-a": {},
		},
	}
	pathResolver := &templating.PathResolver{
		MapsDir: "/etc/haproxy/maps",
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	builder := NewBuilder(cfg, pathResolver, logger)
	ctx, fileRegistry := builder.Build()

	require.NotNil(t, ctx)
	require.NotNil(t, fileRegistry)

	// Check required keys exist
	assert.Contains(t, ctx, "resources")
	assert.Contains(t, ctx, "controller")
	assert.Contains(t, ctx, "templateSnippets")
	assert.Contains(t, ctx, "fileRegistry")
	assert.Contains(t, ctx, "pathResolver")
	assert.Contains(t, ctx, "shared")
	assert.Contains(t, ctx, "runtimeEnvironment")
	assert.Contains(t, ctx, "extraContext")

	// Check snippets are sorted
	snippets := ctx["templateSnippets"].([]string)
	require.Len(t, snippets, 2)
	assert.Equal(t, "snippet-a", snippets[0])
	assert.Equal(t, "snippet-b", snippets[1])
}

func TestBuilder_Build_WithStores(t *testing.T) {
	cfg := &config.Config{}
	pathResolver := &templating.PathResolver{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	stores := map[string]types.Store{
		"ingresses": &mockStore{},
		"services":  &mockStore{},
	}

	builder := NewBuilder(cfg, pathResolver, logger, WithStores(stores))
	ctx, _ := builder.Build()

	resources := ctx["resources"].(map[string]templating.ResourceStore)
	require.Len(t, resources, 2)
	assert.Contains(t, resources, "ingresses")
	assert.Contains(t, resources, "services")
}

func TestBuilder_Build_WithHAProxyPodStore(t *testing.T) {
	cfg := &config.Config{}
	pathResolver := &templating.PathResolver{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	haproxyPodStore := &mockStore{}

	builder := NewBuilder(cfg, pathResolver, logger, WithHAProxyPodStore(haproxyPodStore))
	ctx, _ := builder.Build()

	controller := ctx["controller"].(map[string]templating.ResourceStore)
	require.Len(t, controller, 1)
	assert.Contains(t, controller, "haproxy_pods")
}

func TestBuilder_Build_WithCapabilities(t *testing.T) {
	cfg := &config.Config{}
	pathResolver := &templating.PathResolver{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	capabilities := &dataplane.Capabilities{
		SupportsWAF:   true,
		SupportsHTTP2: true,
	}

	builder := NewBuilder(cfg, pathResolver, logger, WithCapabilities(capabilities))
	ctx, _ := builder.Build()

	caps := ctx["capabilities"].(map[string]interface{})
	assert.True(t, caps["supports_waf"].(bool))
	assert.True(t, caps["supports_http2"].(bool))
	assert.True(t, caps["is_enterprise"].(bool)) // Derived from supports_waf
}

func TestBuilder_Build_WithExtraContext(t *testing.T) {
	cfg := &config.Config{
		TemplatingSettings: config.TemplatingSettings{
			ExtraContext: map[string]interface{}{
				"debug": map[string]interface{}{
					"enabled": true,
				},
				"version": "1.0",
			},
		},
	}
	pathResolver := &templating.PathResolver{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	builder := NewBuilder(cfg, pathResolver, logger)
	ctx, _ := builder.Build()

	// Check extraContext map is populated
	extraContext := ctx["extraContext"].(map[string]interface{})
	assert.Equal(t, "1.0", extraContext["version"])

	// Check values are merged to top level
	assert.Contains(t, ctx, "debug")
	assert.Contains(t, ctx, "version")
}

func TestSortSnippetNames(t *testing.T) {
	tests := []struct {
		name     string
		snippets map[string]config.TemplateSnippet
		want     []string
	}{
		{
			name:     "empty",
			snippets: map[string]config.TemplateSnippet{},
			want:     []string{},
		},
		{
			name: "already sorted",
			snippets: map[string]config.TemplateSnippet{
				"a": {},
				"b": {},
				"c": {},
			},
			want: []string{"a", "b", "c"},
		},
		{
			name: "reverse order",
			snippets: map[string]config.TemplateSnippet{
				"z": {},
				"m": {},
				"a": {},
			},
			want: []string{"a", "m", "z"},
		},
		{
			name: "with priority prefixes",
			snippets: map[string]config.TemplateSnippet{
				"features-100-ssl":     {},
				"features-050-logging": {},
				"features-200-waf":     {},
			},
			want: []string{"features-050-logging", "features-100-ssl", "features-200-waf"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SortSnippetNames(tt.snippets)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMergeExtraContextInto(t *testing.T) {
	t.Run("nil extraContext", func(t *testing.T) {
		cfg := &config.Config{}
		renderCtx := make(map[string]interface{})

		MergeExtraContextInto(renderCtx, cfg)

		// Should create empty extraContext to prevent nil dereference
		assert.Contains(t, renderCtx, "extraContext")
		extraContext := renderCtx["extraContext"].(map[string]interface{})
		assert.Empty(t, extraContext)
	})

	t.Run("with extraContext", func(t *testing.T) {
		cfg := &config.Config{
			TemplatingSettings: config.TemplatingSettings{
				ExtraContext: map[string]interface{}{
					"key1": "value1",
					"key2": 42,
				},
			},
		}
		renderCtx := make(map[string]interface{})

		MergeExtraContextInto(renderCtx, cfg)

		// Check top-level merge
		assert.Equal(t, "value1", renderCtx["key1"])
		assert.Equal(t, 42, renderCtx["key2"])

		// Check extraContext map
		extraContext := renderCtx["extraContext"].(map[string]interface{})
		assert.Equal(t, "value1", extraContext["key1"])
	})
}
