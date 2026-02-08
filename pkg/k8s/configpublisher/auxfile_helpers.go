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

// auxFileHandle provides read-write access to an auxiliary file's pod deployment status.
// This abstracts over the different CRD types (MapFile, GeneralFile, CRTListFile) so that
// shared logic for status mutations can be written once.
type auxFileHandle struct {
	pods     []haproxyv1alpha1.PodDeploymentStatus
	checksum string
	// applyStatus writes the updated pod list back to the Kubernetes API.
	applyStatus func(pods []haproxyv1alpha1.PodDeploymentStatus) error
}

// mapFileHandle fetches a MapFile and returns an auxFileHandle for it.
func (p *Publisher) mapFileHandle(ctx context.Context, namespace, name string) (*auxFileHandle, error) {
	mapFile, err := p.crdClient.HaproxyTemplateICV1alpha1().
		HAProxyMapFiles(namespace).
		Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return &auxFileHandle{
		pods:     mapFile.Status.DeployedToPods,
		checksum: mapFile.Spec.Checksum,
		applyStatus: func(pods []haproxyv1alpha1.PodDeploymentStatus) error {
			mapFile.Status.DeployedToPods = pods
			_, err := p.crdClient.HaproxyTemplateICV1alpha1().
				HAProxyMapFiles(namespace).
				UpdateStatus(ctx, mapFile, metav1.UpdateOptions{})
			if err != nil {
				return fmt.Errorf("failed to update map file status: %w", err)
			}
			return nil
		},
	}, nil
}

// generalFileHandle fetches a GeneralFile and returns an auxFileHandle for it.
func (p *Publisher) generalFileHandle(ctx context.Context, namespace, name string) (*auxFileHandle, error) {
	generalFile, err := p.crdClient.HaproxyTemplateICV1alpha1().
		HAProxyGeneralFiles(namespace).
		Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return &auxFileHandle{
		pods:     generalFile.Status.DeployedToPods,
		checksum: generalFile.Spec.Checksum,
		applyStatus: func(pods []haproxyv1alpha1.PodDeploymentStatus) error {
			generalFile.Status.DeployedToPods = pods
			_, err := p.crdClient.HaproxyTemplateICV1alpha1().
				HAProxyGeneralFiles(namespace).
				UpdateStatus(ctx, generalFile, metav1.UpdateOptions{})
			if err != nil {
				return fmt.Errorf("failed to update general file status: %w", err)
			}
			return nil
		},
	}, nil
}

// crtListFileHandle fetches a CRTListFile and returns an auxFileHandle for it.
func (p *Publisher) crtListFileHandle(ctx context.Context, namespace, name string) (*auxFileHandle, error) {
	crtListFile, err := p.crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCRTListFiles(namespace).
		Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return &auxFileHandle{
		pods:     crtListFile.Status.DeployedToPods,
		checksum: crtListFile.Spec.Checksum,
		applyStatus: func(pods []haproxyv1alpha1.PodDeploymentStatus) error {
			crtListFile.Status.DeployedToPods = pods
			_, err := p.crdClient.HaproxyTemplateICV1alpha1().
				HAProxyCRTListFiles(namespace).
				UpdateStatus(ctx, crtListFile, metav1.UpdateOptions{})
			if err != nil {
				return fmt.Errorf("failed to update crt-list file status: %w", err)
			}
			return nil
		},
	}, nil
}

// getHandleFunc fetches a fresh auxFileHandle inside a retry loop.
type getHandleFunc func() (*auxFileHandle, error)

// mutateAuxFilePodStatus fetches an auxiliary file via getHandle, applies mutatePods to its
// pod list, and writes back the result. Uses retry-on-conflict for concurrent updates.
// If the resource is not found, it returns nil (not an error).
func mutateAuxFilePodStatus(
	getHandle getHandleFunc,
	mutatePods func([]haproxyv1alpha1.PodDeploymentStatus) ([]haproxyv1alpha1.PodDeploymentStatus, bool),
) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		h, err := getHandle()
		if err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}

		newPods, changed := mutatePods(h.pods)
		if !changed {
			return nil
		}

		return h.applyStatus(newPods)
	})
}

