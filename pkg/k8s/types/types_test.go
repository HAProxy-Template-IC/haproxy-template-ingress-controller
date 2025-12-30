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

package types

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestStoreType_String(t *testing.T) {
	tests := []struct {
		name      string
		storeType StoreType
		want      string
	}{
		{
			name:      "memory store",
			storeType: StoreTypeMemory,
			want:      "memory",
		},
		{
			name:      "cached store",
			storeType: StoreTypeCached,
			want:      "cached",
		},
		{
			name:      "unknown store type",
			storeType: StoreType(99),
			want:      "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.storeType.String()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestChangeStats_Total(t *testing.T) {
	tests := []struct {
		name  string
		stats ChangeStats
		want  int
	}{
		{
			name:  "empty stats",
			stats: ChangeStats{},
			want:  0,
		},
		{
			name:  "only created",
			stats: ChangeStats{Created: 5},
			want:  5,
		},
		{
			name:  "only modified",
			stats: ChangeStats{Modified: 3},
			want:  3,
		},
		{
			name:  "only deleted",
			stats: ChangeStats{Deleted: 2},
			want:  2,
		},
		{
			name:  "all types",
			stats: ChangeStats{Created: 5, Modified: 3, Deleted: 2},
			want:  10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.stats.Total()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestChangeStats_IsEmpty(t *testing.T) {
	tests := []struct {
		name  string
		stats ChangeStats
		want  bool
	}{
		{
			name:  "empty stats",
			stats: ChangeStats{},
			want:  true,
		},
		{
			name:  "with created",
			stats: ChangeStats{Created: 1},
			want:  false,
		},
		{
			name:  "with modified",
			stats: ChangeStats{Modified: 1},
			want:  false,
		},
		{
			name:  "with deleted",
			stats: ChangeStats{Deleted: 1},
			want:  false,
		},
		{
			name:  "only IsInitialSync set",
			stats: ChangeStats{IsInitialSync: true},
			want:  true, // IsInitialSync doesn't count as a change
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.stats.IsEmpty()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestWatcherConfig_SetDefaults(t *testing.T) {
	t.Run("sets default CacheTTL", func(t *testing.T) {
		cfg := &WatcherConfig{}
		cfg.SetDefaults()

		expected := 2*time.Minute + 10*time.Second
		assert.Equal(t, expected, cfg.CacheTTL)
	})

	t.Run("sets default DebounceInterval", func(t *testing.T) {
		cfg := &WatcherConfig{}
		cfg.SetDefaults()

		assert.Equal(t, DefaultDebounceInterval, cfg.DebounceInterval)
	})

	t.Run("sets default Context", func(t *testing.T) {
		cfg := &WatcherConfig{}
		cfg.SetDefaults()

		assert.NotNil(t, cfg.Context)
	})

	t.Run("preserves existing values", func(t *testing.T) {
		ctx := context.Background()
		cfg := &WatcherConfig{
			CacheTTL:         5 * time.Minute,
			DebounceInterval: 1 * time.Second,
			Context:          ctx,
		}
		cfg.SetDefaults()

		assert.Equal(t, 5*time.Minute, cfg.CacheTTL)
		assert.Equal(t, 1*time.Second, cfg.DebounceInterval)
		assert.Equal(t, ctx, cfg.Context)
	})
}

func TestWatcherConfig_Validate(t *testing.T) {
	validCallback := func(Store, ChangeStats) {}

	tests := []struct {
		name        string
		config      WatcherConfig
		wantErr     bool
		errContains string
	}{
		{
			name: "valid config",
			config: WatcherConfig{
				GVR: schema.GroupVersionResource{
					Group:    "networking.k8s.io",
					Version:  "v1",
					Resource: "ingresses",
				},
				IndexBy:  []string{"metadata.namespace", "metadata.name"},
				OnChange: validCallback,
			},
			wantErr: false,
		},
		{
			name: "missing GVR.Resource",
			config: WatcherConfig{
				GVR: schema.GroupVersionResource{
					Group:   "networking.k8s.io",
					Version: "v1",
					// Resource missing
				},
				IndexBy:  []string{"metadata.namespace"},
				OnChange: validCallback,
			},
			wantErr:     true,
			errContains: "GVR.Resource",
		},
		{
			name: "empty IndexBy",
			config: WatcherConfig{
				GVR: schema.GroupVersionResource{
					Group:    "networking.k8s.io",
					Version:  "v1",
					Resource: "ingresses",
				},
				IndexBy:  []string{},
				OnChange: validCallback,
			},
			wantErr:     true,
			errContains: "IndexBy",
		},
		{
			name: "nil IndexBy",
			config: WatcherConfig{
				GVR: schema.GroupVersionResource{
					Group:    "networking.k8s.io",
					Version:  "v1",
					Resource: "ingresses",
				},
				IndexBy:  nil,
				OnChange: validCallback,
			},
			wantErr:     true,
			errContains: "IndexBy",
		},
		{
			name: "nil OnChange callback",
			config: WatcherConfig{
				GVR: schema.GroupVersionResource{
					Group:    "networking.k8s.io",
					Version:  "v1",
					Resource: "ingresses",
				},
				IndexBy:  []string{"metadata.namespace"},
				OnChange: nil,
			},
			wantErr:     true,
			errContains: "OnChange",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestSingleWatcherConfig_SetDefaults(t *testing.T) {
	t.Run("sets default Context", func(t *testing.T) {
		cfg := &SingleWatcherConfig{}
		cfg.SetDefaults()

		assert.NotNil(t, cfg.Context)
	})

	t.Run("preserves existing Context", func(t *testing.T) {
		ctx := context.Background()
		cfg := &SingleWatcherConfig{
			Context: ctx,
		}
		cfg.SetDefaults()

		assert.Equal(t, ctx, cfg.Context)
	})
}

func TestSingleWatcherConfig_Validate(t *testing.T) {
	validCallback := func(interface{}) error { return nil }

	tests := []struct {
		name        string
		config      SingleWatcherConfig
		wantErr     bool
		errContains string
	}{
		{
			name: "valid config",
			config: SingleWatcherConfig{
				GVR: schema.GroupVersionResource{
					Group:    "",
					Version:  "v1",
					Resource: "configmaps",
				},
				Namespace: "default",
				Name:      "my-config",
				OnChange:  validCallback,
			},
			wantErr: false,
		},
		{
			name: "missing GVR.Resource",
			config: SingleWatcherConfig{
				GVR: schema.GroupVersionResource{
					Group:   "",
					Version: "v1",
					// Resource missing
				},
				Namespace: "default",
				Name:      "my-config",
				OnChange:  validCallback,
			},
			wantErr:     true,
			errContains: "GVR.Resource",
		},
		{
			name: "missing Namespace",
			config: SingleWatcherConfig{
				GVR: schema.GroupVersionResource{
					Group:    "",
					Version:  "v1",
					Resource: "configmaps",
				},
				Namespace: "",
				Name:      "my-config",
				OnChange:  validCallback,
			},
			wantErr:     true,
			errContains: "Namespace",
		},
		{
			name: "missing Name",
			config: SingleWatcherConfig{
				GVR: schema.GroupVersionResource{
					Group:    "",
					Version:  "v1",
					Resource: "configmaps",
				},
				Namespace: "default",
				Name:      "",
				OnChange:  validCallback,
			},
			wantErr:     true,
			errContains: "Name",
		},
		{
			name: "nil OnChange callback",
			config: SingleWatcherConfig{
				GVR: schema.GroupVersionResource{
					Group:    "",
					Version:  "v1",
					Resource: "configmaps",
				},
				Namespace: "default",
				Name:      "my-config",
				OnChange:  nil,
			},
			wantErr:     true,
			errContains: "OnChange",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestConfigError_Error(t *testing.T) {
	err := &ConfigError{
		Field:   "TestField",
		Message: "test message",
	}

	got := err.Error()

	assert.Equal(t, "config error in TestField: test message", got)
}
