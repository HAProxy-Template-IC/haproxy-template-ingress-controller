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
	"fmt"

	haproxyv1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
)

// CleanupPodReferences removes a terminated pod from all deployment status lists.
//
// This method removes the pod from:
// - All HAProxyCfg.status.deployedToPods in the specified namespace.
// - All HAProxyMapFile.status.deployedToPods in the specified namespace.
//
// The namespace parameter ensures namespace-scoped operations. The controller
// should only manage CRDs in its own namespace.
func (p *Publisher) CleanupPodReferences(ctx context.Context, cleanup *PodCleanupRequest) error {
	p.logger.Debug("cleaning up pod references",
		"pod", cleanup.PodName,
		"namespace", cleanup.Namespace,
	)

	// List HAProxyCfgs in the specified namespace only (namespace-scoped).
	// The controller manages CRDs in its own namespace, not cluster-wide.
	runtimeConfigs, err := p.crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs(cleanup.Namespace).
		List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("listing runtime configs: %w", err)
	}

	for i := range runtimeConfigs.Items {
		p.cleanupRuntimeConfigPodReference(ctx, &runtimeConfigs.Items[i], cleanup)
	}

	return nil
}

// ReconcileDeployedToPods removes status entries for pods that no longer exist.
//
// This reconciles the deployedToPods status in HAProxyCfg resources against
// the list of currently running HAProxy pods. Entries for pods not in the running
// set are removed. This cleans up stale entries from pods that terminated while
// the controller was restarting.
//
// Also cleans up corresponding entries in auxiliary file resources (HAProxyMapFile,
// HAProxyGeneralFile, HAProxyCRTListFile).
//
// The namespace parameter ensures namespace-scoped operations. The controller
// should only manage CRDs in its own namespace.
//
// Uses retry-on-conflict to handle concurrent updates.
func (p *Publisher) ReconcileDeployedToPods(ctx context.Context, namespace string, runningPodNames []string) error {
	runningSet := make(map[string]struct{}, len(runningPodNames))
	for _, name := range runningPodNames {
		runningSet[name] = struct{}{}
	}

	// List HAProxyCfgs in the specified namespace only (namespace-scoped).
	// The controller manages CRDs in its own namespace, not cluster-wide.
	runtimeConfigs, err := p.crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs(namespace).
		List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("listing HAProxyCfgs: %w", err)
	}

	for i := range runtimeConfigs.Items {
		listedCfg := &runtimeConfigs.Items[i]

		// Track auxiliary files for cleanup after main update
		var auxFiles *haproxyv1alpha1.AuxiliaryFileReferences

		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			return p.reconcileSingleRuntimeConfigStatus(ctx, listedCfg, runningSet, &auxFiles)
		})
		if err != nil {
			p.logger.Warn("failed to reconcile HAProxyCfg status",
				"name", listedCfg.Name,
				"error", err,
			)
		}

		// Also clean up auxiliary file status (map files, general files, crt-list files)
		// Use batched cleanup to minimize API calls (one update per file vs one per pod)
		if auxFiles != nil {
			p.reconcileAuxiliaryFilePods(ctx, auxFiles, runningSet)
		}
	}

	return nil
}

// reconcileSingleRuntimeConfigStatus reconciles the DeployedToPods status for a single HAProxyCfg.
// It fetches a fresh copy, filters out stale pods, and updates the status.
// auxFilesOut is populated with auxiliary files reference for cleanup after update.
func (p *Publisher) reconcileSingleRuntimeConfigStatus(
	ctx context.Context,
	listedCfg *haproxyv1alpha1.HAProxyCfg,
	runningSet map[string]struct{},
	auxFilesOut **haproxyv1alpha1.AuxiliaryFileReferences,
) error {
	// Fetch fresh copy of the resource
	cfg, err := p.crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs(listedCfg.Namespace).
		Get(ctx, listedCfg.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil // Resource deleted
		}
		return fmt.Errorf("getting runtime config: %w", err)
	}

	// Find ALL stale pods in one pass
	stalePods, newDeployedToPods := p.filterStalePods(cfg.Status.DeployedToPods, runningSet)
	if len(stalePods) == 0 {
		return nil
	}

	p.logger.Debug("removing stale pod entries from HAProxyCfg status",
		"name", cfg.Name,
		"namespace", cfg.Namespace,
		"stale_pods", stalePods,
	)

	// Store auxiliary files reference for cleanup after update
	*auxFilesOut = cfg.Status.AuxiliaryFiles

	// Update status once with all stale pods removed
	cfg.Status.DeployedToPods = newDeployedToPods
	_, err = p.crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs(cfg.Namespace).
		UpdateStatus(ctx, cfg, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("updating status: %w", err)
	}

	return nil
}

