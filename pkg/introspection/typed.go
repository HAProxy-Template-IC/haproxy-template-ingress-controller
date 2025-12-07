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

// TypedFunc is a generic adapter that wraps a typed function as a Var.
//
// This allows creating type-safe debug variables while still implementing
// the Var interface. The type parameter T specifies the return type of the
// underlying function.
//
// Example:
//
//	// Create a typed var that returns a specific type
//	getStats := func() (Stats, error) {
//	    return Stats{Count: 42, LastUpdate: time.Now()}, nil
//	}
//	statsVar := TypedFunc[Stats](getStats)
//	registry.Publish("stats", statsVar)
type TypedFunc[T any] func() (T, error)

// Get implements the Var interface by calling the underlying typed function.
// The typed result is returned as interface{} to satisfy the Var interface.
func (f TypedFunc[T]) Get() (interface{}, error) {
	return f()
}

// TypedGetter is a generic interface for typed variable access.
//
// Unlike Var which returns interface{}, TypedGetter returns a specific type.
// This is useful for internal code that knows the expected type.
type TypedGetter[T any] interface {
	GetTyped() (T, error)
}

// TypedVar wraps a TypedGetter to implement both Var and TypedGetter interfaces.
type TypedVar[T any] struct {
	getter func() (T, error)
}

// NewTypedVar creates a new TypedVar from a getter function.
func NewTypedVar[T any](getter func() (T, error)) *TypedVar[T] {
	return &TypedVar[T]{getter: getter}
}

// Get implements the Var interface.
func (v *TypedVar[T]) Get() (interface{}, error) {
	return v.getter()
}

// GetTyped returns the value with its concrete type.
func (v *TypedVar[T]) GetTyped() (T, error) {
	return v.getter()
}
