package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseConfig_Success(t *testing.T) {
	yamlConfig := `
pod_selector:
  match_labels:
    app: haproxy

controller:
  healthz_port: 8080
  metrics_port: 9090

logging:
  level: INFO

watched_resources:
  ingresses:
    api_version: networking.k8s.io/v1
    kind: Ingress
    index_by: ["metadata.namespace"]

haproxy_config:
  template: |
    global
      log stdout
`

	cfg, err := parseConfig(yamlConfig)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	assert.Equal(t, "haproxy", cfg.PodSelector.MatchLabels["app"])
	assert.Equal(t, 8080, cfg.Controller.HealthzPort)
	assert.Contains(t, cfg.HAProxyConfig.Template, "global")
}

func TestParseConfig_EmptyString(t *testing.T) {
	cfg, err := parseConfig("")
	assert.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "config YAML is empty")
}

func TestParseConfig_InvalidYAML(t *testing.T) {
	yamlConfig := `
pod_selector:
  match_labels:
    app: haproxy
  invalid_indentation
`

	cfg, err := parseConfig(yamlConfig)
	assert.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "failed to unmarshal YAML")
}

func TestParseConfig_PartialConfig(t *testing.T) {
	// Test that parsing works even with minimal config
	// (validation is separate from parsing)
	yamlConfig := `
pod_selector:
  match_labels:
    app: haproxy

watched_resources:
  ingresses:
    api_version: networking.k8s.io/v1
    kind: Ingress
    index_by: ["metadata.namespace"]

haproxy_config:
  template: "global"
`

	cfg, err := parseConfig(yamlConfig)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	// Zero values should be present for unset fields
	assert.Equal(t, 0, cfg.Controller.HealthzPort) // Will be set by defaults
	assert.Equal(t, "", cfg.Logging.Level)
}

func TestParseConfig_ComplexWatchedResources(t *testing.T) {
	yamlConfig := `
pod_selector:
  match_labels:
    app: haproxy

watched_resources:
  ingresses:
    api_version: networking.k8s.io/v1
    kind: Ingress
    enable_validation_webhook: true
    index_by: ["metadata.namespace", "metadata.name"]
  services:
    api_version: v1
    kind: Service
    enable_validation_webhook: false
    index_by: ["metadata.namespace"]
  endpoints:
    api_version: discovery.k8s.io/v1
    kind: EndpointSlice
    index_by: ["metadata.labels['kubernetes.io/service-name']"]

haproxy_config:
  template: "global"
`

	cfg, err := parseConfig(yamlConfig)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	assert.Len(t, cfg.WatchedResources, 3)

	ingress := cfg.WatchedResources["ingresses"]
	assert.True(t, ingress.EnableValidationWebhook)
	assert.Len(t, ingress.IndexBy, 2)

	service := cfg.WatchedResources["services"]
	assert.False(t, service.EnableValidationWebhook)
	assert.Len(t, service.IndexBy, 1)

	endpoints := cfg.WatchedResources["endpoints"]
	assert.Contains(t, endpoints.IndexBy[0], "kubernetes.io/service-name")
}

func TestLoadCredentials_Success(t *testing.T) {
	secretData := map[string][]byte{
		"dataplane_username": []byte("admin"),
		"dataplane_password": []byte("adminpass"),
	}

	creds, err := LoadCredentials(secretData)
	require.NoError(t, err)
	require.NotNil(t, creds)

	assert.Equal(t, "admin", creds.DataplaneUsername)
	assert.Equal(t, "adminpass", creds.DataplanePassword)
}

func TestLoadCredentials_NilData(t *testing.T) {
	creds, err := LoadCredentials(nil)
	assert.Error(t, err)
	assert.Nil(t, creds)
	assert.Contains(t, err.Error(), "secret data is nil")
}

func TestLoadCredentials_MissingDataplaneUsername(t *testing.T) {
	secretData := map[string][]byte{
		"dataplane_password": []byte("adminpass"),
	}

	creds, err := LoadCredentials(secretData)
	assert.Error(t, err)
	assert.Nil(t, creds)
	assert.Contains(t, err.Error(), "dataplane_username")
}

func TestLoadCredentials_MissingDataplanePassword(t *testing.T) {
	secretData := map[string][]byte{
		"dataplane_username": []byte("admin"),
	}

	creds, err := LoadCredentials(secretData)
	assert.Error(t, err)
	assert.Nil(t, creds)
	assert.Contains(t, err.Error(), "dataplane_password")
}

func TestLoadCredentials_EmptyValues(t *testing.T) {
	tests := []struct {
		name     string
		data     map[string][]byte
		errField string
	}{
		{
			name: "empty dataplane_username",
			data: map[string][]byte{
				"dataplane_username": []byte(""),
				"dataplane_password": []byte("adminpass"),
			},
			errField: "dataplane_username",
		},
		{
			name: "empty dataplane_password",
			data: map[string][]byte{
				"dataplane_username": []byte("admin"),
				"dataplane_password": []byte(""),
			},
			errField: "dataplane_password",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			creds, err := LoadCredentials(tt.data)
			assert.Error(t, err)
			assert.Nil(t, creds)
			assert.Contains(t, err.Error(), tt.errField)
		})
	}
}

func TestLoadConfig_Success(t *testing.T) {
	yamlConfig := `
pod_selector:
  match_labels:
    app: haproxy

watched_resources:
  ingresses:
    api_version: networking.k8s.io/v1
    kind: Ingress
    index_by: ["metadata.namespace"]

haproxy_config:
  template: "global"
`

	cfg, err := LoadConfig(yamlConfig)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	// Verify defaults were applied
	assert.Equal(t, DefaultHealthzPort, cfg.Controller.HealthzPort)
	assert.Equal(t, DefaultMetricsPort, cfg.Controller.MetricsPort)
	assert.Equal(t, DefaultDataplanePort, cfg.Dataplane.Port)
}

func TestLoadConfig_EmptyString(t *testing.T) {
	cfg, err := LoadConfig("")
	assert.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "config YAML is empty")
}

func TestLoadConfig_InvalidYAML(t *testing.T) {
	yamlConfig := `
pod_selector:
  invalid_indent
`

	cfg, err := LoadConfig(yamlConfig)
	assert.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "failed to unmarshal YAML")
}

func TestParseConfig_WithAllSections(t *testing.T) {
	yamlConfig := `
pod_selector:
  match_labels:
    app: haproxy

controller:
  healthz_port: 8080
  metrics_port: 9090

logging:
  level: DEBUG

watched_resources_ignore_fields:
  - metadata.managedFields
  - metadata.annotations

watched_resources:
  ingresses:
    api_version: networking.k8s.io/v1
    kind: Ingress
    index_by: ["metadata.namespace"]

template_snippets:
  test-snippet:
    name: test-snippet
    template: "test template"

maps:
  host.map:
    template: "test map"

files:
  404.http:
    template: "test file"

haproxy_config:
  template: "global"
`

	cfg, err := parseConfig(yamlConfig)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	// Verify all sections were parsed
	assert.Equal(t, "haproxy", cfg.PodSelector.MatchLabels["app"])
	assert.Equal(t, 8080, cfg.Controller.HealthzPort)
	assert.Equal(t, "DEBUG", cfg.Logging.Level)
	assert.Len(t, cfg.WatchedResourcesIgnoreFields, 2)
	assert.Len(t, cfg.WatchedResources, 1)
	assert.Len(t, cfg.TemplateSnippets, 1)
	assert.Len(t, cfg.Maps, 1)
	assert.Len(t, cfg.Files, 1)
	assert.NotEmpty(t, cfg.HAProxyConfig.Template)
}
