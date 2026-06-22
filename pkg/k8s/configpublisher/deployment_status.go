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
	"context"
	"encoding/json"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	haproxyv1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
)

// apiVersionV1Alpha1 is the CRD API version used in SSA payloads. Hard-coded
// rather than derived because v1alpha1 is the only version we serve and a
// future bump is a deliberate operation that should update this constant too.
const apiVersionV1Alpha1 = "haproxy-haptic.org/v1alpha1"

// podStatusFieldManager returns the SSA field manager used for a specific
// pod's status entry. Each pod owns ONLY its own entry in deployedToPods —
// concurrent writes from different pods merge naturally via listType=map
// (keyed on podName) instead of fighting each other through last-write-wins
// on the full-object UpdateStatus path that preceded this design.
//
// The format embeds the pod name so cleanup paths can identify which
// field manager owns which entry without consulting external state. K8s
// field-manager names are limited to 128 chars and pod names cap at 63, so
// the combined length always fits.
func podStatusFieldManager(podName string) string {
	return "haptic-pod-status-" + podName
}

// UpdateDeploymentStatus updates the per-pod deployment status entry on
// HAProxyCfg and all child auxiliary file resources.
//
// Uses Server-Side Apply with one field manager per pod (see
// `podStatusFieldManager`). The CRDs declare deployedToPods as
// `listType=map listMapKey=podName`, so the API server merges
// each per-pod SSA into the existing list without overwriting entries owned
// by other pods' field managers. The previous read-modify-write design
// had a race where two pods completing deploys ~50ms apart would each read
// the same snapshot of deployedToPods and last-write-wins — silently
// dropping one pod's status entry. Confirmed via debug-logs/ from CI
// pipeline 2559320164: HAProxyCfg.status.deployedToPods ended up with
// only one of two HAProxy pods listed, and TestIngress* polls timed out
// because the missing pod never showed up at the latest spec.checksum.
func (p *Publisher) UpdateDeploymentStatus(ctx context.Context, update *DeploymentStatusUpdate) error {
	p.logger.Debug("updating deployment status (SSA)",
		"runtime_config", update.RuntimeConfigName,
		"pod", update.PodName,
	)

	// Build this pod's status entry once; reused for HAProxyCfg + every
	// auxiliary file the controller manages.
	podStatus := buildPodStatus(update)

	// Resolve auxiliary file references. SSA against HAProxyCfg.status
	// doesn't return them, so we need a cached read (or fall back to API)
	// to know which child resources to patch.
	auxFiles, err := p.resolveAuxiliaryFileReferences(ctx, update)
	if err != nil {
		// Auxiliary file lookups are best-effort during status updates —
		// the next reconcile retries. Don't fail the whole call.
		p.logger.Debug("auxiliary file reference lookup failed (proceeding with HAProxyCfg-only SSA)",
			"runtime_config", update.RuntimeConfigName,
			"error", err,
		)
	}

	// SSA-apply this pod's entry to HAProxyCfg.status.deployedToPods.
	if err := p.applyPodStatusToRuntimeConfig(ctx, update, &podStatus); err != nil {
		return fmt.Errorf("applying pod status to HAProxyCfg: %w", err)
	}

	// SSA-apply this pod's entry to every auxiliary file's
	// status.deployedToPods. Same per-pod field manager, same merge
	// semantics — independent of each other and of the HAProxyCfg apply.
	p.applyPodStatusToAuxiliaryFiles(ctx, auxFiles, update.PodName)

	return nil
}

// resolveAuxiliaryFileReferences finds the auxiliary file references for
// the HAProxyCfg this update targets. Tries the lister cache first to
// avoid an API GET on every pod-status update; falls back to the typed
// client on cache miss.
func (p *Publisher) resolveAuxiliaryFileReferences(ctx context.Context, update *DeploymentStatusUpdate) (*haproxyv1alpha1.AuxiliaryFileReferences, error) {
	if p.listers != nil && p.listers.HAProxyCfgs != nil {
		cached, err := p.listers.HAProxyCfgs.HAProxyCfgs(update.RuntimeConfigNamespace).Get(update.RuntimeConfigName)
		if err == nil {
			return cached.Status.AuxiliaryFiles, nil
		}
		// Cache miss falls through to API read.
	}

	runtimeConfig, err := p.crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs(update.RuntimeConfigNamespace).
		Get(ctx, update.RuntimeConfigName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Resource hasn't been published yet — the next reconcile retries.
			// Returning (nil, nil) here is intentional and not the "nilnil"
			// anti-pattern: nil auxFiles means "no aux files to update" and
			// callers branch on that explicitly.
			return nil, errAuxFilesNotPublishedYet
		}
		return nil, fmt.Errorf("getting HAProxyCfg: %w", err)
	}
	return runtimeConfig.Status.AuxiliaryFiles, nil
}

