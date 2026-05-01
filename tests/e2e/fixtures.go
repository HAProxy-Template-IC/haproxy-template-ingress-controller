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
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/e2e-framework/klient"
	"sigs.k8s.io/e2e-framework/klient/k8s/resources"

	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

// NamespaceForTest creates a namespace with a unique name derived from the
// test name and registers a t.Cleanup that deletes the namespace (cascading
// cleanup of all resources inside).
//
// The naming convention is "e2e-<short-test-name>-<rand>" — short enough to
// stay under the 63-char DNS label limit, unique enough that parallel tests
// don't collide.
func NamespaceForTest(ctx context.Context, t *testing.T, client klient.Client) string {
	t.Helper()
	name := uniqueNamespaceName(t.Name())

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if err := client.Resources().Create(ctx, ns); err != nil {
		t.Fatalf("create namespace %q: %v", name, err)
	}

	t.Cleanup(func() {
		if shouldKeepNamespace() {
			t.Logf("KEEP_NAMESPACE=true: keeping %s", name)
			return
		}
		bg := context.Background()
		if err := client.Resources().Delete(bg, ns); err != nil {
			t.Logf("delete namespace %q: %v (best-effort)", name, err)
		}
	})

	return name
}

// uniqueNamespaceName builds a DNS-label-safe namespace name from a test
// name and a 4-byte random suffix.
func uniqueNamespaceName(testName string) string {
	short := dnsLabelify(testName)
	if len(short) > 40 {
		short = short[:40]
	}
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Extremely unlikely; fall back to fixed suffix.
		return "e2e-" + short + "-x"
	}
	return "e2e-" + short + "-" + hex.EncodeToString(b[:])
}

// dnsLabelify converts a Go test name to a DNS-1123-label-friendly string.
// Replaces underscores and slashes with dashes and lowercases.
func dnsLabelify(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z':
			out = append(out, c+('a'-'A'))
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			out = append(out, c)
		default:
			// Collapse runs of non-alphanumerics to a single dash.
			if len(out) > 0 && out[len(out)-1] != '-' {
				out = append(out, '-')
			}
		}
	}
	// Trim leading/trailing dashes.
	for len(out) > 0 && out[0] == '-' {
		out = out[1:]
	}
	for len(out) > 0 && out[len(out)-1] == '-' {
		out = out[:len(out)-1]
	}
	return string(out)
}

// shouldKeepNamespace mirrors tests/acceptance ShouldKeepNamespace.
func shouldKeepNamespace() bool {
	// Lazily imported via os to avoid cyclic deps with env.go.
	v, _ := lookupEnv("KEEP_NAMESPACE")
	return v == "true"
}

// BackendRef identifies a per-test backend (Deployment + Service) the
// tests deploy as their HTTP upstream. Each test owns its own backend so
// HAProxy reaches a real Endpoint when the controller renders it — the
// chart's routing logic only emits server lines for Services with at
// least one ready Endpoint, so ExternalName aliases don't work here.
//
// Backends are stateless and small (echo-server image), and they tear
// down with the test namespace.
type BackendRef struct {
	// Service is the per-test Service name.
	Service string
	// Port is the Service's HTTP port.
	Port int32
}

// EchoServerBackend is the conventional name + port for the echo-server
// backend. Tests that don't care about the specifics use this directly.
var EchoServerBackend = BackendRef{Service: "echo-server", Port: 80}

// echoServerImage is the upstream image used for routing tests. echo-server
// returns the incoming request as JSON, which lets assertions check headers
// and rewritten paths without setting up custom backends.
const echoServerImage = "ealen/echo-server:latest"

// NewEchoServerBackend deploys an echo-server Deployment + Service into the
// test namespace and waits for at least one endpoint to be Ready. Returns
// the BackendRef the caller passes to NewIngress / similar fixtures.
//
// Cleaned up automatically when the test namespace is deleted in t.Cleanup.
func NewEchoServerBackend(ctx context.Context, t *testing.T, client klient.Client, namespace string) BackendRef {
	t.Helper()
	ref := EchoServerBackend
	labels := map[string]string{"app": ref.Service}
	replicas := int32(1)

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ref.Service,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:            "server",
						Image:           echoServerImage,
						ImagePullPolicy: corev1.PullIfNotPresent,
						Ports: []corev1.ContainerPort{{
							Name:          "http",
							ContainerPort: 80,
							Protocol:      corev1.ProtocolTCP,
						}},
					}},
				},
			},
		},
	}
	if err := client.Resources(namespace).Create(ctx, deployment); err != nil {
		t.Fatalf("create echo-server Deployment %s/%s: %v", namespace, ref.Service, err)
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ref.Service,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{{
				Name:       "http",
				Port:       ref.Port,
				TargetPort: intstr.FromString("http"),
				Protocol:   corev1.ProtocolTCP,
			}},
		},
	}
	if err := client.Resources(namespace).Create(ctx, svc); err != nil {
		t.Fatalf("create echo-server Service %s/%s: %v", namespace, ref.Service, err)
	}

	if err := waitForServiceEndpointReady(ctx, client, namespace, ref.Service); err != nil {
		t.Fatalf("echo-server endpoint not ready: %v", err)
	}
	return ref
}

