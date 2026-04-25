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
	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections/executors"
)

// backendSwitchingRuleIdentifier extracts the name identifier from a BackendSwitchingRule model.
func backendSwitchingRuleIdentifier(rule *models.BackendSwitchingRule) string {
	if rule != nil {
		return rule.Name
	}
	return ""
}

// serverSwitchingRuleIdentifier extracts the target server identifier from a ServerSwitchingRule model.
func serverSwitchingRuleIdentifier(rule *models.ServerSwitchingRule) string { return rule.TargetServer }

// CRUD builders for backend and server switching rules.
var (
	backendSwitchingRuleFrontendOps = NewIndexChildCRUD[*models.BackendSwitchingRule](
		"backend_switching_rule", "backend switching rule", "frontend", PriorityBackendSwitchingRule, backendSwitchingRuleIdentifier,
		executors.BackendSwitchingRuleCreate(), executors.BackendSwitchingRuleUpdate(), executors.BackendSwitchingRuleDelete(),
	)
	serverSwitchingRuleBackendOps = NewIndexChildCRUD[*models.ServerSwitchingRule](
		"server_switching_rule", "server switching rule", "backend", PriorityServerSwitchingRule, serverSwitchingRuleIdentifier,
		executors.ServerSwitchingRuleBackendCreate(), executors.ServerSwitchingRuleBackendUpdate(), executors.ServerSwitchingRuleBackendDelete(),
	)
)

// NewBackendSwitchingRuleFrontendCreate creates an operation to create a backend switching rule.
func NewBackendSwitchingRuleFrontendCreate(frontendName string, rule *models.BackendSwitchingRule, index int) Operation {
	return backendSwitchingRuleFrontendOps.Create(frontendName, rule, index)
}

// NewBackendSwitchingRuleFrontendUpdate creates an operation to update a backend switching rule.
func NewBackendSwitchingRuleFrontendUpdate(frontendName string, rule *models.BackendSwitchingRule, index int) Operation {
	return backendSwitchingRuleFrontendOps.Update(frontendName, rule, index)
}

// NewBackendSwitchingRuleFrontendDelete creates an operation to delete a backend switching rule.
func NewBackendSwitchingRuleFrontendDelete(frontendName string, rule *models.BackendSwitchingRule, index int) Operation {
	return backendSwitchingRuleFrontendOps.Delete(frontendName, rule, index)
}

// NewServerSwitchingRuleBackendCreate creates an operation to create a server switching rule in a backend.
func NewServerSwitchingRuleBackendCreate(backendName string, rule *models.ServerSwitchingRule, index int) Operation {
	return serverSwitchingRuleBackendOps.Create(backendName, rule, index)
}

// NewServerSwitchingRuleBackendUpdate creates an operation to update a server switching rule in a backend.
func NewServerSwitchingRuleBackendUpdate(backendName string, rule *models.ServerSwitchingRule, index int) Operation {
	return serverSwitchingRuleBackendOps.Update(backendName, rule, index)
}

// NewServerSwitchingRuleBackendDelete creates an operation to delete a server switching rule from a backend.
func NewServerSwitchingRuleBackendDelete(backendName string, rule *models.ServerSwitchingRule, index int) Operation {
	return serverSwitchingRuleBackendOps.Delete(backendName, rule, index)
}
