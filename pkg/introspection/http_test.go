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

package introspection

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteJSON(t *testing.T) {
	tests := []struct {
		name     string
		data     interface{}
		wantBody string
	}{
		{
			name:     "simple map",
			data:     map[string]string{"key": "value"},
			wantBody: `{"key":"value"}`,
		},
		{
			name:     "nested structure",
			data:     map[string]interface{}{"items": []int{1, 2, 3}},
			wantBody: `{"items":[1,2,3]}`,
		},
		{
			name:     "string",
			data:     "hello",
			wantBody: `"hello"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			WriteJSON(w, tt.data)

			assert.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
			assert.JSONEq(t, tt.wantBody, w.Body.String())
		})
	}
}

func TestWriteJSONWithStatus(t *testing.T) {
	w := httptest.NewRecorder()
	WriteJSONWithStatus(w, http.StatusCreated, map[string]string{"id": "123"})

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"id":"123"}`, w.Body.String())
}

func TestWriteJSONField(t *testing.T) {
	t.Run("empty field returns full object", func(t *testing.T) {
		w := httptest.NewRecorder()
		WriteJSONField(w, map[string]string{"a": "1", "b": "2"}, "")

		assert.Equal(t, http.StatusOK, w.Code)
		assert.JSONEq(t, `{"a":"1","b":"2"}`, w.Body.String())
	})

	t.Run("valid field selection", func(t *testing.T) {
		w := httptest.NewRecorder()
		WriteJSONField(w, map[string]interface{}{"name": "testname"}, "{.name}")

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "testname")
	})

	t.Run("invalid field syntax", func(t *testing.T) {
		w := httptest.NewRecorder()
		// Field must start with { to be valid JSONPath
		WriteJSONField(w, map[string]string{"a": "1"}, "{.nonexistent[}")

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "error")
	})
}

func TestWriteError(t *testing.T) {
	w := httptest.NewRecorder()
	WriteError(w, http.StatusNotFound, "resource not found")

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var response map[string]string
	err := json.NewDecoder(w.Body).Decode(&response)
	require.NoError(t, err)
	assert.Equal(t, "resource not found", response["error"])
}

func TestWriteText(t *testing.T) {
	w := httptest.NewRecorder()
	WriteText(w, "Hello, World!\n")

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "text/plain; charset=utf-8", w.Header().Get("Content-Type"))
	assert.Equal(t, "Hello, World!\n", w.Body.String())
}

func TestRequireGET(t *testing.T) {
	handler := requireGET(func(w http.ResponseWriter, r *http.Request) {
		WriteText(w, "OK")
	})

	tests := []struct {
		name       string
		method     string
		wantStatus int
	}{
		{"GET allowed", http.MethodGet, http.StatusOK},
		{"POST denied", http.MethodPost, http.StatusMethodNotAllowed},
		{"PUT denied", http.MethodPut, http.StatusMethodNotAllowed},
		{"DELETE denied", http.MethodDelete, http.StatusMethodNotAllowed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/test", nil)
			w := httptest.NewRecorder()
			handler(w, req)

			assert.Equal(t, tt.wantStatus, w.Code)
		})
	}
}
