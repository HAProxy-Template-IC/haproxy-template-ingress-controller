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

//go:build acceptance

package acceptance

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	haproxyv1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
)

// InitialConfigYAML is the initial controller configuration.
const InitialConfigYAML = `
pod_selector:
  match_labels:
    app: haproxy
    component: loadbalancer

haproxy_config:
  template: |
    global
      maxconn 2000
      # Initial config - version 1

    defaults
      mode http
      timeout connect 5000ms
      timeout client 50000ms
      timeout server 50000ms

    frontend test-frontend
      bind :8080
      default_backend test-backend

    backend test-backend
      # Placeholder backend - no servers configured

watched_resources:
  ingresses:
    api_version: networking.k8s.io/v1
    kind: Ingress
    resources: ingresses
    index_by:
      - metadata.namespace
      - metadata.name
`

// UpdatedConfigYAML is the updated controller configuration.
const UpdatedConfigYAML = `
pod_selector:
  match_labels:
    app: haproxy
    component: loadbalancer

haproxy_config:
  template: |
    global
      maxconn 4000
      # Updated config - version 2

    defaults
      mode http
      timeout connect 10000ms
      timeout client 100000ms
      timeout server 100000ms

    frontend test-frontend
      bind :8080
      default_backend test-backend

    backend test-backend
      # Placeholder backend - no servers configured

watched_resources:
  ingresses:
    api_version: networking.k8s.io/v1
    kind: Ingress
    resources: ingresses
    index_by:
      - metadata.namespace
      - metadata.name
`

// HAConfigWithLeaderElectionYAML is the controller configuration with leader election enabled.
const HAConfigWithLeaderElectionYAML = `
pod_selector:
  match_labels:
    app: haproxy
    component: loadbalancer

controller:
  leader_election:
    enabled: true
    lease_name: haptic-leader
    lease_duration: 60s
    renew_deadline: 15s
    retry_period: 5s

haproxy_config:
  template: |
    global
      maxconn 2000

    defaults
      mode http
      timeout connect 5000ms
      timeout client 50000ms
      timeout server 50000ms

    frontend test-frontend
      bind :8080
      {% for _, ingress := range resources.ingresses.List() -%}
      {%- var rules = ingress | dig("spec", "rules") | fallback([]any{}) %}
      {%- for _, rule := range rules %}
      {%- var paths = rule | dig("http", "paths") | fallback([]any{}) %}
      {%- for _, path := range paths %}
      {%- var service = path | dig("backend", "service") %}
      {%- if service != nil %}
      {%- var ns = ingress | dig("metadata", "namespace") | fallback("") %}
      {%- var name = ingress | dig("metadata", "name") | fallback("") %}
      {%- var pathVal = path | dig("path") | fallback("/") %}
      use_backend ing_{{ ns }}_{{ name }} if { path_beg {{ pathVal }} }
      {% end %}
      {% end %}
      {% end %}
      {% end %}
      default_backend test-backend

    {%- for _, ingress := range resources.ingresses.List() %}
    {%- var rules = ingress | dig("spec", "rules") | fallback([]any{}) %}
    {%- for _, rule := range rules %}
    {%- var paths = rule | dig("http", "paths") | fallback([]any{}) %}
    {%- for _, path := range paths %}
    {%- var service = path | dig("backend", "service") %}
    {%- if service != nil %}
    {%- var ns = ingress | dig("metadata", "namespace") | fallback("") %}
    {%- var name = ingress | dig("metadata", "name") | fallback("") %}

    backend ing_{{ ns }}_{{ name }}
      server test-server 127.0.0.1:8080
    {% end %}
    {% end %}
    {% end %}
    {% end %}

    backend test-backend
      # Placeholder backend - no servers configured

watched_resources:
  ingresses:
    api_version: networking.k8s.io/v1
    kind: Ingress
    resources: ingresses
    index_by:
      - metadata.namespace
      - metadata.name
`

