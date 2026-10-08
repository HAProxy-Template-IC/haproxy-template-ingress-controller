// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

package publication

import (
	"slices"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	api "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
)

// RetainedMatches waits for checkpoint publication and cascading certificate cleanup.
func RetainedMatches(config *api.HAProxyCfg, configs []api.HAProxyCfg, secrets []corev1.Secret) bool {
	owner := metav1.GetControllerOf(config)
	if owner == nil || owner.UID == "" || len(config.Status.RetainedConfigs) == 0 {
		return false
	}
	byUID := make(map[types.UID]*api.HAProxyCfg, len(configs))
	for i := range configs {
		byUID[configs[i].UID] = &configs[i]
	}
	current := byUID[config.UID]
	if current == nil || current.ResourceVersion != config.ResourceVersion || !retainedReferencesMatch(config, byUID, secrets, owner) {
		return false
	}
	return retainedCleanupComplete(config, byUID, secrets, owner)
}

func retainedReferencesMatch(config *api.HAProxyCfg, configs map[types.UID]*api.HAProxyCfg, secrets []corev1.Secret, owner *metav1.OwnerReference) bool {
	covered := make(map[string]bool)
	seen := make(map[types.UID]bool)
	for _, ref := range config.Status.RetainedConfigs {
		uid := types.UID(ref.UID)
		retained := configs[uid]
		if uid == "" || seen[uid] || retained == nil || retained.Namespace != config.Namespace || !retainedReferenceMatches(retained, ref, owner) {
			return false
		}
		seen[uid] = true
		if !retainedCertificatesMatch(retained, secrets) {
			return false
		}
		if retained.Spec.Checksum == config.Spec.Checksum {
			for i := range retained.Status.DeployedToPods {
				pod := &retained.Status.DeployedToPods[i]
				if retainedAcknowledges(pod, config.Spec.Checksum) {
					covered[pod.AppliedPlanID] = true
				}
			}
		}
	}
	return len(config.Status.DeployedToPods) > 0 && !slices.ContainsFunc(config.Status.DeployedToPods, func(pod api.PodDeploymentStatus) bool {
		return !covered[pod.AppliedPlanID]
	})
}

func retainedCertificatesMatch(config *api.HAProxyCfg, secrets []corev1.Secret) bool {
	setID := config.Annotations[annotationPrefix+"auxiliary-set-id"]
	return setID != "" && config.Status.AuxiliaryFiles != nil && config.Status.AuxiliaryFiles.SetID == setID && certificatesMatch(config, secrets, setID)
}

func retainedAcknowledges(pod *api.PodDeploymentStatus, checksum string) bool {
	return pod.Checksum == checksum && pod.AppliedPlanID != "" && pod.RunningPlanID != "" && pod.LastError == "" && pod.ConsecutiveErrors == 0
}

func retainedReferenceMatches(retained *api.HAProxyCfg, ref api.RetainedConfigReference, owner *metav1.OwnerReference) bool {
	parent := metav1.GetControllerOf(retained)
	return retained.Name == ref.Name && retained.Spec.Checksum == ref.Checksum && retained.Spec.RetainedPlan != "" &&
		retained.DeletionTimestamp == nil && parent != nil && parent.UID == owner.UID && parent.Name == owner.Name && parent.Kind == owner.Kind && parent.APIVersion == owner.APIVersion &&
		retained.Labels[annotationPrefix+"retained-for"] == string(owner.UID)
}

func retainedCleanupComplete(config *api.HAProxyCfg, configs map[types.UID]*api.HAProxyCfg, secrets []corev1.Secret, owner *metav1.OwnerReference) bool {
	for uid, candidate := range configs {
		parent := metav1.GetControllerOf(candidate)
		if candidate.Spec.RetainedPlan == "" || parent == nil || parent.UID != owner.UID {
			continue
		}
		if !slices.ContainsFunc(config.Status.RetainedConfigs, func(ref api.RetainedConfigReference) bool { return ref.UID == string(uid) }) {
			return false
		}
	}
	for i := range secrets {
		parent := metav1.GetControllerOf(&secrets[i])
		if parent != nil && parent.Kind == "HAProxyCfg" && parent.APIVersion == api.SchemeGroupVersion.String() {
			if live := configs[parent.UID]; live == nil || live.Name != parent.Name || live.DeletionTimestamp != nil {
				return false
			}
		}
	}
	return true
}
