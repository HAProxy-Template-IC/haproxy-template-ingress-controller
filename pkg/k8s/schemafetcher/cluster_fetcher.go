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
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/kube-openapi/pkg/spec3"
	"k8s.io/kube-openapi/pkg/validation/spec"
)

// CRDLister returns the cluster's installed CRDs. The production
// implementation calls the apiextensions client; tests stand up a
// stub that returns a fixed slice.
//
// Listing the full set on every Fetch would be wasteful — controllers
// boot once per CRD version of HAProxyTemplateConfig, and during that
// boot they fetch schemas for every watched resource. The
// [ClusterFetcher] caches the list internally; CRDLister
// implementations are only consulted once.
type CRDLister interface {
	ListCRDs(ctx context.Context) ([]apiextensionsv1.CustomResourceDefinition, error)
}

// OpenAPIV3Provider returns the cluster's OpenAPI v3 spec for a
// GroupVersion. Wraps client-go's [openapi3.Root].GVSpec so tests
// can stub the call without standing up a fake discovery client.
type OpenAPIV3Provider interface {
	GVSpec(ctx context.Context, gv schema.GroupVersion) (*spec3.OpenAPI, error)
}

// ClusterFetcher resolves schemas against the cluster. It tries CRDs
// first (cheap, single-resource read once the list is cached) and
// falls back to the aggregated OpenAPI v3 endpoint for built-ins
// AND for any CRD-defined resource whose schema didn't load via the
// CRD path (e.g. because the controller's RBAC doesn't include
// `apiextensions.k8s.io/customresourcedefinitions list`).
//
// Concurrency: Fetch is safe for concurrent use. The CRD list is
// pulled at most once per successful boot (guarded by a mutex);
// the OpenAPI per-GroupVersion specs are pulled at most once per
// GV (guarded by a per-GV done channel cache). Context-scoped
// failures (cancellation, deadline) DO NOT poison the cache —
// the relevant entry is evicted so the next caller retries with
// a fresh context.
type ClusterFetcher struct {
	crds    CRDLister
	openapi OpenAPIV3Provider

	// crdState protects the cached CRD list (or its terminal error).
	// We deliberately don't use sync.Once: it caches the first
	// outcome forever, which permanently poisons the fetcher if the
	// first caller's context expired mid-fetch. Replacing Once with
	// a mutex + done channel lets us evict the cached entry on
	// context cancellation while still coalescing concurrent
	// non-cancelled callers onto a single API call.
	crdState struct {
		mu      sync.Mutex
		done    chan struct{} // closed when populated; nil before first attempt
		list    []apiextensionsv1.CustomResourceDefinition
		err     error
		fetched bool // true after a successful fetch — never re-attempt then
	}

	gvSpecsMu sync.Mutex
	gvSpecs   map[schema.GroupVersion]*gvSpecCache
}

// gvSpecCache holds the once-computed result of fetching a single
// GroupVersion's OpenAPI v3 spec. We don't use sync.Map directly
// because LoadOrStore can't carry the "still computing" state we
// need to coalesce concurrent fetches for the same GV.
type gvSpecCache struct {
	done    chan struct{}
	spec    *spec3.OpenAPI
	err     error
	evicted bool // leader observed ctx error and removed entry from gvSpecs
}

// NewClusterFetcher returns a [ClusterFetcher] that uses the supplied
// CRD lister and OpenAPI v3 provider. Both must be non-nil — passing
// nil for either is a programming error and panics on construction
// rather than failing the first Fetch with an obscure nil-pointer.
func NewClusterFetcher(crds CRDLister, openapi OpenAPIV3Provider) *ClusterFetcher {
	if crds == nil || openapi == nil {
		panic("schemafetcher: NewClusterFetcher requires non-nil CRDLister and OpenAPIV3Provider")
	}
	return &ClusterFetcher{
		crds:    crds,
		openapi: openapi,
		gvSpecs: make(map[schema.GroupVersion]*gvSpecCache),
	}
}

