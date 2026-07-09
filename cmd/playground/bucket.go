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

// This file is intentionally NOT build-tagged for wasm: the resource-bucketing
// logic is pure Go (no syscall/js) so it compiles — and is unit-tested — on the
// normal `go test` path, while main.go (js && wasm) consumes it.
package main

import (
	"bytes"
	"errors"
	"io"
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testrunner"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/indexer"
)

// parseResources turns pasted example resources into the fixtures shape
// (watched-resource name -> []object). It accepts, interchangeably:
//   - a raw `kubectl get ... -o yaml` dump: a List (kind: List, items: [...]), a
//     single object, or a multi-document (--- separated) stream of either;
//   - the name-keyed fixtures shape used by validationTests.
//
// kubectl-style objects are bucketed by (apiVersion, kind) against the config's
// watchedResources and filtered by each watched resource's label/field selector,
// so an object lands in exactly the stores the controller's watchers would
// populate (e.g. a HAPTIC load-balancer Service appears in both `services` and
// `controller_services`; an app Service only in `services`). byKey supplies exact
// kinds from the schema bundle; resources without a bundled schema fall back to a
// singularized plural. The bucketing is resource-agnostic — it names no kinds.
func parseResources(cfg *config.Config, byKey map[string]schema.GroupVersionKind, data []byte) (map[string][]any, *BucketReport, error) {
	if strings.TrimSpace(string(data)) == "" {
		return map[string][]any{}, &BucketReport{}, nil
	}

	docs, err := decodeYAMLDocuments(data)
	if err != nil {
		return nil, nil, err
	}

	// Name-keyed fixtures shape: a single mapping with no top-level apiVersion/kind
	// whose values are all lists. Used verbatim (the validationTests path).
	if len(docs) == 1 && isFixturesShape(docs[0]) {
		f := toFixtures(docs[0])
		return f, fixturesReport(f), nil
	}

	index := buildWatchedIndex(cfg, byKey)
	fixtures := map[string][]any{}
	report := &BucketReport{}
	for _, doc := range docs {
		for _, obj := range expandList(doc) {
			if oc := bucketObject(cfg, index, obj, fixtures); oc != nil {
				report.Objects = append(report.Objects, *oc)
			}
		}
	}
	return fixtures, report, nil
}

// BucketOutcome records where one pasted object landed, or why it was dropped —
// the raw material for the playground's "what matched / what was dropped and why"
// feedback. Resource-agnostic: it carries whatever apiVersion/kind the object had.
type BucketOutcome struct {
	APIVersion string   `json:"apiVersion"`
	Kind       string   `json:"kind"`
	Namespace  string   `json:"namespace,omitempty"`
	Name       string   `json:"name,omitempty"`
	Buckets    []string `json:"buckets,omitempty"` // watched-resource names it matched
	Dropped    bool     `json:"dropped"`
	Reason     string   `json:"reason,omitempty"` // why it was dropped (empty if matched)
}

// BucketReport is the per-object bucketing outcome for one paste.
type BucketReport struct {
	Objects []BucketOutcome `json:"objects"`
}

// fixturesReport builds a trivial "all matched" report for the name-keyed
// fixtures shape (which is already bucketed, so nothing is dropped).
func fixturesReport(f map[string][]any) *BucketReport {
	r := &BucketReport{}
	for name := range f {
		for _, obj := range f[name] {
			m, _ := obj.(map[string]any)
			apiVersion, _ := m["apiVersion"].(string)
			kind, _ := m["kind"].(string)
			r.Objects = append(r.Objects, BucketOutcome{
				APIVersion: apiVersion, Kind: kind,
				Namespace: metaField(m, "namespace"), Name: metaField(m, "name"),
				Buckets: []string{name},
			})
		}
	}
	return r
}

// decodeYAMLDocuments splits a (possibly multi-document) YAML/JSON stream into
// generic maps, dropping empty documents. Uses apimachinery's decoder so numbers
// arrive as JSON-native float64 (unstructured deep-copy rejects bare Go ints).
func decodeYAMLDocuments(data []byte) ([]map[string]any, error) {
	dec := k8syaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
	var docs []map[string]any
	for {
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		if len(m) > 0 {
			docs = append(docs, m)
		}
	}
	return docs, nil
}

// isFixturesShape reports whether a doc is the name-keyed fixtures mapping (no
// top-level apiVersion/kind; every value a list) rather than a K8s object.
func isFixturesShape(doc map[string]any) bool {
	if len(doc) == 0 {
		return false
	}
	if _, ok := doc["kind"]; ok {
		return false
	}
	if _, ok := doc["apiVersion"]; ok {
		return false
	}
	for _, v := range doc {
		if _, ok := v.([]any); !ok {
			return false
		}
	}
	return true
}

// toFixtures adapts a name-keyed fixtures mapping into map[string][]any.
func toFixtures(doc map[string]any) map[string][]any {
	out := make(map[string][]any, len(doc))
	for name, v := range doc {
		if list, ok := v.([]any); ok {
			out[name] = list
		}
	}
	return out
}

