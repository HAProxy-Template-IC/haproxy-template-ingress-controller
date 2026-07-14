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
	"testing"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

// TestHapticCompression verifies response compression (72-compression.yaml):
// with compression enabled and a matching Content-Type, HAProxy gzips the
// response when the client advertises Accept-Encoding: gzip.
//
// The client sets Accept-Encoding manually so Go's transport does not
// transparently decompress (it only does that for the header it adds itself),
// leaving Content-Encoding: gzip visible on the response.
func TestHapticCompression(t *testing.T) {
	t.Parallel()
	RunSimpleIngressTest(t, SimpleIngressTest{
		Description: "Ingress: HAPTIC-native response compression",
		Host:        "ingress-haptic-compression.localdev.me",
		Annotations: map[string]string{
			"haproxy-haptic.org/compress-enable":    "true",
			"haproxy-haptic.org/compress-algorithm": "gzip",
			"haproxy-haptic.org/compress-types":     "application/json,text/plain,text/html",
		},
		Assess: []SimpleIngressAssertion{
			{
				Name: "gzip-advertised request gets a gzipped response",
				Check: func(t *testing.T, host string) {
					httpclient.New(t).GET(host, "/").
						WithHeader("Accept-Encoding", "gzip").
						ExpectHeader(t, "Content-Encoding", "gzip")
				},
			},
		},
	})
}
