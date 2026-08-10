package watcher

import (
	"context"
	"encoding/json"
	"log/slog"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
)

// handleAdd handles resource addition events.
func (w *Watcher) handleAdd(obj any) {
	resource := w.convertToUnstructured(obj)
	if resource == nil {
		return
	}

	// Apply field selector filter (client-side)
	if !w.matchesFieldSelector(resource) {
		w.logFieldSelectorSkip("resource filtered by field selector", resource)
		return
	}

	w.processAdd(resource)
}

// handleUpdate handles resource update events.
func (w *Watcher) handleUpdate(oldObj, newObj any) {
	oldResource := w.convertToUnstructured(oldObj)
	resource := w.convertToUnstructured(newObj)
	if resource == nil {
		return
	}

	if w.shouldSkipUpdate(oldResource, resource) {
		return
	}

	// Full pre/post content dump for forensic debugging. Resource-agnostic
	// (works for ANY kind), gated to DEBUG so it never costs anything in
	// production. Without this we can't reconstruct what changed on a
	// given resourceVersion transition — counts and RV pairs aren't enough.
	w.logUpdateContent(context.Background(), oldResource, resource)

	// Check field selector transitions
	oldMatches := oldResource != nil && w.matchesFieldSelector(oldResource)
	newMatches := w.matchesFieldSelector(resource)

	switch {
	case oldMatches && newMatches:
		// Both match: normal update
		w.processUpdate(resource)

	case oldMatches && !newMatches:
		// Old matched, new doesn't: treat as delete (resource no longer passes filter)
		w.logFieldSelectorSkip("resource no longer matches field selector, treating as delete", resource)
		w.processDelete(oldResource)

	case !oldMatches && newMatches:
		// Old didn't match, new does: treat as add (resource now passes filter)
		w.logFieldSelectorSkip("resource now matches field selector, treating as add", resource)
		w.processAdd(resource)

	default:
		// Neither match: ignore
		w.logFieldSelectorSkip("resource update filtered by field selector", resource)
	}
}

// handleDelete handles resource deletion events.
func (w *Watcher) handleDelete(obj any) {
	resource := w.convertToUnstructured(obj)
	if resource == nil {
		// Handle DeletedFinalStateUnknown
		if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
			resource = w.convertToUnstructured(tombstone.Obj)
		}
		if resource == nil {
			return
		}
	}

	// Only process delete if the resource matched our field selector
	// (meaning it was in our store). Resources that never matched
	// were never added, so there's nothing to delete.
	if !w.matchesFieldSelector(resource) {
		w.logFieldSelectorSkip("deleted resource filtered by field selector", resource)
		return
	}

	w.processDelete(resource)
}

// logFieldSelectorSkip emits a debug log indicating that a resource was
// filtered out by the configured field selector. All field-selector skip
// sites use the same gvr/name/namespace/field_selector tuple; only the
// human-readable message differs.
func (w *Watcher) logFieldSelectorSkip(msg string, resource *unstructured.Unstructured) {
	w.logger.Debug(msg,
		"gvr", w.config.GVR.String(),
		"name", resource.GetName(),
		"namespace", resource.GetNamespace(),
		"field_selector", w.config.FieldSelector)
}

// processAdd adds a resource to the store and records the change.
//
// The resource arrives already filtered and float-converted by the informer's
// transform, so this only reads index keys off it.
func (w *Watcher) processAdd(resource *unstructured.Unstructured) {
	keys, err := w.indexer.ExtractKeys(resource)
	if err != nil {
		w.logger.Error("Failed to extract keys from resource for indexing",
			"gvr", w.config.GVR.String(),
			"name", resource.GetName(),
			"namespace", resource.GetNamespace(),
			"error", err)
		return
	}

	if err := w.store.Add(resource.Object, keys); err != nil {
		w.logger.Error("Failed to add resource to store",
			"gvr", w.config.GVR.String(),
			"name", resource.GetName(),
			"namespace", resource.GetNamespace(),
			"keys", keys,
			"error", err)
		return
	}

	// Resource-level audit log. Routinely required during rolling-restart
	// debugging — without per-resource detail "endpoints modified=1" in the
	// aggregated index-update event is ambiguous across parallel tests.
	w.logger.Debug("Watcher add",
		"gvr", w.config.GVR.String(),
		"name", resource.GetName(),
		"namespace", resource.GetNamespace(),
		"resource_version", resource.GetResourceVersion(),
		"keys", keys)

	// Record change
	w.debouncer.RecordCreate()
}

// processUpdate updates a resource in the store and records the change.
//
// As with processAdd, the informer's transform has already normalised the
// resource, so this only reads index keys off it.
func (w *Watcher) processUpdate(resource *unstructured.Unstructured) {
	keys, err := w.indexer.ExtractKeys(resource)
	if err != nil {
		w.logger.Error("Failed to extract keys from resource for indexing",
			"gvr", w.config.GVR.String(),
			"name", resource.GetName(),
			"namespace", resource.GetNamespace(),
			"error", err)
		return
	}

	if err := w.store.Update(resource.Object, keys); err != nil {
		w.logger.Error("Failed to update resource in store",
			"gvr", w.config.GVR.String(),
			"name", resource.GetName(),
			"namespace", resource.GetNamespace(),
			"keys", keys,
			"error", err)
		return
	}

	// Resource-level audit log — see processAdd for rationale.
	w.logger.Debug("Watcher update",
		"gvr", w.config.GVR.String(),
		"name", resource.GetName(),
		"namespace", resource.GetNamespace(),
		"resource_version", resource.GetResourceVersion(),
		"keys", keys)

	// Record change
	w.debouncer.RecordUpdate()
}

