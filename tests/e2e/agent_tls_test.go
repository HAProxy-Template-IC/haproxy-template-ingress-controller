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
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/e2e-framework/klient"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	agentclient "gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/transportsecurity"
	"gitlab.com/haproxy-haptic/haptic/pkg/transportsecurity/tlstest"
	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

type agentTLSRotation struct {
	client                             klient.Client
	clientset                          kubernetes.Interface
	namespace                          string
	serverName, clientName             string
	backend                            BackendRef
	server, controller                 *corev1.Secret
	ca                                 tlstest.Authority
	serverIdentity, controllerIdentity tlstest.Identity
	controllerUIDs                     map[string]bool
}

func TestAgentTLSRotation(t *testing.T) {
	feature := features.New("Agent mutual TLS rotation").Assess("updates work through staged trust, identity changes, and pod replacement", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		t.Helper()
		f := newAgentTLSRotation(ctx, t, cfg)
		pauseAgentCertificateRenewal(ctx, t, f.clientset)
		f.restoreIdentitiesAfterTest(ctx, t)
		f.route(ctx, t, "initial")
		until := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		for _, secret := range []*corev1.Secret{f.server, f.controller} {
			secret.Data["previous-ca.crt"] = secret.Data["ca.crt"]
			secret.Data["previous-ca-until"] = []byte(until)
			secret.Data["ca.crt"] = f.ca.PEM
			f.update(ctx, t, secret)
		}
		f.waitMaterial(ctx, t, "ca.crt")
		f.route(ctx, t, "trust")
		f.server.Data["tls.crt"], f.server.Data["tls.key"] = f.serverIdentity.Certificate, f.serverIdentity.Key
		f.update(ctx, t, f.server)
		f.route(ctx, t, "server")
		f.waitMaterial(ctx, t, "tls.crt")
		f.replaceAgent(ctx, t)
		f.route(ctx, t, "replacement")
		oldClient := f.oldControllerClient(ctx, t)
		_, err := oldClient.State(ctx, api.StateRead{})
		require.NoError(t, err)
		f.controller.Data["tls.crt"], f.controller.Data["tls.key"] = f.controllerIdentity.Certificate, f.controllerIdentity.Key
		f.update(ctx, t, f.controller)
		f.route(ctx, t, "controller")
		f.waitMaterial(ctx, t, "tls.crt")
		for _, secret := range []*corev1.Secret{f.server, f.controller} {
			delete(secret.Data, "previous-ca.crt")
			delete(secret.Data, "previous-ca-until")
			f.update(ctx, t, secret)
		}
		f.waitMaterial(ctx, t, "previous-ca.crt")
		_, err = oldClient.State(ctx, api.StateRead{})
		var rejected *agentclient.HTTPError
		require.ErrorAs(t, err, &rejected)
		require.Equal(t, http.StatusUnauthorized, rejected.Status, "revoked controller identity must not read agent state")
		f.route(ctx, t, "revoked")
		f.checkAgentAccess(ctx, t)
		return ctx
	}).Feature()
	testEnv.Test(t, feature)
}

func newAgentTLSRotation(ctx context.Context, t *testing.T, cfg *envconf.Config) *agentTLSRotation {
	t.Helper()
	client, err := cfg.NewClient()
	require.NoError(t, err)
	clientset, err := newClientsetForE2E(client.RESTConfig())
	require.NoError(t, err)
	f := &agentTLSRotation{client: client, clientset: clientset, namespace: NamespaceForTest(ctx, t, client), ca: tlstest.NewAuthority(t, "rotated agent CA")}
	DumpLogsOnFailure(t, f.namespace)
	f.backend = NewEchoServerBackend(ctx, t, client, f.namespace)
	controllers, err := clientset.CoreV1().Pods(ControllerNamespace).List(ctx, metav1.ListOptions{LabelSelector: LabelSelectorController})
	require.NoError(t, err)
	f.controllerUIDs = make(map[string]bool)
	for i := range controllers.Items {
		f.controllerUIDs[string(controllers.Items[i].UID)] = true
	}

	f.server, err = clientset.CoreV1().Secrets(ControllerNamespace).Get(ctx, "haptic-agent-tls", metav1.GetOptions{})
	require.NoError(t, err)
	f.controller, err = clientset.CoreV1().Secrets(ControllerNamespace).Get(ctx, "haptic-controller-tls", metav1.GetOptions{})
	require.NoError(t, err)
	f.serverName = certificateDNSName(t, f.server.Data["tls.crt"])
	f.clientName = certificateDNSName(t, f.controller.Data["tls.crt"])
	f.serverIdentity = tlstest.NewIdentity(t, f.ca, f.serverName, x509.ExtKeyUsageServerAuth)
	f.controllerIdentity = tlstest.NewIdentity(t, f.ca, f.clientName, x509.ExtKeyUsageClientAuth)
	return f
}

