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

package helpers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestNewEngineFromConfig_ScriggoDefault(t *testing.T) {
	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: "global\n    daemon",
		},
		TemplatingSettings: config.TemplatingSettings{
			Engine: "", // Empty defaults to Scriggo
		},
	}

	engine, err := NewEngineFromConfig(cfg, nil, nil)

	require.NoError(t, err)
	require.NotNil(t, engine)
	assert.Equal(t, templating.EngineTypeScriggo, engine.EngineType())
}

func TestNewEngineFromConfig_Scriggo(t *testing.T) {
	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: "global\n    daemon",
		},
		TemplatingSettings: config.TemplatingSettings{
			Engine: "scriggo",
		},
	}

	engine, err := NewEngineFromConfig(cfg, nil, nil)

	require.NoError(t, err)
	require.NotNil(t, engine)
	assert.Equal(t, templating.EngineTypeScriggo, engine.EngineType())
	assert.True(t, engine.HasTemplate("haproxy.cfg"))
}

func TestNewEngineFromConfig_InvalidEngineType(t *testing.T) {
	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: "global\n    daemon",
		},
		TemplatingSettings: config.TemplatingSettings{
			Engine: "invalid-engine",
		},
	}

	engine, err := NewEngineFromConfig(cfg, nil, nil)

	require.Error(t, err)
	assert.Nil(t, engine)
	assert.Contains(t, err.Error(), "invalid template engine type")
	assert.Contains(t, err.Error(), "invalid-engine")
}

func TestNewEngineFromConfig_WithGlobalFunctions(t *testing.T) {
	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: "{{ custom_func() }}",
		},
		TemplatingSettings: config.TemplatingSettings{
			Engine: "scriggo",
		},
	}

	customFuncs := map[string]templating.GlobalFunc{
		"custom_func": func(args ...interface{}) (interface{}, error) {
			return "custom_output", nil
		},
	}

	engine, err := NewEngineFromConfig(cfg, customFuncs, nil)

	require.NoError(t, err)
	require.NotNil(t, engine)

	output, err := engine.Render(context.Background(), "haproxy.cfg", nil)
	require.NoError(t, err)
	// Scriggo adds trailing newline
	assert.Equal(t, "custom_output\n", output)
}

func TestNewEngineFromConfig_InvalidTemplate(t *testing.T) {
	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: "{% if unclosed",
		},
		TemplatingSettings: config.TemplatingSettings{
			Engine: "scriggo",
		},
	}

	engine, err := NewEngineFromConfig(cfg, nil, nil)

	require.Error(t, err)
	assert.Nil(t, engine)
	// Error comes directly from templating.New (CompilationError) without double wrapping
	assert.Contains(t, err.Error(), "failed to compile template")
}

func TestExtractTemplatesFromConfig_MainTemplate(t *testing.T) {
	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: "global\n    daemon",
		},
	}

	templates := ExtractTemplatesFromConfig(cfg)

	assert.Len(t, templates.AllTemplates, 1)
	assert.Equal(t, "global\n    daemon", templates.AllTemplates["haproxy.cfg"])
}

func TestExtractTemplatesFromConfig_AllTypes(t *testing.T) {
	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			Template: "main template",
		},
		TemplateSnippets: map[string]config.TemplateSnippet{
			"snippet1": {Template: "snippet content 1"},
			"snippet2": {Template: "snippet content 2"},
		},
		Maps: map[string]config.MapFile{
			"hosts.map": {Template: "host1 backend1"},
		},
		Files: map[string]config.GeneralFile{
			"error.html": {Template: "<html>Error</html>"},
		},
		SSLCertificates: map[string]config.SSLCertificate{
			"cert.pem": {Template: "certificate content"},
		},
	}

	templates := ExtractTemplatesFromConfig(cfg)

	assert.Len(t, templates.AllTemplates, 6)
	assert.Equal(t, "main template", templates.AllTemplates["haproxy.cfg"])
	assert.Equal(t, "snippet content 1", templates.AllTemplates["snippet1"])
	assert.Equal(t, "snippet content 2", templates.AllTemplates["snippet2"])
	assert.Equal(t, "host1 backend1", templates.AllTemplates["hosts.map"])
	assert.Equal(t, "<html>Error</html>", templates.AllTemplates["error.html"])
	assert.Equal(t, "certificate content", templates.AllTemplates["cert.pem"])
}

func TestExtractTemplatesFromConfig_Empty(t *testing.T) {
	cfg := &config.Config{}

	templates := ExtractTemplatesFromConfig(cfg)

	// Always includes haproxy.cfg, even if empty
	assert.Len(t, templates.AllTemplates, 1)
	assert.Equal(t, "", templates.AllTemplates["haproxy.cfg"])
}

func TestNewEngineFromConfig_FiltersRegistered(t *testing.T) {
	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			// Test glob_match filter - use static list since Scriggo needs globals
			Template: `{% var hosts = []string{"api.example.com", "web.example.com", "other.test.org"} %}{% for _, item := range glob_match(hosts, "*.example.com") %}{{ item }} {% end %}`,
		},
		TemplatingSettings: config.TemplatingSettings{
			Engine: "scriggo",
		},
	}

	engine, err := NewEngineFromConfig(cfg, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render(context.Background(), "haproxy.cfg", nil)
	require.NoError(t, err)
	// Verify both matching hosts are present (whitespace handling can vary)
	assert.Contains(t, output, "api.example.com")
	assert.Contains(t, output, "web.example.com")
	assert.NotContains(t, output, "other.test.org")
}

func TestNewEngineFromConfig_B64DecodeFilterRegistered(t *testing.T) {
	cfg := &config.Config{
		HAProxyConfig: config.HAProxyConfig{
			// Test b64decode filter - "dGVzdA==" is base64 for "test"
			Template: `{{ b64decode("dGVzdA==") }}`,
		},
		TemplatingSettings: config.TemplatingSettings{
			Engine: "scriggo",
		},
	}

	engine, err := NewEngineFromConfig(cfg, nil, nil)
	require.NoError(t, err)

	output, err := engine.Render(context.Background(), "haproxy.cfg", nil)
	require.NoError(t, err)
	// Scriggo adds trailing newline
	assert.Equal(t, "test\n", output)
}
