// Package config provides configuration loading and validation.
package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// LoadConfig parses YAML configuration and applies default values.
// This is the recommended function for loading configuration.
//
// It performs two operations atomically:
//  1. Parses YAML into Config struct
//  2. Applies default values to unset fields
//
// Example:
//
//	cfg, err := config.LoadConfig(yamlString)
//	if err != nil {
//	    return err
//	}
//	// cfg now has defaults applied and is ready for validation
func LoadConfig(configYAML string) (*Config, error) {
	cfg, err := parseConfig(configYAML)
	if err != nil {
		return nil, err
	}

	SetDefaults(cfg)

	// Validate ExtraContext contains only JSON-compatible types
	if cfg.TemplatingSettings.ExtraContext != nil {
		if err := ValidateExtraContext(cfg.TemplatingSettings.ExtraContext); err != nil {
			return nil, fmt.Errorf("invalid extra_context: %w", err)
		}
	}

	return cfg, nil
}

// parseConfig parses YAML configuration into a Config struct.
// This is a pure function that only parses YAML - it does not load from
// Kubernetes, apply defaults, or perform validation.
//
// Most callers should use LoadConfig() instead. This function is primarily
// useful for testing parse behavior independently from default application.
func parseConfig(configYAML string) (*Config, error) {
	if configYAML == "" {
		return nil, fmt.Errorf("config YAML is empty")
	}

	var cfg Config
	if err := yaml.Unmarshal([]byte(configYAML), &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal YAML: %w", err)
	}

	return &cfg, nil
}

// LoadCredentials parses Secret data into a Credentials struct.
// This is a pure function that extracts credentials from Secret data.
// It does not load from Kubernetes or perform validation.
//
// Expected Secret keys: dataplane_username, dataplane_password.
func LoadCredentials(secretData map[string][]byte) (*Credentials, error) {
	if secretData == nil {
		return nil, fmt.Errorf("secret data is nil")
	}

	// Extract required fields
	dataplaneUsername, ok := secretData["dataplane_username"]
	if !ok || len(dataplaneUsername) == 0 {
		return nil, fmt.Errorf("missing required secret key: dataplane_username")
	}

	dataplanePassword, ok := secretData["dataplane_password"]
	if !ok || len(dataplanePassword) == 0 {
		return nil, fmt.Errorf("missing required secret key: dataplane_password")
	}

	return &Credentials{
		DataplaneUsername: string(dataplaneUsername),
		DataplanePassword: string(dataplanePassword),
	}, nil
}
