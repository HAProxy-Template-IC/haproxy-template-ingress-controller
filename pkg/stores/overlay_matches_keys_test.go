// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package stores

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/stretchr/testify/assert"
)

// matchesKeys is the unexported predicate the CompositeStore uses to
// decide which overlay additions / modifications to surface for a Get()
// query. The integration-style tests in overlay_test.go exercise the
// happy path through Get() but leave several load-bearing branches
// unpinned. Each branch corresponds to a distinct silent failure mode:
//
//   - empty keys → no match (otherwise overlay additions would
//     "leak" into every unfiltered Get() call from the caller)
//   - resource has no GetNamespace/GetName accessors → no match (a
//     resource we can't key shouldn't pretend to match anything)
//   - 1-key (namespace) query → matches by namespace only (this is
//     the semantics callers depend on for "list all in namespace")
//   - 2-key (namespace + name) query → both must match
//   - mismatched first key short-circuits → false (defensive)
//
// A regression in any of these branches would silently corrupt
// overlay-driven dry-run validation (which is on the webhook hot path).
// Pin each branch with a table.

// nameNamespacer is the minimal accessor matchesKeys looks for. We use
// it directly to test the "resource has no accessor" branch by passing
// values that don't satisfy this interface.
type nameNamespacer interface {
	GetNamespace() string
	GetName() string
}

// untyped is a value with NO accessors — it should never match.
type untyped struct{ Anything string }

func TestCompositeStore_MatchesKeys_NoKeyExtractor(t *testing.T) {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns-a", Name: "cm-1"},
	}

	tests := []struct {
		name     string
		resource any
		keys     []string
		want     bool
		why      string
	}{
		{
			name:     "empty keys must NOT match — prevents overlay leak into unfiltered Get()",
			resource: cm,
			keys:     nil,
			want:     false,
			why: "an empty key list is the 'list everything' query; matching it would " +
				"surface every overlay addition in every Get(), corrupting validation",
		},
		{
			name:     "resource without GetNamespace/GetName accessor returns false",
			resource: untyped{Anything: "x"},
			keys:     []string{"ns-a", "cm-1"},
			want:     false,
			why: "a resource we can't key from shouldn't pretend to match anything; " +
				"the only safe answer is false",
		},
		{
			name:     "single-key query matches by namespace only",
			resource: cm,
			keys:     []string{"ns-a"},
			want:     true,
			why:      "1-key queries are 'list all in namespace' — must succeed when ns matches",
		},
		{
			name:     "single-key query with wrong namespace returns false",
			resource: cm,
			keys:     []string{"other-ns"},
			want:     false,
			why:      "first-key short-circuit prevents accidental cross-namespace matches",
		},
		{
			name:     "two-key query matches namespace AND name",
			resource: cm,
			keys:     []string{"ns-a", "cm-1"},
			want:     true,
			why:      "the canonical (ns, name) lookup",
		},
		{
			name:     "two-key query with wrong name returns false",
			resource: cm,
			keys:     []string{"ns-a", "wrong-name"},
			want:     false,
			why: "second-key check must reject mismatched names — without it overlay " +
				"items would leak into queries for other names in the same namespace",
		},
		{
			name:     "two-key query with wrong namespace short-circuits before name check",
			resource: cm,
			keys:     []string{"wrong-ns", "cm-1"},
			want:     false,
			why:      "namespace mismatch must reject regardless of name match",
		},
	}

	store := NewCompositeStore(newMockStore(), NewStoreOverlay())

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Sanity: the typed test resources must satisfy the
			// accessor interface so the test result reflects the
			// matchesKeys logic, not interface mis-implementation.
			if _, ok := tt.resource.(nameNamespacer); ok && tt.resource != cm {
				t.Fatalf("test fixture %T should not satisfy accessor — would mask the no-accessor branch", tt.resource)
			}

			got := store.matchesKeys(tt.resource, tt.keys)
			assert.Equal(t, tt.want, got, tt.why)
		})
	}
}
