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

//go:build e2e

package e2e

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/certificates"
	"gitlab.com/haproxy-haptic/haptic/pkg/transportsecurity/issuance"
	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

func TestAgentCertificateRenewal(t *testing.T) {
	feature := features.New("Automatic agent certificate renewal").Assess("the shipped renewal Job rotates a due CA and both identities without restarting peers", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		t.Helper()
		f := newAgentTLSRotation(ctx, t, cfg)
		pauseAgentCertificateRenewal(ctx, t, f.clientset)
		f.route(ctx, t, "before-renewal")
		oldServer, oldController := f.server.DeepCopy(), f.controller.DeepCopy()
		setAgentCertificateExpiry(ctx, t, f.clientset, false)
		runAgentCertificateRenewal(ctx, t, f.clientset)
		var err error
		f.server, err = f.clientset.CoreV1().Secrets(ControllerNamespace).Get(ctx, f.server.Name, metav1.GetOptions{})
		require.NoError(t, err)
		f.controller, err = f.clientset.CoreV1().Secrets(ControllerNamespace).Get(ctx, f.controller.Name, metav1.GetOptions{})
		require.NoError(t, err)
		require.NotEqual(t, oldServer.Data["ca.crt"], f.server.Data["ca.crt"])
		require.NotEqual(t, oldServer.Data["tls.key"], f.server.Data["tls.key"])
		require.NotEqual(t, oldController.Data["tls.key"], f.controller.Data["tls.key"])
		require.Equal(t, f.server.Data["ca.crt"], f.controller.Data["ca.crt"])
		require.NotEmpty(t, f.server.Data["previous-ca.crt"])
		deadline, err := time.Parse(time.RFC3339, string(f.server.Data["previous-ca-until"]))
		require.NoError(t, err)
		require.WithinDuration(t, time.Now().Add(time.Hour), deadline, 5*time.Minute)
		f.route(ctx, t, "during-renewal")
		f.waitMaterial(ctx, t, "tls.crt")
		f.waitMaterial(ctx, t, "ca.crt")
		f.route(ctx, t, "after-renewal")
		f.checkAgentAccess(ctx, t)
		runAgentCertificateRenewal(ctx, t, f.clientset)
		unchanged, err := f.clientset.CoreV1().Secrets(ControllerNamespace).Get(ctx, f.server.Name, metav1.GetOptions{})
		require.NoError(t, err)
		require.Equal(t, f.server.Data, unchanged.Data, "an immediate repeat must preserve the current generation")
		return ctx
	}).Feature()
	testEnv.Test(t, feature)
}

func pauseAgentCertificateRenewal(ctx context.Context, t *testing.T, client kubernetes.Interface) {
	t.Helper()
	jobs := client.BatchV1().CronJobs(ControllerNamespace)
	cron, err := jobs.Get(ctx, "haptic-agent-renewal", metav1.GetOptions{})
	require.NoError(t, err)
	original := cron.Spec.Suspend
	paused := true
	cron.Spec.Suspend = &paused
	_, err = jobs.Update(ctx, cron, metav1.UpdateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		current, err := jobs.Get(cleanupCtx, cron.Name, metav1.GetOptions{})
		require.NoError(t, err)
		current.Spec.Suspend = original
		_, err = jobs.Update(cleanupCtx, current, metav1.UpdateOptions{})
		require.NoError(t, err)
	})
	err = testutil.WaitForCondition(ctx, testutil.DefaultWaitConfig(), func(ctx context.Context) (bool, error) {
		current, err := jobs.Get(ctx, cron.Name, metav1.GetOptions{})
		return err == nil && len(current.Status.Active) == 0, err
	})
	require.NoError(t, err)
}

func runAgentCertificateRenewal(ctx context.Context, t *testing.T, client kubernetes.Interface) {
	t.Helper()
	cron, err := client.BatchV1().CronJobs(ControllerNamespace).Get(ctx, "haptic-agent-renewal", metav1.GetOptions{})
	require.NoError(t, err)
	job, err := client.BatchV1().Jobs(ControllerNamespace).Create(ctx, &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "agent-renewal-test-", Namespace: ControllerNamespace},
		Spec:       *cron.Spec.JobTemplate.Spec.DeepCopy(),
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancel()
		propagation := metav1.DeletePropagationBackground
		require.NoError(t, client.BatchV1().Jobs(ControllerNamespace).Delete(cleanupCtx, job.Name, metav1.DeleteOptions{PropagationPolicy: &propagation}))
	})
	err = testutil.WaitForCondition(ctx, testutil.DefaultWaitConfig(), func(ctx context.Context) (bool, error) {
		current, err := client.BatchV1().Jobs(ControllerNamespace).Get(ctx, job.Name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		for _, condition := range current.Status.Conditions {
			if condition.Type == batchv1.JobFailed && condition.Status == "True" {
				return false, fmt.Errorf("certificate renewal Job %s failed: %s", job.Name, condition.Reason)
			}
		}
		return current.Status.Succeeded == 1, nil
	})
	require.NoError(t, err)
}

