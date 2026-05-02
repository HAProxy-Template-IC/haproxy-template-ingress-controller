package validator

import (
	"log/slog"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
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
	eventBus *busevents.EventBus
	logger   *slog.Logger
}

// NewBasicValidator creates a new basic validator component.
//
// Parameters:
//   - eventBus: The EventBus to subscribe to and publish on
//   - logger: Structured logger for diagnostics
//
// Returns:
//   - *BasicValidator ready to start
func NewBasicValidator(eventBus *busevents.EventBus, logger *slog.Logger) *BasicValidator {
	v := &BasicValidator{
		eventBus: eventBus,
		logger:   logger,
	}
	v.BaseValidator = NewBaseValidator(eventBus, logger, ValidatorNameBasic, v)
	return v
}

// HandleRequest processes a ConfigValidationRequest by validating basic structure.
// This implements the ValidationHandler interface.
func (v *BasicValidator) HandleRequest(req *events.ConfigValidationRequest) {
	start := time.Now()
	v.logger.Debug("Validating basic structure", "version", req.Version)

	cfg, ok := v.assertConfigType(req)
	if !ok {
		return
	}

	// Validate basic structure using existing validation function
	var errors []string
	if err := coreconfig.ValidateStructure(cfg); err != nil {
		errors = append(errors, err.Error())
	}

	// Publish validation response
	valid := len(errors) == 0
	response := events.NewConfigValidationResponse(
		req.RequestID(),
		ValidatorNameBasic,
		valid,
		errors,
	)

	v.eventBus.Publish(response)

	duration := time.Since(start)
	if valid {
		v.logger.Debug("Basic validation successful",
			"version", req.Version,
			"duration_ms", duration.Milliseconds())
	} else {
		v.logger.Warn("Basic validation failed",
			"version", req.Version,
			"duration_ms", duration.Milliseconds(),
			"error_count", len(errors))
	}
}