// errAuxFilesNotPublishedYet is the sentinel returned from
// resolveAuxiliaryFileReferences when the HAProxyCfg doesn't yet exist
// in the API. Callers compare with errors.Is to distinguish "not ready
// yet, will retry" from a real lookup failure.
var errAuxFilesNotPublishedYet = fmt.Errorf("HAProxyCfg not yet published")

// applyPodStatusToRuntimeConfig SSA-applies this pod's status entry to
// HAProxyCfg. Field manager is per-pod (`haptic-pod-status-<podName>`) so
// concurrent writes for different pods merge cleanly via listType=map.
func (p *Publisher) applyPodStatusToRuntimeConfig(ctx context.Context, update *DeploymentStatusUpdate, podStatus *haproxyv1alpha1.PodDeploymentStatus) error {
	ssaBytes, err := buildPodStatusSSAPayload("HAProxyCfg", update.RuntimeConfigName, update.RuntimeConfigNamespace, podStatus)
	if err != nil {
		return err
	}
	_, err = p.crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs(update.RuntimeConfigNamespace).
		Patch(ctx, update.RuntimeConfigName, types.ApplyPatchType, ssaBytes,
			metav1.PatchOptions{FieldManager: podStatusFieldManager(update.PodName), Force: new(true)},
			"status",
		)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Resource not published yet; the next reconcile will retry.
			return nil
		}
		return fmt.Errorf("ssa pod status on HAProxyCfg: %w", err)
	}
	return nil
}

// applyPodStatusToAuxiliaryFiles SSA-applies this pod's status entry to
// every child auxiliary file.
//
// Each auxiliary file has its OWN content checksum (spec.checksum). The
// pod's status entry on that aux file records that aux file's checksum,
// not the main HAProxyCfg's. This matches the pre-SSA behaviour and lets
// operators see "did pod X get aux file Y?" independently of the main
// config — a content-unchanged aux file keeps its checksum across main
// config updates instead of being rewritten on every reconcile.
//
// Errors are logged but don't fail the whole status update — the next
// reconcile retries any failures.
func (p *Publisher) applyPodStatusToAuxiliaryFiles(ctx context.Context, auxFiles *haproxyv1alpha1.AuxiliaryFileReferences, podName string) {
	if auxFiles == nil {
		return
	}
	fieldManager := podStatusFieldManager(podName)

	for _, ref := range auxFiles.MapFiles {
		checksum, ok := p.lookupMapFileChecksum(ctx, ref.Namespace, ref.Name)
		if !ok {
			continue
		}
		entry := haproxyv1alpha1.PodDeploymentStatus{PodName: podName, Checksum: checksum}
		p.applyAuxiliaryFilePodStatus("HAProxyMapFile", ref.Namespace, ref.Name, &entry, fieldManager,
			func(name string, data []byte, opts metav1.PatchOptions) error {
				_, err := p.crdClient.HaproxyTemplateICV1alpha1().HAProxyMapFiles(ref.Namespace).
					Patch(ctx, name, types.ApplyPatchType, data, opts, "status")
				return err
			})
	}
	for _, ref := range auxFiles.GeneralFiles {
		checksum, ok := p.lookupGeneralFileChecksum(ctx, ref.Namespace, ref.Name)
		if !ok {
			continue
		}
		entry := haproxyv1alpha1.PodDeploymentStatus{PodName: podName, Checksum: checksum}
		p.applyAuxiliaryFilePodStatus("HAProxyGeneralFile", ref.Namespace, ref.Name, &entry, fieldManager,
			func(name string, data []byte, opts metav1.PatchOptions) error {
				_, err := p.crdClient.HaproxyTemplateICV1alpha1().HAProxyGeneralFiles(ref.Namespace).
					Patch(ctx, name, types.ApplyPatchType, data, opts, "status")
				return err
			})
	}
	for _, ref := range auxFiles.CRTListFiles {
		checksum, ok := p.lookupCRTListFileChecksum(ctx, ref.Namespace, ref.Name)
		if !ok {
			continue
		}
		entry := haproxyv1alpha1.PodDeploymentStatus{PodName: podName, Checksum: checksum}
		p.applyAuxiliaryFilePodStatus("HAProxyCRTListFile", ref.Namespace, ref.Name, &entry, fieldManager,
			func(name string, data []byte, opts metav1.PatchOptions) error {
				_, err := p.crdClient.HaproxyTemplateICV1alpha1().HAProxyCRTListFiles(ref.Namespace).
					Patch(ctx, name, types.ApplyPatchType, data, opts, "status")
				return err
			})
	}
}

