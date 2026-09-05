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

package rendercontext

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"

	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// ErrDerivedResourceStale means a transformation no longer matches its exact source value.
var ErrDerivedResourceStale = errors.New("derived resource source does not match")

// ErrDerivedResourceViewFrozen means a sealed view rejected a transformation.
var ErrDerivedResourceViewFrozen = errors.New("derived resource view is frozen")

// ErrDerivedResourceResolverConfigured means a view already has a resolver.
var ErrDerivedResourceResolverConfigured = errors.New("derived resource resolver is already configured")

// DerivedResourceIdentity is the stable owner of one transformed resource.
type DerivedResourceIdentity struct {
	Resource  string `json:"resource"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

// DerivedResource is an immutable transformation suitable for graph storage and replay.
type DerivedResource struct {
	Identity DerivedResourceIdentity `json:"identity"`
	Source   []byte                  `json:"source"`
	Value    []byte                  `json:"value"`
}

// DerivedResourceResolver looks up one exact transformed resource.
type DerivedResourceResolver interface {
	ResolveDerivedResource(DerivedResourceIdentity) (DerivedResource, bool, error)
}

// SelectiveDerivedResourceResolver identifies resources for which resolution can succeed.
type SelectiveDerivedResourceResolver interface {
	DerivedResourceSupported(string) bool
}

// DerivedResourceResolverFunc adapts a function to DerivedResourceResolver.
type DerivedResourceResolverFunc func(DerivedResourceIdentity) (DerivedResource, bool, error)

// ResolveDerivedResource calls f for identity.
func (f DerivedResourceResolverFunc) ResolveDerivedResource(
	identity DerivedResourceIdentity,
) (DerivedResource, bool, error) {
	return f(identity)
}

// DerivedResourceView overlays immutable transformed values on one render's store snapshots.
type DerivedResourceView struct {
	mu             sync.RWMutex
	entries        map[DerivedResourceIdentity]DerivedResource
	resourceCounts map[string]int
	origins        map[derivedResourceOriginKey]derivedResourceOrigin
	resolver       DerivedResourceResolver
	frozen         bool
}

// NewDerivedResourceView creates an empty render-local derived resource view.
func NewDerivedResourceView() *DerivedResourceView {
	return &DerivedResourceView{
		entries:        map[DerivedResourceIdentity]DerivedResource{},
		resourceCounts: map[string]int{},
		origins:        map[derivedResourceOriginKey]derivedResourceOrigin{},
	}
}

// NewDerivedResourceViewWithResolver creates a view with lazy exact-identity lookups.
func NewDerivedResourceViewWithResolver(resolver DerivedResourceResolver) *DerivedResourceView {
	view := NewDerivedResourceView()
	if err := view.SetResolver(resolver); err != nil {
		panic(err)
	}
	return view
}

// SetResolver installs the view's one lazy exact-identity resolver.
func (v *DerivedResourceView) SetResolver(resolver DerivedResourceResolver) error {
	if v == nil {
		return errors.New("cannot configure a resolver on a nil derived resource view")
	}
	if isNilDerivedResourceResolver(resolver) {
		return errors.New("derived resource resolver is nil")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.resolver != nil {
		return ErrDerivedResourceResolverConfigured
	}
	v.resolver = resolver
	return nil
}

type derivedResourceOriginKey struct {
	resource string
	typeOf   reflect.Type
	pointer  uintptr
}

type derivedResourceOrigin struct {
	identity DerivedResourceIdentity
	source   []byte
	logical  []byte
	exposed  []byte
	keep     any
}

func (v *DerivedResourceView) DeriveResource(resource string, item any, path string, value any) (any, error) {
	if v == nil {
		return nil, errors.New("cannot derive a resource into a nil view")
	}
	if v.isFrozen() {
		return nil, ErrDerivedResourceViewFrozen
	}
	if resource == "" {
		return nil, errors.New("deriveResource requires a resource name")
	}
	identity, err := derivedResourceIdentity(resource, item)
	if err != nil {
		return nil, err
	}
	input, err := encodeDerivedResource(item)
	if err != nil {
		return nil, err
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	if v.frozen {
		return nil, ErrDerivedResourceViewFrozen
	}
	source, derivationInput, err := v.derivationBaseLocked(resource, item, identity, input)
	if err != nil {
		return nil, err
	}
	derived, err := templating.DeriveResourceJSONPath(derivationInput, path, value)
	if err != nil {
		return nil, fmt.Errorf("deriving %s %s/%s at %q: %w", identity.Resource, identity.Namespace, identity.Name, path, err)
	}
	derivedIdentity, err := derivedResourceIdentity(resource, derived)
	if err != nil {
		return nil, err
	}
	if derivedIdentity != identity {
		return nil, fmt.Errorf("deriveResource cannot change resource identity from %s/%s to %s/%s",
			identity.Namespace, identity.Name, derivedIdentity.Namespace, derivedIdentity.Name)
	}
	encoded, err := encodeDerivedResource(derived)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(source, encoded) {
		v.deleteEntryLocked(identity)
	} else {
		v.setEntryLocked(&DerivedResource{
			Identity: identity,
			Source:   slices.Clone(source),
			Value:    slices.Clone(encoded),
		})
	}
	result, err := decodeDerivedResource(encoded)
	if err != nil {
		return nil, err
	}
	v.bindLocked(resource, result, source, encoded, encoded, identity)
	return result, nil
}

func (v *DerivedResourceView) derivationBaseLocked(
	resource string,
	item any,
	identity DerivedResourceIdentity,
	input []byte,
) (source []byte, derivationInput any, err error) {
	origin, hasOrigin := v.origins[derivedResourceObjectKey(resource, item)]
	if hasOrigin && (origin.identity != identity || !bytes.Equal(origin.exposed, input)) {
		return nil, nil, fmt.Errorf("%w for %s %s/%s: exposed resource origin does not match",
			ErrDerivedResourceStale, identity.Resource, identity.Namespace, identity.Name)
	}
	entry, exists := v.entries[identity]
	logical := input
	if hasOrigin {
		logical = origin.logical
	}
	if exists && !bytes.Equal(logical, entry.Value) {
		return nil, nil, fmt.Errorf("%w for %s %s/%s: transformation did not continue from the current derived value",
			ErrDerivedResourceStale, identity.Resource, identity.Namespace, identity.Name)
	}
	source = input
	if exists {
		source = entry.Source
	} else if hasOrigin {
		source = origin.source
	}
	if !hasOrigin {
		return source, item, nil
	}
	derivationInput, err = decodeDerivedResource(origin.logical)
	if err != nil {
		return nil, nil, err
	}
	return source, derivationInput, nil
}

// Project replaces matching source values with their derived values.
func (v *DerivedResourceView) Project(resource string, items []any) ([]any, error) {
	if v == nil || len(items) == 0 {
		return items, nil
	}
	v.mu.RLock()
	localEntries := v.resourceCounts[resource]
	resolver := v.resolver
	v.mu.RUnlock()
	resolverSupported := resolver != nil
	if selective, ok := resolver.(SelectiveDerivedResourceResolver); ok {
		resolverSupported = selective.DerivedResourceSupported(resource)
	}
	if localEntries == 0 && !resolverSupported {
		return items, nil
	}
	if resource == "" {
		return nil, errors.New("derived resource projection requires a resource name")
	}
	projections, err := v.snapshotDerivedResourceProjections(resource, items)
	if err != nil {
		return nil, err
	}
	for index := range projections {
		if err := resolveDerivedResourceProjection(resolver, &projections[index]); err != nil {
			return nil, err
		}
	}
	result := make([]any, len(projections))
	for index := range projections {
		result[index] = projections[index].projected
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.frozen {
		return result, nil
	}
	for index := range projections {
		projection := projections[index]
		if !projection.found {
			v.bindLocked(resource, projection.projected, projection.source, projection.source,
				projection.source, projection.identity)
			continue
		}
		v.bindLocked(resource, projection.projected, projection.entry.Source, projection.entry.Value,
			projection.entry.Value, projection.identity)
	}
	return result, nil
}

type derivedResourceProjection struct {
	projected any
	identity  DerivedResourceIdentity
	source    []byte
	entry     DerivedResource
	found     bool
}

func (v *DerivedResourceView) snapshotDerivedResourceProjections(
	resource string,
	items []any,
) ([]derivedResourceProjection, error) {
	projections := make([]derivedResourceProjection, len(items))
	for index, item := range items {
		identity, err := derivedResourceIdentity(resource, item)
		if err != nil {
			return nil, err
		}
		encoded, err := encodeDerivedResource(item)
		if err != nil {
			return nil, err
		}
		projections[index] = derivedResourceProjection{
			projected: item,
			identity:  identity,
			source:    encoded,
		}
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	for index := range projections {
		entry, found := v.entries[projections[index].identity]
		if found {
			entry.Source = slices.Clone(entry.Source)
			entry.Value = slices.Clone(entry.Value)
		}
		projections[index].entry = entry
		projections[index].found = found
	}
	return projections, nil
}

func resolveDerivedResourceProjection(
	resolver DerivedResourceResolver,
	projection *derivedResourceProjection,
) error {
	if !projection.found && resolver != nil {
		entry, found, err := resolver.ResolveDerivedResource(projection.identity)
		if err != nil {
			return fmt.Errorf("resolving derived resource %s %s/%s: %w",
				projection.identity.Resource, projection.identity.Namespace, projection.identity.Name, err)
		}
		if found {
			projection.entry, err = validateResolvedDerivedResource(projection.identity, projection.source, &entry)
			if err != nil {
				return err
			}
			projection.found = true
		}
	}
	if !projection.found {
		return nil
	}
	if !bytes.Equal(projection.source, projection.entry.Source) {
		return fmt.Errorf("%w for %s %s/%s", ErrDerivedResourceStale,
			projection.identity.Resource, projection.identity.Namespace, projection.identity.Name)
	}
	projected, err := decodeDerivedResource(projection.entry.Value)
	if err != nil {
		return err
	}
	projection.projected = projected
	return nil
}

func validateResolvedDerivedResource(
	identity DerivedResourceIdentity,
	source []byte,
	resolved *DerivedResource,
) (DerivedResource, error) {
	entry := DerivedResource{
		Identity: resolved.Identity,
		Source:   slices.Clone(resolved.Source),
		Value:    slices.Clone(resolved.Value),
	}
	if entry.Identity != identity {
		return DerivedResource{}, errors.New("resolved derived resource identity does not match its lookup")
	}
	for _, candidate := range []struct {
		label   string
		encoded []byte
	}{
		{label: "source", encoded: entry.Source},
		{label: "value", encoded: entry.Value},
	} {
		if !json.Valid(candidate.encoded) {
			return DerivedResource{}, fmt.Errorf("resolved derived resource %s is not valid JSON", candidate.label)
		}
		value, err := decodeDerivedResource(candidate.encoded)
		if err != nil {
			return DerivedResource{}, fmt.Errorf("decoding resolved derived resource %s: %w", candidate.label, err)
		}
		candidateIdentity, err := derivedResourceIdentity(identity.Resource, value)
		if err != nil {
			return DerivedResource{}, fmt.Errorf("resolved derived resource %s: %w", candidate.label, err)
		}
		if candidateIdentity != identity {
			return DerivedResource{}, fmt.Errorf("resolved derived resource %s identity does not match its owner", candidate.label)
		}
	}
	if !bytes.Equal(source, entry.Source) {
		return DerivedResource{}, fmt.Errorf("%w for %s %s/%s", ErrDerivedResourceStale,
			identity.Resource, identity.Namespace, identity.Name)
	}
	return entry, nil
}

// Bind records the exact store source behind a typed or otherwise materialized value.
func (v *DerivedResourceView) Bind(resource string, exposed, source any) error {
	if v == nil {
		return nil
	}
	if v.isFrozen() {
		return nil
	}
	identity, err := derivedResourceIdentity(resource, exposed)
	if err != nil {
		return err
	}
	sourceIdentity, err := derivedResourceIdentity(resource, source)
	if err != nil {
		return err
	}
	if sourceIdentity != identity {
		return fmt.Errorf("materialized resource identity does not match its source")
	}
	exposedValue, err := encodeDerivedResource(exposed)
	if err != nil {
		return err
	}
	sourceValue, err := encodeDerivedResource(source)
	if err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.frozen {
		return nil
	}
	logicalValue := sourceValue
	if origin, exists := v.origins[derivedResourceObjectKey(resource, source)]; exists {
		sourceValue = origin.source
		logicalValue = origin.logical
	} else if entry, exists := v.entries[identity]; exists && bytes.Equal(sourceValue, entry.Value) {
		sourceValue = entry.Source
		logicalValue = entry.Value
	}
	v.bindLocked(resource, exposed, sourceValue, logicalValue, exposedValue, identity)
	return nil
}

// Derivations returns a stable, detached snapshot for graph persistence.
func (v *DerivedResourceView) Derivations() []DerivedResource {
	if v == nil {
		return nil
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.derivationsLocked()
}

// Freeze seals the transformation entries and returns their detached stable snapshot.
func (v *DerivedResourceView) Freeze() []DerivedResource {
	if v == nil {
		return nil
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.frozen = true
	return v.derivationsLocked()
}

func (v *DerivedResourceView) derivationsLocked() []DerivedResource {
	result := make([]DerivedResource, 0, len(v.entries))
	for _, entry := range v.entries {
		entry.Source = slices.Clone(entry.Source)
		entry.Value = slices.Clone(entry.Value)
		result = append(result, entry)
	}
	slices.SortFunc(result, func(left, right DerivedResource) int {
		if compared := strings.Compare(left.Identity.Resource, right.Identity.Resource); compared != 0 {
			return compared
		}
		if compared := strings.Compare(left.Identity.Namespace, right.Identity.Namespace); compared != 0 {
			return compared
		}
		return strings.Compare(left.Identity.Name, right.Identity.Name)
	})
	return result
}

// Replay installs one previously recorded transformation after validating its exact values.
func (v *DerivedResourceView) Replay(entry *DerivedResource) error {
	if v == nil {
		return errors.New("cannot replay a derived resource into a nil view")
	}
	if entry == nil {
		return errors.New("cannot replay a nil derived resource")
	}
	if v.isFrozen() {
		return ErrDerivedResourceViewFrozen
	}
	if entry.Identity.Resource == "" || entry.Identity.Name == "" {
		return errors.New("derived resource replay has an incomplete identity")
	}
	for _, candidate := range []struct {
		label   string
		encoded []byte
	}{
		{label: "source", encoded: entry.Source},
		{label: "value", encoded: entry.Value},
	} {
		value, err := decodeDerivedResource(candidate.encoded)
		if err != nil {
			return fmt.Errorf("decoding derived resource %s: %w", candidate.label, err)
		}
		identity, err := derivedResourceIdentity(entry.Identity.Resource, value)
		if err != nil {
			return fmt.Errorf("derived resource %s: %w", candidate.label, err)
		}
		if identity != entry.Identity {
			return fmt.Errorf("derived resource %s identity does not match its owner", candidate.label)
		}
	}
	detached := DerivedResource{
		Identity: entry.Identity,
		Source:   slices.Clone(entry.Source),
		Value:    slices.Clone(entry.Value),
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.frozen {
		return ErrDerivedResourceViewFrozen
	}
	if bytes.Equal(detached.Source, detached.Value) {
		v.deleteEntryLocked(detached.Identity)
		return nil
	}
	if current, exists := v.entries[detached.Identity]; exists &&
		(!bytes.Equal(current.Source, detached.Source) || !bytes.Equal(current.Value, detached.Value)) {
		return fmt.Errorf("derived resource %s %s/%s has conflicting replay values",
			detached.Identity.Resource, detached.Identity.Namespace, detached.Identity.Name)
	}
	v.setEntryLocked(&detached)
	return nil
}

func (v *DerivedResourceView) isFrozen() bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.frozen
}

func isNilDerivedResourceResolver(resolver DerivedResourceResolver) bool {
	if resolver == nil {
		return true
	}
	value := reflect.ValueOf(resolver)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func (v *DerivedResourceView) setEntryLocked(entry *DerivedResource) {
	if v.entries == nil {
		v.entries = map[DerivedResourceIdentity]DerivedResource{}
	}
	if v.resourceCounts == nil {
		v.resourceCounts = map[string]int{}
	}
	if _, exists := v.entries[entry.Identity]; !exists {
		v.resourceCounts[entry.Identity.Resource]++
	}
	v.entries[entry.Identity] = *entry
}

func (v *DerivedResourceView) deleteEntryLocked(identity DerivedResourceIdentity) {
	if _, exists := v.entries[identity]; !exists {
		return
	}
	delete(v.entries, identity)
	v.resourceCounts[identity.Resource]--
	if v.resourceCounts[identity.Resource] == 0 {
		delete(v.resourceCounts, identity.Resource)
	}
}

func derivedResourceIdentity(resource string, item any) (DerivedResourceIdentity, error) {
	// The round trip below normalises arbitrary input into a JSON object. When
	// the item already is one, marshalling the whole resource to read two
	// strings is pure cost — 10% of a warm render's allocations. Anything the
	// fast path cannot answer falls through, so the errors below stay the ones
	// callers see.
	if identity, ok := derivedResourceIdentityFromObject(resource, item); ok {
		return identity, nil
	}
	encoded, err := encodeDerivedResource(item)
	if err != nil {
		return DerivedResourceIdentity{}, err
	}
	decoded, err := decodeDerivedResource(encoded)
	if err != nil {
		return DerivedResourceIdentity{}, err
	}
	object, ok := decoded.(map[string]any)
	if !ok {
		return DerivedResourceIdentity{}, fmt.Errorf("resource %q is not an object", resource)
	}
	metadata, ok := object["metadata"].(map[string]any)
	if !ok {
		return DerivedResourceIdentity{}, fmt.Errorf("resource %q has no metadata object", resource)
	}
	name, _ := metadata["name"].(string)
	if name == "" {
		return DerivedResourceIdentity{}, fmt.Errorf("resource %q has no metadata.name", resource)
	}
	namespace, _ := metadata["namespace"].(string)
	return DerivedResourceIdentity{Resource: resource, Namespace: namespace, Name: name}, nil
}

// derivedResourceIdentityFromObject reads the identity straight out of an
// already-decoded object. It reports false for anything it cannot fully answer
// — a non-object, a missing or non-string name — so the caller re-derives it
// through the round trip and produces the diagnostic that names the problem.
func derivedResourceIdentityFromObject(resource string, item any) (DerivedResourceIdentity, bool) {
	object, ok := item.(map[string]any)
	if !ok {
		return DerivedResourceIdentity{}, false
	}
	metadata, ok := object["metadata"].(map[string]any)
	if !ok {
		return DerivedResourceIdentity{}, false
	}
	name, ok := metadata["name"].(string)
	if !ok || name == "" {
		return DerivedResourceIdentity{}, false
	}
	namespace, _ := metadata["namespace"].(string)
	return DerivedResourceIdentity{Resource: resource, Namespace: namespace, Name: name}, true
}

func encodeDerivedResource(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encoding derived resource: %w", err)
	}
	return encoded, nil
}

func decodeDerivedResource(encoded []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decoding derived resource: %w", err)
	}
	return normalizeTemplateNumbers(value)
}

func derivedResourceObjectKey(resource string, value any) derivedResourceOriginKey {
	reflected := reflect.ValueOf(value)
	for reflected.IsValid() && reflected.Kind() == reflect.Interface {
		if reflected.IsNil() {
			return derivedResourceOriginKey{}
		}
		reflected = reflected.Elem()
	}
	if !reflected.IsValid() || (reflected.Kind() != reflect.Map && reflected.Kind() != reflect.Pointer) || reflected.IsNil() {
		return derivedResourceOriginKey{}
	}
	return derivedResourceOriginKey{resource: resource, typeOf: reflected.Type(), pointer: reflected.Pointer()}
}

func (v *DerivedResourceView) bindLocked(
	resource string,
	exposed any,
	source, logical, exposedValue []byte,
	identity DerivedResourceIdentity,
) {
	key := derivedResourceObjectKey(resource, exposed)
	if key.pointer == 0 {
		return
	}
	if v.origins == nil {
		v.origins = map[derivedResourceOriginKey]derivedResourceOrigin{}
	}
	v.origins[key] = derivedResourceOrigin{
		identity: identity,
		source:   slices.Clone(source),
		logical:  slices.Clone(logical),
		exposed:  slices.Clone(exposedValue),
		keep:     exposed,
	}
}