// WebhookEnabledConfigYAML is the controller configuration with webhook validation enabled.
const WebhookEnabledConfigYAML = `
pod_selector:
  match_labels:
    app: haproxy
    component: loadbalancer

haproxy_config:
  template: |
    global
      maxconn 2000

    defaults
      mode http
      timeout connect 5000ms
      timeout client 50000ms
      timeout server 50000ms

    frontend test-frontend
      bind :8080
      {% for _, ingress := range resources.ingresses.List() -%}
      {%- var rules = ingress | dig("spec", "rules") | fallback([]any{}) %}
      {%- for _, rule := range rules %}
      {%- var paths = rule | dig("http", "paths") | fallback([]any{}) %}
      {%- for _, path := range paths %}
      {%- var service = path | dig("backend", "service") %}
      {%- if service != nil %}
      {%- var pathVal = path | dig("path") | fallback("/") %}
      use_backend {% include "backend-name" %} if { path_beg {{ pathVal }} }
      {% end %}
      {% end %}
      {% end %}
      {% end %}
      default_backend test-backend

    {%- for _, ingress := range resources.ingresses.List() %}
    {%- var rules = ingress | dig("spec", "rules") | fallback([]any{}) %}
    {%- for _, rule := range rules %}
    {%- var paths = rule | dig("http", "paths") | fallback([]any{}) %}
    {%- for _, path := range paths %}
    {%- var service = path | dig("backend", "service") %}
    {%- if service != nil %}

    backend {% include "backend-name" %}
      {%- include "backend-annotation-haproxytech-auth" %}
      server test-server 127.0.0.1:8080
    {% end %}
    {% end %}
    {% end %}
    {% end %}

    backend test-backend
      # Placeholder backend - no servers configured

watched_resources:
  ingresses:
    api_version: networking.k8s.io/v1
    resources: ingresses
    enable_validation_webhook: true
    index_by:
      - metadata.namespace
      - metadata.name
  services:
    api_version: v1
    resources: services
    index_by:
      - metadata.namespace
      - metadata.name
  secrets:
    api_version: v1
    resources: secrets
    store: on-demand
    index_by:
      - metadata.namespace
      - metadata.name

template_snippets:
  backend-name:
    name: backend-name
    template: >-
      {%- var ns = ingress | dig("metadata", "namespace") | fallback("") -%}
      {%- var ingressName = ingress | dig("metadata", "name") | fallback("") -%}
      {%- var svcName = path | dig("backend", "service", "name") | fallback("") -%}
      {%- var portName = path | dig("backend", "service", "port", "name") -%}
      {%- var portNum = path | dig("backend", "service", "port", "number") -%}
      {%- var portId = portName | fallback(portNum) | fallback("") -%}
      {{- " " -}}ing_{{ ns }}_{{ ingressName }}_{{ svcName }}_{{ portId }}

  backend-annotation-haproxytech-auth:
    name: backend-annotation-haproxytech-auth
    priority: 500
    template: |
      {#- Generate http-request auth directives for backends requiring authentication #}
      {%- var auth_type = ingress | dig("metadata", "annotations", "haproxy.org/auth-type") | fallback("") %}
      {%- if auth_type == "basic-auth" %}
        {%- var auth_realm = ingress | dig("metadata", "annotations", "haproxy.org/auth-realm") | fallback("Protected") %}
      http-request auth realm "{{ auth_realm }}"
      {% end %}
`

// NewConfigMap creates a ConfigMap with the given configuration.
func NewConfigMap(namespace, name, configYAML string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string]string{
			"config": configYAML,
		},
	}
}

// NewSecret creates a Secret with HAProxy credentials.
func NewSecret(namespace, name string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		StringData: map[string]string{
			"dataplane_username": "admin",
			"dataplane_password": "password",
		},
	}
}

