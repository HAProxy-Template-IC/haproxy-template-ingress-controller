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

package templating

import (
	"fmt"
	"reflect"
	"sync"

	"gitlab.com/haproxy-haptic/scriggo/native"
)

var incrementalResourceStoreTypes sync.Map
var incrementalResourceDeclarationTypes sync.Map

// RegisterIncrementalResourceDeclaration marks one controller-built resources
// type for lease-bound batch execution.
func RegisterIncrementalResourceDeclaration(declaration any) {
	declarationType := reflect.TypeOf(declaration)
	if declarationType == nil || declarationType.Kind() != reflect.Pointer ||
		declarationType.Elem().Kind() != reflect.Struct {
		panic(fmt.Sprintf("templating: incremental resources declaration must be a struct pointer, got %T", declaration))
	}
	incrementalResourceDeclarationTypes.Store(declarationType, struct{}{})
}

func registeredIncrementalResourceDeclarationType(declarationType reflect.Type) bool {
	_, registered := incrementalResourceDeclarationTypes.Load(declarationType)
	return registered
}

var exactCyclePreviousOutputTypes sync.Map

// RegisterExactCyclePreviousOutputDeclaration marks one controller-owned
// previous-output type (currentConfig) as replay-safe. The engine must not
// name the controller's packages, so the owner registers the type instead.
func RegisterExactCyclePreviousOutputDeclaration(declaration any) {
	declarationType := reflect.TypeOf(declaration)
	if declarationType == nil || declarationType.Kind() != reflect.Pointer ||
		declarationType.Elem().Kind() != reflect.Struct {
		panic(fmt.Sprintf(
			"templating: exact cycle previous output declaration must be a struct pointer, got %T", declaration))
	}
	exactCyclePreviousOutputTypes.Store(declarationType, struct{}{})
}

func registeredExactCyclePreviousOutputType(declarationType reflect.Type) bool {
	_, registered := exactCyclePreviousOutputTypes.Load(declarationType)
	return registered
}

// IncrementalResourceStoreType adds the execution environment used to bind
// resource reads to one component lease while preserving template call syntax.
func IncrementalResourceStoreType(storeType reflect.Type) reflect.Type {
	if cached, ok := incrementalResourceStoreTypes.Load(storeType); ok {
		return cached.(reflect.Type)
	}
	if storeType == nil || storeType.Kind() != reflect.Struct {
		panic(fmt.Sprintf("templating: incremental resource store must be a struct, got %v", storeType))
	}
	fields := make([]reflect.StructField, storeType.NumField())
	for index := range storeType.NumField() {
		field := storeType.Field(index)
		fields[index] = reflect.StructField{
			Name:      field.Name,
			PkgPath:   field.PkgPath,
			Type:      field.Type,
			Tag:       field.Tag,
			Anonymous: field.Anonymous,
		}
		switch field.Name {
		case memberList, memberFetch, memberGetSingle:
			fields[index].Type = incrementalResourceFunctionType(field.Type)
		}
	}
	result := reflect.StructOf(fields)
	actual, _ := incrementalResourceStoreTypes.LoadOrStore(storeType, result)
	return actual.(reflect.Type)
}

func incrementalResourceFunctionType(functionType reflect.Type) reflect.Type {
	if functionType.Kind() != reflect.Func {
		panic(fmt.Sprintf("templating: incremental resource callable must be a function, got %v", functionType))
	}
	environmentType := reflect.TypeFor[native.Env]()
	if functionType.NumIn() > 0 && functionType.In(0) == environmentType {
		return functionType
	}
	inputs := make([]reflect.Type, functionType.NumIn()+1)
	inputs[0] = environmentType
	for index := range functionType.NumIn() {
		inputs[index+1] = functionType.In(index)
	}
	outputs := make([]reflect.Type, functionType.NumOut())
	for index := range functionType.NumOut() {
		outputs[index] = functionType.Out(index)
	}
	return reflect.FuncOf(inputs, outputs, functionType.IsVariadic())
}

func incrementalResourcesDeclaration(declaration any) (any, bool) {
	declarationType := reflect.TypeOf(declaration)
	if declarationType == nil || declarationType.Kind() != reflect.Pointer {
		panic(fmt.Sprintf("templating: incremental resources declaration must be a struct pointer, got %T", declaration))
	}
	if declarationType.Elem().Kind() == reflect.Map {
		return declaration, false
	}
	if declarationType.Elem().Kind() != reflect.Struct {
		panic(fmt.Sprintf("templating: incremental resources declaration must be a struct pointer, got %T", declaration))
	}
	if _, registered := incrementalResourceDeclarationTypes.Load(declarationType); !registered {
		return declaration, false
	}
	resourcesType := declarationType.Elem()
	fields := make([]reflect.StructField, resourcesType.NumField())
	for index := range resourcesType.NumField() {
		field := resourcesType.Field(index)
		if field.Type.Kind() != reflect.Pointer || field.Type.Elem().Kind() != reflect.Struct {
			return declaration, false
		}
		resourceType := field.Type.Elem()
		for _, name := range [...]string{memberList, memberFetch, memberGetSingle} {
			callable, ok := resourceType.FieldByName(name)
			if !ok || callable.Type.Kind() != reflect.Func {
				return declaration, false
			}
		}
		fields[index] = reflect.StructField{
			Name:      field.Name,
			PkgPath:   field.PkgPath,
			Type:      reflect.PointerTo(IncrementalResourceStoreType(field.Type.Elem())),
			Tag:       field.Tag,
			Anonymous: field.Anonymous,
		}
	}
	result := reflect.Zero(reflect.PointerTo(reflect.StructOf(fields))).Interface()
	RegisterIncrementalResourceDeclaration(result)
	return result, true
}
