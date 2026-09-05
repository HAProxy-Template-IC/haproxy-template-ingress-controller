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
	"context"
	"errors"
	"fmt"

	"gitlab.com/haproxy-haptic/scriggo/native"
)

const metadataObjectKey = "metadata"

type incrementalResourceDeriverContextKey struct{}
type incrementalEventRecorderContextKey struct{}
type incrementalStatusPatchRecorderContextKey struct{}
type incrementalTransitionTimeContextKey struct{}
type incrementalExecutionLeaseContextKey struct{}

// IncrementalExecutionLease rejects native capability work outside its
// component execution.
type IncrementalExecutionLease interface {
	BeginIncrementalExecution(context.Context, string) (func(), error)
	BeforeIncrementalNativeCall(context.Context) error
}

// IncrementalResourceDeriver records one query-local derived resource effect.
type IncrementalResourceDeriver interface {
	DeriveResource(resource string, item any, path string, value any) (any, error)
}

// IncrementalEventRecorder records one query-local Kubernetes Event effect.
type IncrementalEventRecorder interface {
	RecordEvent(namespace, name, apiVersion, kind, eventType, reason, message string) error
}

// IncrementalStatusPatchRecorder records one raw query-local statusPatch call.
type IncrementalStatusPatchRecorder interface {
	RecordStatusPatch(
		namespace, name, apiVersion, kind, uid, resourceVersion string,
		variants map[string]map[string]any,
		sourceTemplate string,
		sourceLine int,
	) error
}

// WithIncrementalExecutionLease binds native capability work to one component.
func WithIncrementalExecutionLease(
	ctx context.Context,
	lease IncrementalExecutionLease,
) context.Context {
	if isNilValue(lease) {
		panic(errors.New("incremental execution lease is nil"))
	}
	return context.WithValue(ctx, incrementalExecutionLeaseContextKey{}, lease)
}

func beginIncrementalExecution(ctx context.Context, operation string) (func(), error) {
	if ctx == nil {
		return func() {}, nil
	}
	lease, _ := ctx.Value(incrementalExecutionLeaseContextKey{}).(IncrementalExecutionLease)
	if isNilValue(lease) {
		return func() {}, nil
	}
	release, err := lease.BeginIncrementalExecution(ctx, operation)
	if err != nil {
		return nil, err
	}
	if release == nil {
		return nil, errors.New("incremental execution lease returned no release")
	}
	return release, nil
}

func beforeIncrementalNativeCall(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	lease, _ := ctx.Value(incrementalExecutionLeaseContextKey{}).(IncrementalExecutionLease)
	if isNilValue(lease) {
		return nil
	}
	return lease.BeforeIncrementalNativeCall(ctx)
}

// WithIncrementalResourceDeriver enables deriveResource for one component query.
func WithIncrementalResourceDeriver(ctx context.Context, deriver IncrementalResourceDeriver) context.Context {
	if isNilValue(deriver) {
		panic(errors.New("incremental resource deriver is nil"))
	}
	return context.WithValue(ctx, incrementalResourceDeriverContextKey{}, deriver)
}

// WithIncrementalEventRecorder enables recordEvent for one component query.
func WithIncrementalEventRecorder(ctx context.Context, recorder IncrementalEventRecorder) context.Context {
	if isNilValue(recorder) {
		panic(errors.New("incremental event recorder is nil"))
	}
	return context.WithValue(ctx, incrementalEventRecorderContextKey{}, recorder)
}

// WithIncrementalStatusPatchRecorder enables statusPatch for one component query.
func WithIncrementalStatusPatchRecorder(
	ctx context.Context,
	recorder IncrementalStatusPatchRecorder,
) context.Context {
	if isNilValue(recorder) {
		panic(errors.New("incremental status patch recorder is nil"))
	}
	return context.WithValue(ctx, incrementalStatusPatchRecorderContextKey{}, recorder)
}

// WithIncrementalTransitionTime provides one immutable timestamp to a status-patch component execution.
func WithIncrementalTransitionTime(ctx context.Context, value string) context.Context {
	if value == "" {
		panic(errors.New("incremental transition time is empty"))
	}
	return context.WithValue(ctx, incrementalTransitionTimeContextKey{}, value)
}

