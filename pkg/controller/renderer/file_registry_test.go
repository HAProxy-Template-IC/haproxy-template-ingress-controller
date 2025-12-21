package renderer

import (
	"testing"

	"haproxy-template-ic/pkg/templating"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileRegistry_Register_ValidTypes(t *testing.T) {
	pathResolver := &templating.PathResolver{
		MapsDir:    "/etc/haproxy/maps",
		GeneralDir: "/etc/haproxy/general",
		SSLDir:     "/etc/haproxy/ssl",
	}
	registry := NewFileRegistry(pathResolver)

	tests := []struct {
		name         string
		fileType     string
		filename     string
		content      string
		expectedPath string
	}{
		{
			name:         "cert file",
			fileType:     "cert",
			filename:     "ca.pem",
			content:      "-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----",
			expectedPath: "/etc/haproxy/ssl/ca.pem",
		},
		{
			name:         "map file",
			fileType:     "map",
			filename:     "domains.map",
			content:      "example.com backend1\n",
			expectedPath: "/etc/haproxy/maps/domains.map",
		},
		{
			name:         "general file",
			fileType:     "file",
			filename:     "500.http",
			content:      "HTTP/1.0 500 Internal Server Error\n",
			expectedPath: "/etc/haproxy/general/500.http",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, err := registry.Register(tt.fileType, tt.filename, tt.content)

			require.NoError(t, err)
			assert.Equal(t, tt.expectedPath, path)
		})
	}
}

func TestFileRegistry_Register_InvalidType(t *testing.T) {
	pathResolver := &templating.PathResolver{
		MapsDir:    "/etc/haproxy/maps",
		GeneralDir: "/etc/haproxy/general",
		SSLDir:     "/etc/haproxy/ssl",
	}
	registry := NewFileRegistry(pathResolver)

	_, err := registry.Register("invalid", "test.txt", "content")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid file type \"invalid\"")
}

func TestFileRegistry_Register_WrongArgumentCount(t *testing.T) {
	pathResolver := &templating.PathResolver{
		MapsDir:    "/etc/haproxy/maps",
		GeneralDir: "/etc/haproxy/general",
		SSLDir:     "/etc/haproxy/ssl",
	}
	registry := NewFileRegistry(pathResolver)

	tests := []struct {
		name string
		args []interface{}
	}{
		{
			name: "too few arguments",
			args: []interface{}{"cert", "ca.pem"},
		},
		{
			name: "too many arguments",
			args: []interface{}{"cert", "ca.pem", "content", "extra"},
		},
		{
			name: "no arguments",
			args: []interface{}{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := registry.Register(tt.args...)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "requires 3 arguments")
		})
	}
}

func TestFileRegistry_Register_WrongArgumentTypes(t *testing.T) {
	pathResolver := &templating.PathResolver{
		MapsDir:    "/etc/haproxy/maps",
		GeneralDir: "/etc/haproxy/general",
		SSLDir:     "/etc/haproxy/ssl",
	}
	registry := NewFileRegistry(pathResolver)

	tests := []struct {
		name  string
		args  []interface{}
		error string
	}{
		{
			name:  "type is not string",
			args:  []interface{}{123, "ca.pem", "content"},
			error: "type must be a string",
		},
		{
			name:  "filename is not string",
			args:  []interface{}{"cert", 123, "content"},
			error: "filename must be a string",
		},
		{
			name:  "content is not string",
			args:  []interface{}{"cert", "ca.pem", 123},
			error: "content must be a string",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := registry.Register(tt.args...)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.error)
		})
	}
}

func TestFileRegistry_Register_ContentConflict(t *testing.T) {
	pathResolver := &templating.PathResolver{
		MapsDir:    "/etc/haproxy/maps",
		GeneralDir: "/etc/haproxy/general",
		SSLDir:     "/etc/haproxy/ssl",
	}
	registry := NewFileRegistry(pathResolver)

	// Register file with initial content
	path1, err := registry.Register("cert", "ca.pem", "content1")
	require.NoError(t, err)
	assert.Equal(t, "/etc/haproxy/ssl/ca.pem", path1)

	// Try to register same file with different content - should error
	_, err = registry.Register("cert", "ca.pem", "content2")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "content conflict")
	assert.Contains(t, err.Error(), "ca.pem")
	assert.Contains(t, err.Error(), "already registered with different content")
}

func TestFileRegistry_Register_Idempotent(t *testing.T) {
	pathResolver := &templating.PathResolver{
		MapsDir:    "/etc/haproxy/maps",
		GeneralDir: "/etc/haproxy/general",
		SSLDir:     "/etc/haproxy/ssl",
	}
	registry := NewFileRegistry(pathResolver)

	content := "-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----"

	// Register file
	path1, err := registry.Register("cert", "ca.pem", content)
	require.NoError(t, err)
	assert.Equal(t, "/etc/haproxy/ssl/ca.pem", path1)

	// Register same file with same content - should succeed (idempotent)
	path2, err := registry.Register("cert", "ca.pem", content)
	require.NoError(t, err)
	assert.Equal(t, path1, path2)
}

