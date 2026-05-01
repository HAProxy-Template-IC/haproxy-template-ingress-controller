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
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

// TestIngressTLSMulti covers test_ingress_tls_multi: a SAN certificate
// covering two hostnames; HAProxy should serve the same cert for both
// SNI values via the chart's TLS rendering. The Ingress declares both
// hostnames in spec.rules[] and a single spec.tls[] with both hosts and
// the shared secret.
func TestIngressTLSMulti(t *testing.T) {
	t.Parallel()

	primary := "ingress-tls-multi.localdev.me"
	alt := "ingress-tls-alt.localdev.me"

	feature := features.New("Ingress: SAN certificate covering multiple hostnames").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			backend := NewEchoServerBackend(ctx, t, client, ns)

			// One Secret, two hosts in DNS SANs.
			NewTLSSecret(ctx, t, client, ns, "ingress-tls-multi-cert", []string{primary, alt})

			// The standard NewIngress fixture only takes a single host;
			// multi-host needs two rules + one TLS block. Build the
			// resource directly here rather than extending the helper
			// (only this test needs multi-host).
			pathType := networkingv1.PathTypePrefix
			ingressClassName := "haptic"
			ing := &networkingv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "echo-tls-multi",
					Namespace: ns,
				},
				Spec: networkingv1.IngressSpec{
					IngressClassName: &ingressClassName,
					TLS: []networkingv1.IngressTLS{{
						Hosts:      []string{primary, alt},
						SecretName: "ingress-tls-multi-cert",
					}},
					Rules: []networkingv1.IngressRule{
						{Host: primary, IngressRuleValue: networkingv1.IngressRuleValue{
							HTTP: &networkingv1.HTTPIngressRuleValue{
								Paths: []networkingv1.HTTPIngressPath{{
									Path: "/", PathType: &pathType,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: backend.Service,
											Port: networkingv1.ServiceBackendPort{Number: backend.Port},
										},
									},
								}},
							},
						}},
						{Host: alt, IngressRuleValue: networkingv1.IngressRuleValue{
							HTTP: &networkingv1.HTTPIngressRuleValue{
								Paths: []networkingv1.HTTPIngressPath{{
									Path: "/", PathType: &pathType,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: backend.Service,
											Port: networkingv1.ServiceBackendPort{Number: backend.Port},
										},
									},
								}},
							},
						}},
					},
				},
			}
			if err := client.Resources(ns).Create(ctx, ing); err != nil {
				t.Fatalf("create multi-host Ingress: %v", err)
			}
			return ctx
		}).
		Assess("primary hostname serves over HTTPS", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.New(t).HTTPS(primary, "/").ExpectOK(t)
			return ctx
		}).
		Assess("alternate hostname (in same SAN cert) serves over HTTPS", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			httpclient.New(t).HTTPS(alt, "/").ExpectOK(t)
			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}
