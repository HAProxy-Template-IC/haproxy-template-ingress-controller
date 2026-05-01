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
	"path/filepath"
	"strconv"

	haproxyv1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
)

const runtimeConfigLabelKey = "haproxy-haptic.org/runtime-config"

// runtimeConfigOwnerRefs builds the OwnerReferences slice that ties an
// auxiliary-file CRD (or Secret) to its parent HAProxyCfg. All four
// createOrUpdate* helpers in this file use the same shape.
func runtimeConfigOwnerRefs(owner *haproxyv1alpha1.HAProxyCfg) []metav1.OwnerReference {
	return []metav1.OwnerReference{
		{
			APIVersion:         "haproxy-haptic.org/v1alpha1",
			Kind:               "HAProxyCfg",
			Name:               owner.Name,
			UID:                owner.UID,
			Controller:         new(true),
			BlockOwnerDeletion: new(true),
		},
	}
}

// runtimeConfigLabels returns the standard label map that pins an auxiliary
// resource to its parent HAProxyCfg via the runtime-config label.
func runtimeConfigLabels(owner *haproxyv1alpha1.HAProxyCfg) map[string]string {
	return map[string]string{
		runtimeConfigLabelKey: owner.Name,
	}
}

// createOrUpdateMapFile creates or updates a HAProxyMapFile resource.
func (p *Publisher) createOrUpdateMapFile(ctx context.Context, req *PublishRequest, owner *haproxyv1alpha1.HAProxyCfg, mapFile auxiliaryfiles.MapFile) (string, error) {
	name := p.generateMapFileName(filepath.Base(mapFile.Path))
	checksum := calculateChecksum(mapFile.Content) // Checksum of original content

	// Compress if content exceeds threshold
	result := p.compressIfNeeded(mapFile.Content, req.CompressionThreshold, "HAProxyMapFile/"+name)

	// Build spec and labels once (these don't change between retries)
	spec := haproxyv1alpha1.HAProxyMapFileSpec{
		MapName:    filepath.Base(mapFile.Path),
		Path:       mapFile.Path,
		Entries:    result.content,
		Checksum:   checksum,
		Compressed: result.compressed,
	}
	labels := runtimeConfigLabels(owner)
	ownerRefs := runtimeConfigOwnerRefs(owner)

	var resultName string
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Get existing resource (must be inside retry loop for fresh resourceVersion)
		existing, err := p.crdClient.HaproxyTemplateICV1alpha1().
			HAProxyMapFiles(req.TemplateConfigNamespace).
			Get(ctx, name, metav1.GetOptions{})

		if err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("getting existing map file: %w", err)
			}

			// Create new resource
			mapFileResource := &haproxyv1alpha1.HAProxyMapFile{
				ObjectMeta: metav1.ObjectMeta{
					Name:            name,
					Namespace:       req.TemplateConfigNamespace,
					Labels:          labels,
					OwnerReferences: ownerRefs,
				},
				Spec: spec,
			}

			created, createErr := p.crdClient.HaproxyTemplateICV1alpha1().
				HAProxyMapFiles(req.TemplateConfigNamespace).
				Create(ctx, mapFileResource, metav1.CreateOptions{})
			if createErr != nil {
				// If AlreadyExists, another reconciler created it - retry to update
				if apierrors.IsAlreadyExists(createErr) {
					return createErr
				}
				return fmt.Errorf("creating map file: %w", createErr)
			}

			resultName = created.Name
			return nil
		}

		// Skip update if checksum hasn't changed
		if existing.Spec.Checksum == spec.Checksum {
			resultName = existing.Name
			return nil
		}

		// Update existing resource with fresh copy
		existing.Spec = spec
		existing.Labels = labels

		updated, updateErr := p.crdClient.HaproxyTemplateICV1alpha1().
			HAProxyMapFiles(req.TemplateConfigNamespace).
			Update(ctx, existing, metav1.UpdateOptions{})
		if updateErr != nil {
			return fmt.Errorf("updating map file: %w", updateErr)
		}

		resultName = updated.Name
		return nil
	})

	if err != nil {
		return "", err
	}

	return resultName, nil
}

