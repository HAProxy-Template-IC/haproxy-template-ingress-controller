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

	// Dataplane defaults
	assert.Equal(t, DefaultDataplanePort, cfg.Dataplane.Port)

	// Leader election defaults
	assert.Equal(t, DefaultLeaderElectionLeaseName, cfg.Controller.LeaderElection.LeaseName)
}

func TestSetDefaults_AllSet(t *testing.T) {
	cfg := &Config{
		Controller: ControllerConfig{
			LeaderElection: LeaderElectionConfig{
				LeaseName: "custom-lease",
			},
		},
		Dataplane: DataplaneConfig{
			Port: 5556,
		},
	}

	SetDefaults(cfg)

	// Verify existing values are not overwritten
	assert.Equal(t, "custom-lease", cfg.Controller.LeaderElection.LeaseName)
	assert.Equal(t, 5556, cfg.Dataplane.Port)
}

func TestSetDefaults_PartiallySet(t *testing.T) {
	cfg := &Config{
		Controller: ControllerConfig{
			LeaderElection: LeaderElectionConfig{
				LeaseName: "custom-lease", // Set, LeaseDuration left unset
			},
		},
		Dataplane: DataplaneConfig{}, // Port left unset
	}

	SetDefaults(cfg)

	// Set values should remain
	assert.Equal(t, "custom-lease", cfg.Controller.LeaderElection.LeaseName)

	// Unset values should get defaults
	assert.Equal(t, DefaultLeaderElectionLeaseDuration.String(), cfg.Controller.LeaderElection.LeaseDuration)
	assert.Equal(t, DefaultDataplanePort, cfg.Dataplane.Port)
}

func TestSetDefaults_ControllerConfig(t *testing.T) {
	cfg := &Config{
		Controller: ControllerConfig{},
	}

	SetDefaults(cfg)

	assert.Equal(t, DefaultLeaderElectionLeaseName, cfg.Controller.LeaderElection.LeaseName)
	assert.Equal(t, DefaultLeaderElectionLeaseDuration.String(), cfg.Controller.LeaderElection.LeaseDuration)
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
	assert.Equal(t, "", DefaultLevel)
	assert.Equal(t, 5555, DefaultDataplanePort)
	assert.False(t, DefaultEnableValidationWebhook)
	assert.Equal(t, "haptic-leader", DefaultLeaderElectionLeaseName)
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

	// Before SetDefaults, leader election fields should be empty
	assert.Equal(t, "", cfg.Controller.LeaderElection.LeaseName)

	SetDefaults(cfg)

	// After SetDefaults, leader election should have default values
	assert.Equal(t, DefaultLeaderElectionLeaseName, cfg.Controller.LeaderElection.LeaseName)

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
	firstLeaseName := cfg.Controller.LeaderElection.LeaseName
	firstPort := cfg.Dataplane.Port

	SetDefaults(cfg)
	secondLeaseName := cfg.Controller.LeaderElection.LeaseName
	secondPort := cfg.Dataplane.Port

	// Should be idempotent
	assert.Equal(t, firstLeaseName, secondLeaseName)
	assert.Equal(t, firstPort, secondPort)
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
