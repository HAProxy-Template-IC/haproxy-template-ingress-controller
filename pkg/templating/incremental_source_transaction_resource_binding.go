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
	"errors"
	"fmt"
	"reflect"
	"runtime"

	"gitlab.com/haproxy-haptic/scriggo/native"
)

func (e *ScriggoEngine) BindIncrementalSourceTransactionResources(
	templateNames []string,
	resources any,
	lease IncrementalResourceInvocationLease,
	selector IncrementalSourceTransactionChildSelector,
) (any, error) {
	if e == nil || len(templateNames) == 0 || isNilValue(lease) || isNilValue(selector) {
		return nil, errors.New("incremental source transaction resource binding is unavailable")
	}
	authenticator, ok := lease.(IncrementalSourceTransactionSelectorAuthenticator)
	if !ok {
		return nil, errors.New("incremental source transaction resource lease cannot authenticate its child selector")
	}
	if err := authenticator.ValidateIncrementalSourceTransactionSelector(selector); err != nil {
		return nil, fmt.Errorf("authenticating incremental source transaction child selector: %w", err)
	}
	rootType := reflect.TypeOf(resources)
	if rootType == nil || rootType.Kind() != reflect.Pointer || rootType.Elem().Kind() != reflect.Struct {
		return nil, fmt.Errorf("incremental source transaction resource binding requires a struct pointer, got %v", rootType)
	}
	metadata, err := fullIncrementalResourceBindingPlan(rootType)
	if err != nil {
		return nil, err
	}
	childMasks, allowedMasks, err := e.incrementalSourceTransactionChildMasks(templateNames, rootType)
	if err != nil {
		return nil, err
	}
	base := reflect.ValueOf(resources)
	if !base.IsValid() || base.Type() != rootType || base.IsNil() {
		return nil, fmt.Errorf("incremental source transaction resource binding requires %v, got %T", rootType, resources)
	}
	facade := reflect.New(rootType.Elem())
	builder := &incrementalSourceTransactionFacadeBuilder{
		lease:              lease,
		selector:           selector,
		childMasks:         childMasks,
		allowedMasks:       allowedMasks,
		baseBindings:       incrementalResourceNativeFunctionBindingsByIdentity(resources),
		owner:              NewIncrementalResourceFunctionBindingOwner(),
		nativeFunctions:    make([]*native.FunctionTrampoline, 0),
		ownerFallbackIndex: -1,
	}
	for _, field := range metadata.fields {
		if !incrementalSourceTransactionAllowed(allowedMasks, field.index, 0) {
			continue
		}
		if err := builder.bindField(field, base, facade); err != nil {
			return nil, err
		}
	}
	if err := builder.registerTrampolines(facade); err != nil {
		return nil, err
	}
	runtime.KeepAlive(builder.owner)
	return facade.Interface(), nil
}

func (e *ScriggoEngine) incrementalSourceTransactionChildMasks(
	templateNames []string,
	rootType reflect.Type,
) (childMasks [][]uint8, allowedMasks []uint8, err error) {
	childMasks = make([][]uint8, len(templateNames))
	allowedMasks = make([]uint8, rootType.Elem().NumField())
	plansByTemplate := make(map[string][]uint8)
	for child, templateName := range templateNames {
		masks, found := plansByTemplate[templateName]
		if !found {
			masks, err = e.incrementalSourceTransactionTemplateMasks(templateName, rootType)
			if err != nil {
				return nil, nil, err
			}
			for field, mask := range masks {
				allowedMasks[field] |= mask
			}
			plansByTemplate[templateName] = masks
		}
		childMasks[child] = masks
	}
	return childMasks, allowedMasks, nil
}

func (e *ScriggoEngine) incrementalSourceTransactionTemplateMasks(
	templateName string,
	rootType reflect.Type,
) ([]uint8, error) {
	if _, configured := e.incrementalEntryPoints[templateName]; !configured {
		return nil, fmt.Errorf("template %q is not an incremental component", templateName)
	}
	plan, planned := e.incrementalResourceBindings[templateName]
	if !planned {
		return nil, fmt.Errorf("template %q has no incremental resource binding plan", templateName)
	}
	masks := make([]uint8, rootType.Elem().NumField())
	if plan != nil {
		if plan.seal != plan || plan.rootType != rootType {
			return nil, fmt.Errorf("template %q has an invalid incremental resource binding plan", templateName)
		}
		for _, field := range plan.fields {
			masks[field.index] = field.mask
		}
	}
	return masks, nil
}

