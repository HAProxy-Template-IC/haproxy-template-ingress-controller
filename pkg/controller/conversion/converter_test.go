package conversion

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/core/config"
)

func TestConvertSpec(t *testing.T) {
	tests := []struct {
		name    string
		spec    v1alpha1.HAProxyTemplateConfigSpec
		want    *config.Config
		wantErr bool
	}{
		{
			name: "minimal valid config",
			spec: v1alpha1.HAProxyTemplateConfigSpec{
				CredentialsSecretRef: v1alpha1.SecretReference{
					Name: "haproxy-creds",
				},
				PodSelector: v1alpha1.PodSelector{
					MatchLabels: map[string]string{
						"app": "haproxy",
					},
				},
				HAProxyConfig: v1alpha1.HAProxyConfig{
					Template: "global\n  daemon",
				},
			},
			want: &config.Config{
				PodSelector: config.PodSelector{
					MatchLabels: map[string]string{
						"app": "haproxy",
					},
				},
				Controller: config.ControllerConfig{
					LeaderElection: config.LeaderElectionConfig{
						Enabled: true, // Default when not specified
					},
				},
				HAProxyConfig: config.HAProxyConfig{
					Template: "global\n  daemon",
				},
				WatchedResources: map[string]config.WatchedResource{},
				TemplateSnippets: map[string]config.TemplateSnippet{},
				Maps:             map[string]config.MapFile{},
				Files:            map[string]config.GeneralFile{},
				SSLCertificates:  map[string]config.SSLCertificate{},
			},
			wantErr: false,
		},
		{
			name: "complete config with all sections",
			spec: v1alpha1.HAProxyTemplateConfigSpec{
				CredentialsSecretRef: v1alpha1.SecretReference{
					Name:      "haproxy-creds",
					Namespace: "default",
				},
				PodSelector: v1alpha1.PodSelector{
					MatchLabels: map[string]string{
						"app":       "haproxy",
						"component": "loadbalancer",
					},
				},
				Controller: v1alpha1.ControllerConfig{
					LeaderElection: v1alpha1.LeaderElectionConfig{
						Enabled:       boolPtr(true),
						LeaseName:     "haproxy-leader",
						LeaseDuration: "15s",
						RenewDeadline: "10s",
						RetryPeriod:   "2s",
					},
				},
				Logging: v1alpha1.LoggingConfig{
					Level: "INFO",
				},
				Dataplane: v1alpha1.DataplaneConfig{
					Port:                    5555,
					MinDeploymentInterval:   "2s",
					DriftPreventionInterval: "5m",
				},
				WatchedResources: map[string]v1alpha1.WatchedResource{
					"ingresses": {
						APIVersion:    "networking.k8s.io/v1",
						Resources:     "ingresses",
						LabelSelector: "app=nginx,env=prod",
						IndexBy:       []string{"metadata.namespace", "metadata.name"},
					},
				},
				TemplateSnippets: map[string]v1alpha1.TemplateSnippet{
					"common_defaults": {
						Template: "timeout connect 5s",
					},
				},
				Maps: map[string]v1alpha1.MapFile{
					"backend_map": {
						Template: "{{ range .ingresses }}{{ .metadata.name }} backend_{{ .metadata.name }}\n{{ end }}",
					},
				},
				Files: map[string]v1alpha1.GeneralFile{
					"error_page": {
						Template: "<html><body>Error</body></html>",
					},
				},
				SSLCertificates: map[string]v1alpha1.SSLCertificate{
					"default_cert": {
						Template: "{{ .secret.data.cert }}",
					},
				},
				HAProxyConfig: v1alpha1.HAProxyConfig{
					Template: "global\n  daemon\n\ndefaults\n  {% include 'common_defaults' %}",
				},
			},
			want: &config.Config{
				PodSelector: config.PodSelector{
					MatchLabels: map[string]string{
						"app":       "haproxy",
						"component": "loadbalancer",
					},
				},
				Controller: config.ControllerConfig{
					LeaderElection: config.LeaderElectionConfig{
						Enabled:       true,
						LeaseName:     "haproxy-leader",
						LeaseDuration: "15s",
						RenewDeadline: "10s",
						RetryPeriod:   "2s",
					},
				},
				Logging: config.LoggingConfig{
					Level: "INFO",
				},
				Dataplane: config.DataplaneConfig{
					Port:                    5555,
					MinDeploymentInterval:   "2s",
					DriftPreventionInterval: "5m",
				},
				WatchedResources: map[string]config.WatchedResource{
					"ingresses": {
						APIVersion: "networking.k8s.io/v1",
						Resources:  "ingresses",
						LabelSelector: map[string]string{
							"app": "nginx",
							"env": "prod",
						},
						IndexBy: []string{"metadata.namespace", "metadata.name"},
					},
				},
				TemplateSnippets: map[string]config.TemplateSnippet{
					"common_defaults": {
						Name:     "common_defaults",
						Template: "timeout connect 5s",
					},
				},
				Maps: map[string]config.MapFile{
					"backend_map": {
						Template: "{{ range .ingresses }}{{ .metadata.name }} backend_{{ .metadata.name }}\n{{ end }}",
					},
				},
				Files: map[string]config.GeneralFile{
					"error_page": {
						Template: "<html><body>Error</body></html>",
					},
				},
				SSLCertificates: map[string]config.SSLCertificate{
					"default_cert": {
						Template: "{{ .secret.data.cert }}",
					},
				},
				HAProxyConfig: config.HAProxyConfig{
					Template: "global\n  daemon\n\ndefaults\n  {% include 'common_defaults' %}",
				},
			},
			wantErr: false,
		},
		{
			name: "leader election disabled",
			spec: v1alpha1.HAProxyTemplateConfigSpec{
				CredentialsSecretRef: v1alpha1.SecretReference{
					Name: "haproxy-creds",
				},
				PodSelector: v1alpha1.PodSelector{
					MatchLabels: map[string]string{"app": "haproxy"},
				},
				Controller: v1alpha1.ControllerConfig{
					LeaderElection: v1alpha1.LeaderElectionConfig{
						Enabled: boolPtr(false),
					},
				},
				HAProxyConfig: v1alpha1.HAProxyConfig{
					Template: "global\n  daemon",
				},
			},
			want: &config.Config{
				PodSelector: config.PodSelector{
					MatchLabels: map[string]string{"app": "haproxy"},
				},
				Controller: config.ControllerConfig{
					LeaderElection: config.LeaderElectionConfig{
						Enabled: false,
					},
				},
				HAProxyConfig: config.HAProxyConfig{
					Template: "global\n  daemon",
				},
				WatchedResources: map[string]config.WatchedResource{},
				TemplateSnippets: map[string]config.TemplateSnippet{},
				Maps:             map[string]config.MapFile{},
				Files:            map[string]config.GeneralFile{},
				SSLCertificates:  map[string]config.SSLCertificate{},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ConvertSpec(&tt.spec)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want.PodSelector, got.PodSelector)
			assert.Equal(t, tt.want.Controller, got.Controller)
			assert.Equal(t, tt.want.Logging, got.Logging)
			assert.Equal(t, tt.want.Dataplane, got.Dataplane)
			assert.Equal(t, tt.want.WatchedResources, got.WatchedResources)
			assert.Equal(t, tt.want.TemplateSnippets, got.TemplateSnippets)
			assert.Equal(t, tt.want.Maps, got.Maps)
			assert.Equal(t, tt.want.Files, got.Files)
			assert.Equal(t, tt.want.SSLCertificates, got.SSLCertificates)
			assert.Equal(t, tt.want.HAProxyConfig, got.HAProxyConfig)
		})
	}
}

