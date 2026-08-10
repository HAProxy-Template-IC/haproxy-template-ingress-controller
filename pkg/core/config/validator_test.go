package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// validConfig returns a Config that passes ValidateStructure; each test mutates
// only the field it is about.
func validConfig() *Config {
	return &Config{
		PodSelector:      PodSelector{MatchLabels: map[string]string{"app": "haproxy"}},
		Logging:          LoggingConfig{Level: "INFO"},
		Dataplane:        DataplaneConfig{Port: 5555, MapsDir: "/etc/haproxy/maps", SSLCertsDir: "/etc/haproxy/certs", GeneralStorageDir: "/etc/haproxy/general", ConfigFile: "/etc/haproxy/haproxy.cfg"},
		WatchedResources: map[string]WatchedResource{"ingresses": {APIVersion: "networking.k8s.io/v1", Resources: "ingresses", IndexBy: []string{"metadata.namespace"}}},
		HAProxyConfig:    HAProxyConfig{Template: "global"},
	}
}

func TestValidateStructure_Success(t *testing.T) {
	cfg := validConfig()

	err := ValidateStructure(cfg)
	assert.NoError(t, err)
}

func TestValidateStructure_NilConfig(t *testing.T) {
	err := ValidateStructure(nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "config is nil")
}

func TestValidateStructure_RejectsInvalidValidatorGlobs(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*ValidatorConfig)
		want      string
	}{
		{
			name: "files",
			configure: func(validator *ValidatorConfig) {
				validator.Files = []string{"general/[broken"}
			},
			want: "validators: spoa-hub.files: invalid glob",
		},
		{
			name: "data files",
			configure: func(validator *ValidatorConfig) {
				validator.DataFiles = []string{"general/[broken"}
			},
			want: "validators: spoa-hub.data_files: invalid glob",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			validator := ValidatorConfig{
				Name:       "spoa-hub",
				SocketPath: "/var/run/haptic-validators/spoa-hub.sock",
				Files:      []string{"general/*.toml"},
			}
			tt.configure(&validator)
			cfg.Validators = []ValidatorConfig{validator}

			err := ValidateStructure(cfg)
			assert.ErrorContains(t, err, tt.want)
		})
	}
}

func TestValidatePodSelector_EmptyMatchLabels(t *testing.T) {
	cfg := validConfig()
	cfg.PodSelector.MatchLabels = map[string]string{}

	err := ValidateStructure(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "match_labels cannot be empty")
}

func TestValidatePodSelector_EmptyLabelKey(t *testing.T) {
	cfg := validConfig()
	cfg.PodSelector.MatchLabels = map[string]string{"": "value"}

	err := ValidateStructure(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "match_labels key cannot be empty")
}

func TestValidatePodSelector_EmptyLabelValue(t *testing.T) {
	cfg := validConfig()
	cfg.PodSelector.MatchLabels = map[string]string{"app": ""}

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
			cfg := validConfig()
			cfg.Logging.Level = tt.level

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
			cfg := validConfig()
			cfg.Logging.Level = level

			err := ValidateStructure(cfg)
			assert.NoError(t, err)
		})
	}
}

func TestValidateWatchedResources_Empty(t *testing.T) {
	cfg := validConfig()
	cfg.WatchedResources = map[string]WatchedResource{}

	err := ValidateStructure(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "at least one resource must be configured")
}

func TestValidateWatchedResource_MissingAPIVersion(t *testing.T) {
	cfg := validConfig()
	cfg.WatchedResources["ingresses"] = WatchedResource{Resources: "ingresses", IndexBy: []string{"metadata.namespace"}}

	err := ValidateStructure(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "api_version")
}

func TestValidateWatchedResource_MissingKind(t *testing.T) {
	cfg := validConfig()
	cfg.WatchedResources["ingresses"] = WatchedResource{APIVersion: "networking.k8s.io/v1", Resources: "", IndexBy: []string{"metadata.namespace"}}

	err := ValidateStructure(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "resources")
}

func TestValidateWatchedResource_EmptyIndexBy(t *testing.T) {
	cfg := validConfig()
	cfg.WatchedResources["ingresses"] = WatchedResource{APIVersion: "networking.k8s.io/v1", Resources: "ingresses", IndexBy: []string{}}

	err := ValidateStructure(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "index_by")
}

func TestValidateWatchedResource_EmptyIndexByElement(t *testing.T) {
	cfg := validConfig()
	cfg.WatchedResources["ingresses"] = WatchedResource{APIVersion: "networking.k8s.io/v1", Resources: "ingresses", IndexBy: []string{"metadata.namespace", ""}}

	err := ValidateStructure(cfg)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "index_by[1] cannot be empty")
}