func TestFileRegistry_GetFiles_Empty(t *testing.T) {
	pathResolver := &templating.PathResolver{
		MapsDir:    "/etc/haproxy/maps",
		GeneralDir: "/etc/haproxy/general",
		SSLDir:     "/etc/haproxy/ssl",
	}
	registry := NewFileRegistry(pathResolver)

	files := registry.GetFiles()

	assert.NotNil(t, files)
	assert.Empty(t, files.SSLCertificates)
	assert.Empty(t, files.MapFiles)
	assert.Empty(t, files.GeneralFiles)
}

func TestFileRegistry_GetFiles_SingleCert(t *testing.T) {
	pathResolver := &templating.PathResolver{
		MapsDir:    "/etc/haproxy/maps",
		GeneralDir: "/etc/haproxy/general",
		SSLDir:     "/etc/haproxy/ssl",
	}
	registry := NewFileRegistry(pathResolver)

	certContent := "-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----"

	_, err := registry.Register("cert", "ca.pem", certContent)
	require.NoError(t, err)

	files := registry.GetFiles()

	require.Len(t, files.SSLCertificates, 1)
	assert.Equal(t, "/etc/haproxy/ssl/ca.pem", files.SSLCertificates[0].Path)
	assert.Equal(t, certContent, files.SSLCertificates[0].Content)
	assert.Empty(t, files.MapFiles)
	assert.Empty(t, files.GeneralFiles)
}

func TestFileRegistry_GetFiles_AllTypes(t *testing.T) {
	pathResolver := &templating.PathResolver{
		MapsDir:    "/etc/haproxy/maps",
		GeneralDir: "/etc/haproxy/general",
		SSLDir:     "/etc/haproxy/ssl",
	}
	registry := NewFileRegistry(pathResolver)

	// Register one of each type
	certContent := "-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----"
	mapContent := "example.com backend1\n"
	fileContent := "HTTP/1.0 500 Internal Server Error\n"

	_, err := registry.Register("cert", "ca.pem", certContent)
	require.NoError(t, err)

	_, err = registry.Register("map", "domains.map", mapContent)
	require.NoError(t, err)

	_, err = registry.Register("file", "500.http", fileContent)
	require.NoError(t, err)

	files := registry.GetFiles()

	// Verify cert
	require.Len(t, files.SSLCertificates, 1)
	assert.Equal(t, "/etc/haproxy/ssl/ca.pem", files.SSLCertificates[0].Path)
	assert.Equal(t, certContent, files.SSLCertificates[0].Content)

	// Verify map
	require.Len(t, files.MapFiles, 1)
	assert.Equal(t, "/etc/haproxy/maps/domains.map", files.MapFiles[0].Path)
	assert.Equal(t, mapContent, files.MapFiles[0].Content)

	// Verify general file (uses filename, not full path)
	require.Len(t, files.GeneralFiles, 1)
	assert.Equal(t, "500.http", files.GeneralFiles[0].Filename)
	assert.Equal(t, fileContent, files.GeneralFiles[0].Content)
}

func TestFileRegistry_GetFiles_MultipleCerts(t *testing.T) {
	pathResolver := &templating.PathResolver{
		MapsDir:    "/etc/haproxy/maps",
		GeneralDir: "/etc/haproxy/general",
		SSLDir:     "/etc/haproxy/ssl",
	}
	registry := NewFileRegistry(pathResolver)

	// Register multiple certs
	cert1 := "-----BEGIN CERTIFICATE-----\ncert1\n-----END CERTIFICATE-----"
	cert2 := "-----BEGIN CERTIFICATE-----\ncert2\n-----END CERTIFICATE-----"

	_, err := registry.Register("cert", "ca1.pem", cert1)
	require.NoError(t, err)

	_, err = registry.Register("cert", "ca2.pem", cert2)
	require.NoError(t, err)

	files := registry.GetFiles()

	require.Len(t, files.SSLCertificates, 2)

	// Find certs by path (order not guaranteed)
	certMap := make(map[string]string)
	for _, cert := range files.SSLCertificates {
		certMap[cert.Path] = cert.Content
	}

	assert.Equal(t, cert1, certMap["/etc/haproxy/ssl/ca1.pem"])
	assert.Equal(t, cert2, certMap["/etc/haproxy/ssl/ca2.pem"])
}

func TestFileRegistry_PathResolutionMatchesGetPath(t *testing.T) {
	// This test verifies that FileRegistry.Register() uses the same path resolution
	// logic as the pathResolver.GetPath() method, ensuring consistency

	pathResolver := &templating.PathResolver{
		MapsDir:    "/etc/haproxy/maps",
		GeneralDir: "/etc/haproxy/general",
		SSLDir:     "/etc/haproxy/ssl",
	}
	registry := NewFileRegistry(pathResolver)

	tests := []struct {
		fileType string
		filename string
	}{
		{"cert", "ca.pem"},
		{"map", "domains.map"},
		{"file", "500.http"},
	}

	for _, tt := range tests {
		t.Run(tt.fileType+"/"+tt.filename, func(t *testing.T) {
			// Get path from registry
			registryPath, err := registry.Register(tt.fileType, tt.filename, "content")
			require.NoError(t, err)

			// Get path from path resolver directly (same logic as pathResolver.GetPath() method)
			resolverPath, err := pathResolver.GetPath(tt.filename, tt.fileType)
			require.NoError(t, err)

			// They should match
			assert.Equal(t, resolverPath, registryPath)
		})
	}
}
