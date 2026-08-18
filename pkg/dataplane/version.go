package dataplane

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
)

// Version is the project-wide HAProxy / DataPlane API version type. It is an
// alias of client.Version (Major.Minor.Patch with Compare), kept under
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

	// versionStr is already a bare "X.Y.Z[-suffix]" token (no banner, no commit
	// hash), so client.ParseVersion's whitespace split is a no-op and Full ends
	// up as versionStr.
	v, err := client.ParseVersion(versionStr)
	if err != nil {
		return nil, fmt.Errorf("parsing version %q: %w", versionStr, err)
	}

	return v, nil
}

// ParseVersionString parses a version string like "3.3" or "3.3.0" into a Version.
// This is useful for parsing user-provided version constraints.
//
// Unlike client.ParseVersion it is intentionally strict about the "v" prefix:
// a leading "v" is rejected rather than stripped, so an operator typo such as
// "v3.3.beta" surfaces as a parse error (callers then run the test anyway
// rather than silently skipping it). The original input is preserved verbatim
// in Version.Full so callers can echo it back unchanged.
func ParseVersionString(version string) (*Version, error) {
	if strings.HasPrefix(version, "v") || strings.HasPrefix(version, "V") {
		return nil, fmt.Errorf("invalid version format: %s", version)
	}

	v, err := client.ParseVersion(version)
	if err != nil {
		return nil, err
	}
	v.Full = version

	return v, nil
}

// DetectLocalVersion runs "haproxy -v" (via the installed HAProxyExecutor)
// and returns the local HAProxy version.
// Returns an error if haproxy is not found or version cannot be parsed.
func DetectLocalVersion() (*Version, error) {
	return DetectLocalVersionContext(context.Background())
}

// DetectLocalVersionContext is DetectLocalVersion with caller cancellation.
func DetectLocalVersionContext(ctx context.Context) (*Version, error) {
	if cause := context.Cause(ctx); cause != nil {
		return nil, cause
	}
	output, err := getHAProxyExecutor().Version(ctx)
	cause := context.Cause(ctx)
	if cause != nil {
		return nil, cause
	}
	if err != nil {
		return nil, err
	}

	return ParseHAProxyVersionOutput(output)
}

// MinimumVersion returns the lowest of the reported versions, ignoring the
// ones it cannot parse. It is how the controller derives what the whole fleet
// supports: during a rolling upgrade the oldest pod is the one that decides.
// Returns nil when no version is readable.
func MinimumVersion(versions []string) *Version {
	var lowest *Version
	for _, raw := range versions {
		parsed, err := client.ParseVersion(raw)
		if err != nil {
			continue
		}
		if lowest == nil || parsed.Compare(lowest) < 0 {
			lowest = parsed
		}
	}
	return lowest
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
