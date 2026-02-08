// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package templating

import (
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"gitlab.com/haproxy-haptic/scriggo"
)

// scriggoTemplateFS implements fs.FS, fs.ReadDirFS, and scriggo.FormatFS for Scriggo template loading.
// FormatFS is required so Scriggo knows all our templates are Text format (HAProxy config).
// ReadDirFS is required for fs.WalkDir used by buildDynamicMacros to discover all templates.
type scriggoTemplateFS struct {
	templates map[string]string
}

func (f *scriggoTemplateFS) Open(name string) (fs.File, error) {
	// Handle root directory
	if name == "." {
		return &scriggoRootDir{fs: f}, nil
	}
	content, ok := f.templates[name]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return &scriggoTemplateFile{
		name:    name,
		content: content,
		reader:  strings.NewReader(content),
	}, nil
}

// ReadDir implements fs.ReadDirFS interface for directory listing.
// This is required by fs.WalkDir used in buildDynamicMacros.
func (f *scriggoTemplateFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if name != "." {
		return nil, fs.ErrNotExist
	}

	// Return all templates as directory entries
	entries := make([]fs.DirEntry, 0, len(f.templates))
	for templateName := range f.templates {
		entries = append(entries, &scriggoDirEntry{
			name: templateName,
			size: int64(len(f.templates[templateName])),
		})
	}

	// Sort for deterministic order
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	return entries, nil
}

// Format implements scriggo.FormatFS interface.
// Returns Text format for all templates since HAProxy config is plain text.
// This tells Scriggo not to apply HTML escaping or other format-specific processing.
func (f *scriggoTemplateFS) Format(name string) (scriggo.Format, error) {
	if _, ok := f.templates[name]; !ok {
		return 0, fs.ErrNotExist
	}
	return scriggo.FormatText, nil
}

// scriggoRootDir represents the root directory for fs.WalkDir.
type scriggoRootDir struct {
	fs *scriggoTemplateFS
}

func (d *scriggoRootDir) Read(b []byte) (int, error) {
	return 0, fmt.Errorf("cannot read directory")
}

func (d *scriggoRootDir) Close() error {
	return nil
}

func (d *scriggoRootDir) Stat() (fs.FileInfo, error) {
	return &scriggoFileInfo{name: ".", size: 0, isDir: true}, nil
}

func (d *scriggoRootDir) ReadDir(n int) ([]fs.DirEntry, error) {
	return d.fs.ReadDir(".")
}

// scriggoDirEntry implements fs.DirEntry for template files.
type scriggoDirEntry struct {
	name string
	size int64
}

func (e *scriggoDirEntry) Name() string      { return e.name }
func (e *scriggoDirEntry) IsDir() bool       { return false }
func (e *scriggoDirEntry) Type() fs.FileMode { return 0 }
func (e *scriggoDirEntry) Info() (fs.FileInfo, error) {
	return &scriggoFileInfo{name: e.name, size: e.size}, nil
}

// scriggoTemplateFile implements fs.File for in-memory template content.
type scriggoTemplateFile struct {
	name    string
	content string
	reader  *strings.Reader
}

func (f *scriggoTemplateFile) Read(b []byte) (int, error) {
	return f.reader.Read(b)
}

func (f *scriggoTemplateFile) Close() error {
	return nil
}

func (f *scriggoTemplateFile) Stat() (fs.FileInfo, error) {
	return &scriggoFileInfo{name: f.name, size: int64(len(f.content))}, nil
}

// scriggoFileInfo implements fs.FileInfo.
type scriggoFileInfo struct {
	name  string
	size  int64
	isDir bool
}

func (fi *scriggoFileInfo) Name() string       { return fi.name }
func (fi *scriggoFileInfo) Size() int64        { return fi.size }
func (fi *scriggoFileInfo) Mode() fs.FileMode  { return 0o444 }
func (fi *scriggoFileInfo) ModTime() time.Time { return time.Time{} }
func (fi *scriggoFileInfo) IsDir() bool        { return fi.isDir }
func (fi *scriggoFileInfo) Sys() interface{}   { return nil }
