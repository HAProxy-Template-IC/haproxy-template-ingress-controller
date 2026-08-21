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
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	haproxyv1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
)

// ErrRuntimeConfigNotPublished reports that the target HAProxyCfg does not
// exist in the API (yet). At startup the first deployment to the HAProxy pods
// races the initial HAProxyCfg publish: the per-pod status SSA can land a few
// hundred milliseconds BEFORE the publisher creates the resource. Callers must
// retry the update once the resource exists — swallowing this case silently
// loses the pod's deployedToPods entry until the next config change or drift
// check (up to driftPreventionInterval), which reads as a never-converging pod
// to checksum-equality consumers.
var ErrRuntimeConfigNotPublished = errors.New("HAProxyCfg not published yet")

const (
	statusSubresource = "status"
	runtimeConfigKind = "HAProxyCfg"

	// Auxiliary-file CRD kinds, used in SSA payloads, ownership references, and
	// cleanup/error messages across this package.
	kindMapFile     = "HAProxyMapFile"
	kindGeneralFile = "HAProxyGeneralFile"
	kindCRTListFile = "HAProxyCRTListFile"
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
	p.logger.Debug("Updating deployment status (SSA)",
		"runtime_config", update.RuntimeConfigName,
		"pod", update.PodName,
	)

	// Build this pod's status entry once; reused for HAProxyCfg + every
	// auxiliary file the controller manages.
	podStatus := buildPodStatus(update)

	// On a FAILED sync the pod did not receive update.Checksum, so advancing
	// deployedToPods[pod].checksum to it would make the entry equal
	// spec.checksum and falsely read as converged — the convergence signal is
	// checksum-equality (LastError is advisory and ignored by e.g. the e2e
	// poll). Preserve the last successfully-deployed checksum instead. Blanking
	// it is not an option: an empty checksum is omitted from the SSA payload
	// (buildPodStatusSSAPayload) and SSA would then DELETE the field this
	// manager already owns, losing the last-good value. So re-emit the existing
	// checksum unchanged; if the pod has no prior success, leave it empty so the
	// pod correctly reads as never-converged.
	if update.Error != "" {
		podStatus.Checksum = ""
		// The plan the pod still runs is unchanged by a failed sync, and it is
		// the baseline the next apply diffs against — same re-emit rule as the
		// checksum above, or SSA deletes it.
		if existing, ok := p.existingPodStatus(ctx, update.RuntimeConfigNamespace, update.RuntimeConfigName, update.PodName); ok &&
			existing.PodUID == update.PodUID && existing.PodRuntimeID == update.PodRuntimeID {
			podStatus.Checksum = existing.Checksum
			podStatus.AppliedPlanID = existing.AppliedPlanID
			podStatus.RunningPlanID = existing.RunningPlanID
			podStatus.Mode = existing.Mode
			podStatus.Reasons = existing.Reasons
		}
	}

	// SSA-apply this pod's entry to HAProxyCfg.status.deployedToPods.
	if err := p.applyPodStatusToRuntimeConfig(ctx, update, &podStatus); err != nil {
		return fmt.Errorf("applying pod status to HAProxyCfg: %w", err)
	}

	// On failure the pod received neither the new config nor the new auxiliary
	// files, so skip advancing their per-pod checksums — leaving the last-good
	// values untouched (same preserve-on-failure rationale as the main config
	// checksum above). The next successful deploy or reconcile re-applies them.
	if update.Error != "" {
		return nil
	}

	// Resolve auxiliary file references. SSA against HAProxyCfg.status
	// doesn't return them, so we need a cached read (or fall back to API)
	// to know which child resources to patch.
	auxFiles, err := p.resolveAuxiliaryFileReferences(ctx, update)
	if err != nil {
		// Auxiliary file lookups are best-effort during status updates —
		// the next reconcile retries. Don't fail the whole call.
		p.logger.Debug("Auxiliary file reference lookup failed (proceeding with HAProxyCfg-only SSA)",
			"runtime_config", update.RuntimeConfigName,
			"error", err,
		)
	}

	// SSA-apply this pod's entry to every auxiliary file's
	// status.deployedToPods. Same per-pod field manager, same merge
	// semantics — independent of each other and of the HAProxyCfg apply.
	p.applyPodStatusToAuxiliaryFiles(ctx, auxFiles, update.PodName, update.PodUID, update.PodRuntimeID, update.IsDriftCheck)

	return nil
}

