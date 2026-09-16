package validator

import (
	"context"
	"log/slog"
	"time"

	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// BasicValidator validates basic structural configuration requirements.
//
// This component subscribes to ConfigValidationRequest events and validates
// basic structural requirements such as:
// - Required fields are present
// - Field types and values are correct
// - Port numbers are in valid ranges
// - Non-empty slices where required
//
// This validator uses the existing config.ValidateStructure() function and
// does NOT validate template syntax or JSONPath expressions (handled by
// specialized validators).
//
// This component is part of the scatter-gather validation pattern and publishes
// ConfigValidationResponse events with validation results.
type BasicValidator struct {
	*BaseValidator
}

// NewBasicValidator creates a new basic validator component.
func NewBasicValidator(eventBus *busevents.EventBus, logger *slog.Logger) *BasicValidator {
	v := &BasicValidator{}
	v.BaseValidator = NewBaseValidator(eventBus, logger, ValidatorNameBasic, v)
	return v
}

// Validate implements ValidationHandler.
func (v *BasicValidator) Validate(_ context.Context, cfg *coreconfig.Config, version string) (valid bool, errors []string) {
	start := time.Now()
	v.Logger().Debug("Validating basic structure", "version", version)

	errors = validateBasic(cfg)

	valid = len(errors) == 0
	duration := time.Since(start)
	if valid {
		v.Logger().Debug("Basic validation successful",
			"version", version,
			"duration_ms", duration.Milliseconds())
	} else {
		v.Logger().Warn("Basic validation failed",
			"version", version,
			"duration_ms", duration.Milliseconds(),
			"error_count", len(errors))
	}
	return valid, errors
}

func validateBasic(cfg *coreconfig.Config) []string {
	if err := coreconfig.ValidateStructure(cfg); err != nil {
		return []string{err.Error()}
	}
	return nil
}
