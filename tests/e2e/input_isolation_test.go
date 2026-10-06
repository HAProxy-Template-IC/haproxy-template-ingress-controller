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
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/e2e-framework/klient"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

func TestInvalidSecretDoesNotBlockOtherInputs(t *testing.T) {
	var owner *testing.T
	const affectedHost = "isolated-certificate.localdev.me"
	const healthyHost = "isolated-healthy.localdev.me"
	const secretName = "isolated-certificate"
	var client klient.Client
	var clientset kubernetes.Interface
	var namespace string
	var certificate []byte
	var rejectedBefore float64
	feature := features.New("Invalid input isolation").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			t.Helper()
			owner = t
			var err error
			client, err = cfg.NewClient()
			if err != nil {
				t.Fatal(err)
			}
			clientset, err = newClientsetForE2E(client.RESTConfig())
			if err != nil {
				t.Fatal(err)
			}
			namespace = NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, namespace)
			backend := NewEchoServerBackend(ctx, t, client, namespace)
			NewTLSSecret(ctx, t, client, namespace, secretName, []string{affectedHost})
			NewIngress(ctx, t, client, namespace, &IngressSpec{Name: "affected", Host: affectedHost, BackendService: backend.Service, BackendPort: backend.Port, TLSSecretName: secretName})
			NewIngress(ctx, t, client, namespace, &IngressSpec{Name: "healthy", Host: healthyHost, BackendService: backend.Service, BackendPort: backend.Port})
			httpclient.New(t).HTTPS(affectedHost, "/").ExpectOK(t)
			httpclient.New(t).GET(healthyHost, "/").ExpectOK(t)
			certificate = servedCertificate(ctx, t, affectedHost)
			rejectedBefore = applyRejectedTotal(ctx, t, clientset)
			return ctx
		}).
		Assess("invalid certificate is held before deployment", func(ctx context.Context, t *testing.T, _ *envconf.Config) context.Context {
			t.Helper()
			corruptTLSSecret(ctx, t, client, namespace, secretName)
			waitForRejectedInputs(ctx, t, clientset, true)
			httpclient.New(t).HTTPS(affectedHost, "/").ExpectOK(t)
			if !bytes.Equal(certificate, servedCertificate(ctx, t, affectedHost)) {
				t.Fatal("rejected Secret changed the served certificate")
			}
			return ctx
		}).
		Assess("endpoints and unrelated admission continue while the Secret is invalid", func(ctx context.Context, t *testing.T, _ *envconf.Config) context.Context {
			t.Helper()
			next := NewNamedEchoServerBackend(ctx, owner, client, namespace, "replacement-backend")
			switchIsolationEndpoint(ctx, t, client, namespace, next, healthyHost)
			NewIngress(ctx, owner, client, namespace, &IngressSpec{Name: "unrelated", Host: "isolated-unrelated.localdev.me", BackendService: next.Service, BackendPort: next.Port})
			httpclient.New(t).GET("isolated-unrelated.localdev.me", "/").ExpectOK(t)
			denial := NewIngressExpectDenied(ctx, t, client, namespace, &IngressSpec{
				Name: "invalid", Host: "isolated-invalid.localdev.me", BackendService: next.Service, BackendPort: next.Port,
				Annotations: map[string]string{"haproxy.org/load-balance": "not-a-balancer"},
			})
			if !strings.Contains(denial.Error(), "denied") {
				t.Fatalf("expected a webhook denial, got %v", denial)
			}
			waitForRejectedInputs(ctx, t, clientset, true)
			if nacks := applyRejectedTotal(ctx, t, clientset) - rejectedBefore; nacks != 0 {
				t.Fatalf("invalid input reached the fleet: %v rejected applies", nacks)
			}
			return ctx
		}).
		Assess("cold controllers isolate the existing invalid Secret", func(ctx context.Context, t *testing.T, _ *envconf.Config) context.Context {
			t.Helper()
			restartInputIsolationControllers(ctx, t, clientset)
			waitForRejectedInputs(ctx, t, clientset, true)
			next := NewNamedEchoServerBackend(ctx, owner, client, namespace, "after-restart")
			switchIsolationEndpoint(ctx, t, client, namespace, next, healthyHost)
			NewIngress(ctx, owner, client, namespace, &IngressSpec{Name: "after-restart", Host: "isolated-restart.localdev.me", BackendService: next.Service, BackendPort: next.Port})
			httpclient.New(t).GET("isolated-restart.localdev.me", "/").ExpectOK(t)
			return ctx
		}).
		Assess("repair is applied automatically", func(ctx context.Context, t *testing.T, _ *envconf.Config) context.Context {
			t.Helper()
			repairTLSSecret(ctx, t, client, namespace, secretName, affectedHost)
			waitForRejectedInputs(ctx, t, clientset, false)
			err := testutil.WaitForCondition(ctx, testutil.DefaultWaitConfig(), func(ctx context.Context) (bool, error) {
				current, err := httpclient.New(t).PeerCertificate(ctx, affectedHost)
				return err == nil && current.VerifyHostname(affectedHost) == nil && !bytes.Equal(current.Raw, certificate), err
			})
			if err != nil {
				t.Fatalf("repaired certificate did not reach the fleet: %v", err)
			}
			httpclient.New(t).HTTPS(affectedHost, "/").ExpectOK(t)
			return ctx
		}).Feature()
	testEnv.Test(t, feature)
}

func waitForRejectedInputs(ctx context.Context, t *testing.T, clientset kubernetes.Interface, rejected bool) {
	t.Helper()
	err := testutil.WaitForConditionWithDescription(ctx, testutil.DefaultWaitConfig(), "watched input rejection state", func(ctx context.Context) (bool, error) {
		var total float64
		for pod := range controllerPodNames(ctx, t, clientset) {
			value, err := labelledMetricSum(ctx, clientset, pod, "haptic_rejected_watched_inputs")
			if err != nil {
				return false, err
			}
			total += value
		}
		if (total > 0) != rejected {
			return false, fmt.Errorf("rejected watched inputs: %v", total)
		}
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func restartInputIsolationControllers(ctx context.Context, t *testing.T, clientset kubernetes.Interface) {
	t.Helper()
	deployment, err := clientset.AppsV1().Deployments(ControllerNamespace).Get(ctx, ControllerDeploymentName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if deployment.Spec.Template.Annotations == nil {
		deployment.Spec.Template.Annotations = map[string]string{}
	}
	deployment.Spec.Template.Annotations["test.haproxy-haptic.org/input-isolation-restart"] = time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := clientset.AppsV1().Deployments(ControllerNamespace).Update(ctx, deployment, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := verifyControllerRollout(ctx, clientset); err != nil {
		t.Fatal(err)
	}
}

func switchIsolationEndpoint(ctx context.Context, t *testing.T, client klient.Client, namespace string, next BackendRef, host string) {
	t.Helper()
	service := &corev1.Service{}
	if err := client.Resources(namespace).Get(ctx, EchoServerBackend.Service, namespace, service); err != nil {
		t.Fatal(err)
	}
	service.Spec.Selector = map[string]string{"app": next.Service}
	if err := client.Resources(namespace).Update(ctx, service); err != nil {
		t.Fatal(err)
	}
	httpclient.New(t).GET(host, "/").ExpectMatching(t, "new EndpointSlice reaches the fleet", func(response *httpclient.Response) bool {
		return response.Status == http.StatusOK && response.Echo != nil && strings.HasPrefix(response.Echo.PodHostname, next.Service+"-")
	})
}
