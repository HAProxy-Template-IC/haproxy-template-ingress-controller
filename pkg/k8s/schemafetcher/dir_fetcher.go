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

package schemafetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/kube-openapi/pkg/validation/spec"
	"sigs.k8s.io/yaml"
)

// DirEntry captures everything DirFetcher learns from a single file
// on disk. The wire form of the input determines what's populated:
//
//   - Full CustomResourceDefinition (kubectl get crd <name> -o yaml):
//     Schema is the openAPIV3Schema for the served version; Plural is
//     spec.names.plural; GVK is fully resolved.
//
//   - Bare kube-openapi v3 spec.Schema with x-kubernetes-group-version-kind:
//     Schema is the parsed schema; Plural is empty (the bare form
//     doesn't carry it); GVK comes from the extension.
//
// Callers that need to map (apiVersion, resources-plural) → GVK (the
// shape OfflineGVKResolver wants) can only succeed when Plural is
// non-empty, i.e. for the CRD input shape. Bare-schema inputs work
// for typed-access at chart-render time but not for the offline
// resolver's plural lookup.
type DirEntry struct {
	GVK    schema.GroupVersionKind
	Schema *spec.Schema
	Plural string
	// Source is the on-disk path the entry was loaded from. Used in
	// error messages so operators know which file is malformed
	// without re-running with verbose logging.
	Source string
}

// DirFetcher implements [Fetcher] from a directory of CustomResourceDefinition
// YAML/JSON files and/or bare OpenAPI v3 schemas. It's the offline
// counterpart to [ClusterFetcher]: instead of asking the apiserver,
// callers point this fetcher at a directory of files extracted from a
// real cluster (or hand-authored). Mirrors the kubeconform pattern of
// `--schema-location` for users who already know that workflow.
//
// Two input shapes are accepted:
//
//  1. Full CRD wire format — apiVersion: apiextensions.k8s.io/v1,
//     kind: CustomResourceDefinition. The fetcher picks the schema
//     from spec.versions[i].schema.openAPIV3Schema for the served
//     version (`served: true`, `storage: true` preferred) and also
//     records spec.names.plural so OfflineGVKResolver can be
//     populated automatically. This is what `kubectl get crd X -o
//     yaml` produces.
//
//  2. Bare OpenAPI v3 spec.Schema with x-kubernetes-group-version-kind.
//     Same shape as the builtin/ package's embedded files. Use this
//     for hand-trimmed schemas where the CRD wrapper isn't needed.
//
// The two shapes mix freely in the same directory; the fetcher
// dispatches per file based on a top-level `kind: CustomResourceDefinition`
// probe.
//
// Concurrency: Fetch is read-only after construction, so it's safe
// for concurrent use without additional synchronization.
type DirFetcher struct {
	schemas map[schema.GroupVersionKind]*spec.Schema
	plurals map[apiVersionPlural]schema.GroupVersionKind
	source  string
}

// apiVersionPlural is the lookup key OfflineGVKResolver consumes
// (apiVersion = "<group>/<version>" form, plural = lowercase
// resources name). Kept package-private; callers use
// [DirFetcher.RegisterPluralsIn] to populate a resolver rather than
// reaching into the map directly.
type apiVersionPlural struct {
	apiVersion string
	plural     string
}

// NewDirFetcher loads every YAML/JSON file in the given directory
// (non-recursive) and returns a fetcher serving the schemas they
// describe. An empty or non-existent directory returns an empty
// fetcher (no error); misses surface as [IsNotFound]-able errors so
// callers can fall through to another fetcher when the directory is
// missing entries.
//
// Errors at parse time fail loudly with the offending filename so
// operators don't have to bisect a 50-CRD directory by hand. Files
// that don't have a top-level `kind: CustomResourceDefinition` and
// don't carry x-kubernetes-group-version-kind are reported as
// "no GVK identification" — the operator can either rewrap the
// bare schema in a CRD or add the extension.
func NewDirFetcher(dir string) (*DirFetcher, error) {
	f := &DirFetcher{
		schemas: map[schema.GroupVersionKind]*spec.Schema{},
		plurals: map[apiVersionPlural]schema.GroupVersionKind{},
		source:  dir,
	}
	if dir == "" {
		return f, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return f, nil
		}
		return nil, fmt.Errorf("reading schema directory %q: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".yaml" && ext != ".yml" && ext != ".json" {
			continue
		}
		path := filepath.Join(dir, name)
		dirEntries, err := parseSchemaFile(path)
		if err != nil {
			return nil, fmt.Errorf("schema file %s: %w", path, err)
		}
		if len(dirEntries) == 0 {
			// File was recognised as a non-schema K8s object and
			// silently skipped. Move to the next entry.
			continue
		}
		for i := range dirEntries {
			de := &dirEntries[i]
			f.schemas[de.GVK] = de.Schema
			if de.Plural != "" {
				key := apiVersionPlural{
					apiVersion: de.GVK.GroupVersion().String(),
					plural:     de.Plural,
				}
				f.plurals[key] = de.GVK
			}
		}
	}
	return f, nil
}

