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

// AuxiliaryFiles.Sort is load-bearing for downstream consumers (checksum
// computation, diff calculation) that iterate the slices directly without
// cloning or sorting again. The Sort contract has TWO non-obvious rules:
//
//  1. Each file-type slice is sorted IN-PLACE by its OWN identifier
//     field — GeneralFile by Filename, the other four (SSLCertificate,
//     SSLCaFile, MapFile, CRTListFile) by Path. A regression that
//     accidentally unified all five to use the same field would silently
//     break GeneralFile sorting (different files can share a Path
//     prefix), making checksum stability brittle.
//  2. Sort must be stable across re-invocation — sorting an already
//     sorted slice must not change the order. Otherwise downstream
//     checksum recomputation would oscillate.
//
// These tests pin the per-field sort behaviour so a refactor cannot
// silently swap the comparator key.

func TestAuxiliaryFiles_Sort_PerSliceFieldSpecific(t *testing.T) {
	// Build a struct with deliberately UNORDERED entries in every
	// slice. After Sort() the order must be ascending by the
	// type-specific identifier field, NOT by the other field.
	aux := &AuxiliaryFiles{
		GeneralFiles: []auxiliaryfiles.GeneralFile{
			// GeneralFile sorts by Filename. We give the entries
			// PATHS in REVERSE alphabetical order to ensure a
			// regression that sorted by Path would produce a
			// detectably wrong result.
			{Filename: "z.http", Path: "/a/z.http"},
			{Filename: "a.http", Path: "/z/a.http"},
			{Filename: "m.http", Path: "/m/m.http"},
		},
		SSLCertificates: []auxiliaryfiles.SSLCertificate{
			{Path: "/etc/ssl/z.pem"},
			{Path: "/etc/ssl/a.pem"},
			{Path: "/etc/ssl/m.pem"},
		},
		SSLCaFiles: []auxiliaryfiles.SSLCaFile{
			{Path: "/etc/ca/z-ca.pem"},
			{Path: "/etc/ca/a-ca.pem"},
		},
		MapFiles: []auxiliaryfiles.MapFile{
			{Path: "/etc/maps/z.map"},
			{Path: "/etc/maps/a.map"},
		},
		CRTListFiles: []auxiliaryfiles.CRTListFile{
			{Path: "/etc/crt-lists/z.txt"},
			{Path: "/etc/crt-lists/a.txt"},
		},
	}

	aux.Sort()

	// GeneralFile pinned to Filename: a.http < m.http < z.http.
	// The Path values are deliberately in reverse — a regression
	// that sorted by Path would yield "/a/z.http" first.
	require.Len(t, aux.GeneralFiles, 3)
	assert.Equal(t, []string{"a.http", "m.http", "z.http"},
		[]string{
			aux.GeneralFiles[0].Filename,
			aux.GeneralFiles[1].Filename,
			aux.GeneralFiles[2].Filename,
		},
		"GeneralFiles MUST be sorted by Filename (not Path) — "+
			"a regression would silently break checksum stability "+
			"because GeneralFiles in different directories would "+
			"sort in unpredictable order")

	// The other four file types all sort by Path. Pin each one so
	// a regression that swapped the comparator on any single type
	// surfaces independently.
	assert.Equal(t, []string{"/etc/ssl/a.pem", "/etc/ssl/m.pem", "/etc/ssl/z.pem"},
		pathsOf(aux.SSLCertificates),
		"SSLCertificates MUST sort by Path")
	assert.Equal(t, []string{"/etc/ca/a-ca.pem", "/etc/ca/z-ca.pem"},
		pathsOfCA(aux.SSLCaFiles),
		"SSLCaFiles MUST sort by Path")
	assert.Equal(t, []string{"/etc/maps/a.map", "/etc/maps/z.map"},
		pathsOfMap(aux.MapFiles),
		"MapFiles MUST sort by Path")
	assert.Equal(t, []string{"/etc/crt-lists/a.txt", "/etc/crt-lists/z.txt"},
		pathsOfCRTList(aux.CRTListFiles),
		"CRTListFiles MUST sort by Path")
}

