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

package enterprise

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewOperations(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{})
	defer server.Close()

	c := newTestClient(t, server)
	ops := NewOperations(c)

	require.NotNil(t, ops)
	assert.Equal(t, c, ops.client)
}

func TestOperations_Client(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{})
	defer server.Close()

	c := newTestClient(t, server)
	ops := NewOperations(c)

	assert.Equal(t, c, ops.Client())
}

func TestOperations_IsAvailable_Enterprise(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		apiVersion: testEnterpriseAPIVersion,
	})
	defer server.Close()

	c := newTestClient(t, server)
	ops := NewOperations(c)

	assert.True(t, ops.IsAvailable())
}

func TestOperations_IsAvailable_Community(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		apiVersion: testCommunityAPIVersion,
	})
	defer server.Close()

	c := newTestClient(t, server)
	ops := NewOperations(c)

	assert.False(t, ops.IsAvailable())
}

func TestOperations_Capabilities_Enterprise(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		apiVersion: testEnterpriseAPIVersion, // v3.2-ee1
	})
	defer server.Close()

	c := newTestClient(t, server)
	ops := NewOperations(c)

	caps := ops.Capabilities()

	// Enterprise v3.2 should have all enterprise capabilities
	assert.True(t, caps.SupportsWAF)
	assert.True(t, caps.SupportsKeepalived)
	assert.True(t, caps.SupportsUDPLoadBalancing)
	assert.True(t, caps.SupportsBotManagement)
	assert.True(t, caps.SupportsGitIntegration)
	assert.True(t, caps.SupportsDynamicUpdate)
	assert.True(t, caps.SupportsALOHA)
	assert.True(t, caps.SupportsAdvancedLogging)
	assert.True(t, caps.SupportsPing)
}

func TestOperations_Capabilities_Community(t *testing.T) {
	server := newMockEnterpriseServer(t, mockServerConfig{
		apiVersion: testCommunityAPIVersion,
	})
	defer server.Close()

	c := newTestClient(t, server)
	ops := NewOperations(c)

	caps := ops.Capabilities()

	// Community should not have enterprise capabilities
	assert.False(t, caps.SupportsWAF)
	assert.False(t, caps.SupportsKeepalived)
	assert.False(t, caps.SupportsUDPLoadBalancing)
	assert.False(t, caps.SupportsBotManagement)
	assert.False(t, caps.SupportsGitIntegration)
	assert.False(t, caps.SupportsDynamicUpdate)
	assert.False(t, caps.SupportsALOHA)
	assert.False(t, caps.SupportsAdvancedLogging)
}

func TestErrNotFound(t *testing.T) {
	// Verify ErrNotFound is defined and has a message
	assert.NotNil(t, ErrNotFound)
	assert.Contains(t, ErrNotFound.Error(), "not found")
}
