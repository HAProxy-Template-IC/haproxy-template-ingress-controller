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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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

// mockStoreWithItems returns unstructured items.
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
	// Create unstructured items
	item1 := &unstructured.Unstructured{}
	item1.SetName("item1")
	item1.SetNamespace("default")

	item2 := &unstructured.Unstructured{}
	item2.SetName("item2")
	item2.SetNamespace("default")

	store := &mockStoreWithItems{items: []interface{}{item1, item2}}
	wrapper := &StoreWrapper{
		Store:        store,
		ResourceType: "test",
		Logger:       testLogger(),
	}

	result := wrapper.List()
	require.Len(t, result, 2)

	// Verify items are unwrapped to maps
	m1, ok := result[0].(map[string]interface{})
	require.True(t, ok, "item should be unwrapped to map")
	assert.Equal(t, "item1", m1["metadata"].(map[string]interface{})["name"])
}

func TestStoreWrapper_List_Caching(t *testing.T) {
	store := &mockStoreWithItems{items: []interface{}{}}

	wrapper := &StoreWrapper{
		Store:        store,
		ResourceType: "test",
		Logger:       testLogger(),
	}

	// First call
	wrapper.List()
	assert.True(t, wrapper.ListCached)

	// Second call should use cache
	wrapper.List()

	// The ListCached flag confirms caching is active
	assert.True(t, wrapper.ListCached)
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
	item := &unstructured.Unstructured{}
	item.SetName("test-item")

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
	item := &unstructured.Unstructured{}
	item.SetName("single-item")

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
	item1 := &unstructured.Unstructured{}
	item1.SetName("item1")
	item2 := &unstructured.Unstructured{}
	item2.SetName("item2")

	store := &mockStoreWithItems{items: []interface{}{item1, item2}}
	wrapper := &StoreWrapper{
		Store:        store,
		ResourceType: "test",
		Logger:       testLogger(),
	}

	result := wrapper.GetSingle("default", "ambiguous")
	assert.Nil(t, result, "should return nil for ambiguous lookup")
}

func TestConvertFloatsToInts(t *testing.T) {
	tests := []struct {
		name  string
		input interface{}
		want  interface{}
	}{
		{
			name:  "float64 whole number",
			input: float64(80),
			want:  int64(80),
		},
		{
			name:  "float64 with fraction",
			input: float64(3.14),
			want:  float64(3.14),
		},
		{
			name:  "string unchanged",
			input: "hello",
			want:  "hello",
		},
		{
			name:  "int unchanged",
			input: 42,
			want:  42,
		},
		{
			name:  "nested map",
			input: map[string]interface{}{"port": float64(8080), "name": "web"},
			want:  map[string]interface{}{"port": int64(8080), "name": "web"},
		},
		{
			name:  "nested slice",
			input: []interface{}{float64(1), float64(2), float64(3)},
			want:  []interface{}{int64(1), int64(2), int64(3)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := convertFloatsToInts(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}
