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
	"bytes"
	"context"
	"encoding/json"

	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections/executors"
)

// serverRuntimeSupportedJSONFields is the set of models.Server JSON field names that can be
// changed via the runtime API without triggering a HAProxy reload.
// Matches DataPlane API's RuntimeSupportedFields["server"] in handlers/runtime.go.
// JSON tags are taken from github.com/haproxytech/client-native/v6/models/server_params.go.
//
// IMPORTANT: Keep in sync with buildRuntimeActions in orchestrator_execution.go, which must
// generate a runtime action for every field listed here. If a field is added here but not
// handled in buildRuntimeActions, the change will be written to disk but never applied at
// runtime — silently deferring it until the next reload.
//
// Exception: "metadata" (inline comments, e.g. "# Pod: my-pod-abc") requires no runtime
// action — changes are written to disk by the skip_reload push and are purely cosmetic.
var serverRuntimeSupportedJSONFields = map[string]struct{}{
	"weight":            {},
	"address":           {},
	"port":              {},
	"maintenance":       {},
	"agent-check":       {},
	"agent-addr":        {},
	"agent-send":        {},
	"health_check_port": {},
	"metadata":          {}, // inline comments (e.g. "# Pod: <name>") — cosmetic only, no runtime action needed
}

// computeServerRuntimeEligibility returns true if all fields that differ between current
// and desired are in serverRuntimeSupportedJSONFields (i.e., no reload is required).
// Conservative: returns false on any error.
func computeServerRuntimeEligibility(current, desired *models.Server) bool {
	return len(ServerIneligibleFields(current, desired)) == 0
}

// ServerIneligibleFields returns the JSON field names that differ between current and desired
// but are not in serverRuntimeSupportedJSONFields (i.e., they require a HAProxy reload).
// Returns an empty slice when all changed fields are runtime-eligible.
// Used for diagnostics to explain why the runtime-optimized path was skipped.
//
// Common causes of non-eligible fields in production:
//   - "check": occurs when individual server lines carry the `check` keyword (e.g., `server SRV_1 10.0.0.1:8080 check enabled`)
//     but reserved slots do not. Fix: move `check` to the `default-server` directive so all server
//     lines stay at address:port + enabled/disabled only, making every slot-swap fully runtime-eligible.
func ServerIneligibleFields(current, desired *models.Server) []string {
	currentJSON, err1 := json.Marshal(current)
	desiredJSON, err2 := json.Marshal(desired)
	if err1 != nil || err2 != nil {
		return []string{"<marshal-error>"}
	}
	var currentMap, desiredMap map[string]json.RawMessage
	if json.Unmarshal(currentJSON, &currentMap) != nil {
		return []string{"<unmarshal-error>"}
	}
	if json.Unmarshal(desiredJSON, &desiredMap) != nil {
		return []string{"<unmarshal-error>"}
	}

	var ineligible []string
	for key, curVal := range currentMap {
		if !bytes.Equal(curVal, desiredMap[key]) {
			if _, ok := serverRuntimeSupportedJSONFields[key]; !ok {
				ineligible = append(ineligible, key)
			}
		}
	}
	for key, desVal := range desiredMap {
		if _, exists := currentMap[key]; !exists {
			// key only in desired — it's a new field
			if !bytes.Equal(desVal, json.RawMessage("null")) {
				if _, ok := serverRuntimeSupportedJSONFields[key]; !ok {
					ineligible = append(ineligible, key)
				}
			}
		}
	}
	return ineligible
}

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
	backendName          string
	currentServer        *models.Server
	server               *models.Server
	reloadTriggered      bool
	fullyRuntimeEligible bool
}

// NewServerUpdate creates an operation to update a server in a backend.
// Unlike other operations, server updates use a specialized type that tracks
// reload status for runtime-eligible operations.
// Both current and desired server models are required to determine whether
// all changed fields are runtime-eligible (no reload needed).
func NewServerUpdate(backendName string, current, desired *models.Server) Operation {
	return &ServerUpdateOp{
		backendName:          backendName,
		currentServer:        current,
		server:               desired,
		fullyRuntimeEligible: computeServerRuntimeEligibility(current, desired),
	}
}

// IsFullyRuntimeEligible returns true if all changed server fields are in the
// runtime-supported set (no reload required for this update).
// Computed once at construction time from current vs desired server models.
func (op *ServerUpdateOp) IsFullyRuntimeEligible() bool {
	return op.fullyRuntimeEligible
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

// CurrentServer returns the current (pre-update) server model.
// Used for diagnostics to identify which fields changed and why they are/aren't runtime-eligible.
func (op *ServerUpdateOp) CurrentServer() *models.Server { return op.currentServer }

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
