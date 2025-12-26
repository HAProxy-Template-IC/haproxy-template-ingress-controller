package dataplane

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
)

func TestVersion_Compare(t *testing.T) {
	tests := []struct {
		name     string
		v1       *Version
		v2       *Version
		expected int
	}{
		{
			name:     "equal versions",
			v1:       &Version{Major: 3, Minor: 2},
			v2:       &Version{Major: 3, Minor: 2},
			expected: 0,
		},
		{
			name:     "v1 major less than v2",
			v1:       &Version{Major: 2, Minor: 5},
			v2:       &Version{Major: 3, Minor: 0},
			expected: -1,
		},
		{
			name:     "v1 major greater than v2",
			v1:       &Version{Major: 3, Minor: 0},
			v2:       &Version{Major: 2, Minor: 5},
			expected: 1,
		},
		{
			name:     "v1 minor less than v2",
			v1:       &Version{Major: 3, Minor: 0},
			v2:       &Version{Major: 3, Minor: 1},
			expected: -1,
		},
		{
			name:     "v1 minor greater than v2",
			v1:       &Version{Major: 3, Minor: 2},
			v2:       &Version{Major: 3, Minor: 1},
			expected: 1,
		},
		{
			// Remote older than local: rejected
			name:     "local 3.1 rejects remote 3.0",
			v1:       &Version{Major: 3, Minor: 0}, // remote
			v2:       &Version{Major: 3, Minor: 1}, // local
			expected: -1,
		},
		{
			// Remote newer than local: accepted
			name:     "local 3.0 accepts remote 3.1",
			v1:       &Version{Major: 3, Minor: 1}, // remote
			v2:       &Version{Major: 3, Minor: 0}, // local
			expected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.v1.Compare(tt.v2)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseHAProxyVersionOutput(t *testing.T) {
	tests := []struct {
		name        string
		output      string
		wantMajor   int
		wantMinor   int
		wantFull    string
		expectError bool
	}{
		{
			name:      "standard version output",
			output:    "HAProxy version 3.2.9 2025/11/21 - https://haproxy.org/\nStatus: long-term supported branch",
			wantMajor: 3,
			wantMinor: 2,
			wantFull:  "3.2.9",
		},
		{
			name:      "version with dev suffix",
			output:    "HAProxy version 3.1.0-dev - https://haproxy.org/\nBuild options: ...",
			wantMajor: 3,
			wantMinor: 1,
			wantFull:  "3.1.0-dev",
		},
		{
			name:      "older version format",
			output:    "HAProxy version 2.8.5 2024/01/15\n",
			wantMajor: 2,
			wantMinor: 8,
			wantFull:  "2.8.5",
		},
		{
			name:        "empty output",
			output:      "",
			expectError: true,
		},
		{
			name:        "invalid format",
			output:      "Some other output",
			expectError: true,
		},
		{
			name:        "missing version number",
			output:      "HAProxy version ",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ver, err := ParseHAProxyVersionOutput(tt.output)

			if tt.expectError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantMajor, ver.Major)
			assert.Equal(t, tt.wantMinor, ver.Minor)
			assert.Equal(t, tt.wantFull, ver.Full)
		})
	}
}

func TestVersionFromAPIInfo(t *testing.T) {
	tests := []struct {
		name        string
		info        *client.VersionInfo
		wantMajor   int
		wantMinor   int
		wantFull    string
		expectError bool
	}{
		{
			name: "standard API version",
			info: &client.VersionInfo{
				API: struct {
					Version string `json:"version"`
				}{Version: "v3.2.6 87ad0bcf"},
			},
			wantMajor: 3,
			wantMinor: 2,
			wantFull:  "v3.2.6 87ad0bcf",
		},
		{
			name: "API version without commit hash",
			info: &client.VersionInfo{
				API: struct {
					Version string `json:"version"`
				}{Version: "v3.0.0"},
			},
			wantMajor: 3,
			wantMinor: 0,
			wantFull:  "v3.0.0",
		},
		{
			name: "API version 3.1",
			info: &client.VersionInfo{
				API: struct {
					Version string `json:"version"`
				}{Version: "v3.1.2 abc123"},
			},
			wantMajor: 3,
			wantMinor: 1,
			wantFull:  "v3.1.2 abc123",
		},
		{
			name:        "nil info",
			info:        nil,
			expectError: true,
		},
		{
			name: "empty version string",
			info: &client.VersionInfo{
				API: struct {
					Version string `json:"version"`
				}{Version: ""},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ver, err := VersionFromAPIInfo(tt.info)

			if tt.expectError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantMajor, ver.Major)
			assert.Equal(t, tt.wantMinor, ver.Minor)
			assert.Equal(t, tt.wantFull, ver.Full)
		})
	}
}

func TestParseVersionParts(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantMajor   int
		wantMinor   int
		expectError bool
	}{
		{
			name:      "X.Y.Z format",
			input:     "3.2.9",
			wantMajor: 3,
			wantMinor: 2,
		},
		{
			name:      "X.Y format",
			input:     "3.2",
			wantMajor: 3,
			wantMinor: 2,
		},
		{
			name:      "X.Y.Z-suffix format",
			input:     "3.1.0-dev",
			wantMajor: 3,
			wantMinor: 1,
		},
		{
			name:      "X.Y.Z-long-suffix format",
			input:     "3.0.0-beta.1",
			wantMajor: 3,
			wantMinor: 0,
		},
		{
			name:        "single number",
			input:       "3",
			expectError: true,
		},
		{
			name:        "empty string",
			input:       "",
			expectError: true,
		},
		{
			name:        "non-numeric major",
			input:       "a.2.3",
			expectError: true,
		},
		{
			name:        "non-numeric minor",
			input:       "3.b.3",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			major, minor, err := parseVersionParts(tt.input)

			if tt.expectError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantMajor, major)
			assert.Equal(t, tt.wantMinor, minor)
		})
	}
}
