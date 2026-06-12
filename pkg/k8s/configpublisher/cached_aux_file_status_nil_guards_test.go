// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package configpublisher

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// cachedMapFileStatus, cachedGeneralFileStatus, and cachedCRTListFileStatus
// are three sister functions on Publisher that follow IDENTICAL structure:
//
//  1. p.listers == nil           → return nil
//  2. p.listers.<Type> == nil    → return nil
//  3. lister Get returns error   → return nil
//  4. happy path                 → return populated *cachedAuxFileStatus
//
// The two NIL-GUARD branches (1) and (2) are the defensive checks that
// prevent a nil-pointer dereference when the Publisher was constructed
// with `NewWithListers(..., nil, ...)` (no listers) or with a partially-
// populated `Listers` —
// which is what the unit tests in this package use, and what the
// pre-listers controller startup path used historically.
//
// Without these guards, the very first call to a status-update path
// would crash the controller with a nil-deref before the informer
// caches finish syncing. The branches are NOT exercised by the
// existing pod-cleanup tests because those don't reach
// `cached*FileStatus`. Pin them here once with a table-driven test
// that covers all three sister functions through a single helper, so
// a future refactor that consolidates them into a shared helper is
// forced to keep the same nil-guard behavior.
//
// We deliberately do NOT pin the happy-path (case 4) here because
// it requires standing up a fake informer + lister, which is heavy
// for a guard-only unit test. The happy path is exercised end-to-end
// by integration tests under `tests/`.
func TestPublisher_CachedAuxFileStatus_NilGuards(t *testing.T) {
	type fn func(p *Publisher, namespace, name string) *cachedAuxFileStatus

	cases := []struct {
		name string
		call fn
	}{
		{
			name: "cachedMapFileStatus",
			call: func(p *Publisher, ns, n string) *cachedAuxFileStatus {
				return p.cachedMapFileStatus(ns, n)
			},
		},
		{
			name: "cachedGeneralFileStatus",
			call: func(p *Publisher, ns, n string) *cachedAuxFileStatus {
				return p.cachedGeneralFileStatus(ns, n)
			},
		},
		{
			name: "cachedCRTListFileStatus",
			call: func(p *Publisher, ns, n string) *cachedAuxFileStatus {
				return p.cachedCRTListFileStatus(ns, n)
			},
		},
	}

	t.Run("nil listers — every sister function returns nil safely", func(t *testing.T) {
		// Bare Publisher with no listers — what NewWithListers(..., nil, ...) returns.
		p := &Publisher{}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got := tc.call(p, "haptic", "anything")
				assert.Nil(t, got,
					"with nil Publisher.listers the function MUST return "+
						"nil — without this guard the lister call would "+
						"nil-deref and crash the controller before informer "+
						"caches sync")
			})
		}
	})

	t.Run("listers struct present but specific field nil — returns nil safely", func(t *testing.T) {
		// Partially-populated Listers: every field nil. This mirrors
		// the case where the caller wires up only some informers (e.g.
		// during incremental rollout of a new aux-file kind, or in a
		// test harness that only cares about one type).
		p := &Publisher{listers: &Listers{}}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				got := tc.call(p, "haptic", "anything")
				assert.Nil(t, got,
					"with Publisher.listers set but the per-type lister "+
						"field nil, the function MUST return nil. A "+
						"regression that dropped the per-field guard "+
						"would only crash on the first cache lookup for "+
						"that file type — i.e. crash AT the wrong moment "+
						"under load, not at startup where it's catchable")
			})
		}
	})
}
