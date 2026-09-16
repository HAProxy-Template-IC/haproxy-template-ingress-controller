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

package helpers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestAsUnstructured_TypedNil(t *testing.T) {
	tests := []struct {
		name     string
		resource any
	}{
		{"typed nil pointer", (*unstructured.Unstructured)(nil)},
		{"untyped nil", nil},
		{"wrong type", "not a resource"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := AsUnstructured(tt.resource)
			assert.Error(t, err)
			assert.Nil(t, got)
		})
	}

	t.Run("a real resource still passes", func(t *testing.T) {
		want := &unstructured.Unstructured{Object: map[string]any{"kind": "X"}}
		got, err := AsUnstructured(want)
		assert.NoError(t, err)
		assert.Same(t, want, got)
	})
}

func TestAsUnstructured_TypedNilDoesNotPanicCaller(t *testing.T) {
	assert.NotPanics(t, func() {
		resource, err := AsUnstructured((*unstructured.Unstructured)(nil))
		if err != nil {
			return
		}
		_ = resource.GetName()
	}, "GetName on the asserted value must not dereference a nil receiver")
}
