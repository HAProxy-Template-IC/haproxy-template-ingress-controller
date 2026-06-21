// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package watcher

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/indexer"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

// matchesFieldSelector is the gate that decides whether a watched
// resource passes the configured field-selector filter and reaches
// the OnChange callback. The branches with direct coverage here:
//
//  1. fieldSelectorMatcher == nil → return true (no filter
//     configured, every resource passes through). A regression that
//     flipped this to false would silently filter out ALL resources
//     when no field selector is set — i.e. the controller would
//     observe an empty set and never reconcile.
//
//  2. matcher.Matches returns (true, nil) → return true. The happy
//     path that makes selectors work.
//
//  3. matcher.Matches returns (false, nil) → return false. The
//     filter rejects the resource cleanly. This branch covers two
//     production cases: "field present with non-matching value" and
//     "field missing from resource".
//
// Build a minimal Watcher (no informer, no client) since the method
// only reads w.fieldSelectorMatcher, w.logger, and w.config.GVR.
func TestWatcher_MatchesFieldSelector(t *testing.T) {
	t.Run("nil matcher accepts every resource", func(t *testing.T) {
		// No field selector configured — gate is open.
		w := &Watcher{
			fieldSelectorMatcher: nil,
			logger:               slog.Default(),
		}
		ok := w.matchesFieldSelector(&unstructured.Unstructured{
			Object: map[string]any{"kind": "Ingress"},
		})
		assert.True(t, ok,
			"nil matcher must let every resource through; "+
				"a regression that returned false would silently filter ALL resources when no selector is configured "+
				"and starve the controller of events")
	})

	t.Run("matching resource is accepted", func(t *testing.T) {
		matcher, err := indexer.NewFieldSelectorMatcher("spec.ingressClassName=haproxy")
		require.NoError(t, err)

		w := &Watcher{
			fieldSelectorMatcher: matcher,
			config:               types.WatcherConfig{},
			logger:               slog.Default(),
		}

		// Resource where spec.ingressClassName == "haproxy" must pass.
		resource := &unstructured.Unstructured{
			Object: map[string]any{
				"apiVersion": "networking.k8s.io/v1",
				"kind":       "Ingress",
				"metadata":   map[string]any{"name": "test"},
				"spec":       map[string]any{"ingressClassName": "haproxy"},
			},
		}
		assert.True(t, w.matchesFieldSelector(resource))
	})

	t.Run("non-matching value is rejected", func(t *testing.T) {
		matcher, err := indexer.NewFieldSelectorMatcher("spec.ingressClassName=haproxy")
		require.NoError(t, err)

		w := &Watcher{
			fieldSelectorMatcher: matcher,
			config:               types.WatcherConfig{},
			logger:               slog.Default(),
		}

		// Resource has the field but with a different value — reject.
		resource := &unstructured.Unstructured{
			Object: map[string]any{
				"apiVersion": "networking.k8s.io/v1",
				"kind":       "Ingress",
				"metadata":   map[string]any{"name": "test"},
				"spec":       map[string]any{"ingressClassName": "nginx"},
			},
		}
		assert.False(t, w.matchesFieldSelector(resource))
	})

	t.Run("missing field is rejected (treated as non-match, not error)", func(t *testing.T) {
		// Matcher.Matches returns (false, nil) when the field doesn't
		// exist on the resource — the gate must reject. A regression
		// that treated "missing field" as "no opinion" and let the
		// resource through would defeat the filter for any resource
		// that doesn't define the selector field.
		matcher, err := indexer.NewFieldSelectorMatcher("spec.ingressClassName=haproxy")
		require.NoError(t, err)

		w := &Watcher{
			fieldSelectorMatcher: matcher,
			config:               types.WatcherConfig{},
			logger:               slog.Default(),
		}

		resource := &unstructured.Unstructured{
			Object: map[string]any{
				"apiVersion": "networking.k8s.io/v1",
				"kind":       "Ingress",
				"metadata":   map[string]any{"name": "test"},
				// Note: no spec.ingressClassName field
			},
		}
		assert.False(t, w.matchesFieldSelector(resource),
			"missing selector field must reject the resource; "+
				"a refactor that defaulted to 'true' here would defeat the filter for any resource that doesn't define the field")
	})
}
