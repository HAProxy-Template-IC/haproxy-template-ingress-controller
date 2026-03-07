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

package templating

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestScriggoSemverGte(t *testing.T) {
	tests := []struct {
		name       string
		version    any
		minVersion any
		want       bool
	}{
		{name: "equal versions", version: "3.3", minVersion: "3.3", want: true},
		{name: "version higher minor", version: "3.4", minVersion: "3.3", want: true},
		{name: "version lower minor", version: "3.2", minVersion: "3.3", want: false},
		{name: "version higher major", version: "4.0", minVersion: "3.3", want: true},
		{name: "version lower major", version: "2.9", minVersion: "3.3", want: false},
		{name: "version 3.10 >= 3.3", version: "3.10", minVersion: "3.3", want: true},
		{name: "version 3.3 < 3.10", version: "3.3", minVersion: "3.10", want: false},
		{name: "with v prefix", version: "v3.3", minVersion: "3.3", want: true},
		{name: "both with v prefix", version: "v3.4", minVersion: "v3.3", want: true},
		{name: "with patch version", version: "3.3.1", minVersion: "3.3", want: true},
		{name: "empty version", version: "", minVersion: "3.3", want: false},
		{name: "empty minVersion", version: "3.3", minVersion: "", want: false},
		{name: "nil version", version: nil, minVersion: "3.3", want: false},
		{name: "non-string version", version: 3.3, minVersion: "3.3", want: true},
		{name: "3.0 vs 3.0", version: "3.0", minVersion: "3.0", want: true},
		{name: "3.0 vs 3.1", version: "3.0", minVersion: "3.1", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scriggoSemverGte(tt.version, tt.minVersion)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseSemver(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantMajor int
		wantMinor int
		wantOk    bool
	}{
		{name: "simple", input: "3.3", wantMajor: 3, wantMinor: 3, wantOk: true},
		{name: "with patch", input: "3.3.1", wantMajor: 3, wantMinor: 3, wantOk: true},
		{name: "with v prefix", input: "v3.2", wantMajor: 3, wantMinor: 2, wantOk: true},
		{name: "double digit minor", input: "3.10", wantMajor: 3, wantMinor: 10, wantOk: true},
		{name: "empty string", input: "", wantMajor: 0, wantMinor: 0, wantOk: false},
		{name: "no dot", input: "3", wantMajor: 0, wantMinor: 0, wantOk: false},
		{name: "non-numeric", input: "abc.def", wantMajor: 0, wantMinor: 0, wantOk: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			major, minor, ok := parseSemver(tt.input)
			assert.Equal(t, tt.wantMajor, major)
			assert.Equal(t, tt.wantMinor, minor)
			assert.Equal(t, tt.wantOk, ok)
		})
	}
}
