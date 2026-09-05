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
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"weak"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/haproxy-haptic/scriggo/native"
)

type incrementalResourceBindingTestStore struct {
	List      func(native.Env) []any
	Fetch     func(native.Env, ...any) []any
	GetSingle func(native.Env, ...any) any
}

type incrementalResourceBindingTestResources struct {
	Routes   *incrementalResourceBindingTestStore `json:"routes"`
	Services *incrementalResourceBindingTestStore `json:"services"`
}

type incrementalResourceBindingStaticStore struct {
	APIVersion func() string
	List       func(native.Env) []any
	Fetch      func(native.Env, ...any) []any
	GetSingle  func(native.Env, ...any) any
}

type incrementalResourceBindingStaticResources struct {
	Routes *incrementalResourceBindingStaticStore `json:"routes"`
}

type incrementalResourceBindingUnsupportedStore struct {
	List      func(native.Env) []any
	Fetch     func(native.Env, ...any) []any
	GetSingle func(native.Env, ...any) any
	Future    func(native.Env) any
}

type incrementalResourceBindingUnsupportedResources struct {
	Routes *incrementalResourceBindingUnsupportedStore `json:"routes"`
}

type incrementalResourceBindingPlanItem struct {
	Value string
}

type incrementalResourceBindingPlanStore struct {
	T          incrementalResourceBindingPlanItem
	APIVersion func() string
	List       func() []*incrementalResourceBindingPlanItem
	Fetch      func(...any) []*incrementalResourceBindingPlanItem
	GetSingle  func(...any) *incrementalResourceBindingPlanItem
}

type incrementalResourceBindingPlanResources struct {
	Routes   *incrementalResourceBindingPlanStore `json:"routes"`
	Services *incrementalResourceBindingPlanStore `json:"services"`
}

type incrementalResourceBindingTestLease struct {
	ctx         context.Context
	validations atomic.Int64
}

type incrementalResourceBindingAcceptLease struct {
	validations *atomic.Int64
}

func (lease *incrementalResourceBindingAcceptLease) ValidateIncrementalResourceInvocation(context.Context) error {
	lease.validations.Add(1)
	return nil
}

func (l *incrementalResourceBindingTestLease) ValidateIncrementalResourceInvocation(ctx context.Context) error {
	l.validations.Add(1)
	if ctx != l.ctx {
		return errors.New("wrong resource generation")
	}
	return nil
}

type incrementalResourceBindingTestEnv struct {
	ctx context.Context
	err error
}

func incrementalResourceBindingTestOwner(tb testing.TB) *IncrementalResourceFunctionBindingOwner {
	tb.Helper()
	owner := NewIncrementalResourceFunctionBindingOwner()
	tb.Cleanup(func() { runtime.KeepAlive(owner) })
	return owner
}

func (*incrementalResourceBindingTestEnv) CallPath() string                    { return "" }
func (*incrementalResourceBindingTestEnv) CallLine() int                       { return 0 }
func (e *incrementalResourceBindingTestEnv) Context() context.Context          { return e.ctx }
func (*incrementalResourceBindingTestEnv) Fatal(any)                           {}
func (*incrementalResourceBindingTestEnv) MarkdownConverter() native.Converter { return nil }
func (*incrementalResourceBindingTestEnv) Print(...any)                        {}
func (*incrementalResourceBindingTestEnv) Println(...any)                      {}
func (e *incrementalResourceBindingTestEnv) Stop(err error)                    { e.err = errors.Join(e.err, err) }
func (*incrementalResourceBindingTestEnv) TypeOf(value reflect.Value) reflect.Type {
	return value.Type()
}

