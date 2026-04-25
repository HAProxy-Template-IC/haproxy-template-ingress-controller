// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package comparator

import (
	"testing"

	"github.com/haproxytech/client-native/v6/models"
	"github.com/stretchr/testify/assert"
)

// resolversEqualWithoutNameservers gates the "did the resolver section attrs
// change?" decision in compareResolvers. The contract is that the comparison
// IGNORES the Nameservers map (which compareNameserversWithIndex handles
// separately), so a resolver whose only difference is in its nameserver
// entries must still report equal at the section level. Pin both branches.
func TestResolversEqualWithoutNameservers(t *testing.T) {
	addr1 := "1.1.1.1"
	addr2 := "8.8.8.8"
	port := int64(53)

	hold := func(v int64) *int64 { return &v }

	tests := []struct {
		name string
		r1   *models.Resolver
		r2   *models.Resolver
		want bool
	}{
		{
			name: "identical resolvers (no nameservers) are equal",
			r1:   &models.Resolver{ResolverBase: models.ResolverBase{Name: "default"}},
			r2:   &models.Resolver{ResolverBase: models.ResolverBase{Name: "default"}},
			want: true,
		},
		{
			name: "differing section attrs are NOT equal (HoldOther differs)",
			r1: &models.Resolver{
				ResolverBase: models.ResolverBase{Name: "default", HoldOther: hold(30)},
			},
			r2: &models.Resolver{
				ResolverBase: models.ResolverBase{Name: "default", HoldOther: hold(60)},
			},
			want: false,
		},
		{
			name: "different names are NOT equal",
			r1:   &models.Resolver{ResolverBase: models.ResolverBase{Name: "left"}},
			r2:   &models.Resolver{ResolverBase: models.ResolverBase{Name: "right"}},
			want: false,
		},
		{
			name: "nameserver-only differences are IGNORED — must report equal",
			r1: &models.Resolver{
				ResolverBase: models.ResolverBase{Name: "default"},
				Nameservers: map[string]models.Nameserver{
					"primary": {Name: "primary", Address: &addr1, Port: &port},
				},
			},
			r2: &models.Resolver{
				ResolverBase: models.ResolverBase{Name: "default"},
				// Different nameserver entries (different name and address)
				Nameservers: map[string]models.Nameserver{
					"secondary": {Name: "secondary", Address: &addr2, Port: &port},
				},
			},
			want: true,
		},
		{
			name: "section attr difference still detected even with nameservers present",
			r1: &models.Resolver{
				ResolverBase: models.ResolverBase{Name: "default", AcceptedPayloadSize: 4096},
				Nameservers: map[string]models.Nameserver{
					"primary": {Name: "primary", Address: &addr1, Port: &port},
				},
			},
			r2: &models.Resolver{
				ResolverBase: models.ResolverBase{Name: "default", AcceptedPayloadSize: 8192},
				Nameservers: map[string]models.Nameserver{
					"primary": {Name: "primary", Address: &addr1, Port: &port},
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, resolversEqualWithoutNameservers(tt.r1, tt.r2))
			// Symmetry: equality must be order-independent.
			assert.Equal(t, tt.want, resolversEqualWithoutNameservers(tt.r2, tt.r1),
				"resolversEqualWithoutNameservers must be symmetric")
		})
	}
}

// The function copies its inputs before clearing nameservers — pin that the
// caller's resolvers retain their nameserver maps after the call.
func TestResolversEqualWithoutNameservers_DoesNotMutateInputs(t *testing.T) {
	addr := "1.1.1.1"
	r1 := &models.Resolver{
		ResolverBase: models.ResolverBase{Name: "default"},
		Nameservers: map[string]models.Nameserver{
			"primary": {Name: "primary", Address: &addr},
		},
	}
	r2 := &models.Resolver{
		ResolverBase: models.ResolverBase{Name: "default"},
		Nameservers: map[string]models.Nameserver{
			"secondary": {Name: "secondary", Address: &addr},
		},
	}

	_ = resolversEqualWithoutNameservers(r1, r2)

	assert.Len(t, r1.Nameservers, 1, "r1 nameservers must not be cleared by the comparison")
	assert.Len(t, r2.Nameservers, 1, "r2 nameservers must not be cleared by the comparison")
	assert.Contains(t, r1.Nameservers, "primary")
	assert.Contains(t, r2.Nameservers, "secondary")
}
