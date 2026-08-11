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
	"path"
	"strconv"

	haproxyv1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"

	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
)

const (
	runtimeConfigLabelKey       = "haproxy-haptic.org/runtime-config"
	auxiliarySetIDAnnotationKey = "haproxy-haptic.org/auxiliary-set-id"
)

// runtimeConfigOwnerRefs builds the OwnerReferences slice that ties an
// auxiliary-file CRD (or Secret) to its parent HAProxyCfg. All four
// createOrUpdate* helpers in this file use the same shape.
func runtimeConfigOwnerRefs(owner *haproxyv1alpha1.HAProxyCfg) []metav1.OwnerReference {
	return []metav1.OwnerReference{
		{
			APIVersion:         apiVersionV1Alpha1,
			Kind:               runtimeConfigKind,
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
		runtimeConfigLabelKey: runtimeConfigLabelValue(owner.Name),
	}
}

func runtimeConfigAnnotations(owner *haproxyv1alpha1.HAProxyCfg) map[string]string {
	return map[string]string{
		auxiliarySetIDAnnotationKey: owner.Annotations[auxiliarySetIDAnnotationKey],
	}
}

func auxiliaryMetadataEqual(
	existing metav1.Object,
	labels, annotations map[string]string,
	ownerReferences []metav1.OwnerReference,
) bool {
	return apiequality.Semantic.DeepEqual(existing.GetLabels(), labels) &&
		apiequality.Semantic.DeepEqual(existing.GetAnnotations(), annotations) &&
		apiequality.Semantic.DeepEqual(existing.GetOwnerReferences(), ownerReferences)
}

// retriableWrite reports whether a create/update error is a transient write
// conflict worth retrying within retry.OnError:
//
//   - a resourceVersion Conflict (409, the case retry.RetryOnConflict covers), or
//   - an AlreadyExists (409 with a different reason): a racing writer created the
//     object between our Get-returns-NotFound and our Create. retry.RetryOnConflict
//     does NOT cover this (it only matches IsConflict), so the "retry to update"
//     create paths below use this predicate to actually re-Get and take the update
//     branch instead of surfacing the AlreadyExists to the caller.
func retriableWrite(err error) bool {
	return apierrors.IsConflict(err) || apierrors.IsAlreadyExists(err)
}

// auxResourceOps abstracts one auxiliary resource kind (map file, SSL secret,
// general file, crt-list file) for createOrUpdateAuxResource. The closures
// capture the typed client, the precomputed name/spec/labels, and the
// kind-specific checksum location.
type auxResourceOps[T interface{ GetName() string }] struct {
	// kind names the resource in error messages (e.g. "map file").
	kind string
	// get fetches the current object (called inside the retry loop so every
	// attempt sees a fresh resourceVersion).
	get func(ctx context.Context) (T, error)
	// create builds and creates the new object.
	create func(ctx context.Context) (T, error)
	// upToDate reports whether the existing object already carries the
	// desired content and ownership metadata.
	upToDate func(existing T) bool
	// managedByOwner reports whether an existing name belongs to this
	// HAProxyCfg and may be updated.
	managedByOwner func(existing T) bool
	// update mutates the freshly fetched object with the desired state and
	// writes it back.
	update func(ctx context.Context, existing T) (T, error)
}

type auxiliaryResourceOwnershipError struct {
	kind string
	name string
}

func (e *auxiliaryResourceOwnershipError) Error() string {
	return fmt.Sprintf("%s %q is managed by another HAProxyCfg", e.kind, e.name)
}

// createOrUpdateAuxResource runs the shared create-or-update workflow for an
// auxiliary resource: get → create when absent (retrying a lost create race
// into the update branch) → skip when the desired state is unchanged → update.
// Returns the name of the resource that ends up holding the desired state.
func createOrUpdateAuxResource[T interface{ GetName() string }](ctx context.Context, ops auxResourceOps[T]) (string, error) {
	var resultName string
	err := retry.OnError(retry.DefaultRetry, retriableWrite, func() error {
		existing, err := ops.get(ctx)
		if err != nil {
			if !apierrors.IsNotFound(err) {
				return fmt.Errorf("getting existing %s: %w", ops.kind, err)
			}

			created, createErr := ops.create(ctx)
			if createErr != nil {
				// If AlreadyExists, another reconciler created it - retry to update
				if apierrors.IsAlreadyExists(createErr) {
					return createErr
				}
				return fmt.Errorf("creating %s: %w", ops.kind, createErr)
			}

			resultName = created.GetName()
			return nil
		}
		if !ops.managedByOwner(existing) {
			return &auxiliaryResourceOwnershipError{kind: ops.kind, name: existing.GetName()}
		}

		// Skip update if the desired state hasn't changed.
		if ops.upToDate(existing) {
			resultName = existing.GetName()
			return nil
		}

		updated, updateErr := ops.update(ctx, existing)
		if updateErr != nil {
			return fmt.Errorf("updating %s: %w", ops.kind, updateErr)
		}

		resultName = updated.GetName()
		return nil
	})
	if err != nil {
		return "", err
	}
	return resultName, nil
}

// createOrUpdateMapFile creates or updates a HAProxyMapFile resource.
func (p *Publisher) createOrUpdateMapFile(ctx context.Context, req *PublishRequest, owner *haproxyv1alpha1.HAProxyCfg, mapFile auxiliaryfiles.MapFile, name string) (string, error) {
	checksum := calculateChecksum(mapFile.Content) // Checksum of original content

	// Compress if content exceeds threshold
	result := p.compressIfNeeded(mapFile.Content, req.CompressionThreshold, "HAProxyMapFile/"+name)

	spec := haproxyv1alpha1.HAProxyMapFileSpec{
		MapName:    path.Base(mapFile.Path),
		Path:       mapFile.Path,
		Entries:    result.content,
		Checksum:   checksum,
		Compressed: result.compressed,
	}
	labels := runtimeConfigLabels(owner)
	annotations := runtimeConfigAnnotations(owner)
	ownerReferences := runtimeConfigOwnerRefs(owner)
	client := p.crdClient.HaproxyTemplateICV1alpha1().HAProxyMapFiles(req.TemplateConfigNamespace)

	return createOrUpdateAuxResource(ctx, auxResourceOps[*haproxyv1alpha1.HAProxyMapFile]{
		kind: "map file",
		get: func(ctx context.Context) (*haproxyv1alpha1.HAProxyMapFile, error) {
			return client.Get(ctx, name, metav1.GetOptions{})
		},
		create: func(ctx context.Context) (*haproxyv1alpha1.HAProxyMapFile, error) {
			return client.Create(ctx, &haproxyv1alpha1.HAProxyMapFile{
				ObjectMeta: metav1.ObjectMeta{
					Name:            name,
					Namespace:       req.TemplateConfigNamespace,
					Labels:          labels,
					Annotations:     annotations,
					OwnerReferences: ownerReferences,
				},
				Spec: spec,
			}, metav1.CreateOptions{})
		},
		upToDate: func(existing *haproxyv1alpha1.HAProxyMapFile) bool {
			return apiequality.Semantic.DeepEqual(existing.Spec, spec) &&
				auxiliaryMetadataEqual(existing, labels, annotations, ownerReferences)
		},
		managedByOwner: func(existing *haproxyv1alpha1.HAProxyMapFile) bool {
			return managedByRuntimeConfig(existing, owner.Name)
		},
		update: func(ctx context.Context, existing *haproxyv1alpha1.HAProxyMapFile) (*haproxyv1alpha1.HAProxyMapFile, error) {
			existing.Spec = spec
			existing.Labels = labels
			existing.Annotations = annotations
			existing.OwnerReferences = ownerReferences
			return client.Update(ctx, existing, metav1.UpdateOptions{})
		},
	})
}

// createOrUpdateSSLSecret creates or updates a Secret for SSL certificates.
func (p *Publisher) createOrUpdateSSLSecret(ctx context.Context, req *PublishRequest, owner *haproxyv1alpha1.HAProxyCfg, cert auxiliaryfiles.SSLCertificate, name string) (string, error) {
	checksum := calculateChecksum(cert.Content) // Checksum of original content

	// Compress if content exceeds threshold
	result := p.compressIfNeeded(cert.Content, req.CompressionThreshold, "Secret/"+name)

	labels := runtimeConfigLabels(owner)
	labels["haproxy-haptic.org/type"] = "ssl-certificate"
	ownerReferences := runtimeConfigOwnerRefs(owner)
	annotations := runtimeConfigAnnotations(owner)
	annotations["haproxy-haptic.org/compressed"] = strconv.FormatBool(result.compressed)
	annotations["haproxy-haptic.org/checksum"] = checksum
	data := map[string][]byte{
		"certificate": []byte(result.content),
		"path":        []byte(cert.Path),
	}
	client := p.k8sClient.CoreV1().Secrets(req.TemplateConfigNamespace)

	return createOrUpdateAuxResource(ctx, auxResourceOps[*corev1.Secret]{
		kind: "secret",
		get: func(ctx context.Context) (*corev1.Secret, error) {
			return client.Get(ctx, name, metav1.GetOptions{})
		},
		create: func(ctx context.Context) (*corev1.Secret, error) {
			return client.Create(ctx, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:            name,
					Namespace:       req.TemplateConfigNamespace,
					Labels:          labels,
					Annotations:     annotations,
					OwnerReferences: ownerReferences,
				},
				Type: corev1.SecretTypeOpaque,
				Data: data,
			}, metav1.CreateOptions{})
		},
		upToDate: func(existing *corev1.Secret) bool {
			return existing.Type == corev1.SecretTypeOpaque &&
				apiequality.Semantic.DeepEqual(existing.Data, data) &&
				auxiliaryMetadataEqual(existing, labels, annotations, ownerReferences)
		},
		managedByOwner: func(existing *corev1.Secret) bool {
			return managedByRuntimeConfig(existing, owner.Name)
		},
		update: func(ctx context.Context, existing *corev1.Secret) (*corev1.Secret, error) {
			existing.Data = data
			existing.Labels = labels
			existing.Annotations = annotations
			existing.OwnerReferences = ownerReferences
			existing.Type = corev1.SecretTypeOpaque
			return client.Update(ctx, existing, metav1.UpdateOptions{})
		},
	})
}