// Fetch implements [Fetcher]. Returns the in-memory schema for the
// requested GVK or [ErrSchemaNotAvailable] when the directory didn't
// contain a matching file. Components is always nil — the directory
// model assumes each file is self-contained (CRDs inline every shared
// shape, hand-trimmed bare schemas don't $ref out). Mirrors
// [MapFetcher]'s component-less contract.
//
// ctx is accepted to match the [Fetcher] interface; the implementation
// is pure-memory after construction, so cancellation isn't observed.
func (f *DirFetcher) Fetch(_ context.Context, gvk schema.GroupVersionKind) (*spec.Schema, map[string]spec.Schema, error) {
	sch, ok := f.schemas[gvk]
	if !ok {
		return nil, nil, &ErrSchemaNotAvailable{GVK: gvk, Cause: errNotFound}
	}
	return sch, nil, nil
}

// PluralsFor returns the registered (apiVersion, plural → GVK)
// mappings DirFetcher learned from CRDs in its directory. Empty for
// directories that only contain bare schemas (which don't carry the
// resources-plural). Used by the offline validate path to populate
// [pkg/controller/typebootstrap.OfflineGVKResolver] without
// hardcoding a table.
//
// The returned map is a fresh copy; callers may mutate it freely.
func (f *DirFetcher) PluralsFor() map[string]map[string]schema.GroupVersionKind {
	out := map[string]map[string]schema.GroupVersionKind{}
	for k, gvk := range f.plurals {
		bucket, ok := out[k.apiVersion]
		if !ok {
			bucket = map[string]schema.GroupVersionKind{}
			out[k.apiVersion] = bucket
		}
		bucket[k.plural] = gvk
	}
	return out
}

// Source returns the directory path the fetcher was loaded from.
// Empty when the fetcher was constructed with an empty dir argument.
// Useful for diagnostic messages — when an offline validate run
// reports "schema not found for X/Y/Z", knowing which directory
// was searched is the first thing the operator needs.
func (f *DirFetcher) Source() string {
	return f.source
}

// Len reports the number of schemas currently held. Useful for
// startup-log "loaded N schemas from <dir>" diagnostics.
func (f *DirFetcher) Len() int {
	return len(f.schemas)
}

// parseSchemaFile reads a single file and returns the DirEntries it
// describes. A file may be:
//
//   - A [apiextensionsv1.CustomResourceDefinition] (one or more served
//     versions, each yielding a DirEntry).
//   - A bare [spec.Schema] with x-kubernetes-group-version-kind
//     identifying the GVK.
//   - Anything else, in which case nil entries + nil error is returned
//     (the file is silently skipped). This tolerates extraction
//     scripts that copy adjacent YAMLs the operator didn't curate
//     — Gateway API's `config/crd/standard` directory, for example,
//     includes a `ValidatingAdmissionPolicy` alongside the CRDs, and
//     we don't want that to block the entire directory load.
//
// Hard errors are reserved for files that LOOK like a CRD or a bare
// schema but fail to parse (malformed shape inside a recognised
// container) — that's a real operator misconfiguration worth
// surfacing rather than swallowing.
func parseSchemaFile(path string) ([]DirEntry, error) {
	// filepath.Clean satisfies gosec G304: the path is already
	// composed via filepath.Join from a directory we accept on
	// the CLI and a filename returned by os.ReadDir on that
	// directory, so it can't escape the user-supplied root, but
	// gosec doesn't see the compositional reasoning.
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	// Probe top-level kind without committing to a full parse.
	var probe struct {
		Kind       string `json:"kind"`
		APIVersion string `json:"apiVersion"`
	}
	if err := yaml.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("probe: %w", err)
	}
	switch {
	case probe.Kind == "CustomResourceDefinition":
		return parseCRDFile(raw, path)
	case probe.Kind != "" && probe.APIVersion != "":
		// Recognised K8s object that isn't a CRD (e.g.
		// ValidatingAdmissionPolicy ships alongside Gateway API
		// CRDs in `config/crd/standard`). Silently skip.
		return nil, nil
	default:
		// No kind/apiVersion at the top level — try bare schema.
		// If that path also doesn't identify a GVK, the bare-schema
		// parser returns a clear error.
		return parseBareSchemaFile(raw, path)
	}
}