// createOrUpdateSSLSecret creates or updates a Secret for SSL certificates.
func (p *Publisher) createOrUpdateSSLSecret(ctx context.Context, req *PublishRequest, owner *haproxyv1alpha1.HAProxyCfg, cert auxiliaryfiles.SSLCertificate) (string, error) {
	name := p.generateSecretName(filepath.Base(cert.Path))
	checksum := calculateChecksum(cert.Content) // Checksum of original content

	// Compress if content exceeds threshold
	result := p.compressIfNeeded(cert.Content, req.CompressionThreshold, "Secret/"+name)

	labels := runtimeConfigLabels(owner)
	labels["haproxy-haptic.org/type"] = "ssl-certificate"
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: req.TemplateConfigNamespace,
			Labels:    labels,
			Annotations: map[string]string{
				"haproxy-haptic.org/compressed": strconv.FormatBool(result.compressed),
				"haproxy-haptic.org/checksum":   checksum,
			},
			OwnerReferences: runtimeConfigOwnerRefs(owner),
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"certificate": []byte(result.content),
			"path":        []byte(cert.Path),
		},
	}

	// Try to get existing secret
	existing, err := p.k8sClient.CoreV1().
		Secrets(req.TemplateConfigNamespace).
		Get(ctx, name, metav1.GetOptions{})

	if err != nil {
		if !apierrors.IsNotFound(err) {
			return "", fmt.Errorf("getting existing secret: %w", err)
		}

		// Create new secret
		created, err := p.k8sClient.CoreV1().
			Secrets(req.TemplateConfigNamespace).
			Create(ctx, secret, metav1.CreateOptions{})
		if err != nil {
			return "", fmt.Errorf("creating secret: %w", err)
		}

		return created.Name, nil
	}

	// Skip update if checksum hasn't changed
	if existing.Annotations != nil && existing.Annotations["haproxy-haptic.org/checksum"] == checksum {
		return existing.Name, nil
	}

	// Update existing secret
	existing.Data = secret.Data
	existing.Labels = secret.Labels
	existing.Annotations = secret.Annotations

	updated, err := p.k8sClient.CoreV1().
		Secrets(req.TemplateConfigNamespace).
		Update(ctx, existing, metav1.UpdateOptions{})
	if err != nil {
		return "", fmt.Errorf("updating secret: %w", err)
	}

	return updated.Name, nil
}

// createOrUpdateGeneralFile creates or updates a HAProxyGeneralFile resource.
func (p *Publisher) createOrUpdateGeneralFile(ctx context.Context, req *PublishRequest, owner *haproxyv1alpha1.HAProxyCfg, generalFile auxiliaryfiles.GeneralFile) (string, error) {
	name := p.generateGeneralFileName(generalFile.Filename)
	checksum := calculateChecksum(generalFile.Content) // Checksum of original content

	// Compress if content exceeds threshold
	result := p.compressIfNeeded(generalFile.Content, req.CompressionThreshold, "HAProxyGeneralFile/"+name)

	// Build spec and labels once (these don't change between retries)
	spec := haproxyv1alpha1.HAProxyGeneralFileSpec{
		FileName:   generalFile.Filename,
		Path:       generalFile.Path,
		Content:    result.content,
		Checksum:   checksum,
		Compressed: result.compressed,
	}
	labels := runtimeConfigLabels(owner)
	ownerRefs := runtimeConfigOwnerRefs(owner)

	var resultName string
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Get existing resource (must be inside retry loop for fresh resourceVersion)
		existing, err := p.crdClient.HaproxyTemplateICV1alpha1().
			HAProxyGeneralFiles(req.TemplateConfigNamespace).
			Get(ctx, name, metav1.GetOptions{})

		if err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("getting existing general file: %w", err)
			}

			// Create new resource
			generalFileResource := &haproxyv1alpha1.HAProxyGeneralFile{
				ObjectMeta: metav1.ObjectMeta{
					Name:            name,
					Namespace:       req.TemplateConfigNamespace,
					Labels:          labels,
					OwnerReferences: ownerRefs,
				},
				Spec: spec,
			}

			created, createErr := p.crdClient.HaproxyTemplateICV1alpha1().
				HAProxyGeneralFiles(req.TemplateConfigNamespace).
				Create(ctx, generalFileResource, metav1.CreateOptions{})
			if createErr != nil {
				// If AlreadyExists, another reconciler created it - retry to update
				if apierrors.IsAlreadyExists(createErr) {
					return createErr
				}
				return fmt.Errorf("creating general file: %w", createErr)
			}

			resultName = created.Name
			return nil
		}

		// Skip update if checksum hasn't changed
		if existing.Spec.Checksum == spec.Checksum {
			resultName = existing.Name
			return nil
		}

		// Update existing resource with fresh copy
		existing.Spec = spec
		existing.Labels = labels

		updated, updateErr := p.crdClient.HaproxyTemplateICV1alpha1().
			HAProxyGeneralFiles(req.TemplateConfigNamespace).
			Update(ctx, existing, metav1.UpdateOptions{})
		if updateErr != nil {
			return fmt.Errorf("updating general file: %w", updateErr)
		}

		resultName = updated.Name
		return nil
	})

	if err != nil {
		return "", err
	}

	return resultName, nil
}

