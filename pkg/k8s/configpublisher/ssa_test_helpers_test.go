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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/testing"

	haproxyv1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/generated/clientset/versioned/fake"
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
			if !apierrors.IsNotFound(err) {
				return true, nil, err
			}
			// Resource doesn't exist yet — SSA against the status subresource
			// on a non-existent resource is a NotFound in real K8s too. Return
			// the same error so tests can assert on it.
			return true, nil, err
		}

		merged := mergeDeployedToPods(getDeployedToPods(existing), patchPods)
		setDeployedToPods(existing, merged)

		// Write the merged object back through the tracker.
		if err := c.Tracker().Update(gvr, existing, ns); err != nil {
			return true, nil, fmt.Errorf("ssa reactor: tracker update: %w", err)
		}
		return true, existing, nil
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
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		var p haproxyv1alpha1.PodDeploymentStatus
		if v, ok := entry["podName"].(string); ok {
			p.PodName = v
		}
		if v, ok := entry["podUID"].(string); ok {
			p.PodUID = v
		}
		if v, ok := entry["podRuntimeID"].(string); ok {
			p.PodRuntimeID = v
		}
		if v, ok := entry["checksum"].(string); ok {
			p.Checksum = v
		}
		if v, ok := entry["lastError"].(string); ok {
			p.LastError = v
		}
		if v, ok := entry["consecutiveErrors"].(float64); ok {
			p.ConsecutiveErrors = int(v)
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
	for i, p := range out {
		byName[p.PodName] = i
	}
	for _, p := range patch {
		if idx, ok := byName[p.PodName]; ok {
			out[idx] = p
		} else {
			out = append(out, p)
			byName[p.PodName] = len(out) - 1
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

// avoid-unused (metav1 + meta) — referenced as a smoke check that imports
// resolve when tests grow.
var _ = metav1.ObjectMeta{}
var _ = meta.IsListType
