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

	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

// TestHapticSSLPassthrough is the haptic-native analogue of
// TestIngressSSLPassthrough: the haproxy-haptic.org/ssl-passthrough
// annotation makes HAProxy route by SNI without terminating TLS. The TLS
// handshake happens at the backend (haproxy-demo-backend's HTTPS listener);
// the chart's frontend just forwards encrypted bytes. We mirror the vendor
// assertion that TLS terminates at the backend by driving an HTTPS request
// straight through and confirming the echo-server JSON comes back.
func TestHapticSSLPassthrough(t *testing.T) {
	t.Parallel()
	host := "ingress-haptic-passthrough.localdev.me"

	feature := features.New("Ingress: haptic ssl-passthrough annotation").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			echoBackend := NewEchoServerBackend(ctx, t, client, ns)
			demo := NewHAProxyDemoBackend(ctx, t, client, ns, echoBackend, host)

			NewIngress(ctx, t, client, ns, IngressSpec{
				Name:           "echo-haptic-passthrough",
				Host:           host,
				Path:           "/",
				BackendService: demo.HTTPS.Service,
				BackendPort:    demo.HTTPS.Port,
				Annotations: map[string]string{
					"haproxy-haptic.org/ssl-passthrough": "true",
				},
			})
			return ctx
		}).
		Assess("HTTPS request passes through to the backend that terminates TLS", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			resp := httpclient.New(t).HTTPS(host, "/").ExpectOK(t)
			if resp.Echo == nil {
				t.Fatalf("expected echo-server JSON via passthrough, got %d bytes", len(resp.Body))
			}
			return ctx
		}).
		Feature()
	testEnv.Test(t, feature)
}
