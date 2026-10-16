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
	"errors"
	"fmt"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	v1 "github.com/haproxy-haptic/HAPTIC/pkg/apis/haproxytemplate/v1alpha1"
	"github.com/haproxy-haptic/HAPTIC/pkg/dataplane/renderartifact"
)

// retainedChildSuffix marks children shared by every checkpoint of one
// template. Checkpoints published before sharing own their children and name
// them with their own retainedSuffix instead.
const retainedChildSuffix = "-retained"

func retainedChildOwnership(req *PublishRequest) auxiliaryChildOwnership {
	return auxiliaryChildOwnership{
		labels:          map[string]string{retainedLabel: string(req.TemplateConfigUID)},
		annotations:     map[string]string{},
		ownerReferences: templateConfigOwnerRefs(req),
		manages: func(existing metav1.Object) bool {
			return ownedByRetainedTemplate(existing, req.TemplateConfigName, req.TemplateConfigUID)
		},
	}
}

func ownedByRetainedTemplate(obj metav1.Object, name string, uid types.UID) bool {
	return obj.GetLabels()[retainedLabel] == string(uid) && ownedByTemplate(obj, name, uid)
}

// pruneRetainedFiles deletes shared children that no checkpoint references.
func (p *Publisher) pruneRetainedFiles(ctx context.Context, req *PublishRequest) error {
	selector := retainedLabel + "=" + string(req.TemplateConfigUID)
	referenced, committed, err := p.retainedFileReferences(ctx, req, selector)
	if err != nil || !committed {
		return err
	}
	candidates, err := p.retainedFileCandidates(ctx, req.TemplateConfigNamespace, selector, retainedFileKinds)
	if err != nil {
		return err
	}
	for _, candidate := range candidates {
		if referenced[candidate.kind+"/"+candidate.obj.GetName()] ||
			!ownedByRetainedTemplate(candidate.obj, req.TemplateConfigName, req.TemplateConfigUID) {
			continue
		}
		err := candidate.remove(ctx, candidate.obj.GetName(), deletionOptions(candidate.obj))
		if err != nil && !apierrors.IsNotFound(err) && !apierrors.IsConflict(err) {
			return err
		}
	}
	return nil
}

// retainedFileReferences returns every child the template's checkpoints
// reference; committed is false while a checkpoint's references are unknown.
func (p *Publisher) retainedFileReferences(ctx context.Context, req *PublishRequest, selector string) (referenced map[string]bool, committed bool, err error) {
	checkpoints, err := p.crdClient.HaproxyTemplateICV1alpha1().HAProxyCfgs(req.TemplateConfigNamespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, false, err
	}
	referenced = make(map[string]bool)
	for i := range checkpoints.Items {
		item := &checkpoints.Items[i]
		if !ownedByTemplate(item, req.TemplateConfigName, req.TemplateConfigUID) || item.DeletionTimestamp != nil {
			continue
		}
		refs := item.Status.AuxiliaryFiles
		if refs == nil || refs.SetID != item.Annotations[AuxiliarySetIDAnnotationKey] {
			return nil, false, nil
		}
		for _, group := range [][]v1.ResourceReference{refs.MapFiles, refs.GeneralFiles, refs.CRTListFiles, refs.SSLCertificates, refs.SSLCaFiles} {
			for _, ref := range group {
				referenced[ref.Kind+"/"+ref.Name] = true
			}
		}
	}
	return referenced, true, nil
}

type retainedFileCandidate struct {
	kind   string
	obj    metav1.Object
	remove func(context.Context, string, metav1.DeleteOptions) error
}

var retainedFileKinds = []string{kindMapFile, kindGeneralFile, kindCRTListFile, kindSecret}

func (p *Publisher) retainedFileCandidates(ctx context.Context, namespace, selector string, kinds []string) ([]retainedFileCandidate, error) {
	var candidates []retainedFileCandidate
	for _, kind := range kinds {
		objects, remove, err := p.listRetainedKind(ctx, namespace, kind, selector)
		if err != nil {
			return nil, err
		}
		for _, obj := range objects {
			candidates = append(candidates, retainedFileCandidate{kind, obj, remove})
		}
	}
	return candidates, nil
}

