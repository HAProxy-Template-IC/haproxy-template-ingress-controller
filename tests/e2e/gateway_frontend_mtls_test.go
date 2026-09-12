// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
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
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
	"k8s.io/klog/v2"
	"k8s.io/klog/v2/textlogger"
	streamhttp "k8s.io/streaming/pkg/httpstream"
	"sigs.k8s.io/e2e-framework/klient"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

type gatewayMTLSFixture struct {
	client              klient.Client
	clientset           kubernetes.Interface
	namespace           string
	host                string
	defaultBundle       *mTLSBundle
	overrideBundle      *mTLSBundle
	overrideCANamespace string
	defaultCert         tls.Certificate
	overrideCert        tls.Certificate
	wrongCert           tls.Certificate
	service             *corev1.Service
	pods                []corev1.Pod
	ports               map[string]int
}

func TestGatewayFrontendMTLSOverrides(t *testing.T) {
	feature := features.New("Gateway: frontend mTLS override lifecycle").
		Assess("CA loss and empty overrides preserve each listener's policy", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			t.Helper()
			fixture := newGatewayMTLSFixture(ctx, t, cfg)
			fixture.apply(ctx, t, "default-ca", "AllowInsecureFallback", true)
			fixture.wait(ctx, t, true, true)
			fixture.discover(ctx, t)
			fixture.checkStrictOverride(ctx, t)

			require.NoError(t, fixture.clientset.CoreV1().ConfigMaps(fixture.namespace).Delete(ctx, "override-ca", metav1.DeleteOptions{}))
			fixture.wait(ctx, t, true, false)
			fixture.checkBlockedOverride(ctx, t)

			fixture.createCA(ctx, t, "override-ca", fixture.overrideBundle.CACertPEM)
			fixture.wait(ctx, t, true, true)
			fixture.checkStrictOverride(ctx, t)

			fixture.apply(ctx, t, "default-ca", "AllowValidOnly", false)
			fixture.wait(ctx, t, true, true)
			fixture.checkEmptyOverride(ctx, t, true)

			fixture.apply(ctx, t, "missing-default-ca", "AllowValidOnly", false)
			fixture.wait(ctx, t, false, true)
			fixture.checkEmptyOverride(ctx, t, false)

			fixture.apply(ctx, t, "default-ca", "AllowInsecureFallback", true)
			fixture.wait(ctx, t, true, true)
			fixture.checkStrictOverride(ctx, t)
			return ctx
		}).Feature()
	testEnv.Test(t, feature)
}

func newGatewayMTLSFixture(ctx context.Context, t *testing.T, cfg *envconf.Config) *gatewayMTLSFixture {
	t.Helper()
	client, err := cfg.NewClient()
	require.NoError(t, err)
	clientset, err := newClientsetForE2E(client.RESTConfig())
	require.NoError(t, err)
	namespace := NamespaceForTest(ctx, t, client)
	// The host is per namespace: a predecessor's Gateway with the same host is
	// older and would keep winning it while its namespace is still terminating.
	host := namespace + ".localdev.me"
	defaultBundle, err := generateMTLSBundle(host)
	require.NoError(t, err)
	overrideBundle, err := generateMTLSBundle(host)
	require.NoError(t, err)
	fixture := &gatewayMTLSFixture{
		client: client, clientset: clientset, namespace: namespace, host: host,
		defaultBundle: defaultBundle, overrideBundle: overrideBundle,
	}
	DumpLogsOnFailure(t, fixture.namespace)
	fixture.defaultCert, err = tls.X509KeyPair(defaultBundle.ClientCertPEM, defaultBundle.ClientKeyPEM)
	require.NoError(t, err)
	fixture.overrideCert, err = tls.X509KeyPair(overrideBundle.ClientCertPEM, overrideBundle.ClientKeyPEM)
	require.NoError(t, err)
	fixture.wrongCert, err = tls.X509KeyPair(defaultBundle.WrongCertPEM, defaultBundle.WrongKeyPEM)
	require.NoError(t, err)
	fixture.createCA(ctx, t, "default-ca", defaultBundle.CACertPEM)
	fixture.createCA(ctx, t, "override-ca", overrideBundle.CACertPEM)
	_, err = clientset.CoreV1().Secrets(fixture.namespace).Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "server-tls", Namespace: fixture.namespace},
		Type:       corev1.SecretTypeTLS,
		Data:       map[string][]byte{"tls.crt": defaultBundle.ServerCertPEM, "tls.key": defaultBundle.ServerKeyPEM},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	backend := NewEchoServerBackend(ctx, t, client, fixture.namespace)
	NewHTTPRoute(ctx, t, fixture.namespace, &HTTPRouteSpec{
		Name: "echo-mtls", GatewayName: "mtls-overrides", Hostnames: []string{fixture.host},
		Rules: []HTTPRouteRule{{BackendRefs: []HTTPRouteBackendRef{{Service: backend.Service, Port: backend.Port}}}},
	})
	return fixture
}

