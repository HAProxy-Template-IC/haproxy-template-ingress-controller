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
	"time"

	haproxyv1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
)

// createOrUpdateRuntimeConfig creates or updates the HAProxyCfg resource.
func (p *Publisher) createOrUpdateRuntimeConfig(ctx context.Context, req *PublishRequest) (*haproxyv1alpha1.HAProxyCfg, error) {
	name := p.generateRuntimeConfigName(req.TemplateConfigName) + req.NameSuffix
	runtimeConfig := p.buildRuntimeConfig(name, req)

	var result *haproxyv1alpha1.HAProxyCfg
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Get existing resource (must be inside retry loop for fresh resourceVersion)
		existing, err := p.crdClient.HaproxyTemplateICV1alpha1().
			HAProxyCfgs(req.TemplateConfigNamespace).
			Get(ctx, name, metav1.GetOptions{})

		if err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to get existing runtime config: %w", err)
			}
			// Create new resource
			created, createErr := p.createRuntimeConfig(ctx, req, runtimeConfig)
			if createErr != nil {
				// If AlreadyExists, another reconciler created it - retry to update
				if apierrors.IsAlreadyExists(createErr) {
					return createErr
				}
				return createErr
			}
			result = created
			return nil
		}

		// Update existing resource with fresh copy
		updated, updateErr := p.updateRuntimeConfig(ctx, req, existing, runtimeConfig)
		if updateErr != nil {
			return updateErr
		}
		result = updated
		return nil
	})

	if err != nil {
		return nil, err
	}
	return result, nil
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
		p.logger.Debug("status update conflict after creation (will retry on next reconciliation)",
			"type", "runtime_config_status",
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
// Skips the update if the checksum is unchanged to avoid unnecessary API calls.
func (p *Publisher) updateRuntimeConfig(ctx context.Context, req *PublishRequest, existing, runtimeConfig *haproxyv1alpha1.HAProxyCfg) (*haproxyv1alpha1.HAProxyCfg, error) {
	// Skip update if checksum hasn't changed (content is identical)
	if existing.Spec.Checksum == runtimeConfig.Spec.Checksum {
		p.logger.Debug("skipping HAProxyCfg spec update, checksum unchanged",
			"name", existing.Name,
			"checksum", existing.Spec.Checksum,
		)
		return existing, nil
	}

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
		p.logger.Debug("status update conflict (will retry on next reconciliation)",
			"type", "runtime_config_status",
			"name", updated.Name,
			"error", err,
		)
	}
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
