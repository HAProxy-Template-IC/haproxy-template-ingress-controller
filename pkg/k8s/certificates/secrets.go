// Copyright 2026 Philipp Hossner
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

package certificates

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func (m *Manager) persist(ctx context.Context, previous *corev1.Secret, next *generation) (*corev1.Secret, error) {
	data, err := json.Marshal(next)
	if err != nil {
		return nil, fmt.Errorf("encode certificate generation: %w", err)
	}
	if previous == nil {
		secret, err := m.secrets.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: m.config.IssuerSecret, Namespace: m.config.Namespace,
				Annotations: map[string]string{ownerAnnotation: m.config.IssuerSecret},
			},
			Type: corev1.SecretTypeOpaque, Data: map[string][]byte{stateKey: data},
		}, metav1.CreateOptions{})
		return secret, retryConcurrentCreation(err, m.config.IssuerSecret)
	}
	updated := previous.DeepCopy()
	updated.Data = map[string][]byte{stateKey: data}
	return m.secrets.Update(ctx, updated, metav1.UpdateOptions{})
}

func (m *Manager) publish(ctx context.Context, issuer *corev1.Secret, identity *identity, data map[string][]byte) error {
	secret, err := m.secrets.Get(ctx, identity.SecretName, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("read identity Secret %s: %w", identity.SecretName, err)
	}
	// Read the target before checking the generation so stale writers conflict.
	if currentErr := m.checkGeneration(ctx, issuer); currentErr != nil {
		return currentErr
	}
	if apierrors.IsNotFound(err) {
		_, err = m.secrets.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name: identity.SecretName, Namespace: m.config.Namespace,
				Annotations:     map[string]string{ownerAnnotation: issuer.Name},
				OwnerReferences: []metav1.OwnerReference{issuerReference(issuer)},
			},
			Type: corev1.SecretTypeTLS, Data: data,
		}, metav1.CreateOptions{})
		return retryConcurrentCreation(err, identity.SecretName)
	}
	if secret.Annotations[ownerAnnotation] != issuer.Name || !hasIssuerOwner(secret, issuer) {
		return fmt.Errorf("identity Secret %s belongs to another manager; choose an unused Secret name", identity.SecretName)
	}
	if secret.Type == corev1.SecretTypeTLS && maps.EqualFunc(secret.Data, data, bytes.Equal) {
		return nil
	}
	updated := secret.DeepCopy()
	updated.Type = corev1.SecretTypeTLS
	updated.Data = data
	_, err = m.secrets.Update(ctx, updated, metav1.UpdateOptions{})
	return err
}

func (m *Manager) checkGeneration(ctx context.Context, issuer *corev1.Secret) error {
	current, err := m.secrets.Get(ctx, issuer.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("check issuer generation: %w", err)
	}
	if current.UID != issuer.UID || current.ResourceVersion != issuer.ResourceVersion || !bytes.Equal(current.Data[stateKey], issuer.Data[stateKey]) {
		return apierrors.NewConflict(schema.GroupResource{Resource: "secrets"}, issuer.Name, errors.New("issuer generation changed"))
	}
	return nil
}

func issuerReference(issuer *corev1.Secret) metav1.OwnerReference {
	return metav1.OwnerReference{APIVersion: "v1", Kind: "Secret", Name: issuer.Name, UID: issuer.UID}
}

func hasIssuerOwner(secret, issuer *corev1.Secret) bool {
	expected := issuerReference(issuer)
	return slices.ContainsFunc(secret.OwnerReferences, func(owner metav1.OwnerReference) bool {
		return owner.APIVersion == expected.APIVersion && owner.Kind == expected.Kind && owner.Name == expected.Name && owner.UID == expected.UID
	})
}

func (m *Manager) checkOwnership(ctx context.Context, issuer *corev1.Secret) error {
	if issuer.Immutable != nil && *issuer.Immutable {
		return fmt.Errorf("issuer Secret %s is immutable; use a mutable Secret for automatic renewal", issuer.Name)
	}
	for _, target := range m.config.Targets {
		secret, err := m.secrets.Get(ctx, target.SecretName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read identity Secret %s: %w", target.SecretName, err)
		}
		if secret.Annotations[ownerAnnotation] != issuer.Name || !hasIssuerOwner(secret, issuer) {
			return fmt.Errorf("identity Secret %s belongs to another manager; choose an unused Secret name", target.SecretName)
		}
		if secret.Immutable != nil && *secret.Immutable {
			return fmt.Errorf("identity Secret %s is immutable; use a mutable Secret for automatic renewal", target.SecretName)
		}
	}
	return nil
}
