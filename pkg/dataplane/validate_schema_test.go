// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package dataplane

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAppendIndexedErrors(t *testing.T) {
	failOn := func(targets ...int) func(int) error {
		set := make(map[int]struct{}, len(targets))
		for _, target := range targets {
			set[target] = struct{}{}
		}
		return func(v int) error {
			if _, bad := set[v]; bad {
				return errors.New("bad value")
			}
			return nil
		}
	}

	tests := []struct {
		name    string
		initial []string
		items   []int
		fail    func(int) error
		want    []string
	}{
		{
			name:    "empty slice yields no new errors",
			initial: nil,
			items:   nil,
			fail:    failOn(),
			want:    nil,
		},
		{
			name:    "all items valid keeps initial errors unchanged",
			initial: []string{"prior"},
			items:   []int{10, 20, 30},
			fail:    failOn(),
			want:    []string{"prior"},
		},
		{
			name:    "single failure produces formatted message at correct index",
			initial: nil,
			items:   []int{1, 2, 3},
			fail:    failOn(2),
			want:    []string{"backend api, http-request rule 1: bad value"},
		},
		{
			name:    "multiple failures preserve original index",
			initial: nil,
			items:   []int{0, 1, 2, 3, 4},
			fail:    failOn(0, 3, 4),
			want: []string{
				"backend api, http-request rule 0: bad value",
				"backend api, http-request rule 3: bad value",
				"backend api, http-request rule 4: bad value",
			},
		},
		{
			name:    "appends to existing slice rather than replacing",
			initial: []string{"prior-error"},
			items:   []int{7},
			fail:    failOn(7),
			want: []string{
				"prior-error",
				"backend api, http-request rule 0: bad value",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := appendIndexedErrors(tt.initial, tt.items, tt.fail, "backend api", "http-request rule")
			assert.Equal(t, tt.want, got)
		})
	}
}

// Named slice types satisfy the ~[]T constraint, which is the actual reason the
// constraint exists (client-native uses named slice types like HTTPRequestRules).
func TestAppendIndexedErrors_AcceptsNamedSliceType(t *testing.T) {
	type rules []string
	items := rules{"first", "bad", "last"}

	got := appendIndexedErrors(nil, items, func(s string) error {
		if s == "bad" {
			return errors.New("rejected")
		}
		return nil
	}, "frontend web", "acl")

	assert.Equal(t, []string{"frontend web, acl 1: rejected"}, got)
}
