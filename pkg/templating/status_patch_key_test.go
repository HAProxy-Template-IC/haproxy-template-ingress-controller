// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package templating

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// statusPatchKey is the function that decides whether two Register() calls
// refer to the SAME Kubernetes resource (and so get their Variants merged)
// or DIFFERENT resources (and stay as separate patches).
//
// The existing TestStatusPatchCollector_Register_Validation pins namespace
// and name validation, but does NOT cover:
//
//  1. Empty apiVersion / Kind validation — the doc string lists all four
//     fields as required and the error message ("namespace, name, apiVersion,
//     and kind are required") is part of the public contract. If a regression
//     dropped either check, callers could silently register patches with no
//     way to distinguish "Ingress my-name" from "Gateway my-name", and the
//     collector would merge them into the wrong resource's status.
//
//  2. Key discrimination by apiVersion AND Kind — namespace+name alone is
//     NOT a unique identifier. A Service named "foo" and an Ingress named
//     "foo" in the same namespace are DIFFERENT resources. A regression
//     that simplified statusPatchKey to just namespace+name would silently
//     merge unrelated resources' status patches.
//
//  3. The exact key format — "namespace/name/apiVersion/kind" with "/"
//     separators. Downstream debugging logs may grep for this format; a
//     regression that changed the separator (or order) would silently
//     break log scrapers.
//
// These tests pin all three contracts directly with table-driven cases.

