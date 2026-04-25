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
	"github.com/stretchr/testify/require"

	haproxyv1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// runtimeConfigOwnerRefs and runtimeConfigLabels are pure helpers shared by all
// four createOrUpdate* paths in pkg/k8s/configpublisher. They control how the
// auxiliary CRDs and Secrets are tied to their parent HAProxyCfg, so the
// returned shapes are part of the controller's contract with garbage
// collection / cascading deletes — pin them.

func TestRuntimeConfigOwnerRefs(t *testing.T) {
	owner := &haproxyv1alpha1.HAProxyCfg{
		ObjectMeta: metav1.ObjectMeta{
			Name: "my-runtime-config",
			UID:  types.UID("a-uid-1234"),
		},
	}

	got := runtimeConfigOwnerRefs(owner)

	require.Len(t, got, 1)
	ref := got[0]

	assert.Equal(t, "haproxy-haptic.org/v1alpha1", ref.APIVersion)
	assert.Equal(t, "HAProxyCfg", ref.Kind)
	assert.Equal(t, "my-runtime-config", ref.Name)
	assert.Equal(t, types.UID("a-uid-1234"), ref.UID)

	// Both flags must be set AND true. nil pointer means "default false" to
	// the API server, so the deref-true check below is what guarantees
	// cascading-deletion + single-controller semantics.
	require.NotNil(t, ref.Controller, "Controller flag must be set")
	assert.True(t, *ref.Controller, "Controller=true so only one controller owns the object")

	require.NotNil(t, ref.BlockOwnerDeletion, "BlockOwnerDeletion flag must be set")
	assert.True(t, *ref.BlockOwnerDeletion, "BlockOwnerDeletion=true so deleting the owner blocks until children are reaped")
}

func TestRuntimeConfigOwnerRefs_PreservesOwnerMetadata(t *testing.T) {
	tests := []struct {
		name string
		in   *haproxyv1alpha1.HAProxyCfg
	}{
		{
			name: "empty name and uid pass through verbatim",
			in:   &haproxyv1alpha1.HAProxyCfg{},
		},
		{
			name: "name with kubernetes-y characters survives",
			in: &haproxyv1alpha1.HAProxyCfg{
				ObjectMeta: metav1.ObjectMeta{
					Name: "my.config-v2",
					UID:  types.UID("11111111-2222-3333-4444-555555555555"),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runtimeConfigOwnerRefs(tt.in)
			require.Len(t, got, 1)
			assert.Equal(t, tt.in.Name, got[0].Name)
			assert.Equal(t, tt.in.UID, got[0].UID)
		})
	}
}

func TestRuntimeConfigLabels(t *testing.T) {
	tests := []struct {
		name      string
		ownerName string
		want      map[string]string
	}{
		{
			name:      "single label keyed by HAProxyCfg name",
			ownerName: "my-runtime-config",
			want: map[string]string{
				"haproxy-haptic.org/runtime-config": "my-runtime-config",
			},
		},
		{
			name:      "empty owner name is preserved verbatim (caller's responsibility)",
			ownerName: "",
			want: map[string]string{
				"haproxy-haptic.org/runtime-config": "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runtimeConfigLabels(&haproxyv1alpha1.HAProxyCfg{
				ObjectMeta: metav1.ObjectMeta{Name: tt.ownerName},
			})
			assert.Equal(t, tt.want, got)
		})
	}
}

// Each call must return an independent map so callers can mutate without
// affecting future invocations.
func TestRuntimeConfigLabels_IndependentMaps(t *testing.T) {
	owner := &haproxyv1alpha1.HAProxyCfg{
		ObjectMeta: metav1.ObjectMeta{Name: "shared"},
	}

	a := runtimeConfigLabels(owner)
	b := runtimeConfigLabels(owner)

	a["mutated"] = "by-caller"

	_, ok := b["mutated"]
	assert.False(t, ok, "second call must not see mutations to the first call's map")
}
