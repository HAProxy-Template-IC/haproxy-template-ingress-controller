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

// describeHTTPRequestRule creates a descriptive string for HTTP request rule operations.
// Uses the rule's Type field for identification, falls back to index if not available.
func describeHTTPRequestRule(opType OperationType, rule *models.HTTPRequestRule, parentType, parentName string, index int) string {
	identifier := fmt.Sprintf("at index %d", index)
	if rule != nil && rule.Type != "" {
		identifier = fmt.Sprintf("(%s)", rule.Type)
	}

	switch opType {
	case OperationCreate:
		return fmt.Sprintf("Create HTTP request rule %s in %s '%s'", identifier, parentType, parentName)
	case OperationUpdate:
		return fmt.Sprintf("Update HTTP request rule %s in %s '%s'", identifier, parentType, parentName)
	case OperationDelete:
		return fmt.Sprintf("Delete HTTP request rule %s from %s '%s'", identifier, parentType, parentName)
	default:
		return fmt.Sprintf("Unknown operation on HTTP request rule %s in %s '%s'", identifier, parentType, parentName)
	}
}

// NewHTTPRequestRuleFrontendCreate creates an operation to create an HTTP request rule in a frontend.
func NewHTTPRequestRuleFrontendCreate(frontendName string, rule *models.HTTPRequestRule, index int) Operation {
	return NewIndexChildOp(
		OperationCreate,
		"http_request_rule",
		PriorityRule, // HTTP request rules use PriorityRule
		frontendName,
		index,
		rule,
		Identity[*models.HTTPRequestRule],
		executors.HTTPRequestRuleFrontendCreate(),
		func() string { return describeHTTPRequestRule(OperationCreate, rule, "frontend", frontendName, index) },
	)
}

// NewHTTPRequestRuleFrontendUpdate creates an operation to update an HTTP request rule in a frontend.
func NewHTTPRequestRuleFrontendUpdate(frontendName string, rule *models.HTTPRequestRule, index int) Operation {
	return NewIndexChildOp(
		OperationUpdate,
		"http_request_rule",
		PriorityRule, // HTTP request rules use PriorityRule
		frontendName,
		index,
		rule,
		Identity[*models.HTTPRequestRule],
		executors.HTTPRequestRuleFrontendUpdate(),
		func() string { return describeHTTPRequestRule(OperationUpdate, rule, "frontend", frontendName, index) },
	)
}

// NewHTTPRequestRuleFrontendDelete creates an operation to delete an HTTP request rule from a frontend.
func NewHTTPRequestRuleFrontendDelete(frontendName string, rule *models.HTTPRequestRule, index int) Operation {
	return NewIndexChildOp(
		OperationDelete,
		"http_request_rule",
		PriorityRule, // HTTP request rules use PriorityRule
		frontendName,
		index,
		rule,
		Nil[*models.HTTPRequestRule],
		executors.HTTPRequestRuleFrontendDelete(),
		func() string { return describeHTTPRequestRule(OperationDelete, rule, "frontend", frontendName, index) },
	)
}

// NewHTTPRequestRuleBackendCreate creates an operation to create an HTTP request rule in a backend.
func NewHTTPRequestRuleBackendCreate(backendName string, rule *models.HTTPRequestRule, index int) Operation {
	return NewIndexChildOp(
		OperationCreate,
		"http_request_rule",
		PriorityRule, // HTTP request rules use PriorityRule
		backendName,
		index,
		rule,
		Identity[*models.HTTPRequestRule],
		executors.HTTPRequestRuleBackendCreate(),
		func() string { return describeHTTPRequestRule(OperationCreate, rule, "backend", backendName, index) },
	)
}

// NewHTTPRequestRuleBackendUpdate creates an operation to update an HTTP request rule in a backend.
func NewHTTPRequestRuleBackendUpdate(backendName string, rule *models.HTTPRequestRule, index int) Operation {
	return NewIndexChildOp(
		OperationUpdate,
		"http_request_rule",
		PriorityRule, // HTTP request rules use PriorityRule
		backendName,
		index,
		rule,
		Identity[*models.HTTPRequestRule],
		executors.HTTPRequestRuleBackendUpdate(),
		func() string { return describeHTTPRequestRule(OperationUpdate, rule, "backend", backendName, index) },
	)
}

// NewHTTPRequestRuleBackendDelete creates an operation to delete an HTTP request rule from a backend.
func NewHTTPRequestRuleBackendDelete(backendName string, rule *models.HTTPRequestRule, index int) Operation {
	return NewIndexChildOp(
		OperationDelete,
		"http_request_rule",
		PriorityRule, // HTTP request rules use PriorityRule
		backendName,
		index,
		rule,
		Nil[*models.HTTPRequestRule],
		executors.HTTPRequestRuleBackendDelete(),
		func() string { return describeHTTPRequestRule(OperationDelete, rule, "backend", backendName, index) },
	)
}

