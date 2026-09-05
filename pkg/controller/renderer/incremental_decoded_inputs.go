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

package renderer

import (
	"errors"
	"fmt"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

type incrementalDecodedInput struct {
	revision incremental.Revision
	found    bool
	encoded  string
	kind     incrementalDecodedInputKind
	value    *incrementalCertifiedDecodedValue
	seal     *incrementalDecodedInput
}

type incrementalDecodedInputKind uint8

const (
	incrementalDecodedComponentInput incrementalDecodedInputKind = iota
	incrementalDecodedPublicationInput
)

type incrementalCertifiedDecodedValue struct {
	encoded     string
	value       any
	certificate *templating.IncrementalImmutableCertificate
	seal        *incrementalCertifiedDecodedValue
}

type incrementalCertifiedObject = incrementalCertifiedDecodedValue

type incrementalDecodedResourceInput struct {
	revision        incremental.Revision
	found           bool
	encoded         string
	value           *incrementalCertifiedResourceItems
	materialization *incrementalResourceMaterialization
	seal            *incrementalDecodedResourceInput
}

type incrementalCertifiedResourceItems struct {
	found           bool
	encoded         string
	items           []any
	certificate     *templating.IncrementalImmutableCertificate
	materialization *incrementalResourceMaterialization
	seal            *incrementalCertifiedResourceItems
}

type incrementalDecodedResourceValueIdentity struct {
	found   bool
	encoded string
}

func exactOwnedIncrementalInput(reader incremental.Reader, key incremental.InputKey) (incremental.Input, error) {
	if owned, ok := reader.(incremental.OwnedInputReader); ok {
		return owned.ExactInputOwned(key)
	}
	return reader.ExactInput(key)
}

func observeCachedIncrementalInput(
	reader incremental.Reader,
	key incremental.InputKey,
	revision incremental.Revision,
	found bool,
	encoded string,
) (bool, error) {
	if observer, ok := reader.(incremental.ExactImmutableInputObserver); ok {
		err := observer.ObserveExactImmutableInput(incremental.ImmutableInput{
			Key: key, Revision: revision, Found: found, Value: encoded,
		})
		return true, err
	}
	observer, ok := reader.(incremental.ExactInputValueObserver)
	if !ok {
		return false, nil
	}
	err := observer.ObserveExactInputValue(decodedCacheInput(key, revision, found, encoded))
	return true, err
}

func decodedCacheInput(key incremental.InputKey, revision incremental.Revision, found bool, encoded string) incremental.Input {
	return incremental.Input{
		Key: key, Revision: revision, Found: found, Value: []byte(encoded),
	}
}

func sameDecodedCacheInput(
	revision incremental.Revision,
	found bool,
	encoded string,
	input incremental.Input,
) bool {
	return revision == input.Revision && found == input.Found && stringBytesEqual(encoded, input.Value)
}

func (v *incrementalCertifiedDecodedValue) authenticate(encoded string) error {
	if v == nil || v.seal != v || v.encoded != encoded || v.certificate == nil ||
		!v.certificate.Guards(v.value) {
		return errors.New("incremental decoded value has invalid provenance")
	}
	return nil
}

func (v *incrementalCertifiedResourceItems) authenticate(found bool, encoded string) error {
	if v == nil || v.seal != v || v.found != found || v.encoded != encoded {
		return errors.New("incremental decoded resource value has invalid provenance")
	}
	if v.materialization != nil {
		if err := v.materialization.authenticateDetached(); err != nil ||
			v.materialization.found != found || v.materialization.encoded != encoded ||
			v.certificate != nil || v.items != nil {
			return errors.New("incremental decoded resource value has invalid materialization provenance")
		}
		return nil
	}
	if v.certificate == nil || !v.certificate.Guards(v.items) {
		return errors.New("incremental decoded resource value has invalid immutable provenance")
	}
	return nil
}

func (r *incrementalRenderSession) decodePublicationInput(
	reader incremental.Reader,
	key incremental.InputKey,
) (value any, certificate *templating.IncrementalImmutableCertificate, found bool, err error) {
	hash := incrementalDecodedCacheStringHash(key.Opaque())
	cached, exists, err := r.decodedInputs.load(key, hash)
	if err != nil {
		return nil, nil, false, err
	}
	if exists {
		observed, observeErr := observeCachedIncrementalInput(
			reader, key, cached.revision, cached.found, cached.encoded,
		)
		if observeErr != nil {
			return nil, nil, false, observeErr
		}
		if !observed {
			input, readErr := exactOwnedIncrementalInput(reader, key)
			if readErr != nil {
				return nil, nil, false, readErr
			}
			if !sameDecodedCacheInput(cached.revision, cached.found, cached.encoded, input) {
				return nil, nil, false, incremental.ErrRevisionConflict
			}
		}
		return authenticatedCachedPublicationValue(cached)
	}

	input, err := exactOwnedIncrementalInput(reader, key)
	if err != nil {
		return nil, nil, false, err
	}
	cached, err = r.decodedInputs.loadOrCompute(key, hash, func() (*incrementalDecodedInput, error) {
		candidate := &incrementalDecodedInput{
			revision: input.Revision,
			found:    input.Found,
			encoded:  string(input.Value),
			kind:     incrementalDecodedPublicationInput,
		}
		candidate.seal = candidate
		if input.Found {
			var certifyErr error
			candidate.value, certifyErr = r.certifyDecodedValueIdentity(candidate.encoded, input.Value, nil)
			if certifyErr != nil {
				return nil, certifyErr
			}
		}
		return candidate, nil
	})
	if err != nil {
		return nil, nil, false, err
	}
	if !sameDecodedCacheInput(cached.revision, cached.found, cached.encoded, input) {
		return nil, nil, false, incremental.ErrRevisionConflict
	}
	return authenticatedCachedPublicationValue(cached)
}

func authenticatedCachedPublicationValue(
	cached *incrementalDecodedInput,
) (value any, certificate *templating.IncrementalImmutableCertificate, found bool, err error) {
	if cached == nil || cached.seal != cached || cached.kind != incrementalDecodedPublicationInput {
		return nil, nil, false, errors.New("incremental publication cache has invalid provenance")
	}
	if !cached.found {
		if cached.value != nil {
			return nil, nil, false, errors.New("absent incremental publication has a cached value")
		}
		return nil, nil, false, nil
	}
	if err := cached.value.authenticate(cached.encoded); err != nil {
		return nil, nil, false, fmt.Errorf("incremental publication cache: %w", err)
	}
	return cached.value.value, cached.value.certificate, true, nil
}

func (r *incrementalRenderSession) observeCachedComponentInput(
	reader incremental.Reader,
	key incremental.InputKey,
	cached *incrementalDecodedInput,
	includeEncoded bool,
) (object map[string]any, encoded []byte, certificate *templating.IncrementalImmutableCertificate, found bool, err error) {
	observed, err := observeCachedIncrementalInput(
		reader,
		key,
		cached.revision,
		cached.found,
		cached.encoded,
	)
	if err != nil {
		return nil, nil, nil, false, err
	}
	if observed {
		return authenticatedCachedComponentInput(cached, includeEncoded)
	}
	input, err := exactOwnedIncrementalInput(reader, key)
	if err != nil {
		return nil, nil, nil, false, err
	}
	if !sameDecodedCacheInput(cached.revision, cached.found, cached.encoded, input) {
		return nil, nil, nil, false, incremental.ErrRevisionConflict
	}
	return authenticatedCachedComponentInput(cached, includeEncoded)
}

func (r *incrementalRenderSession) decodeComponentInputWithEncoding(
	reader incremental.Reader,
	key incremental.InputKey,
	component, label string,
	includeEncoded bool,
) (object map[string]any, encoded []byte, certificate *templating.IncrementalImmutableCertificate, found bool, err error) {
	hash := incrementalDecodedCacheStringHash(key.Opaque())
	cached, exists, err := r.decodedInputs.load(key, hash)
	if err != nil {
		return nil, nil, nil, false, err
	}
	if exists {
		return r.observeCachedComponentInput(reader, key, cached, includeEncoded)
	}
	input, err := exactOwnedIncrementalInput(reader, key)
	if err != nil {
		return nil, nil, nil, false, err
	}
	cached, err = r.decodedInputs.loadOrCompute(key, hash, func() (*incrementalDecodedInput, error) {
		candidate := &incrementalDecodedInput{
			revision: input.Revision,
			found:    input.Found,
			encoded:  string(input.Value),
			kind:     incrementalDecodedComponentInput,
		}
		candidate.seal = candidate
		if input.Found {
			candidate.value, err = r.certifyDecodedValueIdentity(candidate.encoded, input.Value, nil)
			if err != nil {
				return nil, fmt.Errorf(
					"decoding incremental component %q %s: %w", component, label, err,
				)
			}
			if _, ok := candidate.value.value.(map[string]any); !ok {
				return nil, fmt.Errorf(
					"decoding incremental component %q %s: expected an object, got %T",
					component,
					label,
					candidate.value.value,
				)
			}
		}
		return candidate, nil
	})
	if err != nil {
		return nil, nil, nil, false, err
	}
	if !sameDecodedCacheInput(cached.revision, cached.found, cached.encoded, input) {
		return nil, nil, nil, false, incremental.ErrRevisionConflict
	}
	return authenticatedCachedComponentInput(cached, includeEncoded)
}

func authenticatedCachedComponentInput(
	cached *incrementalDecodedInput,
	includeEncoded bool,
) (object map[string]any, encoded []byte, certificate *templating.IncrementalImmutableCertificate, found bool, err error) {
	if cached == nil || cached.seal != cached || cached.kind != incrementalDecodedComponentInput {
		return nil, nil, nil, false, errors.New("incremental component input cache has invalid provenance")
	}
	if includeEncoded {
		encoded = []byte(cached.encoded)
	}
	if !cached.found {
		if cached.value != nil {
			return nil, nil, nil, false, errors.New("absent incremental component input has a cached value")
		}
		return nil, encoded, nil, false, nil
	}
	if err := cached.value.authenticate(cached.encoded); err != nil {
		return nil, nil, nil, false, fmt.Errorf("incremental component input cache: %w", err)
	}
	object, ok := cached.value.value.(map[string]any)
	if !ok {
		return nil, nil, nil, false, fmt.Errorf(
			"incremental component input cache contains %T, want an object", cached.value.value,
		)
	}
	return object, encoded, cached.value.certificate, true, nil
}

func (r *incrementalRenderSession) certifyComponentObject(
	component, label string,
	encoded []byte,
	decoded map[string]any,
) (map[string]any, *templating.IncrementalImmutableCertificate, error) {
	certified, err := r.certifyDecodedValue(encoded, decoded)
	if err != nil {
		return nil, nil, fmt.Errorf("decoding incremental component %q %s: %w", component, label, err)
	}
	object, ok := certified.value.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf(
			"decoding incremental component %q %s: expected an object, got %T",
			component,
			label,
			certified.value,
		)
	}
	return object, certified.certificate, nil
}

