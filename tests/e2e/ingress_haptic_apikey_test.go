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
	"net/http"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/e2e-framework/klient"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

// mustCreateSecret creates an Opaque Secret in the per-test namespace or fails.
func mustCreateSecret(ctx context.Context, t *testing.T, client klient.Client, namespace, name string, data map[string][]byte) {
	t.Helper()
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Type:       corev1.SecretTypeOpaque,
		Data:       data,
	}
	if err := client.Resources(namespace).Create(ctx, s); err != nil {
		t.Fatalf("create secret %s: %v", name, err)
	}
}

// TestHapticAPIKey verifies API-key authentication (62-api-key.yaml): a request
// with an unknown / missing key is denied 401, a valid key reaches the upstream,
// and the resolved consumer id is forwarded upstream via api-key-consumer-header.
func TestHapticAPIKey(t *testing.T) {
	t.Parallel()
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: HAPTIC-native API-key auth",
		Host:        "ingress-haptic-apikey.localdev.me",
		Annotations: map[string]string{
			"haproxy-haptic.org/api-key-secret":          "api-keys",
			"haproxy-haptic.org/api-key-header":          "X-API-Key",
			"haproxy-haptic.org/api-key-consumer-header": "X-Consumer-ID",
		},
		PreSetup: func(ctx context.Context, t *testing.T, client klient.Client, namespace string) {
			mustCreateSecret(ctx, t, client, namespace, "api-keys", map[string][]byte{
				"keys": []byte("apikey-abc123:alice\napikey-def456:bob\n"),
			})
		},
		Assess: []SimpleIngressAssertion{
			{
				Name: "no key returns 401",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, "/").ExpectStatus(t, http.StatusUnauthorized)
				},
			},
			{
				Name: "unknown key returns 401",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, "/").WithHeader("X-API-Key", "nope").ExpectStatus(t, http.StatusUnauthorized)
				},
			},
			{
				Name: "valid key reaches upstream with consumer id forwarded",
				Check: func(t *testing.T, host string) {
					// ExpectEchoHeader polls until the upstream echo JSON shows the
					// header — implies the request was admitted (not 401).
					httpclient.New(t).GET(host, "/").WithHeader("X-API-Key", "apikey-abc123").
						ExpectEchoHeader(t, "X-Consumer-ID", "alice")
				},
			},
		},
	})
}

// TestHapticAPIKeyMissingSecretFailsClosed verifies that an Ingress can be
// admitted before its referenced Secret reaches the controller's watch cache
// without opening the protected route or producing an internally inconsistent
// HAProxyCfg. The route must fail closed while the Secret is absent and recover
// automatically after the Secret is created.
func TestHapticAPIKeyMissingSecretFailsClosed(t *testing.T) {
	t.Parallel()

	const host = "ingress-haptic-apikey-secret-race.localdev.me"
	var namespace string

	feature := features.New("Ingress: HAPTIC API-key auth fails closed until Secret exists").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			namespace = NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, namespace)
			backend := NewEchoServerBackend(ctx, t, client, namespace)

			// Deliberately create the Ingress before api-keys. Admission must
			// succeed, but the generated route must not reference a missing map.
			NewIngress(ctx, t, client, namespace, IngressSpec{
				Name:           "echo",
				Host:           host,
				BackendService: backend.Service,
				BackendPort:    backend.Port,
				Annotations: map[string]string{
					"haproxy-haptic.org/api-key-secret":          "api-keys",
					"haproxy-haptic.org/api-key-header":          "X-API-Key",
					"haproxy-haptic.org/api-key-consumer-header": "X-Consumer-ID",
				},
			})
			return ctx
		}).
		Assess("missing Secret fails closed with 503", func(ctx context.Context, t *testing.T, _ *envconf.Config) context.Context {
			httpclient.New(t).GET(host, "/").WithHeader("X-API-Key", "apikey-abc123").
				ExpectStatus(t, http.StatusServiceUnavailable)
			return ctx
		}).
		Assess("creating Secret restores authenticated traffic", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			mustCreateSecret(ctx, t, client, namespace, "api-keys", map[string][]byte{
				"keys": []byte("apikey-abc123:alice\n"),
			})

			httpclient.New(t).GET(host, "/").WithHeader("X-API-Key", "apikey-abc123").
				ExpectEchoHeader(t, "X-Consumer-ID", "alice")
			return ctx
		})

	testEnv.Test(t, feature.Feature())
}

