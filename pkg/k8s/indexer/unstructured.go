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

// unstructuredInterface matches *unstructured.Unstructured without importing
// the apimachinery package. Both the JSONPath evaluator and the field filter
// need to reach the underlying data map to traverse or modify fields.
type unstructuredInterface interface {
	UnstructuredContent() map[string]any
}

// unwrapUnstructured returns the underlying data map when resource is an
// *unstructured.Unstructured, otherwise it returns the resource unchanged so
// callers can work with plain maps or typed objects uniformly.
func unwrapUnstructured(resource any) any {
	if u, ok := resource.(unstructuredInterface); ok {
		return u.UnstructuredContent()
	}
	return resource
}
