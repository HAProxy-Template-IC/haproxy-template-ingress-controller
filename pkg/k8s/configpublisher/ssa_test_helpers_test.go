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

package configpublisher

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sync/atomic"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/testing"

	haproxyv1alpha1 "github.com/haproxy-haptic/HAPTIC/pkg/apis/haproxytemplate/v1alpha1"
	"github.com/haproxy-haptic/HAPTIC/pkg/generated/clientset/versioned/fake"
)

// installSSAListMapMergeReactor wires a reactor on the fake clientset that
// implements the subset of Server-Side Apply semantics the production code
// relies on: per-pod field managers writing into a listType=map keyed by
// podName end up with the merged list, not the last-writer's payload.
//
// The upstream client-go fake clientset's tracker treats ApplyPatchType as
// a typed Patch overlay — it doesn't read the listMap keys, so concurrent
// per-pod applies overwrite each other. This reactor sits in front of the
// tracker for the HAProxyCfg / HAProxyMapFile / HAProxyGeneralFile /
// HAProxyCRTListFile types, intercepts ApplyPatchType actions on the
// status subresource, and does the listmap merge by podName before
// writing back through the tracker's normal Update path.
//
// Test-only: stripped from any binary that imports this package outside
// `go test`.
func installSSAListMapMergeReactor(c *fake.Clientset) {
	installRuntimeUIDReactor(c)
	c.PrependReactor("patch", "*", func(action testing.Action) (bool, runtime.Object, error) {
		pa, ok := action.(testing.PatchAction)
		if !ok {
			return false, nil, nil // not a PatchAction we recognise
		}
		if pa.GetPatchType() != types.ApplyPatchType {
			return false, nil, nil // not Server-Side Apply
		}
		if pa.GetSubresource() != "status" {
			return false, nil, nil // we only merge status patches here
		}

		gvr := pa.GetResource()
		ns := pa.GetNamespace()
		name := pa.GetName()

		var patchObj map[string]any
		if err := json.Unmarshal(pa.GetPatch(), &patchObj); err != nil {
			return true, nil, fmt.Errorf("ssa reactor: unmarshal patch: %w", err)
		}
		patchPods := extractDeployedToPodsFromPatch(patchObj)
		if patchPods == nil {
			// Nothing to merge — let the default reactor handle it.
			return false, nil, nil
		}

		existing, err := c.Tracker().Get(gvr, ns, name)
		if err != nil {
			// Including NotFound: SSA against the status subresource of a
			// non-existent resource is a NotFound in real K8s too.
			return true, nil, err
		}

		changed, err := applyStatusEntries(existing, action.(testing.PatchActionImpl).PatchOptions.FieldManager, patchPods)
		if err != nil {
			return true, nil, err
		}
		if !changed {
			// Like the API server, an apply that changes nothing stores nothing.
			return true, existing, nil
		}

		// Write the merged object back through the tracker.
		if err := c.Tracker().Update(gvr, existing, ns); err != nil {
			return true, nil, fmt.Errorf("ssa reactor: tracker update: %w", err)
		}
		return true, existing, nil
	})
}

// applyStatusEntries merges pods into obj's deployedToPods and gives manager
// their ownership, reporting whether obj changed.
func applyStatusEntries(obj runtime.Object, manager string, pods []haproxyv1alpha1.PodDeploymentStatus) (bool, error) {
	accessor, err := meta.Accessor(obj)
	if err != nil {
		return false, err
	}
	current := getDeployedToPods(obj)
	merged := mergeDeployedToPods(current, pods)
	managed := ownStatusEntries(accessor.GetManagedFields(), manager, pods)
	if reflect.DeepEqual(merged, current) && reflect.DeepEqual(managed, accessor.GetManagedFields()) {
		return false, nil
	}
	setDeployedToPods(obj, merged)
	accessor.SetManagedFields(managed)
	return true, nil
}

func installRuntimeUIDReactor(c *fake.Clientset) {
	var sequence atomic.Uint64
	c.PrependReactor("create", "haproxycfgs", func(action testing.Action) (bool, runtime.Object, error) {
		object := action.(testing.CreateAction).GetObject().(*haproxyv1alpha1.HAProxyCfg)
		if object.UID == "" {
			object.UID = types.UID(fmt.Sprintf("runtime-uid-%d", sequence.Add(1)))
		}
		return false, nil, nil
	})
}

