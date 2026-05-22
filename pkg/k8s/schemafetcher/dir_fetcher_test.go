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
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/kube-openapi/pkg/validation/spec"
)

// minimalWidgetCRD returns a fixed-shape minimal CRD YAML for the
// Widget kind in group example.com/v1. The tests don't need to vary
// these — every parameter would only exercise string concatenation,
// never the parser's group / version / plural handling paths. The
// fixture is constant so a future reader doesn't have to chase
// what's actually varying.
func minimalWidgetCRD() string {
	return `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.example.com
spec:
  group: example.com
  names:
    kind: Widget
    plural: widgets
    singular: widget
    listKind: WidgetList
  scope: Namespaced
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              properties:
                example:
                  type: string
`
}

// bareSchemaJSON returns a kube-openapi v3 schema JSON with the
// x-kubernetes-group-version-kind extension identifying its GVK.
// Matches the format the builtin/ package's embedded files use.
func bareSchemaJSON(group, version, kind string) string {
	return `{
  "type": "object",
  "x-kubernetes-group-version-kind": [
    {"group": "` + group + `", "version": "` + version + `", "kind": "` + kind + `"}
  ],
  "properties": {
    "spec": {"type": "object"}
  }
}`
}

func TestDirFetcher_LoadsCRDAndServesSchema(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "widget.yaml"),
		[]byte(minimalWidgetCRD()),
		0o600))

	f, err := NewDirFetcher(dir)
	require.NoError(t, err)
	require.Equal(t, 1, f.Len())

	sch, _, err := f.Fetch(context.Background(), schema.GroupVersionKind{
		Group: "example.com", Version: "v1", Kind: "Widget",
	})
	require.NoError(t, err)
	require.NotNil(t, sch)
	// Verify the openAPIV3Schema flowed through the JSON round-trip
	// — nested property must be readable so callers using the
	// schema for typegen see the same shape the cluster fetcher
	// would have served.
	specProp, ok := sch.Properties["spec"]
	require.True(t, ok, "schema must carry top-level spec property")
	exampleProp, ok := specProp.Properties["example"]
	require.True(t, ok, "spec.example survives conversion")
	assert.Equal(t, []string{"string"}, []string(exampleProp.Type))
}

func TestDirFetcher_RegistersCRDPlurals(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "widget.yaml"),
		[]byte(minimalWidgetCRD()),
		0o600))

	f, err := NewDirFetcher(dir)
	require.NoError(t, err)

	plurals := f.PluralsFor()
	require.Contains(t, plurals, "example.com/v1")
	assert.Equal(t,
		schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"},
		plurals["example.com/v1"]["widgets"])
}

func TestDirFetcher_LoadsBareSchema(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "bare.json"),
		[]byte(bareSchemaJSON("example.com", "v1", "Widget")),
		0o600))

	f, err := NewDirFetcher(dir)
	require.NoError(t, err)
	require.Equal(t, 1, f.Len())

	sch, _, err := f.Fetch(context.Background(), schema.GroupVersionKind{
		Group: "example.com", Version: "v1", Kind: "Widget",
	})
	require.NoError(t, err)
	require.NotNil(t, sch)

	// Bare schemas don't carry the plural — pin that contract so
	// callers that mix the two formats don't assume otherwise.
	plurals := f.PluralsFor()
	assert.Empty(t, plurals,
		"bare schemas must not register a plural; only full CRDs do")
}

func TestDirFetcher_NonExistentDirReturnsEmpty(t *testing.T) {
	f, err := NewDirFetcher("/path/that/does/not/exist")
	require.NoError(t, err, "non-existent dir is not a load error — overlay can fall through to builtin")
	assert.Equal(t, 0, f.Len())
}

func TestDirFetcher_EmptyDirArgReturnsEmpty(t *testing.T) {
	f, err := NewDirFetcher("")
	require.NoError(t, err, "empty dir arg short-circuits as 'no dir provided'")
	assert.Equal(t, 0, f.Len())
}

func TestDirFetcher_MissingSchemaReturnsNotFound(t *testing.T) {
	dir := t.TempDir()
	f, err := NewDirFetcher(dir)
	require.NoError(t, err)

	_, _, err = f.Fetch(context.Background(), schema.GroupVersionKind{
		Group: "missing.example.com", Version: "v1", Kind: "Missing",
	})
	require.Error(t, err)
	assert.True(t, IsNotFound(err),
		"missing schemas must surface as IsNotFound — Overlay relies on this to fall through")
}

func TestDirFetcher_MalformedCRDFailsLoud(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "broken.yaml"),
		[]byte("apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nspec: this-is-not-an-object\n"),
		0o600))

	_, err := NewDirFetcher(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "broken.yaml",
		"error must name the offending file so operators don't bisect by hand")
}

func TestDirFetcher_BareSchemaWithoutGVKFails(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "nameless.json"),
		[]byte(`{"type": "object", "properties": {"spec": {"type": "object"}}}`),
		0o600))

	_, err := NewDirFetcher(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "x-kubernetes-group-version-kind",
		"the error must guide the operator toward the missing identifier")
}

