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
	"encoding/json"

	"github.com/haproxytech/client-native/v6/models"
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
// Exceptions (runtime-eligible but no runtime action required):
//   - "metadata": inline comments, e.g. "# Pod: my-pod-abc" — purely cosmetic.
//
// NOTE: "init-addr" used to be listed here because the chart emitted
// `init-addr last,<address>` on every server, so an address rotation co-changed
// init-addr and would otherwise have been re-classified as structural. That
// machinery was removed — HAProxy never restored an IP-literal server's address
// from the state file (only FQDN/DNS-SRV servers consult `init-addr last`), so
// it never preserved pod addresses across reloads. See
// docs/adr/0011-no-haproxy-server-state-file.md. Server lines no longer carry
// init-addr, so it is intentionally absent here.
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

// serverTemplateOps groups create/update/delete factories for server-template
// operations under a backend.
var serverTemplateOps = NewNameChildCRUD[*models.ServerTemplate](
	"server_template", "server template", "backend",
	func(_ *models.ServerTemplate, childName string) string { return childName },
)

// NewServerCreate creates an operation to create a server in a backend.
func NewServerCreate(backendName string, server *models.Server) Operation {
	return newOp(
		OperationCreate, "server",
		DescribeNamedChild(OperationCreate, "server", server.Name, "backend", backendName),
	)
}

// ServerUpdateOp is a specialized operation for server updates. It carries the
// current and desired models alongside the runtime-eligibility flag the
// orchestrator uses to decide whether the diff is fully runtime-eligible (no
// reload required).
type ServerUpdateOp struct {
	backendName          string
	currentServer        *models.Server
	server               *models.Server
	fullyRuntimeEligible bool
}

// NewServerUpdate creates an operation to update a server in a backend.
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
func (op *ServerUpdateOp) IsFullyRuntimeEligible() bool {
	return op.fullyRuntimeEligible
}

func (op *ServerUpdateOp) Type() OperationType { return OperationUpdate }
func (op *ServerUpdateOp) Section() string     { return "server" }

func (op *ServerUpdateOp) Describe() string {
	return DescribeNamedChild(OperationUpdate, "server", op.server.Name, "backend", op.backendName)()
}

// BackendName returns the name of the backend containing this server.
func (op *ServerUpdateOp) BackendName() string { return op.backendName }

// CurrentServer returns the current (pre-update) server model.
// Used for diagnostics to identify which fields changed and why they are/aren't runtime-eligible.
func (op *ServerUpdateOp) CurrentServer() *models.Server { return op.currentServer }

// ServerName returns the name of the server being updated.
func (op *ServerUpdateOp) ServerName() string { return op.server.Name }

// Server returns the server model being updated.
func (op *ServerUpdateOp) Server() *models.Server { return op.server }

// NewServerDelete creates an operation to delete a server from a backend.
func NewServerDelete(backendName string, server *models.Server) Operation {
	return newOp(
		OperationDelete, "server",
		DescribeNamedChild(OperationDelete, "server", server.Name, "backend", backendName),
	)
}

// NewServerTemplateCreate creates an operation to create a server template in a backend.
func NewServerTemplateCreate(backendName string, serverTemplate *models.ServerTemplate) Operation {
	return serverTemplateOps.Create(backendName, serverTemplate.Prefix, serverTemplate)
}

// NewServerTemplateUpdate creates an operation to update a server template in a backend.
func NewServerTemplateUpdate(backendName string, serverTemplate *models.ServerTemplate) Operation {
	return serverTemplateOps.Update(backendName, serverTemplate.Prefix, serverTemplate)
}

// NewServerTemplateDelete creates an operation to delete a server template from a backend.
func NewServerTemplateDelete(backendName string, serverTemplate *models.ServerTemplate) Operation {
	return serverTemplateOps.Delete(backendName, serverTemplate.Prefix, serverTemplate)
}
