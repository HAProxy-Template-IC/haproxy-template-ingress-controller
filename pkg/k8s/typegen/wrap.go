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
	"encoding/json"
	"fmt"
	"reflect"
)

// WrapInto converts an unstructured Kubernetes object (the
// map[string]any shape every watcher in pkg/k8s normalises to) into a
// [reflect.Value] of the generated type produced by [Converter.Convert]
// for the same resource's schema.
//
// The implementation round-trips through encoding/json on purpose:
// every property the [Converter] emits carries a `json:"<original>"`
// struct tag, so encoding/json's reflection-based unmarshaller
// understands exactly how to drive each field. The alternative — a
// hand-rolled reflect-based copier — would duplicate logic that the
// standard library has already battle-hardened (number parsing, slice
// growth, nested map handling, polymorphic [any] field passthrough).
// The round-trip cost is paid once per resource per snapshot load (the
// existing StoreWrapper caches snapshots per render), and on the same
// order as the unstructured-map walks the chart's dig() already does.
//
// On any unmarshal error WrapInto returns the zero reflect.Value and
// the error. Callers in the controller hot path should log-and-skip
// rather than fail the whole reconcile — a single malformed resource
// shouldn't take down the renderer.
func WrapInto(obj map[string]any, typ reflect.Type) (reflect.Value, error) {
	if typ == nil {
		return reflect.Value{}, fmt.Errorf("typegen: WrapInto called with nil target type")
	}

	raw, err := json.Marshal(obj)
	if err != nil {
		// json.Marshal on a map[string]any only fails when a value
		// implements a custom MarshalJSON that errors, or carries a
		// chan / func type. Unstructured K8s objects don't contain
		// either, so this branch is genuinely unexpected in
		// production — but worth surfacing rather than panicking.
		return reflect.Value{}, fmt.Errorf("typegen: marshal unstructured object: %w", err)
	}

	ptr := reflect.New(typ) // *T, addressable for Unmarshal
	if err := json.Unmarshal(raw, ptr.Interface()); err != nil {
		return reflect.Value{}, fmt.Errorf("typegen: unmarshal into generated type %s: %w", typ, err)
	}
	return ptr.Elem(), nil
}
