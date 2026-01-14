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
	"crypto/sha256"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	haproxyv1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/compression"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/generated/clientset/versioned"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
)

// Publisher publishes HAProxy runtime configuration as Kubernetes resources.
//
// This is a pure component (no EventBus dependency) that creates and updates
// HAProxyCfg, HAProxyMapFile, and Secret resources to expose the
// actual runtime configuration applied to HAProxy pods.
type Publisher struct {
	k8sClient kubernetes.Interface
	crdClient versioned.Interface
	logger    *slog.Logger
}

// New creates a new Publisher instance.
func New(k8sClient kubernetes.Interface, crdClient versioned.Interface, logger *slog.Logger) *Publisher {
	return &Publisher{
		k8sClient: k8sClient,
		crdClient: crdClient,
		logger:    logger,
	}
}

// PublishConfig creates or updates HAProxyCfg and its child resources.
//
// This method:
// 1. Creates/updates HAProxyCfg with the rendered config
// 2. Creates/updates HAProxyMapFile resources for each map file
// 3. Creates/updates Secret resources for SSL certificates
// 4. Sets owner references for cascade deletion
// 5. Updates HAProxyCfg status with references to child resources
//
// Returns PublishResult containing the names of created/updated resources.
func (p *Publisher) PublishConfig(ctx context.Context, req *PublishRequest) (*PublishResult, error) {
	p.logger.Debug("publishing runtime config",
		"templateConfig", req.TemplateConfigName,
		"namespace", req.TemplateConfigNamespace,
	)

	// Create or update HAProxyCfg
	runtimeConfig, err := p.createOrUpdateRuntimeConfig(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to create/update runtime config: %w", err)
	}

	result := &PublishResult{
		RuntimeConfigName:      runtimeConfig.Name,
		RuntimeConfigNamespace: runtimeConfig.Namespace,
		MapFileNames:           []string{},
		SecretNames:            []string{},
		GeneralFileNames:       []string{},
		CRTListFileNames:       []string{},
	}

	// Publish auxiliary files (map files, SSL secrets, general files, crt-list files)
	if req.AuxiliaryFiles != nil {
		p.publishAuxiliaryFiles(ctx, req, runtimeConfig, result)
	}

	// Update HAProxyCfg status with child resource references
	if err := p.updateRuntimeConfigStatus(ctx, runtimeConfig, result); err != nil {
		p.logger.Warn("failed to update runtime config status",
			"name", runtimeConfig.Name,
			"error", err,
		)
		// Non-blocking - status update is informational
	}

	p.logger.Debug("published runtime config",
		"runtimeConfig", runtimeConfig.Name,
		"mapFiles", len(result.MapFileNames),
		"secrets", len(result.SecretNames),
		"generalFiles", len(result.GeneralFileNames),
		"crtListFiles", len(result.CRTListFileNames),
	)

	return result, nil
}

// publishAuxiliaryFiles creates or updates all auxiliary file resources.
// Errors are logged but don't fail the overall publish operation.
func (p *Publisher) publishAuxiliaryFiles(
	ctx context.Context,
	req *PublishRequest,
	runtimeConfig *haproxyv1alpha1.HAProxyCfg,
	result *PublishResult,
) {
	// Create or update map files
	for _, mapFile := range req.AuxiliaryFiles.MapFiles {
		mapFileName, err := p.createOrUpdateMapFile(ctx, req, runtimeConfig, mapFile)
		if err != nil {
			p.logger.Warn("failed to create/update map file",
				"name", mapFile.Path,
				"error", err,
			)
			continue
		}
		result.MapFileNames = append(result.MapFileNames, mapFileName)
	}

	// Create or update SSL certificate secrets
	for _, cert := range req.AuxiliaryFiles.SSLCertificates {
		secretName, err := p.createOrUpdateSSLSecret(ctx, req, runtimeConfig, cert)
		if err != nil {
			p.logger.Warn("failed to create/update SSL secret",
				"path", cert.Path,
				"error", err,
			)
			continue
		}
		result.SecretNames = append(result.SecretNames, secretName)
	}

	// Create or update general files
	for _, generalFile := range req.AuxiliaryFiles.GeneralFiles {
		generalFileName, err := p.createOrUpdateGeneralFile(ctx, req, runtimeConfig, generalFile)
		if err != nil {
			p.logger.Warn("failed to create/update general file",
				"name", generalFile.Filename,
				"error", err,
			)
			continue
		}
		result.GeneralFileNames = append(result.GeneralFileNames, generalFileName)
	}

	// Create or update crt-list files
	for _, crtListFile := range req.AuxiliaryFiles.CRTListFiles {
		crtListFileName, err := p.createOrUpdateCRTListFile(ctx, req, runtimeConfig, crtListFile)
		if err != nil {
			p.logger.Warn("failed to create/update crt-list file",
				"path", crtListFile.Path,
				"error", err,
			)
			continue
		}
		result.CRTListFileNames = append(result.CRTListFileNames, crtListFileName)
	}
}

// UpdateDeploymentStatus updates the deployment status to add a pod.
//
// This method adds the pod to the deployedToPods list in:
// - HAProxyCfg.status.deployedToPods.
// - All child HAProxyMapFile.status.deployedToPods.
// - (Secrets don't have deployment tracking).
//
// Uses retry-on-conflict to handle concurrent status updates from multiple
// reconciliation cycles, which can cause 409 Conflict errors due to Kubernetes
// optimistic concurrency control.
func (p *Publisher) UpdateDeploymentStatus(ctx context.Context, update *DeploymentStatusUpdate) error {
	p.logger.Debug("updating deployment status",
		"runtimeConfig", update.RuntimeConfigName,
		"pod", update.PodName,
	)

	// Track auxiliary files for later updates (populated inside retry loop)
	var auxFiles *haproxyv1alpha1.AuxiliaryFileReferences

	// Update HAProxyCfg status with retry-on-conflict
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		return p.updateRuntimeConfigDeploymentStatus(ctx, update, &auxFiles)
	})
	if err != nil {
		return err
	}

	// Build pod status for auxiliary file updates (outside retry loop)
	podStatus := buildPodStatus(update)

	// Update all child auxiliary files
	p.updateAuxiliaryFileDeploymentStatus(ctx, auxFiles, &podStatus)

	return nil
}

