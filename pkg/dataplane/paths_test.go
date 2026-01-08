package dataplane

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
)

func TestResolvePaths(t *testing.T) {
	basePath := PathConfig{
		MapsDir:    "/etc/haproxy/maps",
		SSLDir:     "/etc/haproxy/ssl",
		GeneralDir: "/etc/haproxy/files",
		ConfigFile: "/etc/haproxy/haproxy.cfg",
	}

	// CRTListDir is always GeneralDir because CRT-list files are stored as general files
	// to avoid reload on create (native CRT-list API doesn't support skip_reload).
	tests := []struct {
		name         string
		capabilities Capabilities
	}{
		{
			name: "crt-list supported (v3.2+)",
			capabilities: client.Capabilities{
				SupportsCrtList: true,
			},
		},
		{
			name: "crt-list not supported (v3.0/v3.1)",
			capabilities: client.Capabilities{
				SupportsCrtList: false,
			},
		},
		{
			name:         "empty capabilities",
			capabilities: client.Capabilities{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved := ResolvePaths(basePath, tt.capabilities)

			require.NotNil(t, resolved)
			assert.Equal(t, "/etc/haproxy/maps", resolved.MapsDir)
			assert.Equal(t, "/etc/haproxy/ssl", resolved.SSLDir)
			assert.Equal(t, "/etc/haproxy/files", resolved.GeneralDir)
			assert.Equal(t, "/etc/haproxy/haproxy.cfg", resolved.ConfigFile)
			// CRTListDir always equals GeneralDir to avoid reload on create
			assert.Equal(t, "/etc/haproxy/files", resolved.CRTListDir,
				"CRTListDir should always be GeneralDir regardless of capabilities")
		})
	}
}

func TestResolvedPaths_ToValidationPaths(t *testing.T) {
	resolved := &ResolvedPaths{
		MapsDir:    "/tmp/maps",
		SSLDir:     "/tmp/ssl",
		CRTListDir: "/tmp/crtlist",
		GeneralDir: "/tmp/general",
		ConfigFile: "/tmp/haproxy.cfg",
	}

	validationPaths := resolved.ToValidationPaths()

	require.NotNil(t, validationPaths)
	assert.Equal(t, "/tmp/maps", validationPaths.MapsDir)
	assert.Equal(t, "/tmp/ssl", validationPaths.SSLCertsDir)
	assert.Equal(t, "/tmp/crtlist", validationPaths.CRTListDir)
	assert.Equal(t, "/tmp/general", validationPaths.GeneralStorageDir)
	assert.Equal(t, "/tmp/haproxy.cfg", validationPaths.ConfigFile)
}
