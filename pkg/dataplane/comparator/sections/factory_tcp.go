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

// captureIdentifier extracts the type identifier from a Capture model.
func captureIdentifier(capture *models.Capture) string { return capture.Type }

// tcpRequestRuleIdentifier extracts the type identifier from a TCPRequestRule model.
func tcpRequestRuleIdentifier(rule *models.TCPRequestRule) string { return rule.Type }

// tcpResponseRuleIdentifier extracts the type identifier from a TCPResponseRule model.
func tcpResponseRuleIdentifier(rule *models.TCPResponseRule) string { return rule.Type }

// httpCheckIdentifier extracts the type identifier from an HTTPCheck model.
func httpCheckIdentifier(check *models.HTTPCheck) string { return check.Type }

// tcpCheckIdentifier extracts the action identifier from a TCPCheck model.
func tcpCheckIdentifier(check *models.TCPCheck) string { return check.Action }

// stickRuleIdentifier extracts the type identifier from a StickRule model.
func stickRuleIdentifier(rule *models.StickRule) string { return rule.Type }

// httpAfterResponseRuleIdentifier extracts the type identifier from an HTTPAfterResponseRule model.
func httpAfterResponseRuleIdentifier(rule *models.HTTPAfterResponseRule) string { return rule.Type }

// NewTCPRequestRuleFrontendCreate creates an operation to create a TCP request rule in a frontend.
func NewTCPRequestRuleFrontendCreate(frontendName string, rule *models.TCPRequestRule, index int) Operation {
	return NewIndexChildOp(
		OperationCreate,
		"tcp_request_rule",
		PriorityRule,
		frontendName,
		index,
		rule,
		Identity[*models.TCPRequestRule],
		executors.TCPRequestRuleFrontendCreate(),
		DescribeTypedChild(OperationCreate, "TCP request rule", tcpRequestRuleIdentifier(rule), fmt.Sprintf("at index %d", index), "frontend", frontendName),
	)
}

// NewTCPRequestRuleFrontendUpdate creates an operation to update a TCP request rule in a frontend.
func NewTCPRequestRuleFrontendUpdate(frontendName string, rule *models.TCPRequestRule, index int) Operation {
	return NewIndexChildOp(
		OperationUpdate,
		"tcp_request_rule",
		PriorityRule,
		frontendName,
		index,
		rule,
		Identity[*models.TCPRequestRule],
		executors.TCPRequestRuleFrontendUpdate(),
		DescribeTypedChild(OperationUpdate, "TCP request rule", tcpRequestRuleIdentifier(rule), fmt.Sprintf("at index %d", index), "frontend", frontendName),
	)
}

// NewTCPRequestRuleFrontendDelete creates an operation to delete a TCP request rule from a frontend.
func NewTCPRequestRuleFrontendDelete(frontendName string, rule *models.TCPRequestRule, index int) Operation {
	return NewIndexChildOp(
		OperationDelete,
		"tcp_request_rule",
		PriorityRule,
		frontendName,
		index,
		rule,
		Nil[*models.TCPRequestRule],
		executors.TCPRequestRuleFrontendDelete(),
		DescribeTypedChild(OperationDelete, "TCP request rule", tcpRequestRuleIdentifier(rule), fmt.Sprintf("at index %d", index), "frontend", frontendName),
	)
}

// NewTCPRequestRuleBackendCreate creates an operation to create a TCP request rule in a backend.
func NewTCPRequestRuleBackendCreate(backendName string, rule *models.TCPRequestRule, index int) Operation {
	return NewIndexChildOp(
		OperationCreate,
		"tcp_request_rule",
		PriorityRule,
		backendName,
		index,
		rule,
		Identity[*models.TCPRequestRule],
		executors.TCPRequestRuleBackendCreate(),
		DescribeTypedChild(OperationCreate, "TCP request rule", tcpRequestRuleIdentifier(rule), fmt.Sprintf("at index %d", index), "backend", backendName),
	)
}

