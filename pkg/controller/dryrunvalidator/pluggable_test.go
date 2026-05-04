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

package dryrunvalidator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pipeline"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pluggablevalidator"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
)

func TestBuildPluggableFiles_ConfigOnly(t *testing.T) {
	result := &pipeline.PipelineResult{
		HAProxyConfig: "global\n    daemon\n",
	}

	files := buildPluggableFiles(result)

	require.Len(t, files, 1)
	assert.Equal(t, "/etc/haproxy/haproxy.cfg", files[0].Path)
	assert.Equal(t, "global\n    daemon\n", files[0].Content)
}

func TestBuildPluggableFiles_NilAuxiliaryFiles(t *testing.T) {
	// AuxiliaryFiles is nil — should not panic and should still emit
	// the haproxy.cfg entry.
	result := &pipeline.PipelineResult{
		HAProxyConfig:  "global\n",
		AuxiliaryFiles: nil,
	}

	files := buildPluggableFiles(result)

	require.Len(t, files, 1)
	assert.Equal(t, "/etc/haproxy/haproxy.cfg", files[0].Path)
}

func TestBuildPluggableFiles_AllAuxTypes(t *testing.T) {
	result := &pipeline.PipelineResult{
		HAProxyConfig: "global\n",
		AuxiliaryFiles: &dataplane.AuxiliaryFiles{
			GeneralFiles: []auxiliaryfiles.GeneralFile{
				{Filename: "500.http", Path: "/etc/haproxy/general/500.http", Content: "HTTP/1.0 500\n"},
			},
			SSLCertificates: []auxiliaryfiles.SSLCertificate{
				{Path: "/etc/haproxy/ssl/example.pem", Content: "-----BEGIN CERTIFICATE-----\n"},
			},
			SSLCaFiles: []auxiliaryfiles.SSLCaFile{
				{Path: "/etc/haproxy/ssl/ca/trusted.pem", Content: "-----BEGIN CERTIFICATE-----\n"},
			},
			MapFiles: []auxiliaryfiles.MapFile{
				{Path: "/etc/haproxy/maps/host.map", Content: "example.com api\n"},
			},
			CRTListFiles: []auxiliaryfiles.CRTListFile{
				{Path: "/etc/haproxy/crt-lists/list.txt", Content: "/etc/haproxy/ssl/example.pem\n"},
			},
		},
	}

	files := buildPluggableFiles(result)

	// Aux paths must be passed through verbatim (already absolute) — the
	// validator sees the same paths the running HAProxy will reference.
	require.Len(t, files, 6)
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	assert.Contains(t, paths, "/etc/haproxy/haproxy.cfg")
	assert.Contains(t, paths, "/etc/haproxy/general/500.http")
	assert.Contains(t, paths, "/etc/haproxy/ssl/example.pem")
	assert.Contains(t, paths, "/etc/haproxy/ssl/ca/trusted.pem")
	assert.Contains(t, paths, "/etc/haproxy/maps/host.map")
	assert.Contains(t, paths, "/etc/haproxy/crt-lists/list.txt")
}

func TestFormatDiagnostic(t *testing.T) {
	tests := []struct {
		name string
		in   pluggablevalidator.Diagnostic
		want string
	}{
		{
			name: "full location",
			in:   pluggablevalidator.Diagnostic{Path: "/etc/haproxy/haproxy.cfg", Line: 42, Column: 9, Message: "bad token"},
			want: "/etc/haproxy/haproxy.cfg:42:9: bad token",
		},
		{
			name: "line only",
			in:   pluggablevalidator.Diagnostic{Path: "/etc/haproxy/haproxy.cfg", Line: 42, Column: 0, Message: "bad token"},
			want: "/etc/haproxy/haproxy.cfg:42: bad token",
		},
		{
			name: "file-level",
			in:   pluggablevalidator.Diagnostic{Path: "/etc/haproxy/haproxy.cfg", Line: 0, Column: 0, Message: "missing import"},
			want: "/etc/haproxy/haproxy.cfg: missing import",
		},
		{
			name: "protocol-level (empty path)",
			in:   pluggablevalidator.Diagnostic{Path: "", Line: 0, Column: 0, Message: "validator timeout"},
			want: "validator timeout",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, formatDiagnostic(tt.in))
		})
	}
}

func TestFormatDiagnostics_EmptyReturnsNil(t *testing.T) {
	// Distinguish "no diagnostics" (nil) from "empty slice" — callers
	// pass the result straight to AdmissionResponse.Warnings, and a
	// nil slice marshals to omitted JSON whereas []string{} marshals
	// to "[]". The wire-protocol convention is omission.
	assert.Nil(t, formatDiagnostics(nil))
	assert.Nil(t, formatDiagnostics([]pluggablevalidator.Diagnostic{}))
}

func TestFormatErrorReason_JoinsWithNewline(t *testing.T) {
	diags := []pluggablevalidator.Diagnostic{
		{Path: "/etc/haproxy/haproxy.cfg", Line: 1, Column: 0, Message: "first error"},
		{Path: "/etc/haproxy/haproxy.cfg", Line: 5, Column: 12, Message: "second error"},
	}
	got := formatErrorReason(diags)
	assert.Equal(t,
		"/etc/haproxy/haproxy.cfg:1: first error\n/etc/haproxy/haproxy.cfg:5:12: second error",
		got,
	)
}
