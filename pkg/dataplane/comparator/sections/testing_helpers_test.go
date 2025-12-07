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

package sections

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// assertOperation validates that an Operation has the expected properties.
// This helper reduces repetition in factory function tests.
func assertOperation(t *testing.T, op Operation, wantType OperationType, wantSection string, wantPriority int, wantDescContains string) {
	t.Helper()

	assert.Equal(t, wantType, op.Type(), "unexpected operation type")
	assert.Equal(t, wantSection, op.Section(), "unexpected section")
	assert.Equal(t, wantPriority, op.Priority(), "unexpected priority")
	assert.Contains(t, op.Describe(), wantDescContains, "description should contain expected text")
}