func incrementalTransitionTime(env native.Env, existingConditions any, conditionType, newStatus string) string {
	ctx := env.Context()
	if ctx == nil {
		incrementalStop(env, FuncTransitionTime, errors.New("incremental transition time is unavailable"))
		return ""
	}
	value, ok := ctx.Value(incrementalTransitionTimeContextKey{}).(string)
	if !ok || value == "" {
		incrementalStop(env, FuncTransitionTime, errors.New("incremental transition time is unavailable"))
		return ""
	}
	return transitionTimeAt(existingConditions, conditionType, newStatus, value)
}

func incrementalDeriveResource(env native.Env, resource string, item any, path string, value any) any {
	ctx := env.Context()
	if ctx == nil {
		incrementalStop(env, FuncDeriveResource, errors.New("incremental effect capability is unavailable"))
		return nil
	}
	release, err := beginIncrementalExecution(ctx, FuncDeriveResource)
	if err != nil {
		incrementalStop(env, FuncDeriveResource, err)
		return nil
	}
	defer release()
	deriver, ok := ctx.Value(incrementalResourceDeriverContextKey{}).(IncrementalResourceDeriver)
	if !ok || isNilValue(deriver) {
		incrementalStop(env, FuncDeriveResource, errors.New("incremental effect capability is unavailable"))
		return nil
	}
	if resource == "" {
		incrementalStop(env, FuncDeriveResource, errors.New("resource name is required"))
		return nil
	}
	if _, err := parseConcreteJSONPath(path); err != nil {
		incrementalStop(env, FuncDeriveResource, err)
		return nil
	}
	inputs := []struct {
		name  string
		value any
	}{{name: declItem, value: item}, {name: "value", value: value}}
	for index := range inputs {
		detached, err := cloneIncrementalSerialization(inputs[index].value)
		if err != nil {
			incrementalStop(env, FuncDeriveResource, fmt.Errorf("%s: %w", inputs[index].name, err))
			return nil
		}
		inputs[index].value = detached
	}
	derived, err := deriver.DeriveResource(resource, inputs[0].value, path, inputs[1].value)
	if err != nil {
		incrementalStop(env, FuncDeriveResource, err)
		return nil
	}
	detached, err := cloneIncrementalSerialization(derived)
	if err != nil {
		incrementalStop(env, FuncDeriveResource, fmt.Errorf("result: %w", err))
		return nil
	}
	return detached
}

func incrementalRecordEvent(env native.Env, resource any, reason, message string) string {
	ctx := env.Context()
	if ctx == nil {
		incrementalStop(env, FuncRecordEvent, errors.New("incremental effect capability is unavailable"))
		return ""
	}
	release, err := beginIncrementalExecution(ctx, FuncRecordEvent)
	if err != nil {
		incrementalStop(env, FuncRecordEvent, err)
		return ""
	}
	defer release()
	recorder, ok := ctx.Value(incrementalEventRecorderContextKey{}).(IncrementalEventRecorder)
	if !ok || isNilValue(recorder) {
		incrementalStop(env, FuncRecordEvent, errors.New("incremental effect capability is unavailable"))
		return ""
	}
	namespace, err := incrementalEffectIdentityField(resource, metadataObjectKey, "namespace")
	if err != nil {
		incrementalStop(env, FuncRecordEvent, err)
		return ""
	}
	name, err := incrementalEffectIdentityField(resource, metadataObjectKey, "name")
	if err != nil {
		incrementalStop(env, FuncRecordEvent, err)
		return ""
	}
	apiVersion, err := incrementalEffectIdentityField(resource, "apiVersion")
	if err != nil {
		incrementalStop(env, FuncRecordEvent, err)
		return ""
	}
	kind, err := incrementalEffectIdentityField(resource, "kind")
	if err != nil {
		incrementalStop(env, FuncRecordEvent, err)
		return ""
	}
	if name == "" || apiVersion == "" || kind == "" {
		incrementalStop(env, FuncRecordEvent, errors.New("resource name, apiVersion, and kind are required"))
		return ""
	}
	if reason == "" || message == "" {
		incrementalStop(env, FuncRecordEvent, errors.New("reason and message are required"))
		return ""
	}
	if err := recorder.RecordEvent(
		namespace,
		name,
		apiVersion,
		kind,
		EventTypeWarning,
		reason,
		message,
	); err != nil {
		incrementalStop(env, FuncRecordEvent, err)
	}
	return ""
}