// describeHTTPResponseRule creates a descriptive string for HTTP response rule operations.
// Uses the rule's Type field for identification.
func describeHTTPResponseRule(opType OperationType, rule *models.HTTPResponseRule, parentType, parentName string) string {
	identifier := unknownIdentifier
	if rule != nil && rule.Type != "" {
		identifier = rule.Type
	}

	switch opType {
	case OperationCreate:
		return fmt.Sprintf("Create HTTP response rule (%s) in %s '%s'", identifier, parentType, parentName)
	case OperationUpdate:
		return fmt.Sprintf("Update HTTP response rule (%s) in %s '%s'", identifier, parentType, parentName)
	case OperationDelete:
		return fmt.Sprintf("Delete HTTP response rule (%s) from %s '%s'", identifier, parentType, parentName)
	default:
		return fmt.Sprintf("Unknown operation on HTTP response rule (%s) in %s '%s'", identifier, parentType, parentName)
	}
}

// NewHTTPResponseRuleFrontendCreate creates an operation to create an HTTP response rule in a frontend.
func NewHTTPResponseRuleFrontendCreate(frontendName string, rule *models.HTTPResponseRule, index int) Operation {
	return NewIndexChildOp(
		OperationCreate,
		"http_response_rule",
		PriorityRule, // HTTP response rules use PriorityRule
		frontendName,
		index,
		rule,
		Identity[*models.HTTPResponseRule],
		executors.HTTPResponseRuleFrontendCreate(),
		func() string { return describeHTTPResponseRule(OperationCreate, rule, "frontend", frontendName) },
	)
}

// NewHTTPResponseRuleFrontendUpdate creates an operation to update an HTTP response rule in a frontend.
func NewHTTPResponseRuleFrontendUpdate(frontendName string, rule *models.HTTPResponseRule, index int) Operation {
	return NewIndexChildOp(
		OperationUpdate,
		"http_response_rule",
		PriorityRule, // HTTP response rules use PriorityRule
		frontendName,
		index,
		rule,
		Identity[*models.HTTPResponseRule],
		executors.HTTPResponseRuleFrontendUpdate(),
		func() string { return describeHTTPResponseRule(OperationUpdate, rule, "frontend", frontendName) },
	)
}

// NewHTTPResponseRuleFrontendDelete creates an operation to delete an HTTP response rule from a frontend.
func NewHTTPResponseRuleFrontendDelete(frontendName string, rule *models.HTTPResponseRule, index int) Operation {
	return NewIndexChildOp(
		OperationDelete,
		"http_response_rule",
		PriorityRule, // HTTP response rules use PriorityRule
		frontendName,
		index,
		rule,
		Nil[*models.HTTPResponseRule],
		executors.HTTPResponseRuleFrontendDelete(),
		func() string { return describeHTTPResponseRule(OperationDelete, rule, "frontend", frontendName) },
	)
}

// NewHTTPResponseRuleBackendCreate creates an operation to create an HTTP response rule in a backend.
func NewHTTPResponseRuleBackendCreate(backendName string, rule *models.HTTPResponseRule, index int) Operation {
	return NewIndexChildOp(
		OperationCreate,
		"http_response_rule",
		PriorityRule, // HTTP response rules use PriorityRule
		backendName,
		index,
		rule,
		Identity[*models.HTTPResponseRule],
		executors.HTTPResponseRuleBackendCreate(),
		func() string { return describeHTTPResponseRule(OperationCreate, rule, "backend", backendName) },
	)
}

// NewHTTPResponseRuleBackendUpdate creates an operation to update an HTTP response rule in a backend.
func NewHTTPResponseRuleBackendUpdate(backendName string, rule *models.HTTPResponseRule, index int) Operation {
	return NewIndexChildOp(
		OperationUpdate,
		"http_response_rule",
		PriorityRule, // HTTP response rules use PriorityRule
		backendName,
		index,
		rule,
		Identity[*models.HTTPResponseRule],
		executors.HTTPResponseRuleBackendUpdate(),
		func() string { return describeHTTPResponseRule(OperationUpdate, rule, "backend", backendName) },
	)
}

// NewHTTPResponseRuleBackendDelete creates an operation to delete an HTTP response rule from a backend.
func NewHTTPResponseRuleBackendDelete(backendName string, rule *models.HTTPResponseRule, index int) Operation {
	return NewIndexChildOp(
		OperationDelete,
		"http_response_rule",
		PriorityRule, // HTTP response rules use PriorityRule
		backendName,
		index,
		rule,
		Nil[*models.HTTPResponseRule],
		executors.HTTPResponseRuleBackendDelete(),
		func() string { return describeHTTPResponseRule(OperationDelete, rule, "backend", backendName) },
	)
}
