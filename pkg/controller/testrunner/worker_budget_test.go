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

package testrunner

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAutomaticWorkers(t *testing.T) {
	tests := []struct {
		name        string
		cpus        int
		memoryLimit int64
		want        int
	}{
		{"unlimited memory follows CPUs", 32, math.MaxInt64, 32},
		{"one GiB cgroup with default Go headroom", 32, 966367641, 7},
		{"CPU quota bounds a large memory budget", 2, 8 << 30, 2},
		{"small budget retains one worker", 32, 64 << 20, 1},
		{"exact worker allowance", 32, 256 << 20, 2},
		{"partial allowance does not start another worker", 32, (256 << 20) - 1, 1},
		{"zero soft limit retains one worker", 8, 0, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, automaticWorkers(tt.cpus, tt.memoryLimit))
		})
	}
}
