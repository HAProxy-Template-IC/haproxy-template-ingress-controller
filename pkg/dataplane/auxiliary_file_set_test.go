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

package dataplane

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
)

func TestCanonicalizeAuxiliaryFiles_DeduplicatesAndCopies(t *testing.T) {
	reloadOnPush := true
	files := &AuxiliaryFiles{
		MapFiles: []auxiliaryfiles.MapFile{
			{Path: "z.map", Content: "z"},
			{Path: "a.map", Content: "a"},
			{Path: "a.map", Content: "a"},
		},
		GeneralFiles: []auxiliaryfiles.GeneralFile{
			{Filename: "error.http", Path: "/files/error.http", Content: "error", ReloadOnPush: &reloadOnPush},
			{Filename: "error.http", Path: "/files/error.http", Content: "error"},
		},
	}

	canonical, err := CanonicalizeAuxiliaryFiles(files)

	require.NoError(t, err)
	require.Len(t, canonical.MapFiles, 2)
	assert.Equal(t, []string{"a.map", "z.map"}, []string{canonical.MapFiles[0].Path, canonical.MapFiles[1].Path})
	require.Len(t, canonical.GeneralFiles, 1)
	require.NotNil(t, canonical.GeneralFiles[0].ReloadOnPush)
	assert.NotSame(t, files.GeneralFiles[0].ReloadOnPush, canonical.GeneralFiles[0].ReloadOnPush)
	assert.Equal(t, "z.map", files.MapFiles[0].Path, "canonicalization must not sort the input")

	reloadOnPush = false
	assert.True(t, canonical.GeneralFiles[0].ReloadsOnPush(), "canonical output must not retain mutable input pointers")
}

func TestCanonicalizeAuxiliaryFiles_RejectsConflictingStorageIdentities(t *testing.T) {
	tests := []struct {
		name  string
		files *AuxiliaryFiles
		want  string
	}{
		{
			name: "map path",
			files: &AuxiliaryFiles{MapFiles: []auxiliaryfiles.MapFile{
				{Path: "routes.map", Content: "one"},
				{Path: "routes.map", Content: "two"},
			}},
			want: `Map file "routes.map" has conflicting definitions`,
		},
		{
			name: "general filename",
			files: &AuxiliaryFiles{GeneralFiles: []auxiliaryfiles.GeneralFile{
				{Filename: "error.http", Path: "/files/error.http", Content: "one"},
				{Filename: "error.http", Path: "/files/error.http", Content: "two"},
			}},
			want: `General file "error.http" has conflicting definitions`,
		},
		{
			name: "file and ca-file registration",
			files: &AuxiliaryFiles{GeneralFiles: []auxiliaryfiles.GeneralFile{
				{Filename: "bundle.pem", Path: "/files/bundle.pem", Content: "same"},
				{Filename: "bundle.pem", Path: "/files/bundle.pem", Content: "same", IsCaFile: true},
			}},
			want: `General file "bundle.pem" has conflicting definitions`,
		},
		{
			name: "normalized certificate name",
			files: &AuxiliaryFiles{SSLCertificates: []auxiliaryfiles.SSLCertificate{
				{Path: "example.com.pem", Content: "certificate"},
				{Path: "example_com.pem", Content: "certificate"},
			}},
			want: `SSL certificate "example_com.pem" has conflicting definitions`,
		},
		{
			name: "ca basename",
			files: &AuxiliaryFiles{SSLCaFiles: []auxiliaryfiles.SSLCaFile{
				{Path: "/one/bundle.pem", Content: "one"},
				{Path: "/two/bundle.pem", Content: "two"},
			}},
			want: `SSL CA file "bundle.pem" has conflicting definitions`,
		},
		{
			name: "normalized crt-list name",
			files: &AuxiliaryFiles{CRTListFiles: []auxiliaryfiles.CRTListFile{
				{Path: "example.com.txt", Content: "one"},
				{Path: "example_com.txt", Content: "two"},
			}},
			want: `CRT-list file "example_com.txt" has conflicting definitions`,
		},
		{
			name: "general file and crt-list storage",
			files: &AuxiliaryFiles{
				GeneralFiles: []auxiliaryfiles.GeneralFile{{Filename: "certificate_list.txt", Content: "one"}},
				CRTListFiles: []auxiliaryfiles.CRTListFile{{Path: "certificate.list.txt", Content: "two"}},
			},
			want: `general file and CRT-list "certificate_list.txt" use the same storage name`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := CanonicalizeAuxiliaryFiles(tt.files)
			require.ErrorContains(t, err, tt.want)
		})
	}
}