func TestDirFetcher_IgnoresNonCRDKubernetesObjects(t *testing.T) {
	// Gateway API's `config/crd/standard` directory ships a
	// ValidatingAdmissionPolicy alongside the CRDs. The extractor
	// Makefile copies the whole directory; DirFetcher must skip
	// non-CRD K8s objects silently rather than fail the whole
	// directory load.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "widget.yaml"),
		[]byte(minimalWidgetCRD()),
		0o600))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "vap.yaml"),
		[]byte("apiVersion: admissionregistration.k8s.io/v1\nkind: ValidatingAdmissionPolicy\nmetadata:\n  name: example\nspec: {}\n"),
		0o600))

	f, err := NewDirFetcher(dir)
	require.NoError(t, err,
		"adjacent non-CRD K8s objects must not block CRD loading")
	assert.Equal(t, 1, f.Len(),
		"only the CRD should be registered; the VAP is silently skipped")
}

func TestDirFetcher_IgnoresNonSchemaFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "widget.yaml"),
		[]byte(minimalWidgetCRD()),
		0o600))
	// README, .DS_Store, anything that isn't a .yaml/.yml/.json
	// should be silently skipped — operators dump assorted files
	// into the dir; we ignore noise rather than fail on it.
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "README.md"),
		[]byte("# Local schemas\n"),
		0o600))
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, ".DS_Store"),
		[]byte{},
		0o600))

	f, err := NewDirFetcher(dir)
	require.NoError(t, err)
	assert.Equal(t, 1, f.Len(),
		"only schema-extension files should be loaded; README and dotfiles are ignored")
}

func TestOverlay_FirstHitWins(t *testing.T) {
	// Dir overlays MapFetcher: GVK present in both, dir's value
	// must win. Detect which layer served via a distinguishing
	// schema field (Description) that differs between the two
	// pre-populated schemas.
	gvk := schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "widget.json"),
		[]byte(`{"description":"from-dir","type":"object","x-kubernetes-group-version-kind":[{"group":"example.com","version":"v1","kind":"Widget"}]}`),
		0o600))
	df, err := NewDirFetcher(dir)
	require.NoError(t, err)

	innerSchema := &spec.Schema{}
	innerSchema.Description = "from-inner"
	inner := NewMapFetcher(map[schema.GroupVersionKind]*spec.Schema{gvk: innerSchema})

	sch, _, err := NewOverlay(df, inner).Fetch(context.Background(), gvk)
	require.NoError(t, err)
	require.NotNil(t, sch)
	assert.Equal(t, "from-dir", sch.Description,
		"dir layer hit must short-circuit; inner layer's schema is never returned")
}

func TestOverlay_FallsThroughOnNotFound(t *testing.T) {
	// Outer (dir) has nothing; inner (MapFetcher) has Widget →
	// inner serves.
	outer, err := NewDirFetcher(t.TempDir())
	require.NoError(t, err)

	gvk := schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}
	inner := NewMapFetcher(nil).Add(gvk, mustBareSchema(t))
	o := NewOverlay(outer, inner)

	sch, _, err := o.Fetch(context.Background(), gvk)
	require.NoError(t, err)
	require.NotNil(t, sch)
}

func TestOverlay_NotFoundFromAllLayersBubbles(t *testing.T) {
	empty, err := NewDirFetcher(t.TempDir())
	require.NoError(t, err)
	o := NewOverlay(empty, NewMapFetcher(nil))

	_, _, err = o.Fetch(context.Background(), schema.GroupVersionKind{
		Group: "missing.example.com", Version: "v1", Kind: "Missing",
	})
	require.Error(t, err)
	assert.True(t, IsNotFound(err),
		"every-layer not-found must surface as IsNotFound so upstream callers can branch")
}

func TestOverlay_RealErrorShortCircuits(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}

	// First layer returns a non-not-found error. Overlay must NOT
	// try further layers; the operator needs to see the real
	// failure, not a not-found from a fallback layer covering it
	// up.
	sentinel := errors.New("simulated network failure")
	first := errorFetcher{err: sentinel}
	second := NewMapFetcher(nil).Add(gvk, mustBareSchema(t))

	_, _, err := NewOverlay(first, second).Fetch(context.Background(), gvk)
	require.ErrorIs(t, err, sentinel,
		"real errors must short-circuit; falling through would hide them")
}

func TestNewOverlay_PanicsOnNoLayers(t *testing.T) {
	assert.Panics(t, func() { NewOverlay() })
	assert.Panics(t, func() { NewOverlay(nil, nil) },
		"nil-only is the same configuration mistake as no-layers")
}

// errorFetcher is a test double for TestOverlay_RealErrorShortCircuits.
// Always returns the configured error verbatim — useful for pinning
// "non-not-found errors short-circuit" without standing up a network
// stack.
type errorFetcher struct {
	err error
}

func (e errorFetcher) Fetch(_ context.Context, _ schema.GroupVersionKind) (sch *spec.Schema, components map[string]spec.Schema, err error) {
	return nil, nil, e.err
}

// mustBareSchema parses the test fixture above into a *spec.Schema
// for use as a positive-hit value in MapFetcher seeds.
func mustBareSchema(t *testing.T) *spec.Schema {
	t.Helper()
	var sch spec.Schema
	require.NoError(t, json.Unmarshal([]byte(bareSchemaJSON("example.com", "v1", "Widget")), &sch))
	return &sch
}
