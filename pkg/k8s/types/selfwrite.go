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

package types

import (
	"sync"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

// SelfWriteFilter answers whether a watch event carries a write this
// controller made itself, identified by the object's resourceVersion the API
// server returned for that write.
type SelfWriteFilter interface {
	IsSelfWrite(gr schema.GroupResource, namespace, name, resourceVersion string) bool
}

// SelfWriteRegistry records the resourceVersions of the controller's own
// writes so watchers can refresh their store from the echoed watch event
// without treating it as a change worth re-rendering for. Every render
// derives the status it writes from the same inputs, so nothing downstream
// changes when that status comes back; before this filter each status write
// cost a full render (three to four per route change under sequential churn).
//
// Keyed by GroupResource, not GroupVersionResource: the resourceVersion is a
// property of the object, so a write through one served version matches the
// event a watcher on another version receives.
type SelfWriteRegistry struct {
	mu      sync.Mutex
	entries map[selfWriteKey]struct{}
	order   []selfWriteKey
	limit   int
}

type selfWriteKey struct {
	group, resource, namespace, name, resourceVersion string
}

// DefaultSelfWriteLimit bounds the registry: entries are consumed by the
// echo that follows each write within milliseconds, so the bound only matters
// when watch events are lost; the oldest entry is evicted first.
const DefaultSelfWriteLimit = 4096

// NewSelfWriteRegistry returns an empty registry holding at most limit
// entries (DefaultSelfWriteLimit when limit <= 0).
func NewSelfWriteRegistry(limit int) *SelfWriteRegistry {
	if limit <= 0 {
		limit = DefaultSelfWriteLimit
	}
	return &SelfWriteRegistry{
		entries: make(map[selfWriteKey]struct{}, limit),
		order:   make([]selfWriteKey, 0, limit),
		limit:   limit,
	}
}

// Record remembers a write the controller made. An empty resourceVersion is
// ignored: without it the echo cannot be identified.
func (r *SelfWriteRegistry) Record(gr schema.GroupResource, namespace, name, resourceVersion string) {
	if r == nil || resourceVersion == "" {
		return
	}
	k := selfWriteKey{gr.Group, gr.Resource, namespace, name, resourceVersion}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.entries[k]; ok {
		return
	}
	if len(r.order) >= r.limit {
		delete(r.entries, r.order[0])
		r.order = r.order[1:]
	}
	r.entries[k] = struct{}{}
	r.order = append(r.order, k)
}

// IsSelfWrite reports whether the event identified by the arguments echoes a
// recorded write. Entries are kept (not consumed): several watchers may
// observe the same object, and the FIFO bound retires them.
func (r *SelfWriteRegistry) IsSelfWrite(gr schema.GroupResource, namespace, name, resourceVersion string) bool {
	if r == nil || resourceVersion == "" {
		return false
	}
	k := selfWriteKey{gr.Group, gr.Resource, namespace, name, resourceVersion}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.entries[k]
	return ok
}
