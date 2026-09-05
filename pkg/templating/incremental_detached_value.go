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

package templating

import (
	"errors"
	"sync/atomic"
)

// IncrementalDetachedValue carries an isolated value across the recorder boundary.
type IncrementalDetachedValue struct {
	seal     *IncrementalDetachedValue
	value    any
	consumed atomic.Bool
}

// NewIncrementalDetachedValue creates a single-owner immutable value transfer.
func NewIncrementalDetachedValue(value any) (*IncrementalDetachedValue, error) {
	detached, err := cloneIncrementalSerialization(value)
	if err != nil {
		return nil, err
	}
	result := &IncrementalDetachedValue{value: detached}
	result.seal = result
	return result, nil
}

// ConsumeIncrementalDetachedValue transfers ownership exactly once.
func ConsumeIncrementalDetachedValue(
	value *IncrementalDetachedValue,
) (any, error) {
	if value == nil || value.seal != value || !value.consumed.CompareAndSwap(false, true) {
		return nil, errors.New("detached incremental value has invalid transfer provenance")
	}
	return value.value, nil
}