// createOrUpdateGeneralFile creates or updates a HAProxyGeneralFile resource.
func (p *Publisher) createOrUpdateGeneralFile(ctx context.Context, req *PublishRequest, owner *haproxyv1alpha1.HAProxyCfg, generalFile auxiliaryfiles.GeneralFile, name string) (string, error) {
	checksum := calculateChecksum(generalFile.Content) // Checksum of original content

	// Compress if content exceeds threshold
	result := p.compressIfNeeded(generalFile.Content, req.CompressionThreshold, "HAProxyGeneralFile/"+name)

	spec := haproxyv1alpha1.HAProxyGeneralFileSpec{
		FileName:   generalFile.Filename,
		Path:       generalFile.Path,
		Content:    result.content,
		Checksum:   checksum,
		Compressed: result.compressed,
	}
	labels := runtimeConfigLabels(owner)
	annotations := runtimeConfigAnnotations(owner)
	ownerReferences := runtimeConfigOwnerRefs(owner)
	client := p.crdClient.HaproxyTemplateICV1alpha1().HAProxyGeneralFiles(req.TemplateConfigNamespace)

	return createOrUpdateAuxResource(ctx, auxResourceOps[*haproxyv1alpha1.HAProxyGeneralFile]{
		kind: "general file",
		get: func(ctx context.Context) (*haproxyv1alpha1.HAProxyGeneralFile, error) {
			return client.Get(ctx, name, metav1.GetOptions{})
		},
		create: func(ctx context.Context) (*haproxyv1alpha1.HAProxyGeneralFile, error) {
			return client.Create(ctx, &haproxyv1alpha1.HAProxyGeneralFile{
				ObjectMeta: metav1.ObjectMeta{
					Name:            name,
					Namespace:       req.TemplateConfigNamespace,
					Labels:          labels,
					Annotations:     annotations,
					OwnerReferences: ownerReferences,
				},
				Spec: spec,
			}, metav1.CreateOptions{})
		},
		upToDate: func(existing *haproxyv1alpha1.HAProxyGeneralFile) bool {
			return apiequality.Semantic.DeepEqual(existing.Spec, spec) &&
				auxiliaryMetadataEqual(existing, labels, annotations, ownerReferences)
		},
		managedByOwner: func(existing *haproxyv1alpha1.HAProxyGeneralFile) bool {
			return managedByRuntimeConfig(existing, owner.Name)
		},
		update: func(ctx context.Context, existing *haproxyv1alpha1.HAProxyGeneralFile) (*haproxyv1alpha1.HAProxyGeneralFile, error) {
			existing.Spec = spec
			existing.Labels = labels
			existing.Annotations = annotations
			existing.OwnerReferences = ownerReferences
			return client.Update(ctx, existing, metav1.UpdateOptions{})
		},
	})
}

