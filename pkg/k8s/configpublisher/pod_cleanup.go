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
	"errors"
	"fmt"

	"golang.org/x/sync/errgroup"

	haproxyv1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
)

// CleanupPodReferences removes a terminated pod from runtime and referenced auxiliary statuses.
func (p *Publisher) CleanupPodReferences(ctx context.Context, cleanup *PodCleanupRequest) error {
	p.logger.Debug("Cleaning up pod references",
		"pod", cleanup.PodName,
		"namespace", cleanup.Namespace,
	)

	// Drop the departing pod's remembered aux-file stamps so a recycled pod
	// name re-stamps from scratch instead of being elided against stale state.
	p.auxStamps.forgetPod(cleanup.PodName)

	// List HAProxyCfgs in the specified namespace only (namespace-scoped).
	// The controller manages CRDs in its own namespace, not cluster-wide.
	runtimeConfigs, err := p.crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs(cleanup.Namespace).
		List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("listing runtime configs: %w", err)
	}

	var failures []error
	for i := range runtimeConfigs.Items {
		if err := p.cleanupRuntimeConfigPodReference(ctx, &runtimeConfigs.Items[i], cleanup); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// ReconcileDeployedToPods removes stale pod identities from runtime and auxiliary statuses.
func (p *Publisher) ReconcileDeployedToPods(ctx context.Context, namespace string, runningPods []PodIdentity) error {
	runningSet := make(map[string]PodIdentity, len(runningPods))
	for _, pod := range runningPods {
		runningSet[pod.PodName] = pod
	}

	// Evict aux-file stamps for pods that left the fleet, so a pod removed here
	// (missed termination event, or a transient discovery blip) re-stamps if it
	// returns. runningPods is the complete current fleet, so a podName absent
	// from it is genuinely gone.
	p.auxStamps.retainRunningPods(runningSet)

	// List HAProxyCfgs in the specified namespace only (namespace-scoped).
	// The controller manages CRDs in its own namespace, not cluster-wide.
	runtimeConfigs, err := p.crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs(namespace).
		List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("listing HAProxyCfgs: %w", err)
	}

	var failures []error
	for i := range runtimeConfigs.Items {
		listedCfg := &runtimeConfigs.Items[i]
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			return p.reconcileSingleRuntimeConfigStatus(ctx, listedCfg, runningSet)
		})
		if err != nil {
			failures = append(failures, fmt.Errorf("reconciling HAProxyCfg %s: %w", listedCfg.Name, err))
		}
	}
	return errors.Join(failures...)
}

// reconcileSingleRuntimeConfigStatus reconciles the DeployedToPods status for a single HAProxyCfg.
// It fetches a fresh copy, filters out stale pods, and updates the status.
func (p *Publisher) reconcileSingleRuntimeConfigStatus(
	ctx context.Context,
	listedCfg *haproxyv1alpha1.HAProxyCfg,
	runningSet map[string]PodIdentity,
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
	if cfg.Spec.RetainedPlan != "" {
		return nil // Historical worker receipts survive pod retirement (ADR-0033).
	}

	// A previous partial cleanup may have left stale children behind a current parent.
	if err := p.reconcileAuxiliaryFilePods(ctx, cfg.Status.AuxiliaryFiles, runningSet); err != nil {
		return err
	}
	stalePods, newDeployedToPods := p.filterStalePods(cfg.Status.DeployedToPods, runningSet)
	if len(stalePods) == 0 {
		return nil
	}

	p.logger.Debug("Removing stale pod entries from HAProxyCfg status",
		"name", cfg.Name,
		"namespace", cfg.Namespace,
		"stale_pods", stalePods,
	)

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
	runningSet map[string]PodIdentity,
) (stalePods []string, runningPods []haproxyv1alpha1.PodDeploymentStatus) {
	runningPods = make([]haproxyv1alpha1.PodDeploymentStatus, 0, len(deployedToPods))
	for i := range deployedToPods {
		pod := &deployedToPods[i]
		identity, exists := runningSet[pod.PodName]
		if !exists || !podStatusMatchesIdentity(pod, &identity) {
			stalePods = append(stalePods, pod.PodName)
		} else {
			runningPods = append(runningPods, *pod)
		}
	}
	return stalePods, runningPods
}

