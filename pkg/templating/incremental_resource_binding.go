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
	"slices"
	"strings"
	"sync"
	"weak"

	"gitlab.com/haproxy-haptic/scriggo"
	"gitlab.com/haproxy-haptic/scriggo/native"
)

const (
	incrementalResourceList uint8 = 1 << iota
	incrementalResourceFetch
	incrementalResourceGetSingle
	incrementalResourceStatic
	incrementalResourceAll = incrementalResourceList | incrementalResourceFetch | incrementalResourceGetSingle
)

type incrementalResourceFieldBinding struct {
	index           int
	mask            uint8
	callableIndexes [3]int
	staticIndex     int
}

type incrementalResourceBindingPlan struct {
	seal     *incrementalResourceBindingPlan
	rootType reflect.Type
	fields   []incrementalResourceFieldBinding
}

type incrementalResourceNativeFunctionKey struct {
	typeOf  reflect.Type
	pointer uintptr
}

type incrementalResourceNativeFunctionEntry struct {
	seal     *incrementalResourceNativeFunctionEntry
	bindings []incrementalResourceNativeFunctionBinding
}

type incrementalResourceNativeFunctionBinding struct {
	trampoline        weak.Pointer[native.FunctionTrampoline]
	boundFrameFactory IncrementalResourceBoundFrameFactory
}

var incrementalResourceNativeFunctionRegistry sync.Map

type incrementalResourceNativeFunctionReference struct {
	owner weak.Pointer[IncrementalResourceFunctionBindingOwner]
}

type incrementalResourceFunctionBindingCleanup struct {
	key       incrementalResourceNativeFunctionKey
	reference *incrementalResourceNativeFunctionReference
}

// IncrementalResourceFunctionBindingOwner retains bindings as long as its facade is reachable.
type IncrementalResourceFunctionBindingOwner struct {
	seal       *IncrementalResourceFunctionBindingOwner
	mu         sync.RWMutex
	key        incrementalResourceNativeFunctionKey
	entryValue *incrementalResourceNativeFunctionEntry
}

// NewIncrementalResourceFunctionBindingOwner creates a facade binding owner.
func NewIncrementalResourceFunctionBindingOwner() *IncrementalResourceFunctionBindingOwner {
	owner := &IncrementalResourceFunctionBindingOwner{}
	owner.seal = owner
	return owner
}

func cleanupIncrementalResourceFunctionBindings(cleanup *incrementalResourceFunctionBindingCleanup) {
	if cleanup == nil {
		return
	}
	incrementalResourceNativeFunctionRegistry.CompareAndDelete(cleanup.key, cleanup.reference)
}

