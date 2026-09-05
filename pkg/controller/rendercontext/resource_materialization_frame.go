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
	"log/slog"
	"reflect"

	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// DirectBoundResourceOperation identifies the exact declared result shape.
type DirectBoundResourceOperation uint8

const (
	DirectBoundResourceList DirectBoundResourceOperation = iota + 1
	DirectBoundResourceFetch
	DirectBoundResourceGetSingle
)

// DirectBoundResourceMaterialization describes one authenticated bound declaration.
type DirectBoundResourceMaterialization struct {
	ResourceType string
	Operation    DirectBoundResourceOperation
	ElementType  reflect.Type
	ReturnType   reflect.Type
	request      *DirectBoundResourceMaterializationRequest
	proof        *directBoundResourceMaterializationRequestProof
}

// DirectBoundResourceProjection authenticates one exact immutable source projection.
type DirectBoundResourceProjection interface {
	AuthenticateDirectBoundResourceProjection(resourceType string) error
	ProjectDirectBoundResourceProjection(
		ctx context.Context,
		resourceType string,
		elementType reflect.Type,
	) ([]reflect.Value, error)
}

// DirectBoundResourceResultProjection returns an authenticated final result directly.
type DirectBoundResourceResultProjection interface {
	ProjectDirectBoundResourceResult(
		ctx context.Context,
		resourceType string,
		operation DirectBoundResourceOperation,
		elementType reflect.Type,
		returnType reflect.Type,
	) (reflect.Value, *templating.IncrementalImmutableCertificate, bool, error)
}

// DirectBoundResourceMaterializationRequest binds a declaration to its typed builder.
type DirectBoundResourceMaterializationRequest struct {
	seal        *DirectBoundResourceMaterializationRequest
	proof       *directBoundResourceMaterializationRequestProof
	cache       *ResourceItemCache
	declaration directBoundResourceDeclarationKey
	builder     *directBoundResourceFrameBuilder
	logger      directBoundResourceAmbiguityLogger
}

type directBoundResourceMaterializationRequestProof struct {
	seal        *directBoundResourceMaterializationRequestProof
	request     *DirectBoundResourceMaterializationRequest
	cache       *ResourceItemCache
	declaration directBoundResourceDeclarationKey
	builder     *directBoundResourceFrameBuilder
	logger      directBoundResourceAmbiguityLogger
}

type directBoundResourceDeclarationKey struct {
	resourceType string
	elementType  reflect.Type
	returnType   reflect.Type
	operation    DirectBoundResourceOperation
}

type directBoundResourceFrameBuilder struct {
	seal  *directBoundResourceFrameBuilder
	build func(context.Context, []any) reflect.Value
}

type directBoundResourceAmbiguityLogger struct {
	resourceErrors *ResourceErrorCollector
	logger         *slog.Logger
}

type directBoundResourceFrameKey struct {
	projection  DirectBoundResourceProjection
	declaration directBoundResourceDeclarationKey
}

type directBoundResourceFrameSlot struct {
	seal  *directBoundResourceFrameSlot
	key   directBoundResourceFrameKey
	ready chan struct{}
	frame *directBoundResourceFrame
	err   error
}

type directBoundResourceFrame struct {
	seal        *directBoundResourceFrame
	proof       *directBoundResourceFrameProof
	key         directBoundResourceFrameKey
	value       reflect.Value
	certificate *templating.IncrementalImmutableCertificate
}

type directBoundResourceFrameProof struct {
	seal        *directBoundResourceFrameProof
	frame       *directBoundResourceFrame
	key         directBoundResourceFrameKey
	result      directBoundResourceResultIdentity
	certificate *templating.IncrementalImmutableCertificate
}

type directBoundResourceResultIdentity struct {
	typeOf   reflect.Type
	kind     reflect.Kind
	pointer  uintptr
	length   int
	capacity int
	isNil    bool
}

func newDirectBoundResourceMaterializationRequest(
	cache *ResourceItemCache,
	resourceType string,
	elementType reflect.Type,
	returnType reflect.Type,
	operation DirectBoundResourceOperation,
	build func(context.Context, []any) reflect.Value,
	resourceErrors *ResourceErrorCollector,
	logger *slog.Logger,
) *DirectBoundResourceMaterializationRequest {
	builder := &directBoundResourceFrameBuilder{build: build}
	builder.seal = builder
	request := &DirectBoundResourceMaterializationRequest{
		cache: cache,
		declaration: directBoundResourceDeclarationKey{
			resourceType: resourceType,
			elementType:  elementType,
			returnType:   returnType,
			operation:    operation,
		},
		builder: builder,
		logger: directBoundResourceAmbiguityLogger{
			resourceErrors: resourceErrors,
			logger:         logger,
		},
	}
	request.seal = request
	request.proof = &directBoundResourceMaterializationRequestProof{
		request: request, cache: cache, declaration: request.declaration,
		builder: builder, logger: request.logger,
	}
	request.proof.seal = request.proof
	return request
}

