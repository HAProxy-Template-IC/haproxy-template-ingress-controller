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

// auxFileDiffToOperations is the per-type generic helper that auxDiffsToOperations
// dispatches to. It controls the section/label fields and the description text
// that surface in SyncResult.AppliedOperations, which is part of the public
// orchestrator output.
func TestAuxFileDiffToOperations_GeneralFile(t *testing.T) {
	toCreate := []auxiliaryfiles.GeneralFile{{Filename: "400.http", Content: "x"}}
	toUpdate := []auxiliaryfiles.GeneralFile{{Filename: "503.http", Content: "y"}}
	toDelete := []string{"old.http"}

	got := auxFileDiffToOperations(toCreate, toUpdate, toDelete, "file", "general file")

	assert.Equal(t, []AppliedOperation{
		{Type: opCreate, Section: "file", Resource: "400.http", Description: "Created general file 400.http"},
		{Type: opUpdate, Section: "file", Resource: "503.http", Description: "Updated general file 503.http"},
		{Type: opDelete, Section: "file", Resource: "old.http", Description: "Deleted general file old.http"},
	}, got)
}

func TestAuxFileDiffToOperations_EmptyInputs(t *testing.T) {
	got := auxFileDiffToOperations[auxiliaryfiles.MapFile](nil, nil, nil, "map", "map file")
	assert.Empty(t, got, "all-empty inputs produce no operations")
}

// auxDiffsToOperations dispatches to auxFileDiffToOperations for each populated
// per-type diff, in a fixed order: file → ssl → ca → map → crtlist. Pin the
// dispatch + section labels so a refactor can't silently rename the public
// AppliedOperation.Section identifiers consumed by SyncResult.
func TestAuxDiffsToOperations(t *testing.T) {
	t.Run("nil diffs returns nil", func(t *testing.T) {
		assert.Nil(t, auxDiffsToOperations(nil))
	})

	t.Run("each populated category produces operations with the right section", func(t *testing.T) {
		diffs := &auxiliaryFileDiffs{
			fileDiff:    &auxiliaryfiles.FileDiff{ToCreate: []auxiliaryfiles.GeneralFile{{Filename: "a.http"}}},
			sslDiff:     &auxiliaryfiles.SSLCertificateDiff{ToCreate: []auxiliaryfiles.SSLCertificate{{Path: "/ssl/a.pem"}}},
			caFileDiff:  &auxiliaryfiles.SSLCaFileDiff{ToCreate: []auxiliaryfiles.SSLCaFile{{Path: "/ca/a.pem"}}},
			mapDiff:     &auxiliaryfiles.MapFileDiff{ToCreate: []auxiliaryfiles.MapFile{{Path: "/maps/a.map"}}},
			crtlistDiff: &auxiliaryfiles.CRTListDiff{ToCreate: []auxiliaryfiles.CRTListFile{{Path: "/crt/a.list"}}},
		}

		got := auxDiffsToOperations(diffs)

		// Order matters: file → ssl-cert → ssl-ca → map → crt-list. Each
		// category contributes one create op for the resource above.
		assert.Equal(t, []AppliedOperation{
			{Type: opCreate, Section: "file", Resource: "a.http", Description: "Created general file a.http"},
			{Type: opCreate, Section: "ssl-cert", Resource: "/ssl/a.pem", Description: "Created SSL certificate /ssl/a.pem"},
			{Type: opCreate, Section: "ssl-ca", Resource: "/ca/a.pem", Description: "Created SSL CA file /ca/a.pem"},
			{Type: opCreate, Section: "map", Resource: "/maps/a.map", Description: "Created map file /maps/a.map"},
			{Type: opCreate, Section: "crt-list", Resource: "/crt/a.list", Description: "Created crt-list file /crt/a.list"},
		}, got)
	})

	t.Run("empty struct produces no operations", func(t *testing.T) {
		assert.Empty(t, auxDiffsToOperations(&auxiliaryFileDiffs{}))
	})
}

// addAuxiliaryFileCounts populates DiffDetails from per-type diffs. Pin that
// only present diffs are accounted for and that nil category fields leave the
// corresponding DiffDetails counters at zero.
func TestAddAuxiliaryFileCounts(t *testing.T) {
	t.Run("nil diffs leaves details unchanged", func(t *testing.T) {
		details := &DiffDetails{}
		addAuxiliaryFileCounts(details, nil)
		assert.Equal(t, &DiffDetails{}, details)
	})

	t.Run("each per-type diff populates its category counters", func(t *testing.T) {
		details := &DiffDetails{}
		addAuxiliaryFileCounts(details, &auxiliaryFileDiffs{
			fileDiff: &auxiliaryfiles.FileDiff{
				ToCreate: []auxiliaryfiles.GeneralFile{{}, {}},
				ToUpdate: []auxiliaryfiles.GeneralFile{{}},
				ToDelete: []string{"x"},
			},
			sslDiff: &auxiliaryfiles.SSLCertificateDiff{
				ToCreate: []auxiliaryfiles.SSLCertificate{{}},
			},
			caFileDiff: &auxiliaryfiles.SSLCaFileDiff{
				ToDelete: []string{"a", "b", "c"},
			},
			mapDiff: &auxiliaryfiles.MapFileDiff{
				ToUpdate: []auxiliaryfiles.MapFile{{}, {}},
			},
		})

		assert.Equal(t, 2, details.GeneralFilesAdded)
		assert.Equal(t, 1, details.GeneralFilesModified)
		assert.Equal(t, 1, details.GeneralFilesDeleted)

		assert.Equal(t, 1, details.SSLCertsAdded)
		assert.Equal(t, 0, details.SSLCertsModified)
		assert.Equal(t, 0, details.SSLCertsDeleted)

		assert.Equal(t, 0, details.SSLCaFilesAdded)
		assert.Equal(t, 0, details.SSLCaFilesModified)
		assert.Equal(t, 3, details.SSLCaFilesDeleted)

		assert.Equal(t, 0, details.MapsAdded)
		assert.Equal(t, 2, details.MapsModified)
		assert.Equal(t, 0, details.MapsDeleted)
	})
}
