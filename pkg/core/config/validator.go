package config

import (
	"fmt"
)

// ValidateStructure performs basic structural validation on the configuration.
// Validates required fields, value ranges, and non-empty slices.
// Does NOT validate template syntax or JSONPath expressions.
func ValidateStructure(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}

	// Validate PodSelector
	if err := validatePodSelector(&cfg.PodSelector); err != nil {
		return fmt.Errorf("pod_selector: %w", err)
	}

	// Validate Controller config
	if err := validateControllerConfig(&cfg.Controller); err != nil {
		return fmt.Errorf("controller: %w", err)
	}

	// Validate Logging config
	if err := validateLoggingConfig(&cfg.Logging); err != nil {
		return fmt.Errorf("logging: %w", err)
	}

	// Validate Dataplane config
	if err := validateDataplaneConfig(&cfg.Dataplane); err != nil {
		return fmt.Errorf("dataplane: %w", err)
	}

	// Validate WatchedResources
	if err := validateWatchedResources(cfg.WatchedResources); err != nil {
		return fmt.Errorf("watched_resources: %w", err)
	}

	// Validate HAProxyConfig
	if err := validateHAProxyConfig(&cfg.HAProxyConfig); err != nil {
		return fmt.Errorf("haproxy_config: %w", err)
	}

	return nil
}

// validatePodSelector validates the pod selector configuration.
func validatePodSelector(ps *PodSelector) error {
	if len(ps.MatchLabels) == 0 {
		return fmt.Errorf("match_labels cannot be empty")
	}

	for key, value := range ps.MatchLabels {
		if key == "" {
			return fmt.Errorf("match_labels key cannot be empty")
		}
		if value == "" {
			return fmt.Errorf("match_labels value for key %q cannot be empty", key)
		}
	}

	return nil
}

// validateControllerConfig validates the controller configuration.
func validateControllerConfig(oc *ControllerConfig) error {
	if oc.HealthzPort < 1 || oc.HealthzPort > 65535 {
		return fmt.Errorf("healthz_port must be between 1 and 65535, got %d", oc.HealthzPort)
	}

	if oc.MetricsPort < 1 || oc.MetricsPort > 65535 {
		return fmt.Errorf("metrics_port must be between 1 and 65535, got %d", oc.MetricsPort)
	}

	if oc.HealthzPort == oc.MetricsPort {
		return fmt.Errorf("healthz_port and metrics_port cannot be the same (%d)", oc.HealthzPort)
	}

	return nil
}

// validateLoggingConfig validates the logging configuration.
func validateLoggingConfig(lc *LoggingConfig) error {
	if lc.Level == "" {
		return nil // Empty is valid - means use LOG_LEVEL env var or default
	}

	validLevels := map[string]bool{
		"TRACE": true, "DEBUG": true, "INFO": true, "WARN": true, "ERROR": true,
		"trace": true, "debug": true, "info": true, "warn": true, "error": true,
		"WARNING": true, "warning": true, // Also accept WARNING as alias for WARN
	}
	if !validLevels[lc.Level] {
		return fmt.Errorf("level must be TRACE, DEBUG, INFO, WARN, or ERROR (case-insensitive), got %q", lc.Level)
	}

	return nil
}

// validateDataplaneConfig validates the dataplane configuration.
// This validation is called AFTER setDefaults(), so production ports must be 1-65535.
// A value of 0 indicates defaults were not applied properly.
func validateDataplaneConfig(dc *DataplaneConfig) error {
	// Port validation - must not be 0 after defaults
	// See pkg/core/config/defaults.go for port handling strategy
	if dc.Port < 1 || dc.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535 (got %d, expected default %d)", dc.Port, DefaultDataplanePort)
	}

	// Path validations - must not be empty after defaults
	if dc.MapsDir == "" {
		return fmt.Errorf("maps_dir cannot be empty (expected default %q)", DefaultDataplaneMapsDir)
	}
	if dc.SSLCertsDir == "" {
		return fmt.Errorf("ssl_certs_dir cannot be empty (expected default %q)", DefaultDataplaneSSLCertsDir)
	}
	if dc.GeneralStorageDir == "" {
		return fmt.Errorf("general_storage_dir cannot be empty (expected default %q)", DefaultDataplaneGeneralStorageDir)
	}
	if dc.ConfigFile == "" {
		return fmt.Errorf("config_file cannot be empty (expected default %q)", DefaultDataplaneConfigFile)
	}

	return nil
}

// validateWatchedResources validates the watched resources configuration.
func validateWatchedResources(resources map[string]WatchedResource) error {
	if len(resources) == 0 {
		return fmt.Errorf("at least one resource must be configured")
	}

	for name, resource := range resources {
		if err := validateWatchedResource(name, &resource); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}

	return nil
}

// validateWatchedResource validates a single watched resource configuration.
func validateWatchedResource(name string, resource *WatchedResource) error {
	if resource.APIVersion == "" {
		return fmt.Errorf("resource %q: api_version cannot be empty", name)
	}

	if resource.Resources == "" {
		return fmt.Errorf("resource %q: resources cannot be empty", name)
	}

	if len(resource.IndexBy) == 0 {
		return fmt.Errorf("resource %q: index_by must have at least one expression", name)
	}

	// Validate that index_by expressions are not empty strings
	for i, expr := range resource.IndexBy {
		if expr == "" {
			return fmt.Errorf("resource %q: index_by[%d] cannot be empty", name, i)
		}
	}

	return nil
}

// validateHAProxyConfig validates the HAProxy configuration.
func validateHAProxyConfig(hc *HAProxyConfig) error {
	if hc.Template == "" {
		return fmt.Errorf("template cannot be empty")
	}

	return nil
}

// ValidateCredentials ensures all required credential fields are present and non-empty.
func ValidateCredentials(creds *Credentials) error {
	if creds == nil {
		return fmt.Errorf("credentials are nil")
	}

	if creds.DataplaneUsername == "" {
		return fmt.Errorf("dataplane_username cannot be empty")
	}

	if creds.DataplanePassword == "" {
		return fmt.Errorf("dataplane_password cannot be empty")
	}

	return nil
}
