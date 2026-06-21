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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseVersion_Patch pins patch extraction, including the best-effort cases
// (absent or non-numeric patch -> 0).
func TestParseVersion_Patch(t *testing.T) {
	tests := []struct {
		in                  string
		major, minor, patch int
	}{
		{"v3.3.5 8467a253", 3, 3, 5}, // full API string w/ commit
		{"v3.2.13 abc", 3, 2, 13},
		{"3.3.2", 3, 3, 2},
		{"3.3", 3, 3, 0},       // no patch segment -> 0
		{"v3.3.x", 3, 3, 0},    // non-numeric patch -> 0
		{"3.3.2-dev", 3, 3, 2}, // suffix tolerated by Sscanf
	}
	for _, tt := range tests {
		v, err := ParseVersion(tt.in)
		require.NoError(t, err, tt.in)
		assert.Equal(t, tt.major, v.Major, "major: %s", tt.in)
		assert.Equal(t, tt.minor, v.Minor, "minor: %s", tt.in)
		assert.Equal(t, tt.patch, v.Patch, "patch: %s", tt.in)
		assert.Equal(t, tt.in, v.Full, "full retained: %s", tt.in)
	}
}

// TestVersion_Compare pins that Compare is major.minor-ONLY (patch ignored).
// This is load-bearing: discovery matches a dataplaneapi version against a
// HAProxy version via Compare, and they share major.minor but never patch — a
// patch-aware Compare wrongly rejected every pod (v3.3.5 vs 3.3.10).
func TestVersion_Compare(t *testing.T) {
	lt := func(a, b string) {
		va, _ := ParseVersion(a)
		vb, _ := ParseVersion(b)
		assert.Equal(t, -1, va.Compare(vb), "%s < %s", a, b)
		assert.Equal(t, 1, vb.Compare(va), "%s > %s", b, a)
	}
	lt("3.2.13", "3.3.0") // minor dominates
	lt("2.9.9", "3.0.0")  // major dominates

	eq := func(a, b string) {
		va, _ := ParseVersion(a)
		vb, _ := ParseVersion(b)
		assert.Equal(t, 0, va.Compare(vb), "%s == %s (major.minor)", a, b)
	}
	eq("3.3.1", "3.3.9")            // patch ignored
	eq("v3.3.5 8467a253", "3.3.10") // the discovery case: dataplaneapi vs HAProxy
	eq("3.3.2", "v3.3.2 commit")
}
