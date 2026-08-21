// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

//go:build playground

package parserconfig

import (
	"testing"

	"github.com/haproxytech/client-native/v6/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// NewStructuredConfig must pre-initialise every pointer-based index so
// per-section parsers can write to them unconditionally without a
// nil-check. EE-specific maps stay nil because they're opt-in. Pin both
// halves of that contract so a future refactor can't quietly forget an
// index — that would crash the parser at first write.
func TestNewStructuredConfig(t *testing.T) {
	cfg := NewStructuredConfig()
	require.NotNil(t, cfg)

	t.Run("all index maps are pre-allocated", func(t *testing.T) {
		assert.NotNil(t, cfg.ServerIndex)
		assert.NotNil(t, cfg.ServerTemplateIndex)
		assert.NotNil(t, cfg.BindIndex)
		assert.NotNil(t, cfg.PeerEntryIndex)
		assert.NotNil(t, cfg.NameserverIndex)
		assert.NotNil(t, cfg.MailerEntryIndex)
		assert.NotNil(t, cfg.UserIndex)
		assert.NotNil(t, cfg.GroupIndex)
	})

	t.Run("all index maps start empty", func(t *testing.T) {
		assert.Empty(t, cfg.ServerIndex)
		assert.Empty(t, cfg.ServerTemplateIndex)
		assert.Empty(t, cfg.BindIndex)
		assert.Empty(t, cfg.PeerEntryIndex)
		assert.Empty(t, cfg.NameserverIndex)
		assert.Empty(t, cfg.MailerEntryIndex)
		assert.Empty(t, cfg.UserIndex)
		assert.Empty(t, cfg.GroupIndex)
	})
}

// BuildPointerIndex is the generic helper the section extractors inline
// (for users, groups, binds, servers, and similar nested entries). It has
// three load-bearing skip rules:
//
//   - nil items slice → nil result (lets callers leave the section's
//     index unset rather than allocating an empty map)
//   - nil entries inside a non-nil items slice → skipped, no panic
//   - entries with an empty key → skipped (the comparator won't
//     address them anyway, and indexing them under "" would alias all
//     nameless entries onto a single bucket)
//
// Pin every branch so a future refactor can't silently flip any of
// the three rules and corrupt the per-section indexes.
func TestBuildPointerIndex(t *testing.T) {
	type item struct {
		Name string
	}
	getName := func(it *item) string { return it.Name }

	tests := []struct {
		name string
		in   []*item
		want map[string]*item
	}{
		{
			name: "nil slice yields nil index",
			in:   nil,
			want: nil,
		},
		{
			name: "empty slice yields empty (non-nil) index",
			in:   []*item{},
			want: map[string]*item{},
		},
		{
			name: "non-empty entries are indexed by key",
			in: []*item{
				{Name: "a"},
				{Name: "b"},
				{Name: "c"},
			},
			want: map[string]*item{
				"a": {Name: "a"},
				"b": {Name: "b"},
				"c": {Name: "c"},
			},
		},
		{
			name: "nil entries are skipped without panic",
			in:   []*item{{Name: "a"}, nil, {Name: "b"}, nil},
			want: map[string]*item{
				"a": {Name: "a"},
				"b": {Name: "b"},
			},
		},
		{
			name: "empty-key entries are skipped (no aliasing under \"\")",
			in: []*item{
				{Name: "a"},
				{Name: ""},
				{Name: "b"},
				{Name: ""},
			},
			want: map[string]*item{
				"a": {Name: "a"},
				"b": {Name: "b"},
			},
		},
		{
			name: "duplicate keys keep the LAST seen pointer (map overwrite semantics)",
			in: []*item{
				{Name: "dup"},
				{Name: "other"},
				{Name: "dup"},
			},
			// We assert keys + values; pointer identity for "dup" must be
			// the last item — covered by the next subtest.
			want: map[string]*item{
				"dup":   {Name: "dup"},
				"other": {Name: "other"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildPointerIndex(tt.in, getName)
			if tt.want == nil {
				assert.Nil(t, got)
				return
			}
			assert.Equal(t, len(tt.want), len(got))
			for k, want := range tt.want {
				if assert.Contains(t, got, k) {
					assert.Equal(t, want.Name, got[k].Name)
				}
			}
		})
	}

	t.Run("duplicate keys keep the LAST item's pointer (map overwrite)", func(t *testing.T) {
		first := &item{Name: "dup"}
		last := &item{Name: "dup"}
		got := BuildPointerIndex([]*item{first, last}, getName)
		assert.Same(t, last, got["dup"], "later items must overwrite earlier ones in the index")
		assert.NotSame(t, first, got["dup"])
	})
}

// The userlist extractor inlines BuildPointerIndex with the Username/Name
// key extractors. Pin those bindings so a future refactor can't accidentally
// swap them.
func TestBuildPointerIndex_UserBinding(t *testing.T) {
	users := []*models.User{
		{Username: "alice"},
		{Username: "bob"},
		nil,                  // skipped per BuildPointerIndex contract
		{Username: ""},       // skipped: empty key
		{Username: "claire"}, // last
	}

	got := BuildPointerIndex(users, func(u *models.User) string { return u.Username })

	assert.Len(t, got, 3)
	assert.Contains(t, got, "alice")
	assert.Contains(t, got, "bob")
	assert.Contains(t, got, "claire")

	t.Run("nil input yields nil index", func(t *testing.T) {
		assert.Nil(t, BuildPointerIndex[models.User](nil, func(u *models.User) string { return u.Username }))
	})
}

func TestBuildPointerIndex_GroupBinding(t *testing.T) {
	groups := []*models.Group{
		{Name: "admins"},
		{Name: "users"},
		nil,
		{Name: ""},
	}

	got := BuildPointerIndex(groups, func(g *models.Group) string { return g.Name })

	assert.Len(t, got, 2)
	assert.Contains(t, got, "admins")
	assert.Contains(t, got, "users")

	t.Run("nil input yields nil index", func(t *testing.T) {
		assert.Nil(t, BuildPointerIndex[models.Group](nil, func(g *models.Group) string { return g.Name }))
	})
}
