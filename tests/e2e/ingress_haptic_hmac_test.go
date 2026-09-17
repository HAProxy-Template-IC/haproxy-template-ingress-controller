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
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/e2e-framework/klient"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/e2ecluster"
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

	RunSimpleIngressTest(t, &SimpleIngressTest{
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
			t.Helper()
			mustCreateSecret(ctx, t, client, namespace, "hmac-keys", map[string][]byte{
				"secret": []byte(rawKey),
			})
		},
		Assess: []SimpleIngressAssertion{
			{
				Name: "no signature returns 401",
				Check: func(t *testing.T, host string) {
					t.Helper()
					httpclient.New(t).GET(host, path).ExpectStatus(t, http.StatusUnauthorized)
				},
			},
			{
				Name: "wrong signature returns 401",
				Check: func(t *testing.T, host string) {
					t.Helper()
					httpclient.New(t).GET(host, path).WithHeader("X-Signature", "deadbeef").ExpectStatus(t, http.StatusUnauthorized)
				},
			},
			{
				Name: "valid signature reaches upstream (200)",
				Check: func(t *testing.T, host string) {
					t.Helper()
					httpclient.New(t).GET(host, path).WithHeader("X-Signature", validSig).ExpectStatus(t, http.StatusOK)
				},
			},
		},
	})
}

func TestHapticHMACBodyIntegrity(t *testing.T) {
	t.Parallel()
	const rawKey = "body-verification-key"
	cases := []struct {
		name    string
		body    string
		chunked bool
		invalid bool
		status  int
	}{
		{name: "complete body", body: "verified body", status: http.StatusOK},
		{name: "empty body", status: http.StatusOK},
		{name: "invalid signature", body: "verified body", invalid: true, status: http.StatusUnauthorized},
		{name: "body exceeds buffer", body: strings.Repeat("x", 128<<10), status: http.StatusRequestEntityTooLarge},
		{name: "unknown length", body: "verified body", chunked: true, status: http.StatusLengthRequired},
	}
	assertions := make([]SimpleIngressAssertion, 0, len(cases)+1)
	for _, tc := range cases {
		assertions = append(assertions, SimpleIngressAssertion{
			Name: tc.name,
			Check: func(t *testing.T, host string) {
				t.Helper()
				mac := hmac.New(sha256.New, []byte(rawKey))
				mac.Write([]byte(tc.body))
				signature := hex.EncodeToString(mac.Sum(nil))
				if tc.invalid {
					signature = strings.Repeat("0", len(signature))
				}
				request := httpclient.New(t).GET(host, "/").WithMethod(http.MethodPost).
					WithHeader("X-Signature", signature).WithBody(tc.body)
				if tc.chunked {
					request.WithChunkedBody(tc.body)
				}
				request.ExpectStatus(t, tc.status)
			},
		})
	}
	assertions = append(assertions, SimpleIngressAssertion{
		Name:  "declared length beyond the signed integer range",
		Check: expectIncompleteHMACBodyRejected,
	})
	RunSimpleIngressTest(t, &SimpleIngressTest{
		Description: "HMAC body verification requires the complete declared body",
		Host:        "ingress-haptic-hmac-body.localdev.me",
		Annotations: map[string]string{
			"haproxy-haptic.org/hmac-secret": "body-key",
		},
		PreSetup: func(ctx context.Context, t *testing.T, client klient.Client, namespace string) {
			t.Helper()
			mustCreateSecret(ctx, t, client, namespace, "body-key", map[string][]byte{
				"secret": []byte(rawKey),
			})
		},
		Assess: assertions,
	})
}

func expectIncompleteHMACBodyRejected(t *testing.T, host string) {
	t.Helper()
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	endpoint, err := e2ecluster.ResolveTrafficEndpoint()
	require.NoError(t, err)
	address := net.JoinHostPort(endpoint.Host, strconv.Itoa(endpoint.HTTPPort))
	connection, err := dialer.DialContext(t.Context(), "tcp4", address)
	require.NoError(t, err)
	defer connection.Close()
	require.NoError(t, connection.SetDeadline(time.Now().Add(5*time.Second)))
	request := fmt.Sprintf("POST / HTTP/1.1\r\nHost: %s\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		host, uint64(3)<<62, strings.Repeat("x", 64<<10))
	_, err = io.WriteString(connection, request)
	require.NoError(t, err)
	response, err := http.ReadResponse(bufio.NewReader(connection), nil)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusRequestEntityTooLarge, response.StatusCode)
}
