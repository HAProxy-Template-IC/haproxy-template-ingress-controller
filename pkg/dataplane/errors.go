package dataplane

import (
	"fmt"
	"strings"
)

// SyncError represents a synchronization failure with actionable context.
// It provides detailed information about what stage failed and suggestions
// for how to fix the problem.
type SyncError struct {
	// Stage indicates where the failure occurred:
	// "connect", "fetch", "parse-current", "parse-desired", "compare", "apply", "commit", "fallback"
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
	return fmt.Sprintf("failed to connect to dataplane API at %s: %v", e.Endpoint, e.Cause)
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

	// Line indicates the approximate line number where parsing failed (if available)
	Line int

	// Cause is the underlying parsing error
	Cause error
}

// Error implements the error interface.
func (e *ParseError) Error() string {
	msg := fmt.Sprintf("failed to parse %s configuration", e.ConfigType)
	if e.Line > 0 {
		msg += fmt.Sprintf(" near line %d", e.Line)
	}
	msg += fmt.Sprintf(": %v", e.Cause)
	return msg
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

	// Err is the underlying error
	Err error
}

// Error implements the error interface.
func (e *ValidationError) Error() string {
	if e.Phase != "" {
		return fmt.Sprintf("%s validation failed: %s: %v", e.Phase, e.Message, e.Err)
	}
	return fmt.Sprintf("HAProxy validation failed: %s: %v", e.Message, e.Err)
}

// Unwrap returns the underlying error for error unwrapping.
func (e *ValidationError) Unwrap() error {
	return e.Err
}

// ConflictError represents unresolved version conflicts after exhausting retries.
type ConflictError struct {
	// Retries is the number of retry attempts made
	Retries int

	// ExpectedVersion is the version we tried to use
	ExpectedVersion int64

	// ActualVersion is the version that exists on the server
	ActualVersion string
}

// Error implements the error interface.
func (e *ConflictError) Error() string {
	return fmt.Sprintf("version conflict after %d retries: expected version %d, server has %s",
		e.Retries, e.ExpectedVersion, e.ActualVersion)
}

// OperationError represents a failure of a specific configuration operation.
type OperationError struct {
	// OperationType is "create", "update", or "delete"
	OperationType string

	// Section is the configuration section (e.g., "backend", "server")
	Section string

	// Resource is the resource identifier (e.g., backend name, server name)
	Resource string

	// Cause is the underlying error
	Cause error
}

// Error implements the error interface.
func (e *OperationError) Error() string {
	return fmt.Sprintf("failed to %s %s '%s': %v", e.OperationType, e.Section, e.Resource, e.Cause)
}

// Unwrap returns the underlying cause for error unwrapping.
func (e *OperationError) Unwrap() error {
	return e.Cause
}

// FallbackError represents a failure during raw config fallback.
type FallbackError struct {
	// OriginalError is the error that triggered the fallback
	OriginalError error

	// FallbackCause is the error that occurred during fallback
	FallbackCause error
}

// Error implements the error interface.
func (e *FallbackError) Error() string {
	return fmt.Sprintf("fine-grained sync failed (%v) and fallback to raw config also failed (%v)",
		e.OriginalError, e.FallbackCause)
}

// Unwrap returns the fallback cause for error unwrapping.
func (e *FallbackError) Unwrap() error {
	return e.FallbackCause
}

// Helper functions to create common error scenarios

// NewConnectionError creates a ConnectionError.
func NewConnectionError(endpoint string, cause error) *SyncError {
	return &SyncError{
		Stage:   "connect",
		Message: fmt.Sprintf("failed to connect to dataplane API at %s", endpoint),
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
		"Validate the configuration with: haproxy -c -f <config>",
	}

	if configType == "current" {
		hints = append(hints, "The current config from dataplane API may be corrupted")
	} else {
		hints = append(hints, "Review the desired configuration for syntax errors")
	}

	return &SyncError{
		Stage:   fmt.Sprintf("parse-%s", configType),
		Message: fmt.Sprintf("failed to parse %s configuration", configType),
		Cause:   &ParseError{ConfigType: configType, ConfigSnippet: configSnippet, Cause: cause},
		Hints:   hints,
	}
}

// NewValidationError creates a ValidationError.
func NewValidationError(message string, cause error) *SyncError {
	return &SyncError{
		Stage:   "apply",
		Message: "HAProxy rejected the configuration",
		Cause:   &ValidationError{Phase: "semantic", Message: message, Err: cause},
		Hints: []string{
			"Review the validation error message from HAProxy",
			"Check for references to non-existent backends or servers",
			"Verify all directives are compatible with your HAProxy version",
			"Ensure resource dependencies are satisfied",
		},
	}
}

// NewConflictError creates a ConflictError.
func NewConflictError(retries int, expectedVersion int64, actualVersion string) *SyncError {
	return &SyncError{
		Stage:   "commit",
		Message: fmt.Sprintf("version conflict after %d retries", retries),
		Cause:   &ConflictError{Retries: retries, ExpectedVersion: expectedVersion, ActualVersion: actualVersion},
		Hints: []string{
			"Another process is modifying the HAProxy configuration concurrently",
			"Consider increasing MaxRetries in SyncOptions",
			"Coordinate configuration updates to avoid conflicts",
			"Check if there are other automation tools modifying HAProxy",
		},
	}
}

// NewOperationError creates an OperationError.
func NewOperationError(opType, section, resource string, cause error) *SyncError {
	return &SyncError{
		Stage:   "apply",
		Message: fmt.Sprintf("failed to %s %s '%s'", opType, section, resource),
		Cause:   &OperationError{OperationType: opType, Section: section, Resource: resource, Cause: cause},
		Hints: []string{
			fmt.Sprintf("Check if %s '%s' exists and is accessible", section, resource),
			"Review the operation details for invalid values",
			"Verify resource dependencies are satisfied",
		},
	}
}

// NewFallbackError creates a FallbackError.
func NewFallbackError(originalErr, fallbackCause error) *SyncError {
	return &SyncError{
		Stage:   "fallback",
		Message: "both fine-grained sync and raw config fallback failed",
		Cause:   &FallbackError{OriginalError: originalErr, FallbackCause: fallbackCause},
		Hints: []string{
			"The desired configuration may have fundamental issues",
			"Check HAProxy logs for detailed error messages",
			"Validate the configuration with: haproxy -c -f <config>",
			"Review both the fine-grained sync error and fallback error",
		},
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
	valueIndex := strings.Index(remaining, "Value:\n")
	if valueIndex != -1 {
		valueText := remaining[valueIndex+7:] // Skip "Value:\n"
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
	idx := strings.Index(errStr, marker)
	if idx == -1 {
		// Not a fail() error, return original (could be syntax error, missing variable, etc.)
		return errStr
	}

	// Extract everything after the marker (the user-provided message)
	message := errStr[idx+len(marker):]

	// The message should be the last part of the error chain, but may have trailing whitespace
	return strings.TrimSpace(message)
}