func TestStatusPatchCollector_Register_RequiresThreeOfFourFields(t *testing.T) {
	tests := []struct {
		name       string
		namespace  string
		resName    string
		apiVersion string
		kind       string
		wantErr    bool
	}{
		// Each row toggles ONE field to empty so the test pins per-field
		// validation rather than just "any empty field errors".
		{
			name:       "all four set → ok",
			namespace:  "default",
			resName:    "x",
			apiVersion: "v1",
			kind:       "Pod",
			wantErr:    false,
		},
		{
			name:       "empty namespace → ok (cluster-scoped resource like GatewayClass)",
			namespace:  "",
			resName:    "haptic",
			apiVersion: "gateway.networking.k8s.io/v1",
			kind:       "GatewayClass",
			wantErr:    false,
		},
		{
			name:       "empty name → error",
			namespace:  "default",
			resName:    "",
			apiVersion: "v1",
			kind:       "Pod",
			wantErr:    true,
		},
		{
			name:       "empty apiVersion → error (gap in existing tests)",
			namespace:  "default",
			resName:    "x",
			apiVersion: "",
			kind:       "Pod",
			wantErr:    true,
		},
		{
			name:       "empty kind → error (gap in existing tests)",
			namespace:  "default",
			resName:    "x",
			apiVersion: "v1",
			kind:       "",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewStatusPatchCollector()
			err := c.Register(tt.namespace, tt.resName, tt.apiVersion, tt.kind,
				map[string]map[string]any{"deployed": {"a": 1}})

			if tt.wantErr {
				require.Error(t, err,
					"empty %q must produce an error so callers don't accidentally "+
						"register patches with no way to distinguish target resources",
					tt.name)
				assert.Contains(t, err.Error(), "required",
					"the error message MUST contain 'required' so log scrapers and "+
						"users see the canonical phrasing — the documented error "+
						"says 'namespace, name, apiVersion, and kind are required'")
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestStatusPatchCollector_Register_DiscriminatesByAllFourFieldsInKey(t *testing.T) {
	// Two registrations with the same namespace+name MUST produce SEPARATE
	// patches when apiVersion or Kind differ. This catches a regression
	// that simplified statusPatchKey to just namespace+name and silently
	// merged unrelated resources' status patches.
	tests := []struct {
		name string
		// Two registrations
		ns1, n1, av1, k1 string
		ns2, n2, av2, k2 string
		wantPatchCount   int
	}{
		{
			name: "identical fields → merged into single patch",
			ns1:  "default", n1: "x", av1: "v1", k1: "Pod",
			ns2: "default", n2: "x", av2: "v1", k2: "Pod",
			wantPatchCount: 1,
		},
		{
			name: "different apiVersion → SEPARATE patches",
			// Same NS+Name+Kind but different apiVersion should NOT merge —
			// e.g., Ingress at networking.k8s.io/v1 vs extensions/v1beta1
			// (legacy) are technically different resources.
			ns1: "default", n1: "x", av1: "v1", k1: "Pod",
			ns2: "default", n2: "x", av2: "v2", k2: "Pod",
			wantPatchCount: 2,
		},
		{
			name: "different Kind → SEPARATE patches",
			// A Service named "foo" and an Ingress named "foo" in the same
			// namespace are DIFFERENT resources. Merging their status
			// patches would silently corrupt the wrong resource's status.
			ns1: "default", n1: "foo", av1: "v1", k1: "Service",
			ns2: "default", n2: "foo", av2: "v1", k2: "Ingress",
			wantPatchCount: 2,
		},
		{
			name: "different namespace → SEPARATE patches",
			ns1:  "default", n1: "x", av1: "v1", k1: "Pod",
			ns2: "kube-system", n2: "x", av2: "v1", k2: "Pod",
			wantPatchCount: 2,
		},
		{
			name: "different name → SEPARATE patches",
			ns1:  "default", n1: "a", av1: "v1", k1: "Pod",
			ns2: "default", n2: "b", av2: "v1", k2: "Pod",
			wantPatchCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewStatusPatchCollector()
			require.NoError(t, c.Register(tt.ns1, tt.n1, tt.av1, tt.k1,
				map[string]map[string]any{"deployed": {"first": true}}))
			require.NoError(t, c.Register(tt.ns2, tt.n2, tt.av2, tt.k2,
				map[string]map[string]any{"deployed": {"second": true}}))

			patches := c.Patches()
			assert.Len(t, patches, tt.wantPatchCount,
				"two registrations with this combination MUST yield %d patches — "+
					"a regression in statusPatchKey discrimination would silently "+
					"merge patches across different Kubernetes resources",
				tt.wantPatchCount)
		})
	}
}

func TestStatusPatchKey_FormatPinsSeparatorAndOrder(t *testing.T) {
	// Pin the literal key format. Downstream debugging may grep for keys
	// in this exact shape, and changing the separator (e.g., to ":") or
	// the order would break those tools without an obvious failure.
	tests := []struct {
		name       string
		namespace  string
		resName    string
		apiVersion string
		kind       string
		wantKey    string
	}{
		{
			name:       "core v1 resource",
			namespace:  "default",
			resName:    "my-pod",
			apiVersion: "v1",
			kind:       "Pod",
			wantKey:    "default/my-pod/v1/Pod",
		},
		{
			name:       "GA grouped resource",
			namespace:  "kube-system",
			resName:    "my-ingress",
			apiVersion: "networking.k8s.io/v1",
			kind:       "Ingress",
			wantKey:    "kube-system/my-ingress/networking.k8s.io/v1/Ingress",
		},
		{
			name: "names containing slashes still produce a deterministic key",
			// statusPatchKey doesn't escape slashes — caller-provided slashes
			// in apiVersion (e.g. "networking.k8s.io/v1") naturally appear in
			// the key. Pin this so a regression that started escaping (e.g.,
			// to make keys URL-safe) doesn't silently change merge semantics.
			namespace:  "default",
			resName:    "x",
			apiVersion: "networking.k8s.io/v1",
			kind:       "Ingress",
			wantKey:    "default/x/networking.k8s.io/v1/Ingress",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := statusPatchKey(tt.namespace, tt.resName, tt.apiVersion, tt.kind)
			assert.Equal(t, tt.wantKey, got,
				"statusPatchKey format is part of the resource identity contract; "+
					"changing the separator or order would silently alter merge "+
					"semantics across the collector")
		})
	}
}
