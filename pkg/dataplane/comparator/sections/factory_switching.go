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

// describeBackendSwitchingRule creates a descriptive string for backend switching rule operations.
// Uses the rule's Name field which contains the condition expression, falls back to index if not available.
func describeBackendSwitchingRule(opType OperationType, rule *models.BackendSwitchingRule, frontendName string, index int) string {
	identifier := fmt.Sprintf("at index %d", index)
	if rule != nil && rule.Name != "" {
		identifier = fmt.Sprintf("(%s)", rule.Name)
	}

	switch opType {
	case OperationCreate:
		return fmt.Sprintf("Create backend switching rule %s in frontend '%s'", identifier, frontendName)
	case OperationUpdate:
		return fmt.Sprintf("Update backend switching rule %s in frontend '%s'", identifier, frontendName)
	case OperationDelete:
		return fmt.Sprintf("Delete backend switching rule %s from frontend '%s'", identifier, frontendName)
	default:
		return fmt.Sprintf("Unknown operation on backend switching rule %s in frontend '%s'", identifier, frontendName)
	}
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
		func() string { return describeBackendSwitchingRule(OperationCreate, rule, frontendName, index) },
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
		func() string { return describeBackendSwitchingRule(OperationUpdate, rule, frontendName, index) },
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
		func() string { return describeBackendSwitchingRule(OperationDelete, rule, frontendName, index) },
	)
}

// describeServerSwitchingRule generates a human-readable description for a server switching rule operation.
func describeServerSwitchingRule(opType OperationType, rule *models.ServerSwitchingRule, backendName string, index int) string {
	identifier := rule.TargetServer
	if identifier == "" {
		identifier = fmt.Sprintf("at index %d", index)
	} else {
		identifier = fmt.Sprintf("(%s)", identifier)
	}
	switch opType {
	case OperationCreate:
		return fmt.Sprintf("Create server switching rule %s in backend '%s'", identifier, backendName)
	case OperationUpdate:
		return fmt.Sprintf("Update server switching rule %s in backend '%s'", identifier, backendName)
	case OperationDelete:
		return fmt.Sprintf("Delete server switching rule %s from backend '%s'", identifier, backendName)
	default:
		return fmt.Sprintf("Unknown operation on server switching rule %s in backend '%s'", identifier, backendName)
	}
}

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
		func() string { return describeServerSwitchingRule(OperationCreate, rule, backendName, index) },
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
		func() string { return describeServerSwitchingRule(OperationUpdate, rule, backendName, index) },
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
		func() string { return describeServerSwitchingRule(OperationDelete, rule, backendName, index) },
	)
}
