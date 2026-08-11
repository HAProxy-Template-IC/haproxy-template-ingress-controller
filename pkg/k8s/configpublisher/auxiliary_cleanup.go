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

	haproxyv1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

func desiredResourceNames(names []string) map[string]struct{} {
	desired := make(map[string]struct{}, len(names))
	for _, name := range names {
		desired[name] = struct{}{}
	}
	return desired
}

func referencesRuntimeConfig(obj metav1.Object, runtimeConfigName string) bool {
	for _, ref := range obj.GetOwnerReferences() {
		if ref.APIVersion == apiVersionV1Alpha1 && ref.Kind == runtimeConfigKind && ref.Name == runtimeConfigName {
			return true
		}
	}
	return false
}

func managedByRuntimeConfig(obj metav1.Object, runtimeConfigName string) bool {
	for _, ref := range obj.GetOwnerReferences() {
		if ref.APIVersion != apiVersionV1Alpha1 || ref.Kind != runtimeConfigKind {
			continue
		}
		if ref.Name == runtimeConfigName {
			return true
		}
		return false
	}
	return len(obj.GetOwnerReferences()) == 0 &&
		obj.GetLabels()[runtimeConfigLabelKey] == runtimeConfigLabelValue(runtimeConfigName)
}

func ownedByRuntimeConfig(obj metav1.Object, runtimeConfigName string) bool {
	return obj.GetLabels()[runtimeConfigLabelKey] == runtimeConfigLabelValue(runtimeConfigName) &&
		referencesRuntimeConfig(obj, runtimeConfigName)
}

func pruneOwnedResources[T any](
	ctx context.Context,
	runtimeConfig *haproxyv1alpha1.HAProxyCfg,
	kind, description string,
	desiredNames []string,
	items []T,
	metadata func(*T) metav1.Object,
	publicationCurrent func(context.Context) error,
	deleteResource func(context.Context, string, metav1.DeleteOptions) error,
) error {
	desired := desiredResourceNames(desiredNames)
	for i := range items {
		obj := metadata(&items[i])
		if _, keep := desired[obj.GetName()]; keep || !ownedByRuntimeConfig(obj, runtimeConfig.Name) {
			continue
		}
		if err := publicationCurrent(ctx); err != nil {
			return cleanupError(runtimeConfig, kind, obj.GetName(), err)
		}
		if err := deleteResource(ctx, obj.GetName(), deletionOptions(obj)); err != nil && !apierrors.IsNotFound(err) {
			return cleanupError(runtimeConfig, kind, obj.GetName(), fmt.Errorf("deleting stale %s: %w", description, err))
		}
	}
	return nil
}

func deletionOptions(obj metav1.Object) metav1.DeleteOptions {
	preconditions := &metav1.Preconditions{}
	hasPrecondition := false
	if uid := obj.GetUID(); uid != "" {
		preconditions.UID = &uid
		hasPrecondition = true
	}
	if resourceVersion := obj.GetResourceVersion(); resourceVersion != "" {
		preconditions.ResourceVersion = &resourceVersion
		hasPrecondition = true
	}
	if !hasPrecondition {
		return metav1.DeleteOptions{}
	}
	return metav1.DeleteOptions{Preconditions: preconditions}
}

func (p *Publisher) ensurePublicationCurrent(
	ctx context.Context,
	runtimeConfig *haproxyv1alpha1.HAProxyCfg,
	expectedReferences *haproxyv1alpha1.AuxiliaryFileReferences,
) error {
	current, err := p.crdClient.HaproxyTemplateICV1alpha1().HAProxyCfgs(runtimeConfig.Namespace).
		Get(ctx, runtimeConfig.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("checking publication authority: %w", err)
	}
	want := runtimeConfig.Annotations[AuxiliarySetIDAnnotationKey]
	if current.Annotations[AuxiliarySetIDAnnotationKey] != want {
		return fmt.Errorf("publication %q was superseded; skip stale cleanup", want)
	}
	if !auxiliaryRefsEqual(current.Status.AuxiliaryFiles, expectedReferences) {
		return fmt.Errorf("publication %q no longer owns the committed auxiliary references; skip stale cleanup", want)
	}
	return nil
}

