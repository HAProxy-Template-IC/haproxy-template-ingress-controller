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

package client

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// generalStorageConfig returns the test configuration for general storage tests.
func generalStorageConfig() *storageTestConfig {
	return &storageTestConfig{
		endpoint:         "/services/haproxy/storage/general",
		itemNames:        []string{"500.http", "503.http"},
		itemName:         "500.http",
		notFoundItemName: "nonexistent.http",
		content:          "HTTP/1.0 500 Internal Server Error\r\n\r\nError occurred",
	}
}

func generalStorageFuncs() storageTestFuncs {
	return storageTestFuncs{
		getAll: func(ctx context.Context, c *DataplaneClient) ([]string, error) {
			return c.GetAllGeneralFiles(ctx)
		},
		create: func(ctx context.Context, c *DataplaneClient, name, content string) error {
			_, err := c.CreateGeneralFile(ctx, name, content)
			return err
		},
		update: func(ctx context.Context, c *DataplaneClient, name, content string) error {
			_, err := c.UpdateGeneralFile(ctx, name, content)
			return err
		},
		delete: func(ctx context.Context, c *DataplaneClient, name string) error {
			return c.DeleteGeneralFile(ctx, name)
		},
	}
}

func TestGeneralFileStorage(t *testing.T) {
	runAllStorageCRUDTests(t, generalStorageConfig(), generalStorageFuncs())
}

func TestGetAllGeneralFiles_IdFallback(t *testing.T) {
	// Test that files with 'id' field but no 'storage_name' are still extracted
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/services/haproxy/storage/general": jsonResponse(`[
				{"storage_name": "500.http"},
				{"id": "maintenance.html"},
				{"description": "no name or id"}
			]`),
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	files, err := client.GetAllGeneralFiles(context.Background())
	require.NoError(t, err)
	// Only entries with storage_name or id should be returned
	assert.Len(t, files, 2)
	assert.Contains(t, files, "500.http")
	assert.Contains(t, files, "maintenance.html")
}

func TestGetGeneralFileContent_Success(t *testing.T) {
	expectedContent := "HTTP/1.0 500 Internal Server Error\r\n\r\nError occurred"

	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/services/haproxy/storage/general/500.http": textResponse(expectedContent),
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	content, err := client.GetGeneralFileContent(context.Background(), "500.http")
	require.NoError(t, err)
	assert.Equal(t, expectedContent, content)
}

func TestGetGeneralFileContent_NotFound(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			// No handler for the specific file path - will return 404
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	_, err := client.GetGeneralFileContent(context.Background(), "nonexistent.http")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}
