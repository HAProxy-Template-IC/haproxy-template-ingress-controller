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
	"crypto/sha256"
	"fmt"
	"path"
	"strings"

	haproxyv1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/compression"
)

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

// buildPodStatus constructs a PodDeploymentStatus from a DeploymentStatusUpdate.
func buildPodStatus(update *DeploymentStatusUpdate) haproxyv1alpha1.PodDeploymentStatus {
	podStatus := haproxyv1alpha1.PodDeploymentStatus{
		PodName:  update.PodName,
		Checksum: update.Checksum,
	}

	// Set error tracking
	if update.Error != "" {
		podStatus.LastError = update.Error
	}

	return podStatus
}

// GenerateRuntimeConfigName generates the HAProxyCfg resource name from a template config name.
// This is the single source of truth for the naming convention used by both
// the ConfigPublisher and DeploymentScheduler.
func GenerateRuntimeConfigName(templateConfigName string) string {
	return templateConfigName + "-haproxycfg"
}

// sanitizeResourceName strips a file extension from source and applies the
// supplied character replacements (each pair is old, new), then prepends prefix.
// The result is a Kubernetes-safe resource name segment.
func sanitizeResourceName(prefix, source string, replacements ...string) string {
	name := source
	if ext := path.Ext(name); ext != "" {
		name = name[:len(name)-len(ext)]
	}
	for i := 0; i+1 < len(replacements); i += 2 {
		name = strings.ReplaceAll(name, replacements[i], replacements[i+1])
	}
	return prefix + name
}

func (p *Publisher) generateMapFileName(mapName string) string {
	return sanitizeResourceName("haproxy-map-", mapName)
}

// Replace underscores with hyphens to comply with DNS-1123 subdomain naming
// (Kubernetes secret names can't contain underscores).
func (p *Publisher) generateSecretName(certPath string) string {
	return sanitizeResourceName("haproxy-cert-", path.Base(certPath), "_", "-")
}

func (p *Publisher) generateGeneralFileName(fileName string) string {
	return sanitizeResourceName("haproxy-file-", path.Base(fileName), "_", "-", ".", "-")
}

func (p *Publisher) generateCRTListFileName(listPath string) string {
	return sanitizeResourceName("haproxy-crtlist-", path.Base(listPath), "_", "-")
}

func calculateChecksum(content string) string {
	hash := sha256.Sum256([]byte(content))
	return fmt.Sprintf("sha256:%x", hash)
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

	compressedContent := compression.Compress(content)

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
