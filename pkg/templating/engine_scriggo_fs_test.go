// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package templating

import (
	"errors"
	"io"
	"io/fs"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/scriggo"
)

// scriggoTemplateFS implements three filesystem interfaces (fs.FS,
// fs.ReadDirFS, scriggo.FormatFS) that Scriggo relies on at compile
// and walk time. The basic Open + Read happy-path is already covered
// in engine_scriggo_test.go. The remaining behaviours are load-bearing
// but uncovered:
//
//   - ReadDir(".") returns ALL templates as DirEntries, sorted
//     alphabetically. Sort stability is the contract that keeps
//     buildDynamicMacros' template iteration deterministic across
//     reconciliations — losing it would silently shuffle macro
//     resolution order.
//   - ReadDir(<non-root>) returns fs.ErrNotExist. Scriggo's
//     fs.WalkDir would otherwise descend into phantom subdirectories.
//   - Format("known") -> scriggo.FormatText (NOT FormatHTML). If a
//     refactor returned FormatHTML for our text templates, Scriggo
//     would silently inject HTML escaping into HAProxy config and
//     break every backslash, quote, and bracket in regex ACLs.
//   - Format("unknown") -> fs.ErrNotExist (matches Open's contract).
//   - Open(".") returns the root dir wrapper; the wrapper's Stat
//     reports IsDir=true and Read returns an error (you can't read
//     a directory).
//   - DirEntry / FileInfo plumbing returns the right Name / Size /
//     IsDir / Mode flags so Scriggo's WalkDir treats files as files.
func TestScriggoTemplateFS_ReadDirReturnsSortedTemplateList(t *testing.T) {
	templates := map[string]string{
		"zeta":  "z",
		"alpha": "a",
		"mu":    "m",
	}
	tfs := &scriggoTemplateFS{templates: templates}

	entries, err := tfs.ReadDir(".")
	require.NoError(t, err)
	require.Len(t, entries, 3, "every template must be enumerated for fs.WalkDir")

	got := make([]string, len(entries))
	for i, e := range entries {
		got[i] = e.Name()
	}

	// Stable alphabetical order is the contract that keeps macro
	// resolution deterministic across reconciliations. A regression
	// to map-iteration order would compile fine but produce
	// run-dependent behaviour.
	assert.Equal(t, []string{"alpha", "mu", "zeta"}, got)

	// Each entry must report itself as a regular file (not a
	// directory). buildDynamicMacros relies on this to skip
	// directory entries via WalkDir's d.IsDir() check.
	for _, e := range entries {
		assert.False(t, e.IsDir(), "template entry %q must not be reported as a directory", e.Name())
		assert.Equal(t, fs.FileMode(0), e.Type(), "templates have no type bits set (regular files)")
	}
}

func TestScriggoTemplateFS_ReadDirOnNonRootReturnsNotExist(t *testing.T) {
	// Scriggo's fs.WalkDir would otherwise descend into phantom
	// subdirectories. Returning fs.ErrNotExist matches the standard
	// fs.FS contract for missing paths.
	tfs := &scriggoTemplateFS{templates: map[string]string{"a": "x"}}

	_, err := tfs.ReadDir("subdir")
	require.Error(t, err)
	assert.True(t, errors.Is(err, fs.ErrNotExist),
		"non-root ReadDir must return fs.ErrNotExist; got %v", err)
}

func TestScriggoTemplateFS_FormatHonoursTextForKnownTemplates(t *testing.T) {
	tfs := &scriggoTemplateFS{templates: map[string]string{
		"haproxy.cfg": "global\n  daemon\n",
	}}

	format, err := tfs.Format("haproxy.cfg")
	require.NoError(t, err)
	assert.Equal(t, scriggo.FormatText, format,
		"all our templates are HAProxy config (text). Returning FormatHTML would inject "+
			"HTML escaping that breaks every backslash, quote, and bracket in regex ACLs.")
}

func TestScriggoTemplateFS_FormatReturnsNotExistForUnknownName(t *testing.T) {
	tfs := &scriggoTemplateFS{templates: map[string]string{"a": "x"}}

	_, err := tfs.Format("unknown.cfg")
	require.Error(t, err)
	assert.True(t, errors.Is(err, fs.ErrNotExist),
		"Format must mirror Open's not-exist contract; got %v", err)
}

func TestScriggoTemplateFS_OpenRootDirectory(t *testing.T) {
	templates := map[string]string{"a": "x", "b": "y"}
	tfs := &scriggoTemplateFS{templates: templates}

	rootFile, err := tfs.Open(".")
	require.NoError(t, err)
	require.NotNil(t, rootFile)
	defer rootFile.Close()

	stat, err := rootFile.Stat()
	require.NoError(t, err)
	assert.True(t, stat.IsDir(), "root must report IsDir=true so WalkDir treats it as a directory")
	assert.Equal(t, ".", stat.Name())

	// Reading from a directory must error: scriggo / fs.WalkDir
	// must NEVER consume directory bytes as file content.
	_, err = rootFile.Read(make([]byte, 8))
	require.Error(t, err, "Read on a directory entry must error to prevent content corruption")

	// Root-as-ReadDirFile must yield the same listing as ReadDir(".").
	rootDir, ok := rootFile.(fs.ReadDirFile)
	require.True(t, ok, "root must implement fs.ReadDirFile so WalkDir can recurse via the file handle")
	entries, err := rootDir.ReadDir(-1)
	require.NoError(t, err)
	got := make([]string, len(entries))
	for i, e := range entries {
		got[i] = e.Name()
	}
	assert.Equal(t, []string{"a", "b"}, got, "root.ReadDir must return same sorted listing as fs.ReadDir(.)")
}

func TestScriggoTemplateFS_OpenAndReadRegularFile(t *testing.T) {
	// Pin the contract that Read fully drains the content and
	// reports io.EOF on a follow-up call. A previous regression
	// where Read returned (n, nil) on EOF made callers loop forever.
	tfs := &scriggoTemplateFS{templates: map[string]string{
		"snippet": "abc",
	}}

	file, err := tfs.Open("snippet")
	require.NoError(t, err)
	defer file.Close()

	body, err := io.ReadAll(file)
	require.NoError(t, err, "io.ReadAll must succeed and stop at EOF; a missing io.EOF would loop forever")
	assert.Equal(t, "abc", string(body))

	stat, err := file.Stat()
	require.NoError(t, err)
	assert.Equal(t, "snippet", stat.Name())
	assert.Equal(t, int64(3), stat.Size(), "Size must reflect the underlying string length")
	assert.False(t, stat.IsDir())
	assert.Equal(t, fs.FileMode(0o444), stat.Mode(),
		"templates are read-only — exposing 0o644 would let buggy callers attempt writes")
}

func TestScriggoTemplateFS_OpenReturnsErrNotExistForUnknownName(t *testing.T) {
	tfs := &scriggoTemplateFS{templates: map[string]string{"a": "x"}}

	_, err := tfs.Open("does-not-exist")
	require.Error(t, err)
	assert.True(t, errors.Is(err, fs.ErrNotExist),
		"Open must return fs.ErrNotExist (not a custom error string); fs.WalkDir's error handling depends on it")
}