func TestValidateHAProxyConfig_EmptyTemplate(t *testing.T) {
	cfg := validConfig()
	cfg.HAProxyConfig.Template = ""

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
			cfg := validConfig()
			cfg.Dataplane.Port = tt.port

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
			cfg := validConfig()
			cfg.Dataplane.MapsDir = tt.mapsDir
			cfg.Dataplane.SSLCertsDir = tt.sslDir
			cfg.Dataplane.GeneralStorageDir = tt.genDir
			cfg.Dataplane.ConfigFile = tt.cfgFile

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
			"test-a": {Requires: []string{"routes"}, Assertions: []ValidationAssertion{{Type: "haproxy_valid"}}},
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
			"test-a": {Requires: []string{"nonexistent"}, Assertions: []ValidationAssertion{{Type: "haproxy_valid"}}},
		}
		err := ValidateStructure(cfg)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), `validation_tests.test-a: requires "nonexistent"`)
	})

	t.Run("requiresFields naming a watched resource field is valid", func(t *testing.T) {
		cfg := base()
		cfg.ValidationTests = map[string]ValidationTest{
			"test-a": {RequiresFields: []string{"routes.spec.rules.filters.cors"}, Assertions: []ValidationAssertion{{Type: "haproxy_valid"}}},
		}
		assert.NoError(t, ValidateStructure(cfg))
	})

	t.Run("requiresFields with dangling first segment is rejected", func(t *testing.T) {
		cfg := base()
		cfg.ValidationTests = map[string]ValidationTest{
			"test-a": {RequiresFields: []string{"nonexistent.spec.rules"}, Assertions: []ValidationAssertion{{Type: "haproxy_valid"}}},
		}
		err := ValidateStructure(cfg)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), `validation_tests.test-a: requiresFields entry "nonexistent.spec.rules"`)
		assert.Contains(t, err.Error(), `"nonexistent"`)
	})

	t.Run("requiresFields without a field path is rejected", func(t *testing.T) {
		for _, entry := range []string{"routes", "routes."} {
			cfg := base()
			cfg.ValidationTests = map[string]ValidationTest{
				"test-a": {RequiresFields: []string{entry}, Assertions: []ValidationAssertion{{Type: "haproxy_valid"}}},
			}
			err := ValidateStructure(cfg)
			assert.Error(t, err, "entry %q must be rejected", entry)
			assert.Contains(t, err.Error(), `must be of the form`)
		}
	})
}

func TestWatchedResource_CandidateVersions(t *testing.T) {
	list := WatchedResource{APIVersions: []string{"example.io/v1", "example.io/v1beta1"}}
	assert.Equal(t, []string{"example.io/v1", "example.io/v1beta1"}, list.CandidateVersions())

	single := WatchedResource{APIVersion: "example.io/v1"}
	assert.Equal(t, []string{"example.io/v1"}, single.CandidateVersions())
}

// mergedCompletenessConfig is the minimum a merged config must satisfy: the
// fields whose CRD `Required` markers were dropped so a single object of a
// merged set can be incomplete (ADR-0014). Deliberately carries none of the
// dataplane runtime defaults — ValidateMergedCompleteness must not demand them,
// or the admission webhook would start denying hand-written configs.
func mergedCompletenessConfig() *Config {
	return &Config{
		PodSelector:      PodSelector{MatchLabels: map[string]string{"app": "haproxy"}},
		WatchedResources: map[string]WatchedResource{"ingresses": {APIVersion: "networking.k8s.io/v1", Resources: "ingresses", IndexBy: []string{"metadata.namespace"}}},
		HAProxyConfig:    HAProxyConfig{Template: "global"},
	}
}

func TestValidateMergedCompleteness(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{
			name:   "a complete merged config with no dataplane defaults passes",
			mutate: func(*Config) {},
		},
		{
			name:    "podSelector is required after the merge",
			mutate:  func(c *Config) { c.PodSelector = PodSelector{} },
			wantErr: "pod_selector: match_labels cannot be empty",
		},
		{
			name:    "at least one watched resource is required after the merge",
			mutate:  func(c *Config) { c.WatchedResources = nil },
			wantErr: "watched_resources: at least one resource must be configured",
		},
		{
			name:    "the haproxy template is required after the merge",
			mutate:  func(c *Config) { c.HAProxyConfig = HAProxyConfig{} },
			wantErr: "haproxy_config: template cannot be empty",
		},
		{
			name: "a test that asserts nothing is rejected",
			mutate: func(c *Config) {
				c.ValidationTests = map[string]ValidationTest{"silent": {}}
			},
			wantErr: "validation_tests.silent: must declare at least one assertion",
		},
		{
			name: "a test with an assertion is accepted",
			mutate: func(c *Config) {
				c.ValidationTests = map[string]ValidationTest{
					"real": {Assertions: []ValidationAssertion{{Type: "haproxy_valid"}}},
				}
			},
		},
		{
			// _global is a shared baseline, not a test — the runner never
			// executes its assertions, and several libraries each contribute
			// part of it, so their objects carry an incomplete one.
			name: "the _global baseline may assert nothing",
			mutate: func(c *Config) {
				c.ValidationTests = map[string]ValidationTest{
					GlobalValidationTestName: {Fixtures: map[string][]any{}},
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := mergedCompletenessConfig()
			tt.mutate(cfg)

			err := ValidateMergedCompleteness(cfg)
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

// The load-path gate must keep enforcing everything the narrower prospective
// gate does, so a config the webhook admits can't fail differently on load.
func TestValidateStructure_SubsumesMergedCompleteness(t *testing.T) {
	cfg := mergedCompletenessConfig()
	cfg.Logging = LoggingConfig{Level: "INFO"}
	cfg.Dataplane = DataplaneConfig{
		Port: 5555, MapsDir: "/etc/haproxy/maps", SSLCertsDir: "/etc/haproxy/certs",
		GeneralStorageDir: "/etc/haproxy/general", ConfigFile: "/etc/haproxy/haproxy.cfg",
	}
	cfg.ValidationTests = map[string]ValidationTest{"silent": {}}

	assert.ErrorContains(t, ValidateStructure(cfg),
		"validation_tests.silent: must declare at least one assertion")
}