// processDelete removes a resource from the store and records the change.
func (w *Watcher) processDelete(resource *unstructured.Unstructured) {
	keys, err := w.indexer.ExtractKeys(resource)
	if err != nil {
		w.logger.Error("Failed to extract keys from resource for deletion",
			"gvr", w.config.GVR.String(),
			"name", resource.GetName(),
			"namespace", resource.GetNamespace(),
			"error", err)
		return
	}

	if err := w.store.Delete(resource.GetNamespace(), resource.GetName(), keys); err != nil {
		w.logger.Error("Failed to delete resource from store",
			"gvr", w.config.GVR.String(),
			"name", resource.GetName(),
			"namespace", resource.GetNamespace(),
			"keys", keys,
			"error", err)
		return
	}

	// Resource-level audit log — see processAdd for rationale.
	w.logger.Debug("Watcher delete",
		"gvr", w.config.GVR.String(),
		"name", resource.GetName(),
		"namespace", resource.GetNamespace(),
		"resource_version", resource.GetResourceVersion(),
		"keys", keys)

	// Record change
	w.debouncer.RecordDelete()
}

// shouldSkipUpdate checks if an update event should be skipped.
// Returns true for resync events (resourceVersion unchanged).
func (w *Watcher) shouldSkipUpdate(oldResource, newResource *unstructured.Unstructured) bool {
	if oldResource == nil {
		return false
	}

	// Skip resync events (resource version unchanged).
	// This happens when the informer re-lists resources and triggers Update events
	// even when nothing has changed.
	oldVersion := oldResource.GetResourceVersion()
	newVersion := newResource.GetResourceVersion()
	if oldVersion != "" && newVersion != "" && oldVersion == newVersion {
		w.logger.Debug("Skipping update - resource version unchanged (resync)",
			"gvr", w.config.GVR.String(),
			"name", newResource.GetName(),
			"namespace", newResource.GetNamespace(),
			"resource_version", newVersion)
		return true
	}

	// Note: We intentionally do NOT skip status-only updates based on generation.
	// The generation-based check doesn't work reliably for all resources:
	// - Pods: immutable spec, generation=1 always, but status changes matter
	// - EndpointSlices: generation=0
	// The debouncer already batches rapid updates, so processing status
	// updates is acceptable and avoids missing critical events like
	// Pod containers becoming ready.

	return false
}

// matchesFieldSelector checks if a resource matches the field selector (if configured).
// Returns true if:
// - No field selector is configured (matches everything)
// - The resource matches the field selector expression.
func (w *Watcher) matchesFieldSelector(resource *unstructured.Unstructured) bool {
	if w.fieldSelectorMatcher == nil {
		return true
	}

	matches, err := w.fieldSelectorMatcher.Matches(resource.Object)
	if err != nil {
		// Log unexpected errors, but treat as non-match
		w.logger.Warn("Field selector evaluation error",
			"gvr", w.config.GVR.String(),
			"name", resource.GetName(),
			"namespace", resource.GetNamespace(),
			"error", err)
		return false
	}

	return matches
}

// convertToUnstructured converts a resource to *unstructured.Unstructured.
func (w *Watcher) convertToUnstructured(obj any) *unstructured.Unstructured {
	switch v := obj.(type) {
	case *unstructured.Unstructured:
		return v
	case runtime.Object:
		// Try to convert
		u, ok := v.(*unstructured.Unstructured)
		if ok {
			return u
		}
	}
	return nil
}

// logUpdateContent dumps the full old + new resource JSON at DEBUG level
// so post-mortem analysis can see exactly what changed between
// resourceVersions. Resource-agnostic by construction (operates on
// *unstructured.Unstructured.Object, the generic map).
//
// Why both old and new: a single field flip (e.g. EndpointSlice's
// conditions.terminating going from nil→true) is invisible in a snapshot
// of the final state — we need to compare before/after.
//
// Why JSON: structured, greppable, post-processable with jq. Pretty-print
// avoided to keep one log line per event (jq can re-indent).
//
// Gated on slog.LevelDebug because the json.Marshal of full unstructured
// resources is non-trivial CPU + heap work — and this fires on every
// informer update for high-frequency kinds like EndpointSlice. slog.Debug
// is a no-op above DEBUG, but the marshal would still run unconditionally
// without this guard.
func (w *Watcher) logUpdateContent(ctx context.Context, oldResource, newResource *unstructured.Unstructured) {
	if !w.logger.Enabled(ctx, slog.LevelDebug) {
		return
	}
	oldJSON, oldErr := json.Marshal(oldResource.Object)
	newJSON, newErr := json.Marshal(newResource.Object)
	if oldErr != nil || newErr != nil {
		w.logger.Debug("Watcher update: JSON marshal failed (forensic dump only)",
			"gvr", w.config.GVR.String(),
			"name", newResource.GetName(),
			"namespace", newResource.GetNamespace(),
			"old_err", oldErr,
			"new_err", newErr)
		return
	}
	w.logger.Debug("Watcher update: pre/post content",
		"gvr", w.config.GVR.String(),
		"name", newResource.GetName(),
		"namespace", newResource.GetNamespace(),
		"old_rv", oldResource.GetResourceVersion(),
		"new_rv", newResource.GetResourceVersion(),
		"old", string(oldJSON),
		"new", string(newJSON))
}