func TestIncrementalResourceBindingBindsOnlyCertifiedReachableCallables(t *testing.T) {
	var calls atomic.Int64
	base := &incrementalResourceBindingTestResources{
		Routes: &incrementalResourceBindingTestStore{
			List:  func(native.Env) []any { return nil },
			Fetch: func(native.Env, ...any) []any { return nil },
			GetSingle: func(native.Env, ...any) any {
				calls.Add(1)
				return "route"
			},
		},
		Services: &incrementalResourceBindingTestStore{
			List:      func(native.Env) []any { return nil },
			Fetch:     func(native.Env, ...any) []any { return nil },
			GetSingle: func(native.Env, ...any) any { return nil },
		},
	}
	plan, err := newIncrementalResourceBindingPlan(
		reflect.TypeFor[*incrementalResourceBindingTestResources](),
		map[string]uint8{"Routes": incrementalResourceGetSingle},
	)
	require.NoError(t, err)
	ctxA := context.WithValue(t.Context(), struct{ name string }{"lease"}, "A")
	ctxB := context.WithValue(t.Context(), struct{ name string }{"lease"}, "B")
	lease := &incrementalResourceBindingTestLease{ctx: ctxA}
	boundValue, err := plan.bind(base, lease)
	require.NoError(t, err)
	bound := boundValue.(*incrementalResourceBindingTestResources)
	trampolines := incrementalResourceNativeFunctionTrampolines(bound)
	require.Len(t, trampolines, 1)
	byIdentity := incrementalResourceNativeFunctionTrampolinesByIdentity(bound)
	assert.Same(
		t,
		trampolines[0],
		byIdentity[incrementalResourceNativeFunctionIdentity(reflect.ValueOf(bound.Routes.GetSingle))],
	)
	foreign := &incrementalResourceBindingTestResources{Routes: bound.Routes}
	assert.Empty(t, incrementalResourceNativeFunctionTrampolines(foreign))

	assert.NotSame(t, base, bound)
	assert.NotSame(t, base.Routes, bound.Routes)
	assert.Nil(t, bound.Services)
	assert.Nil(t, bound.Routes.List)
	assert.Nil(t, bound.Routes.Fetch)
	envA := &incrementalResourceBindingTestEnv{ctx: ctxA}
	assert.Equal(t, "route", bound.Routes.GetSingle(envA))
	require.NoError(t, envA.err)
	envB := &incrementalResourceBindingTestEnv{ctx: ctxB}
	assert.Nil(t, bound.Routes.GetSingle(envB))
	assert.ErrorContains(t, envB.err, "wrong resource generation")
	assert.Equal(t, int64(1), calls.Load())
	assert.Equal(t, int64(2), lease.validations.Load())
}

func TestIncrementalResourceFunctionTrampolineRegistrationUsesExactOwner(t *testing.T) {
	base := &incrementalResourceBindingTestResources{
		Routes: &incrementalResourceBindingTestStore{},
	}
	trampoline := native.MakeFunctionTrampoline(
		reflect.TypeFor[func(native.Env, ...any) any](),
		func([]reflect.Value) []reflect.Value {
			result := reflect.New(reflect.TypeFor[any]()).Elem()
			result.Set(reflect.ValueOf("route"))
			return []reflect.Value{result}
		},
	)
	base.Routes.GetSingle = trampoline.Value().Interface().(func(native.Env, ...any) any)
	require.NoError(t, RegisterIncrementalResourceFunctionTrampolines(
		incrementalResourceBindingTestOwner(t), base, trampoline, trampoline,
	))

	registered := incrementalResourceNativeFunctionTrampolines(base)
	require.Len(t, registered, 1)
	assert.Same(t, trampoline, registered[0])
	byIdentity := incrementalResourceNativeFunctionTrampolinesByIdentity(base)
	assert.Same(t, trampoline, byIdentity[incrementalResourceNativeFunctionIdentity(
		reflect.ValueOf(base.Routes.GetSingle),
	)])

	copyOfBase := *base
	assert.Empty(t, incrementalResourceNativeFunctionTrampolines(&copyOfBase))
	require.Error(t, RegisterIncrementalResourceFunctionTrampolines(nil, nil, trampoline))
	require.Error(t, RegisterIncrementalResourceFunctionTrampolines(nil, base, nil))
}

