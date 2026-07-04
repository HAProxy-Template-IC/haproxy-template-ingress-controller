package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateStructure_Success(t *testing.T) {
	cfg := &Config{
		PodSelector: PodSelector{
			MatchLabels: map[string]string{"app": "haproxy"},
		},
		Controller: ControllerConfig{},
		Logging: LoggingConfig{
			Level: "INFO",
		},
		Dataplane: DataplaneConfig{
			Port:              5555,
			MapsDir:           "/etc/haproxy/maps",
			SSLCertsDir:       "/etc/haproxy/certs",
			GeneralStorageDir: "/etc/haproxy/general",
			ConfigFile:        "/etc/haproxy/haproxy.cfg",
		},
		WatchedResources: map[string]WatchedResource{
			"ingresses": {
				APIVersion: "networking.k8s.io/v1",
				Resources:  "ingresses",
				IndexBy:    []string{"metadata.namespace"},
			},
		},
		HAProxyConfig: HAProxyConfig{
			Template: "global",
		},
	}

	err := ValidateStructure(cfg)
	assert.NoError(t, err)
}

func TestValidateStructure_NilConfig(t *testing.T) {
	err := ValidateStructure(nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "config is nil")
}

func TestValidatePodSelector_EmptyMatchLabels(t *testing.T) {
	cfg := &Config{
		PodSelector: PodSelector{
			MatchLabels: map[string]string{},
		},
	}

	err := ValidateStructure(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "match_labels cannot be empty")
}

func TestValidatePodSelector_EmptyLabelKey(t *testing.T) {
	cfg := &Config{
		PodSelector: PodSelector{
			MatchLabels: map[string]string{"": "value"},
		},
	}

	err := ValidateStructure(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "match_labels key cannot be empty")
}

func TestValidatePodSelector_EmptyLabelValue(t *testing.T) {
	cfg := &Config{
		PodSelector: PodSelector{
			MatchLabels: map[string]string{"app": ""},
		},
	}

	err := ValidateStructure(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "match_labels value")
}

func TestValidateLoggingConfig_InvalidLevel(t *testing.T) {
	tests := []struct {
		name  string
		level string
	}{
		{"invalid string", "INVALID"},
		{"numeric string", "1"},
		{"typo", "DEBG"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				PodSelector: PodSelector{
					MatchLabels: map[string]string{"app": "haproxy"},
				},
				Controller: ControllerConfig{},
				Logging: LoggingConfig{
					Level: tt.level,
				},
				WatchedResources: map[string]WatchedResource{
					"ingresses": {
						APIVersion: "networking.k8s.io/v1",
						Resources:  "ingresses",
						IndexBy:    []string{"metadata.namespace"},
					},
				},
				HAProxyConfig: HAProxyConfig{
					Template: "global",
				},
			}

			err := ValidateStructure(cfg)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "level")
		})
	}
}

func TestValidateLoggingConfig_ValidLevels(t *testing.T) {
	validLevels := []string{"", "TRACE", "DEBUG", "INFO", "WARN", "ERROR", "trace", "debug", "info", "warn", "error"}

	for _, level := range validLevels {
		t.Run("level_"+level, func(t *testing.T) {
			cfg := &Config{
				PodSelector: PodSelector{
					MatchLabels: map[string]string{"app": "haproxy"},
				},
				Controller: ControllerConfig{},
				Logging: LoggingConfig{
					Level: level,
				},
				Dataplane: DataplaneConfig{
					Port:              5555,
					MapsDir:           "/etc/haproxy/maps",
					SSLCertsDir:       "/etc/haproxy/certs",
					GeneralStorageDir: "/etc/haproxy/general",
					ConfigFile:        "/etc/haproxy/haproxy.cfg",
				},
				WatchedResources: map[string]WatchedResource{
					"ingresses": {
						APIVersion: "networking.k8s.io/v1",
						Resources:  "ingresses",
						IndexBy:    []string{"metadata.namespace"},
					},
				},
				HAProxyConfig: HAProxyConfig{
					Template: "global",
				},
			}

			err := ValidateStructure(cfg)
			assert.NoError(t, err)
		})
	}
}

