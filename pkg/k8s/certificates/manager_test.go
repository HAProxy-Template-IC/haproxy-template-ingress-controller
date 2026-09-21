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
	"crypto/x509"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
)

func testManager(tb testing.TB) (*Manager, *fake.Clientset, time.Time) {
	tb.Helper()
	client := fake.NewSimpleClientset()
	m, err := New(&Config{
		Namespace: "haptic", IssuerSecret: "issuer",
		Targets: []Target{
			{SecretName: "agent", DNSName: "agent.internal", Usage: x509.ExtKeyUsageServerAuth},
			{SecretName: "controller", DNSName: "controller.internal", Usage: x509.ExtKeyUsageClientAuth},
		},
		Lifetime: 72 * time.Hour, RenewBefore: 24 * time.Hour, Overlap: time.Hour,
	}, client.CoreV1())
	require.NoError(tb, err)
	now := time.Now().UTC().Truncate(time.Second)
	m.now = func() time.Time { return now }
	return m, client, now
}

func readSecret(tb testing.TB, client *fake.Clientset, name string) *corev1.Secret {
	tb.Helper()
	secret, err := client.CoreV1().Secrets("haptic").Get(tb.Context(), name, metav1.GetOptions{})
	require.NoError(tb, err)
	return secret
}

func TestManagerBootstrapAndRepair(t *testing.T) {
	m, client, now := testManager(t)
	result, err := m.Renew(t.Context())
	require.NoError(t, err)
	require.True(t, result.Created)
	require.False(t, result.Renewed)
	require.Equal(t, now.Add(72*time.Hour), result.ExpiresAt)
	issuer := readSecret(t, client, "issuer")
	agent := readSecret(t, client, "agent")
	controller := readSecret(t, client, "controller")
	require.Len(t, agent.Data, 3)
	require.Len(t, controller.Data, 3)
	require.Equal(t, agent.Data["ca.crt"], controller.Data["ca.crt"])
	require.NotEqual(t, agent.Data["tls.key"], controller.Data["tls.key"])
	require.NotContains(t, agent.Data, "ca.key")
	require.True(t, hasIssuerOwner(agent, issuer))

	client.ClearActions()
	result, err = m.Renew(t.Context())
	require.NoError(t, err)
	require.False(t, result.Created)
	require.False(t, result.Renewed)
	for _, action := range client.Actions() {
		require.Equal(t, "get", action.GetVerb())
	}
	require.Equal(t, agent.Data, readSecret(t, client, "agent").Data)
	require.NoError(t, client.CoreV1().Secrets("haptic").Delete(t.Context(), "agent", metav1.DeleteOptions{}))
	_, err = m.Renew(t.Context())
	require.NoError(t, err)
	require.Equal(t, agent.Data, readSecret(t, client, "agent").Data)
}

func TestManagerResumesInterruptedRotation(t *testing.T) {
	m, client, now := testManager(t)
	_, err := m.Renew(t.Context())
	require.NoError(t, err)
	oldAgent := readSecret(t, client, "agent")
	oldController := readSecret(t, client, "controller")
	now = now.Add(48 * time.Hour)
	m.now = func() time.Time { return now }
	interrupt := true
	client.PrependReactor("update", "secrets", func(action clienttesting.Action) (bool, runtime.Object, error) {
		secret := action.(clienttesting.UpdateAction).GetObject().(*corev1.Secret)
		if secret.Name == "controller" && interrupt {
			return true, nil, errors.New("simulated API interruption")
		}
		return false, nil, nil
	})
	_, err = m.Renew(t.Context())
	require.ErrorContains(t, err, "simulated API interruption")
	persisted := readSecret(t, client, "issuer")
	rotatedAgent := readSecret(t, client, "agent")
	require.NotEqual(t, oldAgent.Data["ca.crt"], rotatedAgent.Data["ca.crt"])
	require.Equal(t, oldAgent.Data["ca.crt"], rotatedAgent.Data["previous-ca.crt"])
	require.Equal(t, oldController.Data, readSecret(t, client, "controller").Data)

	interrupt = false
	restarted, err := New(&m.config, client.CoreV1())
	require.NoError(t, err)
	restarted.now = func() time.Time { return now.Add(time.Minute) }
	result, err := restarted.Renew(t.Context())
	require.NoError(t, err)
	require.False(t, result.Renewed)
	require.Equal(t, persisted.Data, readSecret(t, client, "issuer").Data)
	require.Equal(t, rotatedAgent.Data, readSecret(t, client, "agent").Data)
	require.Equal(t, rotatedAgent.Data["ca.crt"], readSecret(t, client, "controller").Data["ca.crt"])

	restarted.now = func() time.Time { return now.Add(2 * time.Hour) }
	_, err = restarted.Renew(t.Context())
	require.NoError(t, err)
	for _, name := range []string{"agent", "controller"} {
		secret := readSecret(t, client, name)
		require.NotContains(t, secret.Data, "previous-ca.crt")
		require.NotContains(t, secret.Data, "previous-ca-until")
		require.Equal(t, rotatedAgent.Data["ca.crt"], secret.Data["ca.crt"])
	}
}