// filterRunningPods returns a mutation function that keeps only pods present in runningSet.
// The second return value indicates whether any pods were removed.
// If pods were removed, their names are logged via the provided logRemoved callback.
func filterRunningPods(
	runningSet map[string]struct{},
	logRemoved func(removed []string),
) func([]haproxyv1alpha1.PodDeploymentStatus) ([]haproxyv1alpha1.PodDeploymentStatus, bool) {
	return func(pods []haproxyv1alpha1.PodDeploymentStatus) ([]haproxyv1alpha1.PodDeploymentStatus, bool) {
		newPods := make([]haproxyv1alpha1.PodDeploymentStatus, 0, len(pods))
		var removed []string
		for i := range pods {
			if _, exists := runningSet[pods[i].PodName]; exists {
				newPods = append(newPods, pods[i])
			} else {
				removed = append(removed, pods[i].PodName)
			}
		}
		if len(removed) == 0 {
			return pods, false
		}
		if logRemoved != nil {
			logRemoved(removed)
		}
		return newPods, true
	}
}

// removePodMutation returns a mutation function that removes a specific pod by name.
func removePodMutation(podName string) func([]haproxyv1alpha1.PodDeploymentStatus) ([]haproxyv1alpha1.PodDeploymentStatus, bool) {
	return func(pods []haproxyv1alpha1.PodDeploymentStatus) ([]haproxyv1alpha1.PodDeploymentStatus, bool) {
		newPods, removed := removePodFromStatus(pods, podName)
		return newPods, removed
	}
}

// cachedAuxFileStatus holds the cached status data needed for checking if an update is needed.
type cachedAuxFileStatus struct {
	pods     []haproxyv1alpha1.PodDeploymentStatus
	checksum string
}

// tryCachedReadFunc attempts to read the auxiliary file from an informer cache.
// Returns nil if no cache is available or the read fails.
type tryCachedReadFunc func() *cachedAuxFileStatus

// updateAuxFileDeploymentStatus updates an auxiliary file's deployment status.
// It first tries a cached read (if available) to skip unnecessary API calls,
// then falls back to retry-on-conflict with fresh API reads.
func updateAuxFileDeploymentStatus(
	podStatus *haproxyv1alpha1.PodDeploymentStatus,
	tryCachedRead tryCachedReadFunc,
	getHandle func() (*auxFileHandle, error),
	fileType string,
) error {
	// Try cached read first to check if update is needed
	if cached := tryCachedRead(); cached != nil {
		existingStatus := findPodStatus(cached.pods, podStatus.PodName)
		auxPodStatus := buildAuxiliaryFilePodStatus(
			podStatus.PodName,
			cached.checksum,
			existingStatus,
			podStatus.DeployedAt.Time,
		)
		newStatuses := addOrUpdatePodStatus(copyPodStatuses(cached.pods), &auxPodStatus)

		if podStatusesEqual(cached.pods, newStatuses) {
			return nil
		}
	}

	// Need to update - do retry-on-conflict with fresh API reads
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		h, err := getHandle()
		if err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("failed to get %s: %w", fileType, err)
		}

		originalStatus := copyPodStatuses(h.pods)

		existingStatus := findPodStatus(h.pods, podStatus.PodName)
		auxPodStatus := buildAuxiliaryFilePodStatus(
			podStatus.PodName,
			h.checksum,
			existingStatus,
			podStatus.DeployedAt.Time,
		)

		newPods := addOrUpdatePodStatus(h.pods, &auxPodStatus)
		if podStatusesEqual(originalStatus, newPods) {
			return nil
		}

		return h.applyStatus(newPods)
	})
}
