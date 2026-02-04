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

package rendercontext

import (
	"errors"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockStoreWithError is a mock store that returns errors.
type mockStoreWithError struct {
	listErr error
	getErr  error
}

func (m *mockStoreWithError) List() ([]interface{}, error) {
	return nil, m.listErr
}

func (m *mockStoreWithError) Get(_ ...string) ([]interface{}, error) {
	return nil, m.getErr
}

func (m *mockStoreWithError) Add(_ interface{}, _ []string) error {
	return nil
}

func (m *mockStoreWithError) Update(_ interface{}, _ []string) error {
	return nil
}

func (m *mockStoreWithError) Delete(_ ...string) error {
	return nil
}

func (m *mockStoreWithError) Clear() error {
	return nil
}

// mockStoreWithItems returns pre-converted map items (as stores now contain).
type mockStoreWithItems struct {
	items []interface{}
}

func (m *mockStoreWithItems) List() ([]interface{}, error) {
	return m.items, nil
}

func (m *mockStoreWithItems) Get(keys ...string) ([]interface{}, error) {
	// Simple key matching for testing
	if len(keys) > 0 && len(m.items) > 0 {
		return m.items, nil
	}
	return nil, nil
}

func (m *mockStoreWithItems) Add(_ interface{}, _ []string) error {
	return nil
}

func (m *mockStoreWithItems) Update(_ interface{}, _ []string) error {
	return nil
}

func (m *mockStoreWithItems) Delete(_ ...string) error {
	return nil
}

func (m *mockStoreWithItems) Clear() error {
	return nil
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// createResourceMap creates a pre-converted resource map (as stores now contain).
func createResourceMap(name string) map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": "default",
		},
	}
}

func TestStoreWrapper_List_Empty(t *testing.T) {
	store := &mockStoreWithItems{items: []interface{}{}}
	wrapper := &StoreWrapper{
		Store:        store,
		ResourceType: "test",
		Logger:       testLogger(),
	}

	result := wrapper.List()
	assert.Empty(t, result)
}

func TestStoreWrapper_List_WithItems(t *testing.T) {
	// Create pre-converted resource maps (as stores now contain)
	item1 := createResourceMap("item1")
	item2 := createResourceMap("item2")

	store := &mockStoreWithItems{items: []interface{}{item1, item2}}
	wrapper := &StoreWrapper{
		Store:        store,
		ResourceType: "test",
		Logger:       testLogger(),
	}

	result := wrapper.List()
	require.Len(t, result, 2)

	// Items are already maps, returned as-is
	m1, ok := result[0].(map[string]interface{})
	require.True(t, ok, "item should be a map")
	assert.Equal(t, "item1", m1["metadata"].(map[string]interface{})["name"])
}

func TestStoreWrapper_List_Error(t *testing.T) {
	store := &mockStoreWithError{listErr: errors.New("list failed")}
	wrapper := &StoreWrapper{
		Store:        store,
		ResourceType: "test",
		Logger:       testLogger(),
	}

	result := wrapper.List()
	assert.Empty(t, result, "should return empty slice on error")
}

func TestStoreWrapper_Fetch(t *testing.T) {
	item := createResourceMap("test-item")

	store := &mockStoreWithItems{items: []interface{}{item}}
	wrapper := &StoreWrapper{
		Store:        store,
		ResourceType: "test",
		Logger:       testLogger(),
	}

	result := wrapper.Fetch("default", "test-item")
	require.Len(t, result, 1)
}

func TestStoreWrapper_Fetch_Error(t *testing.T) {
	store := &mockStoreWithError{getErr: errors.New("get failed")}
	wrapper := &StoreWrapper{
		Store:        store,
		ResourceType: "test",
		Logger:       testLogger(),
	}

	result := wrapper.Fetch("default", "test-item")
	assert.Empty(t, result, "should return empty slice on error")
}

func TestStoreWrapper_GetSingle(t *testing.T) {
	item := createResourceMap("single-item")

	store := &mockStoreWithItems{items: []interface{}{item}}
	wrapper := &StoreWrapper{
		Store:        store,
		ResourceType: "test",
		Logger:       testLogger(),
	}

	result := wrapper.GetSingle("default", "single-item")
	require.NotNil(t, result)

	m, ok := result.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "single-item", m["metadata"].(map[string]interface{})["name"])
}

func TestStoreWrapper_GetSingle_NotFound(t *testing.T) {
	store := &mockStoreWithItems{items: []interface{}{}}
	wrapper := &StoreWrapper{
		Store:        store,
		ResourceType: "test",
		Logger:       testLogger(),
	}

	result := wrapper.GetSingle("default", "missing")
	assert.Nil(t, result)
}

func TestStoreWrapper_GetSingle_Ambiguous(t *testing.T) {
	item1 := createResourceMap("item1")
	item2 := createResourceMap("item2")

	store := &mockStoreWithItems{items: []interface{}{item1, item2}}
	wrapper := &StoreWrapper{
		Store:        store,
		ResourceType: "test",
		Logger:       testLogger(),
	}

	result := wrapper.GetSingle("default", "ambiguous")
	assert.Nil(t, result, "should return nil for ambiguous lookup")
}