func (f *gatewayMTLSFixture) createCA(ctx context.Context, t *testing.T, name string, pem []byte) {
	t.Helper()
	_, err := f.clientset.CoreV1().ConfigMaps(f.namespace).Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: f.namespace}, Data: map[string]string{"ca.crt": string(pem)},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
}

func (f *gatewayMTLSFixture) apply(ctx context.Context, t *testing.T, defaultCA, defaultMode string, validateOverride bool) {
	t.Helper()
	validation := func(name, mode string) map[string]any {
		return map[string]any{"validation": map[string]any{
			"mode": mode, "caCertificateRefs": []any{map[string]any{"group": "", "kind": "ConfigMap", "name": name}},
		}}
	}
	override := map[string]any{}
	if validateOverride {
		override = validation("override-ca", "AllowValidOnly")
		if f.overrideCANamespace != "" {
			references := override["validation"].(map[string]any)["caCertificateRefs"].([]any)
			references[0].(map[string]any)["namespace"] = f.overrideCANamespace
		}
	}
	listeners := make([]any, 0, 2)
	for _, listener := range []struct {
		name string
		port int
	}{{"default", 443}, {"override", 8443}} {
		listeners = append(listeners, map[string]any{
			"name": listener.name, "protocol": "HTTPS", "port": listener.port, "hostname": f.host,
			"tls": map[string]any{"mode": "Terminate", "certificateRefs": []any{map[string]any{"kind": "Secret", "name": "server-tls"}}},
		})
	}
	manifest, err := json.Marshal(map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1", "kind": "Gateway",
		"metadata": map[string]any{"name": "mtls-overrides", "namespace": f.namespace},
		"spec": map[string]any{
			"gatewayClassName": gatewayClassName, "listeners": listeners,
			"tls": map[string]any{"frontend": map[string]any{
				"default": validation(defaultCA, defaultMode), "perPort": []any{map[string]any{"port": 8443, "tls": override}},
			}},
		},
	})
	require.NoError(t, err)
	require.NoError(t, kubectlApplyStdin(ctx, manifest))
}

func (f *gatewayMTLSFixture) wait(ctx context.Context, t *testing.T, defaultReady, overrideReady bool) {
	t.Helper()
	want := map[string]bool{"default": defaultReady, "override": overrideReady}
	waitForResourceDeployed(ctx, t, f.client, gatewayGVR, f.namespace, "mtls-overrides",
		func(gateway *unstructured.Unstructured) (bool, string) {
			listeners, found, err := unstructured.NestedSlice(gateway.Object, "status", "listeners")
			if err != nil || !found || len(listeners) != len(want) {
				return false, "waiting for both listener statuses"
			}
			seen := map[string]bool{}
			for _, raw := range listeners {
				listener, ok := raw.(map[string]any)
				if !ok {
					return false, "malformed listener status"
				}
				name, _, _ := unstructured.NestedString(listener, "name")
				ready, expected := want[name]
				if !expected || seen[name] || !gatewayMTLSListenerConditions(listener, gateway.GetGeneration(), ready) {
					return false, fmt.Sprintf("listener %q has not reached the requested current-generation policy", name)
				}
				seen[name] = true
			}
			return len(seen) == len(want), "listener status incomplete"
		})
}

func gatewayMTLSListenerConditions(listener map[string]any, generation int64, ready bool) bool {
	return gatewayMTLSConditions(listener, generation, ready, []string{"Accepted", "ResolvedRefs", "Programmed"})
}