// createOrUpdateCRTListFile creates or updates a HAProxyCRTListFile resource.
func (p *Publisher) createOrUpdateCRTListFile(ctx context.Context, req *PublishRequest, owner *haproxyv1alpha1.HAProxyCfg, crtListFile auxiliaryfiles.CRTListFile, name string) (string, error) {
	checksum := calculateChecksum(crtListFile.Content) // Checksum of original content

	// Compress if content exceeds threshold
	result := p.compressIfNeeded(crtListFile.Content, req.CompressionThreshold, "HAProxyCRTListFile/"+name)

	spec := haproxyv1alpha1.HAProxyCRTListFileSpec{
		ListName:   path.Base(crtListFile.Path),
		Path:       crtListFile.Path,
		Entries:    result.content,
		Checksum:   checksum,
		Compressed: result.compressed,
	}
	labels := runtimeConfigLabels(owner)
	annotations := runtimeConfigAnnotations(owner)
	ownerReferences := runtimeConfigOwnerRefs(owner)
	client := p.crdClient.HaproxyTemplateICV1alpha1().HAProxyCRTListFiles(req.TemplateConfigNamespace)

	return createOrUpdateAuxResource(ctx, auxResourceOps[*haproxyv1alpha1.HAProxyCRTListFile]{
		kind: "crt-list file",
		get: func(ctx context.Context) (*haproxyv1alpha1.HAProxyCRTListFile, error) {
			return client.Get(ctx, name, metav1.GetOptions{})
		},
		create: func(ctx context.Context) (*haproxyv1alpha1.HAProxyCRTListFile, error) {
			return client.Create(ctx, &haproxyv1alpha1.HAProxyCRTListFile{
				ObjectMeta: metav1.ObjectMeta{
					Name:            name,
					Namespace:       req.TemplateConfigNamespace,
					Labels:          labels,
					Annotations:     annotations,
					OwnerReferences: ownerReferences,
				},
				Spec: spec,
			}, metav1.CreateOptions{})
		},
		upToDate: func(existing *haproxyv1alpha1.HAProxyCRTListFile) bool {
			return apiequality.Semantic.DeepEqual(existing.Spec, spec) &&
				auxiliaryMetadataEqual(existing, labels, annotations, ownerReferences)
		},
		managedByOwner: func(existing *haproxyv1alpha1.HAProxyCRTListFile) bool {
			return managedByRuntimeConfig(existing, owner.Name)
		},
		update: func(ctx context.Context, existing *haproxyv1alpha1.HAProxyCRTListFile) (*haproxyv1alpha1.HAProxyCRTListFile, error) {
			existing.Spec = spec
			existing.Labels = labels
			existing.Annotations = annotations
			existing.OwnerReferences = ownerReferences
			return client.Update(ctx, existing, metav1.UpdateOptions{})
		},
	})
}
