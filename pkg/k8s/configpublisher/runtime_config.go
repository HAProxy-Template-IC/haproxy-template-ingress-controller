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
	"slices"

	haproxyv1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
)

// createOrUpdateRuntimeConfig creates or updates the HAProxyCfg resource.
func (p *Publisher) createOrUpdateRuntimeConfig(ctx context.Context, req *PublishRequest) (*haproxyv1alpha1.HAProxyCfg, error) {
	name := GenerateRuntimeConfigName(req.TemplateConfigName) + req.NameSuffix
	runtimeConfig := p.buildRuntimeConfig(name, req)

	var result *haproxyv1alpha1.HAProxyCfg
	err := retry.OnError(retry.DefaultRetry, retriableWrite, func() error {
		// Get existing resource (must be inside retry loop for fresh resourceVersion)
		existing, err := p.crdClient.HaproxyTemplateICV1alpha1().
			HAProxyCfgs(req.TemplateConfigNamespace).
			Get(ctx, name, metav1.GetOptions{})

		if err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("getting existing runtime config: %w", err)
			}
			// Create new resource. An AlreadyExists here (a racing writer created
			// it after our Get) is retriable via retriableWrite, so the retry
			// re-Gets and takes the update path below.
			created, createErr := p.createRuntimeConfig(ctx, req, runtimeConfig)
			if createErr != nil {
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
					Controller:         new(true),
					BlockOwnerDeletion: new(true),
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

	return runtimeConfig
}

// createRuntimeConfig creates a new HAProxyCfg resource.
func (p *Publisher) createRuntimeConfig(ctx context.Context, req *PublishRequest, runtimeConfig *haproxyv1alpha1.HAProxyCfg) (*haproxyv1alpha1.HAProxyCfg, error) {
	created, err := p.crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs(req.TemplateConfigNamespace).
		Create(ctx, runtimeConfig, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("creating runtime config: %w", err)
	}

	// Set validation error status if this is an invalid config
	if req.ValidationError != "" {
		p.updateValidationErrorStatus(ctx, created, req.ValidationError)
	}

	return created, nil
}

// updateValidationErrorStatus sets or clears the ValidationError on a HAProxyCfg status.
// Called only on validation error state transitions (ok→error or error→ok).
func (p *Publisher) updateValidationErrorStatus(ctx context.Context, cfg *haproxyv1alpha1.HAProxyCfg, validationError string) {
	cfg.Status.ValidationError = validationError

	_, err := p.crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs(cfg.Namespace).
		UpdateStatus(ctx, cfg, metav1.UpdateOptions{})
	if err != nil {
		p.logger.Debug("status update conflict (will retry on next reconciliation)",
			"type", "runtime_config_status",
			"name", cfg.Name,
			"error", err,
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

	// Check for validation error state transition before spec update
	previousValidationError := existing.Status.ValidationError

	// Update existing resource
	existing.Spec = runtimeConfig.Spec
	existing.Labels = runtimeConfig.Labels

	updated, err := p.crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs(req.TemplateConfigNamespace).
		Update(ctx, existing, metav1.UpdateOptions{})
	if err != nil {
		return nil, fmt.Errorf("updating runtime config: %w", err)
	}

	// Only update status on validation error state transitions (ok→error or error→ok).
	// Each UpdateStatus writes the full ~509 KB object to etcd.
	if previousValidationError != req.ValidationError {
		p.updateValidationErrorStatus(ctx, updated, req.ValidationError)
	}
	return updated, nil
}

// updateRuntimeConfigStatus updates the HAProxyCfg status with child resource references.
// Skips the UpdateStatus API call if the references are unchanged.
func (p *Publisher) updateRuntimeConfigStatus(ctx context.Context, runtimeConfig *haproxyv1alpha1.HAProxyCfg, result *PublishResult) error {
	// Get the latest version
	current, err := p.crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs(runtimeConfig.Namespace).
		Get(ctx, runtimeConfig.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("getting runtime config: %w", err)
	}

	// Build new auxiliary file references
	newAux := buildAuxiliaryFileReferences(runtimeConfig.Namespace, result)

	// Skip UpdateStatus if nothing changed
	if auxiliaryRefsEqual(current.Status.AuxiliaryFiles, newAux) {
		p.logger.Debug("skipping HAProxyCfg status update, references unchanged",
			"name", current.Name,
		)
		return nil
	}

	// Apply changes
	current.Status.AuxiliaryFiles = newAux

	_, err = p.crdClient.HaproxyTemplateICV1alpha1().
		HAProxyCfgs(runtimeConfig.Namespace).
		UpdateStatus(ctx, current, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("updating status: %w", err)
	}

	return nil
}

// buildAuxiliaryFileReferences constructs an AuxiliaryFileReferences from a PublishResult.
func buildAuxiliaryFileReferences(namespace string, result *PublishResult) *haproxyv1alpha1.AuxiliaryFileReferences {
	// Returns nil (not []) for empty inputs so the AuxiliaryFileReferences
	// field stays absent in JSON via its omitempty tag — matching the prior
	// behavior of unexecuted appends leaving the field nil.
	refs := func(names []string, kind string) []haproxyv1alpha1.ResourceReference {
		if len(names) == 0 {
			return nil
		}
		out := make([]haproxyv1alpha1.ResourceReference, 0, len(names))
		for _, name := range names {
			out = append(out, haproxyv1alpha1.ResourceReference{
				Kind: kind, Name: name, Namespace: namespace,
			})
		}
		return out
	}
	return &haproxyv1alpha1.AuxiliaryFileReferences{
		MapFiles:        refs(result.MapFileNames, "HAProxyMapFile"),
		SSLCertificates: refs(result.SecretNames, "Secret"),
		GeneralFiles:    refs(result.GeneralFileNames, "HAProxyGeneralFile"),
		CRTListFiles:    refs(result.CRTListFileNames, "HAProxyCRTListFile"),
	}
}

// auxiliaryRefsEqual compares two AuxiliaryFileReferences for equality.
// ResourceReference is comparable (string-only fields), so slices.Equal is sufficient.
func auxiliaryRefsEqual(a, b *haproxyv1alpha1.AuxiliaryFileReferences) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return slices.Equal(a.MapFiles, b.MapFiles) &&
		slices.Equal(a.SSLCertificates, b.SSLCertificates) &&
		slices.Equal(a.GeneralFiles, b.GeneralFiles) &&
		slices.Equal(a.CRTListFiles, b.CRTListFiles)
}