// parseCRDFile extracts DirEntries from a full
// CustomResourceDefinition. One entry per served version, but the
// caller usually only references one — we record all so a switch
// of cluster API version doesn't require re-extraction. Storage
// version is preferred when multiple are served; otherwise the
// first served version wins.
func parseCRDFile(raw []byte, path string) ([]DirEntry, error) {
	var crd apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(raw, &crd); err != nil {
		return nil, fmt.Errorf("parse CRD: %w", err)
	}
	plural := crd.Spec.Names.Plural
	if plural == "" {
		return nil, fmt.Errorf("CRD %s: spec.names.plural is empty", crd.Name)
	}
	var out []DirEntry
	for i := range crd.Spec.Versions {
		v := &crd.Spec.Versions[i]
		if !v.Served {
			continue
		}
		if v.Schema == nil || v.Schema.OpenAPIV3Schema == nil {
			// Tolerated: some CRDs ship without schemas (legacy /
			// dev shapes). The offline validate path just skips
			// them — operators with such CRDs in their dir get
			// a clearer error at Fetch time than during parse.
			continue
		}
		converted, err := convertJSONSchemaPropsExported(v.Schema.OpenAPIV3Schema)
		if err != nil {
			return nil, fmt.Errorf("converting %s/%s schema: %w", crd.Name, v.Name, err)
		}
		out = append(out, DirEntry{
			GVK: schema.GroupVersionKind{
				Group:   crd.Spec.Group,
				Version: v.Name,
				Kind:    crd.Spec.Names.Kind,
			},
			Schema: converted,
			Plural: plural,
			Source: path,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("CRD %s: no served versions with schema", crd.Name)
	}
	return out, nil
}

// parseBareSchemaFile interprets the file as a top-level
// [spec.Schema] and reads its x-kubernetes-group-version-kind
// extension to identify the GVK. This matches the format the
// builtin/ package embeds.
func parseBareSchemaFile(raw []byte, path string) ([]DirEntry, error) {
	// yaml.Unmarshal goes through JSON internally, which matches
	// spec.Schema's JSON-tag-driven shape exactly.
	var sch spec.Schema
	if err := yaml.Unmarshal(raw, &sch); err != nil {
		return nil, fmt.Errorf("parse bare schema: %w", err)
	}
	ext, ok := sch.Extensions["x-kubernetes-group-version-kind"]
	if !ok {
		return nil, fmt.Errorf("no GVK identification: file is not a CRD and bare schema lacks x-kubernetes-group-version-kind")
	}
	entries, ok := ext.([]any)
	if !ok || len(entries) == 0 {
		return nil, fmt.Errorf("malformed x-kubernetes-group-version-kind: expected non-empty array, got %T", ext)
	}
	// One file may declare multiple GVKs (e.g. some upstream
	// schemas list both v1beta1 and v1 with the same body).
	out := make([]DirEntry, 0, len(entries))
	for _, e := range entries {
		m, ok := e.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("malformed x-kubernetes-group-version-kind entry: expected map, got %T", e)
		}
		gvk := schema.GroupVersionKind{
			Group:   stringOrEmpty(m["group"]),
			Version: stringOrEmpty(m["version"]),
			Kind:    stringOrEmpty(m["kind"]),
		}
		if gvk.Kind == "" || gvk.Version == "" {
			return nil, fmt.Errorf("malformed x-kubernetes-group-version-kind entry: missing kind/version (group=%q kind=%q version=%q)",
				gvk.Group, gvk.Kind, gvk.Version)
		}
		// Bare schemas don't carry the plural; leave it empty.
		// Operators who need offline-GVK resolution from a plural
		// must use the full CRD format.
		out = append(out, DirEntry{
			GVK:    gvk,
			Schema: &sch,
			Source: path,
		})
	}
	return out, nil
}

// stringOrEmpty extracts a string value from a map[string]any field
// or returns "" when the field is missing / not a string. Tolerant
// because YAML decoders can give us interface{} for short tokens.
func stringOrEmpty(v any) string {
	s, _ := v.(string)
	return s
}

// convertJSONSchemaPropsExported wraps the package-private
// convertJSONSchemaProps so dir_fetcher.go can reuse the same
// JSONSchemaProps → spec.Schema conversion the ClusterFetcher uses.
// Same JSON round-trip: simpler than hand-mapping every field and
// the alternative would diverge from upstream as JSONSchemaProps
// grows.
func convertJSONSchemaPropsExported(in *apiextensionsv1.JSONSchemaProps) (*spec.Schema, error) {
	data, err := json.Marshal(in)
	if err != nil {
		return nil, fmt.Errorf("marshal JSONSchemaProps: %w", err)
	}
	var out spec.Schema
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("unmarshal into spec.Schema: %w", err)
	}
	return &out, nil
}
