//go:build e2e

package e2e

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

type auxiliaryContentKind struct {
	kind, resource, nameField, contentField string
}

func TestAuxiliaryContentSchema(t *testing.T) {
	t.Parallel()
	feature := features.New("Auxiliary content has an explicit nonempty or empty representation").
		Assess("missing and contradictory content declarations are rejected", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			t.Helper()
			namespace := NamespaceForTest(ctx, t, cfg.Client())
			client, err := dynamic.NewForConfig(cfg.Client().RESTConfig())
			require.NoError(t, err)
			for _, kind := range []auxiliaryContentKind{
				{"HAProxyMapFile", "haproxymapfiles", "mapName", "entries"},
				{"HAProxyGeneralFile", "haproxygeneralfiles", "fileName", "content"},
				{"HAProxyCRTListFile", "haproxycrtlistfiles", "listName", "entries"},
			} {
				t.Run(kind.kind, func(t *testing.T) {
					assertAuxiliaryContentSchema(ctx, t, client, namespace, kind)
				})
			}
			return ctx
		}).Feature()
	testEnv.Test(t, feature)
}

func assertAuxiliaryContentSchema(ctx context.Context, t *testing.T, client dynamic.Interface, namespace string, kind auxiliaryContentKind) {
	t.Helper()
	api := client.Resource(schema.GroupVersionResource{
		Group: "haproxy-haptic.org", Version: "v1alpha1", Resource: kind.resource,
	}).Namespace(namespace)
	for _, tc := range []struct {
		name  string
		input map[string]any
		valid bool
	}{
		{"empty", map[string]any{"empty": true}, true},
		{"nonempty", map[string]any{kind.contentField: "entry"}, true},
		{"missing", map[string]any{}, false},
		{"empty-string", map[string]any{kind.contentField: ""}, false},
		{"false-marker", map[string]any{"empty": false}, false},
		{"contradictory", map[string]any{"empty": true, kind.contentField: "entry"}, false},
		{"compressed-empty", map[string]any{"empty": true, "compressed": true}, false},
		{"wrong-checksum", map[string]any{"empty": true, "checksum": "sha256:wrong"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content, _ := tc.input[kind.contentField].(string)
			spec := map[string]any{
				kind.nameField: "test-file", "path": "/test/file",
				"checksum": fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(content))),
			}
			for key, value := range tc.input {
				spec[key] = value
			}
			object := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "haproxy-haptic.org/v1alpha1", "kind": kind.kind,
				"metadata": map[string]any{"name": tc.name, "namespace": namespace}, "spec": spec,
			}}
			_, err := api.Create(ctx, object, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}})
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.True(t, apierrors.IsInvalid(err), "expected schema rejection, got %v", err)
			}
		})
	}
}