// Fetch implements [Fetcher]. Tries the CRD index first because it's
// cheaper and gives the highest-fidelity schema (the CRD's own
// openAPIV3Schema, including x-kubernetes-preserve-unknown-fields
// subtrees). Falls back to the aggregated OpenAPI v3 endpoint for
// built-ins AND for the case where the CRD path fails for any
// reason — RBAC denial, parse failure, transient list error.
//
// The OpenAPI v3 endpoint is readable by every authenticated user
// on a working cluster and covers every registered CRD, so it's a
// strict superset of the CRD path's coverage. Falling back on
// CRD-path failures (not just NotFound) keeps typed access working
// in security-restricted environments where the controller can't
// list CRDs cluster-wide.
//
// When both paths fail, the OpenAPI error wins as the user-facing
// cause (it's the path with broader coverage and lower permission
// requirements), with the CRD-path error preserved in the chain
// via errors.Join so operators investigating partial-access
// regressions can still see both.
func (f *ClusterFetcher) Fetch(ctx context.Context, gvk schema.GroupVersionKind) (*spec.Schema, map[string]spec.Schema, error) {
	sch, crdErr := f.fetchFromCRDs(ctx, gvk)
	if crdErr == nil {
		// CRD schemas inline every shape — no shared components.
		return sch, nil, nil
	}

	sch, components, openAPIErr := f.fetchFromOpenAPI(ctx, gvk)
	if openAPIErr == nil {
		return sch, components, nil
	}

	// Both paths failed. Preserve both causes so operators can tell
	// "CRD list denied + GV not in OpenAPI" (real RBAC + missing
	// registration) from "CRD list denied + transient OpenAPI 502"
	// (purely RBAC + retryable). errors.Is(...) still works for the
	// IsNotFound sentinel when either leg returned a NotFound.
	return nil, nil, errors.Join(openAPIErr, crdErr)
}

// fetchFromCRDs looks for a CRD whose group + Kind matches the
// requested GVK. The fetcher caches the CRD list once per
// ClusterFetcher lifetime; iteration over the cached slice is O(N)
// in CRD count which is small (~100 even on loaded clusters), so a
// linear scan is fine and avoids carrying a RESTMapper through the
// schema layer.
//
// Returns the schema if found, an *ErrSchemaNotAvailable wrapping
// errNotFound when the CRD doesn't exist (or the underlying list
// permission is missing), or a different *ErrSchemaNotAvailable
// when the CRD exists but its schema doesn't (malformed CRD,
// missing openAPIV3Schema, etc.).
func (f *ClusterFetcher) fetchFromCRDs(ctx context.Context, gvk schema.GroupVersionKind) (*spec.Schema, error) {
	list, err := f.ensureCRDList(ctx)
	if err != nil {
		return nil, &ErrSchemaNotAvailable{GVK: gvk, Cause: err}
	}

	for i := range list {
		crd := &list[i]
		if crd.Spec.Group != gvk.Group {
			continue
		}
		if crd.Spec.Names.Kind != gvk.Kind {
			continue
		}
		// Found the CRD. Pick the requested version.
		for vi := range crd.Spec.Versions {
			v := &crd.Spec.Versions[vi]
			if v.Name != gvk.Version {
				continue
			}
			if v.Schema == nil || v.Schema.OpenAPIV3Schema == nil {
				return nil, &ErrSchemaNotAvailable{
					GVK:   gvk,
					Cause: fmt.Errorf("CRD %s has no openAPIV3Schema for version %s", crd.Name, gvk.Version),
				}
			}
			sch, err := convertJSONSchemaProps(v.Schema.OpenAPIV3Schema)
			if err != nil {
				return nil, &ErrSchemaNotAvailable{
					GVK:   gvk,
					Cause: fmt.Errorf("converting %s/%s schema: %w", crd.Name, gvk.Version, err),
				}
			}
			return sch, nil
		}
		// CRD exists but doesn't serve the requested version.
		return nil, &ErrSchemaNotAvailable{
			GVK:   gvk,
			Cause: fmt.Errorf("CRD %s does not serve version %s", crd.Name, gvk.Version),
		}
	}

	return nil, &ErrSchemaNotAvailable{GVK: gvk, Cause: errNotFound}
}

