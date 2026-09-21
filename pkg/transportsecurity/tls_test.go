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

package transportsecurity

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/haproxy-haptic/haptic/pkg/transportsecurity/tlstest"
)

func securedServer(t *testing.T, source *Source) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			if err := source.VerifyClient(r.TLS); err != nil {
				http.Error(w, "client identity rejected", http.StatusUnauthorized)
				return
			}
		}
		_, _ = w.Write([]byte("ok"))
	}))
	server.TLS = source.ServerConfig()
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

func securedClient(t *testing.T, directory string) *http.Client {
	t.Helper()
	source, err := NewSource(directory, "agent.example.test")
	require.NoError(t, err)
	transport := NewTransport(source, &http.Transport{})
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 5 * time.Second}
}

func requireStatus(t *testing.T, client *http.Client, url string, status int) {
	t.Helper()
	response, err := client.Get(url)
	require.NoError(t, err)
	defer response.Body.Close()
	_, err = io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, status, response.StatusCode)
}

func TestTLSRejectsUnauthorizedPeers(t *testing.T) {
	ca := tlstest.NewAuthority(t, "CA")
	serverDirectory := filepath.Join(t.TempDir(), "active")
	serverIdentity := tlstest.NewIdentity(t, ca, "agent.example.test", x509.ExtKeyUsageServerAuth)
	tlstest.Publish(t, serverDirectory, serverIdentity, ca.PEM, nil, time.Time{})
	serverSource, err := NewSource(serverDirectory, "controller.example.test")
	require.NoError(t, err)
	server := securedServer(t, serverSource)
	for _, tc := range []struct {
		name     string
		identity tlstest.Identity
		status   int
	}{
		{name: "controller", identity: tlstest.NewIdentity(t, ca, "controller.example.test", x509.ExtKeyUsageClientAuth), status: http.StatusOK},
		{name: "different role", identity: tlstest.NewIdentity(t, ca, "other.example.test", x509.ExtKeyUsageClientAuth), status: http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "active")
			tlstest.Publish(t, directory, tc.identity, ca.PEM, nil, time.Time{})
			requireStatus(t, securedClient(t, directory), server.URL, tc.status)
		})
	}
	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM(ca.PEM))
	anonymous := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: "agent.example.test",
	}}, Timeout: 5 * time.Second}
	t.Cleanup(anonymous.CloseIdleConnections)
	requireStatus(t, anonymous, server.URL+"/healthz", http.StatusOK)
	requireStatus(t, anonymous, server.URL, http.StatusUnauthorized)
}

func TestTLSRotationRevokesReusedConnections(t *testing.T) {
	oldCA, newCA := tlstest.NewAuthority(t, "old"), tlstest.NewAuthority(t, "new")
	oldServer := tlstest.NewIdentity(t, oldCA, "agent.example.test", x509.ExtKeyUsageServerAuth)
	newServer := tlstest.NewIdentity(t, newCA, "agent.example.test", x509.ExtKeyUsageServerAuth)
	oldClient := tlstest.NewIdentity(t, oldCA, "controller.example.test", x509.ExtKeyUsageClientAuth)
	newClient := tlstest.NewIdentity(t, newCA, "controller.example.test", x509.ExtKeyUsageClientAuth)
	serverDirectory, clientDirectory := filepath.Join(t.TempDir(), "active"), filepath.Join(t.TempDir(), "active")
	tlstest.Publish(t, serverDirectory, oldServer, oldCA.PEM, nil, time.Time{})
	tlstest.Publish(t, clientDirectory, oldClient, oldCA.PEM, nil, time.Time{})
	serverSource, err := NewSource(serverDirectory, "controller.example.test")
	require.NoError(t, err)
	server := securedServer(t, serverSource)
	client := securedClient(t, clientDirectory)
	requireStatus(t, client, server.URL, http.StatusOK)

	until := time.Now().Add(time.Hour)
	tlstest.Publish(t, serverDirectory, oldServer, newCA.PEM, oldCA.PEM, until)
	tlstest.Publish(t, clientDirectory, oldClient, newCA.PEM, oldCA.PEM, until)
	requireStatus(t, client, server.URL, http.StatusOK)
	tlstest.Publish(t, serverDirectory, newServer, newCA.PEM, oldCA.PEM, until)
	requireStatus(t, client, server.URL, http.StatusOK)
	oldDirectory := filepath.Join(t.TempDir(), "active")
	tlstest.Publish(t, oldDirectory, oldClient, newCA.PEM, oldCA.PEM, until)
	oldPool := securedClient(t, oldDirectory)
	requireStatus(t, oldPool, server.URL, http.StatusOK)

	tlstest.Publish(t, clientDirectory, newClient, newCA.PEM, oldCA.PEM, until)
	requireStatus(t, client, server.URL, http.StatusOK)
	tlstest.Publish(t, serverDirectory, newServer, newCA.PEM, nil, time.Time{})
	requireStatus(t, oldPool, server.URL, http.StatusUnauthorized)
	requireStatus(t, client, server.URL, http.StatusOK)

	tlstest.Publish(t, clientDirectory, newClient, newCA.PEM, nil, time.Time{})
	requireStatus(t, client, server.URL, http.StatusOK)
	tlstest.Publish(t, serverDirectory, oldServer, newCA.PEM, nil, time.Time{})
	client.CloseIdleConnections()
	response, err := client.Get(server.URL)
	if response != nil {
		response.Body.Close()
	}
	require.Error(t, err)
}