func TestParseLabelSelector(t *testing.T) {
	tests := []struct {
		name     string
		selector string
		want     map[string]string
	}{
		{
			name:     "empty string",
			selector: "",
			want:     nil,
		},
		{
			name:     "single label",
			selector: "app=nginx",
			want: map[string]string{
				"app": "nginx",
			},
		},
		{
			name:     "multiple labels",
			selector: "app=nginx,env=prod,version=v1",
			want: map[string]string{
				"app":     "nginx",
				"env":     "prod",
				"version": "v1",
			},
		},
		{
			name:     "labels with spaces",
			selector: "app = nginx , env = prod",
			want: map[string]string{
				"app": "nginx",
				"env": "prod",
			},
		},
		{
			name:     "label with empty value",
			selector: "app=nginx,env=",
			want: map[string]string{
				"app": "nginx",
				"env": "",
			},
		},
		{
			name:     "trailing comma",
			selector: "app=nginx,env=prod,",
			want: map[string]string{
				"app": "nginx",
				"env": "prod",
			},
		},
		{
			name:     "malformed - no equals",
			selector: "app,env=prod",
			want: map[string]string{
				"env": "prod",
			},
		},
		{
			name:     "kubernetes style labels",
			selector: "app.kubernetes.io/name=haproxy,app.kubernetes.io/component=loadbalancer",
			want: map[string]string{
				"app.kubernetes.io/name":      "haproxy",
				"app.kubernetes.io/component": "loadbalancer",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseLabelSelector(tt.selector)
			assert.Equal(t, tt.want, got)
		})
	}
}

