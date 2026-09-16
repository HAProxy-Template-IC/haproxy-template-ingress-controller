package validator

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"time"

	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/indexer"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// JSONPathValidator validates JSONPath expressions in configuration.
//
// This component subscribes to ConfigValidationRequest events and validates
// all JSONPath expressions in the configuration using the k8s indexer package.
//
// Validated fields:
// - WatchedResourcesIgnoreFields (all expressions)
// - WatchedResources[*].IndexBy (all expressions)
// - WatchedResources[*].FieldSelector (field.path=value format)
//
// This component is part of the scatter-gather validation pattern and publishes
// ConfigValidationResponse events with validation results.
type JSONPathValidator struct {
	*BaseValidator
}

// NewJSONPathValidator creates a new JSONPath validator component.
func NewJSONPathValidator(eventBus *busevents.EventBus, logger *slog.Logger) *JSONPathValidator {
	v := &JSONPathValidator{}
	v.BaseValidator = NewBaseValidator(eventBus, logger, ValidatorNameJSONPath, v)
	return v
}

// Validate implements ValidationHandler.
func (v *JSONPathValidator) Validate(_ context.Context, cfg *coreconfig.Config, version string) (valid bool, errors []string) {
	start := time.Now()
	v.Logger().Debug("Validating JSONPath expressions", "version", version)

	errors = validateJSONPaths(cfg)

	valid = len(errors) == 0
	duration := time.Since(start)
	expressionCount := len(cfg.WatchedResourcesIgnoreFields)
	for name := range cfg.WatchedResources {
		resource := cfg.WatchedResources[name]
		expressionCount += len(resource.IndexBy)
		if resource.FieldSelector != "" {
			expressionCount++
		}
	}
	for name := range cfg.TemplateSnippets {
		incremental := cfg.TemplateSnippets[name].Incremental
		if incremental != nil {
			expressionCount += len(incremental.WhenAnyPathExists)
		}
	}

	if valid {
		v.Logger().Debug("JSONPath validation successful",
			"version", version,
			"duration_ms", duration.Milliseconds(),
			"expression_count", expressionCount)
	} else {
		v.Logger().Warn("JSONPath validation failed",
			"version", version,
			"duration_ms", duration.Milliseconds(),
			"expression_count", expressionCount,
			"error_count", len(errors))
	}
	return valid, errors
}

func validateJSONPaths(cfg *coreconfig.Config) []string {
	var errors []string
	for i, expr := range cfg.WatchedResourcesIgnoreFields {
		if err := indexer.ValidateJSONPath(expr); err != nil {
			errors = append(errors, fmt.Sprintf("watched_resources_ignore_fields[%d]: %v", i, err))
		}
	}
	resourceNames := slices.Sorted(maps.Keys(cfg.WatchedResources))
	for _, resourceName := range resourceNames {
		resource := cfg.WatchedResources[resourceName]
		for i, expr := range resource.IndexBy {
			if err := indexer.ValidateJSONPath(expr); err != nil {
				errors = append(errors, fmt.Sprintf("watched_resources.%s.index_by[%d]: %v", resourceName, i, err))
			}
		}
		if resource.FieldSelector != "" {
			if _, err := indexer.NewFieldSelectorMatcher(resource.FieldSelector); err != nil {
				errors = append(errors, fmt.Sprintf("watched_resources.%s.field_selector: %v", resourceName, err))
			}
		}
	}
	errors = append(errors, ValidateIncrementalActivationPaths(cfg)...)
	return errors
}

// ValidateIncrementalActivationPaths validates component activation paths.
func ValidateIncrementalActivationPaths(cfg *coreconfig.Config) []string {
	var errors []string
	snippetNames := slices.Sorted(maps.Keys(cfg.TemplateSnippets))
	for _, snippetName := range snippetNames {
		incremental := cfg.TemplateSnippets[snippetName].Incremental
		if incremental == nil {
			continue
		}
		for index, path := range incremental.WhenAnyPathExists {
			if _, err := templating.CompileExistenceJSONPath(path); err != nil {
				errors = append(errors, fmt.Sprintf(
					"template_snippets.%s.incremental.when_any_path_exists[%d]: %v",
					snippetName,
					index,
					err,
				))
			}
		}
	}
	return errors
}
