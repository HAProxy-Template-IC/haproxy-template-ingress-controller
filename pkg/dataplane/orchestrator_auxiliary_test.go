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
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
)

// preConfigDiff is a tiny generic helper that drops ToDelete from a
// diff before the pre-config phase of the three-phase auxiliary file
// sync. It has NO direct test coverage even though it sits on the
// hot path of every sync that involves auxiliary file changes.
//
// The contract is critical and easy to silently regress:
//
//  1. ToCreate and ToUpdate must round-trip into the new diff
//     (otherwise pre-config phase wouldn't actually create or update
//     the files that the new config will reference).
//  2. ToDelete MUST be dropped (empty/nil in the result). This is
//     the entire reason the helper exists: deletes happen AFTER the
//     new config is live, in deleteUnreferencedFilesPostConfig.
//     A regression that included ToDelete in the pre-config diff
//     would delete files that the OLD live config still references,
//     breaking HAProxy until the new config takes over.
//  3. The original diff MUST NOT be mutated — preConfigDiff returns
//     a new struct value, never the same pointer with fields zeroed.
//     Callers may still want to inspect ToDelete from the original
//     diff for the post-config phase.
//
// Use the auxiliaryfiles types directly so the test exercises the
// real types rather than synthetic stand-ins. GeneralFile is the
// simplest FileItem in the package, suitable for the table-driven
// shape.
func TestPreConfigDiff(t *testing.T) {
	tests := []struct {
		name string
		in   *auxiliaryfiles.FileDiffGeneric[auxiliaryfiles.GeneralFile]
	}{
		{
			name: "empty diff in -> empty diff out (ToDelete drop is a no-op)",
			in:   &auxiliaryfiles.FileDiffGeneric[auxiliaryfiles.GeneralFile]{},
		},
		{
			name: "creates only -> preserved verbatim, ToDelete remains empty",
			in: &auxiliaryfiles.FileDiffGeneric[auxiliaryfiles.GeneralFile]{
				ToCreate: []auxiliaryfiles.GeneralFile{
					{Filename: "400.http", Content: "HTTP/1.0 400 Bad Request\r\n"},
				},
			},
		},
		{
			name: "updates only -> preserved verbatim",
			in: &auxiliaryfiles.FileDiffGeneric[auxiliaryfiles.GeneralFile]{
				ToUpdate: []auxiliaryfiles.GeneralFile{
					{Filename: "500.http", Content: "HTTP/1.0 500 Server Error\r\n"},
				},
			},
		},
		{
			name: "deletes only -> result is empty (deletes go to the post-config phase)",
			in: &auxiliaryfiles.FileDiffGeneric[auxiliaryfiles.GeneralFile]{
				ToDelete: []string{"old-file.txt", "stale.http"},
			},
		},
		{
			name: "all three populated -> creates+updates kept, deletes dropped",
			in: &auxiliaryfiles.FileDiffGeneric[auxiliaryfiles.GeneralFile]{
				ToCreate: []auxiliaryfiles.GeneralFile{{Filename: "new.txt", Content: "x"}},
				ToUpdate: []auxiliaryfiles.GeneralFile{{Filename: "changed.txt", Content: "y"}},
				ToDelete: []string{"deleted.txt"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Snapshot the input ToDelete so we can verify
			// preConfigDiff doesn't mutate it. The bug we're guarding
			// against: a refactor that mutated diff.ToDelete = nil in
			// place would corrupt the post-config phase by losing the
			// file list that needs deletion later.
			origToDelete := tt.in.ToDelete

			out := preConfigDiff(tt.in)

			require.NotNil(t, out, "preConfigDiff must always return a non-nil diff (callers deref it)")

			// 1. ToCreate / ToUpdate round-trip.
			assert.Equal(t, tt.in.ToCreate, out.ToCreate,
				"ToCreate must be preserved; dropping it would skip pre-config file creation "+
					"and break the new config's file references")
			assert.Equal(t, tt.in.ToUpdate, out.ToUpdate,
				"ToUpdate must be preserved; dropping it would skip pre-config file updates "+
					"and serve stale content under the new config")

			// 2. ToDelete is dropped.
			assert.Empty(t, out.ToDelete,
				"ToDelete must be empty in the pre-config diff; "+
					"otherwise files referenced by the still-live OLD config would be deleted "+
					"before the new config takes over, breaking HAProxy mid-transition")

			// 3. Original diff is untouched.
			assert.Equal(t, origToDelete, tt.in.ToDelete,
				"preConfigDiff MUST NOT mutate the input — the post-config phase relies on "+
					"the original ToDelete to know what to clean up after the new config is live")
		})
	}
}

// preConfigDiff is generic. Pin that it works for an additional file
// type so a regression in the type parameter usage (e.g. accidentally
// hardcoding GeneralFile somewhere) would be caught.
func TestPreConfigDiff_Generic_MapFile(t *testing.T) {
	in := &auxiliaryfiles.FileDiffGeneric[auxiliaryfiles.MapFile]{
		ToCreate: []auxiliaryfiles.MapFile{{Path: "/etc/haproxy/maps/host.map", Content: "example.com backend1"}},
		ToDelete: []string{"old-host.map"},
	}

	out := preConfigDiff(in)

	require.NotNil(t, out)
	assert.Equal(t, in.ToCreate, out.ToCreate, "MapFile creates round-trip")
	assert.Empty(t, out.ToDelete, "MapFile deletes are dropped (post-config phase only)")
	assert.NotEmpty(t, in.ToDelete, "input MapFile diff is not mutated")
}
