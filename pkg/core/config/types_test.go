package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestConfig_UnmarshalYAML(t *testing.T) {
	yamlConfig := `
pod_selector:
  match_labels:
    app: haproxy
    component: loadbalancer

controller:
  healthz_port: 8080
  metrics_port: 9090

logging:
  level: DEBUG

watched_resources_ignore_fields:
  - metadata.managedFields

watched_resources:
  ingresses:
    api_version: networking.k8s.io/v1
    resources: ingresses
    enable_validation_webhook: true
    index_by: ["metadata.namespace", "metadata.name"]
  services:
    api_version: v1
    resources: services
    enable_validation_webhook: false
    index_by: ["metadata.namespace"]

template_snippets:
  backend-name:
    name: backend-name
    template: |
      ing_{{ ingress.metadata.namespace }}

maps:
  host.map:
    template: |
      {{ rule.host }} {{ rule.host }}

files:
  400.http:
    template: |
      HTTP/1.0 400 Bad Request

haproxy_config:
  template: |
    global
      log stdout
`

	var cfg Config
	err := yaml.Unmarshal([]byte(yamlConfig), &cfg)
	require.NoError(t, err)

	// Validate PodSelector
	assert.Equal(t, "haproxy", cfg.PodSelector.MatchLabels["app"])
	assert.Equal(t, "loadbalancer", cfg.PodSelector.MatchLabels["component"])

	// Validate Controller
	assert.Equal(t, 8080, cfg.Controller.HealthzPort)
	assert.Equal(t, 9090, cfg.Controller.MetricsPort)

	// Validate Logging
	assert.Equal(t, "DEBUG", cfg.Logging.Level)

	// Validate WatchedResourcesIgnoreFields
	assert.Equal(t, []string{"metadata.managedFields"}, cfg.WatchedResourcesIgnoreFields)

	// Validate WatchedResources
	assert.Len(t, cfg.WatchedResources, 2)

	ingress := cfg.WatchedResources["ingresses"]
	assert.Equal(t, "networking.k8s.io/v1", ingress.APIVersion)
	assert.Equal(t, "ingresses", ingress.Resources)
	assert.True(t, ingress.EnableValidationWebhook)
	assert.Equal(t, []string{"metadata.namespace", "metadata.name"}, ingress.IndexBy)

	service := cfg.WatchedResources["services"]
	assert.Equal(t, "v1", service.APIVersion)
	assert.Equal(t, "services", service.Resources)
	assert.False(t, service.EnableValidationWebhook)
	assert.Equal(t, []string{"metadata.namespace"}, service.IndexBy)

	// Validate TemplateSnippets
	assert.Len(t, cfg.TemplateSnippets, 1)
	snippet := cfg.TemplateSnippets["backend-name"]
	assert.Equal(t, "backend-name", snippet.Name)
	assert.Contains(t, snippet.Template, "ing_{{ ingress.metadata.namespace }}")

	// Validate Maps
	assert.Len(t, cfg.Maps, 1)
	hostMap := cfg.Maps["host.map"]
	assert.Contains(t, hostMap.Template, "{{ rule.host }}")

	// Validate Files
	assert.Len(t, cfg.Files, 1)
	errorFile := cfg.Files["400.http"]
	assert.Contains(t, errorFile.Template, "HTTP/1.0 400 Bad Request")

	// Validate HAProxyConfig
	assert.Contains(t, cfg.HAProxyConfig.Template, "global")
}

func TestPodSelector_UnmarshalYAML(t *testing.T) {
	yamlConfig := `
match_labels:
  app: haproxy
  env: production
`

	var ps PodSelector
	err := yaml.Unmarshal([]byte(yamlConfig), &ps)
	require.NoError(t, err)

	assert.Equal(t, "haproxy", ps.MatchLabels["app"])
	assert.Equal(t, "production", ps.MatchLabels["env"])
}

func TestControllerConfig_UnmarshalYAML(t *testing.T) {
	yamlConfig := `
healthz_port: 8081
metrics_port: 9091
`

	var oc ControllerConfig
	err := yaml.Unmarshal([]byte(yamlConfig), &oc)
	require.NoError(t, err)

	assert.Equal(t, 8081, oc.HealthzPort)
	assert.Equal(t, 9091, oc.MetricsPort)
}

func TestLoggingConfig_UnmarshalYAML(t *testing.T) {
	yamlConfig := `
level: INFO
`

	var lc LoggingConfig
	err := yaml.Unmarshal([]byte(yamlConfig), &lc)
	require.NoError(t, err)

	assert.Equal(t, "INFO", lc.Level)
}

func TestWatchedResource_UnmarshalYAML(t *testing.T) {
	yamlConfig := `
api_version: networking.k8s.io/v1
resources: ingresses
enable_validation_webhook: true
index_by: ["metadata.namespace", "metadata.name"]
`

	var wr WatchedResource
	err := yaml.Unmarshal([]byte(yamlConfig), &wr)
	require.NoError(t, err)

	assert.Equal(t, "networking.k8s.io/v1", wr.APIVersion)
	assert.Equal(t, "ingresses", wr.Resources)
	assert.True(t, wr.EnableValidationWebhook)
	assert.Equal(t, []string{"metadata.namespace", "metadata.name"}, wr.IndexBy)
}

func TestTemplateSnippet_UnmarshalYAML(t *testing.T) {
	yamlConfig := `
name: test-snippet
template: |
  test template content
  line 2
`

	var ts TemplateSnippet
	err := yaml.Unmarshal([]byte(yamlConfig), &ts)
	require.NoError(t, err)

	assert.Equal(t, "test-snippet", ts.Name)
	assert.Contains(t, ts.Template, "test template content")
	assert.Contains(t, ts.Template, "line 2")
}

func TestMapFile_UnmarshalYAML(t *testing.T) {
	yamlConfig := `
template: |
  host.example.com backend_example
`

	var mf MapFile
	err := yaml.Unmarshal([]byte(yamlConfig), &mf)
	require.NoError(t, err)

	assert.Contains(t, mf.Template, "host.example.com backend_example")
}

func TestGeneralFile_UnmarshalYAML(t *testing.T) {
	yamlConfig := `
template: |
  HTTP/1.0 404 Not Found
`

	var gf GeneralFile
	err := yaml.Unmarshal([]byte(yamlConfig), &gf)
	require.NoError(t, err)

	assert.Contains(t, gf.Template, "HTTP/1.0 404 Not Found")
}

func TestHAProxyConfig_UnmarshalYAML(t *testing.T) {
	yamlConfig := `
template: |
  global
    log stdout
  defaults
    mode http
`

	var hc HAProxyConfig
	err := yaml.Unmarshal([]byte(yamlConfig), &hc)
	require.NoError(t, err)

	assert.Contains(t, hc.Template, "global")
	assert.Contains(t, hc.Template, "defaults")
}
