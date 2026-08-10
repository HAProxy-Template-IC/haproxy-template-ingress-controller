package pluggablevalidator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pipeline"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
)

func TestBuildFiles(t *testing.T) {
	result := &pipeline.PipelineResult{
		HAProxyConfig: "global\n",
		AuxiliaryFiles: &dataplane.AuxiliaryFiles{
			GeneralFiles:    []auxiliaryfiles.GeneralFile{{Path: "/etc/haproxy/general/500.http", Content: "response"}},
			SSLCertificates: []auxiliaryfiles.SSLCertificate{{Path: "/etc/haproxy/ssl/example.pem", Content: "certificate"}},
			SSLCaFiles:      []auxiliaryfiles.SSLCaFile{{Path: "/etc/haproxy/ssl/ca.pem", Content: "ca"}},
			MapFiles:        []auxiliaryfiles.MapFile{{Path: "/etc/haproxy/maps/host.map", Content: "map"}},
			CRTListFiles:    []auxiliaryfiles.CRTListFile{{Path: "/etc/haproxy/crt-list.txt", Content: "list"}},
		},
	}

	files := buildFiles(result)

	require.Len(t, files, 6)
	assert.Equal(t, "/etc/haproxy/haproxy.cfg", files[0].Path)
	assert.Equal(t, "global\n", files[0].Content)
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.Path)
	}
	assert.Contains(t, paths, "/etc/haproxy/general/500.http")
	assert.Contains(t, paths, "/etc/haproxy/ssl/example.pem")
	assert.Contains(t, paths, "/etc/haproxy/ssl/ca.pem")
	assert.Contains(t, paths, "/etc/haproxy/maps/host.map")
	assert.Contains(t, paths, "/etc/haproxy/crt-list.txt")
}

func TestBuildFiles_NilAuxiliaryFiles(t *testing.T) {
	files := buildFiles(&pipeline.PipelineResult{HAProxyConfig: "global\n"})

	require.Len(t, files, 1)
	assert.Equal(t, "/etc/haproxy/haproxy.cfg", files[0].Path)
}

func TestFormatDiagnostic(t *testing.T) {
	tests := []struct {
		name string
		in   Diagnostic
		want string
	}{
		{name: "full location", in: Diagnostic{Path: "/file", Line: 4, Column: 2, Message: "bad token"}, want: "/file:4:2: bad token"},
		{name: "line only", in: Diagnostic{Path: "/file", Line: 4, Message: "bad token"}, want: "/file:4: bad token"},
		{name: "file only", in: Diagnostic{Path: "/file", Message: "bad token"}, want: "/file: bad token"},
		{name: "message only", in: Diagnostic{Message: "timeout"}, want: "timeout"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, formatDiagnostic(test.in))
		})
	}
}

func TestFormatDiagnostics(t *testing.T) {
	assert.Nil(t, formatDiagnostics(nil))
	assert.Equal(t, []string{"/file: first", "/file:2:3: second"}, formatDiagnostics([]Diagnostic{
		{Path: "/file", Message: "first"},
		{Path: "/file", Line: 2, Column: 3, Message: "second"},
	}))
	assert.Equal(t, "/file: first\n/file:2:3: second", formatErrorReason([]Diagnostic{
		{Path: "/file", Message: "first"},
		{Path: "/file", Line: 2, Column: 3, Message: "second"},
	}))
}