func (o *IncrementalResourceFunctionBindingOwner) register(
	key incrementalResourceNativeFunctionKey,
	entry *incrementalResourceNativeFunctionEntry,
) error {
	if o == nil || o.seal != o || entry == nil || entry.seal != entry {
		return errors.New("incremental resource function binding owner is unavailable")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.entryValue != nil {
		return errors.New("incremental resource function binding owner is already registered")
	}
	reference := &incrementalResourceNativeFunctionReference{owner: weak.Make(o)}
	o.key = key
	o.entryValue = entry
	incrementalResourceNativeFunctionRegistry.Store(key, reference)
	runtime.AddCleanup(o, cleanupIncrementalResourceFunctionBindings, &incrementalResourceFunctionBindingCleanup{
		key:       key,
		reference: reference,
	})
	runtime.KeepAlive(o)
	return nil
}

func (o *IncrementalResourceFunctionBindingOwner) entry(
	key incrementalResourceNativeFunctionKey,
) *incrementalResourceNativeFunctionEntry {
	if o == nil || o.seal != o {
		return nil
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	if o.key != key {
		return nil
	}
	return o.entryValue
}

func newIncrementalResourceBindingPlan(
	rootType reflect.Type,
	callables map[string]uint8,
) (*incrementalResourceBindingPlan, error) {
	if rootType == nil || rootType.Kind() != reflect.Pointer || rootType.Elem().Kind() != reflect.Struct {
		return nil, fmt.Errorf("incremental resource binding requires a struct pointer, got %v", rootType)
	}
	plan := &incrementalResourceBindingPlan{rootType: rootType}
	for name, mask := range callables {
		binding, err := newIncrementalResourceFieldBinding(rootType, name, mask)
		if err != nil {
			return nil, err
		}
		plan.fields = append(plan.fields, binding)
	}
	slices.SortFunc(plan.fields, func(left, right incrementalResourceFieldBinding) int {
		return left.index - right.index
	})
	plan.seal = plan
	return plan, nil
}

func newIncrementalResourceFieldBinding(
	rootType reflect.Type,
	name string,
	mask uint8,
) (incrementalResourceFieldBinding, error) {
	field, found := rootType.Elem().FieldByName(name)
	if !found || len(field.Index) != 1 || field.Type.Kind() != reflect.Pointer ||
		field.Type.Elem().Kind() != reflect.Struct {
		return incrementalResourceFieldBinding{}, fmt.Errorf(
			"incremental resource binding field %q is unavailable", name,
		)
	}
	if err := validateIncrementalResourceStoreCallables(name, field.Type.Elem()); err != nil {
		return incrementalResourceFieldBinding{}, err
	}
	if mask == 0 || mask&^(incrementalResourceAll|incrementalResourceStatic) != 0 {
		return incrementalResourceFieldBinding{}, fmt.Errorf(
			"incremental resource binding field %q has invalid callable mask", name,
		)
	}
	var callableIndexes [3]int
	for callableIndex, callable := range incrementalResourceCallableDescriptors {
		callableField, callableFound := field.Type.Elem().FieldByName(callable.name)
		if !callableFound || callableField.Type.Kind() != reflect.Func ||
			callableField.Type.NumIn() == 0 || callableField.Type.In(0) != reflect.TypeFor[native.Env]() {
			return incrementalResourceFieldBinding{}, fmt.Errorf(
				"incremental resource binding field %q has invalid %s callable", name, callable.name,
			)
		}
		callableIndexes[callableIndex] = callableField.Index[0]
	}
	staticIndex := -1
	if mask&incrementalResourceStatic != 0 {
		staticField, staticFound := field.Type.Elem().FieldByName(memberAPIVersion)
		if !staticFound || staticField.Type.Kind() != reflect.Func || staticField.Type.NumIn() != 0 {
			return incrementalResourceFieldBinding{}, fmt.Errorf(
				"incremental resource binding field %q has invalid APIVersion callable", name,
			)
		}
		staticIndex = staticField.Index[0]
	}
	return incrementalResourceFieldBinding{
		index: field.Index[0], mask: mask, callableIndexes: callableIndexes, staticIndex: staticIndex,
	}, nil
}

func validateIncrementalResourceStoreCallables(name string, storeType reflect.Type) error {
	for index := range storeType.NumField() {
		field := storeType.Field(index)
		if field.Type.Kind() != reflect.Func {
			continue
		}
		switch field.Name {
		case memberList, memberFetch, memberGetSingle, memberAPIVersion:
		default:
			return fmt.Errorf(
				"incremental resource binding field %q has unsupported callable %q",
				name,
				field.Name,
			)
		}
	}
	return nil
}

func fullIncrementalResourceBindingPlan(rootType reflect.Type) (*incrementalResourceBindingPlan, error) {
	if rootType == nil || rootType.Kind() != reflect.Pointer || rootType.Elem().Kind() != reflect.Struct {
		return nil, fmt.Errorf("incremental resource binding requires a struct pointer, got %v", rootType)
	}
	callables := make(map[string]uint8, rootType.Elem().NumField())
	for index := range rootType.Elem().NumField() {
		callables[rootType.Elem().Field(index).Name] = incrementalResourceAll
	}
	return newIncrementalResourceBindingPlan(rootType, callables)
}

func (p *incrementalResourceBindingPlan) bind(
	resources any,
	lease IncrementalResourceInvocationLease,
) (any, error) {
	if p == nil || p.seal != p || p.rootType == nil || lease == nil {
		return nil, errors.New("incremental resource binding is unavailable")
	}
	base := reflect.ValueOf(resources)
	if !base.IsValid() || base.Type() != p.rootType || base.IsNil() {
		return nil, fmt.Errorf(
			"incremental resource binding requires %v, got %T",
			p.rootType,
			resources,
		)
	}
	facade := reflect.New(p.rootType.Elem())
	owner := NewIncrementalResourceFunctionBindingOwner()
	baseBindings := incrementalResourceNativeFunctionBindingsByIdentity(resources)
	nativeFunctions := make(
		[]*native.FunctionTrampoline,
		0,
		len(p.fields)*len(incrementalResourceCallableDescriptors),
	)
	ownerRetained := false
	fallback := incrementalResourceOwnerFallback{index: -1}
	for _, field := range p.fields {
		baseResource := base.Elem().Field(field.index)
		if baseResource.IsNil() {
			return nil, fmt.Errorf("incremental resource binding field %d is nil", field.index)
		}
		boundResource := reflect.New(baseResource.Type().Elem())
		boundResource.Elem().Set(baseResource.Elem())
		if field.staticIndex >= 0 && baseResource.Elem().Field(field.staticIndex).IsNil() {
			return nil, fmt.Errorf("incremental resource binding field %d APIVersion is nil", field.index)
		}
		nativeFunctions = appendIncrementalResourceStaticBinding(
			field, baseResource, boundResource, baseBindings, nativeFunctions, &fallback,
		)
		var fieldRetained bool
		var bindErr error
		nativeFunctions, fieldRetained, bindErr = bindIncrementalResourceFieldCallables(
			field, baseResource, boundResource, lease, baseBindings, owner, nativeFunctions,
		)
		if bindErr != nil {
			return nil, bindErr
		}
		ownerRetained = ownerRetained || fieldRetained
		facade.Elem().Field(field.index).Set(boundResource)
	}
	if !ownerRetained && fallback.index >= 0 {
		trampoline := retainIncrementalResourceFunctionBindingOwner(
			nativeFunctions[fallback.index],
			owner,
		)
		fallback.field.Set(trampoline.Value())
		nativeFunctions[fallback.index] = trampoline
	}
	if err := RegisterIncrementalResourceFunctionTrampolines(
		owner,
		facade.Interface(),
		nativeFunctions...,
	); err != nil {
		return nil, err
	}
	return facade.Interface(), nil
}

// incrementalResourceOwnerFallback names the first static trampoline, which
// retains the binding owner when no field callable does.
type incrementalResourceOwnerFallback struct {
	index int
	field reflect.Value
}

func appendIncrementalResourceStaticBinding(
	field incrementalResourceFieldBinding,
	baseResource, boundResource reflect.Value,
	baseBindings map[reflect.Value]incrementalResourceNativeFunctionBindingValue,
	nativeFunctions []*native.FunctionTrampoline,
	fallback *incrementalResourceOwnerFallback,
) []*native.FunctionTrampoline {
	if field.staticIndex < 0 {
		return nativeFunctions
	}
	staticCallable := baseResource.Elem().Field(field.staticIndex)
	binding := baseBindings[incrementalResourceNativeFunctionIdentity(staticCallable)]
	if binding.trampoline == nil {
		return nativeFunctions
	}
	if fallback.index < 0 {
		fallback.index = len(nativeFunctions)
		fallback.field = boundResource.Elem().Field(field.staticIndex)
	}
	return append(nativeFunctions, binding.trampoline)
}

func bindIncrementalResourceFieldCallables(
	field incrementalResourceFieldBinding,
	baseResource, boundResource reflect.Value,
	lease IncrementalResourceInvocationLease,
	baseBindings map[reflect.Value]incrementalResourceNativeFunctionBindingValue,
	owner *IncrementalResourceFunctionBindingOwner,
	nativeFunctions []*native.FunctionTrampoline,
) ([]*native.FunctionTrampoline, bool, error) {
	retained := false
	for descriptorIndex, callableIndex := range field.callableIndexes {
		callable := baseResource.Elem().Field(callableIndex)
		if field.mask&incrementalResourceCallableDescriptors[descriptorIndex].mask == 0 {
			boundResource.Elem().Field(callableIndex).Set(reflect.Zero(callable.Type()))
			continue
		}
		if callable.IsNil() {
			return nil, false, fmt.Errorf(
				"incremental resource binding field %d %s is nil",
				field.index,
				incrementalResourceCallableDescriptors[descriptorIndex].name,
			)
		}
		baseBinding := baseBindings[incrementalResourceNativeFunctionIdentity(callable)]
		trampoline, err := bindIncrementalResourceCallableWithFactory(callable, lease, baseBinding, owner)
		if err != nil {
			return nil, false, fmt.Errorf(
				"incremental resource binding field %d %s: %w",
				field.index,
				incrementalResourceCallableDescriptors[descriptorIndex].name,
				err,
			)
		}
		nativeFunctions = append(nativeFunctions, trampoline)
		retained = true
		boundResource.Elem().Field(callableIndex).Set(trampoline.Value())
	}
	return nativeFunctions, retained, nil
}

func retainIncrementalResourceFunctionBindingOwner(
	trampoline *native.FunctionTrampoline,
	owner *IncrementalResourceFunctionBindingOwner,
) *native.FunctionTrampoline {
	if trampoline == nil || owner == nil {
		return trampoline
	}
	call := func(args []reflect.Value) []reflect.Value {
		result := trampoline.Call(args)
		runtime.KeepAlive(owner)
		return result
	}
	if !trampoline.SupportsFunctionCallFrame() {
		return native.MakeFunctionTrampoline(trampoline.Value().Type(), call)
	}
	return native.MakeFunctionTrampolineWithFrame(
		trampoline.Value().Type(),
		call,
		func(frame native.FunctionCallFrame) {
			trampoline.CallFrame(frame)
			runtime.KeepAlive(owner)
		},
	)
}

var incrementalResourceCallableDescriptors = [...]struct {
	name string
	mask uint8
}{
	{name: memberList, mask: incrementalResourceList},
	{name: memberFetch, mask: incrementalResourceFetch},
	{name: memberGetSingle, mask: incrementalResourceGetSingle},
}

var fullIncrementalResourceBindingPlans sync.Map

func incrementalResourceDeclarationType(declaration native.Declaration) reflect.Type {
	if synchronous, ok := declaration.(native.SynchronousDeclaration); ok {
		declaration = synchronous.Declaration
	}
	declarationType := reflect.TypeOf(declaration)
	if declarationType == nil || declarationType.Kind() != reflect.Pointer ||
		declarationType.Elem().Kind() != reflect.Struct {
		return nil
	}
	if !registeredIncrementalResourceDeclarationType(declarationType) {
		return nil
	}
	return declarationType
}

func compileIncrementalResourceBindingPlan(
	compiled *scriggo.Template,
	rootType reflect.Type,
) (*incrementalResourceBindingPlan, error) {
	if compiled == nil {
		return nil, errors.New("incremental resource binding has no compiled template")
	}
	if rootType == nil {
		return nil, errors.New("incremental resource binding has no resources type")
	}
	callables := map[string]uint8{}
	if !collectIncrementalResourceAccessCallables(compiled, rootType, callables) ||
		!collectIncrementalResourceInvokedCallables(compiled, rootType, callables) {
		return fullIncrementalResourceBindingPlan(rootType)
	}
	return newIncrementalResourceBindingPlan(rootType, callables)
}

func collectIncrementalResourceAccessCallables(
	compiled *scriggo.Template,
	rootType reflect.Type,
	callables map[string]uint8,
) bool {
	for _, access := range compiled.UsedNativeValueAccesses() {
		if access.DeclarationName != declResources {
			continue
		}
		if !matchesIncrementalResourceDeclaration(
			access.Package,
			access.Declaration,
			rootType,
		) || access.MemberPath == "" {
			return false
		}
		fieldName, found := incrementalResourceFieldForPath(rootType, access.MemberPath)
		if !found {
			return false
		}
		callables[fieldName] = incrementalResourceAll
	}
	return true
}

func collectIncrementalResourceInvokedCallables(
	compiled *scriggo.Template,
	rootType reflect.Type,
	callables map[string]uint8,
) bool {
	invoked := compiled.UsedNativeCallables()
	for index := range invoked {
		callable := &invoked[index]
		if callable.DeclarationName != declResources {
			continue
		}
		if !matchesIncrementalResourceDeclaration(
			callable.Package,
			callable.Declaration,
			rootType,
		) || callable.Kind != scriggo.NativeCallableFunctionField || callable.Constructed {
			return false
		}
		fieldName, memberName, found := incrementalResourceCallablePath(rootType, callable.MemberPath)
		if !found || callable.Name != memberName {
			return false
		}
		var mask uint8
		switch memberName {
		case memberAPIVersion:
			mask = incrementalResourceStatic
		case memberList:
			mask = incrementalResourceList
		case memberFetch:
			mask = incrementalResourceFetch
		case memberGetSingle:
			mask = incrementalResourceGetSingle
		default:
			return false
		}
		callables[fieldName] |= mask
	}
	return true
}

func matchesIncrementalResourceDeclaration(
	packageName string,
	declaration native.Declaration,
	rootType reflect.Type,
) bool {
	return packageName == scriggoMainPackage && reflect.TypeOf(declaration) == rootType
}

func incrementalResourceFieldForPath(rootType reflect.Type, path string) (string, bool) {
	first, _, _ := strings.Cut(path, ".")
	if first == "" {
		return "", false
	}
	for index := range rootType.Elem().NumField() {
		field := rootType.Elem().Field(index)
		if field.Name == first || incrementalResourceJSONName(&field) == first {
			return field.Name, true
		}
	}
	return "", false
}

func incrementalResourceCallablePath(
	rootType reflect.Type,
	path string,
) (fieldName, callableName string, found bool) {
	first, rest, separated := strings.Cut(path, ".")
	if !separated || rest == "" || strings.Contains(rest, ".") {
		return "", "", false
	}
	fieldName, found = incrementalResourceFieldForPath(rootType, first)
	if !found {
		return "", "", false
	}
	return fieldName, rest, true
}

func incrementalResourceJSONName(field *reflect.StructField) string {
	name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
	if name == "-" {
		return ""
	}
	return name
}

// BindIncrementalResources binds the resource callables reachable by one entry point.
func (e *ScriggoEngine) BindIncrementalResources(
	templateName string,
	resources any,
	lease IncrementalResourceInvocationLease,
) (any, error) {
	if e == nil {
		return nil, errors.New("incremental resource binder is unavailable")
	}
	if _, configured := e.incrementalEntryPoints[templateName]; !configured {
		return nil, fmt.Errorf("template %q is not an incremental component", templateName)
	}
	plan, planned := e.incrementalResourceBindings[templateName]
	if !planned {
		return nil, fmt.Errorf("template %q has no incremental resource binding plan", templateName)
	}
	if plan == nil {
		return resources, nil
	}
	return plan.bind(resources, lease)
}

// BindAllIncrementalResources binds every resource callable to one component lease.
func BindAllIncrementalResources(
	resources any,
	lease IncrementalResourceInvocationLease,
) (any, error) {
	rootType := reflect.TypeOf(resources)
	if cached, found := fullIncrementalResourceBindingPlans.Load(rootType); found {
		return cached.(*incrementalResourceBindingPlan).bind(resources, lease)
	}
	plan, err := fullIncrementalResourceBindingPlan(rootType)
	if err != nil {
		return nil, err
	}
	actual, _ := fullIncrementalResourceBindingPlans.LoadOrStore(rootType, plan)
	return actual.(*incrementalResourceBindingPlan).bind(resources, lease)
}

func bindIncrementalResourceCallable(
	callable reflect.Value,
	lease IncrementalResourceInvocationLease,
	baseTrampoline *native.FunctionTrampoline,
	owner *IncrementalResourceFunctionBindingOwner,
) *native.FunctionTrampoline {
	call := func(args []reflect.Value) []reflect.Value {
		runtime.KeepAlive(owner)
		env := args[0].Interface().(native.Env)
		if err := lease.ValidateIncrementalResourceInvocation(env.Context()); err != nil {
			env.Stop(err)
			results := make([]reflect.Value, callable.Type().NumOut())
			for index := range results {
				results[index] = reflect.Zero(callable.Type().Out(index))
			}
			return results
		}
		if baseTrampoline != nil {
			return baseTrampoline.Call(args)
		}
		if callable.Type().IsVariadic() {
			return callable.CallSlice(args)
		}
		return callable.Call(args)
	}
	if baseTrampoline == nil || !baseTrampoline.SupportsFunctionCallFrame() {
		return native.MakeFunctionTrampoline(callable.Type(), call)
	}
	return native.MakeFunctionTrampolineWithFrame(
		callable.Type(),
		call,
		func(frame native.FunctionCallFrame) {
			runtime.KeepAlive(owner)
			env := frame.ArgEnv(0)
			if err := lease.ValidateIncrementalResourceInvocation(env.Context()); err != nil {
				env.Stop(err)
				for index := range callable.Type().NumOut() {
					frame.SetResultZero(index)
				}
				return
			}
			baseTrampoline.CallFrame(frame)
		},
	)
}

func bindIncrementalResourceCallableWithFactory(
	callable reflect.Value,
	lease IncrementalResourceInvocationLease,
	binding incrementalResourceNativeFunctionBindingValue,
	owner *IncrementalResourceFunctionBindingOwner,
) (*native.FunctionTrampoline, error) {
	if binding.boundFrameFactory == nil {
		return bindIncrementalResourceCallable(callable, lease, binding.trampoline, owner), nil
	}
	trampoline, err := binding.boundFrameFactory(lease)
	if err != nil {
		return nil, err
	}
	if trampoline == nil || !trampoline.SupportsFunctionCallFrame() ||
		!trampoline.Value().IsValid() || trampoline.Value().Type() != callable.Type() {
		return nil, errors.New("bound resource frame factory returned an invalid trampoline")
	}
	return retainIncrementalResourceFunctionBindingOwner(trampoline, owner), nil
}

// IncrementalResourceBoundFrameFactory binds one native resource frame to an exact execution lease.
type IncrementalResourceBoundFrameFactory func(
	IncrementalResourceInvocationLease,
) (*native.FunctionTrampoline, error)

// IncrementalResourceFunctionBinding associates one exact callable with an optional bound frame factory.
type IncrementalResourceFunctionBinding struct {
	Trampoline        *native.FunctionTrampoline
	BoundFrameFactory IncrementalResourceBoundFrameFactory
}

type incrementalResourceNativeFunctionBindingValue struct {
	trampoline        *native.FunctionTrampoline
	boundFrameFactory IncrementalResourceBoundFrameFactory
}

// RegisterIncrementalResourceFunctionTrampolines associates exact native
// function implementations with one owned resources facade.
func RegisterIncrementalResourceFunctionTrampolines(
	owner *IncrementalResourceFunctionBindingOwner,
	resources any,
	trampolines ...*native.FunctionTrampoline,
) error {
	bindings := make([]IncrementalResourceFunctionBinding, len(trampolines))
	for index, trampoline := range trampolines {
		bindings[index].Trampoline = trampoline
	}
	return RegisterIncrementalResourceFunctionBindings(owner, resources, bindings...)
}

// RegisterIncrementalResourceFunctionBindings associates exact native callables with one owned resources facade.
func RegisterIncrementalResourceFunctionBindings(
	owner *IncrementalResourceFunctionBindingOwner,
	resources any,
	bindings ...IncrementalResourceFunctionBinding,
) error {
	key, valid := incrementalResourceNativeFunctionOwnerKey(resources)
	if !valid {
		return fmt.Errorf("incremental resource function trampolines require a non-nil pointer, got %T", resources)
	}
	seen := make(map[*native.FunctionTrampoline]int, len(bindings))
	entry := &incrementalResourceNativeFunctionEntry{
		bindings: make([]incrementalResourceNativeFunctionBinding, 0, len(bindings)),
	}
	for _, binding := range bindings {
		trampoline := binding.Trampoline
		if trampoline == nil || !trampoline.Value().IsValid() {
			return errors.New("incremental resource function trampoline is invalid")
		}
		if seenIndex, duplicate := seen[trampoline]; duplicate {
			if binding.BoundFrameFactory != nil || entry.bindings[seenIndex].boundFrameFactory != nil {
				return errors.New("incremental resource function bound frame factory is duplicated")
			}
			continue
		}
		seen[trampoline] = len(entry.bindings)
		entry.bindings = append(entry.bindings, incrementalResourceNativeFunctionBinding{
			trampoline:        weak.Make(trampoline),
			boundFrameFactory: binding.BoundFrameFactory,
		})
	}
	if len(entry.bindings) == 0 {
		return nil
	}
	entry.seal = entry
	if err := owner.register(key, entry); err != nil {
		return err
	}
	runtime.KeepAlive(resources)
	runtime.KeepAlive(owner)
	return nil
}

func incrementalResourceNativeFunctionTrampolines(resources any) []*native.FunctionTrampoline {
	bindings := incrementalResourceNativeFunctionBindings(resources)
	trampolines := make([]*native.FunctionTrampoline, 0, len(bindings))
	for _, binding := range bindings {
		trampolines = append(trampolines, binding.trampoline)
	}
	return trampolines
}

func incrementalResourceNativeFunctionBindings(
	resources any,
) []incrementalResourceNativeFunctionBindingValue {
	entry, owner := incrementalResourceNativeFunctionEntryFor(resources)
	if entry == nil {
		return nil
	}
	bindings := make([]incrementalResourceNativeFunctionBindingValue, 0, len(entry.bindings))
	for _, binding := range entry.bindings {
		if trampoline := binding.trampoline.Value(); trampoline != nil {
			bindings = append(bindings, incrementalResourceNativeFunctionBindingValue{
				trampoline:        trampoline,
				boundFrameFactory: binding.boundFrameFactory,
			})
		}
	}
	runtime.KeepAlive(resources)
	runtime.KeepAlive(owner)
	return bindings
}

func incrementalResourceNativeFunctionEntryFor(
	resources any,
) (*incrementalResourceNativeFunctionEntry, *IncrementalResourceFunctionBindingOwner) {
	key, valid := incrementalResourceNativeFunctionOwnerKey(resources)
	if !valid {
		return nil, nil
	}
	registered, found := incrementalResourceNativeFunctionRegistry.Load(key)
	if !found {
		return nil, nil
	}
	reference, ok := registered.(*incrementalResourceNativeFunctionReference)
	if !ok || reference == nil {
		return nil, nil
	}
	owner := reference.owner.Value()
	if owner == nil {
		incrementalResourceNativeFunctionRegistry.CompareAndDelete(key, reference)
		return nil, nil
	}
	entry := owner.entry(key)
	if entry == nil || entry.seal != entry {
		return nil, nil
	}
	runtime.KeepAlive(resources)
	return entry, owner
}

func incrementalResourceNativeFunctionOwnerKey(resources any) (
	incrementalResourceNativeFunctionKey,
	bool,
) {
	value := reflect.ValueOf(resources)
	for value.IsValid() && value.Kind() == reflect.Interface {
		if value.IsNil() {
			return incrementalResourceNativeFunctionKey{}, false
		}
		value = value.Elem()
	}
	if !value.IsValid() || value.Kind() != reflect.Pointer || value.IsNil() {
		return incrementalResourceNativeFunctionKey{}, false
	}
	return incrementalResourceNativeFunctionKey{typeOf: value.Type(), pointer: value.Pointer()}, true
}

func incrementalResourceNativeFunctionTrampolinesByIdentity(
	resources any,
) map[reflect.Value]*native.FunctionTrampoline {
	bindings := incrementalResourceNativeFunctionBindingsByIdentity(resources)
	if len(bindings) == 0 {
		return nil
	}
	byIdentity := make(map[reflect.Value]*native.FunctionTrampoline, len(bindings))
	for identity, binding := range bindings {
		byIdentity[identity] = binding.trampoline
	}
	return byIdentity
}

func incrementalResourceNativeFunctionBindingsByIdentity(
	resources any,
) map[reflect.Value]incrementalResourceNativeFunctionBindingValue {
	bindings := incrementalResourceNativeFunctionBindings(resources)
	if len(bindings) == 0 {
		return nil
	}
	byIdentity := make(map[reflect.Value]incrementalResourceNativeFunctionBindingValue, len(bindings))
	for _, binding := range bindings {
		identity := incrementalResourceNativeFunctionIdentity(binding.trampoline.Value())
		if identity.IsValid() {
			byIdentity[identity] = binding
		}
	}
	return byIdentity
}

func incrementalResourceNativeFunctionIdentity(value reflect.Value) reflect.Value {
	if !value.IsValid() || value.Kind() != reflect.Func || !value.CanInterface() {
		return reflect.Value{}
	}
	return reflect.ValueOf(value.Interface())
}

func mergeIncrementalResourceNativeFunctionTrampolines(
	base []*native.FunctionTrampoline,
	resources ...any,
) []*native.FunctionTrampoline {
	collector := newIncrementalResourceNativeFunctionCollector(base)
	for _, value := range resources {
		collector.add(value)
	}
	return collector.trampolines
}

type incrementalResourceNativeFunctionCollector struct {
	seen        map[*native.FunctionTrampoline]struct{}
	trampolines []*native.FunctionTrampoline
}

func newIncrementalResourceNativeFunctionCollector(
	base []*native.FunctionTrampoline,
) *incrementalResourceNativeFunctionCollector {
	collector := &incrementalResourceNativeFunctionCollector{
		seen:        make(map[*native.FunctionTrampoline]struct{}, len(base)),
		trampolines: make([]*native.FunctionTrampoline, 0, len(base)),
	}
	for _, trampoline := range base {
		if trampoline == nil {
			continue
		}
		if _, duplicate := collector.seen[trampoline]; duplicate {
			continue
		}
		collector.seen[trampoline] = struct{}{}
		collector.trampolines = append(collector.trampolines, trampoline)
	}
	return collector
}

func (collector *incrementalResourceNativeFunctionCollector) add(resources any) {
	for _, trampoline := range incrementalResourceNativeFunctionTrampolines(resources) {
		if _, duplicate := collector.seen[trampoline]; duplicate {
			continue
		}
		collector.seen[trampoline] = struct{}{}
		collector.trampolines = append(collector.trampolines, trampoline)
	}
}
