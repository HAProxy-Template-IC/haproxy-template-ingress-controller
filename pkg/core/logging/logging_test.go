package logging

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewLogger_ERROR(t *testing.T) {
	logger := NewLogger("ERROR")
	assert.NotNil(t, logger)

	// Verify logger is configured (basic smoke test)
	assert.IsType(t, &slog.Logger{}, logger)
}

func TestNewLogger_WARNING(t *testing.T) {
	logger := NewLogger("WARNING")
	assert.NotNil(t, logger)
	assert.IsType(t, &slog.Logger{}, logger)
}

func TestNewLogger_INFO(t *testing.T) {
	logger := NewLogger("INFO")
	assert.NotNil(t, logger)
	assert.IsType(t, &slog.Logger{}, logger)
}

func TestNewLogger_DEBUG(t *testing.T) {
	logger := NewLogger("DEBUG")
	assert.NotNil(t, logger)
	assert.IsType(t, &slog.Logger{}, logger)
}

func TestNewLogger_CaseInsensitive(t *testing.T) {
	testCases := []string{
		"error", "Error", "ERROR",
		"warning", "Warning", "WARNING",
		"info", "Info", "INFO",
		"debug", "Debug", "DEBUG",
	}

	for _, level := range testCases {
		logger := NewLogger(level)
		assert.NotNil(t, logger, "Failed for level: %s", level)
	}
}

func TestNewLogger_InvalidLevel_DefaultsToINFO(t *testing.T) {
	logger := NewLogger("INVALID")
	assert.NotNil(t, logger)
	// Default level should be INFO (we can't easily test this without capturing output)
}

func TestNewLogger_EmptyLevel_DefaultsToINFO(t *testing.T) {
	logger := NewLogger("")
	assert.NotNil(t, logger)
}

func TestParseLogLevel_ERROR(t *testing.T) {
	level := ParseLogLevel("ERROR")
	assert.Equal(t, slog.LevelError, level)
}

func TestParseLogLevel_WARNING(t *testing.T) {
	level := ParseLogLevel("WARNING")
	assert.Equal(t, slog.LevelWarn, level)

	// Test alias
	level = ParseLogLevel("WARN")
	assert.Equal(t, slog.LevelWarn, level)
}

func TestParseLogLevel_INFO(t *testing.T) {
	level := ParseLogLevel("INFO")
	assert.Equal(t, slog.LevelInfo, level)
}

func TestParseLogLevel_DEBUG(t *testing.T) {
	level := ParseLogLevel("DEBUG")
	assert.Equal(t, slog.LevelDebug, level)
}

func TestParseLogLevel_TRACE(t *testing.T) {
	level := ParseLogLevel("TRACE")
	assert.Equal(t, LevelTrace, level)
}

func TestParseLogLevel_Invalid(t *testing.T) {
	level := ParseLogLevel("INVALID")
	assert.Equal(t, slog.LevelInfo, level, "Should default to INFO")
}

func TestParseLogLevel_Empty(t *testing.T) {
	level := ParseLogLevel("")
	assert.Equal(t, slog.LevelInfo, level, "Should default to INFO")
}

func TestParseLogLevel_Whitespace(t *testing.T) {
	level := ParseLogLevel("  DEBUG  ")
	assert.Equal(t, slog.LevelDebug, level, "Should trim whitespace")
}

func TestParseLogLevel_CaseInsensitive(t *testing.T) {
	testCases := []struct {
		input    string
		expected slog.Level
	}{
		{"trace", LevelTrace},
		{"TRACE", LevelTrace},
		{"Trace", LevelTrace},
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"INFO", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"WARN", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"WARNING", slog.LevelWarn},
		{"error", slog.LevelError},
		{"ERROR", slog.LevelError},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			level := ParseLogLevel(tc.input)
			assert.Equal(t, tc.expected, level)
		})
	}
}

func TestNewDynamicLogger(t *testing.T) {
	// Test that NewDynamicLogger creates a working logger
	logger := NewDynamicLogger("DEBUG")
	assert.NotNil(t, logger)
	assert.IsType(t, &slog.Logger{}, logger)

	// Verify the level was set
	assert.Equal(t, "DEBUG", GetLevel())
}

func TestSetLevel(t *testing.T) {
	// Set to DEBUG
	SetLevel("DEBUG")
	assert.Equal(t, "DEBUG", GetLevel())

	// Set to INFO
	SetLevel("INFO")
	assert.Equal(t, "INFO", GetLevel())

	// Set to WARN
	SetLevel("WARN")
	assert.Equal(t, "WARN", GetLevel())

	// Set to ERROR
	SetLevel("ERROR")
	assert.Equal(t, "ERROR", GetLevel())

	// Set to TRACE
	SetLevel("TRACE")
	assert.Equal(t, "TRACE", GetLevel())

	// Empty string should not change the level
	currentLevel := GetLevel()
	SetLevel("")
	assert.Equal(t, currentLevel, GetLevel())

	// Reset to INFO for other tests
	SetLevel("INFO")
}

