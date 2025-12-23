package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSetDefaults_AllUnset(t *testing.T) {
	cfg := &Config{
		PodSelector: PodSelector{
			MatchLabels: map[string]string{"app": "haproxy"},
		},
	}

	SetDefaults(cfg)

	// Controller defaults
	assert.Equal(t, DefaultHealthzPort, cfg.Controller.HealthzPort)
	assert.Equal(t, DefaultMetricsPort, cfg.Controller.MetricsPort)

	// Dataplane defaults
	assert.Equal(t, DefaultDataplanePort, cfg.Dataplane.Port)
}

func TestSetDefaults_AllSet(t *testing.T) {
	cfg := &Config{
		Controller: ControllerConfig{
			HealthzPort: 8081,
			MetricsPort: 9091,
		},
		Dataplane: DataplaneConfig{
			Port: 5556,
		},
	}

	SetDefaults(cfg)

	// Verify existing values are not overwritten
	assert.Equal(t, 8081, cfg.Controller.HealthzPort)
	assert.Equal(t, 9091, cfg.Controller.MetricsPort)
	assert.Equal(t, 5556, cfg.Dataplane.Port)
}

func TestSetDefaults_PartiallySet(t *testing.T) {
	cfg := &Config{
		Controller: ControllerConfig{
			HealthzPort: 8081, // Set
			// MetricsPort: 0 (unset)
		},
		Dataplane: DataplaneConfig{
			// Port: 0 (unset)
		},
	}

	SetDefaults(cfg)

	// Set values should remain
	assert.Equal(t, 8081, cfg.Controller.HealthzPort)

	// Unset values should get defaults
	assert.Equal(t, DefaultMetricsPort, cfg.Controller.MetricsPort)
	assert.Equal(t, DefaultDataplanePort, cfg.Dataplane.Port)
}

func TestSetDefaults_OperatorConfig(t *testing.T) {
	cfg := &Config{
		Controller: ControllerConfig{},
	}

	SetDefaults(cfg)

	assert.Equal(t, DefaultHealthzPort, cfg.Controller.HealthzPort)
	assert.Equal(t, DefaultMetricsPort, cfg.Controller.MetricsPort)
}

func TestSetDefaults_LoggingConfig(t *testing.T) {
	// Logging config has no defaults that override empty values
	// (Empty Level is valid = means use LOG_LEVEL env var or default INFO)
	cfg := &Config{
		Logging: LoggingConfig{},
	}

	SetDefaults(cfg)

	// Empty value should remain (it is valid - means use env var)
	assert.Equal(t, "", cfg.Logging.Level)
}

func TestSetDefaults_Constants(t *testing.T) {
	// Verify default constants have expected values
	assert.Equal(t, 8080, DefaultHealthzPort)
	assert.Equal(t, 9090, DefaultMetricsPort)
	assert.Equal(t, "", DefaultLevel)
	assert.Equal(t, 5555, DefaultDataplanePort)
	assert.False(t, DefaultEnableValidationWebhook)
}

func TestSetDefaults_IntegrationWithParsing(t *testing.T) {
	// Test the typical workflow: Parse -> SetDefaults -> Validate
	yamlConfig := `
pod_selector:
  match_labels:
    app: haproxy

watched_resources:
  ingresses:
    api_version: networking.k8s.io/v1
    resources: ingresses
    index_by: ["metadata.namespace"]

haproxy_config:
  template: "global"
`

	cfg, err := parseConfig(yamlConfig)
	assert.NoError(t, err)

	// Before SetDefaults, ports should be 0
	assert.Equal(t, 0, cfg.Controller.HealthzPort)
	assert.Equal(t, 0, cfg.Controller.MetricsPort)

	SetDefaults(cfg)

	// After SetDefaults, ports should have default values
	assert.Equal(t, DefaultHealthzPort, cfg.Controller.HealthzPort)
	assert.Equal(t, DefaultMetricsPort, cfg.Controller.MetricsPort)

	// After SetDefaults, validation should pass
	err = ValidateStructure(cfg)
	assert.NoError(t, err)
}

