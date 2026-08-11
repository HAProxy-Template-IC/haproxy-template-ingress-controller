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

package rendercontext

import (
	"context"
	"errors"
	"sort"
	"sync"
)

// ResourceInputError reports a watched-resource failure that invalidates the render.
type ResourceInputError struct {
	cause error
}

func (e *ResourceInputError) Error() string {
	return "reading template resources: " + e.cause.Error()
}

// Unwrap exposes the store, index, or materialization failure.
func (e *ResourceInputError) Unwrap() error {
	return e.cause
}

// ResourceErrorCollector records failures hidden by template-facing value-only APIs.
type ResourceErrorCollector struct {
	mu     sync.Mutex
	byText map[string]error
}

// NewResourceErrorCollector creates an empty collector for one render.
func NewResourceErrorCollector() *ResourceErrorCollector {
	return &ResourceErrorCollector{byText: make(map[string]error)}
}

// Record adds an error, deduplicating repeated reads of the same failed input.
func (c *ResourceErrorCollector) Record(err error) {
	if c == nil || err == nil {
		return
	}
	c.mu.Lock()
	if c.byText == nil {
		c.byText = make(map[string]error)
	}
	c.byText[err.Error()] = err
	c.mu.Unlock()
}

// Err returns all recorded errors in deterministic order.
func (c *ResourceErrorCollector) Err() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	keys := make([]string, 0, len(c.byText))
	for key := range c.byText {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]error, 0, len(keys))
	for _, key := range keys {
		result = append(result, c.byText[key])
	}
	c.mu.Unlock()
	return errors.Join(result...)
}

// Err returns cancellation first, then any resource failures recorded during the render.
func (r *BuildResult) Err(ctx context.Context) error {
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	if r == nil || r.ResourceErrors == nil {
		return nil
	}
	if err := r.ResourceErrors.Err(); err != nil {
		return &ResourceInputError{cause: err}
	}
	return nil
}
