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

package rendercontext

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// DirectBoundStoreInvocation authenticates one allocation-free bound store call.
type DirectBoundStoreInvocation struct {
	lease      templating.IncrementalResourceInvocationLease
	slot       uint8
	generation uint64
}

// NewDirectBoundStoreInvocation creates an exact invocation token.
func NewDirectBoundStoreInvocation(
	lease templating.IncrementalResourceInvocationLease,
	slot int,
	generation uint64,
) (DirectBoundStoreInvocation, error) {
	if lease == nil || slot < 0 || slot >= 64 || generation == 0 {
		return DirectBoundStoreInvocation{}, errors.New("direct bound store invocation has invalid provenance")
	}
	return DirectBoundStoreInvocation{
		lease: lease, slot: uint8(slot), generation: generation,
	}, nil
}

// Lease returns the exact item lease authenticated by the snapshot view.
func (i DirectBoundStoreInvocation) Lease() templating.IncrementalResourceInvocationLease {
	return i.lease
}

// Slot returns the preallocated execution slot held by this call.
func (i DirectBoundStoreInvocation) Slot() int {
	return int(i.slot)
}

// Generation returns the slot generation held by this call.
func (i DirectBoundStoreInvocation) Generation() uint64 {
	return i.generation
}

// DirectBoundStoreSnapshotView serves an authenticated call without a derived context.
type DirectBoundStoreSnapshotView interface {
	BeginDirectBoundStoreInvocation(
		context.Context,
		templating.IncrementalResourceInvocationLease,
	) (DirectBoundStoreInvocation, error)
	EndDirectBoundStoreInvocation(DirectBoundStoreInvocation) error
	ListDirectBound(
		context.Context,
		DirectBoundStoreInvocation,
		string,
		stores.Store,
	) ([]any, error)
	GetDirectBound(
		context.Context,
		DirectBoundStoreInvocation,
		string,
		stores.Store,
		...string,
	) ([]any, error)
}

// DirectBoundResourceMaterializationView returns a typed frame after exact observation.
type DirectBoundResourceMaterializationView interface {
	MaterializeDirectBoundResource(
		context.Context,
		DirectBoundStoreInvocation,
		*DirectBoundResourceMaterializationRequest,
		stores.Store,
		[]string,
	) (reflect.Value, error)
}

func (w *StoreWrapper) directBoundStoreSnapshotView() (DirectBoundStoreSnapshotView, bool) {
	if w == nil || w.memoizeStoreMaterialization() {
		return nil, false
	}
	view, ok := w.SnapshotView.(DirectBoundStoreSnapshotView)
	return view, ok
}

func (w *StoreWrapper) supportsDirectBoundStoreInvocation() bool {
	_, supported := w.directBoundStoreSnapshotView()
	return supported
}

func (w *StoreWrapper) directBoundResourceMaterializationView() (
	DirectBoundResourceMaterializationView,
	bool,
) {
	if w == nil || w.memoizeStoreMaterialization() {
		return nil, false
	}
	view, ok := w.SnapshotView.(DirectBoundResourceMaterializationView)
	return view, ok
}

func (w *StoreWrapper) materializeDirectBoundResource(
	ctx context.Context,
	invocation DirectBoundStoreInvocation,
	request *DirectBoundResourceMaterializationRequest,
	keys []string,
) (reflect.Value, bool, error) {
	view, supported := w.directBoundResourceMaterializationView()
	if !supported {
		return reflect.Value{}, false, nil
	}
	result, err := view.MaterializeDirectBoundResource(ctx, invocation, request, w.Store, keys)
	return result, true, err
}

func (w *StoreWrapper) beginDirectBoundStoreInvocation(
	ctx context.Context,
	lease templating.IncrementalResourceInvocationLease,
) (DirectBoundStoreInvocation, error) {
	if ctx == nil || lease == nil {
		return DirectBoundStoreInvocation{}, errors.New("direct bound resource invocation is unavailable")
	}
	view, supported := w.directBoundStoreSnapshotView()
	if !supported {
		return DirectBoundStoreInvocation{}, errors.New("direct bound resource invocation is unsupported")
	}
	return view.BeginDirectBoundStoreInvocation(ctx, lease)
}

func (w *StoreWrapper) endDirectBoundStoreInvocation(invocation DirectBoundStoreInvocation) error {
	view, supported := w.directBoundStoreSnapshotView()
	if !supported {
		return errors.New("direct bound resource invocation is unsupported")
	}
	return view.EndDirectBoundStoreInvocation(invocation)
}

func (w *StoreWrapper) listDirectBoundStoreInvocation(
	ctx context.Context,
	invocation DirectBoundStoreInvocation,
) ([]any, error) {
	view, supported := w.directBoundStoreSnapshotView()
	if !supported {
		return nil, errors.New("direct bound resource invocation is unsupported")
	}
	items, err := view.ListDirectBound(ctx, invocation, w.ResourceType, w.Store)
	if err != nil {
		return nil, err
	}
	return w.project(w.cloneStoreItems(items, "List"), "List"), nil
}

func (w *StoreWrapper) getDirectBoundStoreInvocation(
	ctx context.Context,
	invocation DirectBoundStoreInvocation,
	keys resourceInvocationKeys,
	operation string,
) ([]any, error) {
	stringKeys, ok := w.lookupKeySource(keys, operation)
	if !ok {
		return []any{}, nil
	}
	view, supported := w.directBoundStoreSnapshotView()
	if !supported {
		return nil, errors.New("direct bound resource invocation is unsupported")
	}
	items, err := view.GetDirectBound(ctx, invocation, w.ResourceType, w.Store, stringKeys...)
	if err != nil {
		return nil, err
	}
	return w.project(w.cloneStoreItems(items, operation), operation), nil
}

func (w *StoreWrapper) getSingleDirectBoundStoreInvocation(
	ctx context.Context,
	invocation DirectBoundStoreInvocation,
	keys resourceInvocationKeys,
) (item any, found bool, err error) {
	stringKeys, ok := w.lookupKeySource(keys, "GetSingle")
	if !ok {
		return nil, false, nil
	}
	view, supported := w.directBoundStoreSnapshotView()
	if !supported {
		return nil, false, errors.New("direct bound resource invocation is unsupported")
	}
	resolved, err := view.GetDirectBound(
		ctx,
		invocation,
		w.ResourceType,
		w.Store,
		stringKeys...,
	)
	if err != nil {
		return nil, false, err
	}
	items := w.project(w.cloneStoreItems(resolved, "GetSingle"), "GetSingle")
	if len(items) == 0 {
		return nil, false, nil
	}
	if len(items) > 1 {
		w.recordReadFailure(fmt.Errorf(
			"resource %q GetSingle lookup %q matched %d objects; use Fetch or configure unique indexBy values",
			w.ResourceType,
			stringKeys,
			len(items),
		))
		w.Logger.Error("GetSingle found multiple resources (ambiguous lookup)",
			"resource_type", w.ResourceType,
			"keys", stringKeys,
			"count", len(items))
		return nil, false, nil
	}
	return items[0], true, nil
}