// updateRuntimeConfigDeploymentStatus updates the HAProxyCfg status with pod deployment info.
// This is the retry-able portion of UpdateDeploymentStatus.
func (p *Publisher) updateRuntimeConfigDeploymentStatus(ctx context.Context, update *DeploymentStatusUpdate, auxFilesOut **haproxyv1alpha1.AuxiliaryFileReferences) error {
	runtimeConfig, err := p.crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs(update.RuntimeConfigNamespace).
		Get(ctx, update.RuntimeConfigName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			p.logger.Debug("runtime config not found, skipping deployment status update",
				"name", update.RuntimeConfigName,
			)
			return nil // Not an error - resource might not be published yet
		}
		return fmt.Errorf("failed to get runtime config: %w", err)
	}

	// Store auxiliary files reference for updates after main status update
	*auxFilesOut = runtimeConfig.Status.AuxiliaryFiles

	// Build pod status from update
	podStatus := buildPodStatus(update)

	// Update or append pod status
	runtimeConfig.Status.DeployedToPods = updateOrAppendPodStatus(
		runtimeConfig.Status.DeployedToPods,
		&podStatus,
		update,
	)

	_, err = p.crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs(update.RuntimeConfigNamespace).
		UpdateStatus(ctx, runtimeConfig, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update runtime config status: %w", err)
	}

	return nil
}

// updateAuxiliaryFileDeploymentStatus updates deployment status on all auxiliary files.
func (p *Publisher) updateAuxiliaryFileDeploymentStatus(ctx context.Context, auxFiles *haproxyv1alpha1.AuxiliaryFileReferences, podStatus *haproxyv1alpha1.PodDeploymentStatus) {
	if auxFiles == nil {
		return
	}

	for _, mapFileRef := range auxFiles.MapFiles {
		if err := p.updateMapFileDeploymentStatus(ctx, mapFileRef.Namespace, mapFileRef.Name, podStatus); err != nil {
			p.logger.Warn("failed to update map file deployment status",
				"mapFile", mapFileRef.Name,
				"error", err,
			)
		}
	}

	for _, generalFileRef := range auxFiles.GeneralFiles {
		if err := p.updateGeneralFileDeploymentStatus(ctx, generalFileRef.Namespace, generalFileRef.Name, podStatus); err != nil {
			p.logger.Warn("failed to update general file deployment status",
				"generalFile", generalFileRef.Name,
				"error", err,
			)
		}
	}

	for _, crtListFileRef := range auxFiles.CRTListFiles {
		if err := p.updateCRTListFileDeploymentStatus(ctx, crtListFileRef.Namespace, crtListFileRef.Name, podStatus); err != nil {
			p.logger.Warn("failed to update crt-list file deployment status",
				"crtListFile", crtListFileRef.Name,
				"error", err,
			)
		}
	}
}

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

// DeleteRuntimeConfig deletes a HAProxyCfg resource.
//
// Used to clean up invalid configuration resources when validation succeeds again.
func (p *Publisher) DeleteRuntimeConfig(ctx context.Context, namespace, name string) error {
	err := p.crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs(namespace).
		Delete(ctx, name, metav1.DeleteOptions{})

	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete runtime config %s/%s: %w", namespace, name, err)
	}

	if err == nil {
		p.logger.Debug("deleted runtime config",
			"name", name,
			"namespace", namespace,
		)
	}

	return nil
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
		p.logger.Warn("failed to update runtime config status",
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
// Uses retry-on-conflict to handle concurrent updates.
func (p *Publisher) reconcileMapFilePods(ctx context.Context, namespace, name string, runningSet map[string]struct{}) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		mapFile, err := p.crdClient.HaproxyTemplateICV1alpha1().
			HAProxyMapFiles(namespace).
			Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("failed to get map file: %w", err)
		}

		// Filter to keep only running pods
		newPods := make([]haproxyv1alpha1.PodDeploymentStatus, 0, len(mapFile.Status.DeployedToPods))
		var removed []string
		for i := range mapFile.Status.DeployedToPods {
			pod := &mapFile.Status.DeployedToPods[i]
			if _, exists := runningSet[pod.PodName]; exists {
				newPods = append(newPods, *pod)
			} else {
				removed = append(removed, pod.PodName)
			}
		}

		if len(removed) == 0 {
			return nil
		}

		p.logger.Debug("removing stale pods from map file",
			"name", name,
			"removed_pods", removed,
		)

		mapFile.Status.DeployedToPods = newPods
		_, err = p.crdClient.HaproxyTemplateICV1alpha1().
			HAProxyMapFiles(namespace).
			UpdateStatus(ctx, mapFile, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update map file status: %w", err)
		}
		return nil
	})
}

// reconcileGeneralFilePods removes all stale pods from a general file in one update.
// Uses retry-on-conflict to handle concurrent updates.
func (p *Publisher) reconcileGeneralFilePods(ctx context.Context, namespace, name string, runningSet map[string]struct{}) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		generalFile, err := p.crdClient.HaproxyTemplateICV1alpha1().
			HAProxyGeneralFiles(namespace).
			Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("failed to get general file: %w", err)
		}

		// Filter to keep only running pods
		newPods := make([]haproxyv1alpha1.PodDeploymentStatus, 0, len(generalFile.Status.DeployedToPods))
		var removed []string
		for i := range generalFile.Status.DeployedToPods {
			pod := &generalFile.Status.DeployedToPods[i]
			if _, exists := runningSet[pod.PodName]; exists {
				newPods = append(newPods, *pod)
			} else {
				removed = append(removed, pod.PodName)
			}
		}

		if len(removed) == 0 {
			return nil
		}

		p.logger.Debug("removing stale pods from general file",
			"name", name,
			"removed_pods", removed,
		)

		generalFile.Status.DeployedToPods = newPods
		_, err = p.crdClient.HaproxyTemplateICV1alpha1().
			HAProxyGeneralFiles(namespace).
			UpdateStatus(ctx, generalFile, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update general file status: %w", err)
		}
		return nil
	})
}