type incrementalSourceTransactionFacadeBuilder struct {
	lease              IncrementalResourceInvocationLease
	selector           IncrementalSourceTransactionChildSelector
	childMasks         [][]uint8
	allowedMasks       []uint8
	baseBindings       map[reflect.Value]incrementalResourceNativeFunctionBindingValue
	owner              *IncrementalResourceFunctionBindingOwner
	nativeFunctions    []*native.FunctionTrampoline
	ownerRetained      bool
	ownerFallbackIndex int
	ownerFallbackField reflect.Value
}

func (b *incrementalSourceTransactionFacadeBuilder) registerTrampolines(facade reflect.Value) error {
	if !b.ownerRetained && b.ownerFallbackIndex >= 0 {
		trampoline := retainIncrementalResourceFunctionBindingOwner(
			b.nativeFunctions[b.ownerFallbackIndex],
			b.owner,
		)
		b.ownerFallbackField.Set(trampoline.Value())
		b.nativeFunctions[b.ownerFallbackIndex] = trampoline
	}
	return RegisterIncrementalResourceFunctionTrampolines(
		b.owner,
		facade.Interface(),
		b.nativeFunctions...,
	)
}

func (b *incrementalSourceTransactionFacadeBuilder) bindField(
	field incrementalResourceFieldBinding,
	base, facade reflect.Value,
) error {
	baseResource := base.Elem().Field(field.index)
	if baseResource.IsNil() {
		return fmt.Errorf("incremental source transaction resource field %d is nil", field.index)
	}
	boundResource := reflect.New(baseResource.Type().Elem())
	boundResource.Elem().Set(baseResource.Elem())
	if err := b.bindFieldCallables(field, baseResource, boundResource); err != nil {
		return err
	}
	if err := b.bindFieldStatic(field, baseResource, boundResource); err != nil {
		return err
	}
	facade.Elem().Field(field.index).Set(boundResource)
	return nil
}

func (b *incrementalSourceTransactionFacadeBuilder) bindFieldCallables(
	field incrementalResourceFieldBinding,
	baseResource, boundResource reflect.Value,
) error {
	for descriptorIndex, callableIndex := range field.callableIndexes {
		mask := incrementalResourceCallableDescriptors[descriptorIndex].mask
		callable := baseResource.Elem().Field(callableIndex)
		if !incrementalSourceTransactionAllowed(b.allowedMasks, field.index, mask) {
			boundResource.Elem().Field(callableIndex).SetZero()
			continue
		}
		baseBinding := b.baseBindings[incrementalResourceNativeFunctionIdentity(callable)]
		bound, bindErr := bindIncrementalResourceCallableWithFactory(
			callable,
			b.lease,
			baseBinding,
			b.owner,
		)
		if bindErr != nil {
			return fmt.Errorf(
				"incremental source transaction resource field %d callable %d: %w",
				field.index, descriptorIndex, bindErr,
			)
		}
		guarded := bindIncrementalSourceTransactionCallable(bound, b.selector, b.childMasks, field.index, mask)
		b.nativeFunctions = append(b.nativeFunctions, guarded)
		b.ownerRetained = true
		boundResource.Elem().Field(callableIndex).Set(guarded.Value())
	}
	return nil
}