func (r *incrementalRenderSession) certifyDecodedValue(
	encoded []byte,
	decoded any,
) (*incrementalCertifiedDecodedValue, error) {
	identity := string(encoded)
	return r.certifyDecodedValueIdentity(identity, encoded, decoded)
}

func (r *incrementalRenderSession) certifyDecodedValueIdentity(
	identity string,
	encoded []byte,
	decoded any,
) (*incrementalCertifiedDecodedValue, error) {
	hash := incrementalDecodedCacheStringHash(identity)
	certified, err := r.decodedObjects.loadOrCompute(
		identity,
		hash,
		func() (*incrementalCertifiedObject, error) {
			if decoded == nil {
				var decodeErr error
				decoded, decodeErr = decodeResourceValue(encoded)
				if decodeErr != nil {
					return nil, decodeErr
				}
			}
			candidate := &incrementalCertifiedDecodedValue{
				encoded:     identity,
				value:       decoded,
				certificate: templating.CertifyIncrementalImmutableInputs(decoded),
			}
			candidate.seal = candidate
			return candidate, nil
		},
	)
	if err != nil {
		return nil, err
	}
	if err := certified.authenticate(identity); err != nil {
		return nil, err
	}
	return certified, nil
}

func (r *incrementalRenderSession) certifyProjectedComponentObject(
	component string,
	object map[string]any,
	encoded []byte,
) (map[string]any, *templating.IncrementalImmutableCertificate, error) {
	if object == nil {
		return nil, nil, fmt.Errorf("incremental component %q projected a nil source object", component)
	}
	return r.certifyComponentObject(component, "projected source", encoded, object)
}

