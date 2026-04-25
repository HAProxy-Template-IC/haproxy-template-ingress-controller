// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package dataplane

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestDiffDetails_AppendSimpleCountChanges directly exercises the underlying
// counter-formatting helper that appendMapCountChanges and appendIntMapCountChanges
// both delegate to. The shared helper has its own zero-suppression contract
// (only emit a line when count > 0) that wasn't pinned anywhere in isolation.
func TestDiffDetails_AppendSimpleCountChanges(t *testing.T) {
	tests := []struct {
		name         string
		initialParts []string
		added        int
		modified     int
		deleted      int
		resource     string
		want         []string
	}{
		{
			name:         "all zero produces no entries",
			initialParts: []string{},
			added:        0,
			modified:     0,
			deleted:      0,
			resource:     "Backends",
			want:         []string{},
		},
		{
			name:         "only added produces one entry",
			initialParts: []string{},
			added:        3,
			modified:     0,
			deleted:      0,
			resource:     "Backends",
			want:         []string{"- Backends added: 3"},
		},
		{
			name:         "only modified produces one entry",
			initialParts: []string{},
			added:        0,
			modified:     5,
			deleted:      0,
			resource:     "Frontends",
			want:         []string{"- Frontends modified: 5"},
		},
		{
			name:         "only deleted produces one entry",
			initialParts: []string{},
			added:        0,
			modified:     0,
			deleted:      2,
			resource:     "Servers",
			want:         []string{"- Servers deleted: 2"},
		},
		{
			name:         "added + deleted (no modified) skips the modified line",
			initialParts: []string{},
			added:        2,
			modified:     0,
			deleted:      4,
			resource:     "ACLs",
			want: []string{
				"- ACLs added: 2",
				"- ACLs deleted: 4",
			},
		},
		{
			name:         "all three are emitted in added/modified/deleted order",
			initialParts: []string{},
			added:        1,
			modified:     2,
			deleted:      3,
			resource:     "Rules",
			want: []string{
				"- Rules added: 1",
				"- Rules modified: 2",
				"- Rules deleted: 3",
			},
		},
		{
			name:         "appends to existing slice rather than replacing",
			initialParts: []string{"prior line"},
			added:        1,
			modified:     0,
			deleted:      0,
			resource:     "Rules",
			want: []string{
				"prior line",
				"- Rules added: 1",
			},
		},
		{
			name:         "negative counts are treated as zero (only > 0 is emitted)",
			initialParts: []string{},
			added:        -1,
			modified:     0,
			deleted:      -5,
			resource:     "Backends",
			want:         []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &DiffDetails{}
			got := d.appendSimpleCountChanges(tt.initialParts, tt.added, tt.modified, tt.deleted, tt.resource)
			if len(tt.want) == 0 {
				assert.Empty(t, got)
				return
			}
			assert.Equal(t, tt.want, got)
		})
	}
}
