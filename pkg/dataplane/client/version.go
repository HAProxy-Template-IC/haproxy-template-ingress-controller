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

package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
)

// VersionInfo contains detected version information from /v3/info endpoint.
type VersionInfo struct {
	API struct {
		Version string `json:"version"` // e.g., "v3.2.6 87ad0bcf"
	} `json:"api"`
}

// DetectVersion queries the /v3/info endpoint to identify the DataPlane API version
// advertised by the given endpoint. Callers should inspect the result with
// ParseVersion and IsEnterpriseVersion before deriving Capabilities.
func DetectVersion(ctx context.Context, endpoint *Endpoint, _ *slog.Logger) (*VersionInfo, error) {
	// Construct /v3/info URL (strip any version suffix from base URL)
	baseURL := strings.TrimSuffix(endpoint.URL, "/")
	baseURL = strings.TrimSuffix(baseURL, "/v2")
	baseURL = strings.TrimSuffix(baseURL, "/v3")
	infoURL := baseURL + "/v3/info"

	req, err := http.NewRequestWithContext(ctx, "GET", infoURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.SetBasicAuth(endpoint.Username, endpoint.Password)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching version info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("version endpoint returned status %d", resp.StatusCode)
	}

	var versionInfo VersionInfo
	if err := json.NewDecoder(resp.Body).Decode(&versionInfo); err != nil {
		return nil, fmt.Errorf("decoding version response: %w", err)
	}

	if versionInfo.API.Version == "" {
		return nil, errors.New("version string is empty in response")
	}

	return &versionInfo, nil
}

// Version represents a DataPlane API / HAProxy version (major.minor.patch).
// Patch is best-effort: it is 0 when the source string carries only
// "major.minor" or a non-numeric patch segment.
//
// This is the single version type for the project; pkg/dataplane aliases it as
// dataplane.Version.
type Version struct {
	Major int
	Minor int
	Patch int
	Full  string // Original version string, retained for logging
}

// Compare orders two versions by major, then minor. Patch is INTENTIONALLY
// ignored: Compare is used for series compatibility — e.g. discovery matching a
// dataplaneapi version ("v3.3.5") against a HAProxy version ("3.3.10"), which
// share major.minor but never patch.
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

// ParseVersion parses a DataPlane API / HAProxy version string into a Version.
// Examples: "v3.2.6 87ad0bcf" -> {3, 2, 6}, "3.3" -> {3, 3, 0}.
func ParseVersion(version string) (*Version, error) {
	// Split on whitespace to get version part (e.g., "v3.2.6")
	parts := strings.Fields(version)
	if len(parts) == 0 {
		return nil, errors.New("empty version string")
	}

	// Strip 'v' prefix if present
	versionPart := strings.TrimPrefix(parts[0], "v")

	// Split on dots (e.g., "3.2.6" -> ["3", "2", "6"])
	segments := strings.Split(versionPart, ".")
	if len(segments) < 2 {
		return nil, fmt.Errorf("invalid version format: %s", version)
	}

	v := &Version{Full: version}
	if _, err := fmt.Sscanf(segments[0], "%d", &v.Major); err != nil {
		return nil, fmt.Errorf("parsing major version: %w", err)
	}
	if _, err := fmt.Sscanf(segments[1], "%d", &v.Minor); err != nil {
		return nil, fmt.Errorf("parsing minor version: %w", err)
	}
	// Patch is optional and best-effort: a non-numeric segment leaves it 0.
	if len(segments) >= 3 {
		_, _ = fmt.Sscanf(segments[2], "%d", &v.Patch)
	}

	return v, nil
}

// IsEnterpriseVersion detects if a version string indicates HAProxy Enterprise edition.
// Enterprise versions typically contain "r" followed by a number (e.g., "3.0r1", "v3.1r1")
// or contain "Enterprise" in the version string.
//
// Examples:
//   - "v3.0r1" -> true (enterprise version format)
//   - "3.1r1" -> true (enterprise version format)
//   - "v3.2.6 87ad0bcf" -> false (community version format)
//   - "HAProxy Enterprise 3.0r1" -> true (contains "Enterprise")

// enterpriseHAProxyVersionPattern matches enterprise HAProxy version format: X.YrZ (e.g., 3.0r1, v3.1r1).
// This is used for detecting enterprise from HAProxy binary version strings.
var enterpriseHAProxyVersionPattern = regexp.MustCompile(`^v?\d+\.\d+r\d+`)

// enterpriseDataPlaneAPIPattern matches enterprise DataPlane API version format: vX.Y.Z-eeN (e.g., v3.0.15-ee1).
// This is used for detecting enterprise from DataPlane API version strings.
var enterpriseDataPlaneAPIPattern = regexp.MustCompile(`-ee\d+`)

// IsEnterpriseVersion returns true when the version string matches one of the
// known HAProxy Enterprise formats.
func IsEnterpriseVersion(version string) bool {
	// Check for "Enterprise" keyword (case-insensitive)
	if strings.Contains(strings.ToLower(version), "enterprise") {
		return true
	}

	// Check for DataPlane API enterprise suffix: -eeN (e.g., v3.0.15-ee1)
	// This is the most reliable indicator from the DataPlane API /v3/info endpoint
	if enterpriseDataPlaneAPIPattern.MatchString(version) {
		return true
	}

	// Check for HAProxy enterprise version format: X.YrZ (e.g., 3.0r1, 3.1r1)
	// This pattern matches versions like "v3.0r1", "3.1r1", "v3.2r1"
	// Used for HAProxy binary version strings
	versionPart := strings.Fields(version)
	if len(versionPart) == 0 {
		return false
	}

	return enterpriseHAProxyVersionPattern.MatchString(versionPart[0])
}