// waitForServiceEndpointReady blocks until the named Service has at least
// one ready endpoint in its EndpointSlice. The chart's template logic emits
// no backend servers until this is true.
func waitForServiceEndpointReady(ctx context.Context, client klient.Client, namespace, serviceName string) error {
	cfg := testutil.FastWaitConfig()
	cfg.Timeout = DefaultPerTestSetupTimeout

	return testutil.WaitForConditionWithDescription(ctx, cfg, "service "+namespace+"/"+serviceName+" has ready endpoint",
		func(ctx context.Context) (bool, error) {
			var slices discoveryv1.EndpointSliceList
			if err := client.Resources(namespace).List(ctx, &slices,
				resources.WithLabelSelector("kubernetes.io/service-name="+serviceName)); err != nil {
				return false, err
			}
			for _, sl := range slices.Items {
				for _, ep := range sl.Endpoints {
					if ep.Conditions.Ready != nil && *ep.Conditions.Ready {
						return true, nil
					}
				}
			}
			return false, fmt.Errorf("no ready endpoints in %d slices", len(slices.Items))
		})
}

// IngressSpec captures the minimum a routing test needs to declare.
type IngressSpec struct {
	// Name is the Ingress resource name within the test namespace.
	Name string
	// Host is the request Host: header to match.
	Host string
	// Path is the request path prefix to match (default "/").
	Path string
	// Backend identifies the upstream Service in the test namespace,
	// typically created by NewEchoServerBackend.
	BackendService string
	BackendPort    int32
	// Annotations are passed through verbatim. Useful for nginx.ingress.*
	// annotations the chart's nginx-ingress library understands.
	Annotations map[string]string
	// TLSSecretName, if non-empty, populates spec.tls[] with the named
	// Secret covering Host. The Secret must already exist in the test
	// namespace (use NewTLSSecret to create one).
	TLSSecretName string
}

// NewIngress applies the Ingress described by spec. The IngressClass name
// is "haptic" (matches the helm chart's default).
func NewIngress(ctx context.Context, t *testing.T, client klient.Client, namespace string, spec IngressSpec) *networkingv1.Ingress {
	t.Helper()

	if spec.Path == "" {
		spec.Path = "/"
	}
	pathType := networkingv1.PathTypePrefix
	ingressClassName := "haptic"

	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:        spec.Name,
			Namespace:   namespace,
			Annotations: spec.Annotations,
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: &ingressClassName,
			Rules: []networkingv1.IngressRule{
				{
					Host: spec.Host,
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     spec.Path,
									PathType: &pathType,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: spec.BackendService,
											Port: networkingv1.ServiceBackendPort{
												Number: spec.BackendPort,
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	if spec.TLSSecretName != "" {
		ing.Spec.TLS = []networkingv1.IngressTLS{{
			Hosts:      []string{spec.Host},
			SecretName: spec.TLSSecretName,
		}}
	}
	if err := client.Resources(namespace).Create(ctx, ing); err != nil {
		t.Fatalf("create Ingress %s/%s: %v", namespace, spec.Name, err)
	}

	// Delete the Ingress explicitly before the namespace teardown
	// cascades, so the controller observes the Ingress disappear before
	// any Secrets/ConfigMaps it referenced. Without this, a parallel test's
	// webhook validation can fire while this Ingress is still in the
	// controller's resource store but its referenced Secret has already
	// been removed by the cascade — the dry-run render then fails because
	// of the orphaned reference, denying admission for the unrelated
	// resource. t.Cleanup runs in LIFO order, so this runs before
	// NamespaceForTest's namespace-delete cleanup that was registered
	// earlier in the test setup.
	t.Cleanup(func() {
		bg := context.Background()
		if err := client.Resources(namespace).Delete(bg, ing); err != nil && !apierrors.IsNotFound(err) {
			t.Logf("delete Ingress %s/%s: %v (best-effort)", namespace, spec.Name, err)
		}
	})
	return ing
}

// NewTLSSecret generates a self-signed certificate for the given hosts and
// writes it as a kubernetes.io/tls Secret in the test namespace. Returns
// the Secret name so the caller can reference it from IngressSpec.TLSSecretName.
//
// The cert is short-lived (1 year) and self-signed; tests that need to
// verify the chain should use httpclient.WithClientCert with a CA bundle
// instead. This helper is for tests that just want HTTPS termination to
// work and don't care about chain validation (httpclient defaults to
// insecure-skip-verify against dev-env certs).
func NewTLSSecret(ctx context.Context, t *testing.T, client klient.Client, namespace, name string, hosts []string) string {
	t.Helper()
	certPEM, keyPEM, err := generateSelfSignedCert(hosts)
	if err != nil {
		t.Fatalf("generate self-signed cert for %v: %v", hosts, err)
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Type:       corev1.SecretTypeTLS,
		Data: map[string][]byte{
			"tls.crt": certPEM,
			"tls.key": keyPEM,
		},
	}
	if err := client.Resources(namespace).Create(ctx, secret); err != nil {
		t.Fatalf("create TLS Secret %s/%s: %v", namespace, name, err)
	}
	return name
}

// generateSelfSignedCert returns a PEM-encoded certificate and key for a
// fresh self-signed server cert covering the given DNS hosts. Used by
// NewTLSSecret. Mirrors the webhook-cert generator's RSA-2048 / 1-year
// shape so cert-related quirks behave consistently.
func generateSelfSignedCert(hosts []string) (certPEM, keyPEM []byte, err error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	cn := "test-server"
	if len(hosts) > 0 {
		cn = hosts[0]
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     hosts,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return certPEM, keyPEM, nil
}
