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
		return fmt.Errorf("failed to list runtime configs: %w", err)
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
		return fmt.Errorf("failed to get runtime config: %w", err)
	}

	// Find ALL stale pods in one pass
	stalePods, newDeployedToPods := p.filterStalePods(cfg.Status.DeployedToPods, runningSet)
	if len(stalePods) == 0 {
		return nil
	}

	p.logger.Info("removing stale pod entries from HAProxyCfg status",
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
		return fmt.Errorf("failed to update status: %w", err)
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
			return fmt.Errorf("failed to get runtime config: %w", err)
		}

		// Remove pod from deployedToPods list
		newDeployedToPods, removed := p.removePodFromList(current.Status.DeployedToPods, cleanup)
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
			return fmt.Errorf("failed to update runtime config status: %w", err)
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

// removePodFromList removes a pod from the deployment status list.
func (p *Publisher) removePodFromList(pods []haproxyv1alpha1.PodDeploymentStatus, cleanup *PodCleanupRequest) ([]haproxyv1alpha1.PodDeploymentStatus, bool) {
	newPods := []haproxyv1alpha1.PodDeploymentStatus{}
	removed := false

	for i := range pods {
		if pods[i].PodName == cleanup.PodName {
			removed = true
			continue // Skip this pod
		}
		newPods = append(newPods, pods[i])
	}

	return newPods, removed
}

// cleanupAuxiliaryFilePodReferences removes pod reference from all auxiliary files (map files, general files, crt-list files).
func (p *Publisher) cleanupAuxiliaryFilePodReferences(ctx context.Context, auxFiles *haproxyv1alpha1.AuxiliaryFileReferences, cleanup *PodCleanupRequest) {
	if auxFiles == nil {
		return
	}

	for _, mapFileRef := range auxFiles.MapFiles {
		if err := p.cleanupMapFilePodReference(ctx, mapFileRef.Namespace, mapFileRef.Name, *cleanup); err != nil {
			p.logger.Warn("failed to cleanup map file pod reference",
				"mapFile", mapFileRef.Name,
				"error", err,
			)
			// Non-blocking - continue
		}
	}

	for _, generalFileRef := range auxFiles.GeneralFiles {
		if err := p.cleanupGeneralFilePodReference(ctx, generalFileRef.Namespace, generalFileRef.Name, *cleanup); err != nil {
			p.logger.Warn("failed to cleanup general file pod reference",
				"generalFile", generalFileRef.Name,
				"error", err,
			)
			// Non-blocking - continue
		}
	}

	for _, crtListFileRef := range auxFiles.CRTListFiles {
		if err := p.cleanupCRTListFilePodReference(ctx, crtListFileRef.Namespace, crtListFileRef.Name, *cleanup); err != nil {
			p.logger.Warn("failed to cleanup crt-list file pod reference",
				"crtListFile", crtListFileRef.Name,
				"error", err,
			)
			// Non-blocking - continue
		}
	}
}

// reconcileAuxiliaryFilePods removes stale pod entries from all auxiliary files.
// Unlike cleanupAuxiliaryFilePodReferences which handles one pod at a time,
// this processes all pods in a single pass per file to minimize API calls.
func (p *Publisher) reconcileAuxiliaryFilePods(ctx context.Context, auxFiles *haproxyv1alpha1.AuxiliaryFileReferences, runningSet map[string]struct{}) {
	for _, ref := range auxFiles.MapFiles {
		if err := p.reconcileMapFilePods(ctx, ref.Namespace, ref.Name, runningSet); err != nil {
			p.logger.Warn("failed to reconcile map file pods", "name", ref.Name, "error", err)
		}
	}
	for _, ref := range auxFiles.GeneralFiles {
		if err := p.reconcileGeneralFilePods(ctx, ref.Namespace, ref.Name, runningSet); err != nil {
			p.logger.Warn("failed to reconcile general file pods", "name", ref.Name, "error", err)
		}
	}
	for _, ref := range auxFiles.CRTListFiles {
		if err := p.reconcileCRTListFilePods(ctx, ref.Namespace, ref.Name, runningSet); err != nil {
			p.logger.Warn("failed to reconcile crt-list file pods", "name", ref.Name, "error", err)
		}
	}
}

// reconcileMapFilePods removes all stale pods from a map file in one update.
func (p *Publisher) reconcileMapFilePods(ctx context.Context, namespace, name string, runningSet map[string]struct{}) error {
	return mutateAuxFilePodStatus(
		func() (*auxFileHandle, error) { return p.mapFileHandle(ctx, namespace, name) },
		filterRunningPods(runningSet, func(removed []string) {
			p.logger.Debug("removing stale pods from map file", "name", name, "removed_pods", removed)
		}),
	)
}

// reconcileGeneralFilePods removes all stale pods from a general file in one update.
func (p *Publisher) reconcileGeneralFilePods(ctx context.Context, namespace, name string, runningSet map[string]struct{}) error {
	return mutateAuxFilePodStatus(
		func() (*auxFileHandle, error) { return p.generalFileHandle(ctx, namespace, name) },
		filterRunningPods(runningSet, func(removed []string) {
			p.logger.Debug("removing stale pods from general file", "name", name, "removed_pods", removed)
		}),
	)
}

// reconcileCRTListFilePods removes all stale pods from a crt-list file in one update.
func (p *Publisher) reconcileCRTListFilePods(ctx context.Context, namespace, name string, runningSet map[string]struct{}) error {
	return mutateAuxFilePodStatus(
		func() (*auxFileHandle, error) { return p.crtListFileHandle(ctx, namespace, name) },
		filterRunningPods(runningSet, func(removed []string) {
			p.logger.Debug("removing stale pods from crt-list file", "name", name, "removed_pods", removed)
		}),
	)
}

// cleanupMapFilePodReference removes a pod from a map file's deployment status.
func (p *Publisher) cleanupMapFilePodReference(ctx context.Context, namespace, name string, cleanup PodCleanupRequest) error {
	return mutateAuxFilePodStatus(
		func() (*auxFileHandle, error) { return p.mapFileHandle(ctx, namespace, name) },
		removePodMutation(cleanup.PodName),
	)
}

// cleanupGeneralFilePodReference removes a pod from a general file's deployment status.
func (p *Publisher) cleanupGeneralFilePodReference(ctx context.Context, namespace, name string, cleanup PodCleanupRequest) error {
	return mutateAuxFilePodStatus(
		func() (*auxFileHandle, error) { return p.generalFileHandle(ctx, namespace, name) },
		removePodMutation(cleanup.PodName),
	)
}

// cleanupCRTListFilePodReference removes a pod from a crt-list file's deployment status.
func (p *Publisher) cleanupCRTListFilePodReference(ctx context.Context, namespace, name string, cleanup PodCleanupRequest) error {
	return mutateAuxFilePodStatus(
		func() (*auxFileHandle, error) { return p.crtListFileHandle(ctx, namespace, name) },
		removePodMutation(cleanup.PodName),
	)
}
