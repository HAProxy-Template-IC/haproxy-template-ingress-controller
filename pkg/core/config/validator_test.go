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
		Controller: ControllerConfig{
			HealthzPort: 8080,
			MetricsPort: 9090,
		},
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

func TestValidateOperatorConfig_InvalidHealthzPort(t *testing.T) {
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
				Controller: ControllerConfig{
					HealthzPort: tt.port,
					MetricsPort: 9090,
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
			assert.Contains(t, err.Error(), "healthz_port")
		})
	}
}

func TestValidateOperatorConfig_InvalidMetricsPort(t *testing.T) {
	cfg := &Config{
		PodSelector: PodSelector{
			MatchLabels: map[string]string{"app": "haproxy"},
		},
		Controller: ControllerConfig{
			HealthzPort: 8080,
			MetricsPort: 0,
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
	assert.Contains(t, err.Error(), "metrics_port")
}

func TestValidateOperatorConfig_SamePort(t *testing.T) {
	cfg := &Config{
		PodSelector: PodSelector{
			MatchLabels: map[string]string{"app": "haproxy"},
		},
		Controller: ControllerConfig{
			HealthzPort: 8080,
			MetricsPort: 8080,
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
	assert.Contains(t, err.Error(), "cannot be the same")
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
				Controller: ControllerConfig{
					HealthzPort: 8080,
					MetricsPort: 9090,
				},
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
				Controller: ControllerConfig{
					HealthzPort: 8080,
					MetricsPort: 9090,
				},
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
		Controller: ControllerConfig{
			HealthzPort: 8080,
			MetricsPort: 9090,
		},
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
		Controller: ControllerConfig{
			HealthzPort: 8080,
			MetricsPort: 9090,
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
		Controller: ControllerConfig{
			HealthzPort: 8080,
			MetricsPort: 9090,
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
		Controller: ControllerConfig{
			HealthzPort: 8080,
			MetricsPort: 9090,
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
		Controller: ControllerConfig{
			HealthzPort: 8080,
			MetricsPort: 9090,
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
		Controller: ControllerConfig{
			HealthzPort: 8080,
			MetricsPort: 9090,
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
				Controller: ControllerConfig{
					HealthzPort: 8080,
					MetricsPort: 9090,
				},
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
				Controller: ControllerConfig{
					HealthzPort: 8080,
					MetricsPort: 9090,
				},
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