func (f *agentTLSRotation) update(ctx context.Context, t *testing.T, secret *corev1.Secret) {
	t.Helper()
	updated, err := f.clientset.CoreV1().Secrets(ControllerNamespace).Update(ctx, secret, metav1.UpdateOptions{})
	require.NoError(t, err)
	secret.ResourceVersion = updated.ResourceVersion
}

func (f *agentTLSRotation) route(ctx context.Context, t *testing.T, phase string) {
	t.Helper()
	host := phase + "-agent-tls.localdev.me"
	secretName := NewTLSSecret(ctx, t, f.client, f.namespace, phase, []string{host})
	NewIngress(ctx, t, f.client, f.namespace, &IngressSpec{Name: phase, Host: host,
		BackendService: f.backend.Service, BackendPort: f.backend.Port, TLSSecretName: secretName})
	httpclient.New(t).HTTPS(host, "/").ExpectOK(t)
	secret, err := f.clientset.CoreV1().Secrets(f.namespace).Get(ctx, secretName, metav1.GetOptions{})
	require.NoError(t, err)
	block, _ := pem.Decode(secret.Data["tls.crt"])
	require.NotNil(t, block)
	require.Equal(t, block.Bytes, servedCertificate(ctx, t, host))
	t.Logf("new route and frontend certificate deployed during %s", phase)
}

func (f *agentTLSRotation) waitMaterial(ctx context.Context, t *testing.T, file string) {
	t.Helper()
	wait := testutil.SlowWaitConfig()
	wait.Timeout = 3 * time.Minute
	err := testutil.WaitForConditionWithDescription(ctx, wait, "every mounted agent TLS revision contains "+file, func(ctx context.Context) (bool, error) {
		for _, role := range []struct {
			component, container string
			secret               *corev1.Secret
		}{
			{"controller", "controller", f.controller}, {"loadbalancer", "agent", f.server},
		} {
			ready, err := f.projectedMaterial(ctx, role.component, role.container, role.secret, file)
			if !ready {
				return false, err
			}
		}
		return true, nil
	})
	require.NoError(t, err)
}

func (f *agentTLSRotation) replaceAgent(ctx context.Context, t *testing.T) {
	t.Helper()
	pods, err := f.clientset.CoreV1().Pods(ControllerNamespace).List(ctx, metav1.ListOptions{LabelSelector: LabelSelectorHAProxy})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(pods.Items), 2)
	name := pods.Items[0].Name
	require.NoError(t, f.clientset.CoreV1().Pods(ControllerNamespace).Delete(ctx, name, metav1.DeleteOptions{}))
	err = testutil.WaitForConditionWithDescription(ctx, testutil.SlowWaitConfig(), "replacement HAProxy agent is ready", func(ctx context.Context) (bool, error) {
		ready, err := countReadyHAProxyPods(ctx, f.clientset)
		if err != nil || ready < 2 {
			return false, err
		}
		_, err = f.clientset.CoreV1().Pods(ControllerNamespace).Get(ctx, name, metav1.GetOptions{})
		return apierrors.IsNotFound(err), nil
	})
	require.NoError(t, err)
}

