package dataplane

import (
	"errors"
	"fmt"
	"strings"
)

// ErrValidationCacheHit is returned when validation is skipped because the same
// configuration was already validated successfully. Callers should use the parser
// cache to obtain the parsed configuration if needed.
var ErrValidationCacheHit = errors.New("validation cache hit")

const (
	phaseNameSyntax   = "syntax"
	phaseNameSemantic = "semantic"
	configTypeCurrent = "current"
	stageApply        = "apply"

	hintCheckHAProxyLogs = "Check HAProxy logs for detailed error information"
	hintValidateConfig   = "Validate the configuration with: haproxy -c -f <config>"
)

// SyncError represents a synchronization failure with actionable context.
// It provides detailed information about what stage failed and suggestions
// for how to fix the problem.
type SyncError struct {
	// Stage indicates where the failure occurred. Common values:
	//   "connect", "parse-current", "parse-desired",
	//   "compare", "compare_files", "compare_ssl", "compare_ssl_ca", "compare_maps", "compare_crtlists",
	//   "sync_ssl_pre", "sync_ssl_ca_pre", "sync_files_pre", "sync_maps_pre",
	//   "apply", "commit", "reload_verification", "auxiliary_reload_verification",
	//   "fallback"
	Stage string

	// Message provides a detailed error description
	Message string

	// Cause is the underlying error that caused the failure
	Cause error

	// Hints provides actionable suggestions for fixing the problem
	Hints []string
}

// Error implements the error interface.
func (e *SyncError) Error() string {
	msg := fmt.Sprintf("%s stage failed: %s", e.Stage, e.Message)
	if e.Cause != nil {
		msg += fmt.Sprintf(": %v", e.Cause)
	}
	return msg
}

// Unwrap returns the underlying cause for error unwrapping.
func (e *SyncError) Unwrap() error {
	return e.Cause
}

// ConnectionError represents a failure to connect to the Dataplane API.
type ConnectionError struct {
	// Endpoint is the URL that failed to connect
	Endpoint string

	// Cause is the underlying connection error
	Cause error
}

// Error implements the error interface.
func (e *ConnectionError) Error() string {
	return fmt.Sprintf("connecting to dataplane API at %s: %v", e.Endpoint, e.Cause)
}

// Unwrap returns the underlying cause for error unwrapping.
func (e *ConnectionError) Unwrap() error {
	return e.Cause
}

// ParseError represents a configuration parsing failure.
type ParseError struct {
	// ConfigType indicates which config failed: "current" or "desired"
	ConfigType string

	// ConfigSnippet contains the first 200 characters of the problematic config
	ConfigSnippet string

	// Cause is the underlying parsing error
	Cause error
}

// Error implements the error interface.
func (e *ParseError) Error() string {
	return fmt.Sprintf("parsing %s configuration: %v", e.ConfigType, e.Cause)
}

// Unwrap returns the underlying cause for error unwrapping.
func (e *ParseError) Unwrap() error {
	return e.Cause
}

// ValidationError represents semantic validation failure from HAProxy.
type ValidationError struct {
	// Phase indicates which validation phase failed: "syntax" or "semantic"
	Phase string

	// Message is the validation error message
	Message string

	// Cause is the underlying error
	Cause error
}

// Error implements the error interface.
func (e *ValidationError) Error() string {
	if e.Phase != "" {
		return fmt.Sprintf("%s validation failed: %s: %v", e.Phase, e.Message, e.Cause)
	}
	return fmt.Sprintf("HAProxy validation failed: %s: %v", e.Message, e.Cause)
}

// Unwrap returns the underlying error for error unwrapping.
func (e *ValidationError) Unwrap() error {
	return e.Cause
}

// validationPhase pairs a phase identifier with its canonical user-facing
// message, so callers can build a ValidationError with one call instead of
// re-typing the message each time the same phase fails. The phase name is
// also part of the error string surfaced by ValidationError.Error().
type validationPhase struct {
	name    string
	message string
}

var (
	phaseSyntax   = validationPhase{name: phaseNameSyntax, message: "configuration has syntax errors"}
	phaseSchema   = validationPhase{name: "schema", message: "configuration violates API schema constraints"}
	phaseSemantic = validationPhase{name: phaseNameSemantic, message: "configuration has semantic errors"}
)

// wrap builds a ValidationError attributing the failure to this phase.
func (p validationPhase) wrap(cause error) *ValidationError {
	return &ValidationError{Phase: p.name, Message: p.message, Cause: cause}
}

// Helper functions to create common error scenarios

// NewConnectionError creates a ConnectionError.
func NewConnectionError(endpoint string, cause error) *SyncError {
	return &SyncError{
		Stage:   "connect",
		Message: fmt.Sprintf("connecting to dataplane API at %s", endpoint),
		Cause:   &ConnectionError{Endpoint: endpoint, Cause: cause},
		Hints: []string{
			"Verify the dataplane API URL is correct",
			"Check that HAProxy is running and accessible",
			"Ensure network connectivity to the HAProxy host",
			"Verify credentials are correct",
		},
	}
}

