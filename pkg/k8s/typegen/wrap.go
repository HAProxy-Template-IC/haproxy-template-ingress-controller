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

package typegen

import (
	"fmt"
	"reflect"

	k8sruntime "k8s.io/apimachinery/pkg/runtime"
)

// WrapInto converts an unstructured Kubernetes object (the
// map[string]any shape every watcher in pkg/k8s normalises to) into a
// [reflect.Value] of the generated type produced by [Converter.Convert]
// for the same resource's schema.
//
// The conversion is done by apimachinery's own reflect-based converter
// rather than a round-trip through encoding/json. Both are driven by the
// same `json:"<original>"` struct tags the [Converter] emits, so they
// produce identical values — pinned by TestWrapIntoConverterEquivalence,
// including the float-in-an-integer-field case where the two were
// expected to diverge and do not. Serialising only to parse the bytes
// straight back costs 69% more time and 63% more heap per resource
// (BenchmarkWrapInto_*), and this runs per resource per render with the
// result memoized for the render's duration, so it lands on peak memory.
//
// On any conversion error WrapInto returns the zero reflect.Value and
// the error. Callers in the controller hot path should log-and-skip
// rather than fail the whole reconcile — a single malformed resource
// shouldn't take down the renderer.
func WrapInto(obj map[string]any, typ reflect.Type) (reflect.Value, error) {
	if typ == nil {
		return reflect.Value{}, fmt.Errorf("typegen: WrapInto called with nil target type")
	}

	ptr := reflect.New(typ) // *T, addressable for the converter
	if err := k8sruntime.DefaultUnstructuredConverter.FromUnstructured(obj, ptr.Interface()); err != nil {
		return reflect.Value{}, fmt.Errorf("typegen: convert unstructured into generated type %s: %w", typ, err)
	}
	return ptr.Elem(), nil
}
