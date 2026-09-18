package watcher

import (
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"

	"k8s.io/client-go/tools/cache"
)

// handleAdd handles resource addition events.
func (w *Watcher) handleAdd(obj any) {
	resource := watchResource(obj)
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
	oldResource := watchResource(oldObj)
	resource := watchResource(newObj)
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
		w.processUpdate(oldResource, resource)

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
	resource := watchResource(obj)
	if resource == nil {
		// Handle DeletedFinalStateUnknown
		if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
			resource = watchResource(tombstone.Obj)
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
func (w *Watcher) logFieldSelectorSkip(msg string, resource watchedResource) {
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
func (w *Watcher) processAdd(resource watchedResource) {
	keys, err := w.extractResourceKeys(resource)
	if err != nil {
		w.logger.Error("Failed to extract keys from resource for indexing",
			"gvr", w.config.GVR.String(),
			"name", resource.GetName(),
			"namespace", resource.GetNamespace(),
			"error", err)
		return
	}

	beforeRevision, exactRevision := identityRevision(
		w.store,
		resource.GetNamespace(),
		resource.GetName(),
	)
	if err := w.store.Add(storedResourceValue(resource), keys); err != nil {
		w.logger.Error("Failed to add resource to store",
			"gvr", w.config.GVR.String(),
			"name", resource.GetName(),
			"namespace", resource.GetNamespace(),
			"key_count", len(keys),
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
		"key_count", len(keys))

	afterRevision, afterExact := identityRevision(
		w.store,
		resource.GetNamespace(),
		resource.GetName(),
	)
	if exactRevision && afterExact && beforeRevision == afterRevision {
		w.logger.Debug("Watcher add did not change the stored resource",
			"gvr", w.config.GVR.String(),
			"name", resource.GetName(),
			"namespace", resource.GetNamespace(),
			"resource_version", resource.GetResourceVersion())
		return
	}

	// Record change
	w.debouncer.RecordCreate()
}

// processUpdate updates a resource in the store and records the change.
//
// Old/new indexability decides whether the store sees an update, delete, or add.
func (w *Watcher) processUpdate(oldResource, resource watchedResource) {
	_, oldKeysErr := w.extractResourceKeys(oldResource)
	keys, err := w.extractResourceKeys(resource)
	if err != nil {
		w.logger.Error("Failed to extract keys from resource for indexing",
			"gvr", w.config.GVR.String(),
			"name", resource.GetName(),
			"namespace", resource.GetNamespace(),
			"error", err)
		if oldKeysErr == nil {
			w.processDelete(oldResource)
		}
		return
	}
	if oldKeysErr != nil {
		w.processAdd(resource)
		return
	}

	beforeRevision, exactRevision := identityRevision(
		w.store,
		resource.GetNamespace(),
		resource.GetName(),
	)
	if err := w.store.Update(storedResourceValue(resource), keys); err != nil {
		w.logger.Error("Failed to update resource in store",
			"gvr", w.config.GVR.String(),
			"name", resource.GetName(),
			"namespace", resource.GetNamespace(),
			"key_count", len(keys),
			"error", err)
		return
	}

	// Resource-level audit log — see processAdd for rationale.
	w.logger.Debug("Watcher update",
		"gvr", w.config.GVR.String(),
		"name", resource.GetName(),
		"namespace", resource.GetNamespace(),
		"previous_resource_version", oldResource.GetResourceVersion(),
		"resource_version", resource.GetResourceVersion(),
		"key_count", len(keys))

	afterRevision, afterExact := identityRevision(
		w.store,
		resource.GetNamespace(),
		resource.GetName(),
	)
	if exactRevision && afterExact && beforeRevision == afterRevision {
		if w.config.SelfWrites != nil && w.config.SelfWrites.IsSelfWrite(
			w.config.GVR.GroupResource(), resource.GetNamespace(), resource.GetName(), resource.GetResourceVersion()) {
			w.logger.Debug("Watcher self-write did not change the stored resource",
				"gvr", w.config.GVR.String(),
				"name", resource.GetName(),
				"namespace", resource.GetNamespace(),
				"resource_version", resource.GetResourceVersion())
		} else {
			w.logger.Debug("Watcher update did not change the stored resource",
				"gvr", w.config.GVR.String(),
				"name", resource.GetName(),
				"namespace", resource.GetNamespace(),
				"resource_version", resource.GetResourceVersion())
		}
		return
	}

	// Record change
	w.debouncer.RecordUpdate()
}

func identityRevision(resourceStore any, namespace, name string) (stores.Revision, bool) {
	revisioned, ok := resourceStore.(interface {
		IdentityRevision(namespace, name string) stores.Revision
	})
	if !ok {
		return "", false
	}
	revision := revisioned.IdentityRevision(namespace, name)
	return revision, revision != ""
}

// processDelete removes a resource from the store and records the change.
func (w *Watcher) processDelete(resource watchedResource) {
	keys, err := w.extractResourceKeys(resource)
	if err != nil {
		w.logger.Error("Failed to extract keys from resource for deletion",
			"gvr", w.config.GVR.String(),
			"name", resource.GetName(),
			"namespace", resource.GetNamespace(),
			"error", err)
		return
	}

	beforeRevision, exactRevision := identityRevision(
		w.store,
		resource.GetNamespace(),
		resource.GetName(),
	)
	if err := w.store.Delete(resource.GetNamespace(), resource.GetName(), keys); err != nil {
		w.logger.Error("Failed to delete resource from store",
			"gvr", w.config.GVR.String(),
			"name", resource.GetName(),
			"namespace", resource.GetNamespace(),
			"key_count", len(keys),
			"error", err)
		return
	}

	// Resource-level audit log — see processAdd for rationale.
	w.logger.Debug("Watcher delete",
		"gvr", w.config.GVR.String(),
		"name", resource.GetName(),
		"namespace", resource.GetNamespace(),
		"resource_version", resource.GetResourceVersion(),
		"key_count", len(keys))

	afterRevision, afterExact := identityRevision(
		w.store,
		resource.GetNamespace(),
		resource.GetName(),
	)
	if exactRevision && afterExact && beforeRevision == afterRevision {
		w.logger.Debug("Watcher delete did not change the stored resource",
			"gvr", w.config.GVR.String(),
			"name", resource.GetName(),
			"namespace", resource.GetNamespace(),
			"resource_version", resource.GetResourceVersion())
		return
	}

	// Record change
	w.debouncer.RecordDelete()
}

// shouldSkipUpdate checks if an update event should be skipped.
// Returns true for resync events (resourceVersion unchanged).
func (w *Watcher) shouldSkipUpdate(oldResource, newResource watchedResource) bool {
	if watchResource(oldResource) == nil {
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
func (w *Watcher) matchesFieldSelector(resource watchedResource) bool {
	if w.fieldSelectorMatcher == nil {
		return true
	}

	matches, err := w.resourceMatchesSelector(resource)
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
