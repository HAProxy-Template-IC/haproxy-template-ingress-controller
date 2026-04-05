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
	"fmt"

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

// NewBackendSwitchingRuleFrontendCreate creates an operation to create a backend switching rule.
func NewBackendSwitchingRuleFrontendCreate(frontendName string, rule *models.BackendSwitchingRule, index int) Operation {
	return NewIndexChildOp(
		OperationCreate,
		"backend_switching_rule",
		PriorityBackendSwitchingRule,
		frontendName,
		index,
		rule,
		Identity[*models.BackendSwitchingRule],
		executors.BackendSwitchingRuleCreate(),
		DescribeTypedChild(OperationCreate, "backend switching rule", backendSwitchingRuleIdentifier(rule), fmt.Sprintf("at index %d", index), "frontend", frontendName),
	)
}

// NewBackendSwitchingRuleFrontendUpdate creates an operation to update a backend switching rule.
func NewBackendSwitchingRuleFrontendUpdate(frontendName string, rule *models.BackendSwitchingRule, index int) Operation {
	return NewIndexChildOp(
		OperationUpdate,
		"backend_switching_rule",
		PriorityBackendSwitchingRule,
		frontendName,
		index,
		rule,
		Identity[*models.BackendSwitchingRule],
		executors.BackendSwitchingRuleUpdate(),
		DescribeTypedChild(OperationUpdate, "backend switching rule", backendSwitchingRuleIdentifier(rule), fmt.Sprintf("at index %d", index), "frontend", frontendName),
	)
}

// NewBackendSwitchingRuleFrontendDelete creates an operation to delete a backend switching rule.
func NewBackendSwitchingRuleFrontendDelete(frontendName string, rule *models.BackendSwitchingRule, index int) Operation {
	return NewIndexChildOp(
		OperationDelete,
		"backend_switching_rule",
		PriorityBackendSwitchingRule,
		frontendName,
		index,
		rule,
		Nil[*models.BackendSwitchingRule],
		executors.BackendSwitchingRuleDelete(),
		DescribeTypedChild(OperationDelete, "backend switching rule", backendSwitchingRuleIdentifier(rule), fmt.Sprintf("at index %d", index), "frontend", frontendName),
	)
}

// serverSwitchingRuleIdentifier extracts the target server identifier from a ServerSwitchingRule model.
func serverSwitchingRuleIdentifier(rule *models.ServerSwitchingRule) string { return rule.TargetServer }

// NewServerSwitchingRuleBackendCreate creates an operation to create a server switching rule in a backend.
func NewServerSwitchingRuleBackendCreate(backendName string, rule *models.ServerSwitchingRule, index int) Operation {
	return NewIndexChildOp(
		OperationCreate,
		"server_switching_rule",
		PriorityServerSwitchingRule,
		backendName,
		index,
		rule,
		Identity[*models.ServerSwitchingRule],
		executors.ServerSwitchingRuleBackendCreate(),
		DescribeTypedChild(OperationCreate, "server switching rule", serverSwitchingRuleIdentifier(rule), fmt.Sprintf("at index %d", index), "backend", backendName),
	)
}

// NewServerSwitchingRuleBackendUpdate creates an operation to update a server switching rule in a backend.
func NewServerSwitchingRuleBackendUpdate(backendName string, rule *models.ServerSwitchingRule, index int) Operation {
	return NewIndexChildOp(
		OperationUpdate,
		"server_switching_rule",
		PriorityServerSwitchingRule,
		backendName,
		index,
		rule,
		Identity[*models.ServerSwitchingRule],
		executors.ServerSwitchingRuleBackendUpdate(),
		DescribeTypedChild(OperationUpdate, "server switching rule", serverSwitchingRuleIdentifier(rule), fmt.Sprintf("at index %d", index), "backend", backendName),
	)
}

// NewServerSwitchingRuleBackendDelete creates an operation to delete a server switching rule from a backend.
func NewServerSwitchingRuleBackendDelete(backendName string, rule *models.ServerSwitchingRule, index int) Operation {
	return NewIndexChildOp(
		OperationDelete,
		"server_switching_rule",
		PriorityServerSwitchingRule,
		backendName,
		index,
		rule,
		Nil[*models.ServerSwitchingRule],
		executors.ServerSwitchingRuleBackendDelete(),
		DescribeTypedChild(OperationDelete, "server switching rule", serverSwitchingRuleIdentifier(rule), fmt.Sprintf("at index %d", index), "backend", backendName),
	)
}
