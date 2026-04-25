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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSyncPhase_String(t *testing.T) {
	tests := []struct {
		name string
		in   SyncPhase
		want string
	}{
		{name: "PhasePreConfig", in: PhasePreConfig, want: "pre-config"},
		{name: "PhaseConfig", in: PhaseConfig, want: "config"},
		{name: "PhasePostConfig", in: PhasePostConfig, want: "post-config"},
		{name: "zero value (unmapped)", in: 0, want: "unknown-phase(0)"},
		{name: "negative (unmapped)", in: -1, want: "unknown-phase(-1)"},
		{name: "high value (unmapped)", in: 99, want: "unknown-phase(99)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.in.String())
		})
	}
}

// TestSyncPhase_OrderingPreserved pins the documented invariant that the
// phases are sequenced PreConfig → Config → PostConfig. Other code uses these
// constants to compare or order phases (e.g. in error context); a future
// refactor that silently changes the iota base would break that ordering.
func TestSyncPhase_OrderingPreserved(t *testing.T) {
	assert.Less(t, int(PhasePreConfig), int(PhaseConfig))
	assert.Less(t, int(PhaseConfig), int(PhasePostConfig))
}