func (r *incrementalRenderSession) authenticateComponentProjection(
	component string,
	object map[string]any,
	encoded []byte,
	certificate *templating.IncrementalImmutableCertificate,
	projected bool,
) (map[string]any, *templating.IncrementalImmutableCertificate, error) {
	if projected {
		return r.certifyProjectedComponentObject(component, object, encoded)
	}
	if certificate == nil || !certificate.Guards(object) {
		return nil, nil, fmt.Errorf("incremental component %q unchanged source has invalid immutable provenance", component)
	}
	return object, certificate, nil
}

func (r *incrementalRenderSession) decodeResourceInput(
	reader incremental.Reader,
	spec *resourceInputSpec,
) ([]any, *templating.IncrementalImmutableCertificate, error) {
	cached, err := r.observeResourceInput(reader, spec)
	if err != nil {
		return nil, nil, err
	}
	return authenticatedCachedResourceInput(cached)
}

func (r *incrementalRenderSession) decodeMaterializedResourceInput(
	reader incremental.Reader,
	spec *resourceInputSpec,
) (
	[]any,
	*incrementalResourceMaterialization,
	error,
) {
	cached, err := r.observeResourceInput(reader, spec)
	if err != nil {
		return nil, nil, err
	}
	if cached == nil || cached.seal != cached {
		return nil, nil, errors.New("incremental resource input cache has invalid provenance")
	}
	if err := cached.value.authenticate(cached.found, cached.encoded); err != nil {
		return nil, nil, fmt.Errorf("incremental resource input cache: %w", err)
	}
	if cached.materialization == nil {
		return cached.value.items, nil, nil
	}
	if cached.materialization.key != resourceInputKey(spec) ||
		cached.materialization.revision != cached.revision ||
		cached.materialization.found != cached.found ||
		cached.materialization.encoded != cached.encoded {
		return nil, nil, errors.New("incremental resource input cache has invalid materialization provenance")
	}
	if err := cached.materialization.authenticateIdentity(r.resourceMaterializations); err != nil {
		return nil, nil, err
	}
	return nil, cached.materialization, nil
}