// boolPtr returns a pointer to a bool value (helper for tests).
func boolPtr(b bool) *bool {
	return &b
}

func TestParseCRD(t *testing.T) {
	tests := []struct {
		name    string
		crd     *unstructured.Unstructured
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid HAProxyTemplateConfig CRD",
			crd: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "haproxy-haptic.org/v1alpha1",
					"kind":       "HAProxyTemplateConfig",
					"metadata": map[string]interface{}{
						"name":      "test-config",
						"namespace": "default",
					},
					"spec": map[string]interface{}{
						"credentialsSecretRef": map[string]interface{}{
							"name": "haproxy-creds",
						},
						"podSelector": map[string]interface{}{
							"matchLabels": map[string]interface{}{
								"app": "haproxy",
							},
						},
						"haproxyConfig": map[string]interface{}{
							"template": "global\n  daemon",
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid kind",
			crd: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "haproxy-haptic.org/v1alpha1",
					"kind":       "Deployment",
					"metadata": map[string]interface{}{
						"name":      "test-config",
						"namespace": "default",
					},
				},
			},
			wantErr: true,
			errMsg:  "expected HAProxyTemplateConfig, got Deployment",
		},
		{
			name: "invalid apiVersion",
			crd: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "apps/v1",
					"kind":       "HAProxyTemplateConfig",
					"metadata": map[string]interface{}{
						"name":      "test-config",
						"namespace": "default",
					},
				},
			},
			wantErr: true,
			errMsg:  "expected apiVersion haproxy-haptic.org/v1alpha1, got apps/v1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, crd, err := ParseCRD(tt.crd)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				assert.Nil(t, cfg)
				assert.Nil(t, crd)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, cfg)
			require.NotNil(t, crd)
			assert.Equal(t, "test-config", crd.Name)
		})
	}
}