// reconcileCRTListFilePods removes all stale pods from a crt-list file in one update.
// Uses retry-on-conflict to handle concurrent updates.
func (p *Publisher) reconcileCRTListFilePods(ctx context.Context, namespace, name string, runningSet map[string]struct{}) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		crtListFile, err := p.crdClient.HaproxyTemplateICV1alpha1().
			HAProxyCRTListFiles(namespace).
			Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("failed to get crt-list file: %w", err)
		}

		// Filter to keep only running pods
		newPods := make([]haproxyv1alpha1.PodDeploymentStatus, 0, len(crtListFile.Status.DeployedToPods))
		var removed []string
		for i := range crtListFile.Status.DeployedToPods {
			pod := &crtListFile.Status.DeployedToPods[i]
			if _, exists := runningSet[pod.PodName]; exists {
				newPods = append(newPods, *pod)
			} else {
				removed = append(removed, pod.PodName)
			}
		}

		if len(removed) == 0 {
			return nil
		}

		p.logger.Debug("removing stale pods from crt-list file",
			"name", name,
			"removed_pods", removed,
		)

		crtListFile.Status.DeployedToPods = newPods
		_, err = p.crdClient.HaproxyTemplateICV1alpha1().
			HAProxyCRTListFiles(namespace).
			UpdateStatus(ctx, crtListFile, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update crt-list file status: %w", err)
		}
		return nil
	})
}

// createOrUpdateRuntimeConfig creates or updates the HAProxyCfg resource.
func (p *Publisher) createOrUpdateRuntimeConfig(ctx context.Context, req *PublishRequest) (*haproxyv1alpha1.HAProxyCfg, error) {
	name := p.generateRuntimeConfigName(req.TemplateConfigName) + req.NameSuffix
	runtimeConfig := p.buildRuntimeConfig(name, req)

	// Try to get existing resource
	existing, err := p.crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs(req.TemplateConfigNamespace).
		Get(ctx, name, metav1.GetOptions{})

	if err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("failed to get existing runtime config: %w", err)
		}
		return p.createRuntimeConfig(ctx, req, runtimeConfig)
	}

	return p.updateRuntimeConfig(ctx, req, existing, runtimeConfig)
}

// buildRuntimeConfig constructs a HAProxyCfg resource from the request.
func (p *Publisher) buildRuntimeConfig(name string, req *PublishRequest) *haproxyv1alpha1.HAProxyCfg {
	// Compress if content exceeds threshold
	result := p.compressIfNeeded(req.Config, req.CompressionThreshold, "HAProxyCfg")

	runtimeConfig := &haproxyv1alpha1.HAProxyCfg{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: req.TemplateConfigNamespace,
			Labels: map[string]string{
				"haproxy-haptic.org/template-config": req.TemplateConfigName,
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         "haproxy-haptic.org/v1alpha1",
					Kind:               "HAProxyTemplateConfig",
					Name:               req.TemplateConfigName,
					UID:                req.TemplateConfigUID,
					Controller:         boolPtr(true),
					BlockOwnerDeletion: boolPtr(true),
				},
			},
		},
		Spec: haproxyv1alpha1.HAProxyCfgSpec{
			Path:       req.ConfigPath,
			Content:    result.content,
			Checksum:   req.Checksum, // Checksum is of original content
			Compressed: result.compressed,
		},
	}

	// Set validation error in status if provided
	if req.ValidationError != "" {
		if runtimeConfig.Status.Metadata == nil {
			runtimeConfig.Status.Metadata = &haproxyv1alpha1.ConfigMetadata{}
		}
		runtimeConfig.Status.ValidationError = req.ValidationError
		runtimeConfig.Status.Metadata.ValidatedAt = &metav1.Time{Time: time.Now()}
	}

	return runtimeConfig
}

// createRuntimeConfig creates a new HAProxyCfg resource.
func (p *Publisher) createRuntimeConfig(ctx context.Context, req *PublishRequest, runtimeConfig *haproxyv1alpha1.HAProxyCfg) (*haproxyv1alpha1.HAProxyCfg, error) {
	created, err := p.crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs(req.TemplateConfigNamespace).
		Create(ctx, runtimeConfig, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create runtime config: %w", err)
	}

	// Update status after creation if needed
	if req.ValidationError != "" || !req.ValidatedAt.IsZero() {
		p.updateCreatedStatus(ctx, req, created)
	} else {
		p.logger.Debug("created new runtime config, status will be updated on first deployment",
			"name", created.Name,
			"namespace", created.Namespace,
		)
	}

	return created, nil
}

// updateCreatedStatus updates the status of a newly created HAProxyCfg.
func (p *Publisher) updateCreatedStatus(ctx context.Context, req *PublishRequest, created *haproxyv1alpha1.HAProxyCfg) {
	// Initialize status metadata if needed
	if created.Status.Metadata == nil {
		created.Status.Metadata = &haproxyv1alpha1.ConfigMetadata{}
	}

	// Set metadata fields
	created.Status.Metadata.ContentSize = int64(len(req.Config))
	created.Status.Metadata.RenderedAt = &metav1.Time{Time: req.RenderedAt}
	if !req.ValidatedAt.IsZero() {
		created.Status.Metadata.ValidatedAt = &metav1.Time{Time: req.ValidatedAt}
	}

	// Set validation error if provided
	if req.ValidationError != "" {
		created.Status.ValidationError = req.ValidationError
	}

	// Update status
	_, err := p.crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs(req.TemplateConfigNamespace).
		UpdateStatus(ctx, created, metav1.UpdateOptions{})
	if err != nil {
		p.logger.Warn("failed to update runtime config status after creation",
			"name", created.Name,
			"error", err,
		)
	} else {
		p.logger.Debug("created and updated runtime config status",
			"name", created.Name,
			"namespace", created.Namespace,
			"has_validation_error", req.ValidationError != "",
		)
	}
}

// updateRuntimeConfig updates an existing HAProxyCfg resource.
func (p *Publisher) updateRuntimeConfig(ctx context.Context, req *PublishRequest, existing, runtimeConfig *haproxyv1alpha1.HAProxyCfg) (*haproxyv1alpha1.HAProxyCfg, error) {
	// Update existing resource
	existing.Spec = runtimeConfig.Spec
	existing.Labels = runtimeConfig.Labels

	updated, err := p.crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs(req.TemplateConfigNamespace).
		Update(ctx, existing, metav1.UpdateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to update runtime config: %w", err)
	}

	p.updateExistingStatus(ctx, req, updated)
	return updated, nil
}

