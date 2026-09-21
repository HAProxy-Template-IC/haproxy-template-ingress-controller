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
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/util/retry"
)

const (
	stateKey        = "state.json"
	ownerAnnotation = "haproxy-haptic.org/certificate-issuer"
)

type Manager struct {
	config  Config
	secrets corev1client.SecretInterface
	now     func() time.Time
}

func New(config *Config, client corev1client.CoreV1Interface) (*Manager, error) {
	if config == nil {
		return nil, errors.New("certificate configuration is required")
	}
	if err := config.validate(); err != nil {
		return nil, err
	}
	if client == nil {
		return nil, errors.New("certificate manager requires a Kubernetes client")
	}
	copied := *config
	copied.Targets = slices.Clone(config.Targets)
	return &Manager{config: copied, secrets: client.Secrets(config.Namespace), now: time.Now}, nil
}

// Renew persists the next generation before publishing any of its identities.
func (m *Manager) Renew(ctx context.Context) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	var result Result
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		return m.reconcile(ctx, &result)
	})
	return result, err
}

func (m *Manager) reconcile(ctx context.Context, result *Result) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	now := m.now().UTC().Truncate(time.Second)
	issuer, current, expires, err := m.load(ctx)
	if err != nil {
		return err
	}
	if issuer != nil {
		if err := m.checkOwnership(ctx, issuer); err != nil {
			return err
		}
	}
	if current == nil || !expires.After(now.Add(m.config.RenewBefore)) {
		next, err := newGeneration(&m.config, now, current)
		if err != nil {
			return err
		}
		issuer, err = m.persist(ctx, issuer, next)
		if err != nil {
			return err
		}
		result.Created = result.Created || current == nil
		result.Renewed = result.Renewed || current != nil
		current = next
		expires = now.Add(m.config.Lifetime)
	}
	for index := range current.Identities {
		identity := &current.Identities[index]
		if err := m.publish(ctx, issuer, identity, current.identityData(identity, now)); err != nil {
			return err
		}
	}
	result.ExpiresAt = expires
	return nil
}

func (m *Manager) load(ctx context.Context) (*corev1.Secret, *generation, time.Time, error) {
	secret, err := m.secrets.Get(ctx, m.config.IssuerSecret, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil, time.Time{}, m.checkBootstrap(ctx)
	}
	if err != nil {
		return nil, nil, time.Time{}, fmt.Errorf("read issuer Secret: %w", err)
	}
	if secret.Annotations[ownerAnnotation] != m.config.IssuerSecret {
		return nil, nil, time.Time{}, errors.New("issuer Secret belongs to another manager; choose an unused Secret name")
	}
	current, expires, err := readGeneration(secret.Data[stateKey], &m.config)
	return secret, current, expires, err
}

func (m *Manager) checkBootstrap(ctx context.Context) error {
	for _, target := range m.config.Targets {
		_, err := m.secrets.Get(ctx, target.SecretName, metav1.GetOptions{})
		if err == nil {
			return fmt.Errorf("issuer Secret is missing while identity Secret %s exists; restore the issuer Secret", target.SecretName)
		}
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("check identity Secret %s: %w", target.SecretName, err)
		}
	}
	return nil
}

func retryConcurrentCreation(err error, name string) error {
	if apierrors.IsAlreadyExists(err) {
		return apierrors.NewConflict(schema.GroupResource{Resource: "secrets"}, name, err)
	}
	return err
}
