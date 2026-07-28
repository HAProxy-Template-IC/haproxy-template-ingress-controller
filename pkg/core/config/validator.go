package config

import (
	"errors"
	"fmt"
	"strings"
)

// logLevelInfo is the canonical default log level; also keeps the repeated
// "INFO" literal (validator + tests) constified for goconst.
const logLevelInfo = "INFO"

// ValidateStructure performs basic structural validation on the configuration.
// Validates required fields, value ranges, and non-empty slices.
// Does NOT validate template syntax or JSONPath expressions.
func ValidateStructure(cfg *Config) error {
	if cfg == nil {
		return errors.New("config is nil")
	}

	// Completeness first: a missing podSelector or template is more fundamental
	// than a mistuned port, and reporting the port instead would bury it.
	if err := ValidateMergedCompleteness(cfg); err != nil {
		return err
	}

	// Controller config (currently only LeaderElection, which has its own
	// defaults and is validated at runtime when its durations are parsed) needs
	// no structural validation here.

	// Validate Logging config
	if err := validateLoggingConfig(&cfg.Logging); err != nil {
		return fmt.Errorf("logging: %w", err)
	}

	// Validate Dataplane config
	if err := validateDataplaneConfig(&cfg.Dataplane); err != nil {
		return fmt.Errorf("dataplane: %w", err)
	}

	// Validate WatchedResources (full per-resource checks; the prospective gate
	// below only enforces that at least one exists)
	if err := validateWatchedResources(cfg.WatchedResources); err != nil {
		return fmt.Errorf("watched_resources: %w", err)
	}

	// Validate requires references (snippets/tests → watched resources)
	return validateRequires(cfg)
}

// ValidateMergedCompleteness checks the requirements the CRD schema can no
// longer express, because a single HAProxyTemplateConfig of a merged set is
// legitimately incomplete (ADR-0014): a template-library object carries only
// template content, and several libraries each contribute part of the shared
// `_global` validationTests baseline.
//
// It is deliberately narrower than ValidateStructure: it covers exactly the
// fields whose `+kubebuilder:validation:Required` markers were dropped, so the
// gates that judge a *prospective* config — the admission webhook and
// `controller validate` — restore the apiserver-side guarantee without also
// demanding the runtime defaults (dataplane directories, ports) that the chart
// supplies but a hand-written config need not.
func ValidateMergedCompleteness(cfg *Config) error {
	if cfg == nil {
		return errors.New("config is nil")
	}

	if err := validatePodSelector(&cfg.PodSelector); err != nil {
		return fmt.Errorf("pod_selector: %w", err)
	}

	// Only the MinProperties=1 the CRD used to carry. The per-resource checks
	// in validateWatchedResources (indexBy and friends) were never expressed in
	// the schema, so applying them to a prospective config would tighten
	// admission for reasons unrelated to the merge.
	if len(cfg.WatchedResources) == 0 {
		return errors.New("watched_resources: at least one resource must be configured")
	}

	if err := validateHAProxyConfig(&cfg.HAProxyConfig); err != nil {
		return fmt.Errorf("haproxy_config: %w", err)
	}

	return validateValidationTests(cfg)
}

// GlobalValidationTestName is the reserved validationTests entry that is a
// shared baseline rather than a test: it contributes fixtures and an
// extraContext baseline to every other test, and the runner never executes its
// assertions.
const GlobalValidationTestName = "_global"

// validateValidationTests rejects a test that would silently pass because it
// asserts nothing.
//
// The CRD schema cannot express this. Several template libraries each
// contribute part of the shared `_global` baseline, so each of their objects
// carries an incomplete `_global` that only becomes whole after the merge —
// which is why `assertions` is optional in the schema and enforced here, on the
// merged config, instead.
func validateValidationTests(cfg *Config) error {
	for name := range cfg.ValidationTests {
		if name == GlobalValidationTestName {
			continue
		}
		if len(cfg.ValidationTests[name].Assertions) == 0 {
			return fmt.Errorf("validation_tests.%s: must declare at least one assertion "+
				"(a test with none passes without checking anything)", name)
		}
	}
	return nil
}

