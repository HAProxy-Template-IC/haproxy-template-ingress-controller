package dataplane

import (
	"errors"
	"fmt"
	"strings"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
)

// Version is the project-wide HAProxy / DataPlane API version type. It is an
// alias of client.Version (Major.Minor.Patch with Compare/AtLeast), kept under
// the dataplane.Version name so existing callers (discovery, validation,
// testrunner, …) compile unchanged.
type Version = client.Version

// ParseHAProxyVersionOutput parses the output of "haproxy -v" command.
// Expected format: "HAProxy version 3.2.9 2025/11/21 - https://haproxy.org/\n..."
// Returns extracted major.minor version.
func ParseHAProxyVersionOutput(output string) (*Version, error) {
	// Get first line
	lines := strings.Split(output, "\n")
	if len(lines) == 0 {
		return nil, errors.New("empty haproxy version output")
	}

	firstLine := lines[0]

	// Expected: "HAProxy version X.Y.Z ..."
	const prefix = "HAProxy version "
	if !strings.HasPrefix(firstLine, prefix) {
		return nil, fmt.Errorf("unexpected haproxy version format: %s", firstLine)
	}

	// Extract version part after prefix
	versionPart := strings.TrimPrefix(firstLine, prefix)

	// Split by space to get "X.Y.Z"
	parts := strings.Fields(versionPart)
	if len(parts) == 0 {
		return nil, fmt.Errorf("no version number found in: %s", firstLine)
	}

	versionStr := parts[0]

	// Parse major.minor.patch from "X.Y.Z" or "X.Y.Z-suffix"
	major, minor, patch, err := parseVersionParts(versionStr)
	if err != nil {
		return nil, fmt.Errorf("parsing version %q: %w", versionStr, err)
	}

	return &Version{
		Major: major,
		Minor: minor,
		Patch: patch,
		Full:  versionStr,
	}, nil
}

// parseVersionParts extracts major, minor, patch from "X.Y.Z" or "X.Y.Z-suffix".
// Patch is best-effort (0 when absent or non-numeric).
func parseVersionParts(version string) (major, minor, patch int, err error) {
	// Handle versions like "3.2.9" or "3.2.9-dev" or "3.2"
	// Strip suffix after dash
	if idx := strings.Index(version, "-"); idx >= 0 {
		version = version[:idx]
	}

	// Split by dots
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return 0, 0, 0, fmt.Errorf("invalid version format: %s", version)
	}

	// Parse major
	if _, err := fmt.Sscanf(parts[0], "%d", &major); err != nil {
		return 0, 0, 0, fmt.Errorf("invalid major version: %s", parts[0])
	}

	// Parse minor
	if _, err := fmt.Sscanf(parts[1], "%d", &minor); err != nil {
		return 0, 0, 0, fmt.Errorf("invalid minor version: %s", parts[1])
	}

	// Parse patch (optional, best-effort)
	if len(parts) >= 3 {
		_, _ = fmt.Sscanf(parts[2], "%d", &patch)
	}

	return major, minor, patch, nil
}

// ParseVersionString parses a version string like "3.3" or "3.3.0" into a Version.
// This is useful for parsing user-provided version constraints.
func ParseVersionString(version string) (*Version, error) {
	major, minor, patch, err := parseVersionParts(version)
	if err != nil {
		return nil, err
	}

	return &Version{
		Major: major,
		Minor: minor,
		Patch: patch,
		Full:  version,
	}, nil
}

// DetectLocalVersion runs "haproxy -v" (via the installed HAProxyExecutor)
// and returns the local HAProxy version.
// Returns an error if haproxy is not found or version cannot be parsed.
func DetectLocalVersion() (*Version, error) {
	output, err := getHAProxyExecutor().Version()
	if err != nil {
		return nil, err
	}

	return ParseHAProxyVersionOutput(output)
}

// VersionFromAPIInfo converts client.VersionInfo (from /v3/info) to Version.
// The API version string format is "vX.Y.Z commit" (e.g., "v3.2.6 87ad0bcf").
func VersionFromAPIInfo(info *client.VersionInfo) (*Version, error) {
	if info == nil {
		return nil, errors.New("version info is nil")
	}

	v, err := client.ParseVersion(info.API.Version)
	if err != nil {
		return nil, fmt.Errorf("parsing API version: %w", err)
	}

	return v, nil
}
