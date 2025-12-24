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

package rendercontext

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/dataplane"
	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/k8s/types"
)

// mockStore is a simple mock implementation of types.Store for testing.
type mockStore struct {
	items []interface{}
}

func (m *mockStore) List() ([]interface{}, error) {
	return m.items, nil
}

func (m *mockStore) Get(_ ...string) ([]interface{}, error) {
	return m.items, nil
}

func (m *mockStore) Add(_ interface{}, _ []string) error {
	return nil
}

func (m *mockStore) Update(_ interface{}, _ []string) error {
	return nil
}

func (m *mockStore) Delete(_ ...string) error {
	return nil
}

func (m *mockStore) Clear() error {
	return nil
}

func TestSeparateHAProxyPodStore(t *testing.T) {
	tests := []struct {
		name                 string
		stores               map[string]types.Store
		wantResourceCount    int
		wantHAProxyPodStore  bool
		wantResourceStoreKey string
	}{
		{
			name:                "empty stores",
			stores:              map[string]types.Store{},
			wantResourceCount:   0,
			wantHAProxyPodStore: false,
		},
		{
			name: "only resource stores",
			stores: map[string]types.Store{
				"ingresses": &mockStore{},
				"services":  &mockStore{},
			},
			wantResourceCount:   2,
			wantHAProxyPodStore: false,
		},
		{
			name: "only haproxy-pods",
			stores: map[string]types.Store{
				"haproxy-pods": &mockStore{},
			},
			wantResourceCount:   0,
			wantHAProxyPodStore: true,
		},
		{
			name: "mixed stores",
			stores: map[string]types.Store{
				"ingresses":    &mockStore{},
				"haproxy-pods": &mockStore{},
				"services":     &mockStore{},
			},
			wantResourceCount:   2,
			wantHAProxyPodStore: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resourceStores, haproxyPodStore := SeparateHAProxyPodStore(tt.stores)

			assert.Equal(t, tt.wantResourceCount, len(resourceStores))

			if tt.wantHAProxyPodStore {
				assert.NotNil(t, haproxyPodStore)
			} else {
				assert.Nil(t, haproxyPodStore)
			}

			// Verify haproxy-pods is not in resource stores
			_, exists := resourceStores["haproxy-pods"]
			assert.False(t, exists, "haproxy-pods should not be in resource stores")
		})
	}
}

func TestPathResolverFromValidationPaths(t *testing.T) {
	validationPaths := &dataplane.ValidationPaths{
		MapsDir:           "/tmp/maps",
		SSLCertsDir:       "/tmp/certs",
		CRTListDir:        "/tmp/crt-list",
		GeneralStorageDir: "/tmp/general",
	}

	pathResolver := PathResolverFromValidationPaths(validationPaths)

	require.NotNil(t, pathResolver)
	assert.Equal(t, "/tmp/maps", pathResolver.MapsDir)
	assert.Equal(t, "/tmp/certs", pathResolver.SSLDir)
	assert.Equal(t, "/tmp/crt-list", pathResolver.CRTListDir)
	assert.Equal(t, "/tmp/general", pathResolver.GeneralDir)
}
