package validator

import (
	"context"
	"log/slog"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// TypeBootstrapper runs the schema-acquisition pipeline for a
// candidate config and returns the resolved typed reflect.Types
// for each watched resource. Used by [TemplateValidator] so its
// engine compile sees the same typed globals the Stage-5
// production engine will see — without this, a chart that uses
// typed Spec/Status access (e.g. `gw.Spec.Listeners`) would be
// false-positively rejected at Stage 1 against an envelope-only
// declaration set.
//
// Production wiring binds this to controller.runTypeBootstrap
// with the iteration's K8s client captured in the closure;
// tests pass a stub that returns a Result built from in-memory
// schemas.
type TypeBootstrapper func(ctx context.Context, cfg *coreconfig.Config) (*typebootstrap.Result, error)

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
	bootstrap TypeBootstrapper
}

// NewTemplateValidator creates a new template validator component.
//
// Parameters:
//   - eventBus:   the EventBus to subscribe to and publish on
//   - logger:     structured logger for diagnostics
//   - bootstrap:  resolver for typed reflect.Types per watched
//     resource. MUST be non-nil — without real types the engine
//     compile would degrade to envelope-only declarations and
//     false-positively reject any chart that uses typed Spec /
//     Status access. Tests pass a stub returning an in-memory
//     Result; production passes a closure around
//     controller.runTypeBootstrap.
func NewTemplateValidator(eventBus *busevents.EventBus, logger *slog.Logger, bootstrap TypeBootstrapper) *TemplateValidator {
	if bootstrap == nil {
		panic("validator: NewTemplateValidator requires non-nil TypeBootstrapper " +
			"— envelope-only fallback was removed because it false-positively " +
			"rejects valid charts that use typed Spec/Status access")
	}
	v := &TemplateValidator{
		bootstrap: bootstrap,
	}
	v.BaseValidator = NewBaseValidator(eventBus, logger, ValidatorNameTemplate, v)
	return v
}

// Validate implements ValidationHandler.
func (v *TemplateValidator) Validate(ctx context.Context, cfg *coreconfig.Config, version string) (valid bool, errors []string) {
	start := time.Now()
	v.Logger().Debug("Validating templates", "version", version)

	errors = validateTemplates(ctx, cfg, v.bootstrap)
	extraction := helpers.ExtractTemplatesFromConfig(cfg)

	valid = len(errors) == 0
	duration := time.Since(start)
	templateCount := len(extraction.AllTemplates)

	if valid {
		v.Logger().Debug("Template validation successful",
			"version", version,
			"duration_ms", duration.Milliseconds(),
			"template_count", templateCount)
	} else {
		v.Logger().Error("Template validation failed",
			"version", version,
			"duration_ms", duration.Milliseconds(),
			"template_count", templateCount,
			"error_count", len(errors),
			"errors", errors)
	}
	return valid, errors
}

// templateValidatorBootstrapTimeout caps the wall-clock cost of
// schema resolution during template validation. Validation is on
// the config-change hot path (HAProxyTemplateConfig admission /
// reload), so a slow apiserver mustn't block validation
// indefinitely. The deadline only fires when the cluster is
// degraded — at which point validation correctly fails with a
// clear "schema acquisition failed" reason so the operator
// investigates RBAC / CRD installation / apiserver health.
const templateValidatorBootstrapTimeout = 5 * time.Second

func validateTemplates(ctx context.Context, cfg *coreconfig.Config, bootstrap TypeBootstrapper) []string {
	bootstrapCtx, cancel := context.WithTimeout(ctx, templateValidatorBootstrapTimeout)
	defer cancel()
	bootstrapResult, err := bootstrap(bootstrapCtx, cfg)
	if err != nil {
		return []string{
			"schema acquisition failed for one or more watched resources " +
				"(typed template access cannot be validated without real schemas): " + err.Error(),
		}
	}

	extraction := helpers.ExtractTemplatesFromConfig(cfg)
	declarations := helpers.BuildAdditionalDeclarations(cfg, bootstrapResult)
	if _, err := templating.New(extraction.AllTemplates, &templating.Options{
		EntryPoints:                   extraction.EntryPoints,
		IncrementalEntryPoints:        extraction.IncrementalEntryPoints,
		IncrementalBindingEntryPoints: extraction.IncrementalBindingEntryPoints,
		Declarations:                  declarations,
	}); err != nil {
		return []string{templating.FormatCompilationError(err, "templates", "")}
	}
	return nil
}
