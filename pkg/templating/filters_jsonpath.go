// Copyright 2025 Philipp Hossner
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
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"gitlab.com/haproxy-haptic/scriggo/native"
)

// This file implements generic, resource-agnostic engine helpers for
// accessing and mutating watched resources by dynamic name and concrete
// JSONPath (RULE #1: they know nothing about any specific resource). The
// chart's governance layer is the first consumer, but nothing here is
// governance-specific:
//
//   - resource(name)          — dynamic-by-name access to a watched resource.
//   - jsonpathGet(item, path) — read a CONCRETE JSONPath out of any resource.
//   - jsonpathSet(item, path, value) — write a CONCRETE JSONPath into a
//                               detached or template-local resource value.
//   - deriveResourceJSONPath(item, path, value) — return a detached value.
//
// "Concrete" means dotted keys, bracket-quoted keys (`['k8s.io/x']`), and array
// indices (`[0]`). Filtered/wildcard expressions (`[?(...)]`, `[*]`) are NOT
// supported — they are ambiguous as a write target — and callers validate that
// at config time.
//
// Watched-resource values are immutable for a render. jsonpathSet fails the
// render before changing one; deriveResource is the copy-on-write path.

// pathSeg is one segment of a parsed JSONPath.
type pathSeg struct {
	key        string
	index      int
	isIndex    bool
	anyElement bool
}

// ConcreteJSONPath is a parsed path that can test object-field presence.
type ConcreteJSONPath struct {
	segments []pathSeg
}

// Equal reports whether both paths have the same compiled representation.
func (p ConcreteJSONPath) Equal(other ConcreteJSONPath) bool {
	return (p.segments == nil) == (other.segments == nil) && slices.Equal(p.segments, other.segments)
}

// CompileConcreteJSONPath validates and parses one concrete JSONPath.
func CompileConcreteJSONPath(path string) (ConcreteJSONPath, error) {
	segments, err := parseConcreteJSONPath(path)
	if err != nil {
		return ConcreteJSONPath{}, err
	}
	return ConcreteJSONPath{segments: segments}, nil
}

// Exists reports whether every segment exists. A final null value still exists.
func (p ConcreteJSONPath) Exists(item any) (bool, error) {
	if len(p.segments) == 0 {
		return false, fmt.Errorf("concrete JSONPath is not compiled")
	}
	root, err := itemToMap(item)
	if err != nil {
		return false, err
	}
	_, exists := getAtPath(root, p.segments)
	return exists, nil
}

// ExistenceJSONPath is a parsed path that may select any array element.
type ExistenceJSONPath struct {
	segments []pathSeg
}

// Equal reports whether both paths have the same compiled representation.
func (p ExistenceJSONPath) Equal(other ExistenceJSONPath) bool {
	return (p.segments == nil) == (other.segments == nil) && slices.Equal(p.segments, other.segments)
}

// CompileExistenceJSONPath validates a path used by a presence predicate.
func CompileExistenceJSONPath(path string) (ExistenceJSONPath, error) {
	segments, err := parseJSONPath(path, true)
	if err != nil {
		return ExistenceJSONPath{}, err
	}
	return ExistenceJSONPath{segments: segments}, nil
}

// Exists reports whether any selected branch contains every remaining segment.
func (p ExistenceJSONPath) Exists(item any) (bool, error) {
	if len(p.segments) == 0 {
		return false, fmt.Errorf("existence JSONPath is not compiled")
	}
	root, err := itemToMap(item)
	if err != nil {
		return false, err
	}
	return existsAtPath(root, p.segments), nil
}

// parseConcreteJSONPath parses a concrete JSONPath into segments. It accepts an
// optional leading `$` and `.`, dotted identifiers, `['quoted key']` (single or
// double quotes — the only safe way to express a key containing `.` or `/`), and
// `[<int>]` array indices. It rejects filtered/wildcard expressions.
func parseConcreteJSONPath(path string) ([]pathSeg, error) {
	return parseJSONPath(path, false)
}

