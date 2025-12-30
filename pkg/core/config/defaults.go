package config

import "time"

// Default values for configuration fields.
const (
	// DefaultLevel is the default log level.
	// Empty string means use LOG_LEVEL env var or default to INFO.
	DefaultLevel = ""

	// DefaultDataplanePort is the default Dataplane API port for production HAProxy pods.
	DefaultDataplanePort = 5555

	// DefaultEnableValidationWebhook is the default webhook setting for resources.
	DefaultEnableValidationWebhook = false

	// DefaultMinDeploymentInterval is the default minimum time between consecutive deployments.
	DefaultMinDeploymentInterval = 2 * time.Second

	// DefaultDriftPreventionInterval is the default interval for periodic drift prevention deployments.
	DefaultDriftPreventionInterval = 60 * time.Second

	// DefaultDeploymentTimeout is the maximum time to wait for a deployment to complete.
	// If exceeded, the scheduler assumes the deployment was lost and retries.
	DefaultDeploymentTimeout = 30 * time.Second

	// DefaultDataplaneMapsDir is the default directory for HAProxy map files.
	DefaultDataplaneMapsDir = "/etc/haproxy/maps"

	// DefaultDataplaneSSLCertsDir is the default directory for SSL certificates.
	// This matches the values.yaml default and HAProxy container directory structure.
	DefaultDataplaneSSLCertsDir = "/etc/haproxy/certs"

	// DefaultDataplaneGeneralStorageDir is the default directory for general files.
	DefaultDataplaneGeneralStorageDir = "/etc/haproxy/general"

	// DefaultDataplaneConfigFile is the default path to the main HAProxy config file.
	DefaultDataplaneConfigFile = "/etc/haproxy/haproxy.cfg"

	// DefaultLeaderElectionEnabled is the default leader election enabled setting.
	DefaultLeaderElectionEnabled = true

	// DefaultLeaderElectionLeaseName is the default name for the leader election lease.
	DefaultLeaderElectionLeaseName = "haptic-leader"

	// DefaultLeaderElectionLeaseDuration is the default lease duration.
	DefaultLeaderElectionLeaseDuration = 15 * time.Second

	// DefaultLeaderElectionRenewDeadline is the default renew deadline.
	DefaultLeaderElectionRenewDeadline = 10 * time.Second

	// DefaultLeaderElectionRetryPeriod is the default retry period.
	DefaultLeaderElectionRetryPeriod = 2 * time.Second
)

// SetDefaults applies default values to unset configuration fields.
// This modifies the config in-place and should be called after parsing
// the configuration and before validation.
//
// Port Handling Strategy:
//   - A value of 0 for production ports (healthz, metrics, dataplane) means "uninitialized"
//     and will be replaced with the default value
//   - Debug ports may intentionally be 0 to indicate "disabled" (see cmd/controller/main.go)
//   - After defaults are applied, production ports MUST NOT be 0 (validation will catch this)
//
// Most callers should use LoadConfig() instead. This function is primarily
// useful for testing default application independently from YAML parsing.
func SetDefaults(cfg *Config) {
	// Leader election defaults
	// Note: Enabled defaults to true (zero value for bool is false, so we set it explicitly)
	if cfg.Controller.LeaderElection.LeaseName == "" {
		cfg.Controller.LeaderElection.LeaseName = DefaultLeaderElectionLeaseName
	}
	if cfg.Controller.LeaderElection.LeaseDuration == "" {
		cfg.Controller.LeaderElection.LeaseDuration = DefaultLeaderElectionLeaseDuration.String()
	}
	if cfg.Controller.LeaderElection.RenewDeadline == "" {
		cfg.Controller.LeaderElection.RenewDeadline = DefaultLeaderElectionRenewDeadline.String()
	}
	if cfg.Controller.LeaderElection.RetryPeriod == "" {
		cfg.Controller.LeaderElection.RetryPeriod = DefaultLeaderElectionRetryPeriod.String()
	}

	// Logging defaults
	// Note: Empty Level is valid (means use LOG_LEVEL env var or default to INFO)

	// Dataplane defaults
	// Note: This port should never remain 0 after defaults are applied
	if cfg.Dataplane.Port == 0 {
		cfg.Dataplane.Port = DefaultDataplanePort
	}

	// Apply dataplane path defaults
	if cfg.Dataplane.MapsDir == "" {
		cfg.Dataplane.MapsDir = DefaultDataplaneMapsDir
	}
	if cfg.Dataplane.SSLCertsDir == "" {
		cfg.Dataplane.SSLCertsDir = DefaultDataplaneSSLCertsDir
	}
	if cfg.Dataplane.GeneralStorageDir == "" {
		cfg.Dataplane.GeneralStorageDir = DefaultDataplaneGeneralStorageDir
	}
	if cfg.Dataplane.ConfigFile == "" {
		cfg.Dataplane.ConfigFile = DefaultDataplaneConfigFile
	}

	// Watched resources defaults
	// Note: EnableValidationWebhook defaults to false (zero value) which is correct
	// IndexBy must be explicitly configured, no default
}

// GetMinDeploymentInterval returns the configured minimum deployment interval
// or the default if not specified or invalid.
func (d *DataplaneConfig) GetMinDeploymentInterval() time.Duration {
	if d.MinDeploymentInterval != "" {
		if duration, err := time.ParseDuration(d.MinDeploymentInterval); err == nil {
			return duration
		}
	}
	return DefaultMinDeploymentInterval
}

// GetDriftPreventionInterval returns the configured drift prevention interval
// or the default if not specified or invalid.
func (d *DataplaneConfig) GetDriftPreventionInterval() time.Duration {
	if d.DriftPreventionInterval != "" {
		if duration, err := time.ParseDuration(d.DriftPreventionInterval); err == nil {
			return duration
		}
	}
	return DefaultDriftPreventionInterval
}

// GetDeploymentTimeout returns the configured deployment timeout
// or the default if not specified or invalid.
func (d *DataplaneConfig) GetDeploymentTimeout() time.Duration {
	if d.DeploymentTimeout != "" {
		if duration, err := time.ParseDuration(d.DeploymentTimeout); err == nil {
			return duration
		}
	}
	return DefaultDeploymentTimeout
}

// GetLeaseDuration returns the configured lease duration
// or the default if not specified or invalid.
func (le *LeaderElectionConfig) GetLeaseDuration() time.Duration {
	if le.LeaseDuration != "" {
		if duration, err := time.ParseDuration(le.LeaseDuration); err == nil {
			return duration
		}
	}
	return DefaultLeaderElectionLeaseDuration
}

// GetRenewDeadline returns the configured renew deadline
// or the default if not specified or invalid.
func (le *LeaderElectionConfig) GetRenewDeadline() time.Duration {
	if le.RenewDeadline != "" {
		if duration, err := time.ParseDuration(le.RenewDeadline); err == nil {
			return duration
		}
	}
	return DefaultLeaderElectionRenewDeadline
}

// GetRetryPeriod returns the configured retry period
// or the default if not specified or invalid.
func (le *LeaderElectionConfig) GetRetryPeriod() time.Duration {
	if le.RetryPeriod != "" {
		if duration, err := time.ParseDuration(le.RetryPeriod); err == nil {
			return duration
		}
	}
	return DefaultLeaderElectionRetryPeriod
}
