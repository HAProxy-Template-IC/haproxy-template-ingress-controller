// Package logging provides structured logging setup using Go's standard library log/slog package.
//
// The logging package configures slog with logfmt format (human-readable key=value pairs)
// and maps string log levels (ERROR, WARNING, INFO, DEBUG, TRACE) to slog levels.
//
// Dynamic log level updates are supported via SetLevel(), which updates the global log level
// at runtime without requiring logger recreation.
package logging

import (
	"log/slog"
	"os"
	"strings"
)

// LevelTrace is a log level below Debug, used for very verbose diagnostic output.
// Following slog convention of 4-level gaps: TRACE=-8, DEBUG=-4, INFO=0, WARN=4, ERROR=8.
const LevelTrace = slog.Level(-8)

// Log level string constants.
const (
	LevelNameError = "ERROR"
	LevelNameWarn  = "WARN"
	LevelNameInfo  = "INFO"
	LevelNameDebug = "DEBUG"
	LevelNameTrace = "TRACE"
)

// globalLevel is a package-level LevelVar that enables dynamic log level updates.
// Used by NewDynamicLogger and SetLevel.
var globalLevel = new(slog.LevelVar)

// NewLogger creates a new structured logger with the specified log level.
// Supported levels (case-insensitive): ERROR, WARNING, INFO, DEBUG, TRACE.
// Invalid levels default to INFO. Uses logfmt format for output.
//
// Note: This creates a logger with a static level. For dynamic level updates,
// use NewDynamicLogger instead.
func NewLogger(level string) *slog.Logger {
	// Parse log level
	slogLevel := ParseLogLevel(level)

	// Create text handler with logfmt format
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slogLevel,
	})

	return slog.New(handler)
}

// NewDynamicLogger creates a logger with a dynamically adjustable level.
// The level can be changed at runtime via SetLevel().
// Supported levels (case-insensitive): ERROR, WARNING, INFO, DEBUG, TRACE.
// Invalid levels default to INFO. Uses logfmt format for output.
func NewDynamicLogger(level string) *slog.Logger {
	globalLevel.Set(ParseLogLevel(level))

	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: globalLevel,
	})

	return slog.New(handler)
}

// SetLevel updates the global log level at runtime.
// This affects all loggers created with NewDynamicLogger.
// Supported levels (case-insensitive): ERROR, WARNING, INFO, DEBUG, TRACE.
// Invalid levels are silently ignored (level remains unchanged).
func SetLevel(level string) {
	if level == "" {
		return // Empty level means keep current level
	}
	globalLevel.Set(ParseLogLevel(level))
}

// GetLevel returns the current global log level as a string.
func GetLevel() string {
	level := globalLevel.Level()
	switch level {
	case slog.LevelError:
		return LevelNameError
	case slog.LevelWarn:
		return LevelNameWarn
	case slog.LevelInfo:
		return LevelNameInfo
	case slog.LevelDebug:
		return LevelNameDebug
	case LevelTrace:
		return LevelNameTrace
	default:
		return LevelNameInfo
	}
}

// ParseLogLevel converts string log level to slog.Level.
// Returns slog.LevelInfo for invalid or empty levels (safe default).
// Supported levels (case-insensitive): ERROR, WARNING/WARN, INFO, DEBUG, TRACE.
func ParseLogLevel(level string) slog.Level {
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "ERROR":
		return slog.LevelError
	case "WARNING", "WARN":
		return slog.LevelWarn
	case "INFO":
		return slog.LevelInfo
	case "DEBUG":
		return slog.LevelDebug
	case "TRACE":
		return LevelTrace
	default:
		// Default to INFO for invalid or empty levels
		return slog.LevelInfo
	}
}