// applyAuxiliaryFilePodStatus builds and applies a per-pod SSA patch to a
// single auxiliary file's status. Common shape extracted because every
// CRD-type-specific patch above does the same thing modulo the typed
// client call.
func (p *Publisher) applyAuxiliaryFilePodStatus(kind, namespace, name string, entry *haproxyv1alpha1.PodDeploymentStatus, fieldManager string, patcher func(name string, data []byte, opts metav1.PatchOptions) error) {
	ssaBytes, err := buildPodStatusSSAPayload(kind, name, namespace, entry)
	if err != nil {
		p.logger.Debug("ssa payload build failed", "kind", kind, "name", name, "error", err)
		return
	}
	if err := patcher(name, ssaBytes, metav1.PatchOptions{FieldManager: fieldManager, Force: new(true)}); err != nil && !apierrors.IsNotFound(err) {
		p.logger.Debug("ssa pod status on auxiliary file failed", "kind", kind, "name", name, "error", err)
	}
}

// lookupMapFileChecksum returns the aux file's spec.checksum (cache first,
// API fallback). Returns (checksum, true) on success, ("", false) if the
// file isn't yet visible — the next reconcile retries.
func (p *Publisher) lookupMapFileChecksum(ctx context.Context, namespace, name string) (string, bool) {
	if p.listers != nil && p.listers.MapFiles != nil {
		if obj, err := p.listers.MapFiles.HAProxyMapFiles(namespace).Get(name); err == nil {
			return obj.Spec.Checksum, true
		}
	}
	obj, err := p.crdClient.HaproxyTemplateICV1alpha1().HAProxyMapFiles(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", false
	}
	return obj.Spec.Checksum, true
}

// lookupGeneralFileChecksum: same shape as lookupMapFileChecksum.
func (p *Publisher) lookupGeneralFileChecksum(ctx context.Context, namespace, name string) (string, bool) {
	if p.listers != nil && p.listers.GeneralFiles != nil {
		if obj, err := p.listers.GeneralFiles.HAProxyGeneralFiles(namespace).Get(name); err == nil {
			return obj.Spec.Checksum, true
		}
	}
	obj, err := p.crdClient.HaproxyTemplateICV1alpha1().HAProxyGeneralFiles(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", false
	}
	return obj.Spec.Checksum, true
}

// lookupCRTListFileChecksum: same shape as lookupMapFileChecksum.
func (p *Publisher) lookupCRTListFileChecksum(ctx context.Context, namespace, name string) (string, bool) {
	if p.listers != nil && p.listers.CRTListFiles != nil {
		if obj, err := p.listers.CRTListFiles.HAProxyCRTListFiles(namespace).Get(name); err == nil {
			return obj.Spec.Checksum, true
		}
	}
	obj, err := p.crdClient.HaproxyTemplateICV1alpha1().HAProxyCRTListFiles(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", false
	}
	return obj.Spec.Checksum, true
}

// buildPodStatusSSAPayload constructs the minimal Server-Side Apply payload
// containing exactly this pod's status entry. The payload claims ownership
// only of the single deployedToPods[].podName=<this pod> entry — every
// other field on the object (and every other entry in the list) is owned
// by some other field manager and untouched by this apply.
func buildPodStatusSSAPayload(kind, name, namespace string, podStatus *haproxyv1alpha1.PodDeploymentStatus) ([]byte, error) {
	// The status entry's fields need to be marshaled by name (matching the
	// CRD's openAPI schema) rather than as a typed Go struct, so the
	// resulting payload can be applied via the dynamic ApplyPatchType
	// codepath without referencing the typed scheme.
	entry := map[string]any{
		"podName": podStatus.PodName,
	}
	if podStatus.Checksum != "" {
		entry["checksum"] = podStatus.Checksum
	}
	if podStatus.LastError != "" {
		entry["lastError"] = podStatus.LastError
	}
	if podStatus.ConsecutiveErrors > 0 {
		entry["consecutiveErrors"] = podStatus.ConsecutiveErrors
	}

	metadata := map[string]any{"name": name}
	if namespace != "" {
		metadata["namespace"] = namespace
	}

	payload := map[string]any{
		"apiVersion": apiVersionV1Alpha1,
		"kind":       kind,
		"metadata":   metadata,
		"status": map[string]any{
			"deployedToPods": []any{entry},
		},
	}

	return json.Marshal(payload)
}
