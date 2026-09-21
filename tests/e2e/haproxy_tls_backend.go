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

//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/e2e-framework/klient"
)

// HAProxyTLSBackend exposes a private CA and optional client credentials.
type HAProxyTLSBackend struct {
	HTTPS                BackendRef
	CASecretName         string
	ClientCertSecretName string
}

func NewHAProxyTLSBackend(ctx context.Context, t *testing.T, client klient.Client, namespace string, upstream BackendRef, sniHost string) HAProxyTLSBackend {
	t.Helper()
	return newHAProxyTLSBackend(ctx, t, client, namespace, upstream, sniHost, false)
}

func NewHAProxyMTLSBackend(ctx context.Context, t *testing.T, client klient.Client, namespace string, upstream BackendRef, sniHost string) HAProxyTLSBackend {
	t.Helper()
	return newHAProxyTLSBackend(ctx, t, client, namespace, upstream, sniHost, true)
}

func newHAProxyTLSBackend(ctx context.Context, t *testing.T, client klient.Client, namespace string, upstream BackendRef, sniHost string, requireClientCertificate bool) HAProxyTLSBackend {
	t.Helper()

	// Generate a CA + server cert + client cert in one bundle. The
	// existing generateMTLSBundle helper (webhook_certs.go) builds
	// exactly this shape, originally for the inbound auth-tls-secret
	// test — we re-use it for the outbound (backend-side) variant.
	bundle, err := generateMTLSBundle(sniHost)
	if err != nil {
		t.Fatalf("generate mTLS bundle: %v", err)
	}

	// 1. The backend's own server cert — bundled into the HAProxy
	//    container so the TLS-terminating frontend can present it.
	serverPEM := append(append([]byte{}, bundle.ServerCertPEM...), bundle.ServerKeyPEM...)
	serverSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "haproxy-mtls-backend-tls", Namespace: namespace},
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{"tls.pem": serverPEM, "ca.crt": bundle.CACertPEM},
	}
	if err := client.Resources(namespace).Create(ctx, serverSecret); err != nil {
		t.Fatalf("create mTLS-backend server secret: %v", err)
	}

	// 2. The CA Secret HAProxy will reference via server-ca to verify
	//    the backend's cert. Same CA the server cert was signed with.
	caSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "mtls-backend-ca", Namespace: namespace},
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{"ca.crt": bundle.CACertPEM},
	}
	if err := client.Resources(namespace).Create(ctx, caSecret); err != nil {
		t.Fatalf("create mTLS-backend CA secret: %v", err)
	}

	// 3. The client cert+key Secret HAProxy will reference via
	//    server-crt to authenticate to the backend.
	clientSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "mtls-backend-client-cert", Namespace: namespace},
		Type:       corev1.SecretTypeTLS,
		Data: map[string][]byte{
			"tls.crt": bundle.ClientCertPEM,
			"tls.key": bundle.ClientKeyPEM,
		},
	}
	if err := client.Resources(namespace).Create(ctx, clientSecret); err != nil {
		t.Fatalf("create mTLS-backend client secret: %v", err)
	}

	clientVerification := ""
	if requireClientCertificate {
		clientVerification = "ca-file /etc/ssl/demo/ca.crt verify required"
	}
	cfgMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "haproxy-mtls-backend-config", Namespace: namespace},
		Data: map[string]string{
			"haproxy.cfg": fmt.Sprintf(`global
    log stdout format raw local0 info

defaults
    mode http
    log global
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms

frontend mtls_frontend
    bind *:8443 ssl crt /etc/ssl/demo/tls.pem %s
    mode http
    default_backend echo_backend

backend echo_backend
    mode http
    server echo1 %s.%s.svc.cluster.local:%d check
`, clientVerification, upstream.Service, namespace, upstream.Port),
		},
	}
	if err := client.Resources(namespace).Create(ctx, cfgMap); err != nil {
		t.Fatalf("create mTLS-backend ConfigMap: %v", err)
	}

	// Deployment + Service. Mirrors the demo-backend's shape; the only
	// difference is the cert/key are mounted from the *server* secret
	// (which also includes ca.crt for the TLS frontend's verify directive).
	manifest := fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: haproxy-mtls-backend
  namespace: %s
spec:
  replicas: 1
  selector: {matchLabels: {app: haproxy-mtls-backend}}
  template:
    metadata: {labels: {app: haproxy-mtls-backend}}
    spec:
      initContainers:
        - name: mkdir
          image: busybox:1.36
          command: ["sh", "-c", "mkdir -p /etc/ssl/demo"]
          volumeMounts:
            - {name: tls-target, mountPath: /etc/ssl/demo}
      containers:
        - name: haproxy
          image: %s
          imagePullPolicy: IfNotPresent
          command: ["/bin/sh", "-c"]
          args:
            - |
              cp /config/haproxy.cfg /usr/local/etc/haproxy/haproxy.cfg
              cp /tls/tls.pem /etc/ssl/demo/tls.pem
              cp /tls/ca.crt /etc/ssl/demo/ca.crt
              exec haproxy -f /usr/local/etc/haproxy/haproxy.cfg
          ports:
            - {name: https, containerPort: 8443, protocol: TCP}
          volumeMounts:
            - {name: config, mountPath: /config, readOnly: true}
            - {name: tls, mountPath: /tls, readOnly: true}
            - {name: tls-target, mountPath: /etc/ssl/demo}
      volumes:
        - name: config
          configMap: {name: haproxy-mtls-backend-config}
        - name: tls
          secret: {secretName: haproxy-mtls-backend-tls}
        - name: tls-target
          emptyDir: {}
---
apiVersion: v1
kind: Service
metadata: {name: haproxy-mtls-backend, namespace: %s}
spec:
  selector: {app: haproxy-mtls-backend}
  ports:
    - {name: https, port: 8443, targetPort: https, protocol: TCP}
`, namespace, runtimeImages.HAProxy, namespace)

	if err := kubectlApplyStdin(ctx, []byte(manifest)); err != nil {
		t.Fatalf("apply haproxy-mtls-backend: %v", err)
	}
	if err := waitForServiceEndpointReady(ctx, client, namespace, "haproxy-mtls-backend"); err != nil {
		t.Fatalf("haproxy-mtls-backend not ready: %v", err)
	}

	return HAProxyTLSBackend{
		HTTPS:                BackendRef{Service: "haproxy-mtls-backend", Port: 8443},
		CASecretName:         caSecret.Name,
		ClientCertSecretName: clientSecret.Name,
	}
}