func TestConvertSpec_WithValidationTests(t *testing.T) {
	// Create fixture JSON
	ingressJSON, err := json.Marshal(map[string]interface{}{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "Ingress",
		"metadata": map[string]interface{}{
			"name":      "test-ingress",
			"namespace": "default",
		},
	})
	require.NoError(t, err)

	spec := v1alpha1.HAProxyTemplateConfigSpec{
		CredentialsSecretRef: v1alpha1.SecretReference{
			Name: "haproxy-creds",
		},
		PodSelector: v1alpha1.PodSelector{
			MatchLabels: map[string]string{"app": "haproxy"},
		},
		HAProxyConfig: v1alpha1.HAProxyConfig{
			Template: "global\n  daemon",
		},
		ValidationTests: map[string]v1alpha1.ValidationTest{
			"test-basic": {
				Description: "Basic test",
				Fixtures: map[string][]runtime.RawExtension{
					"ingresses": {
						{Raw: ingressJSON},
					},
				},
				HTTPResources: []v1alpha1.HTTPResourceFixture{
					{URL: "http://example.com/data", Content: `{"key": "value"}`},
				},
				Assertions: []v1alpha1.ValidationAssertion{
					{
						Type:        "contains",
						Description: "Check backend exists",
						Target:      "haproxy_config",
						Pattern:     "backend test-ingress",
					},
					{
						Type:        "jsonpath",
						Description: "Check JSON value",
						Target:      "http://example.com/data",
						JSONPath:    "$.key",
						Expected:    "value",
					},
					{
						Type:        "not_contains",
						Description: "Check no errors",
						Target:      "haproxy_config",
						Patterns:    []string{"error", "warning"},
					},
				},
			},
		},
	}

	cfg, err := ConvertSpec(&spec)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	// Check validation tests were converted
	require.Contains(t, cfg.ValidationTests, "test-basic")
	test := cfg.ValidationTests["test-basic"]

	assert.Equal(t, "Basic test", test.Description)

	// Check fixtures were converted
	require.Contains(t, test.Fixtures, "ingresses")
	require.Len(t, test.Fixtures["ingresses"], 1)

	// Check HTTP fixtures were converted
	require.Len(t, test.HTTPFixtures, 1)
	assert.Equal(t, "http://example.com/data", test.HTTPFixtures[0].URL)
	assert.Equal(t, `{"key": "value"}`, test.HTTPFixtures[0].Content)

	// Check assertions were converted
	require.Len(t, test.Assertions, 3)
	assert.Equal(t, "contains", test.Assertions[0].Type)
	assert.Equal(t, "Check backend exists", test.Assertions[0].Description)
	assert.Equal(t, "haproxy_config", test.Assertions[0].Target)
	assert.Equal(t, "backend test-ingress", test.Assertions[0].Pattern)

	assert.Equal(t, "jsonpath", test.Assertions[1].Type)
	assert.Equal(t, "$.key", test.Assertions[1].JSONPath)
	assert.Equal(t, "value", test.Assertions[1].Expected)

	assert.Equal(t, "not_contains", test.Assertions[2].Type)
	assert.Equal(t, []string{"error", "warning"}, test.Assertions[2].Patterns)
}

func TestConvertSpec_WithPostProcessors(t *testing.T) {
	spec := v1alpha1.HAProxyTemplateConfigSpec{
		CredentialsSecretRef: v1alpha1.SecretReference{
			Name: "haproxy-creds",
		},
		PodSelector: v1alpha1.PodSelector{
			MatchLabels: map[string]string{"app": "haproxy"},
		},
		HAProxyConfig: v1alpha1.HAProxyConfig{
			Template: "global\n  daemon",
			PostProcessing: []v1alpha1.PostProcessorConfig{
				{Type: "sort_acls", Params: map[string]string{"order": "asc"}},
				{Type: "deduplicate"},
			},
		},
		Maps: map[string]v1alpha1.MapFile{
			"hosts": {
				Template: "{{ range .ingresses }}{{ .spec.host }} backend\n{{ end }}",
				PostProcessing: []v1alpha1.PostProcessorConfig{
					{Type: "sort_lines"},
				},
			},
		},
		Files: map[string]v1alpha1.GeneralFile{
			"error": {
				Template: "<html>Error</html>",
				PostProcessing: []v1alpha1.PostProcessorConfig{
					{Type: "minify_html"},
				},
			},
		},
		SSLCertificates: map[string]v1alpha1.SSLCertificate{
			"default": {
				Template: "{{ .cert }}{{ .key }}",
				PostProcessing: []v1alpha1.PostProcessorConfig{
					{Type: "pem_format"},
				},
			},
		},
	}

	cfg, err := ConvertSpec(&spec)
	require.NoError(t, err)

	// Check HAProxy config post processors
	require.Len(t, cfg.HAProxyConfig.PostProcessing, 2)
	assert.Equal(t, "sort_acls", cfg.HAProxyConfig.PostProcessing[0].Type)
	assert.Equal(t, map[string]string{"order": "asc"}, cfg.HAProxyConfig.PostProcessing[0].Params)
	assert.Equal(t, "deduplicate", cfg.HAProxyConfig.PostProcessing[1].Type)

	// Check Maps post processors
	require.Contains(t, cfg.Maps, "hosts")
	require.Len(t, cfg.Maps["hosts"].PostProcessing, 1)
	assert.Equal(t, "sort_lines", cfg.Maps["hosts"].PostProcessing[0].Type)

	// Check Files post processors
	require.Contains(t, cfg.Files, "error")
	require.Len(t, cfg.Files["error"].PostProcessing, 1)
	assert.Equal(t, "minify_html", cfg.Files["error"].PostProcessing[0].Type)

	// Check SSL certificates post processors
	require.Contains(t, cfg.SSLCertificates, "default")
	require.Len(t, cfg.SSLCertificates["default"].PostProcessing, 1)
	assert.Equal(t, "pem_format", cfg.SSLCertificates["default"].PostProcessing[0].Type)
}

