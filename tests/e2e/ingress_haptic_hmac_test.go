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
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"

	"sigs.k8s.io/e2e-framework/klient"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

// TestHapticHMAC verifies HMAC request-signature verification (64-gateway-
// security.yaml) with hmac-signed-string=path: HAProxy recomputes
// hmac(sha256, key) over the request path and denies 401 unless the client
// signature matches.
//
// The Secret's `secret` data key holds the raw shared key; the template passes
// its base64 form (the Secret's stored representation) to HAProxy's hmac()
// converter, which base64-decodes it — so the effective key equals the raw
// bytes, and the client signs with those same bytes.
func TestHapticHMAC(t *testing.T) {
	t.Parallel()
	const rawKey = "s3cr3t-key"
	const path = "/"

	mac := hmac.New(sha256.New, []byte(rawKey))
	mac.Write([]byte(path))
	validSig := hex.EncodeToString(mac.Sum(nil))

	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: HAPTIC-native HMAC signature verification",
		Host:        "ingress-haptic-hmac.localdev.me",
		Path:        path,
		Annotations: map[string]string{
			"haproxy-haptic.org/hmac-secret":        "hmac-keys",
			"haproxy-haptic.org/hmac-algorithm":     "sha256",
			"haproxy-haptic.org/hmac-header":        "X-Signature",
			"haproxy-haptic.org/hmac-signed-string": "path",
		},
		PreSetup: func(ctx context.Context, t *testing.T, client klient.Client, namespace string) {
			mustCreateSecret(ctx, t, client, namespace, "hmac-keys", map[string][]byte{
				"secret": []byte(rawKey),
			})
		},
		Assess: []SimpleIngressAssertion{
			{
				Name: "no signature returns 401",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, path).ExpectStatus(t, http.StatusUnauthorized)
				},
			},
			{
				Name: "wrong signature returns 401",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, path).WithHeader("X-Signature", "deadbeef").ExpectStatus(t, http.StatusUnauthorized)
				},
			},
			{
				Name: "valid signature reaches upstream (200)",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, path).WithHeader("X-Signature", validSig).ExpectStatus(t, http.StatusOK)
				},
			},
		},
	})
}