func (b *incrementalSourceTransactionFacadeBuilder) bindFieldStatic(
	field incrementalResourceFieldBinding,
	baseResource, boundResource reflect.Value,
) error {
	staticField, found := baseResource.Type().Elem().FieldByName(memberAPIVersion)
	if !found || staticField.Type.Kind() != reflect.Func || staticField.Type.NumIn() != 0 {
		return nil
	}
	staticIndex := staticField.Index[0]
	if !incrementalSourceTransactionAllowed(b.allowedMasks, field.index, incrementalResourceStatic) {
		boundResource.Elem().Field(staticIndex).SetZero()
		return nil
	}
	if baseResource.Elem().Field(staticIndex).IsNil() {
		return fmt.Errorf(
			"incremental source transaction resource field %d APIVersion is nil",
			field.index,
		)
	}
	staticCallable := baseResource.Elem().Field(staticIndex)
	baseBinding := b.baseBindings[incrementalResourceNativeFunctionIdentity(staticCallable)]
	bound := baseBinding.trampoline
	if bound == nil {
		bound = native.MakeFunctionTrampoline(staticCallable.Type(), func([]reflect.Value) []reflect.Value {
			return staticCallable.Call(nil)
		})
	}
	guarded := bindIncrementalSourceTransactionCallable(
		bound, b.selector, b.childMasks, field.index, incrementalResourceStatic,
	)
	if !b.ownerRetained && b.ownerFallbackIndex < 0 {
		b.ownerFallbackIndex = len(b.nativeFunctions)
		b.ownerFallbackField = boundResource.Elem().Field(staticIndex)
	}
	b.nativeFunctions = append(b.nativeFunctions, guarded)
	boundResource.Elem().Field(staticIndex).Set(guarded.Value())
	return nil
}

func incrementalSourceTransactionAllowed(masks []uint8, field int, callable uint8) bool {
	return field >= 0 && field < len(masks) && masks[field] != 0 &&
		(callable == 0 || masks[field]&callable != 0)
}

func bindIncrementalSourceTransactionCallable(
	base *native.FunctionTrampoline,
	selector IncrementalSourceTransactionChildSelector,
	childMasks [][]uint8,
	field int,
	callable uint8,
) *native.FunctionTrampoline {
	validate := func() error {
		return validateIncrementalSourceTransactionChildOwnership(selector, childMasks, field, callable)
	}
	call := func(args []reflect.Value) []reflect.Value {
		if err := validate(); err != nil {
			return rejectIncrementalSourceTransactionCall(base, args, err)
		}
		return base.Call(args)
	}
	if !base.SupportsFunctionCallFrame() {
		return native.MakeFunctionTrampoline(base.Value().Type(), call)
	}
	return native.MakeFunctionTrampolineWithFrame(
		base.Value().Type(),
		call,
		func(frame native.FunctionCallFrame) {
			guardedIncrementalSourceTransactionCallFrame(base, frame, validate)
		},
	)
}

func validateIncrementalSourceTransactionChildOwnership(
	selector IncrementalSourceTransactionChildSelector,
	childMasks [][]uint8,
	field int,
	callable uint8,
) error {
	child, err := selector.ActiveIncrementalSourceTransactionChild()
	if err != nil {
		return err
	}
	if child < 0 || child >= len(childMasks) || field < 0 || field >= len(childMasks[child]) ||
		childMasks[child][field] == 0 || callable != 0 && childMasks[child][field]&callable == 0 {
		return fmt.Errorf("incremental source transaction child %d does not own resource callable", child)
	}
	return nil
}

func rejectIncrementalSourceTransactionCall(
	base *native.FunctionTrampoline,
	args []reflect.Value,
	err error,
) []reflect.Value {
	if len(args) > 0 && args[0].IsValid() && args[0].Type() == reflect.TypeFor[native.Env]() {
		args[0].Interface().(native.Env).Stop(err)
		results := make([]reflect.Value, base.Value().Type().NumOut())
		for index := range results {
			results[index] = reflect.Zero(base.Value().Type().Out(index))
		}
		return results
	}
	panic(err)
}

func guardedIncrementalSourceTransactionCallFrame(
	base *native.FunctionTrampoline,
	frame native.FunctionCallFrame,
	validate func() error,
) {
	err := validate()
	if err == nil {
		base.CallFrame(frame)
		return
	}
	if base.Value().Type().NumIn() > 0 && base.Value().Type().In(0) == reflect.TypeFor[native.Env]() {
		frame.ArgEnv(0).Stop(err)
		for index := range base.Value().Type().NumOut() {
			frame.SetResultZero(index)
		}
		return
	}
	panic(err)
}
