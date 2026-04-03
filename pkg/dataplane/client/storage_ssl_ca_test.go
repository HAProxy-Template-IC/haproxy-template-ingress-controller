package client

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sslCaStorageConfig() *storageTestConfig {
	return &storageTestConfig{
		endpoint:         "/services/haproxy/runtime/ssl_ca_files",
		itemNames:        []string{"ca-bundle.crt", "internal-ca.crt"},
		itemName:         "ca-bundle.crt",
		notFoundItemName: "missing.crt",
		content:          "-----BEGIN CERTIFICATE-----\nMIIB...\n-----END CERTIFICATE-----",
	}
}

func sslCaStorageFuncs() storageTestFuncs {
	return storageTestFuncs{
		getAll: func(ctx context.Context, c *DataplaneClient) ([]string, error) {
			return c.GetAllSSLCaFiles(ctx)
		},
		create: func(ctx context.Context, c *DataplaneClient, name, content string) error {
			_, err := c.CreateSSLCaFile(ctx, name, content)
			return err
		},
		update: func(ctx context.Context, c *DataplaneClient, name, content string) error {
			_, err := c.UpdateSSLCaFile(ctx, name, content)
			return err
		},
		delete: func(ctx context.Context, c *DataplaneClient, name string) error {
			return c.DeleteSSLCaFile(ctx, name)
		},
	}
}

func TestSSLCaFileStorage(t *testing.T) {
	runAllStorageCRUDTests(t, sslCaStorageConfig(), sslCaStorageFuncs())
}

func TestGetSSLCaFileContent_Success(t *testing.T) {
	cfg := sslCaStorageConfig()
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			cfg.endpoint + "/" + cfg.itemName: textResponse(cfg.content),
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	content, err := client.GetSSLCaFileContent(context.Background(), cfg.itemName)
	require.NoError(t, err)
	assert.Equal(t, cfg.content, content)
}

func TestGetSSLCaFileContent_NotFound(t *testing.T) {
	cfg := sslCaStorageConfig()
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			cfg.endpoint + "/missing.crt": errorResponse(http.StatusNotFound),
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	_, err := client.GetSSLCaFileContent(context.Background(), "missing.crt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestGetAllSSLCaFiles_NilStorageNames(t *testing.T) {
	cfg := sslCaStorageConfig()
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			cfg.endpoint: jsonResponse(`[
				{"storage_name": "valid.crt"},
				{"storage_name": null},
				{"file": "/etc/haproxy/ssl/ca/orphan.crt"}
			]`),
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	names, err := client.GetAllSSLCaFiles(context.Background())
	require.NoError(t, err)
	assert.Len(t, names, 1)
	assert.Equal(t, "valid.crt", names[0])
}