func TestManagerRecoversAfterCAExpiry(t *testing.T) {
	m, client, now := testManager(t)
	_, err := m.Renew(t.Context())
	require.NoError(t, err)
	old := readSecret(t, client, "agent")
	m.now = func() time.Time { return now.Add(96 * time.Hour) }
	result, err := m.Renew(t.Context())
	require.NoError(t, err)
	require.True(t, result.Renewed)
	agent := readSecret(t, client, "agent")
	require.NotEqual(t, old.Data["ca.crt"], agent.Data["ca.crt"])
	require.NotContains(t, agent.Data, "previous-ca.crt")
	require.NotContains(t, agent.Data, "previous-ca-until")
}

func TestManagerRefusesUnownedOrIncompleteState(t *testing.T) {
	for _, secretName := range []string{"issuer", "agent", "controller"} {
		t.Run(secretName, func(t *testing.T) {
			m, client, _ := testManager(t)
			_, err := client.CoreV1().Secrets("haptic").Create(t.Context(), &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: "haptic"},
				Data:       map[string][]byte{"protected": []byte("do-not-replace")},
			}, metav1.CreateOptions{})
			require.NoError(t, err)
			client.ClearActions()
			_, err = m.Renew(t.Context())
			require.Error(t, err)
			for _, action := range client.Actions() {
				require.Equal(t, "get", action.GetVerb())
			}
			require.Equal(t, []byte("do-not-replace"), readSecret(t, client, secretName).Data["protected"])
		})
	}
}

func TestManagerRejectsForeignIdentityOwner(t *testing.T) {
	m, client, now := testManager(t)
	_, err := m.Renew(t.Context())
	require.NoError(t, err)
	agent := readSecret(t, client, "agent")
	agent.OwnerReferences[0].UID = types.UID("different-issuer")
	agent.Data["tls.crt"] = []byte("owned-by-someone-else")
	_, err = client.CoreV1().Secrets("haptic").Update(t.Context(), agent, metav1.UpdateOptions{})
	require.NoError(t, err)
	m.now = func() time.Time { return now.Add(48 * time.Hour) }
	client.ClearActions()
	_, err = m.Renew(t.Context())
	require.ErrorContains(t, err, "belongs to another manager")
	for _, action := range client.Actions() {
		require.Equal(t, "get", action.GetVerb())
	}
	require.Equal(t, agent.Data, readSecret(t, client, "agent").Data)
}

func TestManagerRetriesIssuerConflict(t *testing.T) {
	m, client, now := testManager(t)
	_, err := m.Renew(t.Context())
	require.NoError(t, err)
	m.now = func() time.Time { return now.Add(48 * time.Hour) }
	conflicts := 0
	client.PrependReactor("update", "secrets", func(action clienttesting.Action) (bool, runtime.Object, error) {
		secret := action.(clienttesting.UpdateAction).GetObject().(*corev1.Secret)
		if secret.Name == "issuer" && conflicts == 0 {
			conflicts++
			return true, nil, apierrors.NewConflict(schema.GroupResource{Resource: "secrets"}, "issuer", errors.New("concurrent generation"))
		}
		return false, nil, nil
	})
	result, err := m.Renew(t.Context())
	require.NoError(t, err)
	require.True(t, result.Renewed)
	require.Equal(t, 1, conflicts)
	agent := readSecret(t, client, "agent")
	require.Equal(t, agent.Data["ca.crt"], readSecret(t, client, "controller").Data["ca.crt"])
}