func TestConvertSpec_ExtraContextError(t *testing.T) {
	spec := v1alpha1.HAProxyTemplateConfigSpec{
		CredentialsSecretRef: v1alpha1.SecretReference{
			Name: "haproxy-creds",
		},
		PodSelector: v1alpha1.PodSelector{
			MatchLabels: map[string]string{"app": "haproxy"},
		},
		HAProxyConfig: v1alpha1.HAProxyConfig{
			Template: "global",
		},
		TemplatingSettings: v1alpha1.TemplatingSettings{
			ExtraContext: runtime.RawExtension{
				Raw: []byte("invalid json{"),
			},
		},
	}

	cfg, err := ConvertSpec(&spec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to unmarshal templating_settings.extra_context")
	assert.Nil(t, cfg)
}

func TestConvertSpec_WithExtraContext(t *testing.T) {
	extraContextJSON, err := json.Marshal(map[string]interface{}{
		"environment": "production",
		"maxConns":    1000,
		"servers": []string{
			"server1.example.com",
			"server2.example.com",
		},
	})
	require.NoError(t, err)

	spec := v1alpha1.HAProxyTemplateConfigSpec{
		CredentialsSecretRef: v1alpha1.SecretReference{
			Name: "haproxy-creds",
		},
		PodSelector: v1alpha1.PodSelector{
			MatchLabels: map[string]string{"app": "haproxy"},
		},
		HAProxyConfig: v1alpha1.HAProxyConfig{
			Template: "global",
		},
		TemplatingSettings: v1alpha1.TemplatingSettings{
			ExtraContext: runtime.RawExtension{
				Raw: extraContextJSON,
			},
		},
	}

	cfg, err := ConvertSpec(&spec)
	require.NoError(t, err)

	assert.Equal(t, "production", cfg.TemplatingSettings.ExtraContext["environment"])
	assert.Equal(t, float64(1000), cfg.TemplatingSettings.ExtraContext["maxConns"]) // JSON numbers are float64
	assert.Len(t, cfg.TemplatingSettings.ExtraContext["servers"], 2)
}

func TestConvertFixtures(t *testing.T) {
	t.Run("empty fixtures", func(t *testing.T) {
		fixtures := convertFixtures(nil)
		assert.Empty(t, fixtures)
	})

	t.Run("valid fixtures", func(t *testing.T) {
		ingressJSON, err := json.Marshal(map[string]interface{}{
			"apiVersion": "networking.k8s.io/v1",
			"kind":       "Ingress",
			"metadata": map[string]interface{}{
				"name": "my-ingress",
			},
		})
		require.NoError(t, err)

		serviceJSON, err := json.Marshal(map[string]interface{}{
			"apiVersion": "v1",
			"kind":       "Service",
			"metadata": map[string]interface{}{
				"name": "my-service",
			},
		})
		require.NoError(t, err)

		crdFixtures := map[string][]runtime.RawExtension{
			"ingresses": {{Raw: ingressJSON}},
			"services":  {{Raw: serviceJSON}},
		}

		fixtures := convertFixtures(crdFixtures)
		require.Len(t, fixtures, 2)
		require.Len(t, fixtures["ingresses"], 1)
		require.Len(t, fixtures["services"], 1)

		// Check ingress was parsed correctly
		ingress := fixtures["ingresses"][0].(map[string]interface{})
		assert.Equal(t, "Ingress", ingress["kind"])
	})

	t.Run("invalid JSON falls back to empty object", func(t *testing.T) {
		crdFixtures := map[string][]runtime.RawExtension{
			"invalid": {{Raw: []byte("not valid json")}},
		}

		fixtures := convertFixtures(crdFixtures)
		require.Len(t, fixtures, 1)
		require.Len(t, fixtures["invalid"], 1)

		// Should be empty map due to parse error
		obj := fixtures["invalid"][0].(map[string]interface{})
		assert.Empty(t, obj)
	})
}

func TestConvertPostProcessors(t *testing.T) {
	t.Run("empty list", func(t *testing.T) {
		result := convertPostProcessors(nil)
		assert.Nil(t, result)
	})

	t.Run("empty slice", func(t *testing.T) {
		result := convertPostProcessors([]v1alpha1.PostProcessorConfig{})
		assert.Nil(t, result)
	})

	t.Run("with processors", func(t *testing.T) {
		input := []v1alpha1.PostProcessorConfig{
			{Type: "sort_acls", Params: map[string]string{"order": "desc"}},
			{Type: "deduplicate"},
		}

		result := convertPostProcessors(input)
		require.Len(t, result, 2)
		assert.Equal(t, "sort_acls", result[0].Type)
		assert.Equal(t, map[string]string{"order": "desc"}, result[0].Params)
		assert.Equal(t, "deduplicate", result[1].Type)
	})
}

func TestConvertAssertions(t *testing.T) {
	t.Run("empty assertions", func(t *testing.T) {
		result := convertAssertions(nil)
		assert.Empty(t, result)
	})

	t.Run("various assertion types", func(t *testing.T) {
		input := []v1alpha1.ValidationAssertion{
			{
				Type:        "contains",
				Description: "Check content",
				Target:      "haproxy_config",
				Pattern:     "backend",
			},
			{
				Type:        "jsonpath",
				Description: "Check JSON",
				Target:      "response",
				JSONPath:    "$.status",
				Expected:    "ok",
			},
			{
				Type:        "not_contains",
				Description: "No errors",
				Target:      "output",
				Patterns:    []string{"error", "fail"},
			},
		}

		result := convertAssertions(input)
		require.Len(t, result, 3)

		assert.Equal(t, "contains", result[0].Type)
		assert.Equal(t, "Check content", result[0].Description)
		assert.Equal(t, "haproxy_config", result[0].Target)
		assert.Equal(t, "backend", result[0].Pattern)

		assert.Equal(t, "jsonpath", result[1].Type)
		assert.Equal(t, "$.status", result[1].JSONPath)
		assert.Equal(t, "ok", result[1].Expected)

		assert.Equal(t, "not_contains", result[2].Type)
		assert.Equal(t, []string{"error", "fail"}, result[2].Patterns)
	})
}

func TestConvertHTTPFixtures(t *testing.T) {
	t.Run("empty fixtures", func(t *testing.T) {
		result := convertHTTPFixtures(nil)
		assert.Nil(t, result)
	})

	t.Run("empty slice", func(t *testing.T) {
		result := convertHTTPFixtures([]v1alpha1.HTTPResourceFixture{})
		assert.Nil(t, result)
	})

	t.Run("with fixtures", func(t *testing.T) {
		input := []v1alpha1.HTTPResourceFixture{
			{URL: "http://api.example.com/users", Content: `[{"id": 1}]`},
			{URL: "http://api.example.com/config", Content: `{"enabled": true}`},
		}

		result := convertHTTPFixtures(input)
		require.Len(t, result, 2)

		assert.Equal(t, "http://api.example.com/users", result[0].URL)
		assert.Equal(t, `[{"id": 1}]`, result[0].Content)

		assert.Equal(t, "http://api.example.com/config", result[1].URL)
		assert.Equal(t, `{"enabled": true}`, result[1].Content)
	})
}

func TestConvertSpec_WithWatchedResourcesIgnoreFields(t *testing.T) {
	spec := v1alpha1.HAProxyTemplateConfigSpec{
		CredentialsSecretRef: v1alpha1.SecretReference{
			Name: "haproxy-creds",
		},
		PodSelector: v1alpha1.PodSelector{
			MatchLabels: map[string]string{"app": "haproxy"},
		},
		HAProxyConfig: v1alpha1.HAProxyConfig{
			Template: "global",
		},
		WatchedResourcesIgnoreFields: []string{
			"metadata.managedFields",
			"metadata.annotations['kubectl.kubernetes.io/last-applied-configuration']",
		},
	}

	cfg, err := ConvertSpec(&spec)
	require.NoError(t, err)

	require.Len(t, cfg.WatchedResourcesIgnoreFields, 2)
	assert.Contains(t, cfg.WatchedResourcesIgnoreFields, "metadata.managedFields")
}

func TestConvertSpec_WithWatchedResourceStore(t *testing.T) {
	spec := v1alpha1.HAProxyTemplateConfigSpec{
		CredentialsSecretRef: v1alpha1.SecretReference{
			Name: "haproxy-creds",
		},
		PodSelector: v1alpha1.PodSelector{
			MatchLabels: map[string]string{"app": "haproxy"},
		},
		HAProxyConfig: v1alpha1.HAProxyConfig{
			Template: "global",
		},
		WatchedResources: map[string]v1alpha1.WatchedResource{
			"secrets": {
				APIVersion:              "v1",
				Resources:               "secrets",
				EnableValidationWebhook: true,
				Store:                   "memory",
			},
		},
	}

	cfg, err := ConvertSpec(&spec)
	require.NoError(t, err)

	require.Contains(t, cfg.WatchedResources, "secrets")
	assert.Equal(t, "memory", cfg.WatchedResources["secrets"].Store)
	assert.True(t, cfg.WatchedResources["secrets"].EnableValidationWebhook)
}

func TestConvertSpec_DataplaneDirectories(t *testing.T) {
	spec := v1alpha1.HAProxyTemplateConfigSpec{
		CredentialsSecretRef: v1alpha1.SecretReference{
			Name: "haproxy-creds",
		},
		PodSelector: v1alpha1.PodSelector{
			MatchLabels: map[string]string{"app": "haproxy"},
		},
		HAProxyConfig: v1alpha1.HAProxyConfig{
			Template: "global",
		},
		Dataplane: v1alpha1.DataplaneConfig{
			Port:              5555,
			MapsDir:           "/etc/haproxy/maps",
			SSLCertsDir:       "/etc/haproxy/certs",
			GeneralStorageDir: "/etc/haproxy/storage",
			ConfigFile:        "/etc/haproxy/haproxy.cfg",
		},
	}

	cfg, err := ConvertSpec(&spec)
	require.NoError(t, err)

	assert.Equal(t, "/etc/haproxy/maps", cfg.Dataplane.MapsDir)
	assert.Equal(t, "/etc/haproxy/certs", cfg.Dataplane.SSLCertsDir)
	assert.Equal(t, "/etc/haproxy/storage", cfg.Dataplane.GeneralStorageDir)
	assert.Equal(t, "/etc/haproxy/haproxy.cfg", cfg.Dataplane.ConfigFile)
}