// NewWebhookCertSecret creates a Secret with dummy TLS certificates for webhook testing.
// The certificates are self-signed and only used for controller startup - acceptance tests
// don't actually use the webhook endpoint.
func NewWebhookCertSecret(namespace, name string) *corev1.Secret {
	// Minimal self-signed certificate (valid for 10 years, CN=webhook)
	// Generated with: openssl req -x509 -newkey rsa:2048 -nodes -days 3650 -subj "/CN=webhook"
	cert := `-----BEGIN CERTIFICATE-----
MIIDCzCCAfOgAwIBAgIUFQ7xGq8VPvT8wF8nVqHt0TkGYCwwDQYJKoZIhvcNAQEL
BQAwFTETMBEGA1UEAwwKd2ViaG9vazEwMB4XDTI1MDEyNjAwMDAwMFoXDTM1MDEy
NDAwMDAwMFowFTETMBEGA1UEAwwKd2ViaG9vazEwMIIBIjANBgkqhkiG9w0BAQEF
AAOCAQ8AMIIBCgKCAQEAr8VQqLFfhkX2YJ4KGg9kPMYxGh2vH2xK3fY5N8nY8fXJ
eDGH8ZfX4V5YQj4C5H9xBjR7CJdN3jJ8WYqH4J7YfC8V5Q9xH5C8fYqH4J7YfC8V
5Q9xH5C8fYqH4J7YfC8V5Q9xH5C8fYqH4J7YfC8V5Q9xH5C8fYqH4J7YfC8V5Q9x
H5C8fYqH4J7YfC8V5Q9xH5C8fYqH4J7YfC8V5Q9xH5C8fYqH4J7YfC8V5Q9xH5C8
fYqH4J7YfC8V5Q9xH5C8fYqH4J7YfC8V5Q9xH5C8fYqH4J7YfC8V5Q9xH5C8fYqH
4J7YfC8V5Q9xH5C8fYqH4J7YfC8V5Q9xH5C8fYqH4J7YfC8V5Q9xH5C8fYqH4J7Y
fC8VQwIDAQABo1MwUTAdBgNVHQ4EFgQU5Q9xH5C8fYqH4J7YfC8V5Q9xH5AwHwYD
VR0jBBgwFoAU5Q9xH5C8fYqH4J7YfC8V5Q9xH5AwDwYDVR0TAQH/BAUwAwEB/zAN
BgkqhkiG9w0BAQsFAAOCAQEAC8fYqH4J7YfC8V5Q9xH5C8fYqH4J7YfC8V5Q9xH5
C8fYqH4J7YfC8V5Q9xH5C8fYqH4J7YfC8V5Q9xH5C8fYqH4J7YfC8V5Q9xH5C8fY
qH4J7YfC8V5Q9xH5C8fYqH4J7YfC8V5Q9xH5C8fYqH4J7YfC8V5Q9xH5C8fYqH4J
7YfC8V5Q9xH5C8fYqH4J7YfC8V5Q9xH5C8fYqH4J7YfC8V5Q9xH5C8fYqH4J7YfC
8V5Q9xH5C8fYqH4J7YfC8V5Q9xH5C8fYqH4J7YfC8V5Q9xH5C8fYqH4J7YfC8V5Q
9xH5C8fYqH4J7YfC8V5Q9xH5C8fYqH4J7YfC8V5Q9xH5C8fYqH4J7YfC8V5Q9xH5
C8fYqH4J7YfC8V5Q9xH5Cw==
-----END CERTIFICATE-----`

	key := `-----BEGIN PRIVATE KEY-----
MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQCvxVCosV+GRfZg
ngoad2Q8xjEaHa8fbErd9jk3ydjx9cl4MYfxl9fhXlhCPgLkf3EGNHsIl03eMnxZ
iofgnth8LxXlD3EfkLx9iofgnth8LxXlD3EfkLx9iofgnth8LxXlD3EfkLx9iofg
nth8LxXlD3EfkLx9iofgnth8LxXlD3EfkLx9iofgnth8LxXlD3EfkLx9iofgnth8
LxXlD3EfkLx9iofgnth8LxXlD3EfkLx9iofgnth8LxXlD3EfkLx9iofgnth8LxXl
D3EfkLx9iofgnth8LxXlD3EfkLx9iofgnth8LxXlD3EfkLx9iofgnth8LxXlD3Ef
kLx9iofgnth8LxXlD3EfkLx9iofgnth8LxXlD3EfkLx9iofgnth8LxVDAgMBAAEC
ggEAC8fYqH4J7YfC8V5Q9xH5C8fYqH4J7YfC8V5Q9xH5C8fYqH4J7YfC8V5Q9xH5
C8fYqH4J7YfC8V5Q9xH5C8fYqH4J7YfC8V5Q9xH5C8fYqH4J7YfC8V5Q9xH5C8fY
qH4J7YfC8V5Q9xH5C8fYqH4J7YfC8V5Q9xH5C8fYqH4J7YfC8V5Q9xH5C8fYqH4J
7YfC8V5Q9xH5C8fYqH4J7YfC8V5Q9xH5C8fYqH4J7YfC8V5Q9xH5C8fYqH4J7YfC
8V5Q9xH5C8fYqH4J7YfC8V5Q9xH5C8fYqH4J7YfC8V5Q9xH5C8fYqH4J7YfC8V5Q
9xH5C8fYqH4J7YfC8V5Q9xH5C8fYqH4J7YfC8V5Q9xH5C8fYqH4J7YfC8V5QQKBg
QDlD3EfkLx9iofgnth8LxXlD3EfkLx9iofgnth8LxXlD3EfkLx9iofgnth8LxXl
D3EfkLx9iofgnth8LxXlD3EfkLx9iofgnth8LxXlD3EfkLx9iofgnth8LxXlD3Ef
kLx9iofgnth8LxXlD3EfkLx9iofgnth8LxXlD3EfkLx9iofgnth8LwKBgQDBXlD3
EfkLx9iofgnth8LxXlD3EfkLx9iofgnth8LxXlD3EfkLx9iofgnth8LxXlD3EfkL
x9iofgnth8LxXlD3EfkLx9iofgnth8LxXlD3EfkLx9iofgnth8LxXlD3EfkLx9io
fgnth8LxXlD3EfkLx9iofgnth8LxXlD3EfkLx9iofgQKBgC8V5Q9xH5C8fYqH4J7Y
fC8V5Q9xH5C8fYqH4J7YfC8V5Q9xH5C8fYqH4J7YfC8V5Q9xH5C8fYqH4J7YfC8V
5Q9xH5C8fYqH4J7YfC8V5Q9xH5C8fYqH4J7YfC8V5Q9xH5C8fYqH4J7YfC8V5Q9x
H5C8fYqH4J7YfC8V5Q9xH5C8fYqH4J7YfAoGBALx9iofgnth8LxXlD3EfkLx9iofg
nth8LxXlD3EfkLx9iofgnth8LxXlD3EfkLx9iofgnth8LxXlD3EfkLx9iofgnth8
LxXlD3EfkLx9iofgnth8LxXlD3EfkLx9iofgnth8LxXlD3EfkLx9iofgnth8LxXl
D3EfkLx9iofgnth8LxXlBAoGAD3EfkLx9iofgnth8LxXlD3EfkLx9iofgnth8LxXl
D3EfkLx9iofgnth8LxXlD3EfkLx9iofgnth8LxXlD3EfkLx9iofgnth8LxXlD3Ef
kLx9iofgnth8LxXlD3EfkLx9iofgnth8LxXlD3EfkLx9iofgnth8LxXlD3EfkLx9
iofgnth8Lw==
-----END PRIVATE KEY-----`

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"tls.crt": []byte(cert),
			"tls.key": []byte(key),
		},
		Type: corev1.SecretTypeTLS,
	}
}

