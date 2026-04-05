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

// httpRequestRuleIdentifier extracts the type identifier from an HTTPRequestRule model.
func httpRequestRuleIdentifier(rule *models.HTTPRequestRule) string {
	if rule != nil {
		return rule.Type
	}
	return ""
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
		DescribeTypedChild(OperationCreate, "HTTP request rule", httpRequestRuleIdentifier(rule), fmt.Sprintf("at index %d", index), "frontend", frontendName),
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
		DescribeTypedChild(OperationUpdate, "HTTP request rule", httpRequestRuleIdentifier(rule), fmt.Sprintf("at index %d", index), "frontend", frontendName),
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
		DescribeTypedChild(OperationDelete, "HTTP request rule", httpRequestRuleIdentifier(rule), fmt.Sprintf("at index %d", index), "frontend", frontendName),
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
		DescribeTypedChild(OperationCreate, "HTTP request rule", httpRequestRuleIdentifier(rule), fmt.Sprintf("at index %d", index), "backend", backendName),
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
		DescribeTypedChild(OperationUpdate, "HTTP request rule", httpRequestRuleIdentifier(rule), fmt.Sprintf("at index %d", index), "backend", backendName),
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
		DescribeTypedChild(OperationDelete, "HTTP request rule", httpRequestRuleIdentifier(rule), fmt.Sprintf("at index %d", index), "backend", backendName),
	)
}

// httpResponseRuleIdentifier extracts the type identifier from an HTTPResponseRule model.
// Falls back to unknownIdentifier if the type is empty, to ensure a non-empty identifier
// is always displayed (HTTP response rules always have a type in practice).
func httpResponseRuleIdentifier(rule *models.HTTPResponseRule) string {
	if rule != nil && rule.Type != "" {
		return rule.Type
	}
	return unknownIdentifier
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
		DescribeTypedChild(OperationCreate, "HTTP response rule", httpResponseRuleIdentifier(rule), "", "frontend", frontendName),
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
		DescribeTypedChild(OperationUpdate, "HTTP response rule", httpResponseRuleIdentifier(rule), "", "frontend", frontendName),
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
		DescribeTypedChild(OperationDelete, "HTTP response rule", httpResponseRuleIdentifier(rule), "", "frontend", frontendName),
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
		DescribeTypedChild(OperationCreate, "HTTP response rule", httpResponseRuleIdentifier(rule), "", "backend", backendName),
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
		DescribeTypedChild(OperationUpdate, "HTTP response rule", httpResponseRuleIdentifier(rule), "", "backend", backendName),
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
		DescribeTypedChild(OperationDelete, "HTTP response rule", httpResponseRuleIdentifier(rule), "", "backend", backendName),
	)
}
