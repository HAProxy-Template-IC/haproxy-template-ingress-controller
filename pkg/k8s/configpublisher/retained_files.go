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
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderartifact"
)

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
		{"Secret", renderartifact.Certificate, refs.SSLCertificates},
		{"Secret", renderartifact.CA, refs.SSLCaFiles},
	}
	files := make(map[retainedFileKey]retainedFile)
	seen := make(map[string]bool)
	for _, group := range groups {
		for _, ref := range group.refs {
			file, err := p.readRetainedReference(ctx, owner, ref, group.kind, group.family, seen)
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

func (p *Publisher) readRetainedReference(ctx context.Context, owner *v1.HAProxyCfg, ref v1.ResourceReference, kind string, family renderartifact.Family, seen map[string]bool) (retainedFile, error) {
	if ref.Kind != kind || ref.Name == "" || ref.Namespace != owner.Namespace {
		return retainedFile{}, errors.New("retained auxiliary reference has a foreign kind or namespace")
	}
	if seen[ref.Kind+"/"+ref.Name] {
		return retainedFile{}, errors.New("retained auxiliary reference is repeated")
	}
	seen[ref.Kind+"/"+ref.Name] = true
	file, err := p.readRetainedFile(ctx, ref, family)
	if err != nil {
		return retainedFile{}, fmt.Errorf("reading %s %s/%s: %w", ref.Kind, ref.Namespace, ref.Name, err)
	}
	if err := verifyRetainedFile(&file, owner, ref.Kind); err != nil {
		return retainedFile{}, fmt.Errorf("%s %s/%s: %w", ref.Kind, ref.Namespace, ref.Name, err)
	}
	return file, nil
}

func (p *Publisher) readRetainedFile(ctx context.Context, ref v1.ResourceReference, family renderartifact.Family) (retainedFile, error) {
	client := p.crdClient.HaproxyTemplateICV1alpha1()
	switch family {
	case renderartifact.Map:
		item, err := client.HAProxyMapFiles(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
		if err != nil {
			return retainedFile{}, err
		}
		return retainedFile{meta: item, key: retainedFileKey{family, item.Spec.Path}, content: item.Spec.Entries, checksum: item.Spec.Checksum, compressed: item.Spec.Compressed, empty: item.Spec.Empty}, nil
	case renderartifact.General:
		item, err := client.HAProxyGeneralFiles(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
		if err != nil {
			return retainedFile{}, err
		}
		if item.Spec.CAFile {
			family = renderartifact.GeneralCA
		}
		return retainedFile{meta: item, key: retainedFileKey{family, item.Spec.Path}, name: item.Spec.FileName, content: item.Spec.Content, checksum: item.Spec.Checksum, compressed: item.Spec.Compressed, empty: item.Spec.Empty}, nil
	case renderartifact.CRTList:
		item, err := client.HAProxyCRTListFiles(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
		if err != nil {
			return retainedFile{}, err
		}
		return retainedFile{meta: item, key: retainedFileKey{family, item.Spec.Path}, content: item.Spec.Entries, checksum: item.Spec.Checksum, compressed: item.Spec.Compressed, empty: item.Spec.Empty}, nil
	case renderartifact.Certificate, renderartifact.CA:
		item, err := p.k8sClient.CoreV1().Secrets(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
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
	if !ownedByRuntimeConfig(file.meta, owner) || file.meta.GetDeletionTimestamp() != nil {
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
	suffix := AuxiliaryContentSuffix(kind, file.key.path, file.checksum, ca) + retainedSuffix(owner.Spec.Checksum, owner.Spec.RetainedPlan)
	if !strings.HasSuffix(file.meta.GetName(), suffix) {
		return errors.New("retained auxiliary content address differs")
	}
	file.content = content
	return nil
}
