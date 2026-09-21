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

package server_test

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	agentclient "gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/transportsecurity"
	"gitlab.com/haproxy-haptic/haptic/pkg/transportsecurity/tlstest"
)

func TestAgentTLSAuthenticatesControlRequests(t *testing.T) {
	ca := tlstest.NewAuthority(t, "agent CA")
	serverDir, clientDir := filepath.Join(t.TempDir(), "active"), filepath.Join(t.TempDir(), "active")
	tlstest.Publish(t, serverDir, tlstest.NewIdentity(t, ca, "agent.test", x509.ExtKeyUsageServerAuth), ca.PEM, nil, time.Time{})
	tlstest.Publish(t, clientDir, tlstest.NewIdentity(t, ca, "controller.test", x509.ExtKeyUsageClientAuth), ca.PEM, nil, time.Time{})
	serverSource, err := transportsecurity.NewSource(serverDir, "controller.test")
	require.NoError(t, err)
	clientSource, err := transportsecurity.NewSource(clientDir, "agent.test")
	require.NoError(t, err)
	h := newHarness(t, func(o *options) { o.serverTLS, o.clientTLS = serverSource, clientSource })
	firstApply(t, h)
	client, err := agentclient.New(&agentclient.Config{BaseURL: h.url, TLS: clientSource})
	require.NoError(t, err)
	t.Cleanup(client.Close)
	state, err := client.State(t.Context(), api.StateRead{})
	require.NoError(t, err)
	require.Equal(t, "plan-1", state.AppliedPlanID)

	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM(ca.PEM))
	anonymous := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: "agent.test",
	}}, Timeout: 5 * time.Second}
	t.Cleanup(anonymous.CloseIdleConnections)
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{http.MethodGet, "/healthz", http.StatusOK},
		{http.MethodGet, api.PathState, http.StatusUnauthorized},
		{http.MethodPost, api.PathApply, http.StatusUnauthorized},
		{http.MethodPut, api.PathPlan, http.StatusUnauthorized},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			request, err := http.NewRequestWithContext(t.Context(), tc.method, h.url+tc.path, http.NoBody)
			require.NoError(t, err)
			request.SetBasicAuth(testUser, testPassword)
			response, err := anonymous.Do(request)
			require.NoError(t, err)
			defer response.Body.Close()
			_, err = io.Copy(io.Discard, response.Body)
			require.NoError(t, err)
			require.Equal(t, tc.status, response.StatusCode)
		})
	}
	require.Equal(t, "plan-1", h.state(false).AppliedPlanID)
}

func TestAgentTLSReloadsTrustWithoutRestart(t *testing.T) {
	oldCA, newCA := tlstest.NewAuthority(t, "old"), tlstest.NewAuthority(t, "new")
	oldServer := tlstest.NewIdentity(t, oldCA, "agent.test", x509.ExtKeyUsageServerAuth)
	oldClient := tlstest.NewIdentity(t, oldCA, "controller.test", x509.ExtKeyUsageClientAuth)
	newServer := tlstest.NewIdentity(t, newCA, "agent.test", x509.ExtKeyUsageServerAuth)
	newClient := tlstest.NewIdentity(t, newCA, "controller.test", x509.ExtKeyUsageClientAuth)
	serverDir, clientDir := filepath.Join(t.TempDir(), "active"), filepath.Join(t.TempDir(), "active")
	tlstest.Publish(t, serverDir, oldServer, oldCA.PEM, nil, time.Time{})
	tlstest.Publish(t, clientDir, oldClient, oldCA.PEM, nil, time.Time{})
	serverSource, err := transportsecurity.NewSource(serverDir, "controller.test")
	require.NoError(t, err)
	clientSource, err := transportsecurity.NewSource(clientDir, "agent.test")
	require.NoError(t, err)
	h := newHarness(t, func(o *options) { o.serverTLS, o.clientTLS = serverSource, clientSource })
	first := firstApply(t, h)
	until := time.Now().Add(time.Hour)
	tlstest.Publish(t, serverDir, oldServer, newCA.PEM, oldCA.PEM, until)
	tlstest.Publish(t, clientDir, oldClient, newCA.PEM, oldCA.PEM, until)
	tlstest.Publish(t, serverDir, newServer, newCA.PEM, oldCA.PEM, until)
	require.Equal(t, "plan-1", h.state(false).AppliedPlanID)
	tlstest.Publish(t, clientDir, newClient, newCA.PEM, oldCA.PEM, until)
	list := baseFiles("global\n# rotated\n")
	manifest := buildManifest("plan-2", list)
	manifest.Mode = api.ModeReload
	manifest.ExpectedPrevPlanID = first.AppliedPlanID
	manifest.ExpectedPrevToken = first.AppliedToken
	result := h.apply(&manifest, list)
	require.True(t, result.OK, "%+v", result.Error)
	tlstest.Publish(t, serverDir, newServer, newCA.PEM, nil, time.Time{})
	tlstest.Publish(t, clientDir, newClient, newCA.PEM, nil, time.Time{})
	require.Equal(t, "plan-2", h.state(false).RunningPlanID)
	require.Equal(t, list[0].Content, h.read(configPath))
}
