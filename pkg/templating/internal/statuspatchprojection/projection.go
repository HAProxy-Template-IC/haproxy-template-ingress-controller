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

package statuspatchprojection

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
)

const maxValueDepth = 256

const (
	valueNull byte = iota
	valueBool
	valueString
	valueInt
	valueUint
	valueFloat
	valueArray
	valueObject
)

const (
	numberInt byte = iota
	numberInt8
	numberInt16
	numberInt32
	numberInt64
	numberUint
	numberUint8
	numberUint16
	numberUint32
	numberUint64
	numberFloat32
	numberFloat64
)

type value struct {
	kind       byte
	numberType byte
	boolean    bool
	text       string
	integer    int64
	unsigned   uint64
	floatBits  uint64
	array      []value
	object     []field
}

type field struct {
	name  string
	value value
}

type phase struct {
	name  string
	value value
}

type patch struct {
	namespace       string
	name            string
	apiVersion      string
	kind            string
	uid             string
	resourceVersion string
	phases          []phase
	sourceTemplate  string
	sourceLine      int
}

type part struct {
	root  *Root
	owner any
}

// InputPatch is detached by New before it becomes cached state.
type InputPatch struct {
	Namespace       string
	Name            string
	APIVersion      string
	Kind            string
	UID             string
	ResourceVersion string
	Variants        map[string]map[string]any
	SourceTemplate  string
	SourceLine      int
}

// Part authenticates one child root while constructing a composite root.
type Part struct {
	Root  *Root
	Owner any
}

// Metadata is a detached immutable patch descriptor.
type Metadata struct {
	Namespace       string
	Name            string
	APIVersion      string
	Kind            string
	UID             string
	ResourceVersion string
	SourceTemplate  string
	SourceLine      int
}

// Root owns a projection payload that callers cannot mutate through safe Go.
type Root struct {
	owner      any
	patches    []patch
	parts      []part
	patchCount int
	composite  bool
	seal       *Root
}

// PatchView is an authenticated read-only reference into a Root.
type PatchView struct {
	root       *Root
	owner      any
	patchIndex int
}

// PhaseView is an authenticated read-only reference into a Root.
type PhaseView struct {
	root       *Root
	owner      any
	patchIndex int
	phaseIndex int
}

type identity struct {
	namespace  string
	name       string
	apiVersion string
	kind       string
}

type builder struct {
	patches []patch
	byKey   map[identity]int
}

type visit struct {
	kind byte
	ptr  uintptr
}

// New detaches patches into an opaque root bound to owner.
func New(owner any, calls []InputPatch) (*Root, error) {
	if owner == nil {
		return nil, errors.New("owner is nil")
	}
	b := builder{byKey: make(map[identity]int)}
	for index := range calls {
		if err := b.add(&calls[index]); err != nil {
			return nil, fmt.Errorf("call %d: %w", index, err)
		}
	}
	for patchIndex := range b.patches {
		slices.SortFunc(b.patches[patchIndex].phases, func(left, right phase) int {
			return compareStrings(left.name, right.name)
		})
	}
	root := &Root{owner: owner, patches: b.patches, patchCount: len(b.patches)}
	root.seal = root
	return root, nil
}

// NewGroup composes authenticated roots without exposing or copying payloads.
func NewGroup(owner any, parts []Part) (*Root, error) {
	if owner == nil {
		return nil, errors.New("owner is nil")
	}
	owned := make([]part, len(parts))
	patchCount := 0
	for index, candidate := range parts {
		if err := candidate.Root.Validate(candidate.Owner); err != nil {
			return nil, fmt.Errorf("part %d: %w", index, err)
		}
		owned[index] = part{root: candidate.Root, owner: candidate.Owner}
		patchCount += candidate.Root.patchCount
	}
	root := &Root{owner: owner, parts: owned, patchCount: patchCount, composite: true}
	root.seal = root
	return root, nil
}

