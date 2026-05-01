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

// HAProxyDemoBackends are the two ports the haproxy-demo-backend fixture
// exposes:
//   - 8080: accept-proxy (HAProxy PROXY-protocol-aware HTTP listener)
//   - 8443: HTTPS (TLS termination with a self-signed cert)
//
// Both terminate at the same backend that forwards plain HTTP to the
// per-test echo-server. That makes haproxy-demo-backend the "TLS- /
// PROXY-protocol-capable upstream" tests like backend_ssl, backend_mtls,
// proxy_protocol, ssl_passthrough need to point an Ingress at.
type HAProxyDemoBackends struct {
	HTTPProxyProtocol BackendRef // port 8080, accept-proxy
	HTTPS             BackendRef // port 8443, TLS-terminating
}

// NewHAProxyDemoBackend deploys a per-test haproxy-demo-backend
// (Deployment + Service + ConfigMap + TLS Secret). Mirrors
// scripts/dev-env-assets/haproxy-demo-backend.yaml but with the upstream
// rewritten to the test's own echo-server (cross-namespace doesn't work
// with per-test isolation; this version uses the in-namespace upstream).
//
// The TLS cert is generated fresh per call (subject CN = the host the
// caller will route to). Returns the two BackendRef ports.
//
// Cleaned up automatically when the test namespace is deleted.
func NewHAProxyDemoBackend(ctx context.Context, t *testing.T, client klient.Client, namespace string, upstream BackendRef, sniHost string) HAProxyDemoBackends {
	t.Helper()

	// Generate a self-signed cert for the demo backend's HTTPS listener.
	// The chart's ssl-passthrough sets backend SNI = the configured host;
	// the cert's CN must match.
	certPEM, keyPEM, err := generateSelfSignedCert([]string{sniHost})
	if err != nil {
		t.Fatalf("generate demo-backend cert: %v", err)
	}
	combinedPEM := append(append([]byte{}, certPEM...), keyPEM...)

	tlsSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "haproxy-demo-backend-tls", Namespace: namespace},
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{"tls.pem": combinedPEM},
	}
	if err := client.Resources(namespace).Create(ctx, tlsSecret); err != nil {
		t.Fatalf("create demo-backend TLS secret: %v", err)
	}

	cfgMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "haproxy-demo-backend-config", Namespace: namespace},
		Data: map[string]string{
			"haproxy.cfg": fmt.Sprintf(`global
    log stdout format raw local0 info

defaults
    mode http
    log global
    timeout connect 5000ms
    timeout client 50000ms
    timeout server 50000ms

# PROXY-protocol-aware HTTP frontend.
frontend proxy_frontend
    bind *:8080 accept-proxy
    mode http
    default_backend echo_backend

# TLS-terminating HTTPS frontend. ALPN advertises both h2 and http/1.1
# so it accepts HAProxy → backend HTTP/2 ("haproxy.org/server-proto: h2"
# combined with server-ssl) as well as plain HTTPS.
frontend https_frontend
    bind *:8443 ssl crt /etc/ssl/demo/tls.pem alpn h2,http/1.1
    mode http
    default_backend echo_backend

backend echo_backend
    mode http
    server echo1 %s.%s.svc.cluster.local:%d check
`, upstream.Service, namespace, upstream.Port),
		},
	}
	if err := client.Resources(namespace).Create(ctx, cfgMap); err != nil {
		t.Fatalf("create demo-backend ConfigMap: %v", err)
	}

	manifest := fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: haproxy-demo-backend
  namespace: %s
  labels:
    app: haproxy-demo-backend
spec:
  replicas: 1
  selector:
    matchLabels:
      app: haproxy-demo-backend
  template:
    metadata:
      labels:
        app: haproxy-demo-backend
    spec:
      containers:
        - name: haproxy
          image: haproxytech/haproxy-debian:3.2
          imagePullPolicy: IfNotPresent
          command: ["/bin/sh", "-c"]
          args:
            - |
              cp /config/haproxy.cfg /usr/local/etc/haproxy/haproxy.cfg
              cp /tls/tls.pem /etc/ssl/demo/tls.pem
              exec haproxy -f /usr/local/etc/haproxy/haproxy.cfg
          ports:
            - name: http
              containerPort: 8080
              protocol: TCP
            - name: https
              containerPort: 8443
              protocol: TCP
          volumeMounts:
            - name: config
              mountPath: /config
              readOnly: true
            - name: tls
              mountPath: /tls
              readOnly: true
            - name: tls-target
              mountPath: /etc/ssl/demo
      initContainers:
        - name: mkdir-tls
          image: busybox:1.36
          command: ["sh", "-c", "mkdir -p /etc/ssl/demo"]
          volumeMounts:
            - name: tls-target
              mountPath: /etc/ssl/demo
      volumes:
        - name: config
          configMap:
            name: haproxy-demo-backend-config
        - name: tls
          secret:
            secretName: haproxy-demo-backend-tls
        - name: tls-target
          emptyDir: {}
---
apiVersion: v1
kind: Service
metadata:
  name: haproxy-demo-backend
  namespace: %s
  labels:
    app: haproxy-demo-backend
spec:
  selector:
    app: haproxy-demo-backend
  ports:
    - name: http
      port: 8080
      targetPort: http
      protocol: TCP
    - name: https
      port: 8443
      targetPort: https
      protocol: TCP
`, namespace, namespace)

	if err := kubectlApplyStdin(ctx, []byte(manifest)); err != nil {
		t.Fatalf("apply haproxy-demo-backend: %v", err)
	}

	if err := waitForServiceEndpointReady(ctx, client, namespace, "haproxy-demo-backend"); err != nil {
		t.Fatalf("haproxy-demo-backend not ready: %v", err)
	}

	return HAProxyDemoBackends{
		HTTPProxyProtocol: BackendRef{Service: "haproxy-demo-backend", Port: 8080},
		HTTPS:             BackendRef{Service: "haproxy-demo-backend", Port: 8443},
	}
}