// NewTCPRequestRuleBackendUpdate creates an operation to update a TCP request rule in a backend.
func NewTCPRequestRuleBackendUpdate(backendName string, rule *models.TCPRequestRule, index int) Operation {
	return NewIndexChildOp(
		OperationUpdate,
		"tcp_request_rule",
		PriorityRule,
		backendName,
		index,
		rule,
		Identity[*models.TCPRequestRule],
		executors.TCPRequestRuleBackendUpdate(),
		DescribeTypedChild(OperationUpdate, "TCP request rule", tcpRequestRuleIdentifier(rule), fmt.Sprintf("at index %d", index), "backend", backendName),
	)
}

// NewTCPRequestRuleBackendDelete creates an operation to delete a TCP request rule from a backend.
func NewTCPRequestRuleBackendDelete(backendName string, rule *models.TCPRequestRule, index int) Operation {
	return NewIndexChildOp(
		OperationDelete,
		"tcp_request_rule",
		PriorityRule,
		backendName,
		index,
		rule,
		Nil[*models.TCPRequestRule],
		executors.TCPRequestRuleBackendDelete(),
		DescribeTypedChild(OperationDelete, "TCP request rule", tcpRequestRuleIdentifier(rule), fmt.Sprintf("at index %d", index), "backend", backendName),
	)
}

// NewTCPResponseRuleBackendCreate creates an operation to create a TCP response rule in a backend.
func NewTCPResponseRuleBackendCreate(backendName string, rule *models.TCPResponseRule, index int) Operation {
	return NewIndexChildOp(
		OperationCreate,
		"tcp_response_rule",
		PriorityRule,
		backendName,
		index,
		rule,
		Identity[*models.TCPResponseRule],
		executors.TCPResponseRuleBackendCreate(),
		DescribeTypedChild(OperationCreate, "TCP response rule", tcpResponseRuleIdentifier(rule), fmt.Sprintf("at index %d", index), "backend", backendName),
	)
}

// NewTCPResponseRuleBackendUpdate creates an operation to update a TCP response rule in a backend.
func NewTCPResponseRuleBackendUpdate(backendName string, rule *models.TCPResponseRule, index int) Operation {
	return NewIndexChildOp(
		OperationUpdate,
		"tcp_response_rule",
		PriorityRule,
		backendName,
		index,
		rule,
		Identity[*models.TCPResponseRule],
		executors.TCPResponseRuleBackendUpdate(),
		DescribeTypedChild(OperationUpdate, "TCP response rule", tcpResponseRuleIdentifier(rule), fmt.Sprintf("at index %d", index), "backend", backendName),
	)
}

// NewTCPResponseRuleBackendDelete creates an operation to delete a TCP response rule from a backend.
func NewTCPResponseRuleBackendDelete(backendName string, rule *models.TCPResponseRule, index int) Operation {
	return NewIndexChildOp(
		OperationDelete,
		"tcp_response_rule",
		PriorityRule,
		backendName,
		index,
		rule,
		Nil[*models.TCPResponseRule],
		executors.TCPResponseRuleBackendDelete(),
		DescribeTypedChild(OperationDelete, "TCP response rule", tcpResponseRuleIdentifier(rule), fmt.Sprintf("at index %d", index), "backend", backendName),
	)
}

// NewStickRuleBackendCreate creates an operation to create a stick rule in a backend.
func NewStickRuleBackendCreate(backendName string, rule *models.StickRule, index int) Operation {
	return NewIndexChildOp(
		OperationCreate,
		"stick_rule",
		PriorityStickRule,
		backendName,
		index,
		rule,
		Identity[*models.StickRule],
		executors.StickRuleBackendCreate(),
		DescribeTypedChild(OperationCreate, "stick rule", stickRuleIdentifier(rule), fmt.Sprintf("at index %d", index), "backend", backendName),
	)
}

// NewStickRuleBackendUpdate creates an operation to update a stick rule in a backend.
func NewStickRuleBackendUpdate(backendName string, rule *models.StickRule, index int) Operation {
	return NewIndexChildOp(
		OperationUpdate,
		"stick_rule",
		PriorityStickRule,
		backendName,
		index,
		rule,
		Identity[*models.StickRule],
		executors.StickRuleBackendUpdate(),
		DescribeTypedChild(OperationUpdate, "stick rule", stickRuleIdentifier(rule), fmt.Sprintf("at index %d", index), "backend", backendName),
	)
}