// updateExistingStatus updates the status of an existing HAProxyCfg.
func (p *Publisher) updateExistingStatus(ctx context.Context, req *PublishRequest, updated *haproxyv1alpha1.HAProxyCfg) {
	// Update status metadata
	if updated.Status.Metadata == nil {
		updated.Status.Metadata = &haproxyv1alpha1.ConfigMetadata{}
	}
	updated.Status.Metadata.ContentSize = int64(len(req.Config))
	updated.Status.Metadata.RenderedAt = &metav1.Time{Time: req.RenderedAt}
	if !req.ValidatedAt.IsZero() {
		updated.Status.Metadata.ValidatedAt = &metav1.Time{Time: req.ValidatedAt}
	}

	// Update validation error (set or clear)
	if req.ValidationError != "" {
		updated.Status.ValidationError = req.ValidationError
	} else {
		// Clear validation error if not provided (transitioning from invalid to valid)
		updated.Status.ValidationError = ""
	}

	_, err := p.crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs(req.TemplateConfigNamespace).
		UpdateStatus(ctx, updated, metav1.UpdateOptions{})
	if err != nil {
		p.logger.Warn("failed to update runtime config status",
			"name", updated.Name,
			"error", err,
		)
	}
}

// createOrUpdateMapFile creates or updates a HAProxyMapFile resource.
func (p *Publisher) createOrUpdateMapFile(ctx context.Context, req *PublishRequest, owner *haproxyv1alpha1.HAProxyCfg, mapFile auxiliaryfiles.MapFile) (string, error) {
	name := p.generateMapFileName(filepath.Base(mapFile.Path))
	checksum := calculateChecksum(mapFile.Content) // Checksum of original content

	// Compress if content exceeds threshold
	result := p.compressIfNeeded(mapFile.Content, req.CompressionThreshold, "HAProxyMapFile/"+name)

	// Build spec and labels once (these don't change between retries)
	spec := haproxyv1alpha1.HAProxyMapFileSpec{
		MapName:    filepath.Base(mapFile.Path),
		Path:       mapFile.Path,
		Entries:    result.content,
		Checksum:   checksum,
		Compressed: result.compressed,
	}
	labels := map[string]string{
		"haproxy-haptic.org/runtime-config": owner.Name,
	}
	ownerRefs := []metav1.OwnerReference{
		{
			APIVersion:         "haproxy-haptic.org/v1alpha1",
			Kind:               "HAProxyCfg",
			Name:               owner.Name,
			UID:                owner.UID,
			Controller:         boolPtr(true),
			BlockOwnerDeletion: boolPtr(true),
		},
	}

	var resultName string
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Get existing resource (must be inside retry loop for fresh resourceVersion)
		existing, err := p.crdClient.HaproxyTemplateICV1alpha1().
			HAProxyMapFiles(req.TemplateConfigNamespace).
			Get(ctx, name, metav1.GetOptions{})

		if err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to get existing map file: %w", err)
			}

			// Create new resource
			mapFileResource := &haproxyv1alpha1.HAProxyMapFile{
				ObjectMeta: metav1.ObjectMeta{
					Name:            name,
					Namespace:       req.TemplateConfigNamespace,
					Labels:          labels,
					OwnerReferences: ownerRefs,
				},
				Spec: spec,
			}

			created, createErr := p.crdClient.HaproxyTemplateICV1alpha1().
				HAProxyMapFiles(req.TemplateConfigNamespace).
				Create(ctx, mapFileResource, metav1.CreateOptions{})
			if createErr != nil {
				// If AlreadyExists, another reconciler created it - retry to update
				if apierrors.IsAlreadyExists(createErr) {
					return createErr
				}
				return fmt.Errorf("failed to create map file: %w", createErr)
			}

			resultName = created.Name
			return nil
		}

		// Update existing resource with fresh copy
		existing.Spec = spec
		existing.Labels = labels

		updated, updateErr := p.crdClient.HaproxyTemplateICV1alpha1().
			HAProxyMapFiles(req.TemplateConfigNamespace).
			Update(ctx, existing, metav1.UpdateOptions{})
		if updateErr != nil {
			return fmt.Errorf("failed to update map file: %w", updateErr)
		}

		resultName = updated.Name
		return nil
	})

	if err != nil {
		return "", err
	}

	return resultName, nil
}

// createOrUpdateSSLSecret creates or updates a Secret for SSL certificates.
func (p *Publisher) createOrUpdateSSLSecret(ctx context.Context, req *PublishRequest, owner *haproxyv1alpha1.HAProxyCfg, cert auxiliaryfiles.SSLCertificate) (string, error) {
	name := p.generateSecretName(filepath.Base(cert.Path))

	// Compress if content exceeds threshold
	result := p.compressIfNeeded(cert.Content, req.CompressionThreshold, "Secret/"+name)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: req.TemplateConfigNamespace,
			Labels: map[string]string{
				"haproxy-haptic.org/runtime-config": owner.Name,
				"haproxy-haptic.org/type":           "ssl-certificate",
			},
			Annotations: map[string]string{
				"haproxy-haptic.org/compressed": strconv.FormatBool(result.compressed),
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         "haproxy-haptic.org/v1alpha1",
					Kind:               "HAProxyCfg",
					Name:               owner.Name,
					UID:                owner.UID,
					Controller:         boolPtr(true),
					BlockOwnerDeletion: boolPtr(true),
				},
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"certificate": []byte(result.content),
			"path":        []byte(cert.Path),
		},
	}

	// Try to get existing secret
	existing, err := p.k8sClient.CoreV1().
		Secrets(req.TemplateConfigNamespace).
		Get(ctx, name, metav1.GetOptions{})

	if err != nil {
		if !apierrors.IsNotFound(err) {
			return "", fmt.Errorf("failed to get existing secret: %w", err)
		}

		// Create new secret
		created, err := p.k8sClient.CoreV1().
			Secrets(req.TemplateConfigNamespace).
			Create(ctx, secret, metav1.CreateOptions{})
		if err != nil {
			return "", fmt.Errorf("failed to create secret: %w", err)
		}

		return created.Name, nil
	}

	// Update existing secret
	existing.Data = secret.Data
	existing.Labels = secret.Labels
	existing.Annotations = secret.Annotations

	updated, err := p.k8sClient.CoreV1().
		Secrets(req.TemplateConfigNamespace).
		Update(ctx, existing, metav1.UpdateOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to update secret: %w", err)
	}

	return updated.Name, nil
}