// TestHapticConsumerGroups verifies consumer-group authorization (64-gateway-
// security.yaml): after API-key auth establishes the consumer, the route only
// admits consumers whose group is in allowed-consumer-groups (deny 403 otherwise).
func TestHapticConsumerGroups(t *testing.T) {
	t.Parallel()
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: HAPTIC-native consumer-group authorization",
		Host:        "ingress-haptic-consumergroups.localdev.me",
		Annotations: map[string]string{
			"haproxy-haptic.org/api-key-secret":          "api-keys",
			"haproxy-haptic.org/api-key-header":          "X-API-Key",
			"haproxy-haptic.org/consumer-groups-secret":  "groups",
			"haproxy-haptic.org/allowed-consumer-groups": "admins",
		},
		PreSetup: func(ctx context.Context, t *testing.T, client klient.Client, namespace string) {
			mustCreateSecret(ctx, t, client, namespace, "api-keys", map[string][]byte{
				"keys": []byte("apikey-abc123:alice\napikey-def456:bob\n"),
			})
			// alice → admins (allowed), bob → users (not allowed).
			mustCreateSecret(ctx, t, client, namespace, "groups", map[string][]byte{
				"groups": []byte("alice:admins\nbob:users\n"),
			})
		},
		Assess: []SimpleIngressAssertion{
			{
				Name: "consumer in an allowed group reaches upstream (200)",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, "/").WithHeader("X-API-Key", "apikey-abc123").ExpectStatus(t, http.StatusOK)
				},
			},
			{
				Name: "consumer in a non-allowed group is denied (403)",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, "/").WithHeader("X-API-Key", "apikey-def456").ExpectStatus(t, http.StatusForbidden)
				},
			},
		},
	})
}

// TestHapticConsumerGroupsMissingSecretFailsClosed covers the same
// informer-propagation boundary for the authorization map: both authentication
// and authorization must remain closed until every referenced Secret exists,
// then recover without recreating the Ingress.
func TestHapticConsumerGroupsMissingSecretFailsClosed(t *testing.T) {
	t.Parallel()

	const host = "ingress-haptic-consumergroups-secret-race.localdev.me"
	var namespace string

	feature := features.New("Ingress: HAPTIC consumer groups fail closed until Secret exists").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			namespace = NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, namespace)
			backend := NewEchoServerBackend(ctx, t, client, namespace)
			mustCreateSecret(ctx, t, client, namespace, "api-keys", map[string][]byte{
				"keys": []byte("apikey-abc123:alice\n"),
			})

			// Deliberately leave the consumer-groups Secret absent.
			NewIngress(ctx, t, client, namespace, IngressSpec{
				Name:           "echo",
				Host:           host,
				BackendService: backend.Service,
				BackendPort:    backend.Port,
				Annotations: map[string]string{
					"haproxy-haptic.org/api-key-secret":          "api-keys",
					"haproxy-haptic.org/api-key-header":          "X-API-Key",
					"haproxy-haptic.org/consumer-groups-secret":  "groups",
					"haproxy-haptic.org/allowed-consumer-groups": "admins",
				},
			})
			return ctx
		}).
		Assess("missing groups Secret fails closed with 503", func(ctx context.Context, t *testing.T, _ *envconf.Config) context.Context {
			httpclient.New(t).GET(host, "/").WithHeader("X-API-Key", "apikey-abc123").
				ExpectStatus(t, http.StatusServiceUnavailable)
			return ctx
		}).
		Assess("creating groups Secret restores authorized traffic", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			mustCreateSecret(ctx, t, client, namespace, "groups", map[string][]byte{
				"groups": []byte("alice:admins\n"),
			})

			httpclient.New(t).GET(host, "/").WithHeader("X-API-Key", "apikey-abc123").
				ExpectStatus(t, http.StatusOK)
			return ctx
		})

	testEnv.Test(t, feature.Feature())
}
