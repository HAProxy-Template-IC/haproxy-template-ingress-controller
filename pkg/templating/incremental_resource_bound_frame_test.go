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

package templating

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/haproxy-haptic/scriggo/native"
)

type incrementalResourceBoundFrameLease struct {
	validations atomic.Int64
}

func (l *incrementalResourceBoundFrameLease) ValidateIncrementalResourceInvocation(context.Context) error {
	l.validations.Add(1)
	return errors.New("legacy validation was invoked")
}

type incrementalResourceBoundFrameAccess struct {
	env        native.Env
	variadic   []any
	result     reflect.Value
	generation uint64
}

func (a *incrementalResourceBoundFrameAccess) FunctionCallFrameValid(generation uint64) bool {
	return generation == a.generation
}

func (*incrementalResourceBoundFrameAccess) FunctionCallFrameReceiver(uint64) reflect.Value {
	return reflect.Value{}
}

func (a *incrementalResourceBoundFrameAccess) FunctionCallFrameEnv(uint64, int) native.Env {
	return a.env
}

func (*incrementalResourceBoundFrameAccess) FunctionCallFrameBool(uint64, int, int) bool {
	panic("unexpected bool frame access")
}

func (*incrementalResourceBoundFrameAccess) FunctionCallFrameInt(uint64, int, int) int64 {
	panic("unexpected int frame access")
}

func (*incrementalResourceBoundFrameAccess) FunctionCallFrameUint(uint64, int, int) uint64 {
	panic("unexpected uint frame access")
}

func (*incrementalResourceBoundFrameAccess) FunctionCallFrameFloat(uint64, int, int) float64 {
	panic("unexpected float frame access")
}

func (*incrementalResourceBoundFrameAccess) FunctionCallFrameString(uint64, int, int) string {
	panic("unexpected string frame access")
}

func (a *incrementalResourceBoundFrameAccess) FunctionCallFrameValue(
	_ uint64,
	_ int,
	variadic int,
	typ reflect.Type,
) reflect.Value {
	value := reflect.ValueOf(a.variadic[variadic])
	if value.Type().AssignableTo(typ) {
		return value
	}
	boxed := reflect.New(typ).Elem()
	boxed.Set(value)
	return boxed
}

func (*incrementalResourceBoundFrameAccess) FunctionCallFrameSliceLen(uint64, int) int {
	panic("unexpected slice frame access")
}

func (*incrementalResourceBoundFrameAccess) FunctionCallFrameSliceValue(
	uint64,
	int,
	int,
	reflect.Type,
) reflect.Value {
	panic("unexpected slice frame access")
}

func (*incrementalResourceBoundFrameAccess) FunctionCallFrameSetBool(uint64, int, bool) {
	panic("unexpected bool frame result")
}

func (*incrementalResourceBoundFrameAccess) FunctionCallFrameSetInt(uint64, int, int64) {
	panic("unexpected int frame result")
}

func (*incrementalResourceBoundFrameAccess) FunctionCallFrameSetUint(uint64, int, uint64) {
	panic("unexpected uint frame result")
}

func (*incrementalResourceBoundFrameAccess) FunctionCallFrameSetFloat(uint64, int, float64) {
	panic("unexpected float frame result")
}

func (*incrementalResourceBoundFrameAccess) FunctionCallFrameSetString(uint64, int, string) {
	panic("unexpected string frame result")
}

func (a *incrementalResourceBoundFrameAccess) FunctionCallFrameSetValue(
	_ uint64,
	_ int,
	value reflect.Value,
) {
	a.result = value
}