func TestIncrementalResourceBindingDelegatesToRegisteredBaseTrampoline(t *testing.T) {
	var baseCalls atomic.Int64
	baseTrampoline := native.MakeFunctionTrampoline(
		reflect.TypeFor[func(native.Env, ...any) any](),
		func([]reflect.Value) []reflect.Value {
			baseCalls.Add(1)
			result := reflect.New(reflect.TypeFor[any]()).Elem()
			result.Set(reflect.ValueOf("route"))
			return []reflect.Value{result}
		},
	)
	base := &incrementalResourceBindingTestResources{
		Routes: &incrementalResourceBindingTestStore{
			GetSingle: baseTrampoline.Value().Interface().(func(native.Env, ...any) any),
		},
	}
	require.NoError(t, RegisterIncrementalResourceFunctionTrampolines(
		incrementalResourceBindingTestOwner(t), base, baseTrampoline,
	))
	plan, err := newIncrementalResourceBindingPlan(
		reflect.TypeFor[*incrementalResourceBindingTestResources](),
		map[string]uint8{"Routes": incrementalResourceGetSingle},
	)
	require.NoError(t, err)
	activeContext := context.WithValue(t.Context(), struct{ name string }{"lease"}, "active")
	boundValue, err := plan.bind(base, &incrementalResourceBindingTestLease{ctx: activeContext})
	require.NoError(t, err)
	bound := boundValue.(*incrementalResourceBindingTestResources)
	outerTrampolines := incrementalResourceNativeFunctionTrampolines(bound)
	require.Len(t, outerTrampolines, 1)

	activeEnv := &incrementalResourceBindingTestEnv{ctx: activeContext}
	args := []reflect.Value{
		reflect.ValueOf(activeEnv).Convert(reflect.TypeFor[native.Env]()),
		reflect.ValueOf([]any{"default", "route"}),
	}
	results := outerTrampolines[0].Call(args)
	require.Len(t, results, 1)
	assert.Equal(t, "route", results[0].Interface())
	require.NoError(t, activeEnv.err)
	assert.Equal(t, int64(1), baseCalls.Load())

	revokedEnv := &incrementalResourceBindingTestEnv{ctx: t.Context()}
	args[0] = reflect.ValueOf(revokedEnv).Convert(reflect.TypeFor[native.Env]())
	results = outerTrampolines[0].Call(args)
	assert.Nil(t, results[0].Interface())
	assert.ErrorContains(t, revokedEnv.err, "wrong resource generation")
	assert.Equal(t, int64(1), baseCalls.Load())
}

type incrementalResourceBindingRetentionSentinel struct {
	payload [1024]byte
}

func newIncrementalResourceBindingRetentionFacade(
	tb testing.TB,
) (
	facade *incrementalResourceBindingTestResources,
	ownerRef weak.Pointer[IncrementalResourceFunctionBindingOwner],
	sentinelRef weak.Pointer[incrementalResourceBindingRetentionSentinel],
	key incrementalResourceNativeFunctionKey,
) {
	tb.Helper()
	owner := NewIncrementalResourceFunctionBindingOwner()
	sentinel := &incrementalResourceBindingRetentionSentinel{payload: [1024]byte{1}}
	trampoline := native.MakeFunctionTrampoline(
		reflect.TypeFor[func(native.Env, ...any) any](),
		func([]reflect.Value) []reflect.Value {
			runtime.KeepAlive(owner)
			return []reflect.Value{reflect.Zero(reflect.TypeFor[any]())}
		},
	)
	resources := &incrementalResourceBindingTestResources{
		Routes: &incrementalResourceBindingTestStore{
			GetSingle: trampoline.Value().Interface().(func(native.Env, ...any) any),
		},
	}
	require.NoError(tb, RegisterIncrementalResourceFunctionBindings(
		owner,
		resources,
		IncrementalResourceFunctionBinding{
			Trampoline: trampoline,
			BoundFrameFactory: func(
				IncrementalResourceInvocationLease,
			) (*native.FunctionTrampoline, error) {
				runtime.KeepAlive(sentinel)
				return trampoline, nil
			},
		},
	))
	key, valid := incrementalResourceNativeFunctionOwnerKey(resources)
	require.True(tb, valid)
	return resources, weak.Make(owner), weak.Make(sentinel), key
}

func incrementalResourceBindingRegistrySize() int {
	size := 0
	incrementalResourceNativeFunctionRegistry.Range(func(any, any) bool {
		size++
		return true
	})
	return size
}

func requireIncrementalResourceBindingRegistryKeysAbsent(
	tb testing.TB,
	keys []incrementalResourceNativeFunctionKey,
) {
	tb.Helper()
	for range 20 {
		for range 100 {
			allAbsent := true
			for _, key := range keys {
				if _, found := incrementalResourceNativeFunctionRegistry.Load(key); found {
					allAbsent = false
					break
				}
			}
			if allAbsent {
				return
			}
			runtime.Gosched()
		}
		runtime.GC()
	}
	require.Fail(tb, "incremental resource registry cleanup did not complete")
}

func TestIncrementalResourceFunctionBindingOwnerRetainsFactoryWhileFacadeIsReachable(t *testing.T) {
	resources, owner, sentinel, _ := newIncrementalResourceBindingRetentionFacade(t)
	runtime.GC()

	require.NotNil(t, owner.Value())
	require.NotNil(t, sentinel.Value())
	require.Len(t, incrementalResourceNativeFunctionBindings(resources), 1)
	runtime.KeepAlive(resources)
}