// createOrUpdateGeneralFile creates or updates a HAProxyGeneralFile resource.
func (p *Publisher) createOrUpdateGeneralFile(ctx context.Context, req *PublishRequest, owner *haproxyv1alpha1.HAProxyCfg, generalFile auxiliaryfiles.GeneralFile) (string, error) {
	name := p.generateGeneralFileName(generalFile.Filename)
	checksum := calculateChecksum(generalFile.Content) // Checksum of original content

	// Compress if content exceeds threshold
	result := p.compressIfNeeded(generalFile.Content, req.CompressionThreshold, "HAProxyGeneralFile/"+name)

	// Build spec and labels once (these don't change between retries)
	spec := haproxyv1alpha1.HAProxyGeneralFileSpec{
		FileName:   generalFile.Filename,
		Path:       generalFile.Path,
		Content:    result.content,
		Checksum:   checksum,
		Compressed: result.compressed,
	}
	labels := map[string]string{
		"haproxy-haptic.org/runtime-config": owner.Name,
	}
	ownerRefs := []metav1.OwnerReference{
		{
			APIVersion:         "haproxy-haptic.org/v1alpha1",
			Kind:               "HAProxyCfg",
			Name:               owner.Name,
			UID:                owner.UID,
			Controller:         boolPtr(true),
			BlockOwnerDeletion: boolPtr(true),
		},
	}

	var resultName string
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Get existing resource (must be inside retry loop for fresh resourceVersion)
		existing, err := p.crdClient.HaproxyTemplateICV1alpha1().
			HAProxyGeneralFiles(req.TemplateConfigNamespace).
			Get(ctx, name, metav1.GetOptions{})

		if err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to get existing general file: %w", err)
			}

			// Create new resource
			generalFileResource := &haproxyv1alpha1.HAProxyGeneralFile{
				ObjectMeta: metav1.ObjectMeta{
					Name:            name,
					Namespace:       req.TemplateConfigNamespace,
					Labels:          labels,
					OwnerReferences: ownerRefs,
				},
				Spec: spec,
			}

			created, createErr := p.crdClient.HaproxyTemplateICV1alpha1().
				HAProxyGeneralFiles(req.TemplateConfigNamespace).
				Create(ctx, generalFileResource, metav1.CreateOptions{})
			if createErr != nil {
				// If AlreadyExists, another reconciler created it - retry to update
				if apierrors.IsAlreadyExists(createErr) {
					return createErr
				}
				return fmt.Errorf("failed to create general file: %w", createErr)
			}

			resultName = created.Name
			return nil
		}

		// Update existing resource with fresh copy
		existing.Spec = spec
		existing.Labels = labels

		updated, updateErr := p.crdClient.HaproxyTemplateICV1alpha1().
			HAProxyGeneralFiles(req.TemplateConfigNamespace).
			Update(ctx, existing, metav1.UpdateOptions{})
		if updateErr != nil {
			return fmt.Errorf("failed to update general file: %w", updateErr)
		}

		resultName = updated.Name
		return nil
	})

	if err != nil {
		return "", err
	}

	return resultName, nil
}

// createOrUpdateCRTListFile creates or updates a HAProxyCRTListFile resource.
func (p *Publisher) createOrUpdateCRTListFile(ctx context.Context, req *PublishRequest, owner *haproxyv1alpha1.HAProxyCfg, crtListFile auxiliaryfiles.CRTListFile) (string, error) {
	name := p.generateCRTListFileName(crtListFile.Path)
	checksum := calculateChecksum(crtListFile.Content) // Checksum of original content

	// Compress if content exceeds threshold
	result := p.compressIfNeeded(crtListFile.Content, req.CompressionThreshold, "HAProxyCRTListFile/"+name)

	// Build spec and labels once (these don't change between retries)
	spec := haproxyv1alpha1.HAProxyCRTListFileSpec{
		ListName:   filepath.Base(crtListFile.Path),
		Path:       crtListFile.Path,
		Entries:    result.content,
		Checksum:   checksum,
		Compressed: result.compressed,
	}
	labels := map[string]string{
		"haproxy-haptic.org/runtime-config": owner.Name,
	}
	ownerRefs := []metav1.OwnerReference{
		{
			APIVersion:         "haproxy-haptic.org/v1alpha1",
			Kind:               "HAProxyCfg",
			Name:               owner.Name,
			UID:                owner.UID,
			Controller:         boolPtr(true),
			BlockOwnerDeletion: boolPtr(true),
		},
	}

	var resultName string
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Get existing resource (must be inside retry loop for fresh resourceVersion)
		existing, err := p.crdClient.HaproxyTemplateICV1alpha1().
			HAProxyCRTListFiles(req.TemplateConfigNamespace).
			Get(ctx, name, metav1.GetOptions{})

		if err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to get existing crt-list file: %w", err)
			}

			// Create new resource
			crtListResource := &haproxyv1alpha1.HAProxyCRTListFile{
				ObjectMeta: metav1.ObjectMeta{
					Name:            name,
					Namespace:       req.TemplateConfigNamespace,
					Labels:          labels,
					OwnerReferences: ownerRefs,
				},
				Spec: spec,
			}

			created, createErr := p.crdClient.HaproxyTemplateICV1alpha1().
				HAProxyCRTListFiles(req.TemplateConfigNamespace).
				Create(ctx, crtListResource, metav1.CreateOptions{})
			if createErr != nil {
				// If AlreadyExists, another reconciler created it - retry to update
				if apierrors.IsAlreadyExists(createErr) {
					return createErr
				}
				return fmt.Errorf("failed to create crt-list file: %w", createErr)
			}

			resultName = created.Name
			return nil
		}

		// Update existing resource with fresh copy
		existing.Spec = spec
		existing.Labels = labels

		updated, updateErr := p.crdClient.HaproxyTemplateICV1alpha1().
			HAProxyCRTListFiles(req.TemplateConfigNamespace).
			Update(ctx, existing, metav1.UpdateOptions{})
		if updateErr != nil {
			return fmt.Errorf("failed to update crt-list file: %w", updateErr)
		}

		resultName = updated.Name
		return nil
	})

	if err != nil {
		return "", err
	}

	return resultName, nil
}

