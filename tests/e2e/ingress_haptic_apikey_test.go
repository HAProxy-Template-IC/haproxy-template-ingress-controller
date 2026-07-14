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
