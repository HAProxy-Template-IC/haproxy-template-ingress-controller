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
	"context"
	"crypto/x509"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	agentclient "gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/transportsecurity"
	"gitlab.com/haproxy-haptic/haptic/pkg/transportsecurity/issuance"
	"gitlab.com/haproxy-haptic/haptic/pkg/transportsecurity/tlstest"
)

func TestLocalAgentStateSocketRejectsMutations(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "admin.sock")
	h := newHarness(t, func(o *options) { o.adminSocket = socket })
	firstApply(t, h)
	client, err := agentclient.New(&agentclient.Config{BaseURL: "http://localhost", UnixSocket: socket})
	require.NoError(t, err)
	t.Cleanup(client.Close)
	state, err := client.State(t.Context(), api.StateRead{Verify: true})
	require.NoError(t, err)
	require.Equal(t, "plan-1", state.RunningPlanID)
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	local := &http.Client{Transport: transport, Timeout: time.Second}
	t.Cleanup(local.CloseIdleConnections)
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{http.MethodPost, api.PathApply, http.StatusNotFound},
		{http.MethodPut, api.PathPlan, http.StatusNotFound},
		{http.MethodPost, api.PathState, http.StatusMethodNotAllowed},
	} {
		request, err := http.NewRequestWithContext(t.Context(), tc.method, "http://localhost"+tc.path, http.NoBody)
		require.NoError(t, err)
		response, err := local.Do(request)
		require.NoError(t, err)
		response.Body.Close()
		require.Equal(t, tc.status, response.StatusCode)
	}
	require.Equal(t, "plan-1", h.state(false).RunningPlanID)
	h.stop()
	_, err = os.Stat(socket)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestLocalAgentHealthSurvivesExpiredIdentity(t *testing.T) {
	ca := tlstest.NewAuthority(t, "agent CA")
	directory := filepath.Join(t.TempDir(), "active")
	identity := tlstest.NewIdentity(t, ca, "agent.test", x509.ExtKeyUsageServerAuth)
	tlstest.Publish(t, directory, identity, ca.PEM, nil, time.Time{})
	source, err := transportsecurity.NewSource(directory, "controller.test")
	require.NoError(t, err)
	socket := filepath.Join(t.TempDir(), "admin.sock")
	h := newHarness(t, func(o *options) { o.adminSocket, o.serverTLS = socket, source })
	client, err := agentclient.New(&agentclient.Config{BaseURL: "http://localhost", UnixSocket: socket})
	require.NoError(t, err)
	t.Cleanup(client.Close)
	require.NoError(t, client.Health(t.Context()))

	past := time.Now().Add(-3 * time.Hour)
	expiredCA, err := issuance.NewAuthority("expired CA", past, time.Hour)
	require.NoError(t, err)
	expired, err := expiredCA.Issue("agent.test", x509.ExtKeyUsageServerAuth, past, time.Hour)
	require.NoError(t, err)
	expiredRoot, err := expiredCA.KeyPair()
	require.NoError(t, err)
	tlstest.Publish(t, directory, tlstest.Identity{Certificate: expired.Certificate, Key: expired.PrivateKey}, expiredRoot.Certificate, nil, time.Time{})
	require.NoError(t, client.Health(t.Context()), "certificate expiry must not kill the renewing process")
	_, err = source.ServerConfig().GetConfigForClient(nil)
	require.Error(t, err, "expired identity must still reject network TLS handshakes")

	tlstest.Publish(t, directory, identity, ca.PEM, nil, time.Time{})
	require.NoError(t, client.Health(t.Context()))
	_, err = source.ServerConfig().GetConfigForClient(nil)
	require.NoError(t, err, "fresh certificates must recover without restarting the server")
	h.stop()
	require.Error(t, client.Health(t.Context()), "stopped processes must fail the health probe")
}
