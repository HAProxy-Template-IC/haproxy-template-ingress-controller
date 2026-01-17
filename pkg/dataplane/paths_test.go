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
		MapsDir:    "/tmp/haproxy-validate-12345/maps",
		SSLDir:     "/tmp/haproxy-validate-12345/ssl",
		CRTListDir: "/tmp/haproxy-validate-12345/crtlist",
		GeneralDir: "/tmp/haproxy-validate-12345/general",
		ConfigFile: "/tmp/haproxy-validate-12345/haproxy.cfg",
	}

	validationPaths := resolved.ToValidationPaths()

	require.NotNil(t, validationPaths)
	// TempDir is derived from ConfigFile's parent directory
	assert.Equal(t, "/tmp/haproxy-validate-12345", validationPaths.TempDir)
	assert.Equal(t, "/tmp/haproxy-validate-12345/maps", validationPaths.MapsDir)
	assert.Equal(t, "/tmp/haproxy-validate-12345/ssl", validationPaths.SSLCertsDir)
	assert.Equal(t, "/tmp/haproxy-validate-12345/crtlist", validationPaths.CRTListDir)
	assert.Equal(t, "/tmp/haproxy-validate-12345/general", validationPaths.GeneralStorageDir)
	assert.Equal(t, "/tmp/haproxy-validate-12345/haproxy.cfg", validationPaths.ConfigFile)
}
