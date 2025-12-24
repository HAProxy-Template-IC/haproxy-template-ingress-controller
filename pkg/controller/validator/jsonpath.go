package validator

import (
	"fmt"
	"log/slog"
	"sort"
	"time"

	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/controller/events"
	coreconfig "gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/core/config"
	busevents "gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/events"
	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/k8s/indexer"
)

// JSONPathValidator validates JSONPath expressions in configuration.
//
// This component subscribes to ConfigValidationRequest events and validates
// all JSONPath expressions in the configuration using the k8s indexer package.
//
// Validated fields:
// - WatchedResourcesIgnoreFields (all expressions)
// - WatchedResources[*].IndexBy (all expressions)
//
// This component is part of the scatter-gather validation pattern and publishes
// ConfigValidationResponse events with validation results.
type JSONPathValidator struct {
	*BaseValidator
	eventBus *busevents.EventBus
	logger   *slog.Logger
}

// NewJSONPathValidator creates a new JSONPath validator component.
//
// Parameters:
//   - eventBus: The EventBus to subscribe to and publish on
//   - logger: Structured logger for diagnostics
//
// Returns:
//   - *JSONPathValidator ready to start
func NewJSONPathValidator(eventBus *busevents.EventBus, logger *slog.Logger) *JSONPathValidator {
	v := &JSONPathValidator{
		eventBus: eventBus,
		logger:   logger,
	}
	v.BaseValidator = NewBaseValidator(eventBus, logger, ValidatorNameJSONPath, "JSONPath expression validator", v)
	return v
}

// HandleRequest processes a ConfigValidationRequest by validating all JSONPath expressions.
// This implements the ValidationHandler interface.
func (v *JSONPathValidator) HandleRequest(req *events.ConfigValidationRequest) {
	start := time.Now()
	v.logger.Debug("Validating JSONPath expressions", "version", req.Version)

	// Type-assert config to *coreconfig.Config
	cfg, ok := req.Config.(*coreconfig.Config)
	if !ok {
		v.logger.Error("ConfigValidationRequest contains invalid config type",
			"expected", "*coreconfig.Config",
			"got", fmt.Sprintf("%T", req.Config))

		// Publish response with error and return early - no further validation possible
		response := events.NewConfigValidationResponse(
			req.RequestID(),
			ValidatorNameJSONPath,
			false,
			[]string{fmt.Sprintf("invalid config type: %T", req.Config)},
		)
		v.eventBus.Publish(response)
		return
	}

	// Collect all validation errors
	var errors []string

	// Validate WatchedResourcesIgnoreFields expressions
	// Note: Empty expression validation is handled by indexer.ValidateJSONPath()
	for i, expr := range cfg.WatchedResourcesIgnoreFields {
		if err := indexer.ValidateJSONPath(expr); err != nil {
			errors = append(errors, fmt.Sprintf("watched_resources_ignore_fields[%d]: %v", i, err))
		}
	}

	// Validate IndexBy expressions for each watched resource
	// Note: Empty expression validation is handled by indexer.ValidateJSONPath()
	// Sort keys for deterministic error ordering
	resourceNames := make([]string, 0, len(cfg.WatchedResources))
	for resourceName := range cfg.WatchedResources {
		resourceNames = append(resourceNames, resourceName)
	}
	sort.Strings(resourceNames)
	for _, resourceName := range resourceNames {
		resource := cfg.WatchedResources[resourceName]
		for i, expr := range resource.IndexBy {
			if err := indexer.ValidateJSONPath(expr); err != nil {
				errors = append(errors, fmt.Sprintf("watched_resources.%s.index_by[%d]: %v", resourceName, i, err))
			}
		}
	}

	// Publish validation response
	valid := len(errors) == 0
	response := events.NewConfigValidationResponse(
		req.RequestID(),
		ValidatorNameJSONPath,
		valid,
		errors,
	)

	v.eventBus.Publish(response)

	// Calculate metrics
	duration := time.Since(start)
	expressionCount := len(cfg.WatchedResourcesIgnoreFields)
	for _, resource := range cfg.WatchedResources {
		expressionCount += len(resource.IndexBy)
	}

	if valid {
		v.logger.Debug("JSONPath validation successful",
			"version", req.Version,
			"duration_ms", duration.Milliseconds(),
			"expression_count", expressionCount)
	} else {
		v.logger.Warn("JSONPath validation failed",
			"version", req.Version,
			"duration_ms", duration.Milliseconds(),
			"expression_count", expressionCount,
			"error_count", len(errors))
	}
}
