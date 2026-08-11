package validator

import (
	"context"
	"log/slog"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
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
	eventBus  *busevents.EventBus
	logger    *slog.Logger
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
		eventBus:  eventBus,
		logger:    logger,
		bootstrap: bootstrap,
	}
	v.BaseValidator = NewBaseValidator(eventBus, logger, ValidatorNameTemplate, v)
	return v
}

// HandleRequest processes a ConfigValidationRequest by validating all templates.
// This implements the ValidationHandler interface.
//
// Templates are validated together as a complete set, matching production behavior.
// This ensures snippets that reference each other via render/import work correctly.
//
// Schema acquisition for the request's watched resources happens
// synchronously here so the engine compile sees the same typed
// globals the Stage-5 production engine will see. A failure to
// resolve every declared resource's schema fails validation —
// template authors using typed access need the guarantee that
// every declared watched resource has its real schema (RBAC, CRD
// installation, apiserver health are all surfaced via this gate).
func (v *TemplateValidator) HandleRequest(req *events.ConfigValidationRequest) {
	start := time.Now()
	v.logger.Debug("Validating templates", "version", req.Version)

	cfg, ok := v.assertConfigType(req)
	if !ok {
		return
	}

	var errors []string

	// Resolve real typed reflect.Types via the injected bootstrapper.
	// Failure here is a hard validation failure: the chart cannot be
	// safely admitted without verifying every watched resource has
	// its real schema, because typed Spec/Status access in any
	// template would silently rebind to a mismatched / fallback
	// shape downstream.
	bootstrapCtx, cancel := context.WithTimeout(v.LifecycleContext(), templateValidatorBootstrapTimeout)
	defer cancel()
	bootstrapResult, bootstrapErr := v.bootstrap(bootstrapCtx, cfg)
	if bootstrapErr != nil {
		errors = append(errors,
			"schema acquisition failed for one or more watched resources "+
				"(typed template access cannot be validated without real schemas): "+
				bootstrapErr.Error())
	}

	extraction := helpers.ExtractTemplatesFromConfig(cfg)

	// Compile only if schemas resolved — otherwise the engine
	// compile would itself fail or, worse, succeed against a
	// partial declaration set and let typed-access errors slip
	// through. Recording the bootstrap error in `errors` above is
	// enough operator signal.
	if bootstrapErr == nil {
		additionalDeclarations := helpers.BuildAdditionalDeclarations(cfg, bootstrapResult)
		if _, err := templating.New(extraction.AllTemplates, &templating.Options{EntryPoints: extraction.EntryPoints, Declarations: additionalDeclarations}); err != nil {
			errors = append(errors, templating.FormatCompilationError(err, "templates", ""))
		}
	}

	valid := len(errors) == 0
	response := events.NewConfigValidationResponse(
		req.RequestID(),
		ValidatorNameTemplate,
		valid,
		errors,
	)

	v.eventBus.Publish(response)

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

// templateValidatorBootstrapTimeout caps the wall-clock cost of
// schema resolution during template validation. Validation is on
// the config-change hot path (HAProxyTemplateConfig admission /
// reload), so a slow apiserver mustn't block validation
// indefinitely. The deadline only fires when the cluster is
// degraded — at which point validation correctly fails with a
// clear "schema acquisition failed" reason so the operator
// investigates RBAC / CRD installation / apiserver health.
const templateValidatorBootstrapTimeout = 5 * time.Second
