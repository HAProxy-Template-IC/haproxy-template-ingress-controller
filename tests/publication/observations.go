// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

// Package publication checks observed output independently of publisher helpers.
package publication

import (
	"crypto/sha256"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	agentapi "gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
)

const annotationPrefix = "haproxy-haptic.org/"

func Matches(config *api.HAProxyCfg, agents map[string]agentapi.State, secrets []corev1.Secret, replicas int) bool {
	setID := config.Annotations[annotationPrefix+"auxiliary-set-id"]
	refs := config.Status.AuxiliaryFiles
	if config.Spec.Checksum == "" || setID == "" || refs == nil || refs.SetID != setID || replicas <= 0 || len(agents) != replicas {
		return false
	}
	if !fleetMatches(config, agents) {
		return false
	}
	return certificatesMatch(config, secrets, setID)
}

func fleetMatches(config *api.HAProxyCfg, agents map[string]agentapi.State) bool {
	deployments := make(map[string]*api.PodDeploymentStatus, len(config.Status.DeployedToPods))
	for i := range config.Status.DeployedToPods {
		deployed := &config.Status.DeployedToPods[i]
		deployments[deployed.PodUID] = deployed
	}
	for uid := range agents {
		state := agents[uid]
		deployed := deployments[uid]
		if deployed == nil || deployed.Checksum != config.Spec.Checksum || deployed.LastError != "" || deployed.ConsecutiveErrors != 0 || state.AppliedPlanID == "" || state.RunningPlanID == "" || state.ReloadPendingAt != "" || deployed.AppliedPlanID != state.AppliedPlanID || deployed.RunningPlanID != state.RunningPlanID {
			return false
		}
	}
	return true
}

func certificatesMatch(config *api.HAProxyCfg, secrets []corev1.Secret, setID string) bool {
	refs := config.Status.AuxiliaryFiles
	expected := make(map[string]bool, len(refs.SSLCertificates)+len(refs.SSLCaFiles))
	for _, ref := range refs.SSLCertificates {
		expected[ref.Name] = false
	}
	for _, ref := range refs.SSLCaFiles {
		if _, duplicate := expected[ref.Name]; duplicate {
			return false
		}
		expected[ref.Name] = true
	}
	seen := 0
	for i := range secrets {
		secret := &secrets[i]
		owned := false
		for _, owner := range secret.OwnerReferences {
			if owner.UID == config.UID {
				owned = true
			}
		}
		if !owned {
			continue
		}
		caFile, found := expected[secret.Name]
		if !found || !CertificateIdentityMatches(&secret.ObjectMeta, setID, caFile) {
			return false
		}
		seen++
	}
	return seen == len(expected)
}

func CertificateIdentityMatches(metadata *metav1.ObjectMeta, setID string, caFile bool) bool {
	if metadata.DeletionTimestamp != nil {
		return false
	}
	if !strings.HasPrefix(setID, "content-sha256:") {
		return metadata.Annotations[annotationPrefix+"auxiliary-set-id"] == setID
	}
	path := metadata.Annotations[annotationPrefix+"auxiliary-path"]
	checksum := metadata.Annotations[annotationPrefix+"checksum"]
	role := "ssl-certificate"
	if caFile {
		role = "ssl-ca"
	}
	if path == "" || checksum == "" || metadata.Labels[annotationPrefix+"type"] != role {
		return false
	}
	identity := fmt.Sprintf("Secret\x00%s\x00%s\x00%t", path, checksum, caFile)
	suffix := fmt.Sprintf("-content-%x", sha256.Sum256([]byte(identity)))
	return strings.HasSuffix(metadata.Name, suffix) || strings.Contains(metadata.Name, suffix+"-")
}
