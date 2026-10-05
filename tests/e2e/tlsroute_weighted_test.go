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
	"math"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/e2e-framework/klient"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

var tlsRouteGVR = schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "tlsroutes"}

// TestTLSRouteWeightedBackends checks that a passthrough TLSRoute rule splits
// connections 70/30 across two backendRefs. The split is per connection, so
// every sample opens a new one. The acceptance band is the five-sigma band of
// TestHTTPRouteSplit, derived from the successful-sample count and capped so a
// 50/50 split always fails.
func TestTLSRouteWeightedBackends(t *testing.T) {
	t.Parallel()
	host := "tlsroute-weighted.localdev.me"
	const (
		tlsPort       = 9643
		samples       = 200
		zFiveSigma    = 5.0
		maxTolerance  = 0.18
		primaryWeight = 70
		minorWeight   = 30
	)
	var fwd ServiceForward

	feature := features.New("TLSRoute: 70/30 weighted passthrough split").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			t.Helper()
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			primary := NewTLSResponderBackend(ctx, t, client, ns, "tls-primary", host)
			minor := NewTLSResponderBackend(ctx, t, client, ns, "tls-minor", host)

			gateway := fmt.Sprintf(`apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: tls-gateway
  namespace: %[1]s
spec:
  gatewayClassName: %[2]s
  listeners:
    - name: http
      protocol: HTTP
      port: 80
      allowedRoutes: {namespaces: {from: Same}}
    - name: tls
      protocol: TLS
      port: %[3]d
      hostname: %[4]s
      tls: {mode: Passthrough}
      allowedRoutes:
        namespaces: {from: Same}
        kinds: [{kind: TLSRoute}]
---
apiVersion: gateway.networking.k8s.io/v1
kind: TLSRoute
metadata:
  name: weighted
  namespace: %[1]s
spec:
  parentRefs: [{name: tls-gateway}]
  hostnames: [%[4]s]
  rules:
    - backendRefs:
        - {name: %[5]s, port: %[6]d, weight: %[7]d}
        - {name: %[8]s, port: %[9]d, weight: %[10]d}
`, ns, gatewayClassName, tlsPort, host, primary.Service, primary.Port, primaryWeight,
				minor.Service, minor.Port, minorWeight)
			if err := kubectlApplyStdin(ctx, []byte(gateway)); err != nil {
				t.Fatalf("apply Gateway and TLSRoute: %v", err)
			}
			waitForRouteDeployed(ctx, t, client, tlsRouteGVR, ns, "weighted")
			fwd = ForwardGateway(ctx, t, ns, "tls-gateway", tlsPort)
			return ctx
		}).
		Assess("connections converge to the configured 70/30 within a five-sigma band", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			t.Helper()
			client := httpclient.ForForwarded(t, 0, fwd.Ports[tlsPort])
			deadline := time.Now().Add(15 * time.Second)
			for sampleTLSRoute(ctx, client, host) == "" {
				if time.Now().After(deadline) {
					t.Fatalf("the TLSRoute never answered on %s", host)
				}
			}

			counts := map[string]int{}
			for i := 0; i < samples; i++ {
				counts[sampleTLSRoute(ctx, client, host)]++
			}
			total := counts["tls-primary"] + counts["tls-minor"]
			if total < samples/2 || total+counts[""] != samples {
				t.Fatalf("unexpected answers over %d connections: %v", samples, counts)
			}
			p0 := float64(minorWeight) / float64(primaryWeight+minorWeight)
			tolerance := math.Min(zFiveSigma*math.Sqrt(p0*(1-p0)/float64(total)), maxTolerance)
			observed := float64(counts["tls-minor"]) / float64(total)
			if math.Abs(observed-p0) > tolerance {
				t.Fatalf("minor share %.1f%% is more than %.1fpp from the configured %d%% (counts: %v)",
					observed*100, tolerance*100, minorWeight, counts)
			}
			t.Logf("split converged: %v (tolerance ±%.1fpp)", counts, tolerance*100)
			return ctx
		}).
		Feature()
	testEnv.Test(t, feature)
}

// sampleTLSRoute names the backend that answered one request on a new
// connection, or "" when four attempts fail.
func sampleTLSRoute(ctx context.Context, client *httpclient.Client, host string) string {
	for attempt := 0; attempt < 4; attempt++ {
		resp, err := client.HTTPS(host, "/").Do(ctx)
		client.CloseIdleConnections()
		if err == nil && resp.Status == 200 {
			return strings.TrimSpace(string(resp.Body))
		}
		time.Sleep(50 * time.Millisecond)
	}
	return ""
}

// NewTLSResponderBackend deploys an HAProxy that terminates TLS for host on
// port 8443 and answers every request with its own name, so a passthrough
// route's chosen backend is visible in the response body.
func NewTLSResponderBackend(ctx context.Context, t *testing.T, client klient.Client, namespace, name, host string) BackendRef {
	t.Helper()
	certPEM, keyPEM, err := generateSelfSignedCert([]string{host})
	if err != nil {
		t.Fatalf("generate certificate for %s: %v", name, err)
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name + "-tls", Namespace: namespace},
		Data:       map[string][]byte{"tls.pem": append(append([]byte{}, certPEM...), keyPEM...)},
	}
	if err := client.Resources(namespace).Create(ctx, secret); err != nil {
		t.Fatalf("create %s certificate Secret: %v", name, err)
	}
	config := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name + "-config", Namespace: namespace},
		Data: map[string]string{"haproxy.cfg": fmt.Sprintf(`global
    log stdout format raw local0 info

defaults
    mode http
    timeout connect 5s
    timeout client 30s
    timeout server 30s

frontend tls
    bind *:8443 ssl crt /tls/tls.pem
    http-request return status 200 content-type text/plain string %s
`, name)},
	}
	if err := client.Resources(namespace).Create(ctx, config); err != nil {
		t.Fatalf("create %s ConfigMap: %v", name, err)
	}
	manifest := fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: %[1]s
  namespace: %[2]s
spec:
  replicas: 1
  selector: {matchLabels: {app: %[1]s}}
  template:
    metadata: {labels: {app: %[1]s}}
    spec:
      containers:
        - name: haproxy
          image: %[3]s
          imagePullPolicy: IfNotPresent
          command: ["haproxy", "-db", "-f", "/config/haproxy.cfg"]
          ports:
            - {name: https, containerPort: 8443, protocol: TCP}
          readinessProbe:
            tcpSocket: {port: https}
            periodSeconds: 1
          volumeMounts:
            - {name: config, mountPath: /config, readOnly: true}
            - {name: tls, mountPath: /tls, readOnly: true}
      volumes:
        - name: config
          configMap: {name: %[1]s-config}
        - name: tls
          secret: {secretName: %[1]s-tls}
---
apiVersion: v1
kind: Service
metadata: {name: %[1]s, namespace: %[2]s}
spec:
  selector: {app: %[1]s}
  ports:
    - {name: https, port: 8443, targetPort: https, protocol: TCP}
`, name, namespace, runtimeImages.HAProxy)
	if err := kubectlApplyStdin(ctx, []byte(manifest)); err != nil {
		t.Fatalf("apply %s: %v", name, err)
	}
	if err := waitForServiceEndpointReady(ctx, client, namespace, name); err != nil {
		t.Fatalf("%s not ready: %v", name, err)
	}
	return BackendRef{Service: name, Port: 8443}
}