func (p *Publisher) pruneAuxiliaryFiles(ctx context.Context, runtimeConfig *haproxyv1alpha1.HAProxyCfg, result *PublishResult) error {
	namespace := runtimeConfig.Namespace
	ownerName := runtimeConfig.Name
	expectedReferences := buildAuxiliaryFileReferences(
		namespace,
		result,
		runtimeConfig.Annotations[AuxiliarySetIDAnnotationKey],
	)
	publicationCurrent := func(ctx context.Context) error {
		return p.ensurePublicationCurrent(ctx, runtimeConfig, expectedReferences)
	}
	if err := publicationCurrent(ctx); err != nil {
		return cleanupError(runtimeConfig, runtimeConfigKind, ownerName, err)
	}
	listOptions := metav1.ListOptions{LabelSelector: labels.Set{
		runtimeConfigLabelKey: runtimeConfigLabelValue(ownerName),
	}.AsSelector().String()}

	mapFiles, err := p.crdClient.HaproxyTemplateICV1alpha1().HAProxyMapFiles(namespace).
		List(ctx, listOptions)
	if err != nil {
		return cleanupError(runtimeConfig, "HAProxyMapFile", ownerName, fmt.Errorf("listing map files: %w", err))
	}
	if err := pruneOwnedResources(ctx, runtimeConfig, "HAProxyMapFile", "map file", result.MapFileNames, mapFiles.Items,
		func(file *haproxyv1alpha1.HAProxyMapFile) metav1.Object { return file },
		publicationCurrent,
		func(ctx context.Context, name string, options metav1.DeleteOptions) error {
			return p.crdClient.HaproxyTemplateICV1alpha1().HAProxyMapFiles(namespace).
				Delete(ctx, name, options)
		}); err != nil {
		return err
	}

	secrets, err := p.k8sClient.CoreV1().Secrets(namespace).List(ctx, listOptions)
	if err != nil {
		return cleanupError(runtimeConfig, "Secret", ownerName, fmt.Errorf("listing certificate secrets: %w", err))
	}
	if err := pruneOwnedResources(ctx, runtimeConfig, "Secret", "certificate secret", result.SecretNames, secrets.Items,
		func(secret *corev1.Secret) metav1.Object { return secret },
		publicationCurrent,
		func(ctx context.Context, name string, options metav1.DeleteOptions) error {
			return p.k8sClient.CoreV1().Secrets(namespace).Delete(ctx, name, options)
		}); err != nil {
		return err
	}

	generalFiles, err := p.crdClient.HaproxyTemplateICV1alpha1().HAProxyGeneralFiles(namespace).
		List(ctx, listOptions)
	if err != nil {
		return cleanupError(runtimeConfig, "HAProxyGeneralFile", ownerName, fmt.Errorf("listing general files: %w", err))
	}
	if err := pruneOwnedResources(ctx, runtimeConfig, "HAProxyGeneralFile", "general file", result.GeneralFileNames, generalFiles.Items,
		func(file *haproxyv1alpha1.HAProxyGeneralFile) metav1.Object { return file },
		publicationCurrent,
		func(ctx context.Context, name string, options metav1.DeleteOptions) error {
			return p.crdClient.HaproxyTemplateICV1alpha1().HAProxyGeneralFiles(namespace).
				Delete(ctx, name, options)
		}); err != nil {
		return err
	}

	crtListFiles, err := p.crdClient.HaproxyTemplateICV1alpha1().HAProxyCRTListFiles(namespace).
		List(ctx, listOptions)
	if err != nil {
		return cleanupError(runtimeConfig, "HAProxyCRTListFile", ownerName, fmt.Errorf("listing crt-list files: %w", err))
	}
	if err := pruneOwnedResources(ctx, runtimeConfig, "HAProxyCRTListFile", "crt-list file", result.CRTListFileNames, crtListFiles.Items,
		func(file *haproxyv1alpha1.HAProxyCRTListFile) metav1.Object { return file },
		publicationCurrent,
		func(ctx context.Context, name string, options metav1.DeleteOptions) error {
			return p.crdClient.HaproxyTemplateICV1alpha1().HAProxyCRTListFiles(namespace).
				Delete(ctx, name, options)
		}); err != nil {
		return err
	}

	return nil
}

func cleanupError(runtimeConfig *haproxyv1alpha1.HAProxyCfg, kind, name string, err error) error {
	return incompletePublicationError(
		PublicationStageCleanup,
		runtimeConfig.Namespace,
		runtimeConfig.Name,
		kind,
		name,
		err,
	)
}
