// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package resourcestore

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// OverlayStore.List and OverlayStore.Get both fall through to the
// underlying base store. The existing tests use a mockStore that
// never errors, so the error-propagation paths were uncovered.
// The two contracts pinned here are critical because:
//
//   - DryRunValidator wraps OverlayStore around the real K8s
//     resource stores. If the K8s client returns an error
//     (transient API blip, RBAC denial, marshal failure), the
//     overlay MUST surface that error so the validator can deny
//     admission with a meaningful message rather than silently
//     proceeding with an empty resource view.
//
//   - A regression that swallowed the error and returned the
//     overlay's idea of state would let the dry-run pass against
//     a stale or empty view of the cluster — admitting resources
//     that would in reality conflict with state the overlay
//     couldn't see.
//
// Two contracts pinned:
//
//  1. List propagates baseStore.List() errors as-is.
//  2. Get falls through to baseStore.Get() (when keys don't match
//     the overlay) and propagates that error as-is.

// failingStore wraps mockStore and overrides List/Get to return a
// fixed sentinel error so tests can verify the error-propagation
// path. Other Store methods aren't reached on the read paths the
// overlay tests exercise.
type failingStore struct {
	mockStore // embed so we don't need to re-implement non-tested methods
	listErr   error
	getErr    error
}

func (f *failingStore) List() ([]any, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.mockStore.List()
}

func (f *failingStore) Get(keys ...string) ([]any, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.mockStore.Get(keys...)
}

func TestOverlayStore_List_PropagatesBaseStoreError(t *testing.T) {
	sentinel := errors.New("base store list failed: transient API error")
	base := &failingStore{listErr: sentinel}

	overlay := NewOverlayStore(base, "default", "any-name",
		newMockResource("default", "any-name"), OperationCreate)

	got, err := overlay.List()

	require.Error(t, err,
		"OverlayStore.List MUST surface base-store errors — without this, "+
			"the dryrunvalidator would silently proceed with an empty/stale "+
			"resource view and admit resources that would conflict with state "+
			"the overlay couldn't see")
	assert.True(t, errors.Is(err, sentinel),
		"the error MUST be wrap-equal to the underlying base-store error so "+
			"callers can use errors.Is/As — got %v", err)
	assert.Nil(t, got,
		"on error, the result slice MUST be nil — a partial result would "+
			"silently mask the failure and let downstream code think it had "+
			"the full view")
}

func TestOverlayStore_Get_PropagatesBaseStoreErrorOnFallthrough(t *testing.T) {
	// Get falls through to baseStore.Get when keys DON'T match the
	// overlay's namespace/name. A regression that swallowed the
	// fallthrough error would return empty results for legitimate
	// API failures, masking transient blips as "resource not found".
	sentinel := errors.New("base store get failed: API forbidden")
	base := &failingStore{getErr: sentinel}

	overlay := NewOverlayStore(base, "default", "overlay-resource",
		newMockResource("default", "overlay-resource"), OperationCreate)

	// Keys deliberately do NOT match the overlay's namespace/name
	// so the function MUST fall through to baseStore.Get.
	got, err := overlay.Get("other-namespace", "different-resource")

	require.Error(t, err,
		"OverlayStore.Get MUST surface base-store errors on the fallthrough "+
			"path — without this, transient API failures would silently "+
			"present as 'resource not found' and the validator would either "+
			"admit a resource that conflicts with cluster state or render "+
			"with a wrong view of related resources")
	assert.True(t, errors.Is(err, sentinel),
		"the error MUST be wrap-equal to the underlying base-store error")
	assert.Nil(t, got,
		"on error, the result slice MUST be nil so callers can distinguish "+
			"'API failure' (err != nil) from 'no matches' (err == nil, len == 0)")
}

func TestOverlayStore_Get_OverlayHitDoesNotConsultBaseStore(t *testing.T) {
	// Sanity: when keys DO match the overlay, the function MUST
	// short-circuit before calling baseStore.Get. This is what
	// makes the overlay performant (O(1) for the common case
	// instead of O(k) into the base store).
	sentinel := errors.New("base store should NOT be consulted")
	base := &failingStore{getErr: sentinel}

	resource := newMockResource("default", "overlay-target")
	overlay := NewOverlayStore(base, "default", "overlay-target", resource, OperationCreate)

	got, err := overlay.Get("default", "overlay-target")

	require.NoError(t, err,
		"overlay-hit MUST NOT consult the base store — the overlay's whole "+
			"point is to short-circuit base-store I/O for the changed "+
			"resource. A regression that always called baseStore.Get would "+
			"have surfaced this sentinel error here")
	require.Len(t, got, 1)
	assert.Equal(t, resource, got[0])
}
