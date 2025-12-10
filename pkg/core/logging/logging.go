// Package logging provides structured logging setup using Go's standard library log/slog package.
//
// The logging package configures slog with logfmt format (human-readable key=value pairs)
// and maps string log levels (ERROR, WARNING, INFO, DEBUG, TRACE) to slog levels.
package logging

import (
	"log/slog"
	"os"
	"strings"
)

// LevelTrace is a log level below Debug, used for very verbose diagnostic output.
// Following slog convention of 4-level gaps: TRACE=-8, DEBUG=-4, INFO=0, WARN=4, ERROR=8.
const LevelTrace = slog.Level(-8)

// NewLogger creates a new structured logger with the specified log level.
// Supported levels (case-insensitive): ERROR, WARNING, INFO, DEBUG, TRACE.
// Invalid levels default to INFO. Uses logfmt format for output.
func NewLogger(level string) *slog.Logger {
	// Parse log level
	slogLevel := parseLogLevel(level)

	// Create text handler with logfmt format
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slogLevel,
	})

	return slog.New(handler)
}

// parseLogLevel converts string log level to slog.Level.
// Returns slog.LevelInfo for invalid or empty levels (safe default).
func parseLogLevel(level string) slog.Level {
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
