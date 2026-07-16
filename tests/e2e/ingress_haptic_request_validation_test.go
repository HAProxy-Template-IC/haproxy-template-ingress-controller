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
	"context"
	"net/http"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/e2e-framework/klient"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

// TestHapticRequestValidation exercises the Phase-5 API-gateway validation path:
// native haproxy-haptic.org/request-schema-* annotations, schema resolution from
// a ConfigMap, SPOE dispatch to the bundled api-gateway plugin, and fail-closed
// frontend response handling.
func TestHapticRequestValidation(t *testing.T) {
	RequireAPIGatewayProfile(t)
	t.Parallel()

	const (
		schemaConfigMap = "request-schema"
		schemaKey       = "schema.json"
		maxBodyBytes    = "64"
	)

	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: HAPTIC request-schema validation annotations",
		Host:        "ingress-haptic-request-validation.localdev.me",
		Annotations: map[string]string{
			"haproxy-haptic.org/request-schema-configmap":     schemaConfigMap + ":" + schemaKey,
			"haproxy-haptic.org/request-schema-content-types": "application/json",
			"haproxy-haptic.org/request-schema-max-body-size": maxBodyBytes,
		},
		PreSetup: func(ctx context.Context, t *testing.T, client klient.Client, namespace string) {
			t.Helper()
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      schemaConfigMap,
					Namespace: namespace,
				},
				Data: map[string]string{
					schemaKey: `{"type":"object","required":["name"],"properties":{"name":{"type":"string"}}}`,
				},
			}
			if err := client.Resources(namespace).Create(ctx, cm); err != nil {
				t.Fatalf("create request schema ConfigMap: %v", err)
			}
		},
		Assess: []SimpleIngressAssertion{
			{
				Name: "valid JSON body reaches the backend",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, "/validate").
						WithMethod(http.MethodPost).
						WithHeader("Content-Type", "application/json").
						WithBody(`{"name":"alice"}`).
						ExpectMatching(t, "valid JSON body accepted by api-gateway plugin", func(resp *httpclient.Response) bool {
							return resp.Status == http.StatusOK && resp.Echo != nil
						})
				},
			},
			{
				Name: "schema mismatch is rejected",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, "/validate").
						WithMethod(http.MethodPost).
						WithHeader("Content-Type", "application/json").
						WithBody(`{"name":42}`).
						ExpectMatching(t, "schema-invalid JSON rejected with 422", func(resp *httpclient.Response) bool {
							return resp.Status == http.StatusUnprocessableEntity
						})
				},
			},
			{
				Name: "invalid JSON is rejected",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, "/validate").
						WithMethod(http.MethodPost).
						WithHeader("Content-Type", "application/json").
						WithBody(`{`).
						ExpectMatching(t, "invalid JSON rejected with 422", func(resp *httpclient.Response) bool {
							return resp.Status == http.StatusUnprocessableEntity
						})
				},
			},
			{
				Name: "wrong content type is rejected",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, "/validate").
						WithMethod(http.MethodPost).
						WithHeader("Content-Type", "text/plain").
						WithBody(`{"name":"alice"}`).
						ExpectMatching(t, "unsupported content type rejected with 415", func(resp *httpclient.Response) bool {
							return resp.Status == http.StatusUnsupportedMediaType
						})
				},
			},
			{
				Name: "oversized body is rejected before plugin validation",
				Check: func(t *testing.T, host string) {
					body := `{"name":"` + strings.Repeat("a", 80) + `"}`
					httpclient.New(t).GET(host, "/validate").
						WithMethod(http.MethodPost).
						WithHeader("Content-Type", "application/json").
						WithBody(body).
						ExpectMatching(t, "oversized body rejected with 413", func(resp *httpclient.Response) bool {
							return resp.Status == http.StatusRequestEntityTooLarge
						})
				},
			},
			{
				Name: "unknown-length body is rejected before plugin validation",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, "/validate").
						WithMethod(http.MethodPost).
						WithHeader("Content-Type", "application/json").
						WithChunkedBody(`{"name":"alice"}`).
						ExpectMatching(t, "chunked validation body rejected with 411", func(resp *httpclient.Response) bool {
							return resp.Status == http.StatusLengthRequired
						})
				},
			},
			{
				Name: "GET is not body-validated",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, "/validate").
						ExpectMatching(t, "GET bypasses request-body validation", func(resp *httpclient.Response) bool {
							return resp.Status == http.StatusOK && resp.Echo != nil
						})
				},
			},
		},
	})
}
