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
	"context"
	"errors"
	"fmt"
	"math"
	"sync"

	"gitlab.com/haproxy-haptic/haptic/pkg/incremental"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

type incrementalColdSourceFrameAuthority struct {
	seal    *incrementalColdSourceFrameAuthority
	session *incrementalRenderSession
	wave    int
}

type incrementalColdSourceFrameGeneration struct {
	seal      *incrementalColdSourceFrameGeneration
	authority *incrementalColdSourceFrameAuthority
	session   *incrementalRenderSession
	wave      int
	refs      []incrementalColdSourceFrameRefs
	slots     map[incremental.InputKey]uint32
	slotList  []*incrementalColdSourceInputSlot
	sealed    bool
	revoked   bool
	lifetime  sync.RWMutex
}

type incrementalColdSourceFrameRefs struct {
	generation    *incrementalColdSourceFrameGeneration
	batchPosition uint32
	binding       uint32
	item          uint32
	renderSubject uint32
}

type incrementalColdSourceFrameView struct {
	generation    *incrementalColdSourceFrameGeneration
	refs          *incrementalColdSourceFrameRefs
	queryKey      incremental.QueryKey
	binding       *incrementalColdSourceInputSlot
	item          *incrementalColdSourceInputSlot
	renderSubject *incrementalColdSourceInputSlot
}

type incrementalColdSourceInputKind uint8

const (
	incrementalColdSourceInputBinding incrementalColdSourceInputKind = iota + 1
	incrementalColdSourceInputItem
	incrementalColdSourceInputRenderSubject
)

type incrementalColdSourceInputSlot struct {
	seal      *incrementalColdSourceInputSlot
	authority *incrementalColdSourceFrameAuthority
	proof     incrementalColdSourceInputSlotProof
	key       incremental.InputKey
	kind      incrementalColdSourceInputKind

	once  sync.Once
	value *incrementalColdCertifiedSourceInput
	err   error
}

type incrementalColdSourceInputSlotProof struct {
	seal      *incrementalColdSourceInputSlotProof
	authority *incrementalColdSourceFrameAuthority
	key       incremental.InputKey
	kind      incrementalColdSourceInputKind
}

type incrementalColdCertifiedSourceInput struct {
	seal        *incrementalColdCertifiedSourceInput
	authority   *incrementalColdSourceFrameAuthority
	proof       incrementalColdCertifiedSourceInputProof
	key         incremental.InputKey
	revision    incremental.Revision
	found       bool
	encoded     string
	value       map[string]any
	certificate *templating.IncrementalImmutableCertificate
}

type incrementalColdCertifiedSourceInputProof struct {
	seal        *incrementalColdCertifiedSourceInputProof
	authority   *incrementalColdSourceFrameAuthority
	key         incremental.InputKey
	revision    incremental.Revision
	found       bool
	encoded     string
	certificate *templating.IncrementalImmutableCertificate
}

func newIncrementalColdSourceFrameGeneration(
	session *incrementalRenderSession,
	wave int,
	batchSize int,
) (*incrementalColdSourceFrameGeneration, error) {
	if session == nil || wave < 0 || batchSize <= 0 {
		return nil, errors.New("incremental cold source-frame generation is incomplete")
	}
	authority := &incrementalColdSourceFrameAuthority{session: session, wave: wave}
	authority.seal = authority
	generation := &incrementalColdSourceFrameGeneration{
		authority: authority,
		session:   session,
		wave:      wave,
		refs:      make([]incrementalColdSourceFrameRefs, batchSize),
		slots:     make(map[incremental.InputKey]uint32),
		slotList:  make([]*incrementalColdSourceInputSlot, 0),
	}
	generation.seal = generation
	return generation, nil
}