func podStatusMatchesIdentity(status *haproxyv1alpha1.PodDeploymentStatus, identity *PodIdentity) bool {
	uidMatches := identity.PodUID == "" || status.PodUID == identity.PodUID
	return uidMatches && status.PodRuntimeID == identity.PodRuntimeID
}

// cleanupRuntimeConfigPodReference removes pod reference from a single HAProxyCfg.
// Uses retry-on-conflict to handle concurrent updates.
func (p *Publisher) cleanupRuntimeConfigPodReference(ctx context.Context, runtimeConfig *haproxyv1alpha1.HAProxyCfg, cleanup *PodCleanupRequest) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current, err := p.crdClient.HaproxyTemplateICV1alpha1().
			HAProxyCfgs(runtimeConfig.Namespace).
			Get(ctx, runtimeConfig.Name, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("getting runtime config: %w", err)
		}
		if current.Spec.RetainedPlan != "" {
			return nil // Historical worker receipts survive pod retirement (ADR-0033).
		}
		if err := p.cleanupAuxiliaryFilePodReferences(ctx, current.Status.AuxiliaryFiles, cleanup); err != nil {
			return err
		}
		newDeployedToPods, removed := removePodAuthorityFromStatus(current.Status.DeployedToPods, cleanup.PodName, cleanup.PodUID)
		if !removed {
			return nil
		}
		current.Status.DeployedToPods = newDeployedToPods
		_, err = p.crdClient.HaproxyTemplateICV1alpha1().
			HAProxyCfgs(current.Namespace).
			UpdateStatus(ctx, current, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("updating runtime config status: %w", err)
		}
		return nil
	})
}

// auxFileGroup supplies typed access to one kind of auxiliary file.
type auxFileGroup struct {
	refs          []haproxyv1alpha1.ResourceReference
	label         string
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
		{auxFiles.MapFiles, "map file", p.mapFileHandle, p.cachedMapFileStatus},
		{auxFiles.GeneralFiles, "general file", p.generalFileHandle, p.cachedGeneralFileStatus},
		{auxFiles.CRTListFiles, "crt-list file", p.crtListFileHandle, p.cachedCRTListFileStatus},
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

func (p *Publisher) cleanupAuxiliaryFilePodReferences(ctx context.Context, auxFiles *haproxyv1alpha1.AuxiliaryFileReferences, cleanup *PodCleanupRequest) error {
	return p.mutateAuxiliaryFilePods(ctx, auxFiles, removePodMutation(cleanup.PodName, cleanup.PodUID))
}

func (p *Publisher) reconcileAuxiliaryFilePods(ctx context.Context, auxFiles *haproxyv1alpha1.AuxiliaryFileReferences, runningSet map[string]PodIdentity) error {
	return p.mutateAuxiliaryFilePods(ctx, auxFiles, filterRunningPods(runningSet, nil))
}

const auxiliaryCleanupConcurrency = 8

type podStatusMutation func([]haproxyv1alpha1.PodDeploymentStatus) ([]haproxyv1alpha1.PodDeploymentStatus, bool)

func (p *Publisher) mutateAuxiliaryFilePods(ctx context.Context, auxFiles *haproxyv1alpha1.AuxiliaryFileReferences, mutate podStatusMutation) error {
	var work errgroup.Group
	work.SetLimit(auxiliaryCleanupConcurrency)
	for _, group := range p.auxFileGroupsFor(auxFiles) {
		for _, ref := range group.refs {
			if !group.needsPodMutation(ref, mutate) {
				continue
			}
			work.Go(func() error { return group.mutatePods(ctx, ref, mutate) })
		}
	}
	return work.Wait()
}

func (g *auxFileGroup) needsPodMutation(ref haproxyv1alpha1.ResourceReference, mutate podStatusMutation) bool {
	cached := g.tryCachedRead(ref.Namespace, ref.Name)
	if cached == nil {
		return true
	}
	_, changed := mutate(cached.pods)
	return changed
}

func (g *auxFileGroup) mutatePods(ctx context.Context, ref haproxyv1alpha1.ResourceReference, mutate podStatusMutation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := mutateAuxFilePodStatus(func() (*auxFileHandle, error) { return g.handle(ctx, ref.Namespace, ref.Name) }, mutate)
	if err != nil {
		return fmt.Errorf("cleaning %s %s/%s pod references: %w", g.label, ref.Namespace, ref.Name, err)
	}
	return nil
}
