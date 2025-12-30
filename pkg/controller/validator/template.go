package validator

import (
	"fmt"
	"log/slog"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// TemplateValidator validates template syntax in configuration.
//
// This component subscribes to ConfigValidationRequest events and validates
// all templates together as a complete set. It uses helpers.ExtractTemplatesFromConfig
// to ensure validation matches production behavior exactly (DRY principle).
//
// Templates are validated together, not in isolation, so snippets that reference
// each other via render, import, or inherit_context work correctly.
//
// This component is part of the scatter-gather validation pattern and publishes
// ConfigValidationResponse events with validation results.
type TemplateValidator struct {
	*BaseValidator
	eventBus *busevents.EventBus
	logger   *slog.Logger
}

// NewTemplateValidator creates a new template validator component.
//
// Parameters:
//   - eventBus: The EventBus to subscribe to and publish on
//   - logger: Structured logger for diagnostics
//
// Returns:
//   - *TemplateValidator ready to start
func NewTemplateValidator(eventBus *busevents.EventBus, logger *slog.Logger) *TemplateValidator {
	v := &TemplateValidator{
		eventBus: eventBus,
		logger:   logger,
	}
	v.BaseValidator = NewBaseValidator(eventBus, logger, ValidatorNameTemplate, "Template syntax validator", v)
	return v
}

// HandleRequest processes a ConfigValidationRequest by validating all templates.
// This implements the ValidationHandler interface.
//
// Templates are validated together as a complete set, matching production behavior.
// This ensures snippets that reference each other via render/import work correctly.
func (v *TemplateValidator) HandleRequest(req *events.ConfigValidationRequest) {
	start := time.Now()
	v.logger.Debug("Validating templates", "version", req.Version)

	// Type-assert config to *coreconfig.Config
	cfg, ok := req.Config.(*coreconfig.Config)
	if !ok {
		v.logger.Error("ConfigValidationRequest contains invalid config type",
			"expected", "*coreconfig.Config",
			"got", fmt.Sprintf("%T", req.Config))

		// Publish response with error and return early - no further validation possible
		response := events.NewConfigValidationResponse(
			req.RequestID(),
			ValidatorNameTemplate,
			false,
			[]string{fmt.Sprintf("invalid config type: %T", req.Config)},
		)
		v.eventBus.Publish(response)
		return
	}

	// Collect all validation errors
	var errors []string

	// Use helpers.ExtractTemplatesFromConfig to get all templates and entry points
	// This matches exactly what production does - DRY principle
	extraction := helpers.ExtractTemplatesFromConfig(cfg)

	// Validate by creating engine with complete template set
	// This ensures snippets that reference each other (via render, import, inherit_context) work correctly
	_, err := templating.NewScriggo(
		extraction.AllTemplates,
		extraction.EntryPoints,
		nil, nil, nil,
	)
	if err != nil {
		formattedErr := templating.FormatCompilationError(err, "templates", "")
		errors = append(errors, formattedErr)
	}

	// Publish validation response
	valid := len(errors) == 0
	response := events.NewConfigValidationResponse(
		req.RequestID(),
		ValidatorNameTemplate,
		valid,
		errors,
	)

	v.eventBus.Publish(response)

	// Calculate metrics
	duration := time.Since(start)
	templateCount := len(extraction.AllTemplates)

	if valid {
		v.logger.Debug("Template validation successful",
			"version", req.Version,
			"duration_ms", duration.Milliseconds(),
			"template_count", templateCount)
	} else {
		v.logger.Error("Template validation failed",
			"version", req.Version,
			"duration_ms", duration.Milliseconds(),
			"template_count", templateCount,
			"error_count", len(errors),
			"errors", errors)
	}
}
