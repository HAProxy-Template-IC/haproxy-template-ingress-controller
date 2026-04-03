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
	"path/filepath"
	"strings"

	haproxyv1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/compression"
)

// copyPodStatuses creates a deep copy of a PodDeploymentStatus slice.
// This is necessary because Go slice assignment copies only the header, not the underlying array.
func copyPodStatuses(pods []haproxyv1alpha1.PodDeploymentStatus) []haproxyv1alpha1.PodDeploymentStatus {
	if pods == nil {
		return nil
	}
	result := make([]haproxyv1alpha1.PodDeploymentStatus, len(pods))
	copy(result, pods)
	return result
}

// podStatusesEqual compares two PodDeploymentStatus slices for equality.
// Returns true if both slices have the same pods with the same status values.
// This is used to skip unnecessary UpdateStatus API calls when the status hasn't changed.
func podStatusesEqual(a, b []haproxyv1alpha1.PodDeploymentStatus) bool {
	if len(a) != len(b) {
		return false
	}

	// Create map for efficient lookup
	statusMap := make(map[string]*haproxyv1alpha1.PodDeploymentStatus, len(a))
	for i := range a {
		statusMap[a[i].PodName] = &a[i]
	}

	for i := range b {
		podA, exists := statusMap[b[i].PodName]
		if !exists {
			return false
		}

		if !podStatusEqual(podA, &b[i]) {
			return false
		}
	}

	return true
}

// podStatusEqual compares two individual PodDeploymentStatus structs for equality.
//
// Compares all state fields: checksum (which config is deployed) and error state.
// Per-event telemetry belongs in logs and Prometheus metrics, not in CRD status.
func podStatusEqual(a, b *haproxyv1alpha1.PodDeploymentStatus) bool {
	if a.Checksum != b.Checksum {
		return false
	}
	if a.LastError != b.LastError {
		return false
	}
	if a.ConsecutiveErrors != b.ConsecutiveErrors {
		return false
	}

	return true
}

// findPodStatus finds a pod's status in a slice by name.
// Returns nil if not found.
func findPodStatus(pods []haproxyv1alpha1.PodDeploymentStatus, podName string) *haproxyv1alpha1.PodDeploymentStatus {
	for i := range pods {
		if pods[i].PodName == podName {
			return &pods[i]
		}
	}
	return nil
}

// buildAuxiliaryFilePodStatus constructs a minimal PodDeploymentStatus for auxiliary files.
// Only tracks: PodName, Checksum, and error fields.
// Preserves existing status when checksum hasn't changed, avoiding unnecessary updates.
func buildAuxiliaryFilePodStatus(
	podName string,
	fileChecksum string,
	existingStatus *haproxyv1alpha1.PodDeploymentStatus,
) haproxyv1alpha1.PodDeploymentStatus {
	// Checksum unchanged - preserve existing status entirely
	if existingStatus != nil && existingStatus.Checksum == fileChecksum {
		return haproxyv1alpha1.PodDeploymentStatus{
			PodName:           podName,
			Checksum:          fileChecksum,
			LastError:         existingStatus.LastError,
			ConsecutiveErrors: existingStatus.ConsecutiveErrors,
		}
	}

	// Checksum changed or new pod - new deployment
	return haproxyv1alpha1.PodDeploymentStatus{
		PodName:  podName,
		Checksum: fileChecksum,
	}
}

// addOrUpdatePodStatus adds or updates a pod in the deployment status list.
// Returns the updated slice. This helper is used for auxiliary file types
// (MapFile, GeneralFile, CRTListFile).
func addOrUpdatePodStatus(pods []haproxyv1alpha1.PodDeploymentStatus, podStatus *haproxyv1alpha1.PodDeploymentStatus) []haproxyv1alpha1.PodDeploymentStatus {
	for i := range pods {
		if pods[i].PodName == podStatus.PodName {
			pods[i] = *podStatus
			return pods
		}
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
	return append(pods, *podStatus)
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
