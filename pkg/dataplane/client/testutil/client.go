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

package testutil

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
)

const (
	TestUsername = "admin"
	TestPassword = "password"
)

// NewTestClient creates a client connected to a test server.
func NewTestClient(t *testing.T, server *httptest.Server) *client.DataplaneClient {
	t.Helper()

	c, err := client.New(context.Background(), &client.Config{
		BaseURL:  server.URL,
		Username: TestUsername,
		Password: TestPassword,
	})
	require.NoError(t, err, "failed to create test client")
	return c
}
