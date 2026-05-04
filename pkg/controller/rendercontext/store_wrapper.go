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

package rendercontext

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"gitlab.com/haproxy-haptic/haptic/pkg/core/logging"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/indexer"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// Verify StoreWrapper implements templating.ResourceStore at compile time.
// This enables Scriggo templates to call methods directly on resource stores:
//
//	{% for _, ing := range resources.ingresses.List() %}
var _ templating.ResourceStore = (*StoreWrapper)(nil)

// toString converts various types to string for template compatibility.
//
// This helper handles type conversions:
// - string: returned as-is
// - fmt.Stringer: any type with String() method
// - other types: formatted using fmt.Sprintf
//
// This allows template methods to accept any arguments.
func toString(v any) string {
	switch val := v.(type) {
	case string:
		// Fast path for regular strings
		return val
	case fmt.Stringer:
		// Handles types with String() method
		return val.String()
	default:
		// Fallback: format as string
		return fmt.Sprint(v)
	}
}

// StoreWrapper wraps a stores.Store to provide template-friendly methods
// (no error returns; errors are logged) AND pins a single per-render
// snapshot of the underlying store so every read in one render — List(),
// Fetch(), or GetSingle() — observes the same state.
//
// Why per-render pinning matters: the live informer-backed store mutates
// during admission validation (a parallel test's Ingress lands in the
// store between two snippet executions), so two raw List() calls in one
// render can return different snapshots, and a List() vs Fetch() pair on
// the same resource type can disagree about what's in the store. The
// chart's auth pattern hit exactly the first variant — global-top emits
// userlists from one List() snapshot, backend-directives emits
// http_auth(...) refs from a later (mutated) List() snapshot, leaving
// the rendered config with an http_auth pointing at a userlist that no
// snippet emitted. HAProxy then rejects the config at admission time.
//
// On first access of any kind we call Store.List() once and build an
// in-memory composite-key index using the configured IndexBy JSONPath
// expressions. List() returns the snapshot; Fetch()/GetSingle() resolve
// against the in-memory index, with the same exact-match-vs-prefix-scan
// semantics MemoryStore.Get(...) offers. The wrapper is constructed
// fresh per Render() in rendercontext.Builder, so the cache lifetime
// is naturally one render.
//
// If IndexBy is empty (e.g., a wrapper constructed for a store whose
// indexing config wasn't passed through), we still snapshot for List()
// but fall back to Store.Get(...) for keyed lookups — and warn, because
// that path can't honor the cross-method coherence guarantee.
//
// Resources in stores are already converted (floats to ints) at storage
// time, so StoreWrapper passes data through without additional processing.
type StoreWrapper struct {
	Store        stores.Store
	ResourceType string
	Logger       *slog.Logger

	// IndexBy mirrors the JSONPath expressions the underlying store uses
	// to index resources. Required for snapshot-served Fetch/GetSingle.
	IndexBy []string

	cacheMu       sync.Mutex
	loaded        bool
	snapshot      []any
	snapshotByKey map[string][]any // composite key (parts joined by "/") → matching items
}

// loadSnapshot pins the per-instance snapshot on first access. Caller
// must hold cacheMu.
func (w *StoreWrapper) loadSnapshot() {
	if w.loaded {
		return
	}
	w.loaded = true

	items, err := w.Store.List()
	if err != nil {
		w.Logger.Warn("failed to list resources for snapshot",
			"resource_type", w.ResourceType, "error", err)
		items = []any{}
	}
	w.snapshot = items

	if len(w.IndexBy) == 0 {
		// No indexing config: List() works against the snapshot, but
		// Fetch/GetSingle have to fall back to direct store calls
		// (which can observe a state that diverges from the snapshot).
		// The Builder is expected to wire IndexBy for every wrapper;
		// this branch is a safety net, not a normal path.
		w.Logger.Warn("StoreWrapper has no IndexBy; Fetch/GetSingle will bypass the snapshot",
			"resource_type", w.ResourceType)
		return
	}

	idx, err := indexer.New(indexer.Config{IndexBy: w.IndexBy})
	if err != nil {
		w.Logger.Warn("failed to build snapshot indexer; Fetch/GetSingle will bypass the snapshot",
			"resource_type", w.ResourceType,
			"index_by", w.IndexBy,
			"error", err)
		return
	}

	w.snapshotByKey = make(map[string][]any, len(items))
	for _, item := range items {
		keys, err := idx.ExtractKeys(item)
		if err != nil {
			// Item appears in List() but isn't reachable via keyed
			// lookup. Mirrors what would happen if the underlying
			// store had also failed to extract keys.
			w.Logger.Warn("failed to extract snapshot index keys for item",
				"resource_type", w.ResourceType, "error", err)
			continue
		}
		composite := strings.Join(keys, "/")
		w.snapshotByKey[composite] = append(w.snapshotByKey[composite], item)
	}
}

