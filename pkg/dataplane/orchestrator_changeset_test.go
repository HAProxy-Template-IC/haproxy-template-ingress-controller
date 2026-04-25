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

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
)

// auxiliaryFileDiffs.anyDiffHasChanges is the gate orchestrator.checkForChanges
// uses to decide whether the deploy phase has work to do for aux files.
// Pin every per-category branch so a future refactor can't silently drop one.
func TestAuxiliaryFileDiffs_AnyDiffHasChanges(t *testing.T) {
	withChange := func() *auxiliaryfiles.FileDiff {
		return &auxiliaryfiles.FileDiff{
			ToCreate: []auxiliaryfiles.GeneralFile{{Filename: "x"}},
		}
	}
	emptyFile := func() *auxiliaryfiles.FileDiff { return &auxiliaryfiles.FileDiff{} }

	tests := []struct {
		name string
		in   *auxiliaryFileDiffs
		want bool
	}{
		{
			name: "all nil per-type diffs report no changes",
			in:   &auxiliaryFileDiffs{},
			want: false,
		},
		{
			name: "all empty per-type diffs report no changes",
			in: &auxiliaryFileDiffs{
				fileDiff:    emptyFile(),
				sslDiff:     &auxiliaryfiles.SSLCertificateDiff{},
				caFileDiff:  &auxiliaryfiles.SSLCaFileDiff{},
				mapDiff:     &auxiliaryfiles.MapFileDiff{},
				crtlistDiff: &auxiliaryfiles.CRTListDiff{},
			},
			want: false,
		},
		{
			name: "general file changes report changes",
			in:   &auxiliaryFileDiffs{fileDiff: withChange()},
			want: true,
		},
		{
			name: "ssl cert changes report changes",
			in: &auxiliaryFileDiffs{
				sslDiff: &auxiliaryfiles.SSLCertificateDiff{
					ToUpdate: []auxiliaryfiles.SSLCertificate{{Path: "/p"}},
				},
			},
			want: true,
		},
		{
			name: "ssl ca changes report changes",
			in: &auxiliaryFileDiffs{
				caFileDiff: &auxiliaryfiles.SSLCaFileDiff{
					ToDelete: []string{"old"},
				},
			},
			want: true,
		},
		{
			name: "map file changes report changes",
			in: &auxiliaryFileDiffs{
				mapDiff: &auxiliaryfiles.MapFileDiff{
					ToCreate: []auxiliaryfiles.MapFile{{Path: "/p"}},
				},
			},
			want: true,
		},
		{
			name: "crt-list changes report changes",
			in: &auxiliaryFileDiffs{
				crtlistDiff: &auxiliaryfiles.CRTListDiff{
					ToUpdate: []auxiliaryfiles.CRTListFile{{Path: "/p"}},
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.in.anyDiffHasChanges())
		})
	}
}

// checksumMatchesLastDeployed gates the fast-path that skips the (expensive)
// auxiliary-file Dataplane API roundtrip. The contract — both checksums must
// be set AND equal — is part of the orchestrator's safety story (an empty
// checksum must NEVER short-circuit a sync).
func TestChecksumMatchesLastDeployed(t *testing.T) {
	tests := []struct {
		name string
		opts *SyncOptions
		want bool
	}{
		{
			name: "both checksums set and equal returns true",
			opts: &SyncOptions{ContentChecksum: "abc", LastDeployedChecksum: "abc"},
			want: true,
		},
		{
			name: "set but different returns false",
			opts: &SyncOptions{ContentChecksum: "abc", LastDeployedChecksum: "xyz"},
			want: false,
		},
		{
			name: "ContentChecksum empty returns false (must never short-circuit)",
			opts: &SyncOptions{ContentChecksum: "", LastDeployedChecksum: "abc"},
			want: false,
		},
		{
			name: "LastDeployedChecksum empty returns false (must never short-circuit)",
			opts: &SyncOptions{ContentChecksum: "abc", LastDeployedChecksum: ""},
			want: false,
		},
		{
			name: "both empty returns false (zero == zero must NOT short-circuit)",
			opts: &SyncOptions{},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, checksumMatchesLastDeployed(tt.opts))
		})
	}
}