// NewStickRuleBackendDelete creates an operation to delete a stick rule from a backend.
func NewStickRuleBackendDelete(backendName string, rule *models.StickRule, index int) Operation {
	return NewIndexChildOp(
		OperationDelete,
		"stick_rule",
		PriorityStickRule,
		backendName,
		index,
		rule,
		Nil[*models.StickRule],
		executors.StickRuleBackendDelete(),
		DescribeTypedChild(OperationDelete, "stick rule", stickRuleIdentifier(rule), fmt.Sprintf("at index %d", index), "backend", backendName),
	)
}

// NewHTTPAfterResponseRuleBackendCreate creates an operation to create an HTTP after-response rule in a backend.
func NewHTTPAfterResponseRuleBackendCreate(backendName string, rule *models.HTTPAfterResponseRule, index int) Operation {
	return NewIndexChildOp(
		OperationCreate,
		"http_after_response_rule",
		PriorityHTTPAfterRule,
		backendName,
		index,
		rule,
		Identity[*models.HTTPAfterResponseRule],
		executors.HTTPAfterResponseRuleBackendCreate(),
		DescribeTypedChild(OperationCreate, "HTTP after response rule", httpAfterResponseRuleIdentifier(rule), fmt.Sprintf("at index %d", index), "backend", backendName),
	)
}

// NewHTTPAfterResponseRuleBackendUpdate creates an operation to update an HTTP after-response rule in a backend.
func NewHTTPAfterResponseRuleBackendUpdate(backendName string, rule *models.HTTPAfterResponseRule, index int) Operation {
	return NewIndexChildOp(
		OperationUpdate,
		"http_after_response_rule",
		PriorityHTTPAfterRule,
		backendName,
		index,
		rule,
		Identity[*models.HTTPAfterResponseRule],
		executors.HTTPAfterResponseRuleBackendUpdate(),
		DescribeTypedChild(OperationUpdate, "HTTP after response rule", httpAfterResponseRuleIdentifier(rule), fmt.Sprintf("at index %d", index), "backend", backendName),
	)
}

// NewHTTPAfterResponseRuleBackendDelete creates an operation to delete an HTTP after-response rule from a backend.
func NewHTTPAfterResponseRuleBackendDelete(backendName string, rule *models.HTTPAfterResponseRule, index int) Operation {
	return NewIndexChildOp(
		OperationDelete,
		"http_after_response_rule",
		PriorityHTTPAfterRule,
		backendName,
		index,
		rule,
		Nil[*models.HTTPAfterResponseRule],
		executors.HTTPAfterResponseRuleBackendDelete(),
		DescribeTypedChild(OperationDelete, "HTTP after response rule", httpAfterResponseRuleIdentifier(rule), fmt.Sprintf("at index %d", index), "backend", backendName),
	)
}

// NewHTTPCheckBackendCreate creates an operation to create an HTTP check in a backend.
func NewHTTPCheckBackendCreate(backendName string, check *models.HTTPCheck, index int) Operation {
	return NewIndexChildOp(
		OperationCreate,
		"http_check",
		PriorityHTTPCheck,
		backendName,
		index,
		check,
		Identity[*models.HTTPCheck],
		executors.HTTPCheckBackendCreate(),
		DescribeTypedChild(OperationCreate, "HTTP check", httpCheckIdentifier(check), fmt.Sprintf("at index %d", index), "backend", backendName),
	)
}

// NewHTTPCheckBackendUpdate creates an operation to update an HTTP check in a backend.
func NewHTTPCheckBackendUpdate(backendName string, check *models.HTTPCheck, index int) Operation {
	return NewIndexChildOp(
		OperationUpdate,
		"http_check",
		PriorityHTTPCheck,
		backendName,
		index,
		check,
		Identity[*models.HTTPCheck],
		executors.HTTPCheckBackendUpdate(),
		DescribeTypedChild(OperationUpdate, "HTTP check", httpCheckIdentifier(check), fmt.Sprintf("at index %d", index), "backend", backendName),
	)
}