func TestTLSTransportRejectsPlaintext(t *testing.T) {
	transport := &Transport{}
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/", http.NoBody)
	response, err := transport.RoundTrip(request)
	if response != nil {
		response.Body.Close()
	}
	require.ErrorContains(t, err, "HTTPS endpoint")
}

func TestTLSRejectsWrongServerIdentity(t *testing.T) {
	ca, foreign := tlstest.NewAuthority(t, "trusted"), tlstest.NewAuthority(t, "foreign")
	clientDir := filepath.Join(t.TempDir(), "active")
	tlstest.Publish(t, clientDir, tlstest.NewIdentity(t, ca, "controller.example.test", x509.ExtKeyUsageClientAuth), ca.PEM, nil, time.Time{})
	for _, tc := range []struct {
		name     string
		identity tlstest.Identity
	}{
		{"wrong DNS SAN", tlstest.NewIdentity(t, ca, "other.example.test", x509.ExtKeyUsageServerAuth)},
		{"untrusted CA", tlstest.NewIdentity(t, foreign, "agent.example.test", x509.ExtKeyUsageServerAuth)},
		{"wrong usage", tlstest.NewIdentity(t, ca, "agent.example.test", x509.ExtKeyUsageClientAuth)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "active")
			tlstest.Publish(t, directory, tc.identity, ca.PEM, nil, time.Time{})
			source, err := NewSource(directory, "controller.example.test")
			require.NoError(t, err)
			server := securedServer(t, source)
			response, err := securedClient(t, clientDir).Get(server.URL)
			if response != nil {
				response.Body.Close()
			}
			require.Error(t, err)
		})
	}
}

func TestTLSRejectsWrongClientAuthorityAndUsage(t *testing.T) {
	ca, foreign := tlstest.NewAuthority(t, "trusted"), tlstest.NewAuthority(t, "foreign")
	directory := filepath.Join(t.TempDir(), "active")
	tlstest.Publish(t, directory, tlstest.NewIdentity(t, ca, "agent.example.test", x509.ExtKeyUsageServerAuth), ca.PEM, nil, time.Time{})
	source, err := NewSource(directory, "controller.example.test")
	require.NoError(t, err)
	server := securedServer(t, source)
	for _, tc := range []struct {
		name     string
		identity tlstest.Identity
	}{
		{"untrusted CA", tlstest.NewIdentity(t, foreign, "controller.example.test", x509.ExtKeyUsageClientAuth)},
		{"wrong usage", tlstest.NewIdentity(t, ca, "controller.example.test", x509.ExtKeyUsageServerAuth)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clientDir := filepath.Join(t.TempDir(), "active")
			tlstest.Publish(t, clientDir, tc.identity, ca.PEM, nil, time.Time{})
			response, err := securedClient(t, clientDir).Get(server.URL)
			if response != nil {
				response.Body.Close()
				require.Equal(t, http.StatusUnauthorized, response.StatusCode)
			} else {
				require.Error(t, err)
			}
		})
	}
}
