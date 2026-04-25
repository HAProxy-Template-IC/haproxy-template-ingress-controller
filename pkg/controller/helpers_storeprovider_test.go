// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package controller

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/resourcestore"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

// resourceStoreManagerAdapter bridges resourcestore.Manager (which uses
// k8s/types.Store) to stores.StoreProvider (which uses stores.Store).
// The two interfaces have IDENTICAL methods, so the adapter is correct
// only if it correctly handles three non-obvious paths:
//
//  1. GetStore for a missing resourceType returns nil (not a wrapper
//     around a nil store, which would later panic on List() / Get()).
//  2. GetStore for an existing-but-nil entry also returns nil (the
//     defensive `exists || store == nil` guard inside GetStore).
//  3. GetStore for a real store returns a TypesStoreAdapter that
//     delegates calls to the underlying types.Store. This is the
//     load-bearing path: every reconciliation goes through it.
//
// StoreNames is a snapshot taken at construction time. A caller that
// adds more stores AFTER newStoreProviderFromManager must NOT see
// those new stores in StoreNames() — otherwise iteration order would
// silently change between iterations.

// fakeTypesStore is a minimal types.Store stub that records the last
// call so we can verify the adapter actually delegates instead of
// dropping the call. We only stub the methods the test exercises.
type fakeTypesStore struct {
	listResult []any
	listErr    error
	listCalls  int
}

func (f *fakeTypesStore) Get(_ ...string) ([]any, error) { return nil, nil }
func (f *fakeTypesStore) List() ([]any, error)           { f.listCalls++; return f.listResult, f.listErr }
func (f *fakeTypesStore) Add(_ any, _ []string) error    { return nil }
func (f *fakeTypesStore) Update(_ any, _ []string) error { return nil }
func (f *fakeTypesStore) Delete(_ ...string) error       { return nil }
func (f *fakeTypesStore) Clear() error                   { return nil }
func (f *fakeTypesStore) ModCount() (uint64, bool)       { return 0, false }

// Compile-time check.
var _ types.Store = (*fakeTypesStore)(nil)

func TestResourceStoreManagerAdapter_GetStore(t *testing.T) {
	t.Run("missing resource type returns nil (not a nil-wrapper)", func(t *testing.T) {
		mgr := resourcestore.NewManager()
		provider := newStoreProviderFromManager(mgr)

		got := provider.GetStore("does-not-exist")

		assert.Nil(t, got,
			"GetStore must return a nil interface for missing names; "+
				"a non-nil wrapper around a nil store would panic on later .List()")
	})

	t.Run("explicitly registered nil store also returns nil", func(t *testing.T) {
		mgr := resourcestore.NewManager()
		// Caller registered a nil store (defensive registration). The
		// adapter MUST NOT wrap it — wrapping would defer the nil-deref
		// until the first method call.
		mgr.RegisterStore("nil-entry", nil)
		provider := newStoreProviderFromManager(mgr)

		assert.Nil(t, provider.GetStore("nil-entry"),
			"GetStore must guard against nil entries to prevent deferred panics")
	})

	t.Run("real store is returned as a delegating wrapper", func(t *testing.T) {
		fake := &fakeTypesStore{listResult: []any{"resource-A", "resource-B"}}
		mgr := resourcestore.NewManager()
		mgr.RegisterStore("ingresses", fake)
		provider := newStoreProviderFromManager(mgr)

		got := provider.GetStore("ingresses")

		require.NotNil(t, got, "GetStore must return a non-nil store for registered names")

		// Calls through the adapter must reach the underlying store.
		// If the adapter dropped the call (e.g. returned an empty
		// stub), templates would silently see no resources.
		items, err := got.List()
		require.NoError(t, err)
		assert.Equal(t, []any{"resource-A", "resource-B"}, items)
		assert.Equal(t, 1, fake.listCalls, "List() must reach the wrapped types.Store exactly once")
	})

	t.Run("error from underlying store propagates through the adapter", func(t *testing.T) {
		// The adapter is a thin pass-through; errors must NOT be
		// swallowed. Otherwise silent failures would replace real
		// resource lookups with empty results.
		fake := &fakeTypesStore{listErr: errors.New("upstream failure")}
		mgr := resourcestore.NewManager()
		mgr.RegisterStore("services", fake)
		provider := newStoreProviderFromManager(mgr)

		got := provider.GetStore("services")
		require.NotNil(t, got)

		_, err := got.List()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "upstream failure",
			"adapter must propagate upstream errors verbatim, not swallow them")
	})
}

// StoreNames is captured at construction time. This is a stability
// contract — it lets downstream code iterate the providers without
// worrying about interleaved registrations changing the iteration
// set mid-loop. Pin both directions: names registered BEFORE
// construction appear, names registered AFTER do not.
func TestResourceStoreManagerAdapter_StoreNames_SnapshotAtConstruction(t *testing.T) {
	mgr := resourcestore.NewManager()
	mgr.RegisterStore("ingresses", &fakeTypesStore{})
	mgr.RegisterStore("services", &fakeTypesStore{})

	provider := newStoreProviderFromManager(mgr)

	got := provider.StoreNames()
	assert.ElementsMatch(t, []string{"ingresses", "services"}, got,
		"StoreNames must reflect the names registered before construction")

	// Register more stores AFTER the provider was built.
	mgr.RegisterStore("endpoints", &fakeTypesStore{})
	mgr.RegisterStore("configmaps", &fakeTypesStore{})

	gotAfter := provider.StoreNames()
	assert.ElementsMatch(t, []string{"ingresses", "services"}, gotAfter,
		"StoreNames must remain a snapshot — post-construction registrations must NOT appear, "+
			"otherwise iteration order changes silently between reconciliations")
}

// Compile-time guarantee: the adapter still satisfies stores.StoreProvider.
// If a future refactor changes the interface signature without updating the
// adapter, this assignment will fail to compile and the build breaks at
// the caller — exactly where the contract is needed.
var _ stores.StoreProvider = (*resourceStoreManagerAdapter)(nil)