func (r *incrementalRenderSession) observeCachedResourceInput(
	reader incremental.Reader,
	key incremental.InputKey,
	cached *incrementalDecodedResourceInput,
) (*incrementalDecodedResourceInput, error) {
	observed, observeErr := observeCachedIncrementalInput(
		reader, key, cached.revision, cached.found, cached.encoded,
	)
	if observeErr != nil {
		return nil, observeErr
	}
	if observed {
		if err := r.observeCachedResourceProof(
			key, cached.revision, cached.found, cached.encoded,
		); err != nil {
			return nil, err
		}
		return cached, nil
	}
	input, err := exactOwnedIncrementalInput(reader, key)
	if err != nil {
		return nil, err
	}
	if err := r.observeResourceProof(input); err != nil {
		return nil, err
	}
	if !sameDecodedCacheInput(cached.revision, cached.found, cached.encoded, input) {
		return nil, incremental.ErrRevisionConflict
	}
	return cached, nil
}

func (r *incrementalRenderSession) observeResourceInput(
	reader incremental.Reader,
	spec *resourceInputSpec,
) (*incrementalDecodedResourceInput, error) {
	key := resourceInputKey(spec)
	hash := incrementalDecodedCacheStringHash(key.Opaque())
	cached, exists, err := r.decodedResourceInputs.load(key, hash)
	if err != nil {
		return nil, err
	}
	if exists {
		return r.observeCachedResourceInput(reader, key, cached)
	}
	materialized, observed, err := r.observeExactResourceMaterialization(reader, spec)
	if err != nil {
		return nil, err
	}
	if observed {
		cached, err = r.cacheMaterializedResourceInput(key, hash, materialized)
		if err != nil {
			return nil, err
		}
		return cached, nil
	}
	input, err := exactOwnedIncrementalInput(reader, key)
	if err != nil {
		return nil, err
	}
	if err := r.observeResourceProof(input); err != nil {
		return nil, err
	}
	cached, err = r.decodedResourceInputs.loadOrCompute(
		key,
		hash,
		func() (*incrementalDecodedResourceInput, error) {
			return r.decodeResourceInputCandidate(spec, input)
		},
	)
	if err != nil {
		return nil, err
	}
	if !sameDecodedCacheInput(cached.revision, cached.found, cached.encoded, input) {
		return nil, incremental.ErrRevisionConflict
	}
	return cached, nil
}

