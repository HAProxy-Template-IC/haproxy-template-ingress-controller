package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

// An object may carry only ONE controller ownerReference. Deleting and
// recreating the config gives it a new UID, and a library that outlives that
// still carries the old reference — appending a second would make the apiserver
// reject the patch. ensureLibraryOwnership swallows patch errors, so the tree
// relationship would silently never be re-established.
func TestWithoutSupersededController(t *testing.T) {
	owner := metav1.OwnerReference{
		APIVersion: "haproxy-haptic.org/v1alpha1",
		Kind:       "HAProxyTemplateConfig",
		Name:       "haptic-config",
		UID:        "new-uid",
		Controller: ptr.To(true),
	}
	stale := metav1.OwnerReference{
		APIVersion: "haproxy-haptic.org/v1alpha1",
		Kind:       "HAProxyTemplateConfig",
		Name:       "haptic-config",
		UID:        "old-uid",
		Controller: ptr.To(true),
	}
	unrelated := metav1.OwnerReference{
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Name:       "something-else",
		UID:        "other-uid",
		Controller: ptr.To(true),
	}
	nonController := metav1.OwnerReference{
		APIVersion: "haproxy-haptic.org/v1alpha1",
		Kind:       "HAProxyTemplateConfig",
		Name:       "haptic-config",
		UID:        "old-uid",
	}

	t.Run("drops a same-name controller ref from a previous UID", func(t *testing.T) {
		got := withoutSupersededController([]metav1.OwnerReference{stale}, &owner)
		assert.Empty(t, got, "appending to this would give the object two controller refs")
	})

	t.Run("keeps another object's controller ref", func(t *testing.T) {
		got := withoutSupersededController([]metav1.OwnerReference{unrelated}, &owner)
		assert.Equal(t, []metav1.OwnerReference{unrelated}, got,
			"only OUR superseded reference may be dropped; removing another owner's would orphan it")
	})

	t.Run("keeps a non-controller ref with the same identity", func(t *testing.T) {
		got := withoutSupersededController([]metav1.OwnerReference{nonController}, &owner)
		assert.Equal(t, []metav1.OwnerReference{nonController}, got,
			"only the single controller slot conflicts")
	})

	t.Run("is a no-op when nothing is superseded", func(t *testing.T) {
		got := withoutSupersededController(nil, &owner)
		assert.Empty(t, got)
	})
}
