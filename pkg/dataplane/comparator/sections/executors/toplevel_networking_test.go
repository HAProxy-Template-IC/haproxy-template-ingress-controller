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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client/testutil"
)

func strPtr(s string) *string { return &s }

// --- Cache ---

func TestCacheCreate_AllVersions(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/v3/services/haproxy/configuration/caches": testutil.StatusResponse(http.StatusCreated),
	}
	runAcrossVersions(t, handlers, func(t *testing.T, c *client.DataplaneClient) {
		t.Helper()
		err := CacheCreate()(context.Background(), c, "tx-1", &models.Cache{Name: strPtr("cache1")}, "cache1")
		require.NoError(t, err)
	})
}

func TestCacheUpdate_AllVersions(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/v3/services/haproxy/configuration/caches/cache1": testutil.StatusResponse(http.StatusOK),
	}
	runAcrossVersions(t, handlers, func(t *testing.T, c *client.DataplaneClient) {
		t.Helper()
		err := CacheUpdate()(context.Background(), c, "tx-1", &models.Cache{Name: strPtr("cache1")}, "cache1")
		require.NoError(t, err)
	})
}

func TestCacheDelete_AllVersions(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/v3/services/haproxy/configuration/caches/cache1": testutil.StatusResponse(http.StatusAccepted),
	}
	runAcrossVersions(t, handlers, func(t *testing.T, c *client.DataplaneClient) {
		t.Helper()
		err := CacheDelete()(context.Background(), c, "tx-1", nil, "cache1")
		require.NoError(t, err)
	})
}

// --- HTTPErrorsSection ---

func TestHTTPErrorsSectionCreate_AllVersions(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/v3/services/haproxy/configuration/http_errors_sections": testutil.StatusResponse(http.StatusCreated),
	}
	runAcrossVersions(t, handlers, func(t *testing.T, c *client.DataplaneClient) {
		t.Helper()
		err := HTTPErrorsSectionCreate()(context.Background(), c, "tx-1",
			&models.HTTPErrorsSection{Name: "errs"}, "errs")
		require.NoError(t, err)
	})
}

func TestHTTPErrorsSectionUpdate_AllVersions(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/v3/services/haproxy/configuration/http_errors_sections/errs": testutil.StatusResponse(http.StatusOK),
	}
	runAcrossVersions(t, handlers, func(t *testing.T, c *client.DataplaneClient) {
		t.Helper()
		err := HTTPErrorsSectionUpdate()(context.Background(), c, "tx-1",
			&models.HTTPErrorsSection{Name: "errs"}, "errs")
		require.NoError(t, err)
	})
}

func TestHTTPErrorsSectionDelete_AllVersions(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/v3/services/haproxy/configuration/http_errors_sections/errs": testutil.StatusResponse(http.StatusAccepted),
	}
	runAcrossVersions(t, handlers, func(t *testing.T, c *client.DataplaneClient) {
		t.Helper()
		err := HTTPErrorsSectionDelete()(context.Background(), c, "tx-1", nil, "errs")
		require.NoError(t, err)
	})
}

// --- LogForward ---

func TestLogForwardCreate_AllVersions(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/v3/services/haproxy/configuration/log_forwards": testutil.StatusResponse(http.StatusCreated),
	}
	runAcrossVersions(t, handlers, func(t *testing.T, c *client.DataplaneClient) {
		t.Helper()
		err := LogForwardCreate()(context.Background(), c, "tx-1",
			&models.LogForward{LogForwardBase: models.LogForwardBase{Name: "logf"}}, "logf")
		require.NoError(t, err)
	})
}

func TestLogForwardUpdate_AllVersions(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/v3/services/haproxy/configuration/log_forwards/logf": testutil.StatusResponse(http.StatusOK),
	}
	runAcrossVersions(t, handlers, func(t *testing.T, c *client.DataplaneClient) {
		t.Helper()
		err := LogForwardUpdate()(context.Background(), c, "tx-1",
			&models.LogForward{LogForwardBase: models.LogForwardBase{Name: "logf"}}, "logf")
		require.NoError(t, err)
	})
}

func TestLogForwardDelete_AllVersions(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/v3/services/haproxy/configuration/log_forwards/logf": testutil.StatusResponse(http.StatusAccepted),
	}
	runAcrossVersions(t, handlers, func(t *testing.T, c *client.DataplaneClient) {
		t.Helper()
		err := LogForwardDelete()(context.Background(), c, "tx-1", nil, "logf")
		require.NoError(t, err)
	})
}

// --- PeerSection ---

func TestPeerSectionCreate_AllVersions(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/v3/services/haproxy/configuration/peer_section": testutil.StatusResponse(http.StatusCreated),
	}
	runAcrossVersions(t, handlers, func(t *testing.T, c *client.DataplaneClient) {
		t.Helper()
		err := PeerSectionCreate()(context.Background(), c, "tx-1",
			&models.PeerSection{PeerSectionBase: models.PeerSectionBase{Name: "peers"}}, "peers")
		require.NoError(t, err)
	})
}

// PeerSectionUpdate is not supported by the DataPlane API — the executor returns a
// hard-coded error without touching the network, so parameterizing across versions
// adds no coverage. Tested here once.
func TestPeerSectionUpdate_NotSupported(t *testing.T) {
	err := PeerSectionUpdate()(context.Background(), nil, "tx-1",
		&models.PeerSection{PeerSectionBase: models.PeerSectionBase{Name: "peers"}}, "peers")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not supported")
	assert.Contains(t, err.Error(), "peers")
}

func TestPeerSectionDelete_AllVersions(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/v3/services/haproxy/configuration/peer_section/peers": testutil.StatusResponse(http.StatusAccepted),
	}
	runAcrossVersions(t, handlers, func(t *testing.T, c *client.DataplaneClient) {
		t.Helper()
		err := PeerSectionDelete()(context.Background(), c, "tx-1", nil, "peers")
		require.NoError(t, err)
	})
}