func (p *Publisher) listRetainedKind(ctx context.Context, namespace, kind, selector string) ([]metav1.Object, func(context.Context, string, metav1.DeleteOptions) error, error) {
	crd := p.crdClient.HaproxyTemplateICV1alpha1()
	options := metav1.ListOptions{LabelSelector: selector}
	switch kind {
	case kindMapFile:
		items, err := crd.HAProxyMapFiles(namespace).List(ctx, options)
		if err != nil {
			return nil, nil, err
		}
		return objectsOf(items.Items), crd.HAProxyMapFiles(namespace).Delete, nil
	case kindGeneralFile:
		items, err := crd.HAProxyGeneralFiles(namespace).List(ctx, options)
		if err != nil {
			return nil, nil, err
		}
		return objectsOf(items.Items), crd.HAProxyGeneralFiles(namespace).Delete, nil
	case kindCRTListFile:
		items, err := crd.HAProxyCRTListFiles(namespace).List(ctx, options)
		if err != nil {
			return nil, nil, err
		}
		return objectsOf(items.Items), crd.HAProxyCRTListFiles(namespace).Delete, nil
	case kindSecret:
		items, err := p.k8sClient.CoreV1().Secrets(namespace).List(ctx, options)
		if err != nil {
			return nil, nil, err
		}
		return objectsOf(items.Items), p.k8sClient.CoreV1().Secrets(namespace).Delete, nil
	default:
		return nil, nil, fmt.Errorf("unsupported retained auxiliary kind %q", kind)
	}
}

func objectsOf[E any, P interface {
	*E
	metav1.Object
}](items []E) []metav1.Object {
	objects := make([]metav1.Object, len(items))
	for i := range items {
		objects[i] = P(&items[i])
	}
	return objects
}

// retainedListing holds a template's shared checkpoint children by kind/name.
// Its LISTs set no resourceVersion, so it is as current as one GET per child.
type retainedListing map[string]metav1.Object

func (p *Publisher) listRetainedChildren(ctx context.Context, namespace string, uid types.UID, kinds []string) (retainedListing, error) {
	candidates, err := p.retainedFileCandidates(ctx, namespace, retainedLabel+"="+string(uid), kinds)
	if err != nil {
		return nil, fmt.Errorf("listing shared retained auxiliary files: %w", err)
	}
	listing := make(retainedListing, len(candidates))
	for _, candidate := range candidates {
		listing[candidate.kind+"/"+candidate.obj.GetName()] = candidate.obj
	}
	return listing, nil
}

// listedOrGet reads a child from the listing; children it lacks, such as those
// owned by checkpoints published before sharing, are read live.
func listedOrGet[T metav1.Object](ctx context.Context, listing retainedListing, kind, name string, get func(context.Context, string, metav1.GetOptions) (T, error)) (T, error) {
	if item, found := listing[kind+"/"+name].(T); found {
		return item, nil
	}
	return get(ctx, name, metav1.GetOptions{})
}

// listedFirst answers a publication's first read of a shared child from the
// listing taken at its start; a child absent from it did not exist then.
// Retries read live, so a racing writer is seen as with a GET.
func listedFirst[T metav1.Object](listing retainedListing, kind, name string, get func(context.Context, string, metav1.GetOptions) (T, error)) func(context.Context) (T, error) {
	first := listing != nil
	return func(ctx context.Context) (T, error) {
		if !first {
			return get(ctx, name, metav1.GetOptions{})
		}
		first = false
		if item, found := listing[kind+"/"+name].(T); found {
			return item, nil
		}
		var absent T
		return absent, apierrors.NewNotFound(schema.GroupResource{Resource: kind}, name)
	}
}

