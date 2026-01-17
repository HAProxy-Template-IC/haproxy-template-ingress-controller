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

package rendercontext

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestFileRegistry_Register(t *testing.T) {
	pathResolver := &templating.PathResolver{
		MapsDir:    "/etc/haproxy/maps",
		SSLDir:     "/etc/haproxy/certs",
		CRTListDir: "/etc/haproxy/crt-list",
		GeneralDir: "/etc/haproxy/files",
	}

	tests := []struct {
		name        string
		fileType    string
		filename    string
		content     string
		wantPath    string
		wantErr     bool
		errContains string
	}{
		{
			name:     "register map file",
			fileType: "map",
			filename: "hosts.map",
			content:  "host1 backend1\nhost2 backend2",
			wantPath: "/etc/haproxy/maps/hosts.map",
		},
		{
			name:     "register cert file",
			fileType: "cert",
			filename: "server.pem",
			content:  "-----BEGIN CERTIFICATE-----",
			wantPath: "/etc/haproxy/certs/server.pem",
		},
		{
			name:     "register general file",
			fileType: "file",
			filename: "config.txt",
			content:  "key=value",
			wantPath: "/etc/haproxy/files/config.txt",
		},
		{
			name:     "register crt-list file",
			fileType: "crt-list",
			filename: "certificates.txt",
			content:  "/path/to/cert.pem\n/path/to/cert2.pem",
			wantPath: "/etc/haproxy/crt-list/certificates.txt",
		},
		{
			name:        "invalid file type",
			fileType:    "invalid",
			filename:    "test.txt",
			content:     "content",
			wantErr:     true,
			errContains: "invalid file type",
		},
		{
			name:        "wrong argument count",
			wantErr:     true,
			errContains: "requires 3 arguments",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := NewFileRegistry(pathResolver)

			var path string
			var err error

			if tt.name == "wrong argument count" {
				path, err = registry.Register("map", "test.map") // Only 2 args
			} else {
				path, err = registry.Register(tt.fileType, tt.filename, tt.content)
			}

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantPath, path)
		})
	}
}

func TestFileRegistry_Register_Idempotent(t *testing.T) {
	pathResolver := &templating.PathResolver{
		MapsDir: "/etc/haproxy/maps",
	}
	registry := NewFileRegistry(pathResolver)

	// First registration
	path1, err := registry.Register("map", "test.map", "content1")
	require.NoError(t, err)

	// Same content - should succeed and return same path
	path2, err := registry.Register("map", "test.map", "content1")
	require.NoError(t, err)
	assert.Equal(t, path1, path2)
}

func TestFileRegistry_Register_Conflict(t *testing.T) {
	pathResolver := &templating.PathResolver{
		MapsDir: "/etc/haproxy/maps",
	}
	registry := NewFileRegistry(pathResolver)

	// First registration
	_, err := registry.Register("map", "test.map", "content1")
	require.NoError(t, err)

	// Different content - should fail
	_, err = registry.Register("map", "test.map", "content2")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "content conflict")
}

func TestFileRegistry_GetFiles(t *testing.T) {
	pathResolver := &templating.PathResolver{
		MapsDir:    "/etc/haproxy/maps",
		SSLDir:     "/etc/haproxy/certs",
		CRTListDir: "/etc/haproxy/crt-list",
		GeneralDir: "/etc/haproxy/files",
	}
	registry := NewFileRegistry(pathResolver)

	// Register various file types
	_, _ = registry.Register("map", "hosts.map", "host content")
	_, _ = registry.Register("cert", "server.pem", "cert content")
	_, _ = registry.Register("file", "config.txt", "file content")
	_, _ = registry.Register("crt-list", "certs.txt", "crt-list content")

	files := registry.GetFiles()

	require.Len(t, files.MapFiles, 1)
	assert.Equal(t, "host content", files.MapFiles[0].Content)

	require.Len(t, files.SSLCertificates, 1)
	assert.Equal(t, "cert content", files.SSLCertificates[0].Content)

	// GeneralFiles includes only "file" type files
	require.Len(t, files.GeneralFiles, 1)
	assert.Equal(t, "file content", files.GeneralFiles[0].Content)
	assert.Equal(t, "config.txt", files.GeneralFiles[0].Filename)

	require.Len(t, files.CRTListFiles, 1)
	assert.Equal(t, "crt-list content", files.CRTListFiles[0].Content)
}

func TestMergeAuxiliaryFiles(t *testing.T) {
	tests := []struct {
		name    string
		static  *dataplane.AuxiliaryFiles
		dynamic *dataplane.AuxiliaryFiles
		want    *dataplane.AuxiliaryFiles
	}{
		{
			name:    "both nil",
			static:  nil,
			dynamic: nil,
			want:    &dataplane.AuxiliaryFiles{},
		},
		{
			name:   "static nil",
			static: nil,
			dynamic: &dataplane.AuxiliaryFiles{
				MapFiles: []auxiliaryfiles.MapFile{{Path: "/map1", Content: "c1"}},
			},
			want: &dataplane.AuxiliaryFiles{
				MapFiles: []auxiliaryfiles.MapFile{{Path: "/map1", Content: "c1"}},
			},
		},
		{
			name: "dynamic nil",
			static: &dataplane.AuxiliaryFiles{
				MapFiles: []auxiliaryfiles.MapFile{{Path: "/map1", Content: "c1"}},
			},
			dynamic: nil,
			want: &dataplane.AuxiliaryFiles{
				MapFiles: []auxiliaryfiles.MapFile{{Path: "/map1", Content: "c1"}},
			},
		},
		{
			name: "merge both",
			static: &dataplane.AuxiliaryFiles{
				MapFiles: []auxiliaryfiles.MapFile{{Path: "/static.map", Content: "s"}},
			},
			dynamic: &dataplane.AuxiliaryFiles{
				MapFiles: []auxiliaryfiles.MapFile{{Path: "/dynamic.map", Content: "d"}},
			},
			want: &dataplane.AuxiliaryFiles{
				MapFiles: []auxiliaryfiles.MapFile{
					{Path: "/static.map", Content: "s"},
					{Path: "/dynamic.map", Content: "d"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MergeAuxiliaryFiles(tt.static, tt.dynamic)
			assert.Equal(t, len(tt.want.MapFiles), len(got.MapFiles))
			if len(tt.want.MapFiles) > 0 {
				assert.Equal(t, tt.want.MapFiles[0].Path, got.MapFiles[0].Path)
			}
		})
	}
}