// filterStalePods separates stale pods from running pods.
// Returns the list of stale pod names and the filtered list of running pods.
func (p *Publisher) filterStalePods(
	deployedToPods []haproxyv1alpha1.PodDeploymentStatus,
	runningSet map[string]struct{},
) (stalePods []string, runningPods []haproxyv1alpha1.PodDeploymentStatus) {
	runningPods = make([]haproxyv1alpha1.PodDeploymentStatus, 0, len(deployedToPods))
	for i := range deployedToPods {
		pod := &deployedToPods[i]
		if _, exists := runningSet[pod.PodName]; !exists {
			stalePods = append(stalePods, pod.PodName)
		} else {
			runningPods = append(runningPods, *pod)
		}
	}
	return stalePods, runningPods
}

// cleanupRuntimeConfigPodReference removes pod reference from a single HAProxyCfg.
// Uses retry-on-conflict to handle concurrent updates.
func (p *Publisher) cleanupRuntimeConfigPodReference(ctx context.Context, runtimeConfig *haproxyv1alpha1.HAProxyCfg, cleanup *PodCleanupRequest) {
	// Track auxiliary files for cleanup after main update
	var auxFiles *haproxyv1alpha1.AuxiliaryFileReferences

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Fetch fresh copy of the resource
		current, err := p.crdClient.HaproxyTemplateICV1alpha1().
			HAProxyCfgs(runtimeConfig.Namespace).
			Get(ctx, runtimeConfig.Name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return nil // Resource deleted, nothing to clean up
			}
			return fmt.Errorf("getting runtime config: %w", err)
		}

		// Remove pod from deployedToPods list
		newDeployedToPods, removed := removePodFromStatus(current.Status.DeployedToPods, cleanup.PodName)
		if !removed {
			return nil // Pod not in this runtime config
		}

		// Store auxiliary files reference for cleanup after update
		auxFiles = current.Status.AuxiliaryFiles

		current.Status.DeployedToPods = newDeployedToPods

		_, err = p.crdClient.HaproxyTemplateICV1alpha1().
			HAProxyCfgs(current.Namespace).
			UpdateStatus(ctx, current, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("updating runtime config status: %w", err)
		}

		return nil
	})
	if err != nil {
		p.logger.Debug("status update conflict during cleanup (will retry on next reconciliation)",
			"type", "runtime_config_status",
			"name", runtimeConfig.Name,
			"error", err,
		)
		// Non-blocking - continue with other runtime configs
		return
	}

	// Clean up auxiliary files (map files, general files, crt-list files)
	if auxFiles != nil {
		p.cleanupAuxiliaryFilePodReferences(ctx, auxFiles, cleanup)
	}
}

// auxFileGroup binds an AuxiliaryFileReferences slice to the metadata needed
// to operate on each referenced resource: a human-readable label for log
// messages, a slog key for the file name, a handle accessor, and an optional
// cached-read accessor that tries to satisfy the read from an informer cache.
type auxFileGroup struct {
	refs          []haproxyv1alpha1.ResourceReference
	label         string // e.g. "map file" — interpolated into log messages
	logKey        string // e.g. "map_file" — slog field key for the file name
	handle        func(ctx context.Context, namespace, name string) (*auxFileHandle, error)
	tryCachedRead func(namespace, name string) *cachedAuxFileStatus
}

