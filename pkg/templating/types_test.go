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
	"github.com/stretchr/testify/require"
)

func TestEngineType_String(t *testing.T) {
	tests := []struct {
		name string
		in   EngineType
		want string
	}{
		{name: "scriggo (zero value)", in: EngineTypeScriggo, want: EngineNameScriggo},
		{name: "unknown high value", in: EngineType(99), want: EngineNameUnknown},
		{name: "unknown low value", in: EngineType(-1), want: EngineNameUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.in.String())
		})
	}
}

func TestParseEngineType(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    EngineType
		wantErr bool
	}{
		{name: "scriggo lowercase", in: "scriggo", want: EngineTypeScriggo},
		{name: "scriggo uppercase", in: "SCRIGGO", want: EngineTypeScriggo},
		{name: "scriggo mixed case", in: "Scriggo", want: EngineTypeScriggo},
		{name: "empty string defaults to scriggo", in: "", want: EngineTypeScriggo},
		{name: "unknown engine returns error", in: "jinja2", wantErr: true},
		{name: "whitespace is not normalized", in: " scriggo ", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseEngineType(tt.in)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "unknown engine type")
				assert.Contains(t, err.Error(), EngineNameScriggo)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
