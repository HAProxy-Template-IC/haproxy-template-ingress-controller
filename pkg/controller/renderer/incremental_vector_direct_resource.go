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

package renderer

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

type incrementalVectorDirectInvocationPhase uint64

const (
	incrementalVectorDirectInvocationOpen incrementalVectorDirectInvocationPhase = iota + 1
	incrementalVectorDirectInvocationReading
	incrementalVectorDirectInvocationDone
	incrementalVectorDirectInvocationPhaseBits     = 2
	incrementalVectorDirectInvocationPhaseMask     = 1<<incrementalVectorDirectInvocationPhaseBits - 1
	incrementalVectorDirectInvocationMaxGeneration = ^uint64(0) >> incrementalVectorDirectInvocationPhaseBits
)

type authenticatedIncrementalVectorDirectInvocation struct {
	execution  *incrementalVectorExecution
	item       *incrementalVectorItemState
	slot       int
	generation uint64
}

func (v *incrementalVectorResourceView) BeginDirectBoundStoreInvocation(
	ctx context.Context,
	lease templating.IncrementalResourceInvocationLease,
) (rendercontext.DirectBoundStoreInvocation, error) {
	if v == nil || v.seal != v || v.execution == nil {
		return rendercontext.DirectBoundStoreInvocation{}, errors.New(
			"incremental component vector resource view has invalid provenance",
		)
	}
	execution, ok := lease.(*incrementalVectorExecution)
	if !ok || execution != v.execution {
		return rendercontext.DirectBoundStoreInvocation{}, v.execution.recordViolation(errors.New(
			"incremental component vector resource view has no matching direct execution lease",
		))
	}
	if ctx == nil {
		return rendercontext.DirectBoundStoreInvocation{}, execution.recordViolation(errors.New(
			"direct resource capability has a nil incremental component vector context",
		))
	}
	index, token, err := v.resolveContext(ctx)
	if err != nil {
		return rendercontext.DirectBoundStoreInvocation{}, err
	}
	if cause := context.Cause(ctx); cause != nil {
		return rendercontext.DirectBoundStoreInvocation{}, execution.recordViolation(cause)
	}
	item := &execution.items[index]
	if item.token != token || !item.lease.valid() || item.lease.token != token {
		return rendercontext.DirectBoundStoreInvocation{}, execution.recordViolation(errors.New(
			"direct resource capability has an invalid incremental component vector item",
		))
	}
	generation, available := execution.nextDirectBoundStoreInvocationGeneration()
	if !available {
		return rendercontext.DirectBoundStoreInvocation{}, execution.recordViolation(errors.New(
			"direct resource capability exhausted its invocation generations",
		))
	}
	slot, available := execution.acquireDirectBoundStoreInvocation(generation)
	if !available {
		return rendercontext.DirectBoundStoreInvocation{}, execution.recordViolation(errors.New(
			"direct resource capability has 64 concurrent invocations",
		))
	}
	entered, err := execution.enterDirect(index, "resource capability")
	if err != nil {
		execution.releaseUnpublishedDirectBoundStoreInvocation(slot, generation)
		return rendercontext.DirectBoundStoreInvocation{}, err
	}
	if entered != item {
		execution.releaseUnpublishedDirectBoundStoreInvocation(slot, generation)
		execution.leaveDirect()
		return rendercontext.DirectBoundStoreInvocation{}, execution.recordViolation(errors.New(
			"direct resource capability entered another incremental component vector item",
		))
	}
	invocation, err := rendercontext.NewDirectBoundStoreInvocation(item.lease, slot, generation)
	if err != nil {
		execution.releaseUnpublishedDirectBoundStoreInvocation(slot, generation)
		execution.leaveDirect()
		return rendercontext.DirectBoundStoreInvocation{}, execution.recordViolation(err)
	}
	return invocation, nil
}

func (*incrementalVectorResourceView) NormalizeLookupKeySource(
	_ string,
	keys rendercontext.StoreLookupKeySource,
) ([]string, error) {
	canonical := make([]string, keys.Len())
	reflected, reflects := keys.(rendercontext.StoreLookupReflectKeySource)
	for index := range canonical {
		var key string
		var err error
		if reflects {
			key, err = templating.CanonicalIncrementalResourceValue(index, reflected.ReflectValue(index))
		} else {
			key, err = templating.CanonicalIncrementalResourceKey(index, keys.Value(index))
		}
		if err != nil {
			return nil, err
		}
		canonical[index] = key
	}
	return canonical, nil
}