func (r *incrementalRenderSession) decodeResourceInputCandidate(
	spec *resourceInputSpec,
	input incremental.Input,
) (*incrementalDecodedResourceInput, error) {
	candidate := &incrementalDecodedResourceInput{
		revision: input.Revision,
		found:    input.Found,
		encoded:  string(input.Value),
	}
	candidate.seal = candidate
	identity := decodedResourceValueIdentity(candidate.found, candidate.encoded)
	certified, certifyErr := r.decodedResourceValues.loadOrCompute(
		identity,
		incrementalDecodedResourceValueHash(identity),
		func() (*incrementalCertifiedResourceItems, error) {
			return certifyDecodedResourceItems(spec, input, candidate.found, candidate.encoded)
		},
	)
	if certifyErr != nil {
		return nil, certifyErr
	}
	if err := certified.authenticate(candidate.found, candidate.encoded); err != nil {
		return nil, err
	}
	candidate.value = certified
	return candidate, nil
}

func certifyDecodedResourceItems(
	spec *resourceInputSpec,
	input incremental.Input,
	found bool,
	encoded string,
) (*incrementalCertifiedResourceItems, error) {
	items := []any{}
	if input.Found {
		decoded, decodeErr := decodeResourceValue(input.Value)
		if decodeErr != nil {
			return nil, fmt.Errorf(
				"decoding incremental resource %q input: %w", spec.resourceType, decodeErr,
			)
		}
		if decoded == nil {
			items = nil
		} else if list, ok := decoded.([]any); ok {
			items = list
		} else {
			return nil, fmt.Errorf(
				"decoding incremental resource %q input: expected a list, got %T",
				spec.resourceType,
				decoded,
			)
		}
	}
	value := &incrementalCertifiedResourceItems{
		found:       found,
		encoded:     encoded,
		items:       items,
		certificate: templating.CertifyIncrementalImmutableInputs(items),
	}
	value.seal = value
	return value, nil
}

func (r *incrementalRenderSession) observeExactResourceMaterialization(
	reader incremental.Reader,
	spec *resourceInputSpec,
) (*incrementalResourceMaterialization, bool, error) {
	observer, ok := reader.(incremental.ExactImmutableInputObserver)
	if !ok || r == nil || r.resourceMaterializations == nil || spec == nil {
		return nil, false, nil
	}
	snapshot := r.renderSnapshots[spec.resourceType]
	if !r.resourceSnapshotMatchesRenderGeneration(snapshot, spec) {
		return nil, false, nil
	}
	materialized, supported, err := r.resourceMaterializations.ensure(
		r.contextForReads(), snapshot, spec,
	)
	if err != nil || !supported {
		return nil, false, err
	}
	if err := observer.ObserveExactImmutableInput(materialized.immutableInput()); err != nil {
		return nil, false, err
	}
	if err := r.observeResourceProof(materialized.input()); err != nil {
		return nil, false, err
	}
	return materialized, true, nil
}

