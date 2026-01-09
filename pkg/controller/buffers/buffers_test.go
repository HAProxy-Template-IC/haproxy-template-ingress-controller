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

package buffers

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCalculateSize_Bounds(t *testing.T) {
	// With any multiplier, result should be within bounds
	size := CalculateSize(1.0)
	assert.GreaterOrEqual(t, size, BaseSize, "size should be at least BaseSize")
	assert.LessOrEqual(t, size, MaxSize, "size should be at most MaxSize")
}

func TestCalculateSize_MultiplierScales(t *testing.T) {
	size1 := CalculateSize(1.0)
	size2 := CalculateSize(2.0)

	// size2 should be >= size1 (may be equal if at bounds)
	assert.GreaterOrEqual(t, size2, size1, "higher multiplier should give >= size")
}

func TestObservability_ReturnsValidSize(t *testing.T) {
	size := Observability()
	assert.GreaterOrEqual(t, size, BaseSize)
	assert.LessOrEqual(t, size, MaxSize)
}

func TestCritical_ReturnsValidSize(t *testing.T) {
	size := Critical()
	assert.GreaterOrEqual(t, size, BaseSize)
	assert.LessOrEqual(t, size, MaxSize)
}

func TestObservability_GreaterOrEqualCritical(t *testing.T) {
	obs := Observability()
	crit := Critical()

	// Observability uses 2x multiplier, Critical uses 1x
	// So Observability should be >= Critical
	assert.GreaterOrEqual(t, obs, crit, "Observability buffer should be >= Critical")
}

func TestClamp_EnforcesBounds(t *testing.T) {
	tests := []struct {
		name     string
		input    int
		expected int
	}{
		{"below minimum", 10, BaseSize},
		{"at minimum", BaseSize, BaseSize},
		{"in range", 500, 500},
		{"at maximum", MaxSize, MaxSize},
		{"above maximum", 20000, MaxSize},
		{"zero", 0, BaseSize},
		{"negative", -100, BaseSize},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := clamp(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