func (g *incrementalColdSourceFrameGeneration) bind(
	batchIndex int,
	queryKey incremental.QueryKey,
	component *incrementalComponent,
	source, namespace, name string,
) error {
	if !g.validForPlanning() || batchIndex < 0 || batchIndex >= len(g.refs) ||
		queryKey.Opaque() == "" || component == nil || component.name == "" || source == "" || name == "" ||
		!componentQueryKeyMatches(queryKey, component, source, namespace, name) ||
		uint64(batchIndex) >= uint64(^uint32(0)) {
		return errors.New("incremental cold source-frame binding is incomplete")
	}
	if g.refs[batchIndex].batchPosition != 0 {
		return fmt.Errorf("incremental cold source-frame repeats batch item %d", batchIndex)
	}
	binding, err := g.slot(bindingInputKey(component.name, source), incrementalColdSourceInputBinding)
	if err != nil {
		return err
	}
	item, err := g.slot(resourceInputKey(&resourceInputSpec{
		resourceType: source,
		scope:        resourceInputIdentity,
		namespace:    namespace,
		name:         name,
	}), incrementalColdSourceInputItem)
	if err != nil {
		return err
	}
	renderSubject, err := g.slot(
		renderSubjectInputKey(source, namespace, name),
		incrementalColdSourceInputRenderSubject,
	)
	if err != nil {
		return err
	}
	refs := &g.refs[batchIndex]
	*refs = incrementalColdSourceFrameRefs{
		generation:    g,
		batchPosition: uint32(batchIndex) + 1,
		binding:       binding,
		item:          item,
		renderSubject: renderSubject,
	}
	return nil
}

func (g *incrementalColdSourceFrameGeneration) slot(
	key incremental.InputKey,
	kind incrementalColdSourceInputKind,
) (uint32, error) {
	if existing, found := g.slots[key]; found {
		slot := g.slotList[existing]
		if slot == nil || slot.kind != kind {
			return 0, errors.New("incremental cold source-frame input kind conflicts with its identity")
		}
		return existing, nil
	}
	if !kind.valid() || key.Opaque() == "" || uint64(len(g.slotList)) >= uint64(^uint32(0)) {
		return 0, errors.New("incremental cold source-frame input slot is incomplete")
	}
	slot := &incrementalColdSourceInputSlot{
		authority: g.authority,
		key:       key,
		kind:      kind,
	}
	slot.seal = slot
	slot.proof = incrementalColdSourceInputSlotProof{
		authority: slot.authority,
		key:       slot.key,
		kind:      slot.kind,
	}
	slot.proof.seal = &slot.proof
	slotCount := uint64(len(g.slotList))
	if slotCount > math.MaxUint32 {
		return 0, errors.New("incremental cold source-frame generation has too many slots")
	}
	index := uint32(slotCount)
	g.slotList = append(g.slotList, slot)
	g.slots[key] = index
	return index, nil
}

func (g *incrementalColdSourceFrameGeneration) sealGeneration() error {
	if !g.validForPlanning() {
		return errors.New("incremental cold source-frame generation cannot be sealed")
	}
	for batchIndex := range g.refs {
		refs := &g.refs[batchIndex]
		if refs.batchPosition == 0 {
			continue
		}
		if err := refs.authenticatePosition(g, batchIndex); err != nil {
			return err
		}
		if _, err := g.slotAt(refs.binding, incrementalColdSourceInputBinding); err != nil {
			return err
		}
		if _, err := g.slotAt(refs.item, incrementalColdSourceInputItem); err != nil {
			return err
		}
		if _, err := g.slotAt(refs.renderSubject, incrementalColdSourceInputRenderSubject); err != nil {
			return err
		}
	}
	g.sealed = true
	return nil
}

func (g *incrementalColdSourceFrameGeneration) refsFor(
	batchIndex int,
	queryKey incremental.QueryKey,
	component *incrementalComponent,
	source, namespace, name string,
) (*incrementalColdSourceFrameRefs, error) {
	if component == nil {
		return nil, errors.New("incremental cold source-frame component is unavailable")
	}
	g.lifetime.RLock()
	defer g.lifetime.RUnlock()
	if !g.validLocked() || batchIndex < 0 || batchIndex >= len(g.refs) {
		return nil, errors.New("incremental cold source-frame generation has invalid provenance")
	}
	refs := &g.refs[batchIndex]
	if refs.batchPosition == 0 {
		return nil, fmt.Errorf("incremental cold source-frame omitted batch item %d", batchIndex)
	}
	if _, err := refs.authenticateExpected(g, batchIndex, queryKey, component, source, namespace, name); err != nil {
		return nil, err
	}
	return refs, nil
}

func (g *incrementalColdSourceFrameGeneration) revoke() {
	if g == nil {
		return
	}
	g.lifetime.Lock()
	defer g.lifetime.Unlock()
	if g.revoked {
		return
	}
	g.revoked = true
	if g.authority != nil {
		g.authority.seal = nil
	}
	g.seal = nil
}