func parseJSONPath(path string, allowAnyElement bool) ([]pathSeg, error) {
	p := strings.TrimPrefix(strings.TrimSpace(path), "$")
	p = strings.TrimPrefix(p, ".")
	var segs []pathSeg
	for i := 0; i < len(p); {
		var (
			seg  pathSeg
			next int
			err  error
		)
		if p[i] == '[' {
			seg, next, err = parseBracketSeg(path, p, i, allowAnyElement)
		} else {
			seg, next, err = parseDotSeg(path, p, i)
		}
		if err != nil {
			return nil, err
		}
		// Reject an empty non-index segment (a `..` run or a `['']` key) instead
		// of silently dropping it, so a malformed admin path like `metadata..name`
		// fails config validation rather than quietly resolving to `metadata.name`.
		if !seg.isIndex && !seg.anyElement && seg.key == "" {
			return nil, fmt.Errorf("path %q has an empty segment", path)
		}
		segs = append(segs, seg)
		i = next
		if i < len(p) && p[i] == '.' {
			i++
			if i == len(p) {
				return nil, fmt.Errorf("path %q ends with a trailing '.'", path)
			}
		}
	}
	if len(segs) == 0 {
		return nil, fmt.Errorf("path %q resolves to no segments", path)
	}
	return segs, nil
}

// parseBracketSeg parses a `[...]` segment starting at p[i] (p[i] == '['). It
// accepts a quoted key (`['k8s.io/x']`) or an integer index (`[0]`) and returns
// the segment plus the index just past the closing ']'.
func parseBracketSeg(path, p string, i int, allowAnyElement bool) (pathSeg, int, error) {
	end := strings.IndexByte(p[i:], ']')
	if end < 0 {
		return pathSeg{}, 0, fmt.Errorf("unterminated '[' in path %q", path)
	}
	inner := strings.TrimSpace(p[i+1 : i+end])
	next := i + end + 1
	if inner == "*" && allowAnyElement {
		return pathSeg{anyElement: true}, next, nil
	}
	if len(inner) >= 2 && (inner[0] == '\'' || inner[0] == '"') && inner[len(inner)-1] == inner[0] {
		return pathSeg{key: inner[1 : len(inner)-1]}, next, nil
	}
	idx, err := strconv.Atoi(inner)
	if err != nil {
		if allowAnyElement {
			return pathSeg{}, 0, fmt.Errorf("path %q: unsupported bracket %q (filtered expressions are not supported)", path, inner)
		}
		return pathSeg{}, 0, fmt.Errorf("path %q: unsupported bracket %q (filtered/wildcard expressions are not supported)", path, inner)
	}
	if idx < 0 {
		return pathSeg{}, 0, fmt.Errorf("path %q: array index must not be negative", path)
	}
	return pathSeg{index: idx, isIndex: true}, next, nil
}

// parseDotSeg parses a dotted identifier segment starting at p[i]. It returns
// the segment and the index just past it (the next '.' or '[' or end of input),
// rejecting wildcard/filter segments.
func parseDotSeg(path, p string, i int) (pathSeg, int, error) {
	j := i
	for j < len(p) && p[j] != '.' && p[j] != '[' {
		j++
	}
	key := p[i:j]
	if strings.Contains(key, "]") {
		return pathSeg{}, 0, fmt.Errorf("path %q: unexpected ']' in segment %q", path, key)
	}
	if strings.ContainsAny(key, "*?@()") {
		return pathSeg{}, 0, fmt.Errorf("path %q: wildcard/filter segment %q is not supported", path, key)
	}
	return pathSeg{key: key}, j, nil
}