func (v *incrementalVectorResourceView) EndDirectBoundStoreInvocation(
	invocation rendercontext.DirectBoundStoreInvocation,
) error {
	authenticated, err := v.authenticateDirectBoundStoreInvocation(invocation)
	if err != nil {
		return err
	}
	for {
		state := authenticated.execution.directInvocations[authenticated.slot].Load()
		if directInvocationGeneration(state) != authenticated.generation {
			return authenticated.execution.recordViolation(errors.New(
				"direct resource capability has a stale invocation",
			))
		}
		switch directInvocationPhase(state) {
		case incrementalVectorDirectInvocationOpen, incrementalVectorDirectInvocationDone:
			if !authenticated.execution.directInvocations[authenticated.slot].CompareAndSwap(state, 0) {
				continue
			}
			authenticated.execution.leaveDirect()
			return nil
		case incrementalVectorDirectInvocationReading:
			return authenticated.execution.recordViolation(errors.New(
				"direct resource capability invocation is still reading",
			))
		default:
			return authenticated.execution.recordViolation(errors.New(
				"direct resource capability invocation has an invalid state",
			))
		}
	}
}

func (v *incrementalVectorResourceView) ListDirectBound(
	ctx context.Context,
	invocation rendercontext.DirectBoundStoreInvocation,
	resourceType string,
	_ stores.Store,
) ([]any, error) {
	authenticated, err := v.beginDirectBoundStoreRead(ctx, invocation)
	if err != nil {
		return nil, err
	}
	defer authenticated.finishRead()
	return v.readActive(authenticated.item, &resourceInputSpec{
		resourceType: resourceType,
		scope:        resourceInputList,
	})
}

func (v *incrementalVectorResourceView) GetDirectBound(
	ctx context.Context,
	invocation rendercontext.DirectBoundStoreInvocation,
	resourceType string,
	_ stores.Store,
	keys ...string,
) ([]any, error) {
	authenticated, err := v.beginDirectBoundStoreRead(ctx, invocation)
	if err != nil {
		return nil, err
	}
	defer authenticated.finishRead()
	return v.readActive(authenticated.item, &resourceInputSpec{
		resourceType: resourceType,
		scope:        resourceInputGet,
		keys:         slices.Clone(keys),
	})
}

func (v *incrementalVectorResourceView) beginDirectBoundStoreRead(
	ctx context.Context,
	invocation rendercontext.DirectBoundStoreInvocation,
) (authenticatedIncrementalVectorDirectInvocation, error) {
	authenticated, err := v.authenticateDirectBoundStoreInvocation(invocation)
	if err != nil {
		return authenticatedIncrementalVectorDirectInvocation{}, err
	}
	if !authenticated.execution.directInvocations[authenticated.slot].CompareAndSwap(
		directInvocationState(authenticated.generation, incrementalVectorDirectInvocationOpen),
		directInvocationState(authenticated.generation, incrementalVectorDirectInvocationReading),
	) {
		return authenticatedIncrementalVectorDirectInvocation{}, authenticated.execution.recordViolation(errors.New(
			"direct resource capability invocation was already consumed",
		))
	}
	valid := false
	defer func() {
		if !valid {
			authenticated.finishRead()
		}
	}()
	if ctx == nil {
		return authenticatedIncrementalVectorDirectInvocation{}, authenticated.execution.recordViolation(errors.New(
			"direct resource capability has a nil incremental component vector context",
		))
	}
	token, _ := ctx.Value(incrementalVectorExecutionContextKey{}).(*incrementalVectorItemToken)
	if token != authenticated.item.token || !token.valid(authenticated.execution) ||
		(v.index >= 0 && v.index != token.index) {
		return authenticatedIncrementalVectorDirectInvocation{}, authenticated.execution.recordViolation(errors.New(
			"direct resource capability crossed an incremental component vector boundary",
		))
	}
	if err := authenticated.execution.validateActiveContext(
		ctx, token.index, "direct resource capability",
	); err != nil {
		return authenticatedIncrementalVectorDirectInvocation{}, err
	}
	valid = true
	return authenticated, nil
}