// DefaultHAProxyTemplate is the default HAProxy configuration template used in tests.
const DefaultHAProxyTemplate = `global
  maxconn 2000

defaults
  mode http
  timeout connect 5000ms
  timeout client 50000ms
  timeout server 50000ms

frontend test-frontend
  bind :8080
  {% for _, ingress := range resources.ingresses.List() -%}
  {%- var rules = ingress | dig("spec", "rules") | fallback([]any{}) %}
  {%- for _, rule := range rules %}
  {%- var paths = rule | dig("http", "paths") | fallback([]any{}) %}
  {%- for _, path := range paths %}
  {%- var service = path | dig("backend", "service") %}
  {%- if service != nil %}
  {%- var ns = ingress | dig("metadata", "namespace") | fallback("") %}
  {%- var name = ingress | dig("metadata", "name") | fallback("") %}
  {%- var pathVal = path | dig("path") | fallback("/") %}
  use_backend ing_{{ ns }}_{{ name }} if { path_beg {{ pathVal }} }
  {% end %}
  {% end %}
  {% end %}
  {% end %}
  default_backend test-backend

{%- for _, ingress := range resources.ingresses.List() %}
{%- var rules = ingress | dig("spec", "rules") | fallback([]any{}) %}
{%- for _, rule := range rules %}
{%- var paths = rule | dig("http", "paths") | fallback([]any{}) %}
{%- for _, path := range paths %}
{%- var service = path | dig("backend", "service") %}
{%- if service != nil %}
{%- var ns = ingress | dig("metadata", "namespace") | fallback("") %}
{%- var name = ingress | dig("metadata", "name") | fallback("") %}

backend ing_{{ ns }}_{{ name }}
  server test-server 127.0.0.1:8080
{% end %}
{% end %}
{% end %}
{% end %}

backend test-backend
  # Placeholder backend - no servers configured
`

// HAProxyTemplateConfigBuilder provides a fluent API for creating HAProxyTemplateConfig resources.
// This reduces duplication across the multiple factory functions that create similar configs.
type HAProxyTemplateConfigBuilder struct {
	namespace            string
	name                 string
	secretName           string
	leaderElection       bool
	template             string
	files                map[string]haproxyv1alpha1.GeneralFile
	compressionThreshold *int64
}

// NewHAProxyTemplateConfigBuilder creates a new builder with required parameters.
func NewHAProxyTemplateConfigBuilder(namespace, name, secretName string) *HAProxyTemplateConfigBuilder {
	return &HAProxyTemplateConfigBuilder{
		namespace:      namespace,
		name:           name,
		secretName:     secretName,
		leaderElection: false,
		template:       DefaultHAProxyTemplate,
	}
}

// WithLeaderElection enables or disables leader election.
func (b *HAProxyTemplateConfigBuilder) WithLeaderElection(enabled bool) *HAProxyTemplateConfigBuilder {
	b.leaderElection = enabled
	return b
}

// WithTemplate sets a custom HAProxy template.
func (b *HAProxyTemplateConfigBuilder) WithTemplate(template string) *HAProxyTemplateConfigBuilder {
	b.template = template
	return b
}

