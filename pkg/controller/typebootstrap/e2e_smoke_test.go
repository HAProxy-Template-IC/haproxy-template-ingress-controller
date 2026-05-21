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

package typebootstrap_test

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/kube-openapi/pkg/validation/spec"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/schemafetcher"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/typegen"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// TestE2E_TypedWatchedResources_FullPipeline is the keystone test
// for the typed-watched-resources work (issue #N — typed cluster
// shapes in Scriggo templates). It threads one synthetic Gateway
// schema all the way through the production stack so a regression
// in any single phase breaks this test loudly.
//
// The pipeline under test:
//
//  1. MapFetcher serves a pre-baked spec.Schema for the synthetic
//     Gateway GVK.
//
//  2. typebootstrap.Bootstrap calls into schemafetcher + typegen,
//     producing a *Result whose Types map has one entry: gateways
//     → reflect.Type built via reflect.StructOf.
//
//  3. typebootstrap.BuildEngineDeclarations produces the
//     additionalDeclarations map (`*[]*Generated` per type) that
//     the engine constructor consumes — same shape every chart
//     template will compile against.
//
//  4. templating.NewScriggoWithDeclarations type-checks a template
//     that ranges the typed `gateways` global and reaches into
//     `gw.Metadata.Namespace`. The compile-time type checking is
//     the property that makes typo-at-build-time error reporting
//     work: a misspelled `Namespacee` is rejected here, not at
//     render time on a live cluster.
//
//  5. typegen.WrapSlice converts plain `[]any` (what every
//     stores.Store hands out via List()) into the slice-of-pointer
//     shape the engine declared the global as. This is the
//     run-time half of the contract — render() would not find the
//     value otherwise.
//
//  6. Scriggo renders the template, and we assert the output
//     matches the data we fed in.
//
// If this test fails, look at where it fails:
//
//   - Bootstrap returns errors: schemafetcher or typegen broke.
//   - Engine construction fails: BuildEngineDeclarations no longer
//     matches what Scriggo expects, or
//     NewScriggoWithDeclarations type-checking changed.
//   - Render produces wrong output: WrapSlice or the engine's
//     runtime-variable lookup broke.
//   - Render errors out: the declared type and the wrapped value
//     drifted apart (the *[]*T shape lives in both
//     BuildEngineDeclarations and typegen.WrapSlice; they must
//     agree).
//
// This is a unit test, not an integration test — no kind cluster,
// no k8s clients, no controller goroutines. The production wiring
// (apiextensions client, OpenAPI discovery, REST mapper, store
// providers) is covered by separate per-package tests.
func TestE2E_TypedWatchedResources_FullPipeline(t *testing.T) {
	// Phase 1 — synthetic schema. Shape mirrors a real Gateway CRD's
	// metadata + spec.gatewayClassName subset; that's enough surface
	// to exercise nested struct field access (Metadata.Namespace) and
	// a leaf string field (Spec.GatewayClassName), which is what real
	// chart macros look like.
	gatewayGVK := schema.GroupVersionKind{
		Group:   "gateway.networking.k8s.io",
		Version: "v1",
		Kind:    "Gateway",
	}
	gatewaySchema := &spec.Schema{
		SchemaProps: spec.SchemaProps{
			Type: spec.StringOrArray{"object"},
			Properties: map[string]spec.Schema{
				"metadata": {
					SchemaProps: spec.SchemaProps{
						Type: spec.StringOrArray{"object"},
						Properties: map[string]spec.Schema{
							"name": {SchemaProps: spec.SchemaProps{
								Type: spec.StringOrArray{"string"},
							}},
							"namespace": {SchemaProps: spec.SchemaProps{
								Type: spec.StringOrArray{"string"},
							}},
						},
					},
				},
				"spec": {
					SchemaProps: spec.SchemaProps{
						Type: spec.StringOrArray{"object"},
						Properties: map[string]spec.Schema{
							"gatewayClassName": {SchemaProps: spec.SchemaProps{
								Type: spec.StringOrArray{"string"},
							}},
						},
					},
				},
			},
		},
	}
	fetcher := schemafetcher.NewMapFetcher(map[schema.GroupVersionKind]*spec.Schema{
		gatewayGVK: gatewaySchema,
	})

	// Phase 2 — drive the bootstrap. The Logger discards: this test
	// asserts on functional outcomes, not log output.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	result, err := typebootstrap.Bootstrap(context.Background(), typebootstrap.Config{
		Resources: []typebootstrap.Resource{
			{Name: "gateways", GVK: gatewayGVK},
		},
		Fetcher: fetcher,
		Logger:  logger,
	})
	require.NoError(t, err, "bootstrap must succeed for the in-memory schema")
	require.Empty(t, result.Errors,
		"no per-resource degradations expected when MapFetcher has the schema")
	require.Contains(t, result.Types, "gateways",
		"the generated type must be keyed by the user-facing name, not the GVK")

	// Phase 3 — engine declarations. This is the compile-time half
	// of the contract: the engine needs to see the type to validate
	// `gw.Metadata.Namespace` at parse time.
	declarations := typebootstrap.BuildEngineDeclarations(result)
	require.Contains(t, declarations, "gateways",
		"BuildEngineDeclarations must surface the typed global under the resource name")

	// Phase 4 — compile a template that exercises:
	//   - ranging the typed slice (proves *[]*T is iterable)
	//   - nested field access (gw.Metadata.Namespace)
	//   - leaf field access on a sibling sub-struct (Spec.GatewayClassName)
	//
	// The render syntax matches what chart macros write today, so a
	// future chart-side adoption is a copy of this template.
	const tmpl = `{%- for _, gw := range gateways %}{{ gw.Metadata.Namespace }}/{{ gw.Metadata.Name }} class={{ gw.Spec.GatewayClassName }}
{% end -%}`
	engine, err := templating.NewScriggoWithDeclarations(
		map[string]string{"main": tmpl},
		[]string{"main"},
		nil, nil, nil,
		declarations,
	)
	require.NoError(t, err,
		"engine construction must succeed when the template field references "+
			"match the declared typed global's shape")

	// Phase 5 — wrap a synthetic store snapshot. Plain []any is what
	// every stores.Store hands out via List() in production; WrapSlice
	// converts it to []*<Generated> which Scriggo accepts at runtime.
	snapshot := []any{
		map[string]any{
			"metadata": map[string]any{"name": "edge", "namespace": "ns1"},
			"spec":     map[string]any{"gatewayClassName": "external"},
		},
		map[string]any{
			"metadata": map[string]any{"name": "internal", "namespace": "ns2"},
			"spec":     map[string]any{"gatewayClassName": "internal"},
		},
	}
	typedSlice, err := typegen.WrapSlice(snapshot, result.Types["gateways"])
	require.NoError(t, err,
		"WrapSlice must accept plain unstructured items as long as keys "+
			"match the schema-derived type's json tags")

	// Pointer wrap for the *[]*T shape (matches BuildEngineDeclarations).
	holder := reflect.New(typedSlice.Type())
	holder.Elem().Set(typedSlice)

	// Phase 6 — render and assert. Any field-lookup failure (e.g. the
	// engine seeing a different shape than the wrapper produced) would
	// surface here as an empty / partial output.
	out, err := engine.Render(context.Background(), "main", map[string]any{
		"gateways": holder.Interface(),
	})
	require.NoError(t, err, "render must succeed when typed global is correctly wrapped")
	assert.Equal(t,
		"ns1/edge class=external\nns2/internal class=internal\n",
		out,
		"both gateways must appear with all three typed fields populated; "+
			"any drift here points at the typegen ⇄ engine-declarations contract")
}