type retainedFileKey struct {
	family renderartifact.Family
	path   string
}

type retainedFile struct {
	meta       metav1.Object
	key        retainedFileKey
	name       string
	content    string
	checksum   string
	compressed bool
	empty      bool
}

func (p *Publisher) readRetainedFiles(ctx context.Context, owner *v1.HAProxyCfg) (map[retainedFileKey]retainedFile, error) {
	refs := owner.Status.AuxiliaryFiles
	if refs == nil || !UsesContentIdentity(refs.SetID) {
		return nil, errors.New("retained configuration has no committed content-addressed auxiliary set")
	}
	groups := []struct {
		kind   string
		family renderartifact.Family
		refs   []v1.ResourceReference
	}{
		{kindMapFile, renderartifact.Map, refs.MapFiles},
		{kindGeneralFile, renderartifact.General, refs.GeneralFiles},
		{kindCRTListFile, renderartifact.CRTList, refs.CRTListFiles},
		{kindSecret, renderartifact.Certificate, refs.SSLCertificates},
		{kindSecret, renderartifact.CA, refs.SSLCaFiles},
	}
	var shared []string
	for _, group := range groups {
		if !slices.Contains(shared, group.kind) && slices.ContainsFunc(group.refs, func(ref v1.ResourceReference) bool {
			return strings.HasSuffix(ref.Name, retainedChildSuffix)
		}) {
			shared = append(shared, group.kind)
		}
	}
	var listing retainedListing
	if parent := metav1.GetControllerOf(owner); parent != nil && len(shared) > 0 {
		var err error
		if listing, err = p.listRetainedChildren(ctx, owner.Namespace, parent.UID, shared); err != nil {
			return nil, err
		}
	}
	files := make(map[retainedFileKey]retainedFile)
	seen := make(map[string]bool)
	for _, group := range groups {
		for _, ref := range group.refs {
			file, err := p.readRetainedReference(ctx, owner, ref, group.kind, group.family, seen, listing)
			if err != nil {
				return nil, err
			}
			if _, duplicate := files[file.key]; duplicate {
				return nil, errors.New("retained auxiliary set repeats a file path")
			}
			files[file.key] = file
		}
	}
	return files, nil
}

func (p *Publisher) readRetainedReference(ctx context.Context, owner *v1.HAProxyCfg, ref v1.ResourceReference, kind string, family renderartifact.Family, seen map[string]bool, listing retainedListing) (retainedFile, error) {
	if ref.Kind != kind || ref.Name == "" || ref.Namespace != owner.Namespace {
		return retainedFile{}, errors.New("retained auxiliary reference has a foreign kind or namespace")
	}
	if seen[ref.Kind+"/"+ref.Name] {
		return retainedFile{}, errors.New("retained auxiliary reference is repeated")
	}
	seen[ref.Kind+"/"+ref.Name] = true
	file, err := p.readRetainedFile(ctx, ref, family, listing)
	if err != nil {
		return retainedFile{}, fmt.Errorf("reading %s %s/%s: %w", ref.Kind, ref.Namespace, ref.Name, err)
	}
	if err := verifyRetainedFile(&file, owner, ref.Kind); err != nil {
		return retainedFile{}, fmt.Errorf("%s %s/%s: %w", ref.Kind, ref.Namespace, ref.Name, err)
	}
	return file, nil
}

