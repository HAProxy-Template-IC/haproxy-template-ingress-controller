package validator

import (
	"fmt"
	"log/slog"
	"sort"
	"time"

	"haproxy-template-ic/pkg/controller/events"
	coreconfig "haproxy-template-ic/pkg/core/config"
	busevents "haproxy-template-ic/pkg/events"
	"haproxy-template-ic/pkg/templating"
)

// TemplateValidator validates template syntax in configuration.
//
// This component subscribes to ConfigValidationRequest events and validates
// all template fields in the configuration using the templating package.
//
// Validated fields:
// - HAProxyConfig.Template (main HAProxy config template)
// - TemplateSnippets (all snippet templates)
// - Maps (all map file templates)
// - Files (all general file templates)
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

	// Validate main HAProxy config template
	// Note: Empty template validation is handled by basic validator (required field check)
	// Template validator validates syntax of all templates, including empty ones (which are valid)
	if err := templating.ValidateTemplate(cfg.HAProxyConfig.Template, templating.EngineTypeScriggo); err != nil {
		errors = append(errors, fmt.Sprintf("haproxy_config.template: %v", err))
	}

	// Validate template snippets
	// Sort keys for deterministic error ordering
	snippetNames := make([]string, 0, len(cfg.TemplateSnippets))
	for name := range cfg.TemplateSnippets {
		snippetNames = append(snippetNames, name)
	}
	sort.Strings(snippetNames)
	for _, name := range snippetNames {
		snippet := cfg.TemplateSnippets[name]
		if err := templating.ValidateTemplate(snippet.Template, templating.EngineTypeScriggo); err != nil {
			errors = append(errors, fmt.Sprintf("template_snippets.%s: %v", name, err))
		}
	}

	// Validate map file templates
	// Sort keys for deterministic error ordering
	mapNames := make([]string, 0, len(cfg.Maps))
	for name := range cfg.Maps {
		mapNames = append(mapNames, name)
	}
	sort.Strings(mapNames)
	for _, name := range mapNames {
		mapFile := cfg.Maps[name]
		if err := templating.ValidateTemplate(mapFile.Template, templating.EngineTypeScriggo); err != nil {
			errors = append(errors, fmt.Sprintf("maps.%s.template: %v", name, err))
		}
	}

	// Validate general file templates
	// Sort keys for deterministic error ordering
	fileNames := make([]string, 0, len(cfg.Files))
	for name := range cfg.Files {
		fileNames = append(fileNames, name)
	}
	sort.Strings(fileNames)
	for _, name := range fileNames {
		file := cfg.Files[name]
		if err := templating.ValidateTemplate(file.Template, templating.EngineTypeScriggo); err != nil {
			errors = append(errors, fmt.Sprintf("files.%s.template: %v", name, err))
		}
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
	templateCount := 1 + len(cfg.TemplateSnippets) + len(cfg.Maps) + len(cfg.Files)

	if valid {
		v.logger.Debug("Template validation successful",
			"version", req.Version,
			"duration_ms", duration.Milliseconds(),
			"template_count", templateCount)
	} else {
		v.logger.Warn("Template validation failed",
			"version", req.Version,
			"duration_ms", duration.Milliseconds(),
			"template_count", templateCount,
			"error_count", len(errors))
	}
}