// updateRuntimeConfigStatus updates the HAProxyCfg status with child resource references.
func (p *Publisher) updateRuntimeConfigStatus(ctx context.Context, runtimeConfig *haproxyv1alpha1.HAProxyCfg, result *PublishResult) error {
	// Get the latest version
	current, err := p.crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs(runtimeConfig.Namespace).
		Get(ctx, runtimeConfig.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get runtime config: %w", err)
	}

	// Update auxiliary file references
	if current.Status.AuxiliaryFiles == nil {
		current.Status.AuxiliaryFiles = &haproxyv1alpha1.AuxiliaryFileReferences{}
	}

	// Update map file references
	current.Status.AuxiliaryFiles.MapFiles = []haproxyv1alpha1.ResourceReference{}
	for _, name := range result.MapFileNames {
		current.Status.AuxiliaryFiles.MapFiles = append(current.Status.AuxiliaryFiles.MapFiles, haproxyv1alpha1.ResourceReference{
			Kind:      "HAProxyMapFile",
			Name:      name,
			Namespace: runtimeConfig.Namespace,
		})
	}

	// Update SSL certificate references
	current.Status.AuxiliaryFiles.SSLCertificates = []haproxyv1alpha1.ResourceReference{}
	for _, name := range result.SecretNames {
		current.Status.AuxiliaryFiles.SSLCertificates = append(current.Status.AuxiliaryFiles.SSLCertificates, haproxyv1alpha1.ResourceReference{
			Kind:      "Secret",
			Name:      name,
			Namespace: runtimeConfig.Namespace,
		})
	}

	// Update general file references
	current.Status.AuxiliaryFiles.GeneralFiles = []haproxyv1alpha1.ResourceReference{}
	for _, name := range result.GeneralFileNames {
		current.Status.AuxiliaryFiles.GeneralFiles = append(current.Status.AuxiliaryFiles.GeneralFiles, haproxyv1alpha1.ResourceReference{
			Kind:      "HAProxyGeneralFile",
			Name:      name,
			Namespace: runtimeConfig.Namespace,
		})
	}

	// Update crt-list file references
	current.Status.AuxiliaryFiles.CRTListFiles = []haproxyv1alpha1.ResourceReference{}
	for _, name := range result.CRTListFileNames {
		current.Status.AuxiliaryFiles.CRTListFiles = append(current.Status.AuxiliaryFiles.CRTListFiles, haproxyv1alpha1.ResourceReference{
			Kind:      "HAProxyCRTListFile",
			Name:      name,
			Namespace: runtimeConfig.Namespace,
		})
	}

	// Calculate total size
	totalSize := int64(len(runtimeConfig.Spec.Content))
	if current.Status.Metadata != nil {
		current.Status.Metadata.TotalSize = totalSize
	}

	_, err = p.crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs(runtimeConfig.Namespace).
		UpdateStatus(ctx, current, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update status: %w", err)
	}

	return nil
}

// updateMapFileDeploymentStatus updates a map file's deployment status.
// Uses retry-on-conflict to handle concurrent updates.
func (p *Publisher) updateMapFileDeploymentStatus(ctx context.Context, namespace, name string, podStatus *haproxyv1alpha1.PodDeploymentStatus) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		mapFile, err := p.crdClient.HaproxyTemplateICV1alpha1().
			HAProxyMapFiles(namespace).
			Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return nil // Map file might have been deleted
			}
			return fmt.Errorf("failed to get map file: %w", err)
		}

		mapFile.Status.DeployedToPods = addOrUpdatePodStatus(mapFile.Status.DeployedToPods, podStatus)

		_, err = p.crdClient.HaproxyTemplateICV1alpha1().
			HAProxyMapFiles(namespace).
			UpdateStatus(ctx, mapFile, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update map file status: %w", err)
		}

		return nil
	})
}

// updateGeneralFileDeploymentStatus updates a general file's deployment status.
// Uses retry-on-conflict to handle concurrent updates.
func (p *Publisher) updateGeneralFileDeploymentStatus(ctx context.Context, namespace, name string, podStatus *haproxyv1alpha1.PodDeploymentStatus) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		generalFile, err := p.crdClient.HaproxyTemplateICV1alpha1().
			HAProxyGeneralFiles(namespace).
			Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return nil // General file might have been deleted
			}
			return fmt.Errorf("failed to get general file: %w", err)
		}

		generalFile.Status.DeployedToPods = addOrUpdatePodStatus(generalFile.Status.DeployedToPods, podStatus)

		_, err = p.crdClient.HaproxyTemplateICV1alpha1().
			HAProxyGeneralFiles(namespace).
			UpdateStatus(ctx, generalFile, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update general file status: %w", err)
		}

		return nil
	})
}

// updateCRTListFileDeploymentStatus updates a crt-list file's deployment status.
// Uses retry-on-conflict to handle concurrent updates.
func (p *Publisher) updateCRTListFileDeploymentStatus(ctx context.Context, namespace, name string, podStatus *haproxyv1alpha1.PodDeploymentStatus) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		crtListFile, err := p.crdClient.HaproxyTemplateICV1alpha1().
			HAProxyCRTListFiles(namespace).
			Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return nil // CRT list file might have been deleted
			}
			return fmt.Errorf("failed to get crt-list file: %w", err)
		}

		crtListFile.Status.DeployedToPods = addOrUpdatePodStatus(crtListFile.Status.DeployedToPods, podStatus)

		_, err = p.crdClient.HaproxyTemplateICV1alpha1().
			HAProxyCRTListFiles(namespace).
			UpdateStatus(ctx, crtListFile, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update crt-list file status: %w", err)
		}

		return nil
	})
}