func (g *incrementalColdSourceFrameGeneration) validForPlanning() bool {
	return g != nil && g.seal == g && !g.sealed && !g.revoked && g.session != nil &&
		g.authority != nil && g.authority.seal == g.authority &&
		g.authority.session == g.session && g.authority.wave == g.wave && len(g.refs) > 0 &&
		g.slots != nil && g.slotList != nil
}

func (g *incrementalColdSourceFrameGeneration) validLocked() bool {
	return g != nil && g.seal == g && g.sealed && !g.revoked && g.session != nil &&
		g.authority != nil && g.authority.seal == g.authority &&
		g.authority.session == g.session && g.authority.wave == g.wave && len(g.refs) > 0 &&
		g.slots != nil && g.slotList != nil
}

func (r *incrementalColdSourceFrameRefs) authenticatePosition(
	generation *incrementalColdSourceFrameGeneration,
	batchIndex int,
) error {
	if generation == nil || batchIndex < 0 || batchIndex >= len(generation.refs) ||
		r == nil || r.generation != generation || r.batchPosition == 0 ||
		uint64(r.batchPosition-1) != uint64(batchIndex) || &generation.refs[batchIndex] != r {
		return errors.New("incremental cold source-frame reference has invalid provenance")
	}
	return nil
}

func (r *incrementalColdSourceFrameRefs) authenticateExpected(
	generation *incrementalColdSourceFrameGeneration,
	batchIndex int,
	queryKey incremental.QueryKey,
	component *incrementalComponent,
	source, namespace, name string,
) (incrementalColdSourceFrameView, error) {
	if err := r.authenticatePosition(generation, batchIndex); err != nil {
		return incrementalColdSourceFrameView{}, err
	}
	if component == nil || !componentQueryKeyMatches(queryKey, component, source, namespace, name) {
		return incrementalColdSourceFrameView{}, errors.New("incremental cold source-frame reference has invalid provenance")
	}
	binding, err := generation.slotAtExpectedOpaque(
		r.binding,
		incrementalColdSourceInputBinding,
		"binding-input",
		component.name,
		source,
	)
	if err != nil {
		return incrementalColdSourceFrameView{}, err
	}
	item, err := generation.slotAtExpectedOpaque(
		r.item,
		incrementalColdSourceInputItem,
		"resource",
		source,
		string(resourceInputIdentity),
		namespace,
		name,
	)
	if err != nil {
		return incrementalColdSourceFrameView{}, err
	}
	renderSubject, err := generation.slotAtExpectedOpaque(
		r.renderSubject,
		incrementalColdSourceInputRenderSubject,
		"render-subject",
		source,
		namespace,
		name,
	)
	if err != nil {
		return incrementalColdSourceFrameView{}, err
	}
	return incrementalColdSourceFrameView{
		generation:    generation,
		refs:          r,
		queryKey:      queryKey,
		binding:       binding,
		item:          item,
		renderSubject: renderSubject,
	}, nil
}

func framesGeneration(refs *incrementalColdSourceFrameRefs) *incrementalColdSourceFrameGeneration {
	if refs == nil || refs.generation == nil || refs.batchPosition == 0 {
		return nil
	}
	generation := refs.generation
	batchIndex := uint64(refs.batchPosition - 1)
	if batchIndex >= uint64(len(generation.refs)) || &generation.refs[batchIndex] != refs {
		return nil
	}
	return generation
}

func (r *incrementalColdSourceFrameRefs) authenticateDetached(
	queryKey incremental.QueryKey,
	component *incrementalComponent,
	source, namespace, name string,
) (incrementalColdSourceFrameView, error) {
	generation := framesGeneration(r)
	if generation == nil {
		return incrementalColdSourceFrameView{}, errors.New("incremental cold source-frame reference has invalid provenance")
	}
	generation.lifetime.RLock()
	defer generation.lifetime.RUnlock()
	if !generation.validLocked() {
		return incrementalColdSourceFrameView{}, errors.New("incremental cold source-frame generation has invalid provenance")
	}
	return r.authenticateExpected(
		generation,
		int(r.batchPosition-1),
		queryKey,
		component,
		source,
		namespace,
		name,
	)
}

