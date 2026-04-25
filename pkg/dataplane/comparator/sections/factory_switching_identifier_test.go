// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package sections

import (
	"testing"

	"github.com/haproxytech/client-native/v6/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// backendSwitchingRuleIdentifier and serverSwitchingRuleIdentifier
// feed DescribeTypedChild — the helper that decides whether to render
// a parenthesized identifier "(api)" or fall back to "at index N".
//
// The two helpers have a NON-OBVIOUS asymmetry that has no direct test
// coverage: backendSwitchingRuleIdentifier defensively returns "" for
// nil inputs, but serverSwitchingRuleIdentifier dereferences without a
// nil check and would panic. The factory call sites always pass
// non-nil pointers in practice, so the divergence is latent — but it
// is real and worth pinning so that:
//
//   - A refactor that aligned the two (added a nil guard to the server
//     variant, or removed the guard from the backend variant) is
//     forced to update this test, surfacing the question "which side
//     did you mean?" in code review.
//   - Anyone touching the factory_switching code can see at a glance
//     what the helpers do under each input shape.
//
// Empty Name / TargetServer values are NOT special-cased by either
// helper — they pass through verbatim. This is intentional: the
// describer downstream falls back to the "at index N" fallback string
// only when the identifier is empty, and an empty Name is already an
// unambiguous "no name set" signal worth surfacing.
func TestBackendSwitchingRuleIdentifier(t *testing.T) {
	tests := []struct {
		name string
		rule *models.BackendSwitchingRule
		want string
	}{
		{
			name: "nil rule returns empty (defensive guard)",
			rule: nil,
			want: "",
		},
		{
			name: "empty Name passes through as empty",
			rule: &models.BackendSwitchingRule{Name: ""},
			want: "",
		},
		{
			name: "set Name passes through verbatim",
			rule: &models.BackendSwitchingRule{Name: "api-backend"},
			want: "api-backend",
		},
		{
			name: "Name with hyphens and digits passes through",
			rule: &models.BackendSwitchingRule{Name: "api-v2-backend-7"},
			want: "api-v2-backend-7",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, backendSwitchingRuleIdentifier(tt.rule),
				"backendSwitchingRuleIdentifier must be nil-safe and pass non-nil inputs through verbatim")
		})
	}
}

func TestServerSwitchingRuleIdentifier(t *testing.T) {
	tests := []struct {
		name string
		rule *models.ServerSwitchingRule
		want string
	}{
		{
			name: "empty TargetServer passes through as empty",
			rule: &models.ServerSwitchingRule{TargetServer: ""},
			want: "",
		},
		{
			name: "set TargetServer passes through verbatim",
			rule: &models.ServerSwitchingRule{TargetServer: "srv-1"},
			want: "srv-1",
		},
		{
			name: "TargetServer with mixed identifiers",
			rule: &models.ServerSwitchingRule{TargetServer: "backend.api/srv-1"},
			want: "backend.api/srv-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, serverSwitchingRuleIdentifier(tt.rule),
				"serverSwitchingRuleIdentifier must pass non-nil inputs through verbatim")
		})
	}
}

// Cross-helper asymmetry: nil handling diverges between the two
// identifier helpers. This is a latent divergence — the factory call
// sites pass non-nil pointers in practice — but pin it so the
// contracts stay explicit.
//
//   - backendSwitchingRuleIdentifier(nil) returns "" defensively.
//   - serverSwitchingRuleIdentifier(nil) PANICS (no nil guard).
//
// A future change that aligned the two should explicitly choose
// which side wins (probably: add the guard to the server variant)
// and update this test in the same commit. The intent is to make
// sure the choice is conscious, not silent.
func TestSwitchingIdentifiers_NilHandlingAsymmetry(t *testing.T) {
	// backend variant: defensive empty string.
	assert.Equal(t, "", backendSwitchingRuleIdentifier(nil),
		"backendSwitchingRuleIdentifier defensively returns empty for nil")

	// server variant: nil dereference. Pin the panic to surface the
	// asymmetry in code review if anyone touches either helper.
	require.Panics(t, func() {
		_ = serverSwitchingRuleIdentifier(nil)
	}, "serverSwitchingRuleIdentifier currently lacks a nil guard; "+
		"this asymmetry with backendSwitchingRuleIdentifier is latent (call sites pass non-nil) "+
		"but real — if you align the two, update this test in the same commit so the choice is conscious")
}