// cleanupMapFilePodReference removes a pod from a map file's deployment status.
// Uses retry-on-conflict to handle concurrent updates.
func (p *Publisher) cleanupMapFilePodReference(ctx context.Context, namespace, name string, cleanup PodCleanupRequest) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		mapFile, err := p.crdClient.HaproxyTemplateICV1alpha1().
			HAProxyMapFiles(namespace).
			Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return nil // Map file might have been deleted
			}
			return fmt.Errorf("failed to get map file: %w", err)
		}

		newPods, removed := removePodFromStatus(mapFile.Status.DeployedToPods, cleanup.PodName)
		if !removed {
			return nil // Pod not in this map file
		}

		mapFile.Status.DeployedToPods = newPods

		_, err = p.crdClient.HaproxyTemplateICV1alpha1().
			HAProxyMapFiles(namespace).
			UpdateStatus(ctx, mapFile, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update map file status: %w", err)
		}

		return nil
	})
}

// cleanupGeneralFilePodReference removes a pod from a general file's deployment status.
// Uses retry-on-conflict to handle concurrent updates.
func (p *Publisher) cleanupGeneralFilePodReference(ctx context.Context, namespace, name string, cleanup PodCleanupRequest) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		generalFile, err := p.crdClient.HaproxyTemplateICV1alpha1().
			HAProxyGeneralFiles(namespace).
			Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return nil // General file might have been deleted
			}
			return fmt.Errorf("failed to get general file: %w", err)
		}

		newPods, removed := removePodFromStatus(generalFile.Status.DeployedToPods, cleanup.PodName)
		if !removed {
			return nil // Pod not in this general file
		}

		generalFile.Status.DeployedToPods = newPods

		_, err = p.crdClient.HaproxyTemplateICV1alpha1().
			HAProxyGeneralFiles(namespace).
			UpdateStatus(ctx, generalFile, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update general file status: %w", err)
		}

		return nil
	})
}

// cleanupCRTListFilePodReference removes a pod from a crt-list file's deployment status.
// Uses retry-on-conflict to handle concurrent updates.
func (p *Publisher) cleanupCRTListFilePodReference(ctx context.Context, namespace, name string, cleanup PodCleanupRequest) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		crtListFile, err := p.crdClient.HaproxyTemplateICV1alpha1().
			HAProxyCRTListFiles(namespace).
			Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return nil // CRT list file might have been deleted
			}
			return fmt.Errorf("failed to get crt-list file: %w", err)
		}

		newPods, removed := removePodFromStatus(crtListFile.Status.DeployedToPods, cleanup.PodName)
		if !removed {
			return nil // Pod not in this crt-list file
		}

		crtListFile.Status.DeployedToPods = newPods

		_, err = p.crdClient.HaproxyTemplateICV1alpha1().
			HAProxyCRTListFiles(namespace).
			UpdateStatus(ctx, crtListFile, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update crt-list file status: %w", err)
		}

		return nil
	})
}

// Helper functions

// addOrUpdatePodStatus adds or updates a pod in the deployment status list.
// Returns the updated slice. This helper is used for auxiliary file types
// (MapFile, GeneralFile, CRTListFile) and includes safeguards to ensure
// deployedAt is always set (required field in CRD schema).
func addOrUpdatePodStatus(pods []haproxyv1alpha1.PodDeploymentStatus, podStatus *haproxyv1alpha1.PodDeploymentStatus) []haproxyv1alpha1.PodDeploymentStatus {
	for i := range pods {
		if pods[i].PodName == podStatus.PodName {
			// Preserve existing deployedAt if new one is zero (safeguard)
			// This handles drift checks where no operations were performed
			if podStatus.DeployedAt.IsZero() && !pods[i].DeployedAt.IsZero() {
				podStatus.DeployedAt = pods[i].DeployedAt
			}
			// Use lastCheckedAt as fallback if deployedAt still zero
			if podStatus.DeployedAt.IsZero() && podStatus.LastCheckedAt != nil {
				podStatus.DeployedAt = *podStatus.LastCheckedAt
			}
			pods[i] = *podStatus
			return pods
		}
	}
	// For new pods, ensure deployedAt is set (required field)
	if podStatus.DeployedAt.IsZero() && podStatus.LastCheckedAt != nil {
		podStatus.DeployedAt = *podStatus.LastCheckedAt
	}
	return append(pods, *podStatus)
}

// removePodFromStatus removes a pod from the deployment status list.
// Returns the updated slice and whether the pod was found and removed.
func removePodFromStatus(pods []haproxyv1alpha1.PodDeploymentStatus, podName string) ([]haproxyv1alpha1.PodDeploymentStatus, bool) {
	newPods := make([]haproxyv1alpha1.PodDeploymentStatus, 0, len(pods))
	removed := false
	for i := range pods {
		if pods[i].PodName == podName {
			removed = true
			continue
		}
		newPods = append(newPods, pods[i])
	}
	return newPods, removed
}

// updateOrAppendPodStatus updates an existing pod status or appends a new one.
// Returns the updated slice.
func updateOrAppendPodStatus(
	pods []haproxyv1alpha1.PodDeploymentStatus,
	podStatus *haproxyv1alpha1.PodDeploymentStatus,
	update *DeploymentStatusUpdate,
) []haproxyv1alpha1.PodDeploymentStatus {
	// Try to find and update existing pod
	for i := range pods {
		if pods[i].PodName != update.PodName {
			continue
		}

		// Preserve deployedAt if no operations were performed
		if podStatus.DeployedAt.IsZero() {
			podStatus.DeployedAt = pods[i].DeployedAt
		}

		// Preserve and update consecutive error count
		if update.Error != "" {
			podStatus.ConsecutiveErrors = pods[i].ConsecutiveErrors + 1
		} else {
			podStatus.ConsecutiveErrors = 0
		}

		pods[i] = *podStatus
		return pods
	}

	// Pod not found - append new entry
	// Ensure deployedAt is set for first-time pod
	if podStatus.DeployedAt.IsZero() && podStatus.LastCheckedAt != nil {
		// Safeguard: use LastCheckedAt if deployedAt not set
		// (shouldn't happen in practice - first sync always has operations)
		podStatus.DeployedAt = *podStatus.LastCheckedAt
	}

	return append(pods, *podStatus)
}

