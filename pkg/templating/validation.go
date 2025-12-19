package templating

// ValidateTemplate validates template syntax without executing it.
//
// This is a generic validation function that can be used anywhere template
// validation is needed. It only checks syntax correctness and does not
// execute the template or require context variables.
//
// Parameters:
//   - templateStr: The template string to validate
//   - engineType: The template engine to use (only EngineTypeScriggo is supported)
//
// Returns:
//   - An error if the template syntax is invalid or engine is unsupported
//   - nil if the template is valid
//
// Example:
//
//	err := templating.ValidateTemplate(templateStr, templating.EngineTypeScriggo)
//	if err != nil {
//	    slog.Error("Invalid template", "error", err)
//	}
func ValidateTemplate(templateStr string, engineType EngineType) error {
	// Validate engine type
	if engineType != EngineTypeScriggo {
		return NewUnsupportedEngineError(engineType)
	}

	// Create a minimal template engine to validate syntax
	templates := map[string]string{
		"validation": templateStr,
	}

	_, err := NewScriggo(templates, []string{"validation"}, nil, nil, nil)
	if err != nil {
		return err
	}

	return nil
}
