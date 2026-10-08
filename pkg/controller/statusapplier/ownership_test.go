// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package statusapplier

import (
	"crypto/sha256"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestStatusFieldOwnershipRequiresApplyEvidence(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*metav1.ManagedFieldsEntry)
	}{
		{"different API version", func(e *metav1.ManagedFieldsEntry) { e.APIVersion = "example.org/v2" }},
		{"different subresource", func(e *metav1.ManagedFieldsEntry) { e.Subresource = "" }},
		{"update instead of apply", func(e *metav1.ManagedFieldsEntry) { e.Operation = metav1.ManagedFieldsOperationUpdate }},
		{"foreign manager", func(e *metav1.ManagedFieldsEntry) { e.Manager = "other-deployed" }},
		{"unsupported encoding", func(e *metav1.ManagedFieldsEntry) { e.FieldsType = "unknown" }},
		{"missing fields", func(e *metav1.ManagedFieldsEntry) { e.FieldsV1 = nil }},
		{"invalid fields", func(e *metav1.ManagedFieldsEntry) { e.FieldsV1.SetRawString(`[]`) }},
		{"empty status ownership", func(e *metav1.ManagedFieldsEntry) { e.FieldsV1.SetRawString(`{"f:status":{}}`) }},
		{"only metadata ownership", func(e *metav1.ManagedFieldsEntry) { e.FieldsV1.SetRawString(`{"f:metadata":{"f:labels":{}}}`) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			entry := phaseFieldOwner(t, "deployed", "value")
			test.change(&entry)
			object := &unstructured.Unstructured{}
			object.SetManagedFields([]metav1.ManagedFieldsEntry{entry})
			require.Empty(t, statusFieldOwnership(object, "networking.k8s.io/v1"))
		})
	}
}

func TestStatusFieldOwnershipCanonicalizesFieldSets(t *testing.T) {
	entry := phaseFieldOwner(t, "deployed", "value")
	object := &unstructured.Unstructured{}
	entry.FieldsV1.SetRawString(`{"f:status":{"f:a":{},"f:b":{}},"f:metadata":{"f:labels":{}}}`)
	object.SetManagedFields([]metav1.ManagedFieldsEntry{entry})
	first := statusFieldOwnership(object, entry.APIVersion)
	require.Len(t, first, 1)
	entry.FieldsV1.SetRawString(`{"f:status":{"f:b":{},"f:a":{}}}`)
	object.SetManagedFields([]metav1.ManagedFieldsEntry{entry})
	require.Equal(t, first, statusFieldOwnership(object, entry.APIVersion))
	object.SetManagedFields([]metav1.ManagedFieldsEntry{entry, entry})
	require.Empty(t, statusFieldOwnership(object, entry.APIVersion), "ambiguous ownership cannot prove preservation")
}

func TestStatusOwnershipLossCannotBeReversedByRestoringFields(t *testing.T) {
	owner := sha256.Sum256([]byte("fields"))
	entry := statusCacheEntry{
		uid: "uid", baseResourceVersion: "1", latestResourceVersion: "2",
		lastPhase: "deployed", lastPayload: []byte("first"),
		ownership: statusOwnership{"haptic-deployed": owner},
	}
	for _, test := range []struct {
		name      string
		source    string
		ownership statusOwnership
	}{
		{"another manager overwrites fields", "2", nil},
		{"an unobserved revision intervenes", "external", entry.ownership},
	} {
		t.Run(test.name, func(t *testing.T) {
			next := statusCacheEntry{
				uid: "uid", baseResourceVersion: "2", latestResourceVersion: "3",
				lastPhase: "rendered", lastPayload: []byte("unrelated"),
				superseded: entry.supersededWrites("uid", "3", test.source, test.ownership),
				ownership:  test.ownership,
			}
			next.superseded = next.supersededWrites("uid", "4", "3", entry.ownership)
			next.ownership = entry.ownership
			require.False(t, next.isSupersededEcho("2", "deployed", []byte("first")))
		})
	}
}
