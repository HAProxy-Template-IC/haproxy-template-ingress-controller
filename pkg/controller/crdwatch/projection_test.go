package crdwatch

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/client"
)

func TestProjectDefinition(t *testing.T) {
	definition := crdObj("widgets.example.io", "example.io", "v1")
	definition.SetUID("original-uid")
	definition.SetResourceVersion("12")
	definition.SetGeneration(4)
	definition.SetAnnotations(map[string]string{"last-applied": "large resource body"})
	definition.Object["status"] = map[string]any{"storedVersions": []any{"v1"}}
	before := definition.DeepCopy()

	projected, err := projectDefinition(definition)
	require.NoError(t, err)
	assert.Equal(t, &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apiextensions.k8s.io/v1",
		"kind":       "CustomResourceDefinition",
		"metadata": map[string]any{
			"name": "widgets.example.io", "namespace": "", "uid": "original-uid",
			"resourceVersion": "12", "generation": int64(4),
		},
		"spec": map[string]any{"group": "example.io"},
	}}, projected)
	assert.Equal(t, before, definition)
	projectedAgain, err := projectDefinition(projected)
	require.NoError(t, err)
	assert.Equal(t, projected, projectedAgain)

	definition.SetGeneration(5)
	require.NoError(t, unstructured.SetNestedField(definition.Object, "changed.io", "spec", "group"))
	c := New(nil, map[string]bool{"example.io": true}, nil, nil, slog.Default())
	group, relevant := c.relevantGroup(projected)
	assert.Equal(t, "example.io", group)
	assert.True(t, relevant)
	assert.Equal(t, int64(4), generation(projected))
}

func TestProjectDefinitionPassThrough(t *testing.T) {
	for _, value := range []any{nil, (*unstructured.Unstructured)(nil), "other", cache.DeletedFinalStateUnknown{Key: "missing"}} {
		t.Run(fmt.Sprintf("%T", value), func(t *testing.T) {
			projected, err := projectDefinition(value)
			require.NoError(t, err)
			assert.Equal(t, value, projected)
		})
	}
}

func TestInformerRetainsOnlyDefinitionReloadMetadata(t *testing.T) {
	definition := crdObj("widgets.example.io", "example.io", "v1")
	definition.SetGeneration(1)
	definition.SetResourceVersion("1")
	definition.SetAnnotations(map[string]string{"last-applied": "large resource body"})
	dynamicClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(k8sruntime.NewScheme(),
		map[schema.GroupVersionResource]string{crdGVR: "CustomResourceDefinitionList"}, definition)
	k8sClient := client.NewFromClientset(kubefake.NewClientset(), dynamicClient, "default")
	c := New(k8sClient, map[string]bool{"example.io": true}, nil, nil, slog.Default())
	factory, informer, err := c.newInformer()
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	factory.Start(ctx.Done())
	t.Cleanup(func() {
		cancel()
		factory.Shutdown()
	})
	require.True(t, cache.WaitForCacheSync(ctx.Done(), informer.HasSynced))

	for revision := range int64(3) {
		definition.SetGeneration(revision + 1)
		definition.SetResourceVersion(fmt.Sprint(revision + 1))
		if revision > 0 {
			_, err = dynamicClient.Resource(crdGVR).Update(ctx, definition, metav1.UpdateOptions{})
			require.NoError(t, err)
		}
		want, projectErr := projectDefinition(definition)
		require.NoError(t, projectErr)
		require.EventuallyWithT(t, func(collect *assert.CollectT) {
			got, exists, getErr := informer.GetStore().GetByKey(definition.GetName())
			assert.NoError(collect, getErr)
			assert.True(collect, exists)
			assert.Equal(collect, want, got)
		}, time.Second, time.Millisecond)
	}

	live, err := dynamicClient.Resource(crdGVR).Get(ctx, definition.GetName(), metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, definition, live)
	require.NoError(t, dynamicClient.Resource(crdGVR).Delete(ctx, definition.GetName(), metav1.DeleteOptions{}))
	require.Eventually(t, func() bool { return len(informer.GetStore().ListKeys()) == 0 }, time.Second, time.Millisecond)
}

func BenchmarkDefinitionInformerRetention(b *testing.B) {
	payload := definitionBenchmarkJSON(b)
	for _, projected := range []bool{false, true} {
		b.Run(fmt.Sprintf("projected=%t", projected), func(b *testing.B) {
			var retained int64
			for range b.N {
				b.StopTimer()
				runtime.GC()
				var before runtime.MemStats
				runtime.ReadMemStats(&before)
				b.StartTimer()
				store := populateDefinitionBenchmark(b, payload, projected)
				b.StopTimer()
				runtime.GC()
				var after runtime.MemStats
				runtime.ReadMemStats(&after)
				retained += int64(after.HeapAlloc) - int64(before.HeapAlloc)
				runtime.KeepAlive(store)
			}
			b.ReportMetric(float64(retained)/float64(b.N), "retained-B")
		})
	}
}

func populateDefinitionBenchmark(b *testing.B, payload []byte, projected bool) cache.Store {
	b.Helper()
	var options []cache.StoreOption
	if projected {
		options = append(options, cache.WithTransformer(projectDefinition))
	}
	store := cache.NewStore(cache.DeletionHandlingMetaNamespaceKeyFunc, options...)
	for index := range 128 {
		definition := &unstructured.Unstructured{}
		require.NoError(b, definition.UnmarshalJSON(payload))
		definition.SetName(fmt.Sprintf("widgets-%03d.example.io", index))
		require.NoError(b, store.Add(definition))
	}
	require.Len(b, store.ListKeys(), 128)
	return store
}

func definitionBenchmarkJSON(b *testing.B) []byte {
	b.Helper()
	properties := make(map[string]any)
	for index := range 64 {
		field := map[string]any{"type": "string", "description": strings.Repeat("schema description ", 32)}
		for range 12 {
			field = map[string]any{"type": "object", "properties": map[string]any{"value": field}}
		}
		properties[fmt.Sprintf("field%03d", index)] = field
	}
	definition := crdObj("widgets.example.io", "example.io", "v1")
	definition.SetGeneration(1)
	definition.SetResourceVersion("1")
	definition.Object["spec"].(map[string]any)["versions"] = []any{map[string]any{
		"name": "v1", "served": true, "storage": true,
		"schema": map[string]any{"openAPIV3Schema": map[string]any{"type": "object", "properties": properties}},
	}}
	payload, err := definition.MarshalJSON()
	require.NoError(b, err)
	return payload
}