func (v *incrementalVectorResourceView) authenticateDirectBoundStoreInvocation(
	invocation rendercontext.DirectBoundStoreInvocation,
) (authenticatedIncrementalVectorDirectInvocation, error) {
	if v == nil || v.seal != v || v.execution == nil {
		return authenticatedIncrementalVectorDirectInvocation{}, errors.New(
			"incremental component vector resource view has invalid provenance",
		)
	}
	lease, ok := invocation.Lease().(*incrementalVectorItemLease)
	if !ok || !lease.valid() || lease.token.execution != v.execution ||
		v.execution.items[lease.token.index].lease != lease ||
		(v.index >= 0 && v.index != lease.token.index) {
		return authenticatedIncrementalVectorDirectInvocation{}, v.execution.recordViolation(errors.New(
			"direct resource capability has a foreign incremental component vector item",
		))
	}
	slot := invocation.Slot()
	generation := invocation.Generation()
	if slot < 0 || slot >= len(v.execution.directInvocations) || generation == 0 {
		return authenticatedIncrementalVectorDirectInvocation{}, v.execution.recordViolation(errors.New(
			"direct resource capability has a stale invocation",
		))
	}
	state := v.execution.directInvocations[slot].Load()
	phase := directInvocationPhase(state)
	if directInvocationGeneration(state) != generation ||
		phase < incrementalVectorDirectInvocationOpen || phase > incrementalVectorDirectInvocationDone {
		return authenticatedIncrementalVectorDirectInvocation{}, v.execution.recordViolation(errors.New(
			"direct resource capability has a stale invocation",
		))
	}
	return authenticatedIncrementalVectorDirectInvocation{
		execution:  v.execution,
		item:       &v.execution.items[lease.token.index],
		slot:       slot,
		generation: generation,
	}, nil
}

func (i authenticatedIncrementalVectorDirectInvocation) finishRead() {
	if i.execution == nil ||
		!i.execution.directInvocations[i.slot].CompareAndSwap(
			directInvocationState(i.generation, incrementalVectorDirectInvocationReading),
			directInvocationState(i.generation, incrementalVectorDirectInvocationDone),
		) {
		panic("finishing unauthenticated direct resource invocation read")
	}
}

func directInvocationState(
	generation uint64,
	phase incrementalVectorDirectInvocationPhase,
) uint64 {
	return generation<<incrementalVectorDirectInvocationPhaseBits | uint64(phase)
}

func directInvocationGeneration(state uint64) uint64 {
	return state >> incrementalVectorDirectInvocationPhaseBits
}

func directInvocationPhase(state uint64) incrementalVectorDirectInvocationPhase {
	return incrementalVectorDirectInvocationPhase(state & incrementalVectorDirectInvocationPhaseMask)
}

func (e *incrementalVectorExecution) nextDirectBoundStoreInvocationGeneration() (uint64, bool) {
	for {
		current := e.directInvocationSequence.Load()
		if current >= incrementalVectorDirectInvocationMaxGeneration {
			return 0, false
		}
		if e.directInvocationSequence.CompareAndSwap(current, current+1) {
			return current + 1, true
		}
	}
}

func (e *incrementalVectorExecution) acquireDirectBoundStoreInvocation(
	generation uint64,
) (int, bool) {
	for slot := range e.directInvocations {
		if e.directInvocations[slot].CompareAndSwap(
			0,
			directInvocationState(generation, incrementalVectorDirectInvocationOpen),
		) {
			return slot, true
		}
	}
	return 0, false
}

func (e *incrementalVectorExecution) releaseUnpublishedDirectBoundStoreInvocation(
	slot int,
	generation uint64,
) {
	if slot < 0 || slot >= len(e.directInvocations) || generation == 0 ||
		!e.directInvocations[slot].CompareAndSwap(
			directInvocationState(generation, incrementalVectorDirectInvocationOpen),
			0,
		) {
		panic(fmt.Sprintf(
			"releasing unpublished direct resource invocation slot %d generation %d",
			slot,
			generation,
		))
	}
}