// getAtPath navigates root (a decoded map[string]any / []any tree) by segs.
func getAtPath(root any, segs []pathSeg) (any, bool) {
	cur := root
	for _, s := range segs {
		if cur == nil {
			return nil, false
		}
		if s.isIndex {
			arr, ok := cur.([]any)
			if !ok || s.index < 0 || s.index >= len(arr) {
				return nil, false
			}
			cur = arr[s.index]
			continue
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		v, exists := m[s.key]
		if !exists {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

func existsAtPath(value any, segments []pathSeg) bool {
	if len(segments) == 0 {
		return true
	}
	segment := segments[0]
	if segment.anyElement {
		items, ok := value.([]any)
		if !ok {
			return false
		}
		for _, item := range items {
			if existsAtPath(item, segments[1:]) {
				return true
			}
		}
		return false
	}
	if segment.isIndex {
		items, ok := value.([]any)
		return ok && segment.index >= 0 && segment.index < len(items) &&
			existsAtPath(items[segment.index], segments[1:])
	}
	object, ok := value.(map[string]any)
	if !ok {
		return false
	}
	next, exists := object[segment.key]
	return exists && existsAtPath(next, segments[1:])
}

// setAtPath sets value at segs within root (a map[string]any tree), creating
// intermediate map nodes as needed. Array indices must already exist (we never
// grow arrays — injecting into a specific array element that isn't there is
// ambiguous). Returns an error if an intermediate node has the wrong shape.
func setAtPath(root map[string]any, segs []pathSeg, value any) error {
	if len(segs) == 0 {
		return fmt.Errorf("empty path")
	}
	var cur any = root
	for i := 0; i < len(segs)-1; i++ {
		s := segs[i]
		if s.isIndex {
			arr, ok := cur.([]any)
			if !ok || s.index < 0 || s.index >= len(arr) {
				return fmt.Errorf("cannot descend into array index %d", s.index)
			}
			cur = arr[s.index]
			continue
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return fmt.Errorf("cannot descend into %q: not an object", s.key)
		}
		next, exists := m[s.key]
		if !exists || next == nil {
			created := map[string]any{}
			m[s.key] = created
			cur = created
			continue
		}
		cur = next
	}
	last := segs[len(segs)-1]
	if last.isIndex {
		arr, ok := cur.([]any)
		if !ok || last.index < 0 || last.index >= len(arr) {
			return fmt.Errorf("cannot set array index %d", last.index)
		}
		arr[last.index] = value
		return nil
	}
	m, ok := cur.(map[string]any)
	if !ok {
		return fmt.Errorf("cannot set key %q: parent is not an object", last.key)
	}
	m[last.key] = value
	return nil
}

// annotationKey returns the annotation key if segs is exactly
// metadata.annotations.<key>; the fast path avoids a json round-trip for the
// overwhelmingly common inject/validate target.
func annotationKey(segs []pathSeg) (string, bool) {
	if len(segs) == 3 && segs[0].key == "metadata" && segs[1].key == "annotations" && !segs[2].isIndex {
		return segs[2].key, true
	}
	return "", false
}

// annotationsMap returns the addressable Metadata.Annotations map of a typed
// resource pointer via reflection, or an invalid Value if item is not a struct
// pointer with that field. Current typegen output represents Metadata as an
// embedded value struct; a pointer Metadata is also dereferenced so the fast
// path survives a typegen convention change. Anything else (no Metadata, no
// Annotations map, or an unsettable field) returns an invalid Value, and the
// callers fall back to the (functionally identical) JSON round-trip path.
func annotationsMap(item any) reflect.Value {
	v := reflect.ValueOf(item)
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return reflect.Value{}
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return reflect.Value{}
	}
	meta := v.FieldByName("Metadata")
	for meta.Kind() == reflect.Pointer {
		if meta.IsNil() {
			return reflect.Value{}
		}
		meta = meta.Elem()
	}
	if !meta.IsValid() || meta.Kind() != reflect.Struct {
		return reflect.Value{}
	}
	anns := meta.FieldByName("Annotations")
	if !anns.IsValid() || anns.Kind() != reflect.Map || !anns.CanSet() {
		return reflect.Value{}
	}
	return anns
}

// itemToMap decodes any resource item (typed *T or untyped map) into a
// map[string]any via json for path navigation.
func itemToMap(item any) (map[string]any, error) {
	if m, ok := item.(map[string]any); ok {
		return m, nil
	}
	data, err := json.Marshal(item)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// DeriveResourceJSONPath returns a detached resource value with one concrete path changed.
func DeriveResourceJSONPath(item any, path string, value any) (any, error) {
	if item == nil {
		return nil, fmt.Errorf("cannot derive a nil resource")
	}
	segs, err := parseConcreteJSONPath(path)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(item)
	if err != nil {
		return nil, fmt.Errorf("encoding resource: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var derived map[string]any
	if err := decoder.Decode(&derived); err != nil {
		return nil, fmt.Errorf("decoding resource: %w", err)
	}
	if _, annotation := annotationKey(segs); annotation {
		value = tostringValue(value)
	}
	if err := setAtPath(derived, segs, value); err != nil {
		return nil, err
	}
	return normalizeDerivedNumbers(derived)
}

func normalizeDerivedNumbers(value any) (any, error) {
	switch typed := value.(type) {
	case json.Number:
		if integer, err := strconv.ParseInt(string(typed), 10, 64); err == nil {
			return integer, nil
		}
		decimal, err := strconv.ParseFloat(string(typed), 64)
		if err != nil {
			return nil, err
		}
		return decimal, nil
	case map[string]any:
		for key, item := range typed {
			normalized, err := normalizeDerivedNumbers(item)
			if err != nil {
				return nil, err
			}
			typed[key] = normalized
		}
	case []any:
		for index, item := range typed {
			normalized, err := normalizeDerivedNumbers(item)
			if err != nil {
				return nil, err
			}
			typed[index] = normalized
		}
	}
	return value, nil
}

// scriggoResource returns the per-render items of the watched resource named
// `name`. Returns an empty slice for an unknown/absent resource. Registered as
// the `resource` template function.
func scriggoResource(env native.Env, name string) []any {
	goctx := env.Context()
	if goctx == nil {
		return nil
	}
	res, ok := lookupRenderContextValue(goctx, declResources)
	if !ok {
		return nil
	}
	if res == nil {
		return nil
	}
	rv := reflect.ValueOf(res)
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return nil
	}
	// Match the struct field by its json tag (== the watched-resource name the
	// builder set), so we don't need to replicate typegen's Go-field-name rule.
	rt := rv.Type()
	var field reflect.Value
	for i := 0; i < rt.NumField(); i++ {
		if tag := rt.Field(i).Tag.Get("json"); strings.Split(tag, ",")[0] == name {
			field = rv.Field(i)
			break
		}
	}
	if !field.IsValid() || (field.Kind() == reflect.Pointer && field.IsNil()) {
		return nil
	}
	inner := field
	for inner.Kind() == reflect.Pointer {
		inner = inner.Elem()
	}
	listFn := inner.FieldByName(memberList)
	if !listFn.IsValid() || listFn.Kind() != reflect.Func {
		return nil
	}
	out := listFn.Call(nil)
	if len(out) != 1 {
		return nil
	}
	slice := out[0]
	if slice.Kind() != reflect.Slice {
		return nil
	}
	items := make([]any, slice.Len())
	for i := range items {
		items[i] = slice.Index(i).Interface()
	}
	return items
}

// scriggoJSONPathGet reads a concrete JSONPath out of a resource item. Returns
// nil if the path is absent or unparseable. Registered as `jsonpathGet`.
func scriggoJSONPathGet(item any, path string) any {
	if item == nil {
		return nil
	}
	segs, err := parseConcreteJSONPath(path)
	if err != nil {
		return nil
	}
	// Annotation fast path: no json round-trip.
	if key, ok := annotationKey(segs); ok {
		if anns := annotationsMap(item); anns.IsValid() {
			v := anns.MapIndex(reflect.ValueOf(key))
			if !v.IsValid() {
				return nil
			}
			return v.Interface()
		}
	}
	m, err := itemToMap(item)
	if err != nil {
		return nil
	}
	v, ok := getAtPath(m, segs)
	if !ok {
		return nil
	}
	return v
}

// scriggoJSONPathSet writes a concrete JSONPath into a detached or local item.
func scriggoJSONPathSet(env native.Env, item any, path string, value any) bool {
	if item == nil {
		return false
	}
	segs, err := parseConcreteJSONPath(path)
	if err != nil {
		return false
	}
	if err := immutableNativeMutationError(env, item); err != nil {
		env.Stop(err)
		return false
	}
	return setJSONPathSegments(item, segs, value)
}

func setJSONPath(item any, path string, value any) bool {
	if item == nil {
		return false
	}
	segs, err := parseConcreteJSONPath(path)
	if err != nil {
		return false
	}
	return setJSONPathSegments(item, segs, value)
}

func setJSONPathSegments(item any, segs []pathSeg, value any) bool {
	// Annotation fast path (typed item): reflect-set the map directly.
	if key, ok := annotationKey(segs); ok {
		if anns := annotationsMap(item); anns.IsValid() {
			if anns.IsNil() {
				anns.Set(reflect.MakeMap(anns.Type()))
			}
			anns.SetMapIndex(reflect.ValueOf(key), reflect.ValueOf(tostringValue(value)))
			return true
		}
	}
	// Untyped map item: set directly.
	if m, ok := item.(map[string]any); ok {
		return setAtPath(m, segs, value) == nil
	}
	// Typed item: json round-trip in place.
	data, err := json.Marshal(item)
	if err != nil {
		return false
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return false
	}
	if err := setAtPath(m, segs, value); err != nil {
		return false
	}
	newData, err := json.Marshal(m)
	if err != nil {
		return false
	}
	return json.Unmarshal(newData, item) == nil
}

// tostringValue coerces a value to string for annotation storage (annotations
// are map[string]string).
func tostringValue(v any) string {
	return mustDeterministicScalarText("jsonpathSet", v)
}