// WithFiles sets custom auxiliary files.
func (b *HAProxyTemplateConfigBuilder) WithFiles(files map[string]haproxyv1alpha1.GeneralFile) *HAProxyTemplateConfigBuilder {
	b.files = files
	return b
}

// WithCompressionThreshold sets the compression threshold for CRD content.
// Set to a low value (e.g., 1) to force compression of any config.
// Set to 0 or negative to disable compression.
func (b *HAProxyTemplateConfigBuilder) WithCompressionThreshold(threshold int64) *HAProxyTemplateConfigBuilder {
	b.compressionThreshold = &threshold
	return b
}

// Build creates the HAProxyTemplateConfig resource.
func (b *HAProxyTemplateConfigBuilder) Build() *haproxyv1alpha1.HAProxyTemplateConfig {
	enabled := b.leaderElection

	config := &haproxyv1alpha1.HAProxyTemplateConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: HAProxyTemplateConfigAPIVersion,
			Kind:       HAProxyTemplateConfigKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      b.name,
			Namespace: b.namespace,
		},
		Spec: haproxyv1alpha1.HAProxyTemplateConfigSpec{
			CredentialsSecretRef: haproxyv1alpha1.SecretReference{
				Name: b.secretName,
			},
			PodSelector: haproxyv1alpha1.PodSelector{
				MatchLabels: map[string]string{
					"app":       "haproxy",
					"component": "loadbalancer",
				},
			},
			Controller: haproxyv1alpha1.ControllerConfig{
				LeaderElection: haproxyv1alpha1.LeaderElectionConfig{
					Enabled:       &enabled,
					LeaseName:     "haptic-leader",
					LeaseDuration: "15s",
					RenewDeadline: "10s",
					RetryPeriod:   "2s",
				},
				ConfigPublishing: func() haproxyv1alpha1.ConfigPublishingConfig {
					if b.compressionThreshold != nil {
						return haproxyv1alpha1.ConfigPublishingConfig{
							CompressionThreshold: *b.compressionThreshold,
						}
					}
					return haproxyv1alpha1.ConfigPublishingConfig{}
				}(),
			},
			Logging: haproxyv1alpha1.LoggingConfig{
				Level: "DEBUG", // DEBUG level for tests
			},
			Dataplane: haproxyv1alpha1.DataplaneConfig{
				Port:              5555,
				MapsDir:           "/tmp/haproxy-validation/maps",
				SSLCertsDir:       "/tmp/haproxy-validation/ssl",
				GeneralStorageDir: "/tmp/haproxy-validation/general",
				ConfigFile:        "/tmp/haproxy-validation/haproxy.cfg",
			},
			WatchedResources: map[string]haproxyv1alpha1.WatchedResource{
				"ingresses": {
					APIVersion: "networking.k8s.io/v1",
					Resources:  "ingresses",
					IndexBy:    []string{"metadata.namespace", "metadata.name"},
				},
			},
			HAProxyConfig: haproxyv1alpha1.HAProxyConfig{
				Template: b.template,
			},
			Files: b.files,
		},
	}

	return config
}

// NewHAProxyTemplateConfig creates a HAProxyTemplateConfig CR for testing.
// The leaderElection parameter controls whether leader election is enabled.
func NewHAProxyTemplateConfig(namespace, name, secretName string, leaderElection bool) *haproxyv1alpha1.HAProxyTemplateConfig {
	return NewHAProxyTemplateConfigBuilder(namespace, name, secretName).
		WithLeaderElection(leaderElection).
		Build()
}

// NewServiceAccount creates a ServiceAccount for the controller.
func NewServiceAccount(namespace, name string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
}

// NewRole creates a Role with permissions for ConfigMaps, Secrets, Pods, and Ingresses.
func NewRole(namespace, name string) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"haproxy-haptic.org"},
				Resources: []string{"haproxytemplateconfigs"},
				Verbs:     []string{"get", "watch", "list"},
			},
			{
				APIGroups: []string{"haproxy-haptic.org"},
				Resources: []string{"haproxycfgs"},
				Verbs:     []string{"get", "list", "watch", "create", "update", "patch"},
			},
			{
				APIGroups: []string{"haproxy-haptic.org"},
				Resources: []string{"haproxycfgs/status"},
				Verbs:     []string{"get", "update", "patch"},
			},
			{
				APIGroups: []string{"haproxy-haptic.org"},
				Resources: []string{"haproxymapfiles"},
				Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
			},
			{
				APIGroups: []string{"haproxy-haptic.org"},
				Resources: []string{"haproxymapfiles/status"},
				Verbs:     []string{"get", "update", "patch"},
			},
			{
				APIGroups: []string{"haproxy-haptic.org"},
				Resources: []string{"haproxygeneralfiles"},
				Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
			},
			{
				APIGroups: []string{"haproxy-haptic.org"},
				Resources: []string{"haproxygeneralfiles/status"},
				Verbs:     []string{"get", "update", "patch"},
			},
			{
				APIGroups: []string{"haproxy-haptic.org"},
				Resources: []string{"haproxycrtlistfiles"},
				Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
			},
			{
				APIGroups: []string{"haproxy-haptic.org"},
				Resources: []string{"haproxycrtlistfiles/status"},
				Verbs:     []string{"get", "update", "patch"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"configmaps"},
				Verbs:     []string{"get", "watch", "list"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"secrets"},
				Verbs:     []string{"get", "watch", "list"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"pods"},
				Verbs:     []string{"get", "watch", "list"},
			},
			{
				APIGroups: []string{"networking.k8s.io"},
				Resources: []string{"ingresses"},
				Verbs:     []string{"get", "watch", "list"},
			},
		},
	}
}

