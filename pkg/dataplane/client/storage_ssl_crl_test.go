package client

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sslCrlStorageConfig() *storageTestConfig {
	return &storageTestConfig{
		endpoint:         "/services/haproxy/runtime/ssl_crl_files",
		itemNames:        []string{"revoked.crl", "internal.crl"},
		itemName:         "revoked.crl",
		notFoundItemName: "missing.crl",
		content:          "-----BEGIN X509 CRL-----\nMIIB...\n-----END X509 CRL-----",
	}
}

func sslCrlStorageFuncs() storageTestFuncs {
	return storageTestFuncs{
		getAll: func(ctx context.Context, c *DataplaneClient) ([]string, error) {
			return c.GetAllSSLCrlFiles(ctx)
		},
		create: func(ctx context.Context, c *DataplaneClient, name, content string) error {
			_, err := c.CreateSSLCrlFile(ctx, name, content)
			return err
		},
		update: func(ctx context.Context, c *DataplaneClient, name, content string) error {
			_, err := c.UpdateSSLCrlFile(ctx, name, content)
			return err
		},
		delete: func(ctx context.Context, c *DataplaneClient, name string) error {
			return c.DeleteSSLCrlFile(ctx, name)
		},
	}
}

func TestSSLCrlFileStorage(t *testing.T) {
	runAllStorageCRUDTests(t, sslCrlStorageConfig(), sslCrlStorageFuncs())
}

func TestGetSSLCrlFileContent_Success(t *testing.T) {
	cfg := sslCrlStorageConfig()
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			cfg.endpoint + "/" + cfg.itemName: textResponse(cfg.content),
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	content, err := client.GetSSLCrlFileContent(context.Background(), cfg.itemName)
	require.NoError(t, err)
	assert.Equal(t, cfg.content, content)
}

func TestGetSSLCrlFileContent_NotFound(t *testing.T) {
	cfg := sslCrlStorageConfig()
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			cfg.endpoint + "/missing.crl": errorResponse(http.StatusNotFound),
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	_, err := client.GetSSLCrlFileContent(context.Background(), "missing.crl")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestGetAllSSLCrlFiles_NilStorageNames(t *testing.T) {
	cfg := sslCrlStorageConfig()
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			cfg.endpoint: jsonResponse(`[
				{"storage_name": "valid.crl"},
				{"storage_name": null},
				{"description": "no name"}
			]`),
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	names, err := client.GetAllSSLCrlFiles(context.Background())
	require.NoError(t, err)
	assert.Len(t, names, 1)
	assert.Equal(t, "valid.crl", names[0])
}
