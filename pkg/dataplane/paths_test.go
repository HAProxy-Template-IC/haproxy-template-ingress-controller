package dataplane

import (
	"path/filepath"
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

	// ResolvePaths intentionally ignores the Capabilities argument today
	// (the second parameter is `_ Capabilities`): CRT-list files are
	// always stored under GeneralDir regardless of SupportsCrtList,
	// because the native CRT-list API triggers a reload on create
	// without a skip_reload parameter. We pin three representative
	// capability values to assert this is genuinely capability-invariant
	// — if a future change starts branching on capabilities, the assertion
	// pattern below stops being identical across cases and the test
	// signals which branch needs explicit coverage.
	for _, tt := range []struct {
		name         string
		capabilities Capabilities
	}{
		{"crt-list supported (v3.2+)", client.Capabilities{SupportsCrtList: true}},
		{"crt-list not supported (v3.0/v3.1)", client.Capabilities{SupportsCrtList: false}},
		{"empty capabilities", client.Capabilities{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			resolved := ResolvePaths(basePath, tt.capabilities)

			require.NotNil(t, resolved)
			assert.Equal(t, "/etc/haproxy/maps", resolved.MapsDir)
			assert.Equal(t, "/etc/haproxy/ssl", resolved.SSLDir)
			assert.Equal(t, "/etc/haproxy/files", resolved.GeneralDir)
			assert.Equal(t, "/etc/haproxy/haproxy.cfg", resolved.ConfigFile)
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
	// TempDir is derived from ConfigFile's parent directory via filepath.Dir,
	// which uses the host OS separator (backslashes on Windows).
	assert.Equal(t, filepath.FromSlash("/tmp/haproxy-validate-12345"), validationPaths.TempDir)
	assert.Equal(t, "/tmp/haproxy-validate-12345/maps", validationPaths.MapsDir)
	assert.Equal(t, "/tmp/haproxy-validate-12345/ssl", validationPaths.SSLCertsDir)
	assert.Equal(t, "/tmp/haproxy-validate-12345/crtlist", validationPaths.CRTListDir)
	assert.Equal(t, "/tmp/haproxy-validate-12345/general", validationPaths.GeneralStorageDir)
	assert.Equal(t, "/tmp/haproxy-validate-12345/haproxy.cfg", validationPaths.ConfigFile)
}
