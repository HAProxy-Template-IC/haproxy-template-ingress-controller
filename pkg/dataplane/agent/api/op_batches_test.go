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

package api_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
)

func TestManifestOperationBudgets(t *testing.T) {
	tests := []struct {
		name                                string
		ops, inPlace, additional, batchSize int
		valid                               bool
	}{
		{name: "empty", valid: true},
		{name: "one full batch", ops: api.MaxOpsPerApply, valid: true},
		{name: "shared first batch", ops: 600, inPlace: 400, valid: true},
		{name: "oversized first batch", ops: api.MaxOpsPerApply + 1},
		{name: "combined overflow", ops: 600, inPlace: 401},
		{name: "eight full batches", ops: api.MaxOpsPerApply, additional: 7, batchSize: api.MaxOpsPerApply, valid: true},
		{name: "ninth batch", additional: 8, batchSize: 1},
		{name: "empty continuation", additional: 1},
		{name: "oversized continuation", additional: 1, batchSize: api.MaxOpsPerApply + 1},
		{name: "full in-place batch", inPlace: api.MaxOpsPerApply, additional: 1, batchSize: 1, valid: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := api.Manifest{Ops: make([]api.Op, tt.ops), InPlaceOps: make([]api.Op, tt.inPlace)}
			for range tt.additional {
				m.OpBatches = append(m.OpBatches, make([]api.Op, tt.batchSize))
			}
			if tt.valid {
				assert.NoError(t, m.ValidateOpBatches())
			} else {
				assert.Error(t, m.ValidateOpBatches())
			}
		})
	}
}
