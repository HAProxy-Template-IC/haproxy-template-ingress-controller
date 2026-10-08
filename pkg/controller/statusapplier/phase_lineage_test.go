// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package statusapplier

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
)

func TestStatusEchoAfterAnotherPhasePreservesNewerValue(t *testing.T) {
	for _, test := range []struct {
		name         string
		ownership    bool
		changedOwner bool
		renderBase   string
		secondBase   string
		want         patchOutcome
	}{
		{name: "unrelated phase keeps ownership", renderBase: "3", ownership: true, want: patchSkipped},
		{name: "unrelated phase writes from the old echo", renderBase: "2", ownership: true, want: patchSkipped},
		{name: "overlapping phase takes ownership", renderBase: "3", ownership: true, changedOwner: true, want: patchApplied},
		{name: "overlapping phase writes from the old echo", renderBase: "2", ownership: true, changedOwner: true, want: patchApplied},
		{name: "missing ownership cannot prove safety", renderBase: "3", want: patchApplied},
		{name: "current phase base survives an unrelated write", secondBase: "2", renderBase: "2", ownership: true, want: patchApplied},
		{name: "current phase base survives a newer unrelated write", secondBase: "2", renderBase: "3", ownership: true, want: patchApplied},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := newFakeDynamicClient()
			comp := newTestComponent(testutil.NewTestBus(), client, newTestResolver())
			patch := newTestPatches(nil)[0]
			var owners []metav1.ManagedFieldsEntry
			if test.ownership {
				owners = []metav1.ManagedFieldsEntry{phaseFieldOwner(t, "deployed", "stamp")}
			}
			writes := 0
			client.PrependReactor("patch", "*", func(k8stesting.Action) (bool, runtime.Object, error) {
				writes++
				result := &unstructured.Unstructured{}
				result.SetUID("uid-my-ingress")
				result.SetResourceVersion(strconv.Itoa(writes + 1))
				result.SetManagedFields(owners)
				return true, result, nil
			})
			first := map[string]any{"stamp": "first"}
			second := map[string]any{"stamp": "second"}
			require.Equal(t, patchApplied, comp.applyOnePatch(t.Context(), &patch, first, "deployed"))
			if test.secondBase != "" {
				patch.ResourceVersion = test.secondBase
			}
			require.Equal(t, patchApplied, comp.applyOnePatch(t.Context(), &patch, second, "deployed"))
			if test.ownership {
				owners = append(owners, phaseFieldOwner(t, "rendered", "accepted"))
				if test.changedOwner {
					owners = []metav1.ManagedFieldsEntry{phaseFieldOwner(t, "rendered", "stamp")}
				}
			}
			patch.ResourceVersion = test.renderBase
			require.Equal(t, patchApplied, comp.applyOnePatch(t.Context(), &patch, map[string]any{"accepted": true}, "rendered"))
			patch.ResourceVersion = "2"
			require.Equal(t, test.want, comp.applyOnePatch(t.Context(), &patch, first, "deployed"))
			if test.want == patchSkipped {
				require.Equal(t, 3, writes)
				patch.ResourceVersion = "4"
				require.Equal(t, patchApplied, comp.applyOnePatch(t.Context(), &patch, first, "deployed"), "a new change from the current base still applies")
			}
		})
	}
}

func phaseFieldOwner(t *testing.T, phase, field string) metav1.ManagedFieldsEntry {
	t.Helper()
	fields, err := json.Marshal(map[string]any{"f:status": map[string]any{"f:" + field: map[string]any{}}})
	require.NoError(t, err)
	return metav1.ManagedFieldsEntry{
		Manager: fieldManagerPrefix + "-" + phase, Operation: metav1.ManagedFieldsOperationApply,
		APIVersion: "networking.k8s.io/v1", Subresource: statusKey, FieldsType: "FieldsV1",
		FieldsV1: metav1.NewFieldsV1(string(fields)),
	}
}