func gatewayMTLSConditions(object map[string]any, generation int64, ready bool, required []string) bool {
	conditions, found, err := unstructured.NestedSlice(object, "conditions")
	if err != nil || !found {
		return false
	}
	status := "False"
	if ready {
		status = "True"
	}
	seen := map[string]bool{}
	for _, typ := range required {
		seen[typ] = false
	}
	for _, raw := range conditions {
		condition, ok := raw.(map[string]any)
		if !ok {
			return false
		}
		typ, _, _ := unstructured.NestedString(condition, "type")
		if _, required := seen[typ]; !required {
			continue
		}
		actual, _, _ := unstructured.NestedString(condition, "status")
		observed, _, _ := unstructured.NestedInt64(condition, "observedGeneration")
		if actual != status || observed != generation || seen[typ] {
			return false
		}
		seen[typ] = true
	}
	for _, found := range seen {
		if !found {
			return false
		}
	}
	return len(required) > 0
}

func (f *gatewayMTLSFixture) discover(ctx context.Context, t *testing.T) {
	t.Helper()
	waitForResourceDeployed(ctx, t, f.client, httpRouteGVR, f.namespace, "echo-mtls",
		func(route *unstructured.Unstructured) (bool, string) {
			parents, found, err := unstructured.NestedSlice(route.Object, "status", "parents")
			if err != nil || !found || len(parents) != 1 {
				return false, "route parent status is absent"
			}
			parent, ok := parents[0].(map[string]any)
			if !ok {
				return false, "route parent status is malformed"
			}
			return gatewayMTLSConditions(parent, route.GetGeneration(), true, []string{"Accepted", "ResolvedRefs"}), "route is not accepted at its current generation"
		})
	services, err := f.clientset.CoreV1().Services(ControllerNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: "gateway.networking.k8s.io/gateway-name=mtls-overrides,gateway.networking.k8s.io/gateway-namespace=" + f.namespace,
	})
	require.NoError(t, err)
	require.Len(t, services.Items, 1)
	f.service = &services.Items[0]
	f.ports = map[string]int{}
	for _, port := range f.service.Spec.Ports {
		switch port.Port {
		case 443:
			f.ports["default"] = port.TargetPort.IntValue()
		case 8443:
			f.ports["override"] = port.TargetPort.IntValue()
		}
	}
	require.Len(t, f.ports, 2)
	require.Positive(t, f.ports["default"])
	require.Positive(t, f.ports["override"])
	require.NotEqual(t, f.ports["default"], f.ports["override"])
	require.NotEmpty(t, f.service.Spec.Selector)
	pods, err := f.clientset.CoreV1().Pods(ControllerNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(f.service.Spec.Selector).String(),
	})
	require.NoError(t, err)
	require.NotEmpty(t, pods.Items)
	f.pods = pods.Items
}

func (f *gatewayMTLSFixture) checkStrictOverride(ctx context.Context, t *testing.T) {
	t.Helper()
	f.assertFleet(ctx, t)
	for index := range f.pods {
		pod := &f.pods[index]
		f.assertBind(ctx, t, pod, "default", true)
		f.assertBind(ctx, t, pod, "override", true)
		f.assertOK(ctx, t, pod.Name, "default", nil)
		f.assertOK(ctx, t, pod.Name, "default", &f.wrongCert)
		f.assertOK(ctx, t, pod.Name, "override", &f.overrideCert)
		for _, certificate := range []*tls.Certificate{nil, &f.defaultCert, &f.wrongCert} {
			f.assertTLSRejected(ctx, t, pod.Name, "override", certificate)
		}
		f.assertOK(ctx, t, pod.Name, "default", nil)
	}
}

func (f *gatewayMTLSFixture) checkBlockedOverride(ctx context.Context, t *testing.T) {
	t.Helper()
	f.assertFleet(ctx, t)
	for index := range f.pods {
		pod := &f.pods[index]
		f.assertBind(ctx, t, pod, "default", true)
		f.assertBind(ctx, t, pod, "override", false)
		f.assertOK(ctx, t, pod.Name, "default", nil)
		f.assertUnbound(ctx, t, pod.Name, "override")
		f.assertOK(ctx, t, pod.Name, "default", nil)
	}
}