func (b *builder) add(call *InputPatch) error {
	if call == nil {
		return errors.New("call is nil")
	}
	if call.Name == "" || call.APIVersion == "" || call.Kind == "" {
		return errors.New("name, apiVersion, and kind are required")
	}
	if len(call.Variants) == 0 {
		return errors.New("at least one variant is required")
	}
	key := identity{
		namespace: call.Namespace, name: call.Name, apiVersion: call.APIVersion, kind: call.Kind,
	}
	patchIndex, exists := b.byKey[key]
	if !exists {
		patchIndex = len(b.patches)
		b.byKey[key] = patchIndex
		b.patches = append(b.patches, patch{
			namespace: call.Namespace, name: call.Name, apiVersion: call.APIVersion, kind: call.Kind,
			uid: call.UID, resourceVersion: call.ResourceVersion,
		})
	}
	projectedPatch := &b.patches[patchIndex]
	if projectedPatch.uid != call.UID || projectedPatch.resourceVersion != call.ResourceVersion {
		return fmt.Errorf("%s/%s has conflicting source lineage", call.Namespace, call.Name)
	}
	if projectedPatch.sourceTemplate == "" && call.SourceTemplate != "" {
		projectedPatch.sourceTemplate = call.SourceTemplate
		projectedPatch.sourceLine = call.SourceLine
	}
	for phaseName, raw := range call.Variants {
		projected, err := newValue(raw, make(map[visit]struct{}), 0)
		if err != nil {
			return fmt.Errorf("phase %q: %w", phaseName, err)
		}
		phaseIndex := -1
		for index := range projectedPatch.phases {
			if projectedPatch.phases[index].name == phaseName {
				phaseIndex = index
				break
			}
		}
		if phaseIndex < 0 {
			projectedPatch.phases = append(projectedPatch.phases, phase{name: phaseName, value: projected})
		} else {
			projectedPatch.phases[phaseIndex].value = projected
		}
	}
	return nil
}

// Validate verifies exact root ownership in constant time.
func (r *Root) Validate(owner any) error {
	if r == nil || r.seal != r || owner == nil || !sameOwner(r.owner, owner) {
		return errors.New("root has invalid provenance")
	}
	return nil
}

// PatchCount returns the number of projected patch records.
func (r *Root) PatchCount(owner any) (int, error) {
	if err := r.Validate(owner); err != nil {
		return 0, err
	}
	return r.patchCount, nil
}

// Visit walks every leaf patch in deterministic part and patch order.
func (r *Root) Visit(owner any, visitor func(PatchView) error) error {
	if err := r.Validate(owner); err != nil {
		return err
	}
	if visitor == nil {
		return errors.New("visitor is nil")
	}
	return r.visit(visitor)
}

func (r *Root) visit(visitor func(PatchView) error) error {
	if r.composite {
		for index := range r.parts {
			child := &r.parts[index]
			if err := child.root.Validate(child.owner); err != nil {
				return fmt.Errorf("part %d: %w", index, err)
			}
			if err := child.root.visit(visitor); err != nil {
				return err
			}
		}
		return nil
	}
	for index := range r.patches {
		if err := visitor(PatchView{root: r, owner: r.owner, patchIndex: index}); err != nil {
			return err
		}
	}
	return nil
}

// Metadata returns detached scalar metadata for the referenced patch.
func (v PatchView) Metadata() (Metadata, error) {
	projectedPatch, err := v.patch()
	if err != nil {
		return Metadata{}, err
	}
	return Metadata{
		Namespace: projectedPatch.namespace, Name: projectedPatch.name,
		APIVersion: projectedPatch.apiVersion, Kind: projectedPatch.kind,
		UID: projectedPatch.uid, ResourceVersion: projectedPatch.resourceVersion,
		SourceTemplate: projectedPatch.sourceTemplate, SourceLine: projectedPatch.sourceLine,
	}, nil
}

// Owner returns the exact owner token bound to the referenced leaf root.
func (v PatchView) Owner() (any, error) {
	if _, err := v.patch(); err != nil {
		return nil, err
	}
	return v.owner, nil
}