func incrementalStatusPatch(
	env native.Env,
	resource any,
	variants map[string]any,
) string {
	ctx := env.Context()
	if ctx == nil {
		incrementalStop(env, FuncStatusPatch, errors.New("incremental effect capability is unavailable"))
		return ""
	}
	release, err := beginIncrementalExecution(ctx, FuncStatusPatch)
	if err != nil {
		incrementalStop(env, FuncStatusPatch, err)
		return ""
	}
	defer release()
	recorder, ok := ctx.Value(incrementalStatusPatchRecorderContextKey{}).(IncrementalStatusPatchRecorder)
	if !ok || isNilValue(recorder) {
		incrementalStop(env, FuncStatusPatch, errors.New("incremental effect capability is unavailable"))
		return ""
	}
	typedVariants, err := detachIncrementalStatusPatchVariants(variants)
	if err != nil {
		incrementalStop(env, FuncStatusPatch, err)
		return ""
	}
	identity, err := incrementalStatusPatchIdentityOf(resource)
	if err != nil {
		incrementalStop(env, FuncStatusPatch, err)
		return ""
	}
	if err := recorder.RecordStatusPatch(
		identity.namespace, identity.name, identity.apiVersion, identity.kind,
		identity.uid, identity.resourceVersion,
		typedVariants, env.CallPath(), env.CallLine(),
	); err != nil {
		incrementalStop(env, FuncStatusPatch, err)
	}
	return ""
}

func detachIncrementalStatusPatchVariants(variants map[string]any) (map[string]map[string]any, error) {
	detached, err := cloneIncrementalSerialization(variants)
	if err != nil {
		return nil, fmt.Errorf("variants: %w", err)
	}
	detachedVariants, ok := detached.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("variants must be a map[string]any, got %T", detached)
	}
	typedVariants := make(map[string]map[string]any, len(detachedVariants))
	for phase, value := range detachedVariants {
		status, valid := value.(map[string]any)
		if !valid {
			return nil, fmt.Errorf("variant %q must be a map[string]any, got %T", phase, value)
		}
		typedVariants[phase] = status
	}
	return typedVariants, nil
}

type incrementalStatusPatchIdentity struct {
	namespace, name, apiVersion, kind, uid, resourceVersion string
}

func incrementalStatusPatchIdentityOf(resource any) (incrementalStatusPatchIdentity, error) {
	identity := incrementalStatusPatchIdentity{}
	fields := []struct {
		target *string
		path   []string
	}{
		{&identity.namespace, []string{metadataObjectKey, "namespace"}},
		{&identity.name, []string{metadataObjectKey, "name"}},
		{&identity.apiVersion, []string{"apiVersion"}},
		{&identity.kind, []string{"kind"}},
		{&identity.uid, []string{metadataObjectKey, "uid"}},
		{&identity.resourceVersion, []string{metadataObjectKey, "resourceVersion"}},
	}
	for _, field := range fields {
		value, err := incrementalEffectIdentityField(resource, field.path...)
		if err != nil {
			return incrementalStatusPatchIdentity{}, err
		}
		*field.target = value
	}
	return identity, nil
}

func incrementalEffectIdentityField(resource any, path ...string) (string, error) {
	value, found, err := incrementalDigValue(resource, path)
	if err != nil {
		return "", err
	}
	if !found {
		value = nil
	}
	scalar, err := deterministicScalarOf(value)
	if err != nil {
		return "", fmt.Errorf("field %s: %w", path[len(path)-1], err)
	}
	return scalar.text, nil
}