func (f *gatewayMTLSFixture) checkEmptyOverride(ctx context.Context, t *testing.T, defaultReady bool) {
	t.Helper()
	f.assertFleet(ctx, t)
	for index := range f.pods {
		pod := &f.pods[index]
		f.assertBind(ctx, t, pod, "default", defaultReady)
		f.assertBind(ctx, t, pod, "override", true)
		f.assertOK(ctx, t, pod.Name, "override", nil)
		if defaultReady {
			f.assertOK(ctx, t, pod.Name, "default", &f.defaultCert)
			f.assertTLSRejected(ctx, t, pod.Name, "default", nil)
		} else {
			f.assertUnbound(ctx, t, pod.Name, "default")
		}
		f.assertOK(ctx, t, pod.Name, "override", nil)
	}
}

func (f *gatewayMTLSFixture) assertFleet(ctx context.Context, t *testing.T) {
	t.Helper()
	service, err := f.clientset.CoreV1().Services(ControllerNamespace).Get(ctx, f.service.Name, metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, f.service.UID, service.UID)
	require.Equal(t, f.service.Spec.Ports, service.Spec.Ports)
	require.Equal(t, f.service.Spec.Selector, service.Spec.Selector)
	pods, err := f.clientset.CoreV1().Pods(ControllerNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: labels.SelectorFromSet(service.Spec.Selector).String(),
	})
	require.NoError(t, err)
	require.Len(t, pods.Items, len(f.pods))
	identities := map[string]string{}
	for index := range f.pods {
		pod := &f.pods[index]
		identities[pod.Name] = string(pod.UID)
	}
	for index := range pods.Items {
		pod := &pods.Items[index]
		require.Equal(t, identities[pod.Name], string(pod.UID))
	}
}

func (f *gatewayMTLSFixture) assertBind(ctx context.Context, t *testing.T, pod *corev1.Pod, listener string, present bool) {
	t.Helper()
	current, err := f.clientset.CoreV1().Pods(ControllerNamespace).Get(ctx, pod.Name, metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, pod.UID, current.UID)
	require.Nil(t, current.DeletionTimestamp)
	require.Len(t, current.Status.ContainerStatuses, len(current.Spec.Containers))
	for index := range current.Status.ContainerStatuses {
		container := &current.Status.ContainerStatuses[index]
		require.True(t, container.Ready, "%s/%s", pod.Name, container.Name)
		require.Zero(t, container.RestartCount, "%s/%s", pod.Name, container.Name)
	}
	config, err := readFileFromHAProxyPod(ctx, pod.Name, "/etc/haproxy/haproxy.cfg")
	require.NoError(t, err)
	require.NotEmpty(t, config)
	pattern := fmt.Sprintf(`(?m)^\s+bind \*:%d ssl crt-list \S+`, f.ports[listener])
	require.Equal(t, present, regexp.MustCompile(pattern).MatchString(config), "%s listener=%s port=%d", pod.Name, listener, f.ports[listener])
}

func (f *gatewayMTLSFixture) assertOK(ctx context.Context, t *testing.T, pod, listener string, certificate *tls.Certificate) {
	t.Helper()
	result := f.probe(ctx, t, pod, listener, certificate, false)
	require.NoError(t, result.requestError, "pod=%s listener=%s forward=%s", pod, listener, result.forwardErrors)
	require.Equal(t, http.StatusOK, result.status, "pod=%s listener=%s body=%s", pod, listener, result.body)
	require.NotEmpty(t, result.body)
	var echo map[string]any
	require.NoError(t, json.Unmarshal(result.body, &echo))
	method, _, err := unstructured.NestedString(echo, "http", "method")
	require.NoError(t, err)
	require.Equal(t, http.MethodGet, method)
	path, _, err := unstructured.NestedString(echo, "http", "originalUrl")
	require.NoError(t, err)
	require.Equal(t, "/mtls-override", path)
	host, _, err := unstructured.NestedString(echo, "request", "headers", "host")
	require.NoError(t, err)
	require.Equal(t, f.host, host)
}

func (f *gatewayMTLSFixture) assertTLSRejected(ctx context.Context, t *testing.T, pod, listener string, certificate *tls.Certificate) {
	t.Helper()
	result := f.probe(ctx, t, pod, listener, certificate, false)
	require.Error(t, result.requestError, "pod=%s listener=%s", pod, listener)
	require.Regexp(t, `remote error: tls: (certificate required|bad certificate|unknown certificate authority|unknown certificate)$`,
		result.requestError.Error(), "pod=%s listener=%s forward=%s", pod, listener, result.forwardErrors)
}