func (r *incrementalRenderSession) cacheMaterializedResourceInput(
	key incremental.InputKey,
	hash uint64,
	materialized *incrementalResourceMaterialization,
) (*incrementalDecodedResourceInput, error) {
	if materialized == nil || materialized.key != key {
		return nil, errors.New("incremental resource materialization has invalid input provenance")
	}
	cached, err := r.decodedResourceInputs.loadOrCompute(
		key,
		hash,
		func() (*incrementalDecodedResourceInput, error) {
			if err := materialized.authenticateDetached(); err != nil {
				return nil, err
			}
			candidate := &incrementalDecodedResourceInput{
				revision:        materialized.revision,
				found:           materialized.found,
				encoded:         materialized.encoded,
				materialization: materialized,
			}
			candidate.seal = candidate
			candidate.value = &incrementalCertifiedResourceItems{
				found:           materialized.found,
				encoded:         materialized.encoded,
				materialization: materialized,
			}
			candidate.value.seal = candidate.value
			if err := candidate.value.authenticate(candidate.found, candidate.encoded); err != nil {
				return nil, err
			}
			return candidate, nil
		},
	)
	if err != nil {
		return nil, err
	}
	if cached == nil || cached.revision != materialized.revision ||
		cached.found != materialized.found || cached.encoded != materialized.encoded {
		return nil, incremental.ErrRevisionConflict
	}
	return cached, nil
}

func decodedResourceValueIdentity(found bool, encoded string) incrementalDecodedResourceValueIdentity {
	return incrementalDecodedResourceValueIdentity{found: found, encoded: encoded}
}

func incrementalDecodedResourceValueHash(identity incrementalDecodedResourceValueIdentity) uint64 {
	hash := incrementalDecodedCacheHashOffset
	if identity.found {
		hash ^= 1
	}
	hash *= incrementalDecodedCacheHashPrime
	for index := 0; index < len(identity.encoded); index++ {
		hash ^= uint64(identity.encoded[index])
		hash *= incrementalDecodedCacheHashPrime
	}
	return hash
}

func authenticatedCachedResourceInput(
	cached *incrementalDecodedResourceInput,
) ([]any, *templating.IncrementalImmutableCertificate, error) {
	if cached == nil || cached.seal != cached {
		return nil, nil, errors.New("incremental resource input cache has invalid provenance")
	}
	if err := cached.value.authenticate(cached.found, cached.encoded); err != nil {
		return nil, nil, fmt.Errorf("incremental resource input cache: %w", err)
	}
	if cached.materialization != nil &&
		(cached.materialization.key.Opaque() == "" ||
			cached.materialization.revision != cached.revision ||
			cached.materialization.found != cached.found ||
			cached.materialization.encoded != cached.encoded) {
		return nil, nil, errors.New("incremental resource input cache has invalid materialization provenance")
	}
	if cached.materialization != nil {
		items, err := cached.materialization.rawItems()
		if err != nil {
			return nil, nil, fmt.Errorf("incremental resource input cache: %w", err)
		}
		certificate, err := cached.materialization.immutableCertificate()
		if err != nil {
			return nil, nil, fmt.Errorf("incremental resource input cache: %w", err)
		}
		return items, certificate, nil
	}
	return cached.value.items, cached.value.certificate, nil
}

func (r *incrementalRenderSession) observeCachedResourceProof(
	key incremental.InputKey,
	revision incremental.Revision,
	found bool,
	encoded string,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	previous, exists := r.resourceProofs[key]
	if !exists || previous.Revision != revision || previous.Found != found ||
		!stringBytesEqual(encoded, previous.Value) {
		return incremental.ErrRevisionConflict
	}
	return nil
}