func TestAuxiliaryFiles_Sort_IdempotentForChecksumStability(t *testing.T) {
	// Sort must be stable across re-invocation so downstream
	// checksum recomputation doesn't oscillate. Build an already
	// sorted slice and sort again — the order must not change.
	aux := &AuxiliaryFiles{
		GeneralFiles: []auxiliaryfiles.GeneralFile{
			{Filename: "a.http", Content: "x"},
			{Filename: "b.http", Content: "y"},
		},
		SSLCertificates: []auxiliaryfiles.SSLCertificate{
			{Path: "/a", Content: "x"},
			{Path: "/b", Content: "y"},
		},
		MapFiles: []auxiliaryfiles.MapFile{
			{Path: "/a", Content: "x"},
			{Path: "/b", Content: "y"},
		},
	}

	aux.Sort()
	first := snapshot(aux)
	aux.Sort()
	second := snapshot(aux)

	assert.Equal(t, first, second,
		"Sort MUST be idempotent — sorting an already sorted slice must "+
			"not change order; otherwise downstream checksum recomputation "+
			"would oscillate and make drift detection flaky")
}

func TestAuxiliaryFiles_Sort_HandlesEmptyAndSingleElement(t *testing.T) {
	// Edge cases: empty slices and single-element slices must not
	// panic and must leave the slice unchanged. Both are common —
	// many deployments don't use SSLCaFiles or CRTListFiles, and
	// "first sync" deployments often have just one of each.
	tests := []struct {
		name string
		aux  *AuxiliaryFiles
	}{
		{
			name: "all-empty struct",
			aux:  &AuxiliaryFiles{},
		},
		{
			name: "all-nil slices",
			aux: &AuxiliaryFiles{
				GeneralFiles:    nil,
				SSLCertificates: nil,
				SSLCaFiles:      nil,
				MapFiles:        nil,
				CRTListFiles:    nil,
			},
		},
		{
			name: "single-element slices",
			aux: &AuxiliaryFiles{
				GeneralFiles:    []auxiliaryfiles.GeneralFile{{Filename: "x"}},
				SSLCertificates: []auxiliaryfiles.SSLCertificate{{Path: "/x"}},
				SSLCaFiles:      []auxiliaryfiles.SSLCaFile{{Path: "/x"}},
				MapFiles:        []auxiliaryfiles.MapFile{{Path: "/x"}},
				CRTListFiles:    []auxiliaryfiles.CRTListFile{{Path: "/x"}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.NotPanics(t, tt.aux.Sort,
				"Sort must handle empty/nil slices without panicking")
		})
	}
}

// snapshot captures only the identifier fields (which Sort uses) so
// idempotence assertions ignore content/etc. and focus on order.
func snapshot(a *AuxiliaryFiles) []string {
	out := make([]string, 0,
		len(a.GeneralFiles)+len(a.SSLCertificates)+len(a.SSLCaFiles)+len(a.MapFiles)+len(a.CRTListFiles))
	for _, f := range a.GeneralFiles {
		out = append(out, "g:"+f.Filename)
	}
	for _, f := range a.SSLCertificates {
		out = append(out, "s:"+f.Path)
	}
	for _, f := range a.SSLCaFiles {
		out = append(out, "ca:"+f.Path)
	}
	for _, f := range a.MapFiles {
		out = append(out, "m:"+f.Path)
	}
	for _, f := range a.CRTListFiles {
		out = append(out, "c:"+f.Path)
	}
	return out
}

func pathsOf(items []auxiliaryfiles.SSLCertificate) []string {
	out := make([]string, len(items))
	for i, x := range items {
		out[i] = x.Path
	}
	return out
}

func pathsOfCA(items []auxiliaryfiles.SSLCaFile) []string {
	out := make([]string, len(items))
	for i, x := range items {
		out[i] = x.Path
	}
	return out
}

func pathsOfMap(items []auxiliaryfiles.MapFile) []string {
	out := make([]string, len(items))
	for i, x := range items {
		out[i] = x.Path
	}
	return out
}

func pathsOfCRTList(items []auxiliaryfiles.CRTListFile) []string {
	out := make([]string, len(items))
	for i, x := range items {
		out[i] = x.Path
	}
	return out
}
