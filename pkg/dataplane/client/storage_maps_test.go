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

// mapStorageConfig returns the test configuration for map storage tests.
func mapStorageConfig() storageTestConfig {
	return storageTestConfig{
		endpoint:  "/services/haproxy/storage/maps",
		itemNames: []string{"hosts.map", "backends.map"},
		itemName:  "hosts.map",
		content:   "example.com backend1\ntest.com backend2\n",
	}
}

func getAllMapFiles(ctx context.Context, c *DataplaneClient) ([]string, error) {
	return c.GetAllMapFiles(ctx)
}

func createMapFile(ctx context.Context, c *DataplaneClient, name, content string) error {
	return c.CreateMapFile(ctx, name, content)
}

func updateMapFile(ctx context.Context, c *DataplaneClient, name, content string) error {
	return c.UpdateMapFile(ctx, name, content)
}

func deleteMapFile(ctx context.Context, c *DataplaneClient, name string) error {
	return c.DeleteMapFile(ctx, name)
}

func TestGetAllMapFiles_Success(t *testing.T) {
	runGetAllStorageSuccessTest(t, mapStorageConfig(), getAllMapFiles)
}

func TestGetAllMapFiles_Empty(t *testing.T) {
	runGetAllStorageEmptyTest(t, mapStorageConfig(), getAllMapFiles)
}

func TestGetAllMapFiles_ServerError(t *testing.T) {
	runGetAllStorageServerErrorTest(t, mapStorageConfig(), getAllMapFiles)
}

func TestGetAllMapFiles_InvalidJSON(t *testing.T) {
	runGetAllStorageInvalidJSONTest(t, mapStorageConfig(), getAllMapFiles)
}

func TestGetMapFileContent_Success(t *testing.T) {
	expectedContent := "example.com backend1\ntest.com backend2\n"

	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/services/haproxy/storage/maps/hosts.map": textResponse(http.StatusOK, expectedContent),
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	content, err := client.GetMapFileContent(context.Background(), "hosts.map")
	require.NoError(t, err)
	assert.Equal(t, expectedContent, content)
}

func TestGetMapFileContent_NotFound(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/services/haproxy/storage/maps/missing.map": errorResponse(http.StatusNotFound),
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	_, err := client.GetMapFileContent(context.Background(), "missing.map")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestCreateMapFile_Success(t *testing.T) {
	runCreateStorageSuccessTest(t, mapStorageConfig(), createMapFile)
}

func TestCreateMapFile_AlreadyExists(t *testing.T) {
	runCreateStorageConflictTest(t, mapStorageConfig(), createMapFile)
}

func TestUpdateMapFile_Success(t *testing.T) {
	runUpdateStorageSuccessTest(t, mapStorageConfig(), updateMapFile)
}

func TestUpdateMapFile_NotFound(t *testing.T) {
	cfg := mapStorageConfig()
	cfg.itemName = "missing.map"
	runUpdateStorageNotFoundTest(t, cfg, updateMapFile)
}

func TestDeleteMapFile_Success(t *testing.T) {
	runDeleteStorageSuccessTest(t, mapStorageConfig(), deleteMapFile)
}

func TestDeleteMapFile_NotFound(t *testing.T) {
	cfg := mapStorageConfig()
	cfg.itemName = "missing.map"
	runDeleteStorageNotFoundTest(t, cfg, deleteMapFile)
}

func TestGetAllMapFiles_NilStorageNames(t *testing.T) {
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/services/haproxy/storage/maps": jsonResponse(`[
				{"storage_name": "valid.map"},
				{"storage_name": null},
				{"description": "no name"}
			]`),
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	maps, err := client.GetAllMapFiles(context.Background())
	require.NoError(t, err)
	// Only the valid entry should be returned
	assert.Len(t, maps, 1)
	assert.Equal(t, "valid.map", maps[0])
}