func TestValidateWatchedResources_Empty(t *testing.T) {
	cfg := &Config{
		PodSelector: PodSelector{
			MatchLabels: map[string]string{"app": "haproxy"},
		},
		Controller: ControllerConfig{},
		Dataplane: DataplaneConfig{
			Port:              5555,
			MapsDir:           "/etc/haproxy/maps",
			SSLCertsDir:       "/etc/haproxy/certs",
			GeneralStorageDir: "/etc/haproxy/general",
			ConfigFile:        "/etc/haproxy/haproxy.cfg",
		},
		WatchedResources: map[string]WatchedResource{},
		HAProxyConfig: HAProxyConfig{
			Template: "global",
		},
	}

	err := ValidateStructure(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "at least one resource must be configured")
}

func TestValidateWatchedResource_MissingAPIVersion(t *testing.T) {
	cfg := &Config{
		PodSelector: PodSelector{
			MatchLabels: map[string]string{"app": "haproxy"},
		},
		Controller: ControllerConfig{},
		Dataplane: DataplaneConfig{
			Port:              5555,
			MapsDir:           "/etc/haproxy/maps",
			SSLCertsDir:       "/etc/haproxy/certs",
			GeneralStorageDir: "/etc/haproxy/general",
			ConfigFile:        "/etc/haproxy/haproxy.cfg",
		},
		WatchedResources: map[string]WatchedResource{
			"ingresses": {
				APIVersion: "",
				Resources:  "ingresses",
				IndexBy:    []string{"metadata.namespace"},
			},
		},
		HAProxyConfig: HAProxyConfig{
			Template: "global",
		},
	}

	err := ValidateStructure(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "api_version")
}

func TestValidateWatchedResource_MissingKind(t *testing.T) {
	cfg := &Config{
		PodSelector: PodSelector{
			MatchLabels: map[string]string{"app": "haproxy"},
		},
		Controller: ControllerConfig{},
		Dataplane: DataplaneConfig{
			Port:              5555,
			MapsDir:           "/etc/haproxy/maps",
			SSLCertsDir:       "/etc/haproxy/certs",
			GeneralStorageDir: "/etc/haproxy/general",
			ConfigFile:        "/etc/haproxy/haproxy.cfg",
		},
		WatchedResources: map[string]WatchedResource{
			"ingresses": {
				APIVersion: "networking.k8s.io/v1",
				Resources:  "",
				IndexBy:    []string{"metadata.namespace"},
			},
		},
		HAProxyConfig: HAProxyConfig{
			Template: "global",
		},
	}

	err := ValidateStructure(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "resources")
}

func TestValidateWatchedResource_EmptyIndexBy(t *testing.T) {
	cfg := &Config{
		PodSelector: PodSelector{
			MatchLabels: map[string]string{"app": "haproxy"},
		},
		Controller: ControllerConfig{},
		Dataplane: DataplaneConfig{
			Port:              5555,
			MapsDir:           "/etc/haproxy/maps",
			SSLCertsDir:       "/etc/haproxy/certs",
			GeneralStorageDir: "/etc/haproxy/general",
			ConfigFile:        "/etc/haproxy/haproxy.cfg",
		},
		WatchedResources: map[string]WatchedResource{
			"ingresses": {
				APIVersion: "networking.k8s.io/v1",
				Resources:  "ingresses",
				IndexBy:    []string{},
			},
		},
		HAProxyConfig: HAProxyConfig{
			Template: "global",
		},
	}

	err := ValidateStructure(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "index_by")
}

func TestValidateWatchedResource_EmptyIndexByElement(t *testing.T) {
	cfg := &Config{
		PodSelector: PodSelector{
			MatchLabels: map[string]string{"app": "haproxy"},
		},
		Controller: ControllerConfig{},
		Dataplane: DataplaneConfig{
			Port:              5555,
			MapsDir:           "/etc/haproxy/maps",
			SSLCertsDir:       "/etc/haproxy/certs",
			GeneralStorageDir: "/etc/haproxy/general",
			ConfigFile:        "/etc/haproxy/haproxy.cfg",
		},
		WatchedResources: map[string]WatchedResource{
			"ingresses": {
				APIVersion: "networking.k8s.io/v1",
				Resources:  "ingresses",
				IndexBy:    []string{"metadata.namespace", ""},
			},
		},
		HAProxyConfig: HAProxyConfig{
			Template: "global",
		},
	}

	err := ValidateStructure(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "index_by[1] cannot be empty")
}

