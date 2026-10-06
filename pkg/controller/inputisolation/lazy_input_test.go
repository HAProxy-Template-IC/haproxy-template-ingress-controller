// Copyright 2026 Philipp Hossner
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

package inputisolation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/pipeline"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/indexer"
	k8sstore "gitlab.com/haproxy-haptic/haptic/pkg/k8s/store"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

func TestUnreadRetainedRevisionDoesNotBlockIndependentChanges(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		t.Run(fmt.Sprintf("deleted=%t", deleted), func(t *testing.T) {
			keys := []string{"metadata.namespace", "metadata.name"}
			dependency := widget("dependency", "valid")
			dependency.SetResourceVersion("1")
			client := fake.NewSimpleDynamicClient(runtime.NewScheme(), dependency)
			var gets atomic.Int32
			client.PrependReactor("get", "widgets", func(k8stesting.Action) (bool, runtime.Object, error) {
				gets.Add(1)
				return false, nil, nil
			})
			idx, err := indexer.New(indexer.Config{IndexBy: keys})
			require.NoError(t, err)
			gvr := schema.GroupVersionResource{Group: "example.test", Version: "v1", Resource: "widgets"}
			lazy, err := k8sstore.NewCachedStore(&k8sstore.CachedStoreConfig{NumKeys: 2, Client: client, GVR: gvr, Indexer: idx, Projected: true})
			require.NoError(t, err)
			require.NoError(t, lazy.Add(dependency, []string{"default", "dependency"}))
			references := k8sstore.NewMemoryStore(2)
			provider := stores.NewRealStoreProvider(map[string]stores.Store{"references": references, "dependencies": lazy})
			var unavailable atomic.Int32
			selector := New(lazyDependencyValidator(&unavailable), map[string]config.WatchedResource{
				"references":   {APIVersion: "example.test/v1", Resources: "references", IndexBy: keys},
				"dependencies": {APIVersion: "example.test/v1", Resources: "widgets", IndexBy: keys},
			}, nil)
			_, err = selector.Execute(t.Context(), provider, rendercontext.RenderModeReconcile)
			require.NoError(t, err)
			require.Zero(t, gets.Load(), "accepting an unused reference must not fetch its body")

			dependency.SetResourceVersion("2")
			dependency.Object["spec"] = map[string]any{"value": "invalid"}
			if deleted {
				require.NoError(t, client.Resource(gvr).Namespace("default").Delete(t.Context(), "dependency", metav1.DeleteOptions{}))
				require.NoError(t, lazy.Delete("default", "dependency", []string{"default", "dependency"}))
			} else {
				_, err = client.Resource(gvr).Namespace("default").Update(t.Context(), dependency, metav1.UpdateOptions{})
				require.NoError(t, err)
				require.NoError(t, lazy.Update(dependency, []string{"default", "dependency"}))
			}
			require.NoError(t, references.Add(widget("consumer", "dependency"), []string{"default", "consumer"}))
			for _, endpoint := range []string{"10.0.0.1", "10.0.0.2"} {
				require.NoError(t, references.Update(widget("healthy", endpoint), []string{"default", "healthy"}))
				result, renderErr := selector.Execute(t.Context(), provider, rendercontext.RenderModeReconcile)
				require.NoError(t, renderErr)
				require.Contains(t, result.HAProxyConfig, endpoint)
				_, rejected, _ := selector.Snapshot()
				require.Len(t, rejected, 1)
				require.Equal(t, "consumer", rejected[0].Name)
			}
			require.Positive(t, unavailable.Load(), "the trial must try the unread retained revision")
			dependency.SetResourceVersion("3")
			dependency.Object["spec"] = map[string]any{"value": "valid"}
			if deleted {
				_, err = client.Resource(gvr).Namespace("default").Create(t.Context(), dependency, metav1.CreateOptions{})
			} else {
				_, err = client.Resource(gvr).Namespace("default").Update(t.Context(), dependency, metav1.UpdateOptions{})
			}
			require.NoError(t, err)
			require.NoError(t, lazy.Update(dependency, []string{"default", "dependency"}))
			result, err := selector.Execute(t.Context(), provider, rendercontext.RenderModeReconcile)
			require.NoError(t, err)
			require.Contains(t, result.HAProxyConfig, "consumer")
			_, rejected, _ := selector.Snapshot()
			require.Empty(t, rejected)
		})
	}
}

func TestMovedObservedSnapshotRequiresFreshInputs(t *testing.T) {
	live := k8sstore.NewMemoryStore(2)
	provider := stores.NewRealStoreProvider(map[string]stores.Store{"widgets": live})
	validate := validationFunc(func(ctx context.Context, inputs stores.StoreProvider) (*pipeline.PipelineResult, error) {
		items, err := stores.ListContext(ctx, inputs.GetStore("widgets"))
		if err != nil {
			return nil, err
		}
		if len(items) > 0 {
			return nil, stores.ErrSnapshotChanged
		}
		return &pipeline.PipelineResult{HAProxyConfig: "empty"}, nil
	})
	selector := New(validate, map[string]config.WatchedResource{
		"widgets": {APIVersion: "example.test/v1", Resources: "widgets", IndexBy: []string{"metadata.namespace", "metadata.name"}},
	}, nil)
	_, err := selector.Execute(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.NoError(t, err)
	before := selector.accepted.Load()
	require.NoError(t, live.Add(widget("pending", "new"), []string{"default", "pending"}))
	_, err = selector.Execute(t.Context(), provider, rendercontext.RenderModeReconcile)
	require.ErrorIs(t, err, stores.ErrSnapshotChanged)
	require.Same(t, before, selector.accepted.Load(), "a moved observed snapshot must not publish a selection")
}

func lazyDependencyValidator(unavailable *atomic.Int32) validationFunc {
	return func(ctx context.Context, inputs stores.StoreProvider) (*pipeline.PipelineResult, error) {
		refs, err := stores.ListContext(ctx, inputs.GetStore("references"))
		if err != nil {
			return nil, err
		}
		for _, item := range refs {
			if item.(*unstructured.Unstructured).GetName() != "consumer" {
				continue
			}
			if err := validateLazyDependency(ctx, inputs); err != nil {
				if errors.Is(err, stores.ErrSnapshotChanged) {
					unavailable.Add(1)
				}
				return nil, err
			}
		}
		encoded, err := json.Marshal(refs)
		if err != nil {
			return nil, err
		}
		return &pipeline.PipelineResult{HAProxyConfig: string(encoded)}, nil
	}
}

func validateLazyDependency(ctx context.Context, inputs stores.StoreProvider) error {
	dependencies, err := stores.GetContext(ctx, inputs.GetStore("dependencies"), "default", "dependency")
	if err != nil {
		return err
	}
	if len(dependencies) != 1 {
		return errors.New("dependency is missing")
	}
	value, _, _ := unstructured.NestedString(dependencies[0].(map[string]any), "spec", "value")
	if value != "valid" {
		return errors.New("dependency is invalid")
	}
	return nil
}
