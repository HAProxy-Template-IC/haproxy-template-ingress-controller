// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package store

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/typegen"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

// ImmutableSnapshotProjection exposes only detached projections of a built-in snapshot read.
type ImmutableSnapshotProjection struct {
	seal         *ImmutableSnapshotProjection
	items        []any
	encodedItems [][]byte
	encoded      bool
}

// ProjectImmutableSnapshotList prepares a built-in snapshot list without exposing its owned graph.
func ProjectImmutableSnapshotList(
	ctx context.Context,
	snapshot stores.ReadSnapshot,
) (*ImmutableSnapshotProjection, bool, error) {
	var (
		items []any
		err   error
	)
	switch typed := snapshot.(type) {
	case *memoryReadSnapshot:
		var encodedItems [][]byte
		var encoded bool
		items, encodedItems, encoded, err = typed.listImmutableProjection(ctx)
		if err == nil {
			return newEncodedImmutableSnapshotProjection(items, encodedItems, encoded), true, nil
		}
	case *cachedReadSnapshot:
		items, err = typed.listImmutable(ctx)
	default:
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	return newImmutableSnapshotProjection(items), true, nil
}

// ProjectImmutableSnapshotGet prepares a built-in keyed read without exposing its owned graph.
func ProjectImmutableSnapshotGet(
	ctx context.Context,
	snapshot stores.ReadSnapshot,
	keys ...string,
) (*ImmutableSnapshotProjection, bool, error) {
	var (
		items []any
		err   error
	)
	switch typed := snapshot.(type) {
	case *memoryReadSnapshot:
		var encodedItems [][]byte
		var encoded bool
		items, encodedItems, encoded, err = typed.getImmutableProjection(ctx, keys...)
		if err == nil {
			return newEncodedImmutableSnapshotProjection(items, encodedItems, encoded), true, nil
		}
	case *cachedReadSnapshot:
		items, err = typed.getImmutable(ctx, keys...)
	default:
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	return newImmutableSnapshotProjection(items), true, nil
}

func newImmutableSnapshotProjection(items []any) *ImmutableSnapshotProjection {
	return newEncodedImmutableSnapshotProjection(items, nil, false)
}

func newEncodedImmutableSnapshotProjection(
	items []any,
	encodedItems [][]byte,
	encoded bool,
) *ImmutableSnapshotProjection {
	projection := &ImmutableSnapshotProjection{
		items: items, encodedItems: encodedItems, encoded: encoded,
	}
	projection.seal = projection
	return projection
}

// Len returns the number of resources in the exact projection.
func (p *ImmutableSnapshotProjection) Len() int {
	if p == nil || p.seal != p {
		return -1
	}
	return len(p.items)
}

// Encode returns the exact JSON value without exposing the owned resource graph.
func (p *ImmutableSnapshotProjection) Encode() ([]byte, error) {
	if p == nil || p.seal != p {
		return nil, errors.New("immutable snapshot projection has invalid provenance")
	}
	if p.encoded {
		return encodeImmutableSnapshotItems(p.items, p.encodedItems)
	}
	encoded, err := typegen.MarshalImmutableJSON(p.items)
	if err != nil {
		return nil, fmt.Errorf("encoding immutable snapshot projection: %w", err)
	}
	return encoded, nil
}

func encodeImmutableSnapshotItems(items []any, encodedItems [][]byte) ([]byte, error) {
	if items == nil {
		return []byte("null"), nil
	}
	if len(items) != len(encodedItems) {
		return nil, errors.New("immutable snapshot projection has invalid encoding provenance")
	}
	size := 2
	if len(encodedItems) > 1 {
		size += len(encodedItems) - 1
	}
	for _, encoded := range encodedItems {
		if len(encoded) == 0 {
			return nil, errors.New("immutable snapshot projection has invalid encoding provenance")
		}
		size += len(encoded)
	}
	result := make([]byte, 0, size)
	result = append(result, '[')
	for index, encoded := range encodedItems {
		if index > 0 {
			result = append(result, ',')
		}
		result = append(result, encoded...)
	}
	result = append(result, ']')
	return result, nil
}

// ProjectItems returns fresh typed values, or detached untyped values when elementType is nil.
func (p *ImmutableSnapshotProjection) ProjectItems(elementType reflect.Type) ([]reflect.Value, error) {
	if p == nil || p.seal != p {
		return nil, errors.New("immutable snapshot projection has invalid provenance")
	}
	projected := make([]reflect.Value, len(p.items))
	for index, item := range p.items {
		if elementType == nil {
			detached, err := cloneMemorySnapshotValue(item)
			if err != nil {
				return nil, err
			}
			if detached != nil {
				projected[index] = reflect.ValueOf(detached)
			}
			continue
		}
		object, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("immutable snapshot item %d has type %T", index, item)
		}
		pointer, err := typegen.WrapImmutableIntoPointer(object, elementType)
		if err != nil {
			return nil, fmt.Errorf("projecting immutable snapshot item %d: %w", index, err)
		}
		projected[index] = pointer
	}
	return projected, nil
}
