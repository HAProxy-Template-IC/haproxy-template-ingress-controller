// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dataplane

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Version is a HAProxy version. Patch is best-effort: it is 0 when the source
// string carries only "major.minor" or a non-numeric patch segment.
type Version struct {
	Major int
	Minor int
	Patch int
	Full  string // Original version string, retained for logging
}

// Compare orders two versions by major, then minor. Patch is INTENTIONALLY
// ignored: Compare answers series compatibility — the testrunner gating a test
// on minHAProxyVersion, or the deployer picking the fleet's lowest version.
// Returns -1 if v < other, 0 if same series, 1 if v > other.
func (v *Version) Compare(other *Version) int {
	switch {
	case v.Major != other.Major:
		if v.Major < other.Major {
			return -1
		}
		return 1
	case v.Minor != other.Minor:
		if v.Minor < other.Minor {
			return -1
		}
		return 1
	default:
		return 0
	}
}

// parseVersion parses a HAProxy version string into a Version.
// Examples: "v3.2.6 87ad0bcf" -> {3, 2, 6}, "3.3" -> {3, 3, 0},
// "3.0r1" (Enterprise) -> {3, 0, 0}.
func parseVersion(version string) (*Version, error) {
	// Split on whitespace to get version part (e.g., "v3.2.6")
	parts := strings.Fields(version)
	if len(parts) == 0 {
		return nil, errors.New("empty version string")
	}

	versionPart := strings.TrimPrefix(parts[0], "v")

	segments := strings.Split(versionPart, ".")
	if len(segments) < 2 {
		return nil, fmt.Errorf("invalid version format: %s", version)
	}

	v := &Version{Full: version}
	major, err := strconv.Atoi(segments[0])
	if err != nil {
		return nil, fmt.Errorf("parsing major version: %w", err)
	}
	minorSegment := segments[1]
	if base, revision, found := strings.Cut(minorSegment, "r"); found {
		revision = strings.SplitN(revision, "-", 2)[0]
		if _, rerr := strconv.Atoi(revision); rerr != nil {
			return nil, fmt.Errorf("parsing enterprise revision: %w", rerr)
		}
		minorSegment = base
	}
	minor, err := strconv.Atoi(minorSegment)
	if err != nil {
		return nil, fmt.Errorf("parsing minor version: %w", err)
	}
	v.Major = major
	v.Minor = minor

	// Patch is optional and best-effort: a non-numeric segment leaves it 0.
	// A trailing build suffix ("2-dev") is stripped so the numeric prefix is
	// still extracted.
	if len(segments) >= 3 {
		patchSeg := segments[2]
		if idx := strings.Index(patchSeg, "-"); idx >= 0 {
			patchSeg = patchSeg[:idx]
		}
		if patch, perr := strconv.Atoi(patchSeg); perr == nil {
			v.Patch = patch
		}
	}

	return v, nil
}

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
	// hash), so parseVersion's whitespace split is a no-op and Full ends up as
	// versionStr.
	v, err := parseVersion(versionStr)
	if err != nil {
		return nil, fmt.Errorf("parsing version %q: %w", versionStr, err)
	}

	return v, nil
}

// ParseVersionString parses a version string like "3.3" or "3.3.0" into a Version.
// This is useful for parsing user-provided version constraints.
//
// It is intentionally strict about the "v" prefix: a leading "v" is rejected
// rather than stripped, so an operator typo such as "v3.3.beta" surfaces as a
// parse error (callers then run the test anyway rather than silently skipping
// it). The original input is preserved verbatim in Version.Full so callers can
// echo it back unchanged.
func ParseVersionString(version string) (*Version, error) {
	if strings.HasPrefix(version, "v") || strings.HasPrefix(version, "V") {
		return nil, fmt.Errorf("invalid version format: %s", version)
	}

	v, err := parseVersion(version)
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
		parsed, err := parseVersion(raw)
		if err != nil {
			continue
		}
		if lowest == nil || parsed.Compare(lowest) < 0 {
			lowest = parsed
		}
	}
	return lowest
}
