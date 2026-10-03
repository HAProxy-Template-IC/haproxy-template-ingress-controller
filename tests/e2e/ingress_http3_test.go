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
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

// TestIngressHTTP3 covers the chart's default-on HTTP/3: a TLS Ingress is
// advertised over alt-svc and served over QUIC through the HAProxy Service's
// UDP port. The QUIC request runs from an HAProxy pod: its image ships an
// HTTP/3-capable curl, and the kind port mappings forward TCP only.
func TestIngressHTTP3(t *testing.T) {
	t.Parallel()
	RunSimpleIngressTest(t, &SimpleIngressTest{
		Description:   "Ingress: HTTP/3 over QUIC",
		Host:          "ingress-http3.localdev.me",
		TLSSecretName: "ingress-http3-tls",
		Assess: []SimpleIngressAssertion{
			{
				Name: "HTTPS response advertises HTTP/3 on the port the client used",
				Check: func(t *testing.T, host string) {
					t.Helper()
					resp := httpclient.New(t).HTTPS(host, "/").ExpectOK(t)
					// The client sends Host without a port, i.e. it used 443.
					require.Equal(t, `h3=":443"; ma=86400`, resp.Header.Get("Alt-Svc"))
				},
			},
			{
				Name: "HTTP/3-only request through the Service's UDP port is served",
				Check: func(t *testing.T, host string) {
					t.Helper()
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
					defer cancel()
					clusterIP := haproxyServiceClusterIP(ctx, t)
					pods := listHAProxyPods(t)
					require.NotEmpty(t, pods, "no HAProxy pod to run curl from")

					require.EventuallyWithT(t, func(c *assert.CollectT) {
						out, err := execInHAProxyPod(ctx, pods[0], "haproxy", "curl",
							"-sk", "--http3-only", "--connect-timeout", "3", "--max-time", "10",
							"--resolve", host+":443:"+clusterIP,
							"-o", "/dev/null", "-w", "%{http_code} %{http_version}",
							"https://"+host+"/")
						assert.NoError(c, err)
						assert.Equal(c, "200 3", strings.TrimSpace(out), "status and HTTP version via %s", clusterIP)
					}, time.Minute, time.Second)
				},
			},
		},
	})
}

func haproxyServiceClusterIP(ctx context.Context, t *testing.T) string {
	t.Helper()
	out, err := exec.CommandContext(ctx, "kubectl",
		kubeconfigFlag, kubeconfigPath, "-n", ControllerNamespace,
		"get", "service", HelmReleaseName+"-haproxy",
		"-o", "jsonpath={.spec.clusterIP}").Output()
	require.NoError(t, err)
	clusterIP := strings.TrimSpace(string(out))
	require.NotEmpty(t, clusterIP)
	return clusterIP
}
