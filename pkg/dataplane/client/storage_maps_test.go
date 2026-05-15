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
func mapStorageConfig() *storageTestConfig {
	return &storageTestConfig{
		endpoint:         "/services/haproxy/storage/maps",
		itemNames:        []string{"hosts.map", "backends.map"},
		itemName:         "hosts.map",
		notFoundItemName: "missing.map",
		content:          "example.com backend1\ntest.com backend2\n",
	}
}

func mapStorageFuncs() storageTestFuncs {
	return storageTestFuncs{
		getAll: func(ctx context.Context, c *DataplaneClient) ([]string, error) {
			return c.GetAllMapFiles(ctx)
		},
		create: func(ctx context.Context, c *DataplaneClient, name, content string) error {
			_, err := c.CreateMapFile(ctx, name, content)
			return err
		},
		update: func(ctx context.Context, c *DataplaneClient, name, content string) error {
			_, err := c.UpdateMapFile(ctx, name, content)
			return err
		},
		delete: func(ctx context.Context, c *DataplaneClient, name string) error {
			return c.DeleteMapFile(ctx, name)
		},
	}
}

func TestMapFileStorage(t *testing.T) {
	runAllStorageCRUDTests(t, mapStorageConfig(), mapStorageFuncs())
}

func TestGetMapFileContent_Success(t *testing.T) {
	expectedContent := "example.com backend1\ntest.com backend2\n"

	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/services/haproxy/storage/maps/hosts.map": textResponse(expectedContent),
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

// TestUpdateMapFile_SendsSkipReload mirrors the spoe.conf test for map files.
// Same rationale: aux-file UPDATEs must not trigger the dataplane auto-reload
// because that reload runs against the current haproxy.cfg, which may
// reference content the new map no longer provides.
func TestUpdateMapFile_SendsSkipReload(t *testing.T) {
	var capturedQuery string
	server := newMockServer(t, mockServerConfig{
		handlers: map[string]http.HandlerFunc{
			"/services/haproxy/storage/maps/hosts.map": func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPut {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				capturedQuery = r.URL.RawQuery
				w.WriteHeader(http.StatusOK)
			},
		},
	})
	defer server.Close()

	client := newTestClient(t, server)

	_, err := client.UpdateMapFile(context.Background(), "hosts.map", "example.com backend1\n")
	require.NoError(t, err)

	assert.Contains(t, capturedQuery, "skip_reload=true",
		"UpdateMapFile must always send skip_reload=true; got query %q", capturedQuery)
}