func TestIncrementalResourceBoundFrameReplacesLegacyValidationAndBaseFrame(t *testing.T) {
	callableType := reflect.TypeFor[func(native.Env, ...any) any]()
	var baseCalls atomic.Int64
	baseTrampoline := native.MakeFunctionTrampolineWithFrame(
		callableType,
		func([]reflect.Value) []reflect.Value {
			baseCalls.Add(1)
			return []reflect.Value{incrementalResourceBoundFrameResult("base")}
		},
		func(frame native.FunctionCallFrame) {
			baseCalls.Add(1)
			frame.SetResultValue(0, incrementalResourceBoundFrameResult("base"))
		},
	)
	base := &incrementalResourceBindingTestResources{
		Routes: &incrementalResourceBindingTestStore{
			GetSingle: baseTrampoline.Value().Interface().(func(native.Env, ...any) any),
		},
	}
	lease := &incrementalResourceBoundFrameLease{}
	var factoryCalls atomic.Int64
	var boundCalls atomic.Int64
	require.NoError(t, RegisterIncrementalResourceFunctionBindings(
		incrementalResourceBindingTestOwner(t),
		base,
		IncrementalResourceFunctionBinding{
			Trampoline: baseTrampoline,
			BoundFrameFactory: func(
				actual IncrementalResourceInvocationLease,
			) (*native.FunctionTrampoline, error) {
				factoryCalls.Add(1)
				if actual != lease {
					return nil, errors.New("wrong bound lease")
				}
				return native.MakeFunctionTrampolineWithFrame(
					callableType,
					func([]reflect.Value) []reflect.Value {
						boundCalls.Add(1)
						return []reflect.Value{incrementalResourceBoundFrameResult("bound-call")}
					},
					func(frame native.FunctionCallFrame) {
						boundCalls.Add(1)
						frame.SetResultValue(0, incrementalResourceBoundFrameResult("bound-frame"))
					},
				), nil
			},
		},
	))
	plan, err := newIncrementalResourceBindingPlan(
		reflect.TypeFor[*incrementalResourceBindingTestResources](),
		map[string]uint8{"Routes": incrementalResourceGetSingle},
	)
	require.NoError(t, err)
	bound, err := plan.bind(base, lease)
	require.NoError(t, err)
	trampolines := incrementalResourceNativeFunctionTrampolines(bound)
	require.Len(t, trampolines, 1)

	env := &incrementalResourceBindingTestEnv{ctx: t.Context()}
	access := &incrementalResourceBoundFrameAccess{
		env: env, variadic: []any{"default", "route"}, generation: 1,
	}
	frame := trampolines[0].NewFunctionCallFrame(access, access.generation, len(access.variadic))
	trampolines[0].CallFrame(frame)
	require.True(t, access.result.IsValid())
	assert.Equal(t, "bound-frame", access.result.Interface())
	assert.Equal(t, int64(1), factoryCalls.Load())
	assert.Equal(t, int64(1), boundCalls.Load())
	assert.Zero(t, baseCalls.Load())
	assert.Zero(t, lease.validations.Load())
	require.NoError(t, env.err)
}

func TestIncrementalResourceBoundFrameFactoryFailureIsFailClosed(t *testing.T) {
	callableType := reflect.TypeFor[func(native.Env, ...any) any]()
	baseTrampoline := native.MakeFunctionTrampoline(
		callableType,
		func([]reflect.Value) []reflect.Value {
			return []reflect.Value{incrementalResourceBoundFrameResult("base")}
		},
	)
	base := &incrementalResourceBindingTestResources{
		Routes: &incrementalResourceBindingTestStore{
			GetSingle: baseTrampoline.Value().Interface().(func(native.Env, ...any) any),
		},
	}
	require.NoError(t, RegisterIncrementalResourceFunctionBindings(
		incrementalResourceBindingTestOwner(t),
		base,
		IncrementalResourceFunctionBinding{
			Trampoline: baseTrampoline,
			BoundFrameFactory: func(
				IncrementalResourceInvocationLease,
			) (*native.FunctionTrampoline, error) {
				return nil, errors.New("bound factory rejected lease")
			},
		},
	))
	plan, err := newIncrementalResourceBindingPlan(
		reflect.TypeFor[*incrementalResourceBindingTestResources](),
		map[string]uint8{"Routes": incrementalResourceGetSingle},
	)
	require.NoError(t, err)
	_, err = plan.bind(base, &incrementalResourceBindingAcceptLease{validations: &atomic.Int64{}})
	require.ErrorContains(t, err, "bound factory rejected lease")
}

func TestIncrementalResourceBoundFrameRejectsInvalidFactoryResult(t *testing.T) {
	callableType := reflect.TypeFor[func(native.Env, ...any) any]()
	baseTrampoline := native.MakeFunctionTrampoline(
		callableType,
		func([]reflect.Value) []reflect.Value {
			return []reflect.Value{incrementalResourceBoundFrameResult("base")}
		},
	)
	base := &incrementalResourceBindingTestResources{
		Routes: &incrementalResourceBindingTestStore{
			GetSingle: baseTrampoline.Value().Interface().(func(native.Env, ...any) any),
		},
	}
	require.NoError(t, RegisterIncrementalResourceFunctionBindings(
		incrementalResourceBindingTestOwner(t),
		base,
		IncrementalResourceFunctionBinding{
			Trampoline: baseTrampoline,
			BoundFrameFactory: func(
				IncrementalResourceInvocationLease,
			) (*native.FunctionTrampoline, error) {
				return native.MakeFunctionTrampoline(callableType, func([]reflect.Value) []reflect.Value {
					return []reflect.Value{incrementalResourceBoundFrameResult("not-a-frame")}
				}), nil
			},
		},
	))
	plan, err := newIncrementalResourceBindingPlan(
		reflect.TypeFor[*incrementalResourceBindingTestResources](),
		map[string]uint8{"Routes": incrementalResourceGetSingle},
	)
	require.NoError(t, err)
	_, err = plan.bind(base, &incrementalResourceBindingAcceptLease{validations: &atomic.Int64{}})
	require.ErrorContains(t, err, "invalid trampoline")
}

func incrementalResourceBoundFrameResult(value any) reflect.Value {
	result := reflect.New(reflect.TypeFor[any]()).Elem()
	result.Set(reflect.ValueOf(value))
	return result
}
