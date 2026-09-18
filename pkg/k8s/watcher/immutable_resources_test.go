package watcher

import (
	"runtime"
	"testing"
	"weak"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
)

func TestImmutableTransformReusesRelistsBeforeHandlersRun(t *testing.T) {
	transform := newNormalizeTransform(newNormalizeTestIndexer(t, nil))
	source := immutableTransformFixture()
	first, err := transform(source.DeepCopy())
	require.NoError(t, err)
	second, err := transform(source.DeepCopy())
	require.NoError(t, err)
	assert.Same(t, first, second)

	source.SetResourceVersion("2")
	changed, err := transform(source.DeepCopy())
	require.NoError(t, err)
	assert.NotSame(t, first, changed)
	assert.Equal(t, "2", changed.(*store.ImmutableResource).GetResourceVersion())
	relisted, err := transform(source.DeepCopy())
	require.NoError(t, err)
	assert.Same(t, changed, relisted)
}

func TestImmutableTransformDoesNotRetainRemovedResources(t *testing.T) {
	transform := newNormalizeTransform(newNormalizeTestIndexer(t, nil))
	retained := func() weak.Pointer[store.ImmutableResource] {
		value, err := transform(immutableTransformFixture())
		require.NoError(t, err)
		return weak.Make(value.(*store.ImmutableResource))
	}()
	runtime.GC()
	assert.True(t, retained.Value() == nil)
	runtime.KeepAlive(transform)
}

func TestImmutableTransformOldCleanupKeepsReplacement(t *testing.T) {
	owner := newImmutableResources(newNormalizeTestIndexer(t, nil))
	source := immutableTransformFixture()
	old, err := owner.transform(source.DeepCopy())
	require.NoError(t, err)
	oldReference := weak.Make(old.(*store.ImmutableResource))
	source.Object["spec"] = map[string]any{"value": "changed"}
	current, err := owner.transform(source.DeepCopy())
	require.NoError(t, err)
	assert.NotSame(t, old, current)
	cleanImmutableResource(immutableResourceCleanup{
		owner: owner, identity: immutableResourceIdentity{namespace: "default", name: "target"}, reference: oldReference,
	})
	relisted, err := owner.transform(source.DeepCopy())
	require.NoError(t, err)
	assert.Same(t, current, relisted)
}

func immutableTransformFixture() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"namespace": "default", "name": "target", "resourceVersion": "1"},
		"spec":     map[string]any{"value": "original"},
	}}
}
