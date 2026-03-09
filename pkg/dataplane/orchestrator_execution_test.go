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

package dataplane

import (
	"testing"

	"github.com/haproxytech/client-native/v6/models"
	"github.com/stretchr/testify/assert"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections"
)

func TestBuildRuntimeActions(t *testing.T) {
	port8080 := int64(8080)
	port9090 := int64(9090)
	weight50 := int64(50)
	checkPort8888 := int64(8888)

	tests := []struct {
		name    string
		current *models.Server
		desired *models.Server
		want    string
	}{
		{
			name: "address and port change",
			current: &models.Server{
				Name:    "SRV_1",
				Address: "10.0.0.1",
				Port:    &port8080,
			},
			desired: &models.Server{
				Name:    "SRV_1",
				Address: "10.0.0.2",
				Port:    &port9090,
			},
			want: "SetServerAddr mybackend SRV_1 10.0.0.2 9090",
		},
		{
			name: "maintenance enabled",
			current: &models.Server{
				Name:    "SRV_1",
				Address: "10.0.0.1",
				Port:    &port8080,
			},
			desired: &models.Server{
				Name:         "SRV_1",
				Address:      "10.0.0.1",
				Port:         &port8080,
				ServerParams: models.ServerParams{Maintenance: "enabled"},
			},
			want: "SetServerAddr mybackend SRV_1 10.0.0.1 8080;SetServerState mybackend SRV_1 maint",
		},
		{
			name: "maintenance disabled",
			current: &models.Server{
				Name:         "SRV_1",
				Address:      "10.0.0.1",
				Port:         &port8080,
				ServerParams: models.ServerParams{Maintenance: "enabled"},
			},
			desired: &models.Server{
				Name:         "SRV_1",
				Address:      "10.0.0.1",
				Port:         &port8080,
				ServerParams: models.ServerParams{Maintenance: "disabled"},
			},
			want: "SetServerAddr mybackend SRV_1 10.0.0.1 8080;SetServerState mybackend SRV_1 ready",
		},
		{
			name: "weight change",
			current: &models.Server{
				Name:    "SRV_1",
				Address: "10.0.0.1",
				Port:    &port8080,
			},
			desired: &models.Server{
				Name:         "SRV_1",
				Address:      "10.0.0.1",
				Port:         &port8080,
				ServerParams: models.ServerParams{Weight: &weight50},
			},
			want: "SetServerAddr mybackend SRV_1 10.0.0.1 8080;SetServerWeight mybackend SRV_1 50",
		},
		{
			name: "health check port",
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
			want: "SetServerAddr mybackend SRV_1 10.0.0.1 8080;SetServerCheckPort mybackend SRV_1 8888",
		},
		{
			name: "agent check enabled",
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
			want: "SetServerAddr mybackend SRV_1 10.0.0.1 8080;EnableAgentCheck mybackend SRV_1",
		},
		{
			name: "agent check disabled",
			current: &models.Server{
				Name:         "SRV_1",
				Address:      "10.0.0.1",
				Port:         &port8080,
				ServerParams: models.ServerParams{AgentCheck: "enabled"},
			},
			desired: &models.Server{
				Name:         "SRV_1",
				Address:      "10.0.0.1",
				Port:         &port8080,
				ServerParams: models.ServerParams{AgentCheck: "disabled"},
			},
			want: "SetServerAddr mybackend SRV_1 10.0.0.1 8080;DisableAgentCheck mybackend SRV_1",
		},
		{
			name: "agent addr",
			current: &models.Server{
				Name:    "SRV_1",
				Address: "10.0.0.1",
				Port:    &port8080,
			},
			desired: &models.Server{
				Name:         "SRV_1",
				Address:      "10.0.0.1",
				Port:         &port8080,
				ServerParams: models.ServerParams{AgentAddr: "10.0.0.1"},
			},
			want: "SetServerAddr mybackend SRV_1 10.0.0.1 8080;SetServerAgentAddr mybackend SRV_1 10.0.0.1",
		},
		{
			name: "agent send",
			current: &models.Server{
				Name:    "SRV_1",
				Address: "10.0.0.1",
				Port:    &port8080,
			},
			desired: &models.Server{
				Name:         "SRV_1",
				Address:      "10.0.0.1",
				Port:         &port8080,
				ServerParams: models.ServerParams{AgentSend: "ping"},
			},
			want: "SetServerAddr mybackend SRV_1 10.0.0.1 8080;SetServerAgentSend mybackend SRV_1 ping",
		},
		{
			name: "all fields combined",
			current: &models.Server{
				Name:    "SRV_1",
				Address: "10.0.0.1",
				Port:    &port8080,
			},
			desired: &models.Server{
				Name:    "SRV_1",
				Address: "10.0.0.2",
				Port:    &port9090,
				ServerParams: models.ServerParams{
					Maintenance:     "disabled",
					Weight:          &weight50,
					HealthCheckPort: &checkPort8888,
					AgentCheck:      "enabled",
					AgentAddr:       "10.0.0.2",
					AgentSend:       "ping",
				},
			},
			want: "SetServerAddr mybackend SRV_1 10.0.0.2 9090;SetServerState mybackend SRV_1 ready;SetServerWeight mybackend SRV_1 50;SetServerCheckPort mybackend SRV_1 8888;EnableAgentCheck mybackend SRV_1;SetServerAgentAddr mybackend SRV_1 10.0.0.2;SetServerAgentSend mybackend SRV_1 ping",
		},
		{
			name: "non-ServerUpdateOp operations are skipped",
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
			want: "SetServerAddr mybackend SRV_1 10.0.0.1 8080",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ops := []comparator.Operation{
				sections.NewServerUpdate("mybackend", tt.current, tt.desired),
			}
			got := buildRuntimeActions(ops)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBuildRuntimeActions_MultipleOps(t *testing.T) {
	port8080 := int64(8080)
	port9090 := int64(9090)

	ops := []comparator.Operation{
		sections.NewServerUpdate("backend1", &models.Server{
			Name:    "SRV_1",
			Address: "10.0.0.1",
			Port:    &port8080,
		}, &models.Server{
			Name:    "SRV_1",
			Address: "10.0.0.2",
			Port:    &port9090,
		}),
		sections.NewServerUpdate("backend2", &models.Server{
			Name:    "SRV_2",
			Address: "192.168.0.1",
			Port:    &port8080,
		}, &models.Server{
			Name:         "SRV_2",
			Address:      "192.168.0.1",
			Port:         &port8080,
			ServerParams: models.ServerParams{Maintenance: "enabled"},
		}),
	}

	got := buildRuntimeActions(ops)
	assert.Equal(t,
		"SetServerAddr backend1 SRV_1 10.0.0.2 9090;SetServerAddr backend2 SRV_2 192.168.0.1 8080;SetServerState backend2 SRV_2 maint",
		got,
	)
}

func TestBuildRuntimeActions_NonServerOpSkipped(t *testing.T) {
	ops := []comparator.Operation{
		&mockOperation{
			opType:  sections.OperationUpdate,
			section: "backend",
			desc:    "Update backend 'api'",
		},
	}

	got := buildRuntimeActions(ops)
	assert.Equal(t, "", got)
}

func TestBuildRuntimeActions_Empty(t *testing.T) {
	got := buildRuntimeActions([]comparator.Operation{})
	assert.Equal(t, "", got)
}

func TestAreAllOperationsRuntimeEligible(t *testing.T) {
	o := &orchestrator{}
	port8080 := int64(8080)
	port9090 := int64(9090)
	weight10 := int64(10)

	tests := []struct {
		name       string
		operations func() []comparator.Operation
		want       bool
	}{
		{
			name: "empty operations: not eligible",
			operations: func() []comparator.Operation {
				return []comparator.Operation{}
			},
			want: false,
		},
		{
			name: "single runtime-eligible server update",
			operations: func() []comparator.Operation {
				return []comparator.Operation{
					sections.NewServerUpdate("backend", &models.Server{
						Name:    "SRV_1",
						Address: "10.0.0.1",
						Port:    &port8080,
					}, &models.Server{
						Name:    "SRV_1",
						Address: "10.0.0.2",
						Port:    &port9090,
					}),
				}
			},
			want: true,
		},
		{
			name: "multiple runtime-eligible server updates",
			operations: func() []comparator.Operation {
				return []comparator.Operation{
					sections.NewServerUpdate("backend", &models.Server{
						Name:    "SRV_1",
						Address: "10.0.0.1",
						Port:    &port8080,
					}, &models.Server{
						Name:         "SRV_1",
						Address:      "10.0.0.2",
						Port:         &port9090,
						ServerParams: models.ServerParams{Weight: &weight10},
					}),
					sections.NewServerUpdate("backend", &models.Server{
						Name:    "SRV_2",
						Address: "10.0.0.3",
						Port:    &port8080,
					}, &models.Server{
						Name:         "SRV_2",
						Address:      "10.0.0.3",
						Port:         &port8080,
						ServerParams: models.ServerParams{Maintenance: "disabled"},
					}),
				}
			},
			want: true,
		},
		{
			name: "non-ServerUpdateOp operation: not eligible",
			operations: func() []comparator.Operation {
				return []comparator.Operation{
					&mockOperation{
						opType:  sections.OperationUpdate,
						section: "backend",
					},
				}
			},
			want: false,
		},
		{
			name: "mix of server update and non-server: not eligible",
			operations: func() []comparator.Operation {
				return []comparator.Operation{
					sections.NewServerUpdate("backend", &models.Server{
						Name:    "SRV_1",
						Address: "10.0.0.1",
						Port:    &port8080,
					}, &models.Server{
						Name:    "SRV_1",
						Address: "10.0.0.2",
						Port:    &port9090,
					}),
					&mockOperation{
						opType:  sections.OperationCreate,
						section: "backend",
					},
				}
			},
			want: false,
		},
		{
			name: "server update with reload-required field: not eligible",
			operations: func() []comparator.Operation {
				return []comparator.Operation{
					sections.NewServerUpdate("backend", &models.Server{
						Name:         "SRV_1",
						Address:      "10.0.0.1",
						Port:         &port8080,
						ServerParams: models.ServerParams{Check: "disabled"},
					}, &models.Server{
						Name:         "SRV_1",
						Address:      "10.0.0.1",
						Port:         &port8080,
						ServerParams: models.ServerParams{Check: "enabled"},
					}),
				}
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := o.areAllOperationsRuntimeEligible(tt.operations())
			assert.Equal(t, tt.want, got)
		})
	}
}
