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

package resourceloader

import (
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// TestAssertUnstructured_TypedNil pins the boundary against a typed nil, which
// satisfies the type assertion but panics on the first method call (#140).
func TestAssertUnstructured_TypedNil(t *testing.T) {
	loader := NewBaseLoader(
		busevents.NewEventBus(1),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		"test", 1, &panickyProcessor{},
		events.EventTypeConfigResourceChanged,
	)

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
			got, ok := loader.AssertUnstructured("TestEvent", tt.resource)
			assert.False(t, ok, "a resource that cannot be dereferenced must not be reported usable")
			assert.Nil(t, got)
		})
	}

	t.Run("a real resource still passes", func(t *testing.T) {
		want := &unstructured.Unstructured{Object: map[string]any{"kind": "X"}}
		got, ok := loader.AssertUnstructured("TestEvent", want)
		assert.True(t, ok)
		assert.Same(t, want, got)
	})
}

// TestAssertUnstructured_TypedNilDoesNotPanicCaller demonstrates the actual
// crash: the caller's first method call on the asserted value.
func TestAssertUnstructured_TypedNilDoesNotPanicCaller(t *testing.T) {
	loader := NewBaseLoader(
		busevents.NewEventBus(1),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		"test", 1, &panickyProcessor{},
		events.EventTypeConfigResourceChanged,
	)

	assert.NotPanics(t, func() {
		resource, ok := loader.AssertUnstructured("TestEvent", (*unstructured.Unstructured)(nil))
		if !ok {
			return
		}
		_ = resource.GetName()
	}, "GetName on the asserted value must not dereference a nil receiver")
}