func TestIncrementalResourceFunctionBindingOwnerReleasesFactoryAfterOneGC(t *testing.T) {
	owner, sentinel, key := func() (
		weak.Pointer[IncrementalResourceFunctionBindingOwner],
		weak.Pointer[incrementalResourceBindingRetentionSentinel],
		incrementalResourceNativeFunctionKey,
	) {
		resources, owner, sentinel, key := newIncrementalResourceBindingRetentionFacade(t)
		runtime.KeepAlive(resources)
		return owner, sentinel, key
	}()

	runtime.GC()
	require.Nil(t, owner.Value())
	require.Nil(t, sentinel.Value())
	requireIncrementalResourceBindingRegistryKeysAbsent(t, []incrementalResourceNativeFunctionKey{key})
}

func TestIncrementalResourceBoundFacadeRetainsItsRegistrationAcrossGC(t *testing.T) {
	callableType := reflect.TypeFor[func(native.Env, ...any) any]()
	bound, owner := func() (
		*incrementalResourceBindingTestResources,
		weak.Pointer[IncrementalResourceFunctionBindingOwner],
	) {
		baseOwner := NewIncrementalResourceFunctionBindingOwner()
		baseTrampoline := native.MakeFunctionTrampoline(
			callableType,
			func([]reflect.Value) []reflect.Value {
				runtime.KeepAlive(baseOwner)
				result := reflect.New(reflect.TypeFor[any]()).Elem()
				result.Set(reflect.ValueOf("base"))
				return []reflect.Value{result}
			},
		)
		base := &incrementalResourceBindingTestResources{
			Routes: &incrementalResourceBindingTestStore{
				GetSingle: baseTrampoline.Value().Interface().(func(native.Env, ...any) any),
			},
		}
		require.NoError(t, RegisterIncrementalResourceFunctionBindings(
			baseOwner,
			base,
			IncrementalResourceFunctionBinding{
				Trampoline: baseTrampoline,
				BoundFrameFactory: func(
					IncrementalResourceInvocationLease,
				) (*native.FunctionTrampoline, error) {
					return native.MakeFunctionTrampolineWithFrame(
						callableType,
						func([]reflect.Value) []reflect.Value {
							result := reflect.New(reflect.TypeFor[any]()).Elem()
							result.Set(reflect.ValueOf("bound"))
							return []reflect.Value{result}
						},
						func(frame native.FunctionCallFrame) {
							result := reflect.New(reflect.TypeFor[any]()).Elem()
							result.Set(reflect.ValueOf("bound"))
							frame.SetResultValue(0, result)
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
		boundValue, err := plan.bind(base, &incrementalResourceBindingAcceptLease{
			validations: &atomic.Int64{},
		})
		require.NoError(t, err)
		bound := boundValue.(*incrementalResourceBindingTestResources)
		_, boundOwner := incrementalResourceNativeFunctionEntryFor(bound)
		require.NotNil(t, boundOwner)
		return bound, weak.Make(boundOwner)
	}()

	runtime.GC()
	require.NotNil(t, owner.Value())
	require.Len(t, incrementalResourceNativeFunctionTrampolines(bound), 1)
	assert.Equal(t, "bound", bound.Routes.GetSingle(&incrementalResourceBindingTestEnv{ctx: t.Context()}))
	runtime.KeepAlive(bound)
}

func TestIncrementalResourceStaticOnlyBoundFacadeRetainsItsRegistrationAcrossGC(t *testing.T) {
	bound, owner := func() (
		*incrementalResourceBindingStaticResources,
		weak.Pointer[IncrementalResourceFunctionBindingOwner],
	) {
		baseOwner := NewIncrementalResourceFunctionBindingOwner()
		apiVersionTrampoline := native.MakeFunctionTrampoline(
			reflect.TypeFor[func() string](),
			func([]reflect.Value) []reflect.Value {
				runtime.KeepAlive(baseOwner)
				return []reflect.Value{reflect.ValueOf("gateway.networking.k8s.io/v1")}
			},
		)
		base := &incrementalResourceBindingStaticResources{
			Routes: &incrementalResourceBindingStaticStore{
				APIVersion: apiVersionTrampoline.Value().Interface().(func() string),
			},
		}
		require.NoError(t, RegisterIncrementalResourceFunctionTrampolines(
			baseOwner,
			base,
			apiVersionTrampoline,
		))
		plan, err := newIncrementalResourceBindingPlan(
			reflect.TypeFor[*incrementalResourceBindingStaticResources](),
			map[string]uint8{"Routes": incrementalResourceStatic},
		)
		require.NoError(t, err)
		boundValue, err := plan.bind(base, &incrementalResourceBindingAcceptLease{
			validations: &atomic.Int64{},
		})
		require.NoError(t, err)
		bound := boundValue.(*incrementalResourceBindingStaticResources)
		_, boundOwner := incrementalResourceNativeFunctionEntryFor(bound)
		require.NotNil(t, boundOwner)
		return bound, weak.Make(boundOwner)
	}()

	runtime.GC()
	require.NotNil(t, owner.Value())
	require.Len(t, incrementalResourceNativeFunctionTrampolines(bound), 1)
	assert.Equal(t, "gateway.networking.k8s.io/v1", bound.Routes.APIVersion())
	runtime.KeepAlive(bound)
}

func TestIncrementalResourceFunctionBindingRegistryChurnIsBounded(t *testing.T) {
	runtime.GC()
	baseline := incrementalResourceBindingRegistrySize()
	for range 8 {
		owners := make([]weak.Pointer[IncrementalResourceFunctionBindingOwner], 0, 128)
		sentinels := make([]weak.Pointer[incrementalResourceBindingRetentionSentinel], 0, 128)
		keys := make([]incrementalResourceNativeFunctionKey, 0, 128)
		for range 128 {
			owner, sentinel, key := func() (
				weak.Pointer[IncrementalResourceFunctionBindingOwner],
				weak.Pointer[incrementalResourceBindingRetentionSentinel],
				incrementalResourceNativeFunctionKey,
			) {
				resources, owner, sentinel, key := newIncrementalResourceBindingRetentionFacade(t)
				runtime.KeepAlive(resources)
				return owner, sentinel, key
			}()
			owners = append(owners, owner)
			sentinels = append(sentinels, sentinel)
			keys = append(keys, key)
		}

		runtime.GC()
		for _, owner := range owners {
			require.Nil(t, owner.Value())
		}
		for _, sentinel := range sentinels {
			require.Nil(t, sentinel.Value())
		}
		requireIncrementalResourceBindingRegistryKeysAbsent(t, keys)
		require.LessOrEqual(t, incrementalResourceBindingRegistrySize(), baseline)
	}
}

func TestIncrementalResourceFunctionBindingRegistryCleanupDoesNotPoisonReplacement(t *testing.T) {
	resources := &incrementalResourceBindingTestResources{
		Routes: &incrementalResourceBindingTestStore{},
	}
	trampoline := native.MakeFunctionTrampoline(
		reflect.TypeFor[func(native.Env, ...any) any](),
		func([]reflect.Value) []reflect.Value {
			return []reflect.Value{reflect.Zero(reflect.TypeFor[any]())}
		},
	)
	resources.Routes.GetSingle = trampoline.Value().Interface().(func(native.Env, ...any) any)
	key, valid := incrementalResourceNativeFunctionOwnerKey(resources)
	require.True(t, valid)

	owners := make([]*IncrementalResourceFunctionBindingOwner, 0, 257)
	references := make([]*incrementalResourceNativeFunctionReference, 0, 257)
	var cleanups sync.WaitGroup
	for range 257 {
		owner := NewIncrementalResourceFunctionBindingOwner()
		require.NoError(t, RegisterIncrementalResourceFunctionTrampolines(owner, resources, trampoline))
		registered, found := incrementalResourceNativeFunctionRegistry.Load(key)
		require.True(t, found)
		reference := registered.(*incrementalResourceNativeFunctionReference)
		owners = append(owners, owner)
		references = append(references, reference)
		if len(references) > 1 {
			stale := references[len(references)-2]
			cleanups.Add(2)
			go func() {
				defer cleanups.Done()
				cleanupIncrementalResourceFunctionBindings(&incrementalResourceFunctionBindingCleanup{
					key: key, reference: stale,
				})
			}()
			go func() {
				defer cleanups.Done()
				_ = incrementalResourceNativeFunctionBindings(resources)
			}()
		}
	}
	cleanups.Wait()

	registered, found := incrementalResourceNativeFunctionRegistry.Load(key)
	require.True(t, found)
	assert.Same(t, references[len(references)-1], registered)
	require.Len(t, incrementalResourceNativeFunctionBindings(resources), 1)
	runtime.KeepAlive(owners)
	runtime.KeepAlive(resources)
}

func TestIncrementalVectorPreflightCollectsExactResourceTrampolines(t *testing.T) {
	declaration := (*incrementalResourceBindingTestResources)(nil)
	RegisterIncrementalResourceDeclaration(declaration)
	engine, err := New(map[string]string{
		"component": `{{ tostring(resources.routes.GetSingle()) }}`,
	}, &Options{
		EntryPoints:            []string{"component"},
		IncrementalEntryPoints: []string{"component"},
		Declarations:           map[string]any{"resources": declaration},
	})
	require.NoError(t, err)
	var validations atomic.Int64
	input := newIncrementalVectorTestInput(t, engine, 2, func(_ int, values map[string]any) {
		base := reflect.ValueOf(values["resources"])
		routes := reflect.New(base.Elem().FieldByName("Routes").Type().Elem())
		getSingle := routes.Elem().FieldByName("GetSingle")
		getSingle.Set(reflect.MakeFunc(getSingle.Type(), func([]reflect.Value) []reflect.Value {
			return []reflect.Value{reflect.ValueOf("value")}
		}))
		base.Elem().FieldByName("Routes").Set(routes)
		bound, bindErr := engine.BindIncrementalResources(
			"component",
			base.Interface(),
			&incrementalResourceBindingAcceptLease{validations: &validations},
		)
		require.NoError(t, bindErr)
		values["resources"] = bound
	})

	prepared, err := prepareIncrementalVectorInput(
		t.Context(),
		engine.incrementalVectorEntryPoints["component"],
		input,
	)
	require.NoError(t, err)
	assert.Len(t, prepared.nativeFunctionTrampolines, 2)
	vectorLifecycle := input.Lifecycle.(*incrementalVectorTestLifecycle)
	require.NoError(t, engine.RenderIncrementalComponentVector(t.Context(), "component", input))
	assert.Equal(t, []string{"value", "value"}, vectorLifecycle.outputs)

	lane := IncrementalComponentVectorCarrierLane{
		TemplateName: "component",
		Count:        input.Count,
		Bindings:     input.Bindings,
		Contexts:     input.Contexts,
	}
	carrierLifecycle := &incrementalVectorCarrierWavesTestLifecycle{
		incrementalVectorTestLifecycle: newIncrementalVectorTestLifecycle(input.Count),
		waves:                          [][]IncrementalComponentVectorCarrierLane{{lane}},
	}
	require.NoError(t, engine.RenderIncrementalComponentVectorCarrierWaves(
		t.Context(),
		IncrementalComponentVectorCarrierWavesInput{
			Waves: []IncrementalComponentVectorCarrierWave{{
				Lanes: []IncrementalComponentVectorCarrierWaveLane{
					{TemplateName: "component", Count: input.Count},
				},
			}},
			Lifecycle: carrierLifecycle,
		},
	))
	assert.Equal(t, []string{"value", "value"}, carrierLifecycle.outputs)
	assert.Equal(t, int64(4), validations.Load())
}

func TestIncrementalResourceBindingWholeMemberProtectsEveryCallable(t *testing.T) {
	base := &incrementalResourceBindingTestResources{
		Routes: &incrementalResourceBindingTestStore{
			List:      func(native.Env) []any { return []any{"list"} },
			Fetch:     func(native.Env, ...any) []any { return []any{"fetch"} },
			GetSingle: func(native.Env, ...any) any { return "single" },
		},
		Services: &incrementalResourceBindingTestStore{
			List:      func(native.Env) []any { return nil },
			Fetch:     func(native.Env, ...any) []any { return nil },
			GetSingle: func(native.Env, ...any) any { return nil },
		},
	}
	plan, err := newIncrementalResourceBindingPlan(
		reflect.TypeFor[*incrementalResourceBindingTestResources](),
		map[string]uint8{"Routes": incrementalResourceAll},
	)
	require.NoError(t, err)
	ctxA := context.WithValue(t.Context(), struct{ name string }{"lease"}, "A")
	ctxB := context.WithValue(t.Context(), struct{ name string }{"lease"}, "B")
	boundValue, err := plan.bind(base, &incrementalResourceBindingTestLease{ctx: ctxA})
	require.NoError(t, err)
	bound := boundValue.(*incrementalResourceBindingTestResources)

	for name, call := range map[string]func(native.Env){
		"List":      func(env native.Env) { bound.Routes.List(env) },
		"Fetch":     func(env native.Env) { bound.Routes.Fetch(env, "key") },
		"GetSingle": func(env native.Env) { bound.Routes.GetSingle(env, "key") },
	} {
		t.Run(name, func(t *testing.T) {
			env := &incrementalResourceBindingTestEnv{ctx: ctxB}
			call(env)
			assert.ErrorContains(t, env.err, "wrong resource generation")
		})
	}
}

func TestIncrementalResourceBindingRejectsWrongRoot(t *testing.T) {
	plan, err := newIncrementalResourceBindingPlan(
		reflect.TypeFor[*incrementalResourceBindingTestResources](),
		map[string]uint8{"Routes": incrementalResourceList},
	)
	require.NoError(t, err)
	_, err = plan.bind(&struct{}{}, &incrementalResourceBindingTestLease{ctx: t.Context()})
	require.ErrorContains(t, err, "requires")
}

func TestIncrementalResourceBindingRejectsUnsupportedCallable(t *testing.T) {
	_, err := newIncrementalResourceBindingPlan(
		reflect.TypeFor[*incrementalResourceBindingUnsupportedResources](),
		map[string]uint8{"Routes": incrementalResourceAll},
	)
	require.ErrorContains(t, err, `unsupported callable "Future"`)
}

func TestIncrementalResourceBindingPlanUsesExactCompiledReachability(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want map[string]uint8
	}{
		{
			name: "one direct callable",
			src:  `{{ resources.routes.GetSingle("default", "route").Value }}`,
			want: map[string]uint8{"Routes": incrementalResourceGetSingle},
		},
		{
			name: "selected callable",
			src:  `{% call := resources.routes.Fetch %}{{ len(call("default")) }}`,
			want: map[string]uint8{"Routes": incrementalResourceFetch},
		},
		{
			name: "member alias escapes",
			src:  `{% store := resources.routes %}{{ store.GetSingle("default", "route").Value }}`,
			want: map[string]uint8{"Routes": incrementalResourceAll},
		},
		{
			name: "member passed to native",
			src:  `{{ tostring(resources.routes) }}`,
			want: map[string]uint8{"Routes": incrementalResourceAll},
		},
		{
			name: "root passed to native",
			src:  `{{ tostring(resources) }}`,
			want: map[string]uint8{
				"Routes": incrementalResourceAll, "Services": incrementalResourceAll,
			},
		},
		{
			name: "static callable needs no lease",
			src:  `{{ resources.routes.APIVersion() }}`,
			want: map[string]uint8{"Routes": incrementalResourceStatic},
		},
		{
			name: "type selector has no runtime reachability",
			src:  `{% macro value(item *resources.routes.T) string %}{{ item.Value }}{% end %}`,
			want: map[string]uint8{},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			declaration := (*incrementalResourceBindingPlanResources)(nil)
			RegisterIncrementalResourceDeclaration(declaration)
			engine, err := New(map[string]string{"component": test.src}, &Options{
				EntryPoints:            []string{"component"},
				IncrementalEntryPoints: []string{"component"},
				Declarations:           map[string]any{"resources": declaration},
			})
			require.NoError(t, err)
			plan := engine.incrementalResourceBindings["component"]
			require.NotNil(t, plan)
			assert.Equal(t, test.want, incrementalResourceBindingPlanMasks(plan))
		})
	}
}

func TestIncrementalResourceBindingPassesThroughUnregisteredResources(t *testing.T) {
	engine, err := New(map[string]string{
		"component": `{{ resources.Values["value"] }}`,
	}, &Options{
		EntryPoints:            []string{"component"},
		IncrementalEntryPoints: []string{"component"},
		Declarations: map[string]any{
			"resources": (*incrementalImmutableResources)(nil),
		},
	})
	require.NoError(t, err)
	plan, planned := engine.incrementalResourceBindings["component"]
	assert.True(t, planned)
	assert.Nil(t, plan)

	resources := &incrementalImmutableResources{Values: map[string]string{"value": "original"}}
	bound, err := engine.BindIncrementalResources(
		"component",
		resources,
		&incrementalResourceBindingTestLease{ctx: t.Context()},
	)
	require.NoError(t, err)
	assert.Same(t, resources, bound)
}

func incrementalResourceBindingPlanMasks(plan *incrementalResourceBindingPlan) map[string]uint8 {
	result := make(map[string]uint8, len(plan.fields))
	for _, field := range plan.fields {
		outerField := plan.rootType.Elem().Field(field.index)
		result[outerField.Name] = field.mask
	}
	return result
}

var incrementalResourceBindingBenchmarkSink any

func BenchmarkIncrementalResourceBinding(b *testing.B) {
	const resourceCount = 20
	storeType := reflect.StructOf([]reflect.StructField{
		{Name: "List", Type: reflect.TypeFor[func(native.Env) []any]()},
		{Name: "Fetch", Type: reflect.TypeFor[func(native.Env, ...any) []any]()},
		{Name: "GetSingle", Type: reflect.TypeFor[func(native.Env, ...any) any]()},
	})
	fields := make([]reflect.StructField, resourceCount)
	baseStores := make([]reflect.Value, resourceCount)
	for index := range resourceCount {
		fields[index] = reflect.StructField{Name: fmt.Sprintf("Resource%d", index), Type: reflect.PointerTo(storeType)}
		store := reflect.New(storeType)
		store.Elem().Field(0).Set(reflect.ValueOf(func(native.Env) []any { return nil }))
		store.Elem().Field(1).Set(reflect.ValueOf(func(native.Env, ...any) []any { return nil }))
		store.Elem().Field(2).Set(reflect.ValueOf(func(native.Env, ...any) any { return nil }))
		baseStores[index] = store
	}
	rootType := reflect.PointerTo(reflect.StructOf(fields))
	base := reflect.New(rootType.Elem())
	for index := range resourceCount {
		base.Elem().Field(index).Set(baseStores[index])
	}
	lease := &incrementalResourceBindingTestLease{ctx: b.Context()}
	tests := []struct {
		name      string
		callables map[string]uint8
	}{
		{name: "one callable", callables: map[string]uint8{"Resource0": incrementalResourceGetSingle}},
		{name: "one full field", callables: map[string]uint8{"Resource0": incrementalResourceAll}},
		{name: "full root", callables: func() map[string]uint8 {
			result := make(map[string]uint8, resourceCount)
			for index := range resourceCount {
				result[fmt.Sprintf("Resource%d", index)] = incrementalResourceAll
			}
			return result
		}()},
	}
	for _, test := range tests {
		b.Run(test.name, func(b *testing.B) {
			plan, err := newIncrementalResourceBindingPlan(rootType, test.callables)
			require.NoError(b, err)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				incrementalResourceBindingBenchmarkSink, err = plan.bind(base.Interface(), lease)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkIncrementalResourceBindingInvocation(b *testing.B) {
	const callsPerOperation = 128
	callableType := reflect.TypeFor[func(native.Env, ...any) any]()
	result := reflect.New(reflect.TypeFor[any]()).Elem()
	result.Set(reflect.ValueOf("route"))
	callback := func([]reflect.Value) []reflect.Value {
		return []reflect.Value{result}
	}
	plan, err := newIncrementalResourceBindingPlan(
		reflect.TypeFor[*incrementalResourceBindingTestResources](),
		map[string]uint8{"Routes": incrementalResourceGetSingle},
	)
	require.NoError(b, err)
	env := &incrementalResourceBindingTestEnv{ctx: b.Context()}
	args := []reflect.Value{
		reflect.ValueOf(env).Convert(reflect.TypeFor[native.Env]()),
		reflect.ValueOf([]any{"default", "route"}),
	}

	for _, directBase := range []bool{false, true} {
		name := "reflective base"
		if directBase {
			name = "trampoline base"
		}
		b.Run(name, func(b *testing.B) {
			base := &incrementalResourceBindingTestResources{Routes: &incrementalResourceBindingTestStore{}}
			if directBase {
				baseTrampoline := native.MakeFunctionTrampoline(callableType, callback)
				base.Routes.GetSingle = baseTrampoline.Value().Interface().(func(native.Env, ...any) any)
				require.NoError(b, RegisterIncrementalResourceFunctionTrampolines(
					incrementalResourceBindingTestOwner(b), base, baseTrampoline,
				))
			} else {
				base.Routes.GetSingle = reflect.MakeFunc(callableType, callback).
					Interface().(func(native.Env, ...any) any)
			}
			boundValue, bindErr := plan.bind(base, &incrementalResourceBindingAcceptLease{
				validations: &atomic.Int64{},
			})
			require.NoError(b, bindErr)
			outerTrampolines := incrementalResourceNativeFunctionTrampolines(boundValue)
			require.Len(b, outerTrampolines, 1)
			outer := outerTrampolines[0]
			b.ReportAllocs()
			b.ReportMetric(callsPerOperation, "calls/op")
			b.ResetTimer()
			for range b.N {
				for range callsPerOperation {
					incrementalResourceBindingBenchmarkSink = outer.Call(args)
				}
			}
		})
	}
}