// NewHTTPCheckBackendDelete creates an operation to delete an HTTP check from a backend.
func NewHTTPCheckBackendDelete(backendName string, check *models.HTTPCheck, index int) Operation {
	return NewIndexChildOp(
		OperationDelete,
		"http_check",
		PriorityHTTPCheck,
		backendName,
		index,
		check,
		Nil[*models.HTTPCheck],
		executors.HTTPCheckBackendDelete(),
		DescribeTypedChild(OperationDelete, "HTTP check", httpCheckIdentifier(check), fmt.Sprintf("at index %d", index), "backend", backendName),
	)
}

// NewTCPCheckBackendCreate creates an operation to create a TCP check in a backend.
func NewTCPCheckBackendCreate(backendName string, check *models.TCPCheck, index int) Operation {
	return NewIndexChildOp(
		OperationCreate,
		"tcp_check",
		PriorityTCPCheck,
		backendName,
		index,
		check,
		Identity[*models.TCPCheck],
		executors.TCPCheckBackendCreate(),
		DescribeTypedChild(OperationCreate, "TCP check", tcpCheckIdentifier(check), fmt.Sprintf("at index %d", index), "backend", backendName),
	)
}

// NewTCPCheckBackendUpdate creates an operation to update a TCP check in a backend.
func NewTCPCheckBackendUpdate(backendName string, check *models.TCPCheck, index int) Operation {
	return NewIndexChildOp(
		OperationUpdate,
		"tcp_check",
		PriorityTCPCheck,
		backendName,
		index,
		check,
		Identity[*models.TCPCheck],
		executors.TCPCheckBackendUpdate(),
		DescribeTypedChild(OperationUpdate, "TCP check", tcpCheckIdentifier(check), fmt.Sprintf("at index %d", index), "backend", backendName),
	)
}

// NewTCPCheckBackendDelete creates an operation to delete a TCP check from a backend.
func NewTCPCheckBackendDelete(backendName string, check *models.TCPCheck, index int) Operation {
	return NewIndexChildOp(
		OperationDelete,
		"tcp_check",
		PriorityTCPCheck,
		backendName,
		index,
		check,
		Nil[*models.TCPCheck],
		executors.TCPCheckBackendDelete(),
		DescribeTypedChild(OperationDelete, "TCP check", tcpCheckIdentifier(check), fmt.Sprintf("at index %d", index), "backend", backendName),
	)
}

// NewCaptureFrontendCreate creates an operation to create a capture declaration in a frontend.
func NewCaptureFrontendCreate(frontendName string, capture *models.Capture, index int) Operation {
	return NewIndexChildOp(
		OperationCreate,
		"capture",
		PriorityCapture,
		frontendName,
		index,
		capture,
		Identity[*models.Capture],
		executors.DeclareCaptureFrontendCreate(),
		DescribeTypedChild(OperationCreate, "capture", captureIdentifier(capture), fmt.Sprintf("at index %d", index), "frontend", frontendName),
	)
}

// NewCaptureFrontendUpdate creates an operation to update a capture declaration in a frontend.
func NewCaptureFrontendUpdate(frontendName string, capture *models.Capture, index int) Operation {
	return NewIndexChildOp(
		OperationUpdate,
		"capture",
		PriorityCapture,
		frontendName,
		index,
		capture,
		Identity[*models.Capture],
		executors.DeclareCaptureFrontendUpdate(),
		DescribeTypedChild(OperationUpdate, "capture", captureIdentifier(capture), fmt.Sprintf("at index %d", index), "frontend", frontendName),
	)
}

// NewCaptureFrontendDelete creates an operation to delete a capture declaration from a frontend.
func NewCaptureFrontendDelete(frontendName string, capture *models.Capture, index int) Operation {
	return NewIndexChildOp(
		OperationDelete,
		"capture",
		PriorityCapture,
		frontendName,
		index,
		capture,
		Nil[*models.Capture],
		executors.DeclareCaptureFrontendDelete(),
		DescribeTypedChild(OperationDelete, "capture", captureIdentifier(capture), fmt.Sprintf("at index %d", index), "frontend", frontendName),
	)
}