// buildPodStatus constructs a PodDeploymentStatus from a DeploymentStatusUpdate.
func buildPodStatus(update *DeploymentStatusUpdate) haproxyv1alpha1.PodDeploymentStatus {
	podStatus := haproxyv1alpha1.PodDeploymentStatus{
		PodName:  update.PodName,
		Checksum: update.Checksum,
	}

	// Set LastCheckedAt - always set on every successful sync
	if update.LastCheckedAt != nil {
		checkedTime := metav1.NewTime(*update.LastCheckedAt)
		podStatus.LastCheckedAt = &checkedTime
	}

	// Set DeployedAt only when operations > 0 (actual changes made)
	// If no operations, we'll preserve the existing DeployedAt in the caller
	if !update.DeployedAt.IsZero() {
		podStatus.DeployedAt = metav1.NewTime(update.DeployedAt)
	}

	// Set reload information if provided
	if update.LastReloadAt != nil {
		reloadTime := metav1.NewTime(*update.LastReloadAt)
		podStatus.LastReloadAt = &reloadTime
		podStatus.LastReloadID = update.LastReloadID
	}

	// Set performance metrics
	if update.SyncDuration != nil {
		duration := metav1.Duration{Duration: *update.SyncDuration}
		podStatus.SyncDuration = &duration
	}
	podStatus.VersionConflictRetries = update.VersionConflictRetries
	podStatus.FallbackUsed = update.FallbackUsed

	// Set operation summary
	if update.OperationSummary != nil {
		podStatus.LastOperationSummary = &haproxyv1alpha1.OperationSummary{
			TotalAPIOperations: update.OperationSummary.TotalAPIOperations,
			BackendsAdded:      update.OperationSummary.BackendsAdded,
			BackendsRemoved:    update.OperationSummary.BackendsRemoved,
			BackendsModified:   update.OperationSummary.BackendsModified,
			ServersAdded:       update.OperationSummary.ServersAdded,
			ServersRemoved:     update.OperationSummary.ServersRemoved,
			ServersModified:    update.OperationSummary.ServersModified,
			FrontendsAdded:     update.OperationSummary.FrontendsAdded,
			FrontendsRemoved:   update.OperationSummary.FrontendsRemoved,
			FrontendsModified:  update.OperationSummary.FrontendsModified,
		}
	}

	// Set error tracking
	if update.Error != "" {
		podStatus.LastError = update.Error
		now := metav1.NewTime(update.DeployedAt)
		podStatus.LastErrorAt = &now
	}

	return podStatus
}

// GenerateRuntimeConfigName generates the HAProxyCfg resource name from a template config name.
// This is the single source of truth for the naming convention used by both
// the ConfigPublisher and DeploymentScheduler.
func GenerateRuntimeConfigName(templateConfigName string) string {
	return templateConfigName + "-haproxycfg"
}

func (p *Publisher) generateRuntimeConfigName(templateConfigName string) string {
	return GenerateRuntimeConfigName(templateConfigName)
}

func (p *Publisher) generateMapFileName(mapName string) string {
	// Sanitize map name to create valid Kubernetes resource name
	// Remove file extension and special characters
	name := mapName
	if ext := filepath.Ext(name); ext != "" {
		name = name[:len(name)-len(ext)]
	}
	return "haproxy-map-" + name
}

func (p *Publisher) generateSecretName(certPath string) string {
	// Sanitize cert path to create valid Kubernetes resource name
	name := filepath.Base(certPath)
	if ext := filepath.Ext(name); ext != "" {
		name = name[:len(name)-len(ext)]
	}
	// Replace underscores with hyphens to comply with DNS-1123 subdomain naming
	// (Kubernetes secret names can't contain underscores)
	name = strings.ReplaceAll(name, "_", "-")
	return "haproxy-cert-" + name
}

func (p *Publisher) generateGeneralFileName(fileName string) string {
	// Sanitize file name to create valid Kubernetes resource name
	name := filepath.Base(fileName)
	if ext := filepath.Ext(name); ext != "" {
		name = name[:len(name)-len(ext)]
	}
	name = strings.ReplaceAll(name, "_", "-")
	name = strings.ReplaceAll(name, ".", "-")
	return "haproxy-file-" + name
}

func (p *Publisher) generateCRTListFileName(listPath string) string {
	// Sanitize list path to create valid Kubernetes resource name
	name := filepath.Base(listPath)
	if ext := filepath.Ext(name); ext != "" {
		name = name[:len(name)-len(ext)]
	}
	name = strings.ReplaceAll(name, "_", "-")
	return "haproxy-crtlist-" + name
}

func calculateChecksum(content string) string {
	hash := sha256.Sum256([]byte(content))
	return fmt.Sprintf("sha256:%x", hash)
}

func boolPtr(b bool) *bool {
	return &b
}

// compressResult holds the result of a compression attempt.
type compressResult struct {
	content    string
	compressed bool
}

// compressIfNeeded compresses content if it exceeds the threshold and compression is beneficial.
// Returns the (possibly compressed) content and whether compression was applied.
//
// Threshold semantics:
//   - threshold <= 0: compression disabled (never compress)
//   - threshold > 0: compress if content exceeds threshold
func (p *Publisher) compressIfNeeded(content string, threshold int64, resourceType string) compressResult {
	if threshold <= 0 || int64(len(content)) <= threshold {
		return compressResult{content: content, compressed: false}
	}

	compressedContent, err := compression.Compress(content)
	if err != nil {
		p.logger.Warn("compression failed, storing uncompressed",
			"resource_type", resourceType,
			"error", err,
			"size_bytes", len(content),
		)
		return compressResult{content: content, compressed: false}
	}

	// Only use compression if it actually reduces size
	if len(compressedContent) >= len(content) {
		p.logger.Debug("compression skipped, no size reduction",
			"resource_type", resourceType,
			"original_bytes", len(content),
			"compressed_bytes", len(compressedContent),
		)
		return compressResult{content: content, compressed: false}
	}

	ratio := float64(len(compressedContent)) * 100 / float64(len(content))
	p.logger.Debug("compressed content for CRD storage",
		"resource_type", resourceType,
		"original_bytes", len(content),
		"compressed_bytes", len(compressedContent),
		"ratio", fmt.Sprintf("%.1f%%", ratio),
	)

	return compressResult{content: compressedContent, compressed: true}
}
