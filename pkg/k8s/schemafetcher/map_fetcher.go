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

package schemafetcher

import (
	"context"
	"sync"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/kube-openapi/pkg/validation/spec"
)

// MapFetcher serves pre-populated schemas indexed by GVK. Useful in
// tests where standing up the real cluster machinery (discovery
// client + CRD client) is overkill, and in any future "schemas from
// a file on disk" path where the controller still wants to drive
// typegen without talking to the API server.
//
// Concurrent reads via Fetch are safe; concurrent writes via Add
// are guarded by a mutex.
//
// The zero value is ready to use.
type MapFetcher struct {
	mu      sync.RWMutex
	schemas map[schema.GroupVersionKind]*spec.Schema
}

// NewMapFetcher returns a [MapFetcher] pre-populated with the supplied
// schemas. The map is copied so subsequent mutations of the caller's
// map don't affect lookups.
func NewMapFetcher(seed map[schema.GroupVersionKind]*spec.Schema) *MapFetcher {
	f := &MapFetcher{schemas: make(map[schema.GroupVersionKind]*spec.Schema, len(seed))}
	for k, v := range seed {
		f.schemas[k] = v
	}
	return f
}

// Add registers a schema for the supplied GVK, overwriting any
// previous entry for that GVK. Returning the receiver lets callers
// chain Add calls in test setup.
func (f *MapFetcher) Add(gvk schema.GroupVersionKind, sch *spec.Schema) *MapFetcher {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.schemas == nil {
		f.schemas = make(map[schema.GroupVersionKind]*spec.Schema)
	}
	f.schemas[gvk] = sch
	return f
}

// Fetch implements [Fetcher]. Missing GVKs surface as
// [ErrSchemaNotAvailable] wrapping the [IsNotFound]-able sentinel,
// matching the cluster fetcher's contract so tests exercise the same
// fail-open code paths in callers.
//
// MapFetcher returns nil for the components map: its seed schemas
// are expected to be self-contained (inlined refs). Tests that need
// $ref resolution should populate the test components via a
// separate seeding mechanism (currently none — extend if needed).
func (f *MapFetcher) Fetch(_ context.Context, gvk schema.GroupVersionKind) (*spec.Schema, map[string]spec.Schema, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	sch, ok := f.schemas[gvk]
	if !ok {
		return nil, nil, &ErrSchemaNotAvailable{GVK: gvk, Cause: errNotFound}
	}
	return sch, nil, nil
}