func TestSetDefaults_Idempotent(t *testing.T) {
	cfg := &Config{
		Controller: ControllerConfig{},
	}

	// Apply defaults twice
	SetDefaults(cfg)
	firstHealthz := cfg.Controller.HealthzPort
	firstMetrics := cfg.Controller.MetricsPort

	SetDefaults(cfg)
	secondHealthz := cfg.Controller.HealthzPort
	secondMetrics := cfg.Controller.MetricsPort

	// Should be idempotent
	assert.Equal(t, firstHealthz, secondHealthz)
	assert.Equal(t, firstMetrics, secondMetrics)
}

func TestDataplaneConfig_GetMinDeploymentInterval(t *testing.T) {
	tests := []struct {
		name     string
		interval string
		want     time.Duration
	}{
		{
			name:     "empty returns default",
			interval: "",
			want:     DefaultMinDeploymentInterval,
		},
		{
			name:     "valid duration parsed",
			interval: "5s",
			want:     5 * time.Second,
		},
		{
			name:     "valid milliseconds",
			interval: "500ms",
			want:     500 * time.Millisecond,
		},
		{
			name:     "invalid duration returns default",
			interval: "invalid",
			want:     DefaultMinDeploymentInterval,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &DataplaneConfig{
				MinDeploymentInterval: tt.interval,
			}
			got := cfg.GetMinDeploymentInterval()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDataplaneConfig_GetDriftPreventionInterval(t *testing.T) {
	tests := []struct {
		name     string
		interval string
		want     time.Duration
	}{
		{
			name:     "empty returns default",
			interval: "",
			want:     DefaultDriftPreventionInterval,
		},
		{
			name:     "valid duration parsed",
			interval: "120s",
			want:     120 * time.Second,
		},
		{
			name:     "valid minutes",
			interval: "5m",
			want:     5 * time.Minute,
		},
		{
			name:     "invalid duration returns default",
			interval: "notaduration",
			want:     DefaultDriftPreventionInterval,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &DataplaneConfig{
				DriftPreventionInterval: tt.interval,
			}
			got := cfg.GetDriftPreventionInterval()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestLeaderElectionConfig_GetLeaseDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration string
		want     time.Duration
	}{
		{
			name:     "empty returns default",
			duration: "",
			want:     DefaultLeaderElectionLeaseDuration,
		},
		{
			name:     "valid duration parsed",
			duration: "30s",
			want:     30 * time.Second,
		},
		{
			name:     "invalid duration returns default",
			duration: "bad",
			want:     DefaultLeaderElectionLeaseDuration,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &LeaderElectionConfig{
				LeaseDuration: tt.duration,
			}
			got := cfg.GetLeaseDuration()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestLeaderElectionConfig_GetRenewDeadline(t *testing.T) {
	tests := []struct {
		name     string
		deadline string
		want     time.Duration
	}{
		{
			name:     "empty returns default",
			deadline: "",
			want:     DefaultLeaderElectionRenewDeadline,
		},
		{
			name:     "valid duration parsed",
			deadline: "20s",
			want:     20 * time.Second,
		},
		{
			name:     "invalid duration returns default",
			deadline: "xyz",
			want:     DefaultLeaderElectionRenewDeadline,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &LeaderElectionConfig{
				RenewDeadline: tt.deadline,
			}
			got := cfg.GetRenewDeadline()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestLeaderElectionConfig_GetRetryPeriod(t *testing.T) {
	tests := []struct {
		name   string
		period string
		want   time.Duration
	}{
		{
			name:   "empty returns default",
			period: "",
			want:   DefaultLeaderElectionRetryPeriod,
		},
		{
			name:   "valid duration parsed",
			period: "5s",
			want:   5 * time.Second,
		},
		{
			name:   "invalid duration returns default",
			period: "garbage",
			want:   DefaultLeaderElectionRetryPeriod,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &LeaderElectionConfig{
				RetryPeriod: tt.period,
			}
			got := cfg.GetRetryPeriod()
			assert.Equal(t, tt.want, got)
		})
	}
}
