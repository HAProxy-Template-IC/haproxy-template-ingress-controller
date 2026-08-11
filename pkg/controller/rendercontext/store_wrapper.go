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

// StoreWrapper wraps a stores.Store with template-friendly value-only methods,
// records read failures for the render boundary, and pins a single per-render
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
// On first access we call Store.List() once and build an in-memory
// composite-key index using the configured IndexBy JSONPath
// expressions. List() returns the snapshot; Fetch()/GetSingle() resolve
// against the in-memory index, with the same exact-match-vs-prefix-scan
// semantics MemoryStore.Get(...) offers. The wrapper is constructed
// fresh per Render() in rendercontext.Builder, so the cache lifetime
// is naturally one render.
//
// LazySnapshot mode (CachedStore-backed resources, typically Secrets):
// the eager Store.List() defeats the whole point of CachedStore —
// listing a CachedStore fans out into one API fetch per cached
// reference (see pkg/k8s/store/cached.go's "Listing cached store
// causes individual API lookups" WARN). For wrappers whose
// WatchedResources[name].Store == "on-demand", set LazySnapshot=true:
//
//  1. Snapshot is primed at first access from the underlying store's
//     CachedList() (only the LRU's warm entries — no API fetches).
//     If the store doesn't expose CachedList(), the snapshot starts
//     empty.
//  2. Fetch/GetSingle look up the snapshot index first. On miss they
//     call Store.Get(stringKeys...) for that single key, add the
//     result back into the snapshot + index, and return it. The
//     snapshot grows as the render touches keys; a key looked up
//     twice in the same render costs at most one API fetch (LRU
//     warm thereafter).
//  3. List() returns the snapshot as-is — the partial set the
//     render has assembled. No surprise full-cluster fetch, no
//     warning. Operators who set `store: on-demand` are opting out
//     of full-cluster iteration; templates that need to scan every
//     instance of a kind should use the default `store: full`.
//
// Per-render consistency: a key looked up via Fetch/GetSingle and
// then iterated via List() returns the same value (both served
// from the snapshot). The narrow weakening vs eager mode is that
// List() doesn't include uncached items the render never asked
// for — for the canonical "many Secrets, only a few touched" use
// case (Secrets is the whole reason `store: on-demand` exists),
// that's the contract, not a bug.
//
// If IndexBy is empty (e.g., a wrapper constructed for a store whose
// indexing config wasn't passed through), we still snapshot for List()
// but fall back to Store.Get(...) for keyed lookups — and warn, because
// that path can't honor the cross-method coherence guarantee.
//
// Resources in stores are already converted (floats to ints) at storage
// time, so StoreWrapper passes data through without additional processing.
type StoreWrapper struct {
	Store          stores.Store
	ResourceType   string
	Logger         *slog.Logger
	readContext    context.Context
	resourceErrors *ResourceErrorCollector

	// IndexBy mirrors the JSONPath expressions the underlying store uses
	// to index resources. Required for snapshot-served Fetch/GetSingle.
	IndexBy []string

	// LazySnapshot defers the eager Store.List() until List() is
	// actually called. Set when the underlying store is a CachedStore
	// (WatchedResources[name].Store == "on-demand"). See type doc.
	LazySnapshot bool

	cacheMu       sync.Mutex
	loaded        bool
	indexer       *indexer.Indexer // lazy mode: used to extract keys from items added incrementally
	snapshot      []any
	snapshotByKey map[string][]any // composite key (parts joined by "/") → matching items
}

// cachedLister is an optional interface implemented by stores that can
// return their in-memory cache contents without triggering API fetches.
// LazySnapshot wrappers use it to prime the snapshot cheaply at first
// access. CachedStore implements it; MemoryStore doesn't need to
// because eager mode is always cheap there.
type cachedLister interface {
	ListCached() ([]any, error)
}

func (w *StoreWrapper) storeContext() context.Context {
	if w.readContext != nil {
		return w.readContext
	}
	return context.Background()
}

