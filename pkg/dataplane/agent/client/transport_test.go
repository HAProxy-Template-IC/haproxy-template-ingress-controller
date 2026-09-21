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

package client

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/transportsecurity"
)

func TestAgentClientRejectsTransportMismatch(t *testing.T) {
	for _, cfg := range []Config{
		{BaseURL: "http://agent.test", TLS: &transportsecurity.Source{}},
		{BaseURL: "https://agent.test", UnixSocket: "agent.sock"},
		{BaseURL: "http://localhost", UnixSocket: "agent.sock", TLS: &transportsecurity.Source{}},
	} {
		_, err := New(&cfg)
		require.Error(t, err)
	}
}

func TestAgentClientDoesNotFollowRedirects(t *testing.T) {
	var received atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		received.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	client := newTestClient(t, origin.URL)
	_, err := client.State(t.Context(), api.StateRead{})
	require.Error(t, err)
	require.False(t, received.Load())
}

func TestAgentClientCopiesSocketConfiguration(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "agent.sock")
	listener, err := net.Listen("unix", socket)
	require.NoError(t, err)
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Empty(t, r.Header.Get("Authorization"))
		writeJSON(t, w, http.StatusOK, api.State{APIVersion: api.Version, AgentVersion: "local"})
	})}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		require.NoError(t, server.Shutdown(context.Background()))
		require.ErrorIs(t, <-done, http.ErrServerClosed)
	})
	cfg := &Config{BaseURL: "http://localhost", UnixSocket: socket, Username: "unused", Password: "unused"}
	client, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(client.Close)
	cfg.UnixSocket = filepath.Join(t.TempDir(), "wrong.sock")
	state, err := client.State(t.Context(), api.StateRead{})
	require.NoError(t, err)
	require.Equal(t, "local", state.AgentVersion)
}