func TestValidateHAProxyConfig_EmptyTemplate(t *testing.T) {
	cfg := &Config{
		PodSelector: PodSelector{
			MatchLabels: map[string]string{"app": "haproxy"},
		},
		Controller: ControllerConfig{},
		Dataplane: DataplaneConfig{
			Port:              5555,
			MapsDir:           "/etc/haproxy/maps",
			SSLCertsDir:       "/etc/haproxy/certs",
			GeneralStorageDir: "/etc/haproxy/general",
			ConfigFile:        "/etc/haproxy/haproxy.cfg",
		},
		WatchedResources: map[string]WatchedResource{
			"ingresses": {
				APIVersion: "networking.k8s.io/v1",
				Resources:  "ingresses",
				IndexBy:    []string{"metadata.namespace"},
			},
		},
		HAProxyConfig: HAProxyConfig{
			Template: "",
		},
	}

	err := ValidateStructure(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "template cannot be empty")
}

func TestValidateCredentials_Success(t *testing.T) {
	creds := &Credentials{
		DataplaneUsername: "admin",
		DataplanePassword: "pass",
	}

	err := ValidateCredentials(creds)
	assert.NoError(t, err)
}

func TestValidateCredentials_Nil(t *testing.T) {
	err := ValidateCredentials(nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "credentials are nil")
}

func TestValidateCredentials_MissingFields(t *testing.T) {
	tests := []struct {
		name     string
		creds    *Credentials
		errField string
	}{
		{
			name: "missing dataplane_username",
			creds: &Credentials{
				DataplaneUsername: "",
				DataplanePassword: "pass",
			},
			errField: "dataplane_username",
		},
		{
			name: "missing dataplane_password",
			creds: &Credentials{
				DataplaneUsername: "admin",
				DataplanePassword: "",
			},
			errField: "dataplane_password",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCredentials(tt.creds)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.errField)
		})
	}
}

func TestValidateDataplaneConfig_InvalidPort(t *testing.T) {
	tests := []struct {
		name string
		port int
	}{
		{"zero", 0},
		{"negative", -1},
		{"too large", 65536},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				PodSelector: PodSelector{
					MatchLabels: map[string]string{"app": "haproxy"},
				},
				Controller: ControllerConfig{},
				Dataplane: DataplaneConfig{
					Port:              tt.port,
					MapsDir:           "/etc/haproxy/maps",
					SSLCertsDir:       "/etc/haproxy/certs",
					GeneralStorageDir: "/etc/haproxy/general",
					ConfigFile:        "/etc/haproxy/haproxy.cfg",
				},
				WatchedResources: map[string]WatchedResource{
					"ingresses": {
						APIVersion: "networking.k8s.io/v1",
						Resources:  "ingresses",
						IndexBy:    []string{"metadata.namespace"},
					},
				},
				HAProxyConfig: HAProxyConfig{
					Template: "global",
				},
			}

			err := ValidateStructure(cfg)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "port must be between 1 and 65535")
		})
	}
}

func TestValidateDataplaneConfig_EmptyPaths(t *testing.T) {
	tests := []struct {
		name      string
		mapsDir   string
		sslDir    string
		genDir    string
		cfgFile   string
		errSubstr string
	}{
		{
			name:      "empty maps_dir",
			mapsDir:   "",
			sslDir:    "/etc/haproxy/certs",
			genDir:    "/etc/haproxy/general",
			cfgFile:   "/etc/haproxy/haproxy.cfg",
			errSubstr: "maps_dir cannot be empty",
		},
		{
			name:      "empty ssl_certs_dir",
			mapsDir:   "/etc/haproxy/maps",
			sslDir:    "",
			genDir:    "/etc/haproxy/general",
			cfgFile:   "/etc/haproxy/haproxy.cfg",
			errSubstr: "ssl_certs_dir cannot be empty",
		},
		{
			name:      "empty general_storage_dir",
			mapsDir:   "/etc/haproxy/maps",
			sslDir:    "/etc/haproxy/certs",
			genDir:    "",
			cfgFile:   "/etc/haproxy/haproxy.cfg",
			errSubstr: "general_storage_dir cannot be empty",
		},
		{
			name:      "empty config_file",
			mapsDir:   "/etc/haproxy/maps",
			sslDir:    "/etc/haproxy/certs",
			genDir:    "/etc/haproxy/general",
			cfgFile:   "",
			errSubstr: "config_file cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				PodSelector: PodSelector{
					MatchLabels: map[string]string{"app": "haproxy"},
				},
				Controller: ControllerConfig{},
				Dataplane: DataplaneConfig{
					Port:              5555,
					MapsDir:           tt.mapsDir,
					SSLCertsDir:       tt.sslDir,
					GeneralStorageDir: tt.genDir,
					ConfigFile:        tt.cfgFile,
				},
				WatchedResources: map[string]WatchedResource{
					"ingresses": {
						APIVersion: "networking.k8s.io/v1",
						Resources:  "ingresses",
						IndexBy:    []string{"metadata.namespace"},
					},
				},
				HAProxyConfig: HAProxyConfig{
					Template: "global",
				},
			}

			err := ValidateStructure(cfg)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.errSubstr)
		})
	}
}