func (f *gatewayMTLSFixture) assertUnbound(ctx context.Context, t *testing.T, pod, listener string) {
	t.Helper()
	result := f.probe(ctx, t, pod, listener, &f.overrideCert, true)
	require.Error(t, result.requestError)
	require.Contains(t, result.forwardErrors, "connect: connection refused")
	require.Contains(t, result.forwardErrors, strconv.Itoa(f.ports[listener]))
}

type gatewayMTLSProbeResult struct {
	status        int
	body          []byte
	requestError  error
	forwardErrors string
}

type gatewayMTLSForwardErrors struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *gatewayMTLSForwardErrors) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

func (b *gatewayMTLSForwardErrors) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func (f *gatewayMTLSFixture) probe(ctx context.Context, t *testing.T, pod, listener string, certificate *tls.Certificate, awaitForwardClose bool) (result gatewayMTLSProbeResult) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	forwardErrors := &gatewayMTLSForwardErrors{}
	ready := make(chan struct{})
	forwarder := f.forwarder(ctx, t, pod, f.ports[listener], ready, forwardErrors)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := forwarder.ForwardPorts(); err != nil {
			fmt.Fprintln(forwardErrors, err)
		}
	}()
	defer func() {
		cancel()
		<-done
		result.forwardErrors = forwardErrors.String()
	}()
	select {
	case <-ready:
	case <-done:
		t.Fatalf("port forward ended before readiness: %s", forwardErrors)
	case <-ctx.Done():
		t.Fatalf("port forward readiness: %v", ctx.Err())
	}
	ports, err := forwarder.GetPorts()
	require.NoError(t, err)
	require.Len(t, ports, 1)
	result = f.request(ctx, t, ports[0].Local, certificate)
	if awaitForwardClose && result.requestError != nil {
		select {
		case <-done:
		case <-ctx.Done():
			t.Fatalf("port forward did not report its failure: %v; %s", ctx.Err(), forwardErrors)
		}
	}
	return result
}

func (f *gatewayMTLSFixture) request(ctx context.Context, t *testing.T, port uint16, certificate *tls.Certificate) gatewayMTLSProbeResult {
	t.Helper()
	result := gatewayMTLSProbeResult{}
	transport := &http.Transport{TLSClientConfig: f.clientTLS(t, certificate), DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("https://127.0.0.1:%d/mtls-override", port), http.NoBody)
	require.NoError(t, err)
	request.Host = f.host
	response, err := client.Do(request)
	result.requestError = err
	if err != nil {
		return result
	}
	defer response.Body.Close()
	result.status = response.StatusCode
	result.body, result.requestError = io.ReadAll(io.LimitReader(response.Body, 1<<20))
	return result
}

func (f *gatewayMTLSFixture) clientTLS(t *testing.T, certificate *tls.Certificate) *tls.Config {
	t.Helper()
	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM(f.defaultBundle.CACertPEM))
	config := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: f.host, RootCAs: roots}
	if certificate != nil {
		// Present the selected certificate even when its CA is not in CertificateRequest.
		config.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return certificate, nil }
	}
	return config
}

func (f *gatewayMTLSFixture) forwarder(ctx context.Context, t *testing.T, pod string, port int, ready chan struct{}, errors io.Writer) *portforward.PortForwarder {
	t.Helper()
	config := rest.CopyConfig(f.client.RESTConfig())
	config.Timeout = 15 * time.Second
	url := f.clientset.CoreV1().RESTClient().Post().Resource("pods").Namespace(ControllerNamespace).Name(pod).SubResource("portforward").URL()
	transport, upgrader, err := spdy.RoundTripperFor(config)
	require.NoError(t, err)
	spdyDialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport, Timeout: 15 * time.Second}, http.MethodPost, url)
	websocketDialer, err := portforward.NewSPDYOverWebsocketDialer(url, config)
	require.NoError(t, err)
	dialer := portforward.NewFallbackDialer(websocketDialer, spdyDialer, func(err error) bool {
		return streamhttp.IsUpgradeFailure(err) || streamhttp.IsHTTPSProxyError(err)
	})
	ctx = klog.NewContext(ctx, textlogger.NewLogger(textlogger.NewConfig(textlogger.Output(errors))))
	forwarder, err := portforward.NewOnAddressesWithContext(ctx, dialer, []string{"127.0.0.1"}, []string{"0:" + strconv.Itoa(port)}, ready, io.Discard, errors)
	require.NoError(t, err)
	return forwarder
}