// NewRoleBinding creates a RoleBinding that binds the Role to the ServiceAccount.
func NewRoleBinding(namespace, name, roleName, serviceAccountName string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     roleName,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      serviceAccountName,
				Namespace: namespace,
			},
		},
	}
}

// NewClusterRole creates a ClusterRole with a unique name for test isolation.
// The name parameter is the base name, and namespace is appended to ensure uniqueness.
func NewClusterRole(name, namespace string) *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s-%s", name, namespace),
			Labels: map[string]string{
				"app.kubernetes.io/name": ControllerDeploymentName,
				"test-namespace":         namespace,
			},
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"haproxy-haptic.org"},
				Resources: []string{"haproxytemplateconfigs"},
				Verbs:     []string{"get", "watch", "list"},
			},
			{
				APIGroups: []string{"networking.k8s.io"},
				Resources: []string{"ingresses"},
				Verbs:     []string{"get", "watch", "list"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"services", "secrets"},
				Verbs:     []string{"get", "watch", "list"},
			},
			{
				APIGroups: []string{"admissionregistration.k8s.io"},
				Resources: []string{"validatingwebhookconfigurations"},
				Verbs:     []string{"create", "update", "get", "list", "watch", "delete"},
			},
			{
				APIGroups: []string{"coordination.k8s.io"},
				Resources: []string{"leases"},
				Verbs:     []string{"get", "create", "update"},
			},
		},
	}
}

// NewClusterRoleBinding creates a ClusterRoleBinding with a unique name for test isolation.
// The name parameter is the base name, clusterRoleName is the base ClusterRole name,
// and testNamespace is appended to both to ensure uniqueness and proper references.
func NewClusterRoleBinding(name, clusterRoleName, serviceAccountName, serviceAccountNamespace, testNamespace string) *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s-%s", name, testNamespace),
			Labels: map[string]string{
				"app.kubernetes.io/name": ControllerDeploymentName,
				"test-namespace":         testNamespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     fmt.Sprintf("%s-%s", clusterRoleName, testNamespace),
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      serviceAccountName,
				Namespace: serviceAccountNamespace,
			},
		},
	}
}

// NewControllerDeployment creates a controller deployment.
func NewControllerDeployment(namespace, crdName, secretName, serviceAccountName string, debugPort int32, replicas int32) *appsv1.Deployment {

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ControllerDeploymentName,
			Namespace: namespace,
			Labels: map[string]string{
				"app": ControllerDeploymentName,
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": ControllerDeploymentName,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": ControllerDeploymentName,
					},
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: serviceAccountName,
					Containers: []corev1.Container{
						{
							Name:  "controller",
							Image: "haptic:test",
							Env: []corev1.EnvVar{
								{
									Name:  "CRD_NAME",
									Value: crdName,
								},
								{
									Name:  "SECRET_NAME",
									Value: secretName,
								},
								{
									Name:  "DEBUG_PORT",
									Value: "6060",
								},
								{
									Name:  "VERBOSE",
									Value: "2", // DEBUG level
								},
								{
									Name:  "WEBHOOK_SERVICE_NAME",
									Value: "haptic-webhook",
								},
								{
									Name: "POD_NAME",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "metadata.name",
										},
									},
								},
								{
									Name: "POD_NAMESPACE",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{
											FieldPath: "metadata.namespace",
										},
									},
								},
							},
							Ports: []corev1.ContainerPort{
								{
									Name:          "debug",
									ContainerPort: debugPort,
									Protocol:      corev1.ProtocolTCP,
								},
								{
									Name:          "metrics",
									ContainerPort: MetricsPort,
									Protocol:      corev1.ProtocolTCP,
								},
								{
									Name:          "webhook",
									ContainerPort: 9443,
									Protocol:      corev1.ProtocolTCP,
								},
							},
						},
					},
				},
			},
		},
	}
}

