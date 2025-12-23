package dataplane

import (
	"fmt"
	"os/exec"
	"strings"

	"haptic/pkg/dataplane/client"
)

// Version represents HAProxy or DataPlane API version with major.minor components.
// Only major and minor are used for compatibility comparison.
type Version struct {
	Major int
	Minor int
	Full  string // Original version string for logging
}

// Compare compares two versions using major.minor semantics.
// Returns:
//   - -1 if v < other
//   - 0 if v == other
//   - 1 if v > other
//
// Only Major and Minor are compared; patch versions are ignored.
func (v *Version) Compare(other *Version) int {
	if v.Major < other.Major {
		return -1
	}
	if v.Major > other.Major {
		return 1
	}
	// Major versions equal, compare minor
	if v.Minor < other.Minor {
		return -1
	}
	if v.Minor > other.Minor {
		return 1
	}
	return 0
}

// ParseHAProxyVersionOutput parses the output of "haproxy -v" command.
// Expected format: "HAProxy version 3.2.9 2025/11/21 - https://haproxy.org/\n..."
// Returns extracted major.minor version.
func ParseHAProxyVersionOutput(output string) (*Version, error) {
	// Get first line
	lines := strings.Split(output, "\n")
	if len(lines) == 0 {
		return nil, fmt.Errorf("empty haproxy version output")
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

	// Parse major.minor from "X.Y.Z" or "X.Y.Z-suffix"
	major, minor, err := parseVersionParts(versionStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse version %q: %w", versionStr, err)
	}

	return &Version{
		Major: major,
		Minor: minor,
		Full:  versionStr,
	}, nil
}

// parseVersionParts extracts major and minor from "X.Y.Z" or "X.Y.Z-suffix".
func parseVersionParts(version string) (major, minor int, err error) {
	// Handle versions like "3.2.9" or "3.2.9-dev" or "3.2"
	// Strip suffix after dash
	if idx := strings.Index(version, "-"); idx >= 0 {
		version = version[:idx]
	}

	// Split by dots
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return 0, 0, fmt.Errorf("invalid version format: %s", version)
	}

	// Parse major
	if _, err := fmt.Sscanf(parts[0], "%d", &major); err != nil {
		return 0, 0, fmt.Errorf("invalid major version: %s", parts[0])
	}

	// Parse minor
	if _, err := fmt.Sscanf(parts[1], "%d", &minor); err != nil {
		return 0, 0, fmt.Errorf("invalid minor version: %s", parts[1])
	}

	return major, minor, nil
}

// DetectLocalVersion runs "haproxy -v" and returns the local HAProxy version.
// Returns an error if haproxy is not found or version cannot be parsed.
func DetectLocalVersion() (*Version, error) {
	haproxyBin, err := exec.LookPath("haproxy")
	if err != nil {
		return nil, fmt.Errorf("haproxy binary not found: %w", err)
	}

	cmd := exec.Command(haproxyBin, "-v")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to run haproxy -v: %w", err)
	}

	return ParseHAProxyVersionOutput(string(output))
}

// VersionFromAPIInfo converts client.VersionInfo (from /v3/info) to Version.
// The API version string format is "vX.Y.Z commit" (e.g., "v3.2.6 87ad0bcf").
func VersionFromAPIInfo(info *client.VersionInfo) (*Version, error) {
	if info == nil {
		return nil, fmt.Errorf("version info is nil")
	}

	major, minor, err := client.ParseVersion(info.API.Version)
	if err != nil {
		return nil, fmt.Errorf("failed to parse API version: %w", err)
	}

	return &Version{
		Major: major,
		Minor: minor,
		Full:  info.API.Version,
	}, nil
}