func (k incrementalColdSourceInputKind) valid() bool {
	return k >= incrementalColdSourceInputBinding && k <= incrementalColdSourceInputRenderSubject
}

func (k incrementalColdSourceInputKind) label() string {
	switch k {
	case incrementalColdSourceInputBinding:
		return incrementalPropsContextName
	case incrementalColdSourceInputItem:
		return incrementalSourceContextName
	case incrementalColdSourceInputRenderSubject:
		return "render subject"
	default:
		return "input"
	}
}

func (g *incrementalColdSourceFrameGeneration) slotAt(
	index uint32,
	kind incrementalColdSourceInputKind,
) (*incrementalColdSourceInputSlot, error) {
	if g == nil || !kind.valid() || uint64(index) >= uint64(len(g.slotList)) {
		return nil, errors.New("incremental cold source-frame input slot has invalid provenance")
	}
	slot := g.slotList[index]
	if err := slot.authenticateIdentity(g, index, kind); err != nil {
		return nil, err
	}
	return slot, nil
}

func (g *incrementalColdSourceFrameGeneration) slotAtExpectedOpaque(
	index uint32,
	kind incrementalColdSourceInputKind,
	opaqueKind string,
	parts ...string,
) (*incrementalColdSourceInputSlot, error) {
	slot, err := g.slotAt(index, kind)
	if err != nil {
		return nil, err
	}
	if !incrementalColdSourceOpaqueMatches(slot.key.Opaque(), opaqueKind, parts...) {
		return nil, errors.New("incremental cold source-frame input slot has invalid provenance")
	}
	return slot, nil
}

func incrementalColdSourceOpaqueMatches(value, kind string, parts ...string) bool {
	return opaqueMatches(value, kind, parts...)
}

func (s *incrementalColdSourceInputSlot) authenticateIdentity(
	generation *incrementalColdSourceFrameGeneration,
	index uint32,
	kind incrementalColdSourceInputKind,
) error {
	if generation == nil || s == nil || s.seal != s || s.authority != generation.authority ||
		s.key.Opaque() == "" || s.kind != kind || !s.kind.valid() ||
		uint64(index) >= uint64(len(generation.slotList)) || generation.slotList[index] != s {
		return errors.New("incremental cold source-frame input slot has invalid provenance")
	}
	proof := &s.proof
	if proof.seal != proof || proof.authority != s.authority ||
		proof.key != s.key || proof.kind != s.kind {
		return errors.New("incremental cold source-frame input slot has invalid provenance")
	}
	mapped, found := generation.slots[s.key]
	if !found || mapped != index {
		return errors.New("incremental cold source-frame input slot has invalid provenance")
	}
	return nil
}

func (s *incrementalColdSourceInputSlot) load(
	ctx context.Context,
	reader incremental.Reader,
	generation *incrementalColdSourceFrameGeneration,
) (*incrementalColdCertifiedSourceInput, error) {
	if ctx == nil || reader == nil || generation == nil {
		return nil, errors.New("incremental cold source-frame input is unavailable")
	}
	generation.lifetime.RLock()
	defer generation.lifetime.RUnlock()
	if !generation.validLocked() {
		return nil, errors.New("incremental cold source-frame generation has invalid provenance")
	}
	index, found := generation.slots[s.key]
	if !found {
		return nil, errors.New("incremental cold source-frame input slot has invalid provenance")
	}
	if err := s.authenticateIdentity(generation, index, s.kind); err != nil {
		return nil, err
	}

	if cause := context.Cause(ctx); cause != nil {
		return nil, cause
	}
	builder := false
	s.once.Do(func() {
		builder = true
		s.value, s.err = buildIncrementalColdCertifiedSourceInput(generation, reader, s)
	})
	value, err := s.value, s.err
	if err != nil {
		return nil, err
	}
	if err := value.authenticate(generation, s); err != nil {
		return nil, err
	}
	if !builder {
		if err := observeIncrementalColdCertifiedSourceInput(reader, value); err != nil {
			return nil, err
		}
	}
	return value, nil
}

