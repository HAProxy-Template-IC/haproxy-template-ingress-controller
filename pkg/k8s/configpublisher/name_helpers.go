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
	"time"

	haproxyv1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/compression"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

	// Create map for efficient lookup (using pointers to avoid copying 144-byte structs)
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
func podStatusEqual(a, b *haproxyv1alpha1.PodDeploymentStatus) bool {
	// Compare DeployedAt
	if !a.DeployedAt.Equal(&b.DeployedAt) {
		return false
	}

	// Compare simple fields
	if a.Checksum != b.Checksum {
		return false
	}
	if a.LastReloadID != b.LastReloadID {
		return false
	}
	if a.LastError != b.LastError {
		return false
	}
	if a.VersionConflictRetries != b.VersionConflictRetries {
		return false
	}
	if a.FallbackUsed != b.FallbackUsed {
		return false
	}
	if a.ConsecutiveErrors != b.ConsecutiveErrors {
		return false
	}

	// Compare LastReloadAt (both could be nil)
	if !metaTimeEqual(a.LastReloadAt, b.LastReloadAt) {
		return false
	}

	// Compare LastErrorAt (both could be nil)
	if !metaTimeEqual(a.LastErrorAt, b.LastErrorAt) {
		return false
	}

	// Compare SyncDuration (both could be nil)
	if !metaDurationEqual(a.SyncDuration, b.SyncDuration) {
		return false
	}

	// Compare LastOperationSummary (both could be nil)
	if !operationSummaryEqual(a.LastOperationSummary, b.LastOperationSummary) {
		return false
	}

	return true
}

// metaTimeEqual compares two *metav1.Time pointers for equality.
func metaTimeEqual(a, b *metav1.Time) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a != nil && b != nil {
		if !a.Equal(b) {
			return false
		}
	}
	return true
}

// metaDurationEqual compares two *metav1.Duration pointers for equality.
func metaDurationEqual(a, b *metav1.Duration) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a != nil && b != nil {
		if a.Duration != b.Duration {
			return false
		}
	}
	return true
}

// operationSummaryEqual compares two *OperationSummary pointers for equality.
func operationSummaryEqual(a, b *haproxyv1alpha1.OperationSummary) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a == nil && b == nil {
		return true
	}
	// Both are non-nil, compare all fields
	return a.TotalAPIOperations == b.TotalAPIOperations &&
		a.BackendsAdded == b.BackendsAdded &&
		a.BackendsRemoved == b.BackendsRemoved &&
		a.BackendsModified == b.BackendsModified &&
		a.ServersAdded == b.ServersAdded &&
		a.ServersRemoved == b.ServersRemoved &&
		a.ServersModified == b.ServersModified &&
		a.FrontendsAdded == b.FrontendsAdded &&
		a.FrontendsRemoved == b.FrontendsRemoved &&
		a.FrontendsModified == b.FrontendsModified
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
// Only tracks: PodName, Checksum, DeployedAt, and error fields.
// Preserves existing status when checksum hasn't changed, avoiding unnecessary updates.
func buildAuxiliaryFilePodStatus(
	podName string,
	fileChecksum string,
	existingStatus *haproxyv1alpha1.PodDeploymentStatus,
	deployedAt time.Time,
) haproxyv1alpha1.PodDeploymentStatus {
	// Handle zero deployedAt (drift check with no operations)
	newDeployedAt := metav1.NewTime(deployedAt)
	if deployedAt.IsZero() && existingStatus != nil {
		newDeployedAt = existingStatus.DeployedAt
	}

	// Checksum unchanged - preserve existing status entirely
	if existingStatus != nil && existingStatus.Checksum == fileChecksum {
		return haproxyv1alpha1.PodDeploymentStatus{
			PodName:           podName,
			Checksum:          fileChecksum,
			DeployedAt:        existingStatus.DeployedAt,
			LastError:         existingStatus.LastError,
			ConsecutiveErrors: existingStatus.ConsecutiveErrors,
			LastErrorAt:       existingStatus.LastErrorAt,
		}
	}

	// Checksum changed or new pod - new deployment
	return haproxyv1alpha1.PodDeploymentStatus{
		PodName:    podName,
		Checksum:   fileChecksum,
		DeployedAt: newDeployedAt,
	}
}

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

		// Preserve deployedAt if no operations were performed
		if podStatus.DeployedAt.IsZero() {
			podStatus.DeployedAt = pods[i].DeployedAt
		}

		// Preserve performance metrics if not being updated (e.g., drift check with no changes)
		if podStatus.SyncDuration == nil {
			podStatus.SyncDuration = pods[i].SyncDuration
		}
		if podStatus.LastOperationSummary == nil {
			podStatus.LastOperationSummary = pods[i].LastOperationSummary
		}
		// Note: VersionConflictRetries and FallbackUsed are only meaningful when
		// SyncDuration is set, so they're implicitly preserved when SyncDuration is nil

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
