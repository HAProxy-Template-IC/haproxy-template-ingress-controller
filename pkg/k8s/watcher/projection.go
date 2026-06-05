package watcher

import (
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/indexer"
)

// projectionRoots computes the set of top-level object fields a CachedStore
// (on-demand) informer must retain so that the watcher's handler path still
// works on the projected object:
//
//   - index-key extraction (indexer.Process → ExtractKeys) reads the IndexBy
//     JSONPath fields,
//   - field-selector evaluation (matchesFieldSelector) reads the FieldSelector
//     field,
//   - identity / resync handling reads metadata (namespace, name,
//     resourceVersion, labels, annotations).
//
// Everything outside these roots is dropped from the informer's cached copy —
// for Secrets that is `data`/`stringData` (the certificate bytes). This is
// resource-agnostic: it derives only from the JSONPath config, never from a
// resource kind. It is conservative — it retains whole top-level blocks rather
// than subtree-trimming arbitrary JSONPath — so it can never drop a field that
// indexing, field-selection, or identity needs.
//
// The render never reads a projected body: on-demand reads resolve through
// CachedStore.Get → a live API GET of the full, un-projected object, so the
// projected copy is never template-visible. (This is why projection is only
// ever applied to CachedStore-backed kinds — see ADR-0012.)
func projectionRoots(indexBy []string, fieldSelector string) map[string]bool {
	roots := map[string]bool{
		"apiVersion": true,
		"kind":       true,
		"metadata":   true,
	}
	for _, expr := range indexBy {
		if r := indexer.RootField(expr); r != "" {
			roots[r] = true
		}
	}
	if fieldSelector != "" {
		// FieldSelector is "field.path=value" (validated at watcher
		// construction). Parse the field path directly rather than re-building
		// a matcher: a matcher re-parse that errored would silently drop the
		// root, stripping the selected field from the husk so the handler's
		// selector eval rejects everything. strings.Cut without "=" keeps the
		// whole string, which RootField still reduces to a root — over-retain,
		// never under-retain.
		field, _, _ := strings.Cut(fieldSelector, "=")
		if r := indexer.RootField(field); r != "" {
			roots[r] = true
		}
	}
	return roots
}

// newProjectionTransform returns a client-go TransformFunc that strips an
// *unstructured.Unstructured down to the given top-level roots before the
// informer caches it and before handlers run. Non-unstructured inputs (e.g.
// cache.DeletedFinalStateUnknown tombstones) pass through unchanged.
//
// The retained `metadata` block is then passed through the indexer's
// FilterFields so the configured IgnoreFields (e.g. metadata.managedFields and
// the kubectl last-applied-configuration annotation — which duplicates the
// whole object body) are stripped from the husk too. Without this the husk
// could still carry a near-full copy of every object via last-applied,
// eroding the memory win. idx may be nil (no field filtering).
//
// Must be installed via SharedIndexInformer.SetTransform before the informer
// is started.
func newProjectionTransform(roots map[string]bool, idx *indexer.Indexer) cache.TransformFunc {
	return func(obj any) (any, error) {
		u, ok := obj.(*unstructured.Unstructured)
		if !ok {
			return obj, nil
		}
		projected := make(map[string]any, len(roots))
		for k := range roots {
			if v, present := u.Object[k]; present {
				projected[k] = v
			}
		}
		husk := &unstructured.Unstructured{Object: projected}
		if idx != nil {
			// Best-effort: a filter error just means the husk keeps a field
			// it could have dropped — never a correctness problem, since the
			// render reads the full body live regardless.
			_ = idx.FilterFields(husk)
		}
		return husk, nil
	}
}