// existingPodStatus returns the status currently recorded for podName in the
// HAProxyCfg (lister cache first, API fallback).
func (p *Publisher) existingPodStatus(ctx context.Context, namespace, name, podName string) (haproxyv1alpha1.PodDeploymentStatus, bool) {
	find := func(cfg *haproxyv1alpha1.HAProxyCfg) (haproxyv1alpha1.PodDeploymentStatus, bool) {
		for i := range cfg.Status.DeployedToPods {
			if cfg.Status.DeployedToPods[i].PodName == podName {
				return cfg.Status.DeployedToPods[i], true
			}
		}
		return haproxyv1alpha1.PodDeploymentStatus{}, false
	}
	if p.listers != nil && p.listers.HAProxyCfgs != nil {
		if cfg, err := p.listers.HAProxyCfgs.HAProxyCfgs(namespace).Get(name); err == nil {
			return find(cfg)
		}
	}
	cfg, err := p.crdClient.HaproxyTemplateICV1alpha1().HAProxyCfgs(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return haproxyv1alpha1.PodDeploymentStatus{}, false
	}
	return find(cfg)
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
	ssaBytes, err := buildPodStatusSSAPayload(runtimeConfigKind, update.RuntimeConfigName, update.RuntimeConfigNamespace, podStatus)
	if err != nil {
		return err
	}
	_, err = p.crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs(update.RuntimeConfigNamespace).
		Patch(ctx, update.RuntimeConfigName, types.ApplyPatchType, ssaBytes,
			metav1.PatchOptions{FieldManager: podStatusFieldManager(update.PodName), Force: new(true)},
			statusSubresource,
		)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Surface the miss so the caller retries once the resource is
			// published. This was previously swallowed as success, which
			// permanently lost the pod's entry (see ErrRuntimeConfigNotPublished).
			return fmt.Errorf("%w: %s/%s", ErrRuntimeConfigNotPublished,
				update.RuntimeConfigNamespace, update.RuntimeConfigName)
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
//
// Precondition — single-writer-per-pod: the caller must serialize all
// UpdateDeploymentStatus calls for a given pod, so this never runs twice
// concurrently for the same podName. The controller guarantees this — its
// processAllPendingStatusWork coalesces onto one goroutine per pod key. The
// final auxStamps.retainLivePodKeys deliberately does NOT bump the cache
// generation, which is only safe under this invariant (see its comment). A
// future caller that violates single-writer-per-pod (an independent
// drift-check path, a second worker pool) must add per-pod locking or make
// retainLivePodKeys bump the generation, or it silently reintroduces the
// stale-cache race the generation counter closes elsewhere.
func (p *Publisher) applyPodStatusToAuxiliaryFiles(ctx context.Context, auxFiles *haproxyv1alpha1.AuxiliaryFileReferences, podName, podUID, podRuntimeID string, driftCheck bool) {
	if auxFiles == nil {
		return
	}
	fieldManager := podStatusFieldManager(podName)

	// live is the set of keys for this pod's currently-referenced aux files.
	// After stamping, entries for keys no longer live are evicted so the
	// content-hashed names of a superseded set don't accumulate.
	live := make(map[stampKey]struct{}, len(auxFiles.MapFiles)+len(auxFiles.GeneralFiles)+len(auxFiles.CRTListFiles))

	for _, ref := range auxFiles.MapFiles {
		key := stampKey{kind: kindMapFile, namespace: ref.Namespace, name: ref.Name, podName: podName}
		live[key] = struct{}{}
		checksum, ok := p.lookupMapFileChecksum(ctx, ref.Namespace, ref.Name)
		if !ok {
			continue
		}
		p.stampAuxiliaryFilePodStatus(key, podUID, podRuntimeID, checksum, fieldManager, driftCheck,
			func(name string, data []byte, opts metav1.PatchOptions) error {
				_, err := p.crdClient.HaproxyTemplateICV1alpha1().HAProxyMapFiles(ref.Namespace).
					Patch(ctx, name, types.ApplyPatchType, data, opts, statusSubresource)
				return err
			})
	}
	for _, ref := range auxFiles.GeneralFiles {
		key := stampKey{kind: kindGeneralFile, namespace: ref.Namespace, name: ref.Name, podName: podName}
		live[key] = struct{}{}
		checksum, ok := p.lookupGeneralFileChecksum(ctx, ref.Namespace, ref.Name)
		if !ok {
			continue
		}
		p.stampAuxiliaryFilePodStatus(key, podUID, podRuntimeID, checksum, fieldManager, driftCheck,
			func(name string, data []byte, opts metav1.PatchOptions) error {
				_, err := p.crdClient.HaproxyTemplateICV1alpha1().HAProxyGeneralFiles(ref.Namespace).
					Patch(ctx, name, types.ApplyPatchType, data, opts, statusSubresource)
				return err
			})
	}
	for _, ref := range auxFiles.CRTListFiles {
		key := stampKey{kind: kindCRTListFile, namespace: ref.Namespace, name: ref.Name, podName: podName}
		live[key] = struct{}{}
		checksum, ok := p.lookupCRTListFileChecksum(ctx, ref.Namespace, ref.Name)
		if !ok {
			continue
		}
		p.stampAuxiliaryFilePodStatus(key, podUID, podRuntimeID, checksum, fieldManager, driftCheck,
			func(name string, data []byte, opts metav1.PatchOptions) error {
				_, err := p.crdClient.HaproxyTemplateICV1alpha1().HAProxyCRTListFiles(ref.Namespace).
					Patch(ctx, name, types.ApplyPatchType, data, opts, statusSubresource)
				return err
			})
	}

	p.auxStamps.retainLivePodKeys(podName, live)
}

// stampAuxiliaryFilePodStatus SSA-applies this pod's entry to one auxiliary
// file, unless the exact entry is already the last one applied for that key —
// in which case the Patch would write a byte-identical value and is elided
// (issue #163: content-hashed aux-file names make every re-stamp a no-op, and
// under churn they saturate the client rate limiter). driftCheck forces the
// Patch through the elision: a drift-prevention re-sync re-stamps every live
// (pod, file) once per interval, the authoritative periodic write that self-heals
// an out-of-band strip — the high-frequency inter-drift re-stamps are what get
// elided. Records the entry only on a successful apply and only if no
// invalidation raced the Patch (commitStamp), so a failed/NotFound Patch or a
// concurrent cleanup retries next time.
func (p *Publisher) stampAuxiliaryFilePodStatus(key stampKey, podUID, podRuntimeID, checksum, fieldManager string, driftCheck bool, patcher func(name string, data []byte, opts metav1.PatchOptions) error) {
	value := stampedEntry{podUID: podUID, podRuntimeID: podRuntimeID, checksum: checksum}
	skip, gen := p.auxStamps.beginStamp(key, value, driftCheck)
	if skip {
		return
	}
	entry := haproxyv1alpha1.PodDeploymentStatus{
		PodName: key.podName, PodUID: podUID, PodRuntimeID: podRuntimeID, Checksum: checksum,
	}
	if err := p.applyAuxiliaryFilePodStatus(key.kind, key.namespace, key.name, &entry, fieldManager, patcher); err == nil {
		p.auxStamps.commitStamp(key, value, gen)
	}
}

// applyAuxiliaryFilePodStatus builds and applies a per-pod SSA patch to a
// single auxiliary file's status. Common shape extracted because every
// CRD-type-specific patch above does the same thing modulo the typed
// client call. Returns the patch error (NotFound included, silently) so the
// caller knows whether the apply landed before caching the value.
func (p *Publisher) applyAuxiliaryFilePodStatus(kind, namespace, name string, entry *haproxyv1alpha1.PodDeploymentStatus, fieldManager string, patcher func(name string, data []byte, opts metav1.PatchOptions) error) error {
	ssaBytes, err := buildPodStatusSSAPayload(kind, name, namespace, entry)
	if err != nil {
		p.logger.Debug("Ssa payload build failed", "kind", kind, "name", name, "error", err)
		return err
	}
	if err := patcher(name, ssaBytes, metav1.PatchOptions{FieldManager: fieldManager, Force: new(true)}); err != nil {
		if !apierrors.IsNotFound(err) {
			p.logger.Debug("Ssa pod status on auxiliary file failed", "kind", kind, "name", name, "error", err)
		}
		return err
	}
	return nil
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
	if podStatus.PodUID != "" {
		entry["podUID"] = podStatus.PodUID
	}
	if podStatus.PodRuntimeID != "" {
		entry["podRuntimeID"] = podStatus.PodRuntimeID
	}
	if podStatus.Checksum != "" {
		entry["checksum"] = podStatus.Checksum
	}
	if podStatus.AppliedPlanID != "" {
		entry["appliedPlanID"] = podStatus.AppliedPlanID
	}
	if podStatus.RunningPlanID != "" {
		entry["runningPlanID"] = podStatus.RunningPlanID
	}
	if podStatus.Mode != "" {
		entry["mode"] = podStatus.Mode
	}
	if len(podStatus.Reasons) > 0 {
		entry["reasons"] = podStatus.Reasons
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
		statusSubresource: map[string]any{
			"deployedToPods": []any{entry},
		},
	}

	return json.Marshal(payload)
}
