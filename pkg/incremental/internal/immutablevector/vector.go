// Copyright 2026 Philipp Hossner
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

// Package immutablevector owns compact immutable slices behind authenticated roots.
package immutablevector

import (
	"errors"
	"fmt"
	"slices"
)

// Authority authenticates roots created for one owner.
type Authority[T any] struct {
	seal *Authority[T]
}

// Root is a copyable authenticated immutable vector.
type Root[T any] struct {
	state *state[T]
}

type state[T any] struct {
	seal      *state[T]
	authority *Authority[T]
	values    []T
}

// NewAuthority creates an isolated root authority.
func NewAuthority[T any]() *Authority[T] {
	authority := &Authority[T]{}
	authority.seal = authority
	return authority
}

// Own clones values and returns their authenticated immutable root.
func (a *Authority[T]) Own(values []T) (Root[T], error) {
	if !a.valid() {
		return Root[T]{}, errors.New("immutable vector authority has invalid provenance")
	}
	owned := &state[T]{authority: a, values: slices.Clone(values)}
	owned.seal = owned
	return Root[T]{state: owned}, nil
}

// ValidateOwnership verifies a root in O(1).
func (r Root[T]) ValidateOwnership(authority *Authority[T]) error {
	if !authority.valid() || r.state == nil || r.state.seal != r.state ||
		r.state.authority != authority {
		return errors.New("immutable vector root has invalid provenance")
	}
	return nil
}

// SameRoot compares exact authenticated storage identity in O(1).
func (r Root[T]) SameRoot(authority *Authority[T], other Root[T]) (bool, error) {
	if err := r.ValidateOwnership(authority); err != nil {
		return false, err
	}
	if err := other.ValidateOwnership(authority); err != nil {
		return false, err
	}
	return r.state == other.state, nil
}

// Len returns the immutable vector length.
func (r Root[T]) Len(authority *Authority[T]) (int, error) {
	if err := r.ValidateOwnership(authority); err != nil {
		return 0, err
	}
	return len(r.state.values), nil
}

// At returns one value without exposing the backing slice.
func (r Root[T]) At(authority *Authority[T], index int) (T, error) {
	var zero T
	if err := r.ValidateOwnership(authority); err != nil {
		return zero, err
	}
	if index < 0 || index >= len(r.state.values) {
		return zero, fmt.Errorf("immutable vector index %d is out of bounds", index)
	}
	return r.state.values[index], nil
}

// Values returns a detached copy of every value.
func (r Root[T]) Values(authority *Authority[T]) ([]T, error) {
	if err := r.ValidateOwnership(authority); err != nil {
		return nil, err
	}
	return slices.Clone(r.state.values), nil
}

// Range visits values in order until visit returns false.
func (r Root[T]) Range(authority *Authority[T], visit func(T) bool) error {
	if err := r.ValidateOwnership(authority); err != nil {
		return err
	}
	if visit == nil {
		return errors.New("immutable vector visitor is nil")
	}
	for _, value := range r.state.values {
		if !visit(value) {
			break
		}
	}
	return nil
}

func (a *Authority[T]) valid() bool {
	return a != nil && a.seal == a
}
