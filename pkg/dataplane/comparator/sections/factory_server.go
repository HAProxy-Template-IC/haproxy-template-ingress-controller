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
	"context"

	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections/executors"
)

// NewServerCreate creates an operation to create a server in a backend.
func NewServerCreate(backendName string, server *models.Server) Operation {
	return NewNameChildOp(
		OperationCreate,
		"server",
		PriorityServer,
		backendName,
		server.Name,
		server,
		Identity[*models.Server],
		executors.ServerCreate(backendName),
		DescribeNamedChild(OperationCreate, "server", server.Name, "backend", backendName),
	)
}

// ServerUpdateOp is a specialized operation for server updates that tracks
// whether the update triggered a HAProxy reload. This is needed because server
// updates can be executed via the runtime API (without transaction) and the
// DataPlane API returns 202 if a reload was required.
type ServerUpdateOp struct {
	backendName     string
	server          *models.Server
	reloadTriggered bool
}

// NewServerUpdate creates an operation to update a server in a backend.
// Unlike other operations, server updates use a specialized type that tracks
// reload status for runtime-eligible operations.
func NewServerUpdate(backendName string, server *models.Server) Operation {
	return &ServerUpdateOp{
		backendName: backendName,
		server:      server,
	}
}

func (op *ServerUpdateOp) Type() OperationType { return OperationUpdate }
func (op *ServerUpdateOp) Section() string     { return "server" }
func (op *ServerUpdateOp) Priority() int       { return PriorityServer * 1000 }
func (op *ServerUpdateOp) Describe() string {
	return DescribeNamedChild(OperationUpdate, "server", op.server.Name, "backend", op.backendName)()
}

// TriggeredReload implements RuntimeReloadTracker interface.
// Returns true if the last Execute call triggered a HAProxy reload.
func (op *ServerUpdateOp) TriggeredReload() bool {
	return op.reloadTriggered
}

// BackendName returns the name of the backend containing this server.
// Used by the orchestrator for direct executor calls with version caching.
func (op *ServerUpdateOp) BackendName() string { return op.backendName }

// ServerName returns the name of the server being updated.
// Used by the orchestrator for direct executor calls with version caching.
func (op *ServerUpdateOp) ServerName() string { return op.server.Name }

// Server returns the server model being updated.
// Used by the orchestrator for direct executor calls with version caching.
func (op *ServerUpdateOp) Server() *models.Server { return op.server }

// Execute performs the server update operation.
// When txID is empty (runtime execution), it tracks whether the operation triggered a reload.
func (op *ServerUpdateOp) Execute(ctx context.Context, c *client.DataplaneClient, txID string) error {
	// Pass 0 for version to let ServerUpdateWithReloadTracking fetch the current version.
	// The orchestrator uses direct executor calls with version caching for better performance.
	reloaded, err := executors.ServerUpdateWithReloadTracking(ctx, c, op.backendName, op.server.Name, op.server, txID, 0)
	op.reloadTriggered = reloaded
	return err
}

// NewServerDelete creates an operation to delete a server from a backend.
func NewServerDelete(backendName string, server *models.Server) Operation {
	return NewNameChildOp(
		OperationDelete,
		"server",
		PriorityServer,
		backendName,
		server.Name,
		server,
		Nil[*models.Server],
		executors.ServerDelete(backendName),
		DescribeNamedChild(OperationDelete, "server", server.Name, "backend", backendName),
	)
}

// NewServerTemplateCreate creates an operation to create a server template in a backend.
func NewServerTemplateCreate(backendName string, serverTemplate *models.ServerTemplate) Operation {
	return NewNameChildOp(
		OperationCreate,
		"server_template",
		PriorityServer, // Server templates use same priority as servers
		backendName,
		serverTemplate.Prefix,
		serverTemplate,
		Identity[*models.ServerTemplate],
		executors.ServerTemplateCreate(backendName),
		DescribeNamedChild(OperationCreate, "server template", serverTemplate.Prefix, "backend", backendName),
	)
}

// NewServerTemplateUpdate creates an operation to update a server template in a backend.
func NewServerTemplateUpdate(backendName string, serverTemplate *models.ServerTemplate) Operation {
	return NewNameChildOp(
		OperationUpdate,
		"server_template",
		PriorityServer, // Server templates use same priority as servers
		backendName,
		serverTemplate.Prefix,
		serverTemplate,
		Identity[*models.ServerTemplate],
		executors.ServerTemplateUpdate(backendName),
		DescribeNamedChild(OperationUpdate, "server template", serverTemplate.Prefix, "backend", backendName),
	)
}

// NewServerTemplateDelete creates an operation to delete a server template from a backend.
func NewServerTemplateDelete(backendName string, serverTemplate *models.ServerTemplate) Operation {
	return NewNameChildOp(
		OperationDelete,
		"server_template",
		PriorityServer, // Server templates use same priority as servers
		backendName,
		serverTemplate.Prefix,
		serverTemplate,
		Nil[*models.ServerTemplate],
		executors.ServerTemplateDelete(backendName),
		DescribeNamedChild(OperationDelete, "server template", serverTemplate.Prefix, "backend", backendName),
	)
}
