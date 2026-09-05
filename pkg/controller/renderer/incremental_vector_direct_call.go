// Copyright 2026 Philipp Hossner
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

package renderer

import (
	"context"
	"fmt"
)

func (e *incrementalVectorExecution) enterDirect(
	index int,
	operation string,
) (*incrementalVectorItemState, error) {
	e.callGate.RLock()
	if !e.valid() || e.failed.Load() || index < 0 || index >= len(e.items) ||
		e.active.Load() != int64(index) {
		e.callGate.RUnlock()
		return nil, e.recordViolation(fmt.Errorf(
			"%s used inactive incremental component vector item %d",
			operation,
			index,
		))
	}
	item := &e.items[index]
	token, _ := item.ctx.Value(incrementalVectorExecutionContextKey{}).(*incrementalVectorItemToken)
	if !token.valid(e) || token.index != index {
		e.callGate.RUnlock()
		return nil, e.recordViolation(fmt.Errorf(
			"%s crossed incremental component vector item %d",
			operation,
			index,
		))
	}
	if cause := context.Cause(item.ctx); cause != nil {
		e.callGate.RUnlock()
		return nil, e.recordViolation(cause)
	}
	e.inflight.Add(1)
	return item, nil
}

func (e *incrementalVectorExecution) leaveDirect() {
	if calls := e.inflight.Add(-1); calls < 0 {
		e.callGate.RUnlock()
		panic("negative incremental component vector invocation count")
	}
	e.callGate.RUnlock()
}
