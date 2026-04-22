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

package executors

import (
	"context"
	"net/http"
	"testing"

	"github.com/haproxytech/client-native/v6/models"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client/testutil"
)

// --- User (child of userlist container) ---

func TestUserCreate_AllVersions(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/v3/services/haproxy/configuration/users": testutil.StatusResponse(http.StatusCreated),
	}
	runAcrossVersions(t, handlers, func(t *testing.T, c *client.DataplaneClient) {
		t.Helper()
		err := UserCreate("authlist")(context.Background(), c, "tx-1", "authlist", "alice",
			&models.User{Username: "alice", Password: "secret"})
		require.NoError(t, err)
	})
}

func TestUserUpdate_AllVersions(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/v3/services/haproxy/configuration/users/alice": testutil.StatusResponse(http.StatusOK),
	}
	runAcrossVersions(t, handlers, func(t *testing.T, c *client.DataplaneClient) {
		t.Helper()
		err := UserUpdate("authlist")(context.Background(), c, "tx-1", "authlist", "alice",
			&models.User{Username: "alice", Password: "secret2"})
		require.NoError(t, err)
	})
}

func TestUserDelete_AllVersions(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/v3/services/haproxy/configuration/users/alice": testutil.StatusResponse(http.StatusAccepted),
	}
	runAcrossVersions(t, handlers, func(t *testing.T, c *client.DataplaneClient) {
		t.Helper()
		err := UserDelete("authlist")(context.Background(), c, "tx-1", "authlist", "alice", nil)
		require.NoError(t, err)
	})
}

// --- MailerEntry (child of mailers_section container) ---

func TestMailerEntryCreate_AllVersions(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/v3/services/haproxy/configuration/mailer_entries": testutil.StatusResponse(http.StatusCreated),
	}
	runAcrossVersions(t, handlers, func(t *testing.T, c *client.DataplaneClient) {
		t.Helper()
		err := MailerEntryCreate("mailers")(context.Background(), c, "tx-1", "mailers", "m1",
			&models.MailerEntry{Name: "m1", Address: "smtp.example.com", Port: 25})
		require.NoError(t, err)
	})
}

func TestMailerEntryUpdate_AllVersions(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/v3/services/haproxy/configuration/mailer_entries/m1": testutil.StatusResponse(http.StatusOK),
	}
	runAcrossVersions(t, handlers, func(t *testing.T, c *client.DataplaneClient) {
		t.Helper()
		err := MailerEntryUpdate("mailers")(context.Background(), c, "tx-1", "mailers", "m1",
			&models.MailerEntry{Name: "m1", Address: "smtp2.example.com", Port: 25})
		require.NoError(t, err)
	})
}

func TestMailerEntryDelete_AllVersions(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/v3/services/haproxy/configuration/mailer_entries/m1": testutil.StatusResponse(http.StatusAccepted),
	}
	runAcrossVersions(t, handlers, func(t *testing.T, c *client.DataplaneClient) {
		t.Helper()
		err := MailerEntryDelete("mailers")(context.Background(), c, "tx-1", "mailers", "m1", nil)
		require.NoError(t, err)
	})
}

// --- PeerEntry (child of peer_section container) ---

func TestPeerEntryCreate_AllVersions(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/v3/services/haproxy/configuration/peer_entries": testutil.StatusResponse(http.StatusCreated),
	}
	runAcrossVersions(t, handlers, func(t *testing.T, c *client.DataplaneClient) {
		t.Helper()
		err := PeerEntryCreate("peers")(context.Background(), c, "tx-1", "peers", "peer1",
			&models.PeerEntry{Name: "peer1", Address: strPtr("10.0.0.1"), Port: ptrInt64(1024)})
		require.NoError(t, err)
	})
}

func TestPeerEntryUpdate_AllVersions(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/v3/services/haproxy/configuration/peer_entries/peer1": testutil.StatusResponse(http.StatusOK),
	}
	runAcrossVersions(t, handlers, func(t *testing.T, c *client.DataplaneClient) {
		t.Helper()
		err := PeerEntryUpdate("peers")(context.Background(), c, "tx-1", "peers", "peer1",
			&models.PeerEntry{Name: "peer1", Address: strPtr("10.0.0.2"), Port: ptrInt64(1024)})
		require.NoError(t, err)
	})
}

func TestPeerEntryDelete_AllVersions(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/v3/services/haproxy/configuration/peer_entries/peer1": testutil.StatusResponse(http.StatusAccepted),
	}
	runAcrossVersions(t, handlers, func(t *testing.T, c *client.DataplaneClient) {
		t.Helper()
		err := PeerEntryDelete("peers")(context.Background(), c, "tx-1", "peers", "peer1", nil)
		require.NoError(t, err)
	})
}

// --- Nameserver (child of resolvers container) ---

func TestNameserverCreate_AllVersions(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/v3/services/haproxy/configuration/nameservers": testutil.StatusResponse(http.StatusCreated),
	}
	runAcrossVersions(t, handlers, func(t *testing.T, c *client.DataplaneClient) {
		t.Helper()
		err := NameserverCreate("dns")(context.Background(), c, "tx-1", "dns", "ns1",
			&models.Nameserver{Name: "ns1", Address: strPtr("8.8.8.8"), Port: ptrInt64(53)})
		require.NoError(t, err)
	})
}

func TestNameserverUpdate_AllVersions(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/v3/services/haproxy/configuration/nameservers/ns1": testutil.StatusResponse(http.StatusOK),
	}
	runAcrossVersions(t, handlers, func(t *testing.T, c *client.DataplaneClient) {
		t.Helper()
		err := NameserverUpdate("dns")(context.Background(), c, "tx-1", "dns", "ns1",
			&models.Nameserver{Name: "ns1", Address: strPtr("1.1.1.1"), Port: ptrInt64(53)})
		require.NoError(t, err)
	})
}

func TestNameserverDelete_AllVersions(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/v3/services/haproxy/configuration/nameservers/ns1": testutil.StatusResponse(http.StatusAccepted),
	}
	runAcrossVersions(t, handlers, func(t *testing.T, c *client.DataplaneClient) {
		t.Helper()
		err := NameserverDelete("dns")(context.Background(), c, "tx-1", "dns", "ns1", nil)
		require.NoError(t, err)
	})
}