// NewWebhookService creates a Service for webhook endpoint.
// The service routes traffic from the Kubernetes API server to the controller's webhook server.
func NewWebhookService(namespace, serviceName string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: namespace,
			Labels: map[string]string{
				"app": ControllerDeploymentName,
			},
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{
				{
					Name:       "webhook",
					Port:       443,
					TargetPort: intstr.FromInt(9443),
					Protocol:   corev1.ProtocolTCP,
				},
			},
			Selector: map[string]string{
				"app": ControllerDeploymentName,
			},
		},
	}
}

// NewValidIngress creates an Ingress with valid auth annotations.
// This Ingress should pass webhook validation.
func NewValidIngress(namespace, name string) *networkingv1.Ingress {
	pathType := networkingv1.PathTypePrefix

	return &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Annotations: map[string]string{
				"haproxy.org/auth-type":  "basic-auth",
				"haproxy.org/auth-realm": "Protected", // Valid: no whitespace
			},
		},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{
				{
					Host: "example.com",
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     "/",
									PathType: &pathType,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: "test-service",
											Port: networkingv1.ServiceBackendPort{
												Number: 80,
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

// NewInvalidIngress creates an Ingress with invalid auth annotations.
// This Ingress should be rejected by webhook validation because auth_realm contains whitespace.
func NewInvalidIngress(namespace, name string) *networkingv1.Ingress {
	pathType := networkingv1.PathTypePrefix

	return &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Annotations: map[string]string{
				"haproxy.org/auth-type":  "basic-auth",
				"haproxy.org/auth-realm": "Invalid With Spaces", // Invalid: contains whitespace
			},
		},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{
				{
					Host: "example.com",
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     "/",
									PathType: &pathType,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: "test-service",
											Port: networkingv1.ServiceBackendPort{
												Number: 80,
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

// BlocklistServerName is the name of the blocklist server deployment.
const BlocklistServerName = "blocklist-server"

// BlocklistContentConfigMapName is the name of the ConfigMap containing the blocklist.
const BlocklistContentConfigMapName = "blocklist-content"

// ValidBlocklistContent contains valid IP addresses in CIDR notation.
const ValidBlocklistContent = `192.168.1.0/24
10.0.0.0/8`

// UpdatedBlocklistContent contains different valid IP addresses.
const UpdatedBlocklistContent = `172.16.0.0/12
192.168.0.0/16`

// InvalidBlocklistContent contains invalid content that will fail HAProxy validation.
const InvalidBlocklistContent = `not-an-ip-address
invalid-cidr`

// NewBlocklistContentConfigMap creates a ConfigMap with blocklist content.
func NewBlocklistContentConfigMap(namespace, content string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      BlocklistContentConfigMapName,
			Namespace: namespace,
		},
		Data: map[string]string{
			"blocklist.txt": content,
		},
	}
}

// NewBlocklistServerDeployment creates an nginx deployment serving the blocklist ConfigMap.
func NewBlocklistServerDeployment(namespace string) *appsv1.Deployment {
	replicas := int32(1)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      BlocklistServerName,
			Namespace: namespace,
			Labels: map[string]string{
				"app": BlocklistServerName,
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": BlocklistServerName,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": BlocklistServerName,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "nginx",
							Image: "nginx:alpine",
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: 80,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "content",
									MountPath: "/usr/share/nginx/html",
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "content",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: BlocklistContentConfigMapName,
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

// NewBlocklistService creates a Service for the blocklist server.
func NewBlocklistService(namespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      BlocklistServerName,
			Namespace: namespace,
			Labels: map[string]string{
				"app": BlocklistServerName,
			},
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{
				{
					Port:       80,
					TargetPort: intstr.FromInt(80),
					Protocol:   corev1.ProtocolTCP,
				},
			},
			Selector: map[string]string{
				"app": BlocklistServerName,
			},
		},
	}
}

// InvalidBackendTemplate is a HAProxy template that references a non-existent backend.
// HAProxy validation (haproxy -c) will fail, but template rendering succeeds.
const InvalidBackendTemplate = `global
  maxconn 2000

defaults
  mode http
  timeout connect 5000ms
  timeout client 50000ms
  timeout server 50000ms

frontend http_front
  bind :8080
  # This references a backend that doesn't exist - HAProxy will reject this
  default_backend nonexistent_backend

backend test-backend
  server test-server 127.0.0.1:8080
`

// NewInvalidBackendHAProxyTemplateConfig creates a HAProxyTemplateConfig with an invalid
// configuration that references a non-existent backend. This should fail HAProxy validation
// (haproxy -c) but the template renders successfully.
func NewInvalidBackendHAProxyTemplateConfig(namespace, name, secretName string) *haproxyv1alpha1.HAProxyTemplateConfig {
	return NewHAProxyTemplateConfigBuilder(namespace, name, secretName).
		WithTemplate(InvalidBackendTemplate).
		Build()
}

// VersionedTemplateFormat is a HAProxy template with a version marker placeholder.
// Use fmt.Sprintf(VersionedTemplateFormat, version) to create the template.
const VersionedTemplateFormat = `global
  maxconn 2000
  # version %d

defaults
  mode http
  timeout connect 5000ms
  timeout client 50000ms
  timeout server 50000ms

frontend test-frontend
  bind :8080
  default_backend test-backend

backend test-backend
  server test-server 127.0.0.1:8080
`

// NewHAProxyTemplateConfigWithVersion creates a HAProxyTemplateConfig with a version marker
// in the template. Used for testing rapid config updates and debouncing.
func NewHAProxyTemplateConfigWithVersion(namespace, name, secretName string, version int) *haproxyv1alpha1.HAProxyTemplateConfig {
	return NewHAProxyTemplateConfigBuilder(namespace, name, secretName).
		WithTemplate(fmt.Sprintf(VersionedTemplateFormat, version)).
		Build()
}

// NewDebugService creates a ClusterIP Service for accessing the controller's debug endpoint.
// Access is provided via the Kubernetes API server proxy, which is more reliable than
// port-forwarding (SPDY) or NodePort (which requires extraPortMappings in DinD environments).
func NewDebugService(namespace, deploymentName string, debugPort int32) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      DebugServiceName(deploymentName),
			Namespace: namespace,
			Labels: map[string]string{
				"app": deploymentName,
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"app": deploymentName,
			},
			Ports: []corev1.ServicePort{
				{
					Name:       "debug",
					Port:       debugPort,
					TargetPort: intstr.FromInt(int(debugPort)),
					Protocol:   corev1.ProtocolTCP,
				},
			},
			Type: corev1.ServiceTypeClusterIP,
		},
	}
}

// NewMetricsService creates a ClusterIP Service for accessing the controller's metrics endpoint.
// This is used by WaitForControllerReady to verify the controller has completed startup reconciliation.
// Access is provided via the Kubernetes API server proxy.
func NewMetricsService(namespace, deploymentName string, metricsPort int32) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      MetricsServiceName(deploymentName),
			Namespace: namespace,
			Labels: map[string]string{
				"app": deploymentName,
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"app": deploymentName,
			},
			Ports: []corev1.ServicePort{
				{
					Name:       "metrics",
					Port:       metricsPort,
					TargetPort: intstr.FromInt(int(metricsPort)),
					Protocol:   corev1.ProtocolTCP,
				},
			},
			Type: corev1.ServiceTypeClusterIP,
		},
	}
}