// ensureCRDList pulls the cluster's CRD list at most once per
// ClusterFetcher lifetime and returns the cached slice on subsequent
// calls. Concurrent callers coalesce onto a single API call.
//
// Context-scoped failures (cancellation, deadline) are NOT cached:
// if the first caller's context expires mid-fetch, the in-flight
// marker is evicted so the next caller retries with a fresh
// context. This avoids the sync.Once trap where one cancelled
// startup attempt permanently poisons every later schema lookup.
//
// Non-context errors (RBAC denial, parse failures, network errors
// not tied to ctx) ARE cached — they reflect real cluster state
// that won't change by re-asking. Callers retry by constructing a
// new ClusterFetcher.
func (f *ClusterFetcher) ensureCRDList(ctx context.Context) ([]apiextensionsv1.CustomResourceDefinition, error) {
	f.crdState.mu.Lock()

	if f.crdState.fetched {
		list, err := f.crdState.list, f.crdState.err
		f.crdState.mu.Unlock()
		return list, err
	}

	if f.crdState.done != nil {
		// Another goroutine is fetching. Wait for it.
		done := f.crdState.done
		f.crdState.mu.Unlock()
		select {
		case <-done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		f.crdState.mu.Lock()
		list, err := f.crdState.list, f.crdState.err
		f.crdState.mu.Unlock()
		return list, err
	}

	// We're the first caller — initiate the fetch.
	done := make(chan struct{})
	f.crdState.done = done
	f.crdState.mu.Unlock()

	list, err := f.crds.ListCRDs(ctx)

	f.crdState.mu.Lock()
	defer f.crdState.mu.Unlock()
	defer close(done)

	if err != nil && ctx.Err() != nil {
		// Context-scoped failure. Evict the in-flight marker so
		// the next caller (with a fresh context) gets to retry.
		f.crdState.done = nil
		return nil, err
	}
	// Terminal outcome (success OR persistent error). Cache it.
	f.crdState.list = list
	f.crdState.err = err
	f.crdState.fetched = true
	f.crdState.done = nil
	return list, err
}

// fetchFromOpenAPI resolves a GVK against the cluster's aggregated
// OpenAPI v3 endpoint. The schema key follows convention
// (io.k8s.api.<group>.<version>.<Kind> for built-ins) but we don't
// rely on it — every schema carries an x-kubernetes-group-version-kind
// extension that we match against directly, so renamed schemas or
// non-canonical group paths still resolve.
//
// Returns the matched schema PLUS the full components map for the
// GV. K8s aggregated OpenAPI v3 uses `allOf: [$ref: ObjectMeta]`
// to attach defaults to refs (canonical pattern for `metadata` on
// every resource); the consuming converter needs the components
// map to resolve those refs into typed sub-structs. Without it,
// `gw.Metadata` collapses to `interface{}` and chart code reaching
// in compiles to nothing.
//
// The components map is shared by reference across all calls for
// the same GV: same spec3.OpenAPI value is cached per-GV in
// gvSpecs. Callers must NOT mutate it.
func (f *ClusterFetcher) fetchFromOpenAPI(ctx context.Context, gvk schema.GroupVersionKind) (*spec.Schema, map[string]spec.Schema, error) {
	gvSpec, err := f.gvSpec(ctx, gvk.GroupVersion())
	if err != nil {
		return nil, nil, &ErrSchemaNotAvailable{GVK: gvk, Cause: err}
	}
	if gvSpec == nil {
		// The provider returned no spec and no error — treat the
		// same as a NotFound, so callers fall back to the generic
		// envelope. Providers that want to flag this as transient
		// should return an explicit error from GVSpec instead.
		return nil, nil, &ErrSchemaNotAvailable{GVK: gvk, Cause: errNotFound}
	}
	if gvSpec.Components == nil {
		return nil, nil, &ErrSchemaNotAvailable{
			GVK:   gvk,
			Cause: fmt.Errorf("OpenAPI v3 spec for %s has no components", gvk.GroupVersion()),
		}
	}
	// Build a map[string]spec.Schema from the spec3 components for
	// the converter (whose Components signature takes value types
	// rather than pointers — matches the OpenAPI v3 wire form).
	components := make(map[string]spec.Schema, len(gvSpec.Components.Schemas))
	for name, ptr := range gvSpec.Components.Schemas {
		if ptr != nil {
			components[name] = *ptr
		}
	}
	for _, sch := range gvSpec.Components.Schemas {
		if !schemaMatchesGVK(sch, gvk) {
			continue
		}
		return sch, components, nil
	}
	return nil, nil, &ErrSchemaNotAvailable{GVK: gvk, Cause: errNotFound}
}

// gvSpec returns the cached OpenAPI v3 spec for a GroupVersion,
// fetching it via the provider on first request. Concurrent callers
// for the same GV coalesce on the entry's done channel so we don't
// issue duplicate HTTP requests for the same GV.
//
// Context-scoped failures (cancellation, deadline) DO NOT poison
// the cache: if the leader's context expires mid-fetch, the entry
// is evicted so the next caller retries with a fresh context.
// Persistent provider errors (404, parse failures, etc.) ARE
// cached — they reflect real cluster state.
//
// Marker for "leader hit a context error and the entry was
// evicted" is the `evicted` bool inside gvSpecCache: waiters
// observing it after their wake re-enter the function to retry
// with their own context. Using a dedicated field rather than
// `spec == nil && err == nil` avoids a tight retry loop in the
// legitimate "the GV genuinely has no spec" case (which is a
// valid persistent outcome).
func (f *ClusterFetcher) gvSpec(ctx context.Context, gv schema.GroupVersion) (*spec3.OpenAPI, error) {
	for {
		f.gvSpecsMu.Lock()
		entry, ok := f.gvSpecs[gv]
		if !ok {
			entry = &gvSpecCache{done: make(chan struct{})}
			f.gvSpecs[gv] = entry
			f.gvSpecsMu.Unlock()

			sp, err := f.openapi.GVSpec(ctx, gv)
			if err != nil && ctx.Err() != nil {
				// Context-scoped failure. Evict so the next
				// caller retries with a fresh context.
				f.gvSpecsMu.Lock()
				delete(f.gvSpecs, gv)
				f.gvSpecsMu.Unlock()
				entry.evicted = true
				close(entry.done) // wake coalescing callers; they'll re-enter
				return nil, err
			}
			entry.spec, entry.err = sp, err
			close(entry.done)
			return entry.spec, entry.err
		}
		f.gvSpecsMu.Unlock()

		select {
		case <-entry.done:
			if entry.evicted {
				continue // retry through the leader path with our own ctx
			}
			return entry.spec, entry.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// schemaMatchesGVK reports whether the OpenAPI schema's
// x-kubernetes-group-version-kind extension lists the requested GVK.
// Real K8s schemas usually list exactly one GVK, but the wire format
// is an array so we tolerate multiple entries.
func schemaMatchesGVK(sch *spec.Schema, gvk schema.GroupVersionKind) bool {
	if sch == nil {
		return false
	}
	ext, ok := sch.Extensions["x-kubernetes-group-version-kind"]
	if !ok {
		return false
	}
	entries, ok := ext.([]any)
	if !ok {
		return false
	}
	for _, e := range entries {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if m["group"] == gvk.Group && m["version"] == gvk.Version && m["kind"] == gvk.Kind {
			return true
		}
	}
	return false
}

// convertJSONSchemaProps adapts the CRD-shaped schema type
// (apiextensionsv1.JSONSchemaProps) into the kube-openapi
// spec.Schema typegen consumes. Both types serialise to compatible
// OpenAPI v3 JSON, so a JSON round-trip is the simplest viable
// conversion — it picks up every JSON-encoded field including the
// x-kubernetes-* extensions we care about for preserve-unknown
// detection. The alternative (a hand-rolled field-by-field copy)
// would duplicate the apiextensions-apiserver's internal
// ConvertJSONSchemaProps and require keeping the two in sync as
// JSONSchemaProps grows new fields.
//
// The conversion is the only allocation per CRD-version in the
// fetcher's hot path, but CRD schemas are read once per controller
// boot, so it's not a perf concern.
func convertJSONSchemaProps(in *apiextensionsv1.JSONSchemaProps) (*spec.Schema, error) {
	data, err := json.Marshal(in)
	if err != nil {
		return nil, fmt.Errorf("marshal JSONSchemaProps: %w", err)
	}
	var out spec.Schema
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("unmarshal into spec.Schema: %w", err)
	}
	return &out, nil
}