func TestManagerCancellationAndChangedIdentity(t *testing.T) {
	m, client, _ := testManager(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := m.Renew(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, client.Actions())
	_, err = m.Renew(t.Context())
	require.NoError(t, err)
	before := readSecret(t, client, "issuer")
	m.config.Targets[0].DNSName = "changed.internal"
	_, err = m.Renew(t.Context())
	require.ErrorContains(t, err, "names or usages changed")
	require.Equal(t, before.Data, readSecret(t, client, "issuer").Data)
}

func TestManagerRefusesCorruptStateWithoutMutation(t *testing.T) {
	for _, corrupt := range []string{"not-json", `{}`, `{"version":1,"unknown":true}`} {
		t.Run(corrupt, func(t *testing.T) {
			m, client, now := testManager(t)
			_, err := m.Renew(t.Context())
			require.NoError(t, err)
			issuer := readSecret(t, client, "issuer")
			issuer.Data[stateKey] = []byte(corrupt)
			_, err = client.CoreV1().Secrets("haptic").Update(t.Context(), issuer, metav1.UpdateOptions{})
			require.NoError(t, err)
			m.now = func() time.Time { return now.Add(48 * time.Hour) }
			client.ClearActions()
			_, err = m.Renew(t.Context())
			require.Error(t, err)
			for _, action := range client.Actions() {
				require.Equal(t, "get", action.GetVerb())
			}
		})
	}
}

func TestManagerRefusesImmutableSecretsBeforeRotation(t *testing.T) {
	for _, name := range []string{"issuer", "agent", "controller"} {
		t.Run(name, func(t *testing.T) {
			m, client, now := testManager(t)
			_, err := m.Renew(t.Context())
			require.NoError(t, err)
			secret := readSecret(t, client, name)
			immutable := true
			secret.Immutable = &immutable
			_, err = client.CoreV1().Secrets("haptic").Update(t.Context(), secret, metav1.UpdateOptions{})
			require.NoError(t, err)
			m.now = func() time.Time { return now.Add(48 * time.Hour) }
			client.ClearActions()
			_, err = m.Renew(t.Context())
			require.ErrorContains(t, err, "immutable")
			for _, action := range client.Actions() {
				require.Equal(t, "get", action.GetVerb())
			}
		})
	}
}

func TestManagerDoesNotPublishAnObsoleteGeneration(t *testing.T) {
	m, client, now := testManager(t)
	_, err := m.Renew(t.Context())
	require.NoError(t, err)
	oldIssuer := readSecret(t, client, "issuer")
	old, _, err := readGeneration(oldIssuer.Data[stateKey], &m.config)
	require.NoError(t, err)
	m.now = func() time.Time { return now.Add(48 * time.Hour) }
	_, err = m.Renew(t.Context())
	require.NoError(t, err)
	newAgent := readSecret(t, client, "agent")
	client.ClearActions()
	err = m.publish(t.Context(), oldIssuer, &old.Identities[0], old.identityData(&old.Identities[0], now))
	require.True(t, apierrors.IsConflict(err))
	for _, action := range client.Actions() {
		require.Equal(t, "get", action.GetVerb())
	}
	require.Equal(t, newAgent.Data, readSecret(t, client, "agent").Data)
}

func TestManagerAdoptsConcurrentBootstrap(t *testing.T) {
	m, client, now := testManager(t)
	concurrent, err := newGeneration(&m.config, now, nil)
	require.NoError(t, err)
	data, err := json.Marshal(concurrent)
	require.NoError(t, err)
	inserted := false
	client.PrependReactor("create", "secrets", func(action clienttesting.Action) (bool, runtime.Object, error) {
		secret := action.(clienttesting.CreateAction).GetObject().(*corev1.Secret)
		if secret.Name != "issuer" || inserted {
			return false, nil, nil
		}
		inserted = true
		competing := secret.DeepCopy()
		competing.Data[stateKey] = data
		require.NoError(t, client.Tracker().Create(corev1.SchemeGroupVersion.WithResource("secrets"), competing, "haptic"))
		return true, nil, apierrors.NewAlreadyExists(schema.GroupResource{Resource: "secrets"}, "issuer")
	})
	_, err = m.Renew(t.Context())
	require.NoError(t, err)
	require.True(t, inserted)
	require.Equal(t, concurrent.Authority.Certificate, readSecret(t, client, "agent").Data["ca.crt"])
	require.Equal(t, concurrent.Identities[1].Pair.Certificate, readSecret(t, client, "controller").Data["tls.crt"])
}

func TestManagerRejectsCorruptTransitionBeforePublication(t *testing.T) {
	for _, corrupt := range []struct {
		name   string
		change func(*generation)
	}{
		{name: "invalid previous CA", change: func(g *generation) { g.PreviousCA = []byte("not a certificate") }},
		{name: "extended deadline", change: func(g *generation) { g.PreviousUntil = g.PreviousUntil.Add(time.Hour) }},
		{name: "unbounded deadline", change: func(g *generation) { g.PreviousUntil = g.PreviousUntil.Add(48 * time.Hour) }},
		{name: "missing previous CA", change: func(g *generation) { g.PreviousCA = nil }},
		{name: "missing deadline", change: func(g *generation) { g.PreviousUntil = time.Time{} }},
	} {
		t.Run(corrupt.name, func(t *testing.T) {
			m, client, now := testManager(t)
			_, err := m.Renew(t.Context())
			require.NoError(t, err)
			now = now.Add(48 * time.Hour)
			m.now = func() time.Time { return now }
			_, err = m.Renew(t.Context())
			require.NoError(t, err)
			secret := readSecret(t, client, "issuer")
			state, _, err := readGeneration(secret.Data[stateKey], &m.config)
			require.NoError(t, err)
			corrupt.change(state)
			secret.Data[stateKey], err = json.Marshal(state)
			require.NoError(t, err)
			_, err = client.CoreV1().Secrets("haptic").Update(t.Context(), secret, metav1.UpdateOptions{})
			require.NoError(t, err)
			client.ClearActions()
			_, err = m.Renew(t.Context())
			require.Error(t, err)
			for _, action := range client.Actions() {
				require.Equal(t, "get", action.GetVerb())
			}
		})
	}
}