func (f *agentTLSRotation) oldControllerClient(ctx context.Context, t *testing.T) *agentclient.Client {
	t.Helper()
	service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "agent-tls-probe", Namespace: ControllerNamespace}, Spec: corev1.ServiceSpec{
		Selector: map[string]string{"app.kubernetes.io/component": "loadbalancer", "app.kubernetes.io/instance": HelmReleaseName},
		Ports:    []corev1.ServicePort{{Name: "agent", Port: 5555, TargetPort: intstr.FromInt32(5555)}},
	}}
	_, err := f.clientset.CoreV1().Services(ControllerNamespace).Create(ctx, service, metav1.CreateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, f.clientset.CoreV1().Services(ControllerNamespace).Delete(context.WithoutCancel(ctx), service.Name, metav1.DeleteOptions{}))
	})
	command, ports, err := startForwardTunnel(ctx, service.Name, []string{":5555"}, 1)
	require.NoError(t, err)
	t.Cleanup(func() { _ = command.Process.Kill(); _ = command.Wait() })
	directory := filepath.Join(t.TempDir(), "active")
	tlstest.Publish(t, directory, tlstest.Identity{Certificate: f.controller.Data["tls.crt"], Key: f.controller.Data["tls.key"]}, f.ca.PEM, nil, time.Time{})
	source, err := transportsecurity.NewSource(directory, f.serverName)
	require.NoError(t, err)
	client, err := agentclient.New(&agentclient.Config{BaseURL: "https://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(ports[0])), TLS: source})
	require.NoError(t, err)
	t.Cleanup(client.Close)
	return client
}

func (f *agentTLSRotation) checkAgentAccess(ctx context.Context, t *testing.T) {
	t.Helper()
	controllers, err := f.clientset.CoreV1().Pods(ControllerNamespace).List(ctx, metav1.ListOptions{LabelSelector: LabelSelectorController})
	require.NoError(t, err)
	require.NotEmpty(t, controllers.Items)
	require.Len(t, controllers.Items, len(f.controllerUIDs))
	for i := range controllers.Items {
		pod := &controllers.Items[i]
		require.True(t, f.controllerUIDs[string(pod.UID)], "certificate rotation replaced a controller pod")
		for j := range pod.Status.ContainerStatuses {
			require.Zero(t, pod.Status.ContainerStatuses[j].RestartCount)
		}
	}

	pods, err := f.clientset.CoreV1().Pods(ControllerNamespace).List(ctx, metav1.ListOptions{LabelSelector: LabelSelectorHAProxy})
	require.NoError(t, err)
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.DeletionTimestamp != nil {
			continue
		}
		for j := range pod.Status.InitContainerStatuses {
			status := &pod.Status.InitContainerStatuses[j]
			if status.Name == "agent" {
				require.Zero(t, status.RestartCount, "certificate changes must not restart the agent")
			}
		}
		endpoint := "https://" + net.JoinHostPort(pod.Status.PodIP, "5555")
		output, err := execInHAProxyPod(ctx, controllers.Items[0].Name, "controller", "haptic", "agent", "state", "--url", endpoint, "-o", "json")
		require.NoError(t, err)
		var state api.State
		require.NoError(t, json.Unmarshal([]byte(output), &state))
		require.NotEmpty(t, state.RunningPlanID)
		_, err = execInHAProxyPod(ctx, pod.Name, "agent", "haptic", "agent", "state", "--url", endpoint,
			"--tls-dir", "/etc/haptic/agent-tls/..data", "--tls-server-name", f.serverName)
		require.Error(t, err, "agent server identity must not impersonate a controller")
		_, err = execInHAProxyPod(ctx, pod.Name, "agent", "haptic", "agent", "state")
		require.NoError(t, err)
	}
}

func (f *agentTLSRotation) projectedMaterial(ctx context.Context, component, container string, secret *corev1.Secret, file string) (bool, error) {
	selector := "app.kubernetes.io/instance=" + HelmReleaseName + ",app.kubernetes.io/component=" + component
	pods, err := f.clientset.CoreV1().Pods(ControllerNamespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return false, err
	}
	count := 0
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.DeletionTimestamp != nil {
			continue
		}
		count++
		ready, err := podHasTLSMaterial(ctx, pod.Name, container, secret, file)
		if !ready {
			return false, err
		}
	}
	return count >= 2, nil
}

func podHasTLSMaterial(ctx context.Context, pod, container string, secret *corev1.Secret, file string) (bool, error) {
	path := "/etc/haptic/agent-tls/..data/" + file
	want, exists := secret.Data[file]
	if !exists {
		_, err := execInHAProxyPod(ctx, pod, container, "test", "!", "-e", path)
		return err == nil, err
	}
	got, err := execInHAProxyPod(ctx, pod, container, "cat", path)
	return err == nil && got == string(want), err
}

func certificateDNSName(t *testing.T, data []byte) string {
	t.Helper()
	block, _ := pem.Decode(data)
	require.NotNil(t, block)
	certificate, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)
	require.Len(t, certificate.DNSNames, 1)
	return certificate.DNSNames[0]
}

func (f *agentTLSRotation) restoreIdentitiesAfterTest(ctx context.Context, t *testing.T) {
	t.Helper()
	originals := []*corev1.Secret{f.server.DeepCopy(), f.controller.DeepCopy()}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Minute)
		defer cancel()
		for _, original := range originals {
			current, err := f.clientset.CoreV1().Secrets(ControllerNamespace).Get(cleanupCtx, original.Name, metav1.GetOptions{})
			require.NoError(t, err)
			current.Data = original.Data
			f.update(cleanupCtx, t, current)
			if original.Name == f.server.Name {
				f.server = current
			} else {
				f.controller = current
			}
		}
		f.waitMaterial(cleanupCtx, t, "tls.crt")
		f.waitMaterial(cleanupCtx, t, "ca.crt")
	})
}
