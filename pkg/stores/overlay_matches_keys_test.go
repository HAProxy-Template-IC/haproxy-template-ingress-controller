// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package stores

import (
	"errors"
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
//  Without keyExtractor (the default constructor):
//   * empty keys → no match (otherwise overlay additions would
//     "leak" into every unfiltered Get() call from the caller)
//   * resource has no GetNamespace/GetName accessors → no match (a
//     resource we can't key shouldn't pretend to match anything)
//   * 1-key (namespace) query → matches by namespace only (this is
//     the semantics callers depend on for "list all in namespace")
//   * 2-key (namespace + name) query → both must match
//   * mismatched first key short-circuits → false (defensive)
//
//  With keyExtractor (multi-key indexed stores):
//   * keyExtractor error → false (a resource we can't key shouldn't
//     match anything — same defensive principle as the no-accessor
//     branch above)
//   * len(resourceKeys) < len(keys) → false (the query is asking for
//     MORE specificity than the resource carries; a regression that
//     wrongly accepted this would over-match overlay items)
//   * prefix-match: providing FEWER keys than the resource has must
//     still match if the prefix lines up. This is what enables
//     wide-scope queries (e.g. "list all EndpointSlices for service X"
//     when EndpointSlices are indexed by [serviceName, sliceName])
//   * any key mismatch in the prefix → false
//
// A regression in any of these branches would silently corrupt
// overlay-driven dry-run validation (which is on the webhook hot path).
// Pin each branch with a table.

// nameNamespacer is the minimal accessor matchesKeys looks for in the
// no-keyExtractor branch. We use it directly to test the "resource has
// no accessor" branch by passing values that don't satisfy this
// interface.
type nameNamespacer interface {
	GetNamespace() string
	GetName() string
}

// untyped is a value with NO accessors — it should never match in the
// no-keyExtractor branch.
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

	// Use a CompositeStore without a keyExtractor.
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

func TestCompositeStore_MatchesKeys_WithKeyExtractor(t *testing.T) {
	// Build a key extractor that returns three keys for a typed object.
	// We exercise prefix matching by providing fewer keys than the
	// extractor returns.
	threeKeyExtractor := func(resource any) ([]string, error) {
		// Marker payload used to simulate extractor failure for one
		// specific input.
		if u, ok := resource.(untyped); ok && u.Anything == "fail" {
			return nil, errors.New("extractor failed")
		}
		if u, ok := resource.(untyped); ok && u.Anything == "short" {
			// Resource only carries one key — caller asking for two
			// keys must NOT match.
			return []string{"only-one"}, nil
		}
		// Default: 3-key resource.
		return []string{"k1", "k2", "k3"}, nil
	}

	subject := untyped{Anything: "regular"}
	failing := untyped{Anything: "fail"}
	short := untyped{Anything: "short"}

	tests := []struct {
		name string
		res  any
		keys []string
		want bool
		why  string
	}{
		{
			name: "extractor error returns false (defensive)",
			res:  failing,
			keys: []string{"k1"},
			want: false,
			why: "if the extractor errors the resource is unkeyable; same principle as the " +
				"no-accessor branch — pretend it matches nothing",
		},
		{
			name: "len(resourceKeys) < len(keys) returns false (over-specific query)",
			res:  short,
			keys: []string{"only-one", "extra"},
			want: false,
			why: "asking for MORE specificity than the resource carries can't match; a " +
				"regression that wrongly accepted this would over-match overlay items",
		},
		{
			name: "all keys present and matching → true (full match)",
			res:  subject,
			keys: []string{"k1", "k2", "k3"},
			want: true,
		},
		{
			name: "prefix match with fewer keys → true (wide-scope query)",
			res:  subject,
			keys: []string{"k1"},
			want: true,
			why: "this is what enables 'list all EndpointSlices for service X' when " +
				"EndpointSlices are indexed by [serviceName, sliceName]; querying with " +
				"only the service name must match every slice for that service",
		},
		{
			name: "prefix match with TWO keys when resource has THREE → true",
			res:  subject,
			keys: []string{"k1", "k2"},
			want: true,
		},
		{
			name: "first-key mismatch breaks prefix → false",
			res:  subject,
			keys: []string{"WRONG", "k2"},
			want: false,
		},
		{
			name: "mid-prefix mismatch breaks → false",
			res:  subject,
			keys: []string{"k1", "WRONG"},
			want: false,
			why: "the loop must check EVERY provided key, not just the first; a regression " +
				"that broke after the first match would cause overlay items keyed on a " +
				"shared prefix to leak into unrelated queries",
		},
		{
			name: "empty key list → true (every prefix-of-zero matches)",
			res:  subject,
			keys: nil,
			want: true,
			why: "with a keyExtractor the no-keys branch falls through to the prefix-loop " +
				"with a zero-length range, which trivially matches; this is the documented " +
				"semantic difference from the no-extractor branch (which rejects empty keys)",
		},
	}

	store := NewCompositeStoreWithKeyExtractor(newMockStore(), NewStoreOverlay(), threeKeyExtractor)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := store.matchesKeys(tt.res, tt.keys)
			assert.Equal(t, tt.want, got, tt.why)
		})
	}
}