// TestE2E_TypedWatchedResources_TypoCaughtAtCompile is the
// complementary half of the smoke test: prove that a misspelled
// field on a typed global is rejected at engine construction, not
// silently at render time. This is the property that makes the
// whole feature worthwhile — without it, typed access offers no
// benefit over the existing dig() shape.
//
// Pipeline shape is identical to the happy-path test through Phase
// 3; the divergence is at Phase 4 where the template references a
// non-existent field.
func TestE2E_TypedWatchedResources_TypoCaughtAtCompile(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "Gateway"}
	fetcher := schemafetcher.NewMapFetcher(map[schema.GroupVersionKind]*spec.Schema{
		gvk: {
			SchemaProps: spec.SchemaProps{
				Type: spec.StringOrArray{"object"},
				Properties: map[string]spec.Schema{
					"metadata": {SchemaProps: spec.SchemaProps{
						Type: spec.StringOrArray{"object"},
						Properties: map[string]spec.Schema{
							"namespace": {SchemaProps: spec.SchemaProps{
								Type: spec.StringOrArray{"string"},
							}},
						},
					}},
				},
			},
		},
	})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	result, err := typebootstrap.Bootstrap(context.Background(), typebootstrap.Config{
		Resources: []typebootstrap.Resource{{Name: "gateways", GVK: gvk}},
		Fetcher:   fetcher,
		Logger:    logger,
	})
	require.NoError(t, err)

	// Deliberate typo: "Namespacee" instead of "Namespace". A future
	// chart author would expect to hear about this from the compiler,
	// not from a silent empty render against a live cluster.
	const tmpl = `{%- for _, gw := range gateways %}{{ gw.Metadata.Namespacee }}{% end -%}`
	_, err = templating.NewScriggoWithDeclarations(
		map[string]string{"main": tmpl},
		[]string{"main"},
		nil, nil, nil,
		typebootstrap.BuildEngineDeclarations(result),
	)
	require.Error(t, err,
		"typed-global misspelling must be rejected at engine construction, "+
			"which is the property that makes typed access worth the wiring")
}
