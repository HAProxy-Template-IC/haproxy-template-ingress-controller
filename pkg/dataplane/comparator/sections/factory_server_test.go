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

package sections

import (
	"testing"

	"github.com/haproxytech/client-native/v6/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServerUpdateOp_IsFullyRuntimeEligible(t *testing.T) {
	port8080 := int64(8080)
	port9090 := int64(9090)
	weight10 := int64(10)
	weight20 := int64(20)
	checkPort8888 := int64(8888)

	tests := []struct {
		name    string
		current *models.Server
		desired *models.Server
		want    bool
	}{
		{
			name: "address change only",
			current: &models.Server{
				Name:    "SRV_1",
				Address: "10.0.0.1",
				Port:    &port8080,
			},
			desired: &models.Server{
				Name:    "SRV_1",
				Address: "10.0.0.2",
				Port:    &port8080,
			},
			want: true,
		},
		{
			name: "port change only",
			current: &models.Server{
				Name:    "SRV_1",
				Address: "10.0.0.1",
				Port:    &port8080,
			},
			desired: &models.Server{
				Name:    "SRV_1",
				Address: "10.0.0.1",
				Port:    &port9090,
			},
			want: true,
		},
		{
			name: "maintenance change: enabled",
			current: &models.Server{
				Name:         "SRV_1",
				Address:      "127.0.0.1",
				Port:         &port8080,
				ServerParams: models.ServerParams{Maintenance: "disabled"},
			},
			desired: &models.Server{
				Name:         "SRV_1",
				Address:      "127.0.0.1",
				Port:         &port8080,
				ServerParams: models.ServerParams{Maintenance: "enabled"},
			},
			want: true,
		},
		{
			name: "weight change",
			current: &models.Server{
				Name:         "SRV_1",
				Address:      "10.0.0.1",
				Port:         &port8080,
				ServerParams: models.ServerParams{Weight: &weight10},
			},
			desired: &models.Server{
				Name:         "SRV_1",
				Address:      "10.0.0.1",
				Port:         &port8080,
				ServerParams: models.ServerParams{Weight: &weight20},
			},
			want: true,
		},
		{
			name: "health_check_port change",
			current: &models.Server{
				Name:    "SRV_1",
				Address: "10.0.0.1",
				Port:    &port8080,
			},
			desired: &models.Server{
				Name:         "SRV_1",
				Address:      "10.0.0.1",
				Port:         &port8080,
				ServerParams: models.ServerParams{HealthCheckPort: &checkPort8888},
			},
			want: true,
		},
		{
			name: "agent-check change",
			current: &models.Server{
				Name:    "SRV_1",
				Address: "10.0.0.1",
				Port:    &port8080,
			},
			desired: &models.Server{
				Name:         "SRV_1",
				Address:      "10.0.0.1",
				Port:         &port8080,
				ServerParams: models.ServerParams{AgentCheck: "enabled"},
			},
			want: true,
		},
		{
			name: "multiple runtime-eligible changes",
			current: &models.Server{
				Name:         "SRV_1",
				Address:      "10.0.0.1",
				Port:         &port8080,
				ServerParams: models.ServerParams{Weight: &weight10},
			},
			desired: &models.Server{
				Name:         "SRV_1",
				Address:      "10.0.0.2",
				Port:         &port9090,
				ServerParams: models.ServerParams{Weight: &weight20, Maintenance: "disabled"},
			},
			want: true,
		},
		{
			name: "check field change requires reload",
			current: &models.Server{
				Name:         "SRV_1",
				Address:      "10.0.0.1",
				Port:         &port8080,
				ServerParams: models.ServerParams{Check: "disabled"},
			},
			desired: &models.Server{
				Name:         "SRV_1",
				Address:      "10.0.0.1",
				Port:         &port8080,
				ServerParams: models.ServerParams{Check: "enabled"},
			},
			want: false,
		},
		{
			name: "ssl field change requires reload",
			current: &models.Server{
				Name:    "SRV_1",
				Address: "10.0.0.1",
				Port:    &port8080,
			},
			desired: &models.Server{
				Name:         "SRV_1",
				Address:      "10.0.0.1",
				Port:         &port8080,
				ServerParams: models.ServerParams{Ssl: "enabled"},
			},
			want: false,
		},
		{
			name: "runtime-eligible + reload-required field: not eligible",
			current: &models.Server{
				Name:         "SRV_1",
				Address:      "10.0.0.1",
				Port:         &port8080,
				ServerParams: models.ServerParams{Check: "disabled"},
			},
			desired: &models.Server{
				Name:         "SRV_1",
				Address:      "10.0.0.2",                            // runtime-eligible
				Port:         &port9090,                             // runtime-eligible
				ServerParams: models.ServerParams{Check: "enabled"}, // reload-required
			},
			want: false,
		},
		{
			name: "identical servers: eligible (no changes)",
			current: &models.Server{
				Name:    "SRV_1",
				Address: "10.0.0.1",
				Port:    &port8080,
			},
			desired: &models.Server{
				Name:    "SRV_1",
				Address: "10.0.0.1",
				Port:    &port8080,
			},
			want: true,
		},
		{
			// Reproduces the production regression where templates put `check` on individual
			// active server lines but not on reserved (disabled) slots.
			// Reserved slot: `server SRV_1 127.0.0.1:1 disabled`       (no check)
			// Active server: `server SRV_1 10.0.0.1:8080 check enabled` (check on server line)
			// Fix: move `check` to `default-server` so server lines stay at address:port + enabled/disabled.
			name: "reserved-to-active with check on server line: not eligible (check requires reload)",
			current: &models.Server{
				Name:         "SRV_1",
				Address:      "127.0.0.1",
				Port:         &port8080,
				ServerParams: models.ServerParams{Maintenance: "enabled"},
			},
			desired: &models.Server{
				Name:         "SRV_1",
				Address:      "10.0.0.1",
				Port:         &port8080,
				ServerParams: models.ServerParams{Maintenance: "disabled", Check: "enabled"},
			},
			want: false,
		},
		{
			// Same as above but in reverse: active → reserved also not eligible when check differs.
			name: "active-to-reserved with check removal: not eligible (check requires reload)",
			current: &models.Server{
				Name:         "SRV_1",
				Address:      "10.0.0.1",
				Port:         &port8080,
				ServerParams: models.ServerParams{Maintenance: "disabled", Check: "enabled"},
			},
			desired: &models.Server{
				Name:         "SRV_1",
				Address:      "127.0.0.1",
				Port:         &port8080,
				ServerParams: models.ServerParams{Maintenance: "enabled"},
			},
			want: false,
		},
		{
			// The correct template pattern: check in default-server, server line has only
			// address:port + enabled/disabled. Slot-swap is fully runtime-eligible.
			name: "reserved-to-active with check in default-server only: eligible",
			current: &models.Server{
				Name:         "SRV_1",
				Address:      "127.0.0.1",
				Port:         &port8080,
				ServerParams: models.ServerParams{Maintenance: "enabled"},
			},
			desired: &models.Server{
				Name:         "SRV_1",
				Address:      "10.0.0.1",
				Port:         &port8080,
				ServerParams: models.ServerParams{Maintenance: "disabled"},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := NewServerUpdate("backend", tt.current, tt.desired)
			serverOp, ok := op.(*ServerUpdateOp)
			require.True(t, ok, "expected *ServerUpdateOp")
			assert.Equal(t, tt.want, serverOp.IsFullyRuntimeEligible())
		})
	}
}