// NewParseError creates a ParseError.
func NewParseError(configType, configSnippet string, cause error) *SyncError {
	hints := []string{
		"Check the HAProxy configuration syntax",
		hintValidateConfig,
	}

	if configType == configTypeCurrent {
		hints = append(hints, "The current config from dataplane API may be corrupted")
	} else {
		hints = append(hints, "Review the desired configuration for syntax errors")
	}

	return &SyncError{
		Stage:   fmt.Sprintf("parse-%s", configType),
		Message: fmt.Sprintf("parsing %s configuration", configType),
		Cause:   &ParseError{ConfigType: configType, ConfigSnippet: configSnippet, Cause: cause},
		Hints:   hints,
	}
}

// SimplifyValidationError parses HAProxy validation errors and extracts
// the key information for user-friendly error messages.
//
// Handles two types of validation errors:
//
//  1. Schema validation errors - OpenAPI spec violations:
//     Input: "schema validation failed: configuration violates API schema constraints: ... Error at "/field": constraint"
//     Output: "field constraint (got value)"
//
//  2. Semantic validation errors - HAProxy binary validation failures:
//     Input: "semantic validation failed: configuration has semantic errors: haproxy validation failed: <context>"
//     Output: "<context>" (preserves parseHAProxyError output with context lines)
//
// Returns original error string if parsing fails.
func SimplifyValidationError(err error) string {
	if err == nil {
		return ""
	}

	errStr := err.Error()

	// Try semantic validation error first (preserves context from parseHAProxyError)
	if strings.Contains(errStr, "semantic validation failed") {
		return simplifySemanticError(errStr)
	}

	// Try schema validation error
	if strings.Contains(errStr, "schema validation failed") {
		return simplifySchemaError(errStr)
	}

	// Unknown error type, return as-is
	return errStr
}

// simplifySemanticError extracts HAProxy semantic validation context by stripping redundant wrappers.
//
// Input format:
//
//	"semantic validation failed: configuration has semantic errors: haproxy validation failed: <context>"
//
// Output: "<context>" (the parseHAProxyError output).
func simplifySemanticError(errStr string) string {
	// Find the last "haproxy validation failed:" which precedes the actual error
	marker := "haproxy validation failed: "
	idx := strings.LastIndex(errStr, marker)
	if idx == -1 {
		// Can't find marker, return original
		return errStr
	}

	// Extract everything after the marker (the parseHAProxyError output)
	return errStr[idx+len(marker):]
}

// simplifySchemaError extracts OpenAPI schema validation constraint details by parsing error messages.
//
// Input format:
//
//	"schema validation failed: ... Error at "/field_name": constraint"
//	Value: "value"
//
// Output: "field_name constraint (got value)".
func simplifySchemaError(errStr string) string {
	// Try to extract the "Error at" line which contains the useful information
	// Format: Error at "/field_name": <constraint description>
	errorAtIndex := strings.Index(errStr, "Error at \"")
	if errorAtIndex == -1 {
		// Can't find "Error at", return original
		return errStr
	}

	// Extract from "Error at" to the end of that line
	remaining := errStr[errorAtIndex:]
	lines := strings.Split(remaining, "\n")
	if len(lines) == 0 {
		return errStr
	}

	errorLine := lines[0]

	// Parse field name: Error at "/field_name": ...
	fieldStart := strings.Index(errorLine, "\"/") + 2
	fieldEnd := strings.Index(errorLine[fieldStart:], "\"")
	if fieldEnd == -1 {
		return errStr
	}

	field := errorLine[fieldStart : fieldStart+fieldEnd]

	// Extract constraint description (after the field name)
	constraintStart := fieldStart + fieldEnd + 3 // Skip ": "
	if constraintStart >= len(errorLine) {
		return errStr
	}

	constraint := errorLine[constraintStart:]

	// Try to extract value if present
	// Format: Value:\n  "value"
	var value string
	_, after, ok := strings.Cut(remaining, "Value:\n")
	if ok {
		valueText := after // Skip "Value:\n"
		valueLines := strings.Split(valueText, "\n")
		if len(valueLines) > 0 {
			value = strings.TrimSpace(valueLines[0])
			// Remove only the outermost quotes (not escaped quotes inside)
			if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
				value = value[1 : len(value)-1]
			}
		}
	}

	// Build simplified message
	var simplified string
	if value != "" {
		simplified = fmt.Sprintf("%s %s (got %s)", field, constraint, value)
	} else {
		simplified = fmt.Sprintf("%s %s", field, constraint)
	}

	return simplified
}

// SimplifyRenderingError extracts meaningful error messages from template rendering failures.
//
// Handles template-level validation errors from the fail() function which are buried
// in the template engine's execution stack trace.
//
// Input format:
//
//	"failed to render haproxy.cfg: failed to render template 'haproxy.cfg': unable to execute template: ... invalid call to function 'fail': <message>"
//
// Output: "<message>" (the user-provided error message from fail() call)
//
// If the error doesn't match this pattern (e.g., syntax errors, missing variables),
// returns the original error string.
func SimplifyRenderingError(err error) string {
	if err == nil {
		return ""
	}

	errStr := err.Error()

	// Look for the fail() function error pattern
	// This is the marker that indicates a template-level validation error
	marker := "invalid call to function 'fail': "
	_, after, ok := strings.Cut(errStr, marker)
	if !ok {
		// Not a fail() error, return original (could be syntax error, missing variable, etc.)
		return errStr
	}

	// Extract everything after the marker (the user-provided message)
	message := after

	// The message should be the last part of the error chain, but may have trailing whitespace
	return strings.TrimSpace(message)
}