// VisitPhases walks the referenced patch's phases in sorted order.
func (v PatchView) VisitPhases(visitor func(PhaseView) error) error {
	projectedPatch, err := v.patch()
	if err != nil {
		return err
	}
	if visitor == nil {
		return errors.New("visitor is nil")
	}
	for index := range projectedPatch.phases {
		if err := visitor(PhaseView{
			root: v.root, owner: v.owner, patchIndex: v.patchIndex, phaseIndex: index,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (v PatchView) patch() (*patch, error) {
	if err := v.root.Validate(v.owner); err != nil || v.root.composite ||
		v.patchIndex < 0 || v.patchIndex >= len(v.root.patches) {
		return nil, errors.New("patch view has invalid provenance")
	}
	return &v.root.patches[v.patchIndex], nil
}

// Name returns the referenced phase name.
func (v PhaseView) Name() (string, error) {
	projectedPhase, err := v.phase()
	if err != nil {
		return "", err
	}
	return projectedPhase.name, nil
}

// Materialize returns a detached status object.
func (v PhaseView) Materialize() (map[string]any, error) {
	projectedPhase, err := v.phase()
	if err != nil {
		return nil, err
	}
	var materialized any
	if err := projectedPhase.value.materializeInto(&materialized); err != nil {
		return nil, err
	}
	object, ok := materialized.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("projected variant has type %T", materialized)
	}
	return object, nil
}

// Same reports exact authenticated phase identity.
func (v PhaseView) Same(other PhaseView) bool {
	return v.root == other.root && sameOwner(v.owner, other.owner) &&
		v.patchIndex == other.patchIndex && v.phaseIndex == other.phaseIndex
}

// BelongsTo reports whether the phase belongs to patchView.
func (v PhaseView) BelongsTo(patchView PatchView) bool {
	return v.root == patchView.root && sameOwner(v.owner, patchView.owner) &&
		v.patchIndex == patchView.patchIndex
}

func (v PhaseView) phase() (*phase, error) {
	if err := v.root.Validate(v.owner); err != nil || v.root.composite ||
		v.patchIndex < 0 || v.patchIndex >= len(v.root.patches) {
		return nil, errors.New("phase view has invalid provenance")
	}
	projectedPatch := &v.root.patches[v.patchIndex]
	if v.phaseIndex < 0 || v.phaseIndex >= len(projectedPatch.phases) {
		return nil, errors.New("phase view has invalid provenance")
	}
	return &projectedPatch.phases[v.phaseIndex], nil
}

func newValue(raw any, active map[visit]struct{}, depth int) (value, error) {
	if depth > maxValueDepth {
		return value{}, errors.New("value exceeds the incremental serialization depth limit")
	}
	switch typed := raw.(type) {
	case nil:
		return value{kind: valueNull}, nil
	case bool:
		return value{kind: valueBool, boolean: typed}, nil
	case string:
		return value{kind: valueString, text: typed}, nil
	case int:
		return value{kind: valueInt, numberType: numberInt, integer: int64(typed)}, nil
	case int8:
		return value{kind: valueInt, numberType: numberInt8, integer: int64(typed)}, nil
	case int16:
		return value{kind: valueInt, numberType: numberInt16, integer: int64(typed)}, nil
	case int32:
		return value{kind: valueInt, numberType: numberInt32, integer: int64(typed)}, nil
	case int64:
		return value{kind: valueInt, numberType: numberInt64, integer: typed}, nil
	case uint:
		return value{kind: valueUint, numberType: numberUint, unsigned: uint64(typed)}, nil
	case uint8:
		return value{kind: valueUint, numberType: numberUint8, unsigned: uint64(typed)}, nil
	case uint16:
		return value{kind: valueUint, numberType: numberUint16, unsigned: uint64(typed)}, nil
	case uint32:
		return value{kind: valueUint, numberType: numberUint32, unsigned: uint64(typed)}, nil
	case uint64:
		return value{kind: valueUint, numberType: numberUint64, unsigned: typed}, nil
	case float32:
		return newFloatValue(float64(typed), numberFloat32, uint64(math.Float32bits(typed)))
	case float64:
		return newFloatValue(typed, numberFloat64, math.Float64bits(typed))
	case []any:
		return newArrayValue(typed, active, depth)
	case map[string]any:
		return newObjectValue(typed, active, depth)
	default:
		return value{}, fmt.Errorf("value has unsupported type %T", raw)
	}
}

func newFloatValue(raw float64, numberType byte, bits uint64) (value, error) {
	if math.IsNaN(raw) || math.IsInf(raw, 0) {
		return value{}, errors.New("floating-point value is not finite")
	}
	return value{kind: valueFloat, numberType: numberType, floatBits: bits}, nil
}

func newArrayValue(typed []any, active map[visit]struct{}, depth int) (value, error) {
	marker := visit{kind: valueArray, ptr: reflect.ValueOf(typed).Pointer()}
	if err := startVisit(marker, active); err != nil {
		return value{}, err
	}
	if marker.ptr != 0 {
		defer delete(active, marker)
	}
	items := make([]value, len(typed))
	for index := range typed {
		projected, err := newValue(typed[index], active, depth+1)
		if err != nil {
			return value{}, fmt.Errorf("index %d: %w", index, err)
		}
		items[index] = projected
	}
	return value{kind: valueArray, array: items}, nil
}

func newObjectValue(typed map[string]any, active map[visit]struct{}, depth int) (value, error) {
	marker := visit{kind: valueObject, ptr: reflect.ValueOf(typed).Pointer()}
	if err := startVisit(marker, active); err != nil {
		return value{}, err
	}
	if marker.ptr != 0 {
		defer delete(active, marker)
	}
	names := make([]string, 0, len(typed))
	for name := range typed {
		names = append(names, name)
	}
	slices.Sort(names)
	fields := make([]field, len(names))
	for index, name := range names {
		projected, err := newValue(typed[name], active, depth+1)
		if err != nil {
			return value{}, fmt.Errorf("field %q: %w", name, err)
		}
		fields[index] = field{name: name, value: projected}
	}
	return value{kind: valueObject, object: fields}, nil
}

func startVisit(marker visit, active map[visit]struct{}) error {
	if marker.ptr == 0 {
		return nil
	}
	if _, exists := active[marker]; exists {
		return errors.New("value contains a reference cycle")
	}
	active[marker] = struct{}{}
	return nil
}

func (v *value) materializeInto(destination *any) error {
	switch v.kind {
	case valueNull:
		*destination = nil
		return nil
	case valueBool:
		*destination = v.boolean
		return nil
	case valueString:
		*destination = v.text
		return nil
	case valueInt:
		return materializeNumberInto(destination, v.materializeInt)
	case valueUint:
		return materializeNumberInto(destination, v.materializeUint)
	case valueFloat:
		return materializeNumberInto(destination, v.materializeFloat)
	case valueArray:
		result := make([]any, len(v.array))
		for index := range v.array {
			if err := v.array[index].materializeInto(&result[index]); err != nil {
				return fmt.Errorf("index %d: %w", index, err)
			}
		}
		*destination = result
		return nil
	case valueObject:
		result := make(map[string]any, len(v.object))
		for index := range v.object {
			projectedField := &v.object[index]
			var item any
			if err := projectedField.value.materializeInto(&item); err != nil {
				return fmt.Errorf("field %q: %w", projectedField.name, err)
			}
			result[projectedField.name] = item
		}
		*destination = result
		return nil
	default:
		return fmt.Errorf("projected variant has invalid kind %d", v.kind)
	}
}

func materializeNumberInto(destination *any, materialize func() (any, error)) error {
	item, err := materialize()
	if err != nil {
		return err
	}
	*destination = item
	return nil
}

func (v *value) materializeInt() (any, error) {
	switch v.numberType {
	case numberInt:
		return int(v.integer), nil
	case numberInt8:
		if v.integer < math.MinInt8 || v.integer > math.MaxInt8 {
			return nil, errors.New("projected variant overflows int8")
		}
		return int8(v.integer), nil
	case numberInt16:
		if v.integer < math.MinInt16 || v.integer > math.MaxInt16 {
			return nil, errors.New("projected variant overflows int16")
		}
		return int16(v.integer), nil
	case numberInt32:
		if v.integer < math.MinInt32 || v.integer > math.MaxInt32 {
			return nil, errors.New("projected variant overflows int32")
		}
		return int32(v.integer), nil
	case numberInt64:
		return v.integer, nil
	default:
		return nil, errors.New("projected variant has an invalid signed number type")
	}
}

func (v *value) materializeUint() (any, error) {
	switch v.numberType {
	case numberUint:
		return uint(v.unsigned), nil
	case numberUint8:
		if v.unsigned > math.MaxUint8 {
			return nil, errors.New("projected variant overflows uint8")
		}
		return uint8(v.unsigned), nil
	case numberUint16:
		if v.unsigned > math.MaxUint16 {
			return nil, errors.New("projected variant overflows uint16")
		}
		return uint16(v.unsigned), nil
	case numberUint32:
		if v.unsigned > math.MaxUint32 {
			return nil, errors.New("projected variant overflows uint32")
		}
		return uint32(v.unsigned), nil
	case numberUint64:
		return v.unsigned, nil
	default:
		return nil, errors.New("projected variant has an invalid unsigned number type")
	}
}

func (v *value) materializeFloat() (any, error) {
	switch v.numberType {
	case numberFloat32:
		if v.floatBits > math.MaxUint32 {
			return nil, errors.New("projected variant overflows float32 bits")
		}
		return math.Float32frombits(uint32(v.floatBits)), nil
	case numberFloat64:
		return math.Float64frombits(v.floatBits), nil
	default:
		return nil, errors.New("projected variant has an invalid floating-point number type")
	}
}

func sameOwner(left, right any) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	leftValue := reflect.ValueOf(left)
	rightValue := reflect.ValueOf(right)
	return leftValue.Type() == rightValue.Type() && leftValue.Comparable() && rightValue.Comparable() && left == right
}

func compareStrings(left, right string) int {
	switch {
	case left < right:
		return -1
	case left > right:
		return 1
	default:
		return 0
	}
}