// createOrUpdateCRTListFile creates or updates a HAProxyCRTListFile resource.
func (p *Publisher) createOrUpdateCRTListFile(ctx context.Context, req *PublishRequest, owner *haproxyv1alpha1.HAProxyCfg, crtListFile auxiliaryfiles.CRTListFile) (string, error) {
	name := p.generateCRTListFileName(crtListFile.Path)
	checksum := calculateChecksum(crtListFile.Content) // Checksum of original content

	// Compress if content exceeds threshold
	result := p.compressIfNeeded(crtListFile.Content, req.CompressionThreshold, "HAProxyCRTListFile/"+name)

	// Build spec and labels once (these don't change between retries)
	spec := haproxyv1alpha1.HAProxyCRTListFileSpec{
		ListName:   filepath.Base(crtListFile.Path),
		Path:       crtListFile.Path,
		Entries:    result.content,
		Checksum:   checksum,
		Compressed: result.compressed,
	}
	labels := runtimeConfigLabels(owner)
	ownerRefs := runtimeConfigOwnerRefs(owner)

	var resultName string
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Get existing resource (must be inside retry loop for fresh resourceVersion)
		existing, err := p.crdClient.HaproxyTemplateICV1alpha1().
			HAProxyCRTListFiles(req.TemplateConfigNamespace).
			Get(ctx, name, metav1.GetOptions{})

		if err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("getting existing crt-list file: %w", err)
			}

			// Create new resource
			crtListResource := &haproxyv1alpha1.HAProxyCRTListFile{
				ObjectMeta: metav1.ObjectMeta{
					Name:            name,
					Namespace:       req.TemplateConfigNamespace,
					Labels:          labels,
					OwnerReferences: ownerRefs,
				},
				Spec: spec,
			}

			created, createErr := p.crdClient.HaproxyTemplateICV1alpha1().
				HAProxyCRTListFiles(req.TemplateConfigNamespace).
				Create(ctx, crtListResource, metav1.CreateOptions{})
			if createErr != nil {
				// If AlreadyExists, another reconciler created it - retry to update
				if apierrors.IsAlreadyExists(createErr) {
					return createErr
				}
				return fmt.Errorf("creating crt-list file: %w", createErr)
			}

			resultName = created.Name
			return nil
		}

		// Skip update if checksum hasn't changed
		if existing.Spec.Checksum == spec.Checksum {
			resultName = existing.Name
			return nil
		}

		// Update existing resource with fresh copy
		existing.Spec = spec
		existing.Labels = labels

		updated, updateErr := p.crdClient.HaproxyTemplateICV1alpha1().
			HAProxyCRTListFiles(req.TemplateConfigNamespace).
			Update(ctx, existing, metav1.UpdateOptions{})
		if updateErr != nil {
			return fmt.Errorf("updating crt-list file: %w", updateErr)
		}

		resultName = updated.Name
		return nil
	})

	if err != nil {
		return "", err
	}

	return resultName, nil
}
