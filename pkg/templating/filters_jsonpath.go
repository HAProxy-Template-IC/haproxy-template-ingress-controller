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
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"gitlab.com/haproxy-haptic/scriggo/native"
)

// This file implements three generic, resource-agnostic engine helpers for
// accessing and mutating watched resources by dynamic name and concrete
// JSONPath (RULE #1: they know nothing about any specific resource). The
// chart's governance layer is the first consumer, but nothing here is
// governance-specific:
//
//   - resource(name)          — dynamic-by-name access to a watched resource,
//                               returning the same per-render objects the typed
//                               `resources.<name>.List()` returns (so a write is
//                               observed downstream).
//   - jsonpathGet(item, path) — read a CONCRETE JSONPath out of any resource.
//   - jsonpathSet(item, path, value) — write a CONCRETE JSONPath into any
//                               resource (annotations + any concrete field).
//
// "Concrete" means dotted keys, bracket-quoted keys (`['k8s.io/x']`), and array
// indices (`[0]`). Filtered/wildcard expressions (`[?(...)]`, `[*]`) are NOT
// supported — they are ambiguous as a write target — and callers validate that
// at config time.
//
// CONCURRENCY CONTRACT. `jsonpathSet` mutates the memoized per-render resource
// pointer in place, and `jsonpathGet`'s annotation fast path reads it — both
// WITHOUT synchronization. The memoized pointers are shared across the whole
// render: the parallel aux-file phase (renderer.renderAuxiliaryFiles) and any
// `shard_slice`/`{{ go }}` goroutines read the SAME pointers concurrently (this
// is exactly why builder.go guards the wrap path with a mutex). These two
// helpers are therefore only safe to call from the single synchronous
// main-config render (service.go renders haproxy.cfg fully before spawning the
// aux goroutines), where governance runs today. Do NOT call `jsonpathSet` (or
// `jsonpathGet` on a value another goroutine may be writing) from an aux-file
// template, a `{{ go }}` block, or any concurrently-rendered snippet — a Go map
// mutation racing a concurrent read is a data race. If a future feature needs
// concurrent mutation, add synchronization here (or a copy-on-write path)
// first.

// pathSeg is one segment of a parsed concrete JSONPath: either a map key or an
// array index.
type pathSeg struct {
	key     string
	index   int
	isIndex bool
}

// parseConcreteJSONPath parses a concrete JSONPath into segments. It accepts an
// optional leading `$` and `.`, dotted identifiers, `['quoted key']` (single or
// double quotes — the only safe way to express a key containing `.` or `/`), and
// `[<int>]` array indices. It rejects filtered/wildcard expressions.
func parseConcreteJSONPath(path string) ([]pathSeg, error) {
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
			seg, next, err = parseBracketSeg(path, p, i)
		} else {
			seg, next, err = parseDotSeg(path, p, i)
		}
		if err != nil {
			return nil, err
		}
		// Reject an empty non-index segment (a `..` run or a `['']` key) instead
		// of silently dropping it, so a malformed admin path like `metadata..name`
		// fails config validation rather than quietly resolving to `metadata.name`.
		if !seg.isIndex && seg.key == "" {
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
func parseBracketSeg(path, p string, i int) (pathSeg, int, error) {
	end := strings.IndexByte(p[i:], ']')
	if end < 0 {
		return pathSeg{}, 0, fmt.Errorf("unterminated '[' in path %q", path)
	}
	inner := strings.TrimSpace(p[i+1 : i+end])
	next := i + end + 1
	if len(inner) >= 2 && (inner[0] == '\'' || inner[0] == '"') && inner[len(inner)-1] == inner[0] {
		return pathSeg{key: inner[1 : len(inner)-1]}, next, nil
	}
	idx, err := strconv.Atoi(inner)
	if err != nil {
		return pathSeg{}, 0, fmt.Errorf("path %q: unsupported bracket %q (filtered/wildcard expressions are not supported)", path, inner)
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
	if key == "*" || strings.ContainsAny(key, "?@()") {
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

// scriggoResource returns the per-render items of the watched resource named
// `name` — the same objects `resources.<name>.List()` returns (memoized, so a
// jsonpathSet write is observed downstream). Returns an empty slice for an
// unknown/absent resource. Registered as the `resource` template function.
func scriggoResource(env native.Env, name string) []any {
	goctx := env.Context()
	if goctx == nil {
		return nil
	}
	renderCtx, ok := goctx.Value(RenderContextContextKey).(map[string]any)
	if !ok {
		return nil
	}
	res := renderCtx["resources"]
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
	listFn := inner.FieldByName("List")
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

// scriggoJSONPathSet writes a concrete JSONPath into a resource item, in place,
// so downstream reads of the same (memoized) item observe it. Returns true on
// success. Annotation paths on a typed item avoid a json round-trip; other
// concrete paths marshal the item to a map, set the value, and unmarshal back
// into the same pointer. Untyped map items are also supported (set directly).
// Registered as `jsonpathSet`.
//
// Not safe under concurrent render — see the CONCURRENCY CONTRACT at the top of
// this file. Callable only from the synchronous main-config render.
func scriggoJSONPathSet(item any, path string, value any) bool {
	if item == nil {
		return false
	}
	segs, err := parseConcreteJSONPath(path)
	if err != nil {
		return false
	}
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
	switch s := v.(type) {
	case string:
		return s
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", s)
	}
}
