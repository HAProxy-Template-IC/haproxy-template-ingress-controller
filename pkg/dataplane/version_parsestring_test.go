// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package dataplane

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseVersionString covers the public version-string parser
// that user-facing config consumers call to validate version constraints.
// The wrapper has its own contract: it must preserve the *original* input
// string in Version.Full (for logging) regardless of which dotted form was
// supplied — that's the property callers rely on when echoing versions
// back to operators.
func TestParseVersionString(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMajor int
		wantMinor int
		wantFull  string
		wantErr   bool
	}{
		{
			name:      "two-component version preserves Full verbatim",
			input:     "3.2",
			wantMajor: 3,
			wantMinor: 2,
			wantFull:  "3.2",
		},
		{
			name:      "three-component version preserves Full verbatim",
			input:     "3.2.9",
			wantMajor: 3,
			wantMinor: 2,
			wantFull:  "3.2.9",
		},
		{
			name:      "version with suffix keeps the suffix in Full",
			input:     "3.1.0-dev",
			wantMajor: 3,
			wantMinor: 1,
			wantFull:  "3.1.0-dev",
		},
		{
			name:    "single component is rejected (parser requires major.minor)",
			input:   "3",
			wantErr: true,
		},
		{
			name:    "empty string is rejected",
			input:   "",
			wantErr: true,
		},
		{
			name:    "non-numeric minor is rejected",
			input:   "3.x",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := ParseVersionString(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, v)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, v)
			assert.Equal(t, tt.wantMajor, v.Major)
			assert.Equal(t, tt.wantMinor, v.Minor)
			assert.Equal(t, tt.wantFull, v.Full, "Full must echo the caller's input, not a normalized form")
		})
	}
}

// Pin the divergent Full conventions of the two parsers using inputs that
// actually exercise the difference: ParseHAProxyVersionOutput strips the
// banner down to the version token, while ParseVersionString preserves its
// caller's input verbatim — including suffixes. A future refactor that
// normalized one form to the other would now flip the assert.NotEqual.
func TestParseVersionString_FullDiffersFromHAProxyOutputParsing(t *testing.T) {
	const haproxyOutput = "HAProxy version 3.2.9 2025/11/21 - https://haproxy.org/\n"

	fromOutput, err := ParseHAProxyVersionOutput(haproxyOutput)
	require.NoError(t, err)

	// Use a suffixed version so the Full strings are observably different
	// even though Major/Minor agree.
	fromString, err := ParseVersionString("3.2.9-custom")
	require.NoError(t, err)

	assert.Equal(t, fromOutput.Major, fromString.Major)
	assert.Equal(t, fromOutput.Minor, fromString.Minor)
	assert.Equal(t, "3.2.9", fromOutput.Full, "ParseHAProxyVersionOutput stores the version-only token in Full")
	assert.Equal(t, "3.2.9-custom", fromString.Full, "ParseVersionString stores the user-supplied input in Full")
	assert.NotEqual(t, fromOutput.Full, fromString.Full, "Full conventions must remain distinct between the two parsers")
}