// auxFileGroupsFor collects the per-type metadata for each auxiliary file
// reference list on a HAProxyCfg's AuxiliaryFiles status.
func (p *Publisher) auxFileGroupsFor(auxFiles *haproxyv1alpha1.AuxiliaryFileReferences) []auxFileGroup {
	if auxFiles == nil {
		return nil
	}
	return []auxFileGroup{
		{auxFiles.MapFiles, "map file", "map_file", p.mapFileHandle, p.cachedMapFileStatus},
		{auxFiles.GeneralFiles, "general file", "general_file", p.generalFileHandle, p.cachedGeneralFileStatus},
		{auxFiles.CRTListFiles, "crt-list file", "crt_list_file", p.crtListFileHandle, p.cachedCRTListFileStatus},
	}
}

// cachedMapFileStatus returns the cached status of a HAProxyMapFile from the
// informer cache, or nil when listers aren't configured / the read fails.
func (p *Publisher) cachedMapFileStatus(namespace, name string) *cachedAuxFileStatus {
	if p.listers == nil || p.listers.MapFiles == nil {
		return nil
	}
	cached, err := p.listers.MapFiles.HAProxyMapFiles(namespace).Get(name)
	if err != nil {
		return nil
	}
	return &cachedAuxFileStatus{pods: cached.Status.DeployedToPods, checksum: cached.Spec.Checksum}
}

// cachedGeneralFileStatus mirrors cachedMapFileStatus for HAProxyGeneralFile.
func (p *Publisher) cachedGeneralFileStatus(namespace, name string) *cachedAuxFileStatus {
	if p.listers == nil || p.listers.GeneralFiles == nil {
		return nil
	}
	cached, err := p.listers.GeneralFiles.HAProxyGeneralFiles(namespace).Get(name)
	if err != nil {
		return nil
	}
	return &cachedAuxFileStatus{pods: cached.Status.DeployedToPods, checksum: cached.Spec.Checksum}
}

// cachedCRTListFileStatus mirrors cachedMapFileStatus for HAProxyCRTListFile.
func (p *Publisher) cachedCRTListFileStatus(namespace, name string) *cachedAuxFileStatus {
	if p.listers == nil || p.listers.CRTListFiles == nil {
		return nil
	}
	cached, err := p.listers.CRTListFiles.HAProxyCRTListFiles(namespace).Get(name)
	if err != nil {
		return nil
	}
	return &cachedAuxFileStatus{pods: cached.Status.DeployedToPods, checksum: cached.Spec.Checksum}
}

// cleanupAuxiliaryFilePodReferences removes pod reference from all auxiliary files (map files, general files, crt-list files).
func (p *Publisher) cleanupAuxiliaryFilePodReferences(ctx context.Context, auxFiles *haproxyv1alpha1.AuxiliaryFileReferences, cleanup *PodCleanupRequest) {
	for _, group := range p.auxFileGroupsFor(auxFiles) {
		for _, ref := range group.refs {
			err := mutateAuxFilePodStatus(
				func() (*auxFileHandle, error) { return group.handle(ctx, ref.Namespace, ref.Name) },
				removePodMutation(cleanup.PodName),
			)
			if err != nil {
				p.logger.Warn("failed to cleanup "+group.label+" pod reference",
					group.logKey, ref.Name,
					"error", err,
				)
				// Non-blocking - continue
			}
		}
	}
}

// reconcileAuxiliaryFilePods removes stale pod entries from all auxiliary files.
// Unlike cleanupAuxiliaryFilePodReferences which handles one pod at a time,
// this processes all pods in a single pass per file to minimize API calls.
func (p *Publisher) reconcileAuxiliaryFilePods(ctx context.Context, auxFiles *haproxyv1alpha1.AuxiliaryFileReferences, runningSet map[string]struct{}) {
	for _, group := range p.auxFileGroupsFor(auxFiles) {
		for _, ref := range group.refs {
			err := mutateAuxFilePodStatus(
				func() (*auxFileHandle, error) { return group.handle(ctx, ref.Namespace, ref.Name) },
				filterRunningPods(runningSet, func(removed []string) {
					p.logger.Debug("removing stale pods from "+group.label, "name", ref.Name, "removed_pods", removed)
				}),
			)
			if err != nil {
				p.logger.Warn("failed to reconcile "+group.label+" pods", "name", ref.Name, "error", err)
			}
		}
	}
}
