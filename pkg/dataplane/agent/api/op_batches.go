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

package api

import "fmt"

// ValidateOpBatches enforces the per-batch and complete-transaction budgets.
func (m *Manifest) ValidateOpBatches() error {
	if count := len(m.Ops) + len(m.InPlaceOps); count > MaxOpsPerApply {
		return fmt.Errorf("%d ops exceed the %d-op limit", count, MaxOpsPerApply)
	}
	if len(m.OpBatches) >= MaxOpBatches {
		return fmt.Errorf("%d operation batches exceed the %d-batch limit", 1+len(m.OpBatches), MaxOpBatches)
	}
	for i, batch := range m.OpBatches {
		if len(batch) == 0 || len(batch) > MaxOpsPerApply {
			return fmt.Errorf("operation batch %d must contain 1 to %d ops; got %d", i+2, MaxOpsPerApply, len(batch))
		}
	}
	return nil
}

// RuntimeOps returns all runtime operations in execution order.
func (m *Manifest) RuntimeOps() []Op {
	if len(m.OpBatches) == 0 {
		return m.Ops
	}
	count := len(m.Ops)
	for _, batch := range m.OpBatches {
		count += len(batch)
	}
	ops := make([]Op, 0, count)
	ops = append(ops, m.Ops...)
	for _, batch := range m.OpBatches {
		ops = append(ops, batch...)
	}
	return ops
}
