package watcher

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
)

// handleAdd handles resource addition events.
func (w *Watcher) handleAdd(obj interface{}) {
	resource := w.convertToUnstructured(obj)
	if resource == nil {
		return
	}

	// Apply field selector filter (client-side)
	if !w.matchesFieldSelector(resource) {
		w.logger.Debug("resource filtered by field selector",
			"gvr", w.config.GVR.String(),
			"name", resource.GetName(),
			"namespace", resource.GetNamespace(),
			"field_selector", w.config.FieldSelector)
		return
	}

	w.processAdd(resource)
}

// handleUpdate handles resource update events.
func (w *Watcher) handleUpdate(oldObj, newObj interface{}) {
	oldResource := w.convertToUnstructured(oldObj)
	resource := w.convertToUnstructured(newObj)
	if resource == nil {
		return
	}

	if w.shouldSkipUpdate(oldResource, resource) {
		return
	}

	// Check field selector transitions
	oldMatches := oldResource != nil && w.matchesFieldSelector(oldResource)
	newMatches := w.matchesFieldSelector(resource)

	switch {
	case oldMatches && newMatches:
		// Both match: normal update
		w.processUpdate(resource)

	case oldMatches && !newMatches:
		// Old matched, new doesn't: treat as delete (resource no longer passes filter)
		w.logger.Debug("resource no longer matches field selector, treating as delete",
			"gvr", w.config.GVR.String(),
			"name", resource.GetName(),
			"namespace", resource.GetNamespace(),
			"field_selector", w.config.FieldSelector)
		w.processDelete(oldResource)

	case !oldMatches && newMatches:
		// Old didn't match, new does: treat as add (resource now passes filter)
		w.logger.Debug("resource now matches field selector, treating as add",
			"gvr", w.config.GVR.String(),
			"name", resource.GetName(),
			"namespace", resource.GetNamespace(),
			"field_selector", w.config.FieldSelector)
		w.processAdd(resource)

	default:
		// Neither match: ignore
		w.logger.Debug("resource update filtered by field selector",
			"gvr", w.config.GVR.String(),
			"name", resource.GetName(),
			"namespace", resource.GetNamespace(),
			"field_selector", w.config.FieldSelector)
	}
}

// handleDelete handles resource deletion events.
func (w *Watcher) handleDelete(obj interface{}) {
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
		w.logger.Debug("deleted resource filtered by field selector",
			"gvr", w.config.GVR.String(),
			"name", resource.GetName(),
			"namespace", resource.GetNamespace(),
			"field_selector", w.config.FieldSelector)
		return
	}

	w.processDelete(resource)
}

// processAdd adds a resource to the store and records the change.
func (w *Watcher) processAdd(resource *unstructured.Unstructured) {
	// Process resource (filter fields, extract keys, convert for templates)
	result, err := w.indexer.Process(resource)
	if err != nil {
		w.logger.Error("failed to process resource for indexing",
			"gvr", w.config.GVR.String(),
			"name", resource.GetName(),
			"namespace", resource.GetNamespace(),
			"error", err)
		return
	}

	// Add converted resource to store
	if err := w.store.Add(result.ConvertedResource, result.Keys); err != nil {
		w.logger.Error("failed to add resource to store",
			"gvr", w.config.GVR.String(),
			"name", resource.GetName(),
			"namespace", resource.GetNamespace(),
			"keys", result.Keys,
			"error", err)
		return
	}

	// Record change
	w.debouncer.RecordCreate()
}

// processUpdate updates a resource in the store and records the change.
func (w *Watcher) processUpdate(resource *unstructured.Unstructured) {
	// Process resource (filter fields, extract keys, convert for templates)
	result, err := w.indexer.Process(resource)
	if err != nil {
		w.logger.Error("failed to process resource for indexing",
			"gvr", w.config.GVR.String(),
			"name", resource.GetName(),
			"namespace", resource.GetNamespace(),
			"error", err)
		return
	}

	// Update converted resource in store
	if err := w.store.Update(result.ConvertedResource, result.Keys); err != nil {
		w.logger.Error("failed to update resource in store",
			"gvr", w.config.GVR.String(),
			"name", resource.GetName(),
			"namespace", resource.GetNamespace(),
			"keys", result.Keys,
			"error", err)
		return
	}

	// Record change
	w.debouncer.RecordUpdate()
}

// processDelete removes a resource from the store and records the change.
func (w *Watcher) processDelete(resource *unstructured.Unstructured) {
	// Extract keys for deletion (before filtering)
	keys, err := w.indexer.ExtractKeys(resource)
	if err != nil {
		w.logger.Error("failed to extract keys from resource for deletion",
			"gvr", w.config.GVR.String(),
			"name", resource.GetName(),
			"namespace", resource.GetNamespace(),
			"error", err)
		return
	}

	// Delete from store
	if err := w.store.Delete(keys...); err != nil {
		w.logger.Error("failed to delete resource from store",
			"gvr", w.config.GVR.String(),
			"name", resource.GetName(),
			"namespace", resource.GetNamespace(),
			"keys", keys,
			"error", err)
		return
	}

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
		w.logger.Debug("skipping update - resource version unchanged (resync)",
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
		w.logger.Warn("field selector evaluation error",
			"gvr", w.config.GVR.String(),
			"name", resource.GetName(),
			"namespace", resource.GetNamespace(),
			"error", err)
		return false
	}

	return matches
}

// convertToUnstructured converts a resource to *unstructured.Unstructured.
func (w *Watcher) convertToUnstructured(obj interface{}) *unstructured.Unstructured {
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