type renewalFixture struct {
	Version    int              `json:"version"`
	Authority  issuance.KeyPair `json:"authority"`
	Identities []struct {
		certificates.Target
		Pair issuance.KeyPair `json:"pair"`
	} `json:"identities"`
}

// Keep the live CA key so the fixture only changes when renewal becomes due.
func setAgentCertificateExpiry(ctx context.Context, t *testing.T, client kubernetes.Interface, expired bool) {
	t.Helper()
	secret, err := client.CoreV1().Secrets(ControllerNamespace).Get(ctx, "haptic-agent-issuer", metav1.GetOptions{})
	require.NoError(t, err)
	var state renewalFixture
	require.NoError(t, json.Unmarshal(secret.Data["state.json"], &state))
	pair, err := tls.X509KeyPair(state.Authority.Certificate, state.Authority.PrivateKey)
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Second)
	if expired {
		now = now.Add(-3 * time.Hour)
	}
	root := pair.Leaf
	root.NotBefore = now.Add(-5 * time.Minute)
	root.NotAfter = now.Add(2 * time.Hour)
	signer, ok := pair.PrivateKey.(crypto.Signer)
	require.True(t, ok)
	der, err := x509.CreateCertificate(rand.Reader, root, root, signer.Public(), signer)
	require.NoError(t, err)
	state.Authority.Certificate = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	ca, err := issuance.ParseAuthority(state.Authority)
	require.NoError(t, err)
	for index := range state.Identities {
		identity := &state.Identities[index]
		identity.Pair, err = ca.Issue(identity.DNSName, identity.Usage, now, time.Hour)
		require.NoError(t, err)
	}
	secret.Data["state.json"], err = json.Marshal(state)
	require.NoError(t, err)
	_, err = client.CoreV1().Secrets(ControllerNamespace).Update(ctx, secret, metav1.UpdateOptions{})
	require.NoError(t, err)
	if expired {
		for _, identity := range state.Identities {
			target, err := client.CoreV1().Secrets(ControllerNamespace).Get(ctx, identity.SecretName, metav1.GetOptions{})
			require.NoError(t, err)
			target.Data = map[string][]byte{"tls.crt": identity.Pair.Certificate, "tls.key": identity.Pair.PrivateKey, "ca.crt": state.Authority.Certificate}
			_, err = client.CoreV1().Secrets(ControllerNamespace).Update(ctx, target, metav1.UpdateOptions{})
			require.NoError(t, err)
		}
	}
}

func TestAgentCertificateExpiryRecovery(t *testing.T) {
	feature := features.New("Expired agent identity recovery").Assess("Kubernetes credentials let renewal recover an expired CA and both expired identities", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		t.Helper()
		f := newAgentTLSRotation(ctx, t, cfg)
		pauseAgentCertificateRenewal(ctx, t, f.clientset)
		t.Cleanup(func() {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
			defer cancel()
			runAgentCertificateRenewal(cleanupCtx, t, f.clientset)
		})
		f.route(ctx, t, "before-expiry")
		setAgentCertificateExpiry(ctx, t, f.clientset, true)
		f.reloadIdentitySecrets(ctx, t)
		f.waitMaterial(ctx, t, "tls.crt")
		f.waitMaterial(ctx, t, "ca.crt")
		controllers, err := f.clientset.CoreV1().Pods(ControllerNamespace).List(ctx, metav1.ListOptions{LabelSelector: LabelSelectorController})
		require.NoError(t, err)
		agents, err := f.clientset.CoreV1().Pods(ControllerNamespace).List(ctx, metav1.ListOptions{LabelSelector: LabelSelectorHAProxy})
		require.NoError(t, err)
		require.NotEmpty(t, controllers.Items)
		require.NotEmpty(t, agents.Items)
		endpoint := "https://" + net.JoinHostPort(agents.Items[0].Status.PodIP, "5555")
		_, err = execInHAProxyPod(ctx, controllers.Items[0].Name, "controller", "haptic", "agent", "state", "--url", endpoint, "-o", "json")
		require.Error(t, err, "expired identities must reject authenticated requests")
		runAgentCertificateRenewal(ctx, t, f.clientset)
		f.reloadIdentitySecrets(ctx, t)
		require.NotContains(t, f.server.Data, "previous-ca.crt", "recovery must not restore an expired root")
		f.waitMaterial(ctx, t, "tls.crt")
		f.waitMaterial(ctx, t, "ca.crt")
		f.route(ctx, t, "expiry-recovered")
		f.checkAgentAccess(ctx, t)
		return ctx
	}).Feature()
	testEnv.Test(t, feature)
}

func (f *agentTLSRotation) reloadIdentitySecrets(ctx context.Context, t *testing.T) {
	t.Helper()
	var err error
	f.server, err = f.clientset.CoreV1().Secrets(ControllerNamespace).Get(ctx, f.server.Name, metav1.GetOptions{})
	require.NoError(t, err)
	f.controller, err = f.clientset.CoreV1().Secrets(ControllerNamespace).Get(ctx, f.controller.Name, metav1.GetOptions{})
	require.NoError(t, err)
}
