//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"haptic/pkg/dataplane/parser"
)

// getHAPEEMajorMinor extracts the major.minor version from HAPROXY_VERSION env var.
// Examples: "3.0r1" → "3.0", "3.2" → "3.2", "3.1r1" → "3.1"
func getHAPEEMajorMinor() string {
	version := os.Getenv("HAPROXY_VERSION")
	if version == "" {
		version = "3.2" // Default
	}
	// Extract major.minor (e.g., "3.0r1" → "3.0", "3.2" → "3.2")
	if len(version) >= 3 {
		return version[:3]
	}
	return version
}

// LoadTestConfig loads a test HAProxy configuration file.
// The path is relative to the testdata directory.
//
// Template substitution:
// - ${HAPEE_MAJOR_MINOR} is replaced with the major.minor version (e.g., "3.0", "3.1", "3.2")
//
// This allows test configs to use version-specific paths like:
//
//	module-load /opt/hapee-${HAPEE_MAJOR_MINOR}/modules/hapee-lb-botmgmt.so
func LoadTestConfig(t *testing.T, relativePath string) string {
	t.Helper()

	// Get the directory of this source file
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok, "failed to get caller information")

	baseDir := filepath.Dir(filename)
	fullPath := filepath.Join(baseDir, "testdata", relativePath)

	content, err := os.ReadFile(fullPath)
	require.NoError(t, err, "failed to read test config file: %s", fullPath)

	// Perform template substitution for version-specific paths
	result := string(content)
	result = strings.ReplaceAll(result, "${HAPEE_MAJOR_MINOR}", getHAPEEMajorMinor())

	return result
}

// ParseTestConfig loads and parses a test configuration file.
func ParseTestConfig(t *testing.T, p *parser.Parser, relativePath string) *parser.StructuredConfig {
	t.Helper()

	configStr := LoadTestConfig(t, relativePath)
	cfg, err := p.ParseFromString(configStr)
	require.NoError(t, err, "failed to parse config from: %s", relativePath)

	return cfg
}