// extractDeployedToPodsFromPatch pulls .status.deployedToPods out of the
// unmarshaled SSA payload. Returns nil if the patch doesn't carry a
// deployedToPods array (so the caller falls back to the default reactor).
func extractDeployedToPodsFromPatch(patch map[string]any) []haproxyv1alpha1.PodDeploymentStatus {
	status, ok := patch["status"].(map[string]any)
	if !ok {
		return nil
	}
	rawPods, ok := status["deployedToPods"].([]any)
	if !ok {
		return nil
	}
	result := make([]haproxyv1alpha1.PodDeploymentStatus, 0, len(rawPods))
	for _, raw := range rawPods {
		// Round-trip through the type's own json tags so a field added to
		// PodDeploymentStatus is merged here without touching this helper.
		encoded, err := json.Marshal(raw)
		if err != nil {
			continue
		}
		var p haproxyv1alpha1.PodDeploymentStatus
		if err := json.Unmarshal(encoded, &p); err != nil {
			continue
		}
		result = append(result, p)
	}
	return result
}

// mergeDeployedToPods merges patch entries into existing entries by podName.
// New podName → append. Existing podName → fields from the patch replace
// the existing entry's fields (matches SSA semantics where each per-pod
// field manager owns its own entry's fields).
func mergeDeployedToPods(existing, patch []haproxyv1alpha1.PodDeploymentStatus) []haproxyv1alpha1.PodDeploymentStatus {
	byName := make(map[string]int, len(existing))
	out := append([]haproxyv1alpha1.PodDeploymentStatus(nil), existing...)
	for i := range out {
		byName[out[i].PodName] = i
	}
	for i := range patch {
		if idx, ok := byName[patch[i].PodName]; ok {
			out[idx] = patch[i]
		} else {
			out = append(out, patch[i])
			byName[patch[i].PodName] = len(out) - 1
		}
	}
	return out
}

// getDeployedToPods reads the DeployedToPods field via reflection-free
// type switches. Covers all four CRD types that carry one.
func getDeployedToPods(obj runtime.Object) []haproxyv1alpha1.PodDeploymentStatus {
	switch o := obj.(type) {
	case *haproxyv1alpha1.HAProxyCfg:
		return o.Status.DeployedToPods
	case *haproxyv1alpha1.HAProxyMapFile:
		return o.Status.DeployedToPods
	case *haproxyv1alpha1.HAProxyGeneralFile:
		return o.Status.DeployedToPods
	case *haproxyv1alpha1.HAProxyCRTListFile:
		return o.Status.DeployedToPods
	}
	return nil
}

// setDeployedToPods writes DeployedToPods via the same type-switch dispatch.
func setDeployedToPods(obj runtime.Object, pods []haproxyv1alpha1.PodDeploymentStatus) {
	switch o := obj.(type) {
	case *haproxyv1alpha1.HAProxyCfg:
		o.Status.DeployedToPods = pods
	case *haproxyv1alpha1.HAProxyMapFile:
		o.Status.DeployedToPods = pods
	case *haproxyv1alpha1.HAProxyGeneralFile:
		o.Status.DeployedToPods = pods
	case *haproxyv1alpha1.HAProxyCRTListFile:
		o.Status.DeployedToPods = pods
	}
}

// ownStatusEntries gives manager one Apply entry owning the applied pods'
// status entries, as the API server records it; an unchanged entry keeps its
// time.
func ownStatusEntries(entries []metav1.ManagedFieldsEntry, manager string, pods []haproxyv1alpha1.PodDeploymentStatus) []metav1.ManagedFieldsEntry {
	owned := map[string]any{}
	for i := range pods {
		owned[fmt.Sprintf(`k:{"podName":%q}`, pods[i].PodName)] = map[string]any{}
	}
	fields, err := json.Marshal(map[string]any{"f:status": map[string]any{"f:deployedToPods": owned}})
	if err != nil {
		panic(err)
	}
	want := metav1.ManagedFieldsEntry{
		Manager: manager, Operation: metav1.ManagedFieldsOperationApply, APIVersion: "haproxy-haptic.org/v1alpha1",
		Subresource: "status", FieldsType: "FieldsV1", FieldsV1: metav1.NewFieldsV1(string(fields)),
	}
	now := &metav1.Time{Time: time.Now().Truncate(time.Second)}
	out := make([]metav1.ManagedFieldsEntry, 0, len(entries)+1)
	owns := false
	for i := range entries {
		if entries[i].Manager != manager {
			out = append(out, entries[i])
			continue
		}
		if owns {
			continue
		}
		owns = true
		entry := want
		entry.Time = entries[i].Time
		if !reflect.DeepEqual(entries[i], entry) {
			entry.Time = now
		}
		out = append(out, entry)
	}
	if !owns {
		want.Time = now
		out = append(out, want)
	}
	return out
}