// expandList returns the individual objects a doc contributes: a List's items,
// or the doc itself for a single object. Kind-less docs yield nothing.
func expandList(doc map[string]any) []any {
	kind, _ := doc["kind"].(string)
	if kind == "List" || strings.HasSuffix(kind, "List") {
		if items, ok := doc["items"].([]any); ok {
			return items
		}
	}
	if kind == "" {
		return nil
	}
	return []any{doc}
}

// buildWatchedIndex maps "<apiVersion>/<kind>" to the watched-resource names
// declaring that GVK. A resource may be watched under several names (services,
// controller_services), and under several candidate apiVersions.
func buildWatchedIndex(cfg *config.Config, byKey map[string]schema.GroupVersionKind) map[string][]string {
	index := map[string][]string{}
	for name := range cfg.WatchedResources {
		wr := cfg.WatchedResources[name]
		for _, apiVersion := range watchedAPIVersions(&wr) {
			kind := testrunner.SingularizeResourceType(wr.Resources)
			if gvk, ok := byKey[apiVersion+"|"+wr.Resources]; ok {
				kind = gvk.Kind
			}
			key := apiVersion + "/" + kind
			index[key] = append(index[key], name)
		}
	}
	return index
}

// watchedAPIVersions returns a watched resource's candidate apiVersions
// (the singular APIVersion, else the ordered APIVersions list).
func watchedAPIVersions(wr *config.WatchedResource) []string {
	if wr.APIVersion != "" {
		return []string{wr.APIVersion}
	}
	return wr.APIVersions
}

// bucketObject adds obj to every watched-resource bucket whose GVK matches AND
// whose label/field selector obj satisfies — mirroring the controller's watch
// filtering — and returns a BucketOutcome describing where it landed or why it
// was dropped (nil for a non-object).
func bucketObject(cfg *config.Config, index map[string][]string, obj any, fixtures map[string][]any) *BucketOutcome {
	m, ok := obj.(map[string]any)
	if !ok {
		return nil
	}
	apiVersion, _ := m["apiVersion"].(string)
	kind, _ := m["kind"].(string)
	oc := &BucketOutcome{APIVersion: apiVersion, Kind: kind, Namespace: metaField(m, "namespace"), Name: metaField(m, "name")}
	if apiVersion == "" || kind == "" {
		oc.Dropped = true
		oc.Reason = "object has no apiVersion/kind"
		return oc
	}
	candidates := index[apiVersion+"/"+kind]
	if len(candidates) == 0 {
		oc.Dropped = true
		oc.Reason = "kind " + apiVersion + "/" + kind + " is not watched by this config"
		return oc
	}
	var misses []string
	for _, name := range candidates {
		wr := cfg.WatchedResources[name]
		if matchesLabelSelector(wr.LabelSelector, m) && matchesFieldSelector(wr.FieldSelector, m) {
			fixtures[name] = append(fixtures[name], m)
			oc.Buckets = append(oc.Buckets, name)
		} else {
			misses = append(misses, name+" needs "+selectorDesc(&wr))
		}
	}
	if len(oc.Buckets) == 0 {
		oc.Dropped = true
		oc.Reason = "excluded by selector: " + strings.Join(misses, "; ")
	}
	return oc
}

// metaField returns metadata.<field> as a string ("" if absent).
func metaField(m map[string]any, field string) string {
	md, _ := m["metadata"].(map[string]any)
	s, _ := md[field].(string)
	return s
}

// selectorDesc renders a watched resource's label/field selectors for a
// human-readable "why it was dropped" message.
func selectorDesc(wr *config.WatchedResource) string {
	parts := make([]string, 0, len(wr.LabelSelector)+1)
	if wr.FieldSelector != "" {
		parts = append(parts, wr.FieldSelector)
	}
	for k, v := range wr.LabelSelector {
		parts = append(parts, k+"="+v)
	}
	if len(parts) == 0 {
		return "its selector"
	}
	return strings.Join(parts, ",")
}

// matchesLabelSelector reports whether obj's labels satisfy every entry of the
// equality-based selector (empty selector matches everything), matching how the
// controller builds a watched resource's server-side label selector.
func matchesLabelSelector(sel map[string]string, obj map[string]any) bool {
	if len(sel) == 0 {
		return true
	}
	labels := digStringMap(obj, "metadata", "labels")
	for k, v := range sel {
		if labels[k] != v {
			return false
		}
	}
	return true
}

// matchesFieldSelector reuses the controller's FieldSelectorMatcher so client-side
// field filtering behaves identically to the watchers'.
func matchesFieldSelector(expr string, obj map[string]any) bool {
	if expr == "" {
		return true
	}
	matcher, err := indexer.NewFieldSelectorMatcher(expr)
	if err != nil {
		return false
	}
	ok, err := matcher.Matches(obj)
	return err == nil && ok
}

// digStringMap walks obj along path and returns the string-valued entries of the
// map found there (nil if the path is absent or not a map).
func digStringMap(obj map[string]any, path ...string) map[string]string {
	cur := any(obj)
	for _, p := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[p]
	}
	m, ok := cur.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}