// List returns all resources from the per-render snapshot.
//
// First call lazily loads the snapshot via Store.List(). Subsequent calls
// return the same slice — every read in one render observes the same
// store state.
func (w *StoreWrapper) List() []any {
	w.cacheMu.Lock()
	defer w.cacheMu.Unlock()
	w.loadSnapshot()

	w.Logger.Log(context.Background(), logging.LevelTrace, "store list called",
		"resource_type", w.ResourceType,
		"count", len(w.snapshot))

	return w.snapshot
}

// get is the shared snapshot-keyed lookup used by Fetch and GetSingle.
//
// When the snapshot was indexed (IndexBy was set and the indexer built
// cleanly), exact-match (full key set) hits the in-memory map and
// partial-match (prefix) does a small scan — same shape as
// MemoryStore.Get. When the snapshot wasn't indexed, falls back to
// Store.Get(stringKeys...) — that path can disagree with the snapshot
// returned by List(), but the wrapper logged a warning when the snapshot
// was loaded so operators can see why.
func (w *StoreWrapper) get(stringKeys []string, op string) []any {
	w.cacheMu.Lock()
	defer w.cacheMu.Unlock()
	w.loadSnapshot()

	if w.snapshotByKey == nil {
		// Fall-back path: no snapshot index available. We still go
		// through the live store rather than returning empty, so this
		// wrapper remains functional even without IndexBy.
		items, err := w.Store.Get(stringKeys...)
		if err != nil {
			w.Logger.Warn("failed to get resources from store (no snapshot index)",
				"resource_type", w.ResourceType,
				"op", op,
				"keys", stringKeys,
				"error", err)
			return []any{}
		}
		w.Logger.Log(context.Background(), logging.LevelTrace, "store get called (live, no snapshot)",
			"resource_type", w.ResourceType,
			"op", op,
			"keys", stringKeys,
			"found_count", len(items))
		return items
	}

	if len(stringKeys) == len(w.IndexBy) {
		composite := strings.Join(stringKeys, "/")
		items := w.snapshotByKey[composite]
		w.Logger.Log(context.Background(), logging.LevelTrace, "store get called (snapshot, exact)",
			"resource_type", w.ResourceType,
			"op", op,
			"keys", stringKeys,
			"found_count", len(items))
		return items
	}

	// Partial-match prefix scan over the snapshot index.
	prefix := strings.Join(stringKeys, "/") + "/"
	var results []any
	for k, items := range w.snapshotByKey {
		if strings.HasPrefix(k, prefix) {
			results = append(results, items...)
		}
	}
	w.Logger.Log(context.Background(), logging.LevelTrace, "store get called (snapshot, prefix)",
		"resource_type", w.ResourceType,
		"op", op,
		"keys", stringKeys,
		"found_count", len(results))
	return results
}

// Fetch performs O(1) indexed lookup over the per-render snapshot using
// the provided keys.
//
// This method enables efficient lookups in templates and supports
// non-unique index keys by returning all resources matching the provided
// keys:
//
//	{% for endpoint_slice in resources.endpoints.Fetch(service_name) %}
//	  {{ endpoint_slice.metadata.name }}
//	{% endfor %}
//
// The keys must match the index configuration for the resource type. For
// example, if EndpointSlices are indexed by service name:
//
//	index_by: ["metadata.labels['kubernetes.io/service-name']"]
//
// Then you can look them up with:
//
//	resources.endpoints.Fetch("my-service")
//
// This will return ALL EndpointSlices for that service (typically
// multiple).
//
// Accepts any arguments for template compatibility.
//
// If an error occurs during snapshot loading, it's logged and an empty
// slice is returned.
func (w *StoreWrapper) Fetch(keys ...any) []any {
	stringKeys := make([]string, len(keys))
	for i, key := range keys {
		stringKeys[i] = toString(key)
	}
	return w.get(stringKeys, "fetch")
}

// GetSingle performs O(1) indexed lookup over the per-render snapshot
// and expects exactly one matching resource.
//
// This method is useful when you know the index keys uniquely identify
// a resource:
//
//	{% set ingress = resources.ingresses.GetSingle("default", "my-ingress") %}
//	{% if ingress %}
//	  {{ ingress.metadata.name }}
//	{% endif %}
//
//	{# Cross-namespace reference #}
//	{% set ref = "namespace/name".split("/") %}
//	{% set secret = resources.secrets.GetSingle(ref[0], ref[1]) %}
//
// Accepts any arguments for template compatibility.
//
// Returns:
//   - nil if no resources match (this is NOT an error - allows templates
//     to check existence)
//   - The single matching resource if exactly one matches
//   - nil + logs error if multiple resources match (ambiguous lookup)
//
// If an error occurs during snapshot loading, it's logged and nil is
// returned.
func (w *StoreWrapper) GetSingle(keys ...any) any {
	stringKeys := make([]string, len(keys))
	for i, key := range keys {
		stringKeys[i] = toString(key)
	}

	items := w.get(stringKeys, "get_single")

	if len(items) == 0 {
		return nil
	}

	if len(items) > 1 {
		w.Logger.Error("GetSingle found multiple resources (ambiguous lookup)",
			"resource_type", w.ResourceType,
			"keys", stringKeys,
			"count", len(items))
		return nil
	}

	return items[0]
}