func (p *Publisher) readRetainedFile(ctx context.Context, ref v1.ResourceReference, family renderartifact.Family, listing retainedListing) (retainedFile, error) {
	client := p.crdClient.HaproxyTemplateICV1alpha1()
	switch family {
	case renderartifact.Map:
		item, err := listedOrGet(ctx, listing, kindMapFile, ref.Name, client.HAProxyMapFiles(ref.Namespace).Get)
		if err != nil {
			return retainedFile{}, err
		}
		return retainedFile{meta: item, key: retainedFileKey{family, item.Spec.Path}, content: item.Spec.Entries, checksum: item.Spec.Checksum, compressed: item.Spec.Compressed, empty: item.Spec.Empty}, nil
	case renderartifact.General:
		item, err := listedOrGet(ctx, listing, kindGeneralFile, ref.Name, client.HAProxyGeneralFiles(ref.Namespace).Get)
		if err != nil {
			return retainedFile{}, err
		}
		if item.Spec.CAFile {
			family = renderartifact.GeneralCA
		}
		return retainedFile{meta: item, key: retainedFileKey{family, item.Spec.Path}, name: item.Spec.FileName, content: item.Spec.Content, checksum: item.Spec.Checksum, compressed: item.Spec.Compressed, empty: item.Spec.Empty}, nil
	case renderartifact.CRTList:
		item, err := listedOrGet(ctx, listing, kindCRTListFile, ref.Name, client.HAProxyCRTListFiles(ref.Namespace).Get)
		if err != nil {
			return retainedFile{}, err
		}
		return retainedFile{meta: item, key: retainedFileKey{family, item.Spec.Path}, content: item.Spec.Entries, checksum: item.Spec.Checksum, compressed: item.Spec.Compressed, empty: item.Spec.Empty}, nil
	case renderartifact.Certificate, renderartifact.CA:
		item, err := listedOrGet(ctx, listing, kindSecret, ref.Name, p.k8sClient.CoreV1().Secrets(ref.Namespace).Get)
		if err != nil {
			return retainedFile{}, err
		}
		return retainedSecretFile(item, family)
	default:
		return retainedFile{}, errors.New("unsupported retained auxiliary family")
	}
}

func retainedSecretFile(item *corev1.Secret, family renderartifact.Family) (retainedFile, error) {
	key, role := "certificate", "ssl-certificate"
	if family == renderartifact.CA {
		key, role = "ca", "ssl-ca"
	}
	filePath := item.Annotations[AuxiliaryPathAnnotationKey]
	content, present := item.Data[key]
	compressed := item.Annotations["haproxy-haptic.org/compressed"]
	if item.Type != corev1.SecretTypeOpaque || item.Labels["haproxy-haptic.org/type"] != role ||
		!present || filePath == "" || string(item.Data["path"]) != filePath || len(item.Data) != 2 || (compressed != "true" && compressed != "false") {
		return retainedFile{}, errors.New("retained certificate metadata differs from its payload")
	}
	return retainedFile{meta: item, key: retainedFileKey{family, filePath}, content: string(content), checksum: item.Annotations[AuxiliaryChecksumAnnotationKey], compressed: compressed == "true", empty: len(content) == 0}, nil
}

func verifyRetainedFile(file *retainedFile, owner *v1.HAProxyCfg, kind string) error {
	parent := metav1.GetControllerOf(owner)
	shared := parent != nil && ownedByRetainedTemplate(file.meta, parent.Name, parent.UID)
	if !shared && !ownedByRuntimeConfig(file.meta, owner) || file.meta.GetDeletionTimestamp() != nil {
		return errors.New("retained auxiliary object has a foreign owner or is being deleted")
	}
	if file.empty != (file.content == "") || (file.empty && file.compressed) {
		return errors.New("retained auxiliary content declaration is invalid")
	}
	content, err := decodeRetainedContent(file.content, file.compressed)
	if err != nil {
		return err
	}
	if file.key.path == "" || file.checksum != calculateChecksum(content) {
		return errors.New("retained auxiliary checksum differs from its content")
	}
	ca := file.key.family == renderartifact.CA || file.key.family == renderartifact.GeneralCA
	address := retainedSuffix(owner.Spec.Checksum, owner.Spec.RetainedPlan)
	if shared {
		address = retainedChildSuffix
	}
	suffix := AuxiliaryContentSuffix(kind, file.key.path, file.checksum, ca) + address
	if !strings.HasSuffix(file.meta.GetName(), suffix) {
		return errors.New("retained auxiliary content address differs")
	}
	file.content = content
	return nil
}
