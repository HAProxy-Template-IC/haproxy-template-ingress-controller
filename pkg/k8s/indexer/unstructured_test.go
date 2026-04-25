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

package indexer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestUnwrapUnstructured(t *testing.T) {
	innerMap := map[string]any{"apiVersion": "v1", "kind": "Pod"}
	wrapped := &unstructured.Unstructured{Object: innerMap}
	plainMap := map[string]any{"foo": "bar"}

	tests := []struct {
		name string
		in   any
		want any
	}{
		{name: "*unstructured.Unstructured returns inner map", in: wrapped, want: innerMap},
		{name: "plain map returned unchanged", in: plainMap, want: plainMap},
		{name: "nil returned unchanged", in: nil, want: nil},
		{name: "scalar returned unchanged", in: 42, want: 42},
		{name: "string returned unchanged", in: "abc", want: "abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := unwrapUnstructured(tt.in)
			assert.Equal(t, tt.want, got)
		})
	}
}