// HTTPStoreTemplate is a HAProxy template that uses an ACL file for IP blocking.
const HTTPStoreTemplate = `global
  maxconn 2000

defaults
  mode http
  timeout connect 5000ms
  timeout client 50000ms
  timeout server 50000ms

frontend http_front
  bind :8080
  # ACL using the blocklist file - HAProxy validates CIDR syntax
  acl blocked_ips src -f {{ pathResolver.GetPath("blocked-ips.acl", "file") }}
  http-request deny if blocked_ips
  default_backend http_back

backend http_back
  server test-server 127.0.0.1:8080
`

// NewHTTPStoreHAProxyTemplateConfig creates a HAProxyTemplateConfig that uses http.Fetch()
// to fetch a blocklist from an HTTP server. The template creates an ACL file that triggers
// HAProxy validation - invalid content (non-CIDR IPs) will cause validation to fail.
func NewHTTPStoreHAProxyTemplateConfig(namespace, name, secretName string, leaderElection bool) *haproxyv1alpha1.HAProxyTemplateConfig {
	blocklistURL := fmt.Sprintf("http://%s.%s.svc:80/blocklist.txt", BlocklistServerName, namespace)

	return NewHAProxyTemplateConfigBuilder(namespace, name, secretName).
		WithLeaderElection(leaderElection).
		WithTemplate(HTTPStoreTemplate).
		WithFiles(map[string]haproxyv1alpha1.GeneralFile{
			"blocked-ips.acl": {
				Template: fmt.Sprintf(`{%%- var blocklist, fetchErr = http.Fetch("%s", map[string]any{"critical": true, "delay": "5s"}) -%%}
{%%- if fetchErr != nil -%%}{{ fail("failed to fetch blocklist: " + tostring(fetchErr)) }}{%%- end -%%}
{{ blocklist }}`, blocklistURL),
			},
		}).
		Build()
}
