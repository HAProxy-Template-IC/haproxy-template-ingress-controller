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
//
// When listers are available, first checks the cache to determine if an update
// is needed, avoiding unnecessary API GETs in most cases.
func (p *Publisher) UpdateDeploymentStatus(ctx context.Context, update *DeploymentStatusUpdate) error {
	p.logger.Debug("updating deployment status",
		"runtimeConfig", update.RuntimeConfigName,
		"pod", update.PodName,
	)

	// Track auxiliary files for later updates (populated inside retry loop or from cache)
	var auxFiles *haproxyv1alpha1.AuxiliaryFileReferences

	// Try cached read first (if listers available) to check if update is needed
	if p.listers != nil && p.listers.HAProxyCfgs != nil {
		cached, err := p.listers.HAProxyCfgs.HAProxyCfgs(update.RuntimeConfigNamespace).Get(update.RuntimeConfigName)
		if err == nil {
			// Check if update is needed using cached data
			podStatus := buildPodStatus(update)
			newStatuses := updateOrAppendPodStatus(
				copyPodStatuses(cached.Status.DeployedToPods),
				&podStatus,
				update,
			)

			// Skip HAProxyCfg update if no actual change needed
			if podStatusesEqual(cached.Status.DeployedToPods, newStatuses) {
				// Still need to update auxiliary files, so get the reference from cache
				auxFiles = cached.Status.AuxiliaryFiles
				auxPodStatus := buildPodStatus(update)
				p.updateAuxiliaryFileDeploymentStatus(ctx, auxFiles, &auxPodStatus)
				return nil
			}
			// Change is needed - still cache the auxFiles reference for use after update
			auxFiles = cached.Status.AuxiliaryFiles
		}
		// On cache miss or error, fall through to API read
	}

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
// Skips the UpdateStatus API call if the status would not actually change.
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

	// Save original status for comparison (deep copy to avoid aliasing issues)
	originalStatus := copyPodStatuses(runtimeConfig.Status.DeployedToPods)

	// Build pod status from update
	podStatus := buildPodStatus(update)

	// Update or append pod status
	runtimeConfig.Status.DeployedToPods = updateOrAppendPodStatus(
		runtimeConfig.Status.DeployedToPods,
		&podStatus,
		update,
	)

	// Skip UpdateStatus if the status didn't actually change
	if podStatusesEqual(originalStatus, runtimeConfig.Status.DeployedToPods) {
		return nil
	}

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
			p.logger.Debug("deployment status update conflict (will retry on next reconciliation)",
				"type", "map_file",
				"name", mapFileRef.Name,
				"error", err,
			)
		}
	}

	for _, generalFileRef := range auxFiles.GeneralFiles {
		if err := p.updateGeneralFileDeploymentStatus(ctx, generalFileRef.Namespace, generalFileRef.Name, podStatus); err != nil {
			p.logger.Debug("deployment status update conflict (will retry on next reconciliation)",
				"type", "general_file",
				"name", generalFileRef.Name,
				"error", err,
			)
		}
	}

	for _, crtListFileRef := range auxFiles.CRTListFiles {
		if err := p.updateCRTListFileDeploymentStatus(ctx, crtListFileRef.Namespace, crtListFileRef.Name, podStatus); err != nil {
			p.logger.Debug("deployment status update conflict (will retry on next reconciliation)",
				"type", "crt_list_file",
				"name", crtListFileRef.Name,
				"error", err,
			)
		}
	}
}

// updateMapFileDeploymentStatus updates a map file's deployment status.
// When listers are available, first checks the cache to skip unnecessary API calls.
func (p *Publisher) updateMapFileDeploymentStatus(ctx context.Context, namespace, name string, podStatus *haproxyv1alpha1.PodDeploymentStatus) error {
	return updateAuxFileDeploymentStatus(podStatus,
		func() *cachedAuxFileStatus {
			if p.listers == nil || p.listers.MapFiles == nil {
				return nil
			}
			cached, err := p.listers.MapFiles.HAProxyMapFiles(namespace).Get(name)
			if err != nil {
				return nil
			}
			return &cachedAuxFileStatus{pods: cached.Status.DeployedToPods, checksum: cached.Spec.Checksum}
		},
		func() (*auxFileHandle, error) { return p.mapFileHandle(ctx, namespace, name) },
		"map file",
	)
}

// updateGeneralFileDeploymentStatus updates a general file's deployment status.
// When listers are available, first checks the cache to skip unnecessary API calls.
func (p *Publisher) updateGeneralFileDeploymentStatus(ctx context.Context, namespace, name string, podStatus *haproxyv1alpha1.PodDeploymentStatus) error {
	return updateAuxFileDeploymentStatus(podStatus,
		func() *cachedAuxFileStatus {
			if p.listers == nil || p.listers.GeneralFiles == nil {
				return nil
			}
			cached, err := p.listers.GeneralFiles.HAProxyGeneralFiles(namespace).Get(name)
			if err != nil {
				return nil
			}
			return &cachedAuxFileStatus{pods: cached.Status.DeployedToPods, checksum: cached.Spec.Checksum}
		},
		func() (*auxFileHandle, error) { return p.generalFileHandle(ctx, namespace, name) },
		"general file",
	)
}

// updateCRTListFileDeploymentStatus updates a crt-list file's deployment status.
// When listers are available, first checks the cache to skip unnecessary API calls.
func (p *Publisher) updateCRTListFileDeploymentStatus(ctx context.Context, namespace, name string, podStatus *haproxyv1alpha1.PodDeploymentStatus) error {
	return updateAuxFileDeploymentStatus(podStatus,
		func() *cachedAuxFileStatus {
			if p.listers == nil || p.listers.CRTListFiles == nil {
				return nil
			}
			cached, err := p.listers.CRTListFiles.HAProxyCRTListFiles(namespace).Get(name)
			if err != nil {
				return nil
			}
			return &cachedAuxFileStatus{pods: cached.Status.DeployedToPods, checksum: cached.Spec.Checksum}
		},
		func() (*auxFileHandle, error) { return p.crtListFileHandle(ctx, namespace, name) },
		"crt-list file",
	)
}