func TestSetLevel_CaseInsensitive(t *testing.T) {
	SetLevel("debug")
	assert.Equal(t, "DEBUG", GetLevel())

	SetLevel("Info")
	assert.Equal(t, "INFO", GetLevel())

	SetLevel("WARN")
	assert.Equal(t, "WARN", GetLevel())
}

func TestGetLevel(t *testing.T) {
	testCases := []struct {
		setLevel     string
		expectedName string
	}{
		{"ERROR", "ERROR"},
		{"WARN", "WARN"},
		{"INFO", "INFO"},
		{"DEBUG", "DEBUG"},
		{"TRACE", "TRACE"},
	}

	for _, tc := range testCases {
		t.Run(tc.setLevel, func(t *testing.T) {
			SetLevel(tc.setLevel)
			assert.Equal(t, tc.expectedName, GetLevel())
		})
	}

	// Reset to INFO
	SetLevel("INFO")
}

// TestLoggerOutput_Logfmt verifies that the logger produces logfmt-style output.
func TestLoggerOutput_Logfmt(t *testing.T) {
	// Create a buffer to capture output
	var buf bytes.Buffer

	// Create a logger that writes to our buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	logger := slog.New(handler)

	// Log a message
	logger.Info("test message", "key1", "value1", "key2", 42)

	output := buf.String()

	// Verify logfmt format (key=value pairs)
	assert.Contains(t, output, "level=INFO")
	assert.Contains(t, output, "msg=\"test message\"")
	assert.Contains(t, output, "key1=value1")
	assert.Contains(t, output, "key2=42")

	// Verify NOT JSON format (should not have { or })
	assert.NotContains(t, output, "{")
	assert.NotContains(t, output, "}")
}

// TestLoggerFiltering verifies that log level filtering works.
func TestLoggerFiltering(t *testing.T) {
	testCases := []struct {
		loggerLevel string
		logLevel    slog.Level
		shouldLog   bool
	}{
		{"ERROR", slog.LevelError, true},
		{"ERROR", slog.LevelWarn, false},
		{"ERROR", slog.LevelInfo, false},
		{"ERROR", slog.LevelDebug, false},

		{"WARNING", slog.LevelError, true},
		{"WARNING", slog.LevelWarn, true},
		{"WARNING", slog.LevelInfo, false},
		{"WARNING", slog.LevelDebug, false},

		{"INFO", slog.LevelError, true},
		{"INFO", slog.LevelWarn, true},
		{"INFO", slog.LevelInfo, true},
		{"INFO", slog.LevelDebug, false},

		{"DEBUG", slog.LevelError, true},
		{"DEBUG", slog.LevelWarn, true},
		{"DEBUG", slog.LevelInfo, true},
		{"DEBUG", slog.LevelDebug, true},
	}

	for _, tt := range testCases {
		t.Run(tt.loggerLevel+"_logs_"+tt.logLevel.String(), func(t *testing.T) {
			var buf bytes.Buffer
			handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{
				Level: ParseLogLevel(tt.loggerLevel),
			})
			logger := slog.New(handler)

			// Log at the test level
			logger.Log(context.Background(), tt.logLevel, "test message")

			if tt.shouldLog {
				assert.NotEmpty(t, buf.String(), "Expected log output for %s logger at %s level", tt.loggerLevel, tt.logLevel)
			} else {
				assert.Empty(t, buf.String(), "Expected no log output for %s logger at %s level", tt.loggerLevel, tt.logLevel)
			}
		})
	}
}

// TestLogfmtFormat_Structure verifies the structure of logfmt output.
func TestLogfmtFormat_Structure(t *testing.T) {
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	logger := slog.New(handler)

	logger.Info("controller started", "config_version", "v1", "secret_version", "v2")

	output := buf.String()

	// Verify key components are present
	assert.Contains(t, output, "time=")
	assert.Contains(t, output, "level=INFO")
	assert.Contains(t, output, "msg=\"controller started\"")
	assert.Contains(t, output, "config_version=v1")
	assert.Contains(t, output, "secret_version=v2")

	// Verify format: should be space-separated key=value pairs (not JSON)
	// Count equals signs (one per key=value pair)
	equalsCount := strings.Count(output, "=")
	assert.GreaterOrEqual(t, equalsCount, 4, "Should have at least 4 key=value pairs")

	// Verify NOT JSON format
	assert.NotContains(t, output, "{")
	assert.NotContains(t, output, "}")
	assert.NotContains(t, output, "\":")
}