func (w *StoreWrapper) getStore(keys ...string) ([]any, error) {
	ctx := w.storeContext()
	if store, ok := w.Store.(stores.ContextGetter); ok {
		return store.GetContext(ctx, keys...)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	items, err := w.Store.Get(keys...)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	return items, err
}

func (w *StoreWrapper) listStore() ([]any, error) {
	ctx := w.storeContext()
	if store, ok := w.Store.(stores.ContextLister); ok {
		return store.ListContext(ctx)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	items, err := w.Store.List()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	return items, err
}

func (w *StoreWrapper) warnReadFailure(message string, args ...any) {
	if w.storeContext().Err() == nil {
		w.Logger.Warn(message, args...)
	}
}

func (w *StoreWrapper) recordReadFailure(err error) {
	w.resourceErrors.Record(err)
}

func (w *StoreWrapper) prepareSnapshotIndex() {
	if len(w.IndexBy) == 0 {
		w.Logger.Warn("StoreWrapper has no IndexBy; Fetch/GetSingle will bypass the snapshot",
			"resource_type", w.ResourceType)
		return
	}

	idx, err := indexer.New(indexer.Config{IndexBy: w.IndexBy})
	if err != nil {
		w.recordReadFailure(fmt.Errorf("resource %q could not build its snapshot index: %w", w.ResourceType, err))
		w.Logger.Warn("Failed to build snapshot indexer; Fetch/GetSingle will bypass the snapshot",
			"resource_type", w.ResourceType,
			"index_by", w.IndexBy,
			"error", err)
		return
	}
	w.indexer = idx
	w.snapshotByKey = map[string][]any{}
}

func (w *StoreWrapper) loadInitialSnapshot() []any {
	if !w.LazySnapshot {
		items, err := w.listStore()
		if err != nil {
			w.recordReadFailure(fmt.Errorf("resource %q List failed: %w", w.ResourceType, err))
			w.warnReadFailure("Failed to list resources for snapshot",
				"resource_type", w.ResourceType, "error", err)
			return nil
		}
		return items
	}

	if w.storeContext().Err() != nil {
		return nil
	}
	lister, ok := w.Store.(cachedLister)
	if !ok {
		return nil
	}
	items, err := lister.ListCached()
	if err != nil {
		w.recordReadFailure(fmt.Errorf("resource %q cached snapshot failed: %w", w.ResourceType, err))
		w.warnReadFailure("Failed to list cached resources for snapshot prime",
			"resource_type", w.ResourceType, "error", err)
		return nil
	}
	return items
}

// loadSnapshot pins the per-instance snapshot on first access. In
// eager mode, calls Store.List() and indexes everything. In lazy mode,
// primes from the underlying store's ListCached() (LRU-only, no API
// fetches) and leaves room for incremental growth via addToSnapshot.
// Caller must hold cacheMu.
func (w *StoreWrapper) loadSnapshot() {
	if w.loaded {
		return
	}
	w.loaded = true
	w.prepareSnapshotIndex()
	items := w.loadInitialSnapshot()
	w.snapshot = items
	if w.snapshotByKey == nil {
		return
	}
	for _, item := range items {
		w.indexItemLocked(item)
	}
}

func (w *StoreWrapper) getWithoutSnapshotIndex(stringKeys []string, op string) []any {
	items, err := w.getStore(stringKeys...)
	if err != nil {
		w.recordReadFailure(fmt.Errorf("resource %q %s lookup %q failed: %w", w.ResourceType, op, stringKeys, err))
		w.warnReadFailure("Failed to get resources from store (no snapshot index)",
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

func (w *StoreWrapper) getExact(stringKeys []string, op string) []any {
	composite := strings.Join(stringKeys, "/")
	if items, ok := w.snapshotByKey[composite]; ok {
		w.Logger.Log(context.Background(), logging.LevelTrace, "store get called (snapshot, exact)",
			"resource_type", w.ResourceType,
			"op", op,
			"keys", stringKeys,
			"found_count", len(items))
		return items
	}
	if !w.LazySnapshot {
		w.Logger.Log(context.Background(), logging.LevelTrace, "store get called (snapshot, exact miss)",
			"resource_type", w.ResourceType,
			"op", op,
			"keys", stringKeys,
			"found_count", 0)
		return nil
	}

	items, err := w.getStore(stringKeys...)
	if err != nil {
		w.recordReadFailure(fmt.Errorf("resource %q %s lookup %q failed: %w", w.ResourceType, op, stringKeys, err))
		w.warnReadFailure("Failed to fetch single key for lazy snapshot",
			"resource_type", w.ResourceType,
			"op", op,
			"keys", stringKeys,
			"error", err)
		return []any{}
	}
	w.addToSnapshotLocked(items)
	if _, indexed := w.snapshotByKey[composite]; !indexed {
		w.snapshotByKey[composite] = []any{}
	}
	w.Logger.Log(context.Background(), logging.LevelTrace, "store get called (lazy fetch)",
		"resource_type", w.ResourceType,
		"op", op,
		"keys", stringKeys,
		"found_count", len(items))
	return items
}

func (w *StoreWrapper) getPrefix(stringKeys []string, op string) []any {
	prefix := strings.Join(stringKeys, "/") + "/"
	var results []any
	for key, items := range w.snapshotByKey {
		if strings.HasPrefix(key, prefix) {
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

// indexItemLocked extracts index keys from item and appends it to the
// snapshot index. Caller must hold cacheMu. No-op when the indexer
// isn't available (no IndexBy or indexer build failed).
func (w *StoreWrapper) indexItemLocked(item any) {
	if w.indexer == nil || w.snapshotByKey == nil {
		return
	}
	keys, err := w.indexer.ExtractKeys(item)
	if err != nil {
		w.recordReadFailure(fmt.Errorf("resource %q could not index a snapshot item: %w", w.ResourceType, err))
		// Item appears but isn't reachable via keyed lookup.
		// Mirrors what would happen if the underlying store had
		// also failed to extract keys.
		w.Logger.Warn("Failed to extract snapshot index keys for item",
			"resource_type", w.ResourceType, "error", err)
		return
	}
	composite := strings.Join(keys, "/")
	w.snapshotByKey[composite] = append(w.snapshotByKey[composite], item)
}

// addToSnapshotLocked appends items fetched on-demand into the
// snapshot and updates the keyed index. Used by the lazy-mode
// per-key fetch path. Caller must hold cacheMu.
func (w *StoreWrapper) addToSnapshotLocked(items []any) {
	for _, item := range items {
		w.snapshot = append(w.snapshot, item)
		w.indexItemLocked(item)
	}
}

// List returns all resources from the per-render snapshot.
//
// First call lazily loads the snapshot. In eager mode the snapshot
// covers everything in the underlying store. In lazy mode it covers
// (initial: LRU-warm entries) + (incrementally: any keys touched via
// Fetch/GetSingle during this render). Subsequent calls return the
// same slice — every read in one render observes the same view, and
// keys looked up after List() will GROW the snapshot but won't change
// what earlier List() snippets already saw (sliced references are
// stable; new entries land past the original length).
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
// MemoryStore.Get.
//
// In LazySnapshot mode, an exact-match miss falls through to a single
// Store.Get(stringKeys...) call (LRU-cached for CachedStore); the
// fetched items are added to the snapshot + index so subsequent
// lookups (and any later List()) see them. Partial-match misses
// don't trigger a fetch — there's no general way to enumerate "all
// keys with this prefix" without doing the very full-list we're
// avoiding, so prefix scans see only items already in the snapshot.
//
// When the snapshot wasn't indexed (no IndexBy), falls back to
// Store.Get(stringKeys...) — that path can disagree with the snapshot
// returned by List(), but the wrapper logged a warning when the
// snapshot was loaded so operators can see why.
func (w *StoreWrapper) get(stringKeys []string, op string) []any {
	w.cacheMu.Lock()
	defer w.cacheMu.Unlock()
	w.loadSnapshot()

	if w.snapshotByKey == nil {
		return w.getWithoutSnapshotIndex(stringKeys, op)
	}

	if len(stringKeys) == len(w.IndexBy) {
		return w.getExact(stringKeys, op)
	}
	return w.getPrefix(stringKeys, op)
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
// A failed read returns an empty slice to the template and fails the render at
// its next phase boundary.
func (w *StoreWrapper) Fetch(keys ...any) []any {
	stringKeys := make([]string, len(keys))
	for i, key := range keys {
		stringKeys[i] = toString(key)
	}
	return w.get(stringKeys, "Fetch")
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
//   - nil if multiple resources match; the ambiguity then fails the render
//
// A failed read returns nil to the template and fails the render at its next
// phase boundary.
func (w *StoreWrapper) GetSingle(keys ...any) any {
	stringKeys := make([]string, len(keys))
	for i, key := range keys {
		stringKeys[i] = toString(key)
	}

	items := w.get(stringKeys, "GetSingle")

	if len(items) == 0 {
		return nil
	}

	if len(items) > 1 {
		w.recordReadFailure(fmt.Errorf(
			"resource %q GetSingle lookup %q matched %d objects; use Fetch or configure unique indexBy values",
			w.ResourceType, stringKeys, len(items)))
		w.Logger.Error("GetSingle found multiple resources (ambiguous lookup)",
			"resource_type", w.ResourceType,
			"keys", stringKeys,
			"count", len(items))
		return nil
	}

	return items[0]
}