// Describe authenticates and returns the immutable public declaration.
func (r *DirectBoundResourceMaterializationRequest) Describe() (
	DirectBoundResourceMaterialization,
	error,
) {
	if err := r.authenticate(); err != nil {
		return DirectBoundResourceMaterialization{}, err
	}
	return DirectBoundResourceMaterialization{
		ResourceType: r.declaration.resourceType,
		Operation:    r.declaration.operation,
		ElementType:  r.declaration.elementType,
		ReturnType:   r.declaration.returnType,
		request:      r,
		proof:        r.proof,
	}, nil
}

// Authenticate validates the exact request and declared result shape.
func (d DirectBoundResourceMaterialization) Authenticate() error {
	if d.request == nil || d.proof == nil || d.request.proof != d.proof ||
		d.proof.request != d.request {
		return errors.New("direct bound resource materialization has invalid provenance")
	}
	if err := d.request.authenticate(); err != nil {
		return err
	}
	declaration := d.request.declaration
	if d.ResourceType != declaration.resourceType || d.Operation != declaration.operation ||
		d.ElementType != declaration.elementType || d.ReturnType != declaration.returnType {
		return errors.New("direct bound resource materialization has invalid provenance")
	}
	return nil
}

// Materialize returns a cached typed frame for one exact source projection.
func (r *DirectBoundResourceMaterializationRequest) Materialize(
	ctx context.Context,
	projection DirectBoundResourceProjection,
	keys []string,
) (reflect.Value, error) {
	if err := r.authenticate(); err != nil {
		return reflect.Value{}, err
	}
	if value, handled, err := r.materializeDirectResult(ctx, projection); err != nil {
		return reflect.Value{}, err
	} else if handled {
		return value, nil
	}
	key := directBoundResourceFrameKey{projection: projection, declaration: r.declaration}
	if cached, found := r.cache.frames.Load(key); found {
		if !r.cache.valid() {
			return reflect.Value{}, errors.New("direct bound resource declaration has invalid provenance")
		}
		return r.awaitFrame(ctx, key, cached)
	}
	slot := &directBoundResourceFrameSlot{key: key, ready: make(chan struct{})}
	slot.seal = slot
	actual, loaded := r.cache.frames.LoadOrStore(key, slot)
	if !r.cache.valid() {
		if !loaded {
			slot.err = errors.New("direct bound resource declaration has invalid provenance")
			close(slot.ready)
			r.cache.frames.CompareAndDelete(key, slot)
		}
		return reflect.Value{}, errors.New("direct bound resource declaration has invalid provenance")
	}
	if loaded {
		return r.awaitFrame(ctx, key, actual)
	}
	panicValue := r.buildFrame(ctx, slot, keys)
	if panicValue != nil {
		panic(panicValue)
	}
	return r.awaitFrame(ctx, key, slot)
}

func (r *DirectBoundResourceMaterializationRequest) materializeDirectResult(
	ctx context.Context,
	projection DirectBoundResourceProjection,
) (reflect.Value, bool, error) {
	direct, ok := projection.(DirectBoundResourceResultProjection)
	if !ok {
		return reflect.Value{}, false, nil
	}
	value, certificate, projected, err := direct.ProjectDirectBoundResourceResult(
		ctx,
		r.declaration.resourceType,
		r.declaration.operation,
		r.declaration.elementType,
		r.declaration.returnType,
	)
	if err != nil {
		return reflect.Value{}, false, err
	}
	if !projected {
		return reflect.Value{}, false, nil
	}
	if err := authenticateDirectBoundResourceProjection(
		projection, r.declaration.resourceType,
	); err != nil {
		return reflect.Value{}, false, err
	}
	if err := r.validateResult(value); err != nil {
		return reflect.Value{}, false, err
	}
	if certificate == nil || !certificate.Guards(value.Interface()) {
		return reflect.Value{}, false, errors.New("direct bound resource result has invalid immutable provenance")
	}
	if err := templating.RegisterIncrementalImmutableCertificate(ctx, certificate); err != nil {
		return reflect.Value{}, false, err
	}
	return value, true, nil
}