func buildIncrementalColdCertifiedSourceInput(
	generation *incrementalColdSourceFrameGeneration,
	reader incremental.Reader,
	slot *incrementalColdSourceInputSlot,
) (value *incrementalColdCertifiedSourceInput, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			value = nil
			err = fmt.Errorf("incremental cold source-frame input construction panicked: %v", recovered)
		}
	}()
	input, err := exactOwnedIncrementalInput(reader, slot.key)
	if err != nil {
		return nil, err
	}
	if err := validateIncrementalColdSourceInput(slot.key, input); err != nil {
		return nil, err
	}
	encoded := string(input.Value)
	value = &incrementalColdCertifiedSourceInput{
		authority: generation.authority,
		key:       input.Key,
		revision:  input.Revision,
		found:     input.Found,
		encoded:   encoded,
	}
	if input.Found {
		certified, certifyErr := generation.session.certifyDecodedValueIdentity(encoded, input.Value, nil)
		if certifyErr != nil {
			return nil, fmt.Errorf("decoding incremental component %s: %w", slot.kind.label(), certifyErr)
		}
		object, ok := certified.value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf(
				"decoding incremental component %s: expected an object, got %T",
				slot.kind.label(),
				certified.value,
			)
		}
		value.value = object
		value.certificate = certified.certificate
	}
	value.seal = value
	value.proof = incrementalColdCertifiedSourceInputProof{
		authority:   value.authority,
		key:         value.key,
		revision:    value.revision,
		found:       value.found,
		encoded:     value.encoded,
		certificate: value.certificate,
	}
	value.proof.seal = &value.proof
	if err := value.authenticate(generation, slot); err != nil {
		return nil, err
	}
	return value, nil
}

func validateIncrementalColdSourceInput(
	expected incremental.InputKey,
	input incremental.Input,
) error {
	if expected.Opaque() == "" || input.Key != expected || input.Revision.Opaque() == "" ||
		(!input.Found && len(input.Value) != 0) {
		return errors.New("incremental cold source-frame input has invalid provenance")
	}
	return nil
}

func (v *incrementalColdCertifiedSourceInput) sealedFor(
	generation *incrementalColdSourceFrameGeneration,
	slot *incrementalColdSourceInputSlot,
) bool {
	return generation != nil && slot != nil && v != nil && v.seal == v &&
		v.authority == generation.authority && v.key == slot.key && v.key.Opaque() != "" &&
		v.revision.Opaque() != ""
}

func (v *incrementalColdCertifiedSourceInput) matchesProof() bool {
	proof := &v.proof
	return proof.seal == proof && proof.authority == v.authority && proof.key == v.key &&
		proof.revision == v.revision && proof.found == v.found && proof.encoded == v.encoded &&
		proof.certificate == v.certificate
}

func (v *incrementalColdCertifiedSourceInput) authenticate(
	generation *incrementalColdSourceFrameGeneration,
	slot *incrementalColdSourceInputSlot,
) error {
	if !v.sealedFor(generation, slot) || !v.matchesProof() {
		return errors.New("incremental cold source-frame input has invalid provenance")
	}
	if v.found {
		if v.value == nil || v.certificate == nil || !v.certificate.Guards(v.value) {
			return errors.New("incremental cold source-frame input has invalid immutable provenance")
		}
		return nil
	}
	if v.encoded != "" || v.value != nil || v.certificate != nil {
		return errors.New("absent incremental cold source-frame input has a value")
	}
	return nil
}

func observeIncrementalColdCertifiedSourceInput(
	reader incremental.Reader,
	value *incrementalColdCertifiedSourceInput,
) error {
	if reader == nil || value == nil {
		return errors.New("incremental cold source-frame input observation is unavailable")
	}
	if observer, ok := reader.(incremental.ExactImmutableInputObserver); ok {
		return observer.ObserveExactImmutableInput(incremental.ImmutableInput{
			Key: value.key, Revision: value.revision, Found: value.found, Value: value.encoded,
		})
	}
	if observer, ok := reader.(incremental.ExactInputValueObserver); ok {
		return observer.ObserveExactInputValue(incremental.Input{
			Key: value.key, Revision: value.revision, Found: value.found, Value: []byte(value.encoded),
		})
	}
	actual, err := exactOwnedIncrementalInput(reader, value.key)
	if err != nil {
		return err
	}
	if err := validateIncrementalColdSourceInput(value.key, actual); err != nil {
		return err
	}
	if actual.Revision != value.revision || actual.Found != value.found ||
		!stringBytesEqual(value.encoded, actual.Value) {
		return incremental.ErrRevisionConflict
	}
	return nil
}
