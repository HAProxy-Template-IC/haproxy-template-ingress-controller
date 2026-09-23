// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package statusapplier

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8stesting "k8s.io/client-go/testing"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func TestOwnedListsRetryPreservesConcurrentForeignUpdates(t *testing.T) {
	client := newFakeDynamicClient()
	comp := newTestComponent(testutil.NewTestBus(), client, newTestResolver())
	gvr := schema.GroupVersionResource{Group: "example.test", Version: "v1", Resource: "widgets"}
	patch := &templating.StatusPatch{
		Namespace: "default", Name: "sample", APIVersion: "example.test/v1", Kind: "Widget",
		UID: "uid-sample", ResourceVersion: "1", ListOwnership: `{"/entries":{"writer":"ours"}}`,
	}
	foreign := map[string]any{"writer": "other", "value": "original", "timestamp": "2026-01-01T00:00:00Z"}
	current := &unstructured.Unstructured{Object: map[string]any{
		"status": map[string]any{"entries": []any{foreign, map[string]any{"writer": "ours", "value": "old"}}},
	}}
	current.SetUID("uid-sample")
	current.SetResourceVersion("7")
	reads, writes := 0, 0
	client.PrependReactor("get", "widgets", func(k8stesting.Action) (bool, runtime.Object, error) {
		reads++
		return true, current.DeepCopy(), nil
	})
	client.PrependReactor("patch", "widgets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		writes++
		var payload map[string]any
		require.NoError(t, json.Unmarshal(action.(k8stesting.PatchAction).GetPatch(), &payload))
		require.Equal(t, "status", action.GetSubresource())
		metadata := payload["metadata"].(map[string]any)
		require.Equal(t, "uid-sample", metadata["uid"])
		require.Equal(t, current.GetResourceVersion(), metadata["resourceVersion"])
		entries := payload["status"].(map[string]any)["entries"].([]any)
		require.Equal(t, map[string]any{"writer": "ours", "value": "updated"}, entries[0])
		require.Equal(t, foreign, entries[1])
		if writes == 1 {
			foreign["value"] = "concurrent"
			current.Object["status"].(map[string]any)["entries"] = []any{foreign, map[string]any{"writer": "third", "value": "new"}}
			current.SetResourceVersion("8")
			return true, nil, apierrors.NewConflict(gvr.GroupResource(), "sample", errors.New("concurrent update"))
		}
		require.Len(t, entries, 3)
		require.Equal(t, map[string]any{"writer": "third", "value": "new"}, entries[2])
		result := &unstructured.Unstructured{Object: payload}
		result.SetResourceVersion("9")
		return true, result, nil
	})
	result, err := comp.applyOwnedLists(t.Context(), gvr, patch, "deployed", map[string]any{
		"entries": []any{map[string]any{"writer": "ours", "value": "updated"}},
	})
	require.NoError(t, err)
	require.Equal(t, "9", result.GetResourceVersion())
	require.Equal(t, 2, reads)
	require.Equal(t, 2, writes)
}

func TestOwnedListsRejectsRecreatedTarget(t *testing.T) {
	client := newFakeDynamicClient()
	comp := newTestComponent(testutil.NewTestBus(), client, newTestResolver())
	gvr := schema.GroupVersionResource{Group: "example.test", Version: "v1", Resource: "widgets"}
	client.PrependReactor("get", "widgets", func(k8stesting.Action) (bool, runtime.Object, error) {
		current := &unstructured.Unstructured{}
		current.SetUID("replacement")
		current.SetResourceVersion("2")
		return true, current, nil
	})
	_, err := comp.applyOwnedLists(t.Context(), gvr, &templating.StatusPatch{
		Name: "sample", UID: "original", ListOwnership: `{"/entries":{"writer":"ours"}}`,
	}, "deployed", map[string]any{"entries": []any{}})
	require.ErrorContains(t, err, "target has changed")
	require.Len(t, client.Actions(), 1)
}