// MaterializeUncached preserves exact projection semantics when sharing is unsafe.
func (r *DirectBoundResourceMaterializationRequest) MaterializeUncached(
	ctx context.Context,
	items []any,
	keys []string,
) (reflect.Value, error) {
	if err := r.authenticate(); err != nil {
		return reflect.Value{}, err
	}
	if result, handled := r.singleCardinality(items, keys); handled {
		return result, nil
	}
	value := r.builder.build(ctx, items)
	if err := r.authenticate(); err != nil {
		return reflect.Value{}, err
	}
	if err := r.validateResult(value); err != nil {
		return reflect.Value{}, err
	}
	certificate := templating.CertifyIncrementalImmutableInputs(value.Interface())
	if certificate == nil || !certificate.Guards(value.Interface()) {
		return reflect.Value{}, errors.New("direct bound resource result has invalid immutable provenance")
	}
	if err := templating.RegisterIncrementalImmutableCertificate(ctx, certificate); err != nil {
		return reflect.Value{}, err
	}
	return value, nil
}

func (r *DirectBoundResourceMaterializationRequest) authenticate() error {
	if r == nil || r.seal != r || r.proof == nil || r.proof.seal != r.proof ||
		r.proof.request != r || r.cache == nil || !r.cache.valid() ||
		r.proof.cache != r.cache || r.declaration != r.proof.declaration ||
		r.builder == nil || r.builder.seal != r.builder || r.builder.build == nil ||
		r.proof.builder != r.builder || r.logger != r.proof.logger ||
		r.declaration.resourceType == "" || r.declaration.returnType == nil ||
		!r.declaration.operation.valid() {
		return errors.New("direct bound resource declaration has invalid provenance")
	}
	return nil
}

func (o DirectBoundResourceOperation) valid() bool {
	return o >= DirectBoundResourceList && o <= DirectBoundResourceGetSingle
}

func (r *DirectBoundResourceMaterializationRequest) singleCardinality(
	items []any,
	keys []string,
) (reflect.Value, bool) {
	return r.singleCardinalityCount(len(items), keys)
}

func (r *DirectBoundResourceMaterializationRequest) singleCardinalityCount(
	count int,
	keys []string,
) (reflect.Value, bool) {
	if r.declaration.operation != DirectBoundResourceGetSingle {
		return reflect.Value{}, false
	}
	if count == 0 {
		return reflect.Zero(r.declaration.returnType), true
	}
	if count == 1 {
		return reflect.Value{}, false
	}
	err := fmt.Errorf(
		"resource %q GetSingle lookup %q matched %d objects; use Fetch or configure unique indexBy values",
		r.declaration.resourceType,
		keys,
		count,
	)
	if r.logger.resourceErrors != nil {
		r.logger.resourceErrors.Record(err)
	}
	if r.logger.logger != nil {
		r.logger.logger.Error(
			"GetSingle found multiple resources (ambiguous lookup)",
			"resource_type", r.declaration.resourceType,
			"keys", keys,
			"count", count,
		)
	}
	return reflect.Zero(r.declaration.returnType), true
}

func (r *DirectBoundResourceMaterializationRequest) buildFrame(
	ctx context.Context,
	slot *directBoundResourceFrameSlot,
	keys []string,
) (panicValue any) {
	defer func() {
		panicValue = recover()
		close(slot.ready)
		if slot.err != nil || panicValue != nil {
			r.cache.frames.CompareAndDelete(slot.key, slot)
		}
	}()
	if err := authenticateDirectBoundResourceProjection(
		slot.key.projection,
		slot.key.declaration.resourceType,
	); err != nil {
		slot.err = err
		return nil
	}
	projected, err := slot.key.projection.ProjectDirectBoundResourceProjection(
		ctx,
		slot.key.declaration.resourceType,
		slot.key.declaration.elementType,
	)
	if err != nil {
		slot.err = fmt.Errorf("direct bound resource projection: %w", err)
		return nil
	}
	value, err := r.projectedValue(projected, keys)
	if err != nil {
		slot.err = err
		return nil
	}
	if err := r.authenticate(); err != nil {
		slot.err = err
		return nil
	}
	if err := r.validateResult(value); err != nil {
		slot.err = err
		return nil
	}
	certificate := templating.CertifyIncrementalImmutableInputs(value.Interface())
	if certificate == nil || !certificate.Guards(value.Interface()) {
		slot.err = errors.New("direct bound resource frame has invalid immutable provenance")
		return nil
	}
	frame := &directBoundResourceFrame{key: slot.key, value: value, certificate: certificate}
	frame.seal = frame
	frame.proof = &directBoundResourceFrameProof{
		frame: frame, key: frame.key, result: directBoundResourceIdentity(value), certificate: certificate,
	}
	frame.proof.seal = frame.proof
	slot.frame = frame
	return nil
}