// versionFieldsBaseConfig returns a minimal valid config whose single watched
// resource is overridden per test case.
func versionFieldsBaseConfig(res *WatchedResource) *Config {
	return &Config{
		PodSelector: PodSelector{
			MatchLabels: map[string]string{"app": "haproxy"},
		},
		Dataplane: DataplaneConfig{
			Port:              5555,
			MapsDir:           "/etc/haproxy/maps",
			SSLCertsDir:       "/etc/haproxy/certs",
			GeneralStorageDir: "/etc/haproxy/general",
			ConfigFile:        "/etc/haproxy/haproxy.cfg",
		},
		WatchedResources: map[string]WatchedResource{"routes": *res},
		HAProxyConfig:    HAProxyConfig{Template: "global"},
	}
}

func TestValidateWatchedResource_VersionFields(t *testing.T) {
	tests := []struct {
		name      string
		resource  WatchedResource
		errSubstr string // empty = expect success
	}{
		{
			name: "api_versions list alone is valid",
			resource: WatchedResource{
				APIVersions: []string{"example.io/v1", "example.io/v1beta1"},
				Resources:   "routes",
				IndexBy:     []string{"metadata.name"},
			},
		},
		{
			name: "api_version and api_versions are mutually exclusive",
			resource: WatchedResource{
				APIVersion:  "example.io/v1",
				APIVersions: []string{"example.io/v1beta1"},
				Resources:   "routes",
				IndexBy:     []string{"metadata.name"},
			},
			errSubstr: "mutually exclusive",
		},
		{
			name: "empty api_versions element is rejected",
			resource: WatchedResource{
				APIVersions: []string{"example.io/v1", ""},
				Resources:   "routes",
				IndexBy:     []string{"metadata.name"},
			},
			errSubstr: "api_versions[1] cannot be empty",
		},
		{
			name: "neither field set is rejected",
			resource: WatchedResource{
				Resources: "routes",
				IndexBy:   []string{"metadata.name"},
			},
			errSubstr: "one of api_version or api_versions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateStructure(versionFieldsBaseConfig(&tt.resource))
			if tt.errSubstr == "" {
				assert.NoError(t, err)
				return
			}
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.errSubstr)
		})
	}
}

func TestValidateRequires(t *testing.T) {
	base := func() *Config {
		return versionFieldsBaseConfig(&WatchedResource{
			APIVersion: "example.io/v1",
			Resources:  "routes",
			IndexBy:    []string{"metadata.name"},
		})
	}

	t.Run("requires naming a watched resource is valid", func(t *testing.T) {
		cfg := base()
		cfg.TemplateSnippets = map[string]TemplateSnippet{
			"snippet-a": {Name: "snippet-a", Template: "x", Requires: []string{"routes"}},
		}
		cfg.ValidationTests = map[string]ValidationTest{
			"test-a": {Requires: []string{"routes"}},
		}
		assert.NoError(t, ValidateStructure(cfg))
	})

	t.Run("dangling snippet requires is rejected", func(t *testing.T) {
		cfg := base()
		cfg.TemplateSnippets = map[string]TemplateSnippet{
			"snippet-a": {Name: "snippet-a", Template: "x", Requires: []string{"nonexistent"}},
		}
		err := ValidateStructure(cfg)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), `template_snippets.snippet-a: requires "nonexistent"`)
	})

	t.Run("dangling test requires is rejected", func(t *testing.T) {
		cfg := base()
		cfg.ValidationTests = map[string]ValidationTest{
			"test-a": {Requires: []string{"nonexistent"}},
		}
		err := ValidateStructure(cfg)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), `validation_tests.test-a: requires "nonexistent"`)
	})
}

func TestWatchedResource_CandidateVersions(t *testing.T) {
	list := WatchedResource{APIVersions: []string{"example.io/v1", "example.io/v1beta1"}}
	assert.Equal(t, []string{"example.io/v1", "example.io/v1beta1"}, list.CandidateVersions())

	single := WatchedResource{APIVersion: "example.io/v1"}
	assert.Equal(t, []string{"example.io/v1"}, single.CandidateVersions())
}