// validateRequires checks that every `requires` entry on templateSnippets and
// validationTests names an existing watchedResources key, and that every
// `requiresFields` entry on validationTests is of the form
// "<watchedResource>.<field.path>" with an existing watchedResources key as
// its first dot-segment. A dangling entry would silently never strip (the
// availability / schema-field check could not match it), so it is rejected at
// load time instead.
func validateRequires(cfg *Config) error {
	for name, snippet := range cfg.TemplateSnippets {
		for _, req := range snippet.Requires {
			if _, ok := cfg.WatchedResources[req]; !ok {
				return fmt.Errorf("template_snippets.%s: requires %q does not name a watched resource", name, req)
			}
		}
	}
	for name := range cfg.ValidationTests {
		test := cfg.ValidationTests[name]
		if err := validateTestRequires(cfg, name, &test); err != nil {
			return err
		}
	}
	return nil
}

// validateTestRequires checks one validation test's requires and
// requiresFields entries against the watchedResources keys.
func validateTestRequires(cfg *Config, name string, test *ValidationTest) error {
	for _, req := range test.Requires {
		if _, ok := cfg.WatchedResources[req]; !ok {
			return fmt.Errorf("validation_tests.%s: requires %q does not name a watched resource", name, req)
		}
	}
	for _, entry := range test.RequiresFields {
		key, fieldPath, ok := strings.Cut(entry, ".")
		if !ok || fieldPath == "" {
			return fmt.Errorf("validation_tests.%s: requiresFields entry %q must be of the form \"<watchedResource>.<field.path>\"", name, entry)
		}
		if _, ok := cfg.WatchedResources[key]; !ok {
			return fmt.Errorf("validation_tests.%s: requiresFields entry %q does not name a watched resource (first segment %q)", name, entry, key)
		}
	}
	return nil
}

// validatePodSelector validates the pod selector configuration.
func validatePodSelector(ps *PodSelector) error {
	if len(ps.MatchLabels) == 0 {
		return errors.New("match_labels cannot be empty")
	}

	for key, value := range ps.MatchLabels {
		if key == "" {
			return errors.New("match_labels key cannot be empty")
		}
		if value == "" {
			return fmt.Errorf("match_labels value for key %q cannot be empty", key)
		}
	}

	return nil
}

// validateLoggingConfig validates the logging configuration.
func validateLoggingConfig(lc *LoggingConfig) error {
	if lc.Level == "" {
		return nil // Empty is valid - means use LOG_LEVEL env var or default
	}

	switch strings.ToUpper(lc.Level) {
	case "TRACE", "DEBUG", logLevelInfo, "WARN", "WARNING", "ERROR":
		// WARNING is accepted as an alias for WARN.
		return nil
	default:
		return fmt.Errorf("level must be TRACE, DEBUG, INFO, WARN, or ERROR (case-insensitive), got %q", lc.Level)
	}
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
		return errors.New("at least one resource must be configured")
	}

	for name := range resources {
		resource := resources[name]
		if err := validateWatchedResource(name, &resource); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}

	return nil
}

// validateWatchedResource validates a single watched resource configuration.
func validateWatchedResource(name string, resource *WatchedResource) error {
	if resource.APIVersion == "" && len(resource.APIVersions) == 0 {
		return fmt.Errorf("resource %q: one of api_version or api_versions must be set", name)
	}
	if resource.APIVersion != "" && len(resource.APIVersions) > 0 {
		return fmt.Errorf("resource %q: api_version and api_versions are mutually exclusive", name)
	}
	for i, v := range resource.APIVersions {
		if v == "" {
			return fmt.Errorf("resource %q: api_versions[%d] cannot be empty", name, i)
		}
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
		return errors.New("template cannot be empty")
	}

	return nil
}

// ValidateCredentials ensures all required credential fields are present and non-empty.
func ValidateCredentials(creds *Credentials) error {
	if creds == nil {
		return errors.New("credentials are nil")
	}

	if creds.DataplaneUsername == "" {
		return errors.New("dataplane_username cannot be empty")
	}

	if creds.DataplanePassword == "" {
		return errors.New("dataplane_password cannot be empty")
	}

	return nil
}