func (r *DirectBoundResourceMaterializationRequest) awaitFrame(
	ctx context.Context,
	key directBoundResourceFrameKey,
	raw any,
) (reflect.Value, error) {
	slot, ok := raw.(*directBoundResourceFrameSlot)
	if !ok || slot == nil || slot.seal != slot || slot.key != key || slot.ready == nil {
		return reflect.Value{}, errors.New("direct bound resource frame slot has invalid provenance")
	}
	<-slot.ready
	if err := r.authenticate(); err != nil {
		return reflect.Value{}, err
	}
	if slot.seal != slot || slot.key != key || slot.ready == nil {
		return reflect.Value{}, errors.New("direct bound resource frame slot has invalid provenance")
	}
	if slot.err != nil {
		return reflect.Value{}, slot.err
	}
	if err := authenticateDirectBoundResourceProjection(
		key.projection, key.declaration.resourceType,
	); err != nil {
		return reflect.Value{}, err
	}
	if err := slot.frame.authenticate(key); err != nil {
		return reflect.Value{}, err
	}
	if err := templating.RegisterIncrementalImmutableCertificate(ctx, slot.frame.certificate); err != nil {
		return reflect.Value{}, err
	}
	return slot.frame.value, nil
}

func (r *DirectBoundResourceMaterializationRequest) projectedValue(
	items []reflect.Value,
	keys []string,
) (reflect.Value, error) {
	if result, handled := r.singleCardinalityCount(len(items), keys); handled {
		return result, nil
	}
	if r.declaration.operation == DirectBoundResourceGetSingle {
		return r.projectedItem(items[0], r.declaration.returnType, 0)
	}
	if r.declaration.returnType.Kind() != reflect.Slice {
		return reflect.Value{}, errors.New("direct bound resource list result is not a slice")
	}
	result := reflect.MakeSlice(r.declaration.returnType, len(items), len(items))
	for index, item := range items {
		value, err := r.projectedItem(item, r.declaration.returnType.Elem(), index)
		if err != nil {
			return reflect.Value{}, err
		}
		result.Index(index).Set(value)
	}
	return result, nil
}

func (r *DirectBoundResourceMaterializationRequest) projectedItem(
	value reflect.Value,
	want reflect.Type,
	index int,
) (reflect.Value, error) {
	if !value.IsValid() {
		if want.Kind() == reflect.Interface {
			return reflect.Zero(want), nil
		}
		return reflect.Value{}, fmt.Errorf(
			"direct bound resource %q item %d is nil", r.declaration.resourceType, index,
		)
	}
	if want.Kind() == reflect.Interface && value.Type().Implements(want) {
		result := reflect.New(want).Elem()
		result.Set(value)
		return result, nil
	}
	if value.Type().AssignableTo(want) {
		return value, nil
	}
	if value.Type().ConvertibleTo(want) {
		return value.Convert(want), nil
	}
	return reflect.Value{}, fmt.Errorf(
		"direct bound resource %q item %d has type %v, want %v",
		r.declaration.resourceType,
		index,
		value.Type(),
		want,
	)
}

func (r *DirectBoundResourceMaterializationRequest) validateResult(value reflect.Value) error {
	if !value.IsValid() || value.Type() != r.declaration.returnType {
		return fmt.Errorf(
			"direct bound resource %q %d returned %v, want %v",
			r.declaration.resourceType,
			r.declaration.operation,
			valueType(value),
			r.declaration.returnType,
		)
	}
	return nil
}

func authenticateDirectBoundResourceProjection(
	projection DirectBoundResourceProjection,
	resourceType string,
) error {
	if projection == nil || resourceType == "" || !reflect.TypeOf(projection).Comparable() {
		return errors.New("direct bound resource projection has invalid provenance")
	}
	if err := projection.AuthenticateDirectBoundResourceProjection(resourceType); err != nil {
		return fmt.Errorf("direct bound resource projection: %w", err)
	}
	return nil
}

func (f *directBoundResourceFrame) authenticate(key directBoundResourceFrameKey) error {
	if f == nil || f.seal != f || f.proof == nil || f.proof.seal != f.proof ||
		f.proof.frame != f || f.key != key || f.proof.key != key ||
		!f.value.IsValid() || f.value.Type() != key.declaration.returnType ||
		f.certificate == nil || f.proof.certificate != f.certificate ||
		f.proof.result != directBoundResourceIdentity(f.value) {
		return errors.New("direct bound resource frame has invalid provenance")
	}
	return nil
}

func directBoundResourceIdentity(value reflect.Value) directBoundResourceResultIdentity {
	identity := directBoundResourceResultIdentity{typeOf: value.Type(), kind: value.Kind()}
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Map, reflect.Pointer, reflect.Slice:
		identity.isNil = value.IsNil()
		if !identity.isNil {
			identity.pointer = value.Pointer()
		}
	}
	if value.Kind() == reflect.Slice {
		identity.length = value.Len()
		identity.capacity = value.Cap()
	}
	return identity
}

func valueType(value reflect.Value) any {
	if !value.IsValid() {
		return "<invalid>"
	}
	return value.Type()
}
