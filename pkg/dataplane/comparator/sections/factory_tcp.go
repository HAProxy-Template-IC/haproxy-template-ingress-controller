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

// describeCapture generates a human-readable description for a capture operation.
func describeCapture(opType OperationType, capture *models.Capture, frontendName string, index int) string {
	identifier := capture.Type
	if identifier == "" {
		identifier = fmt.Sprintf("at index %d", index)
	} else {
		identifier = fmt.Sprintf("(%s)", identifier)
	}
	switch opType {
	case OperationCreate:
		return fmt.Sprintf("Create capture %s in frontend '%s'", identifier, frontendName)
	case OperationUpdate:
		return fmt.Sprintf("Update capture %s in frontend '%s'", identifier, frontendName)
	case OperationDelete:
		return fmt.Sprintf("Delete capture %s from frontend '%s'", identifier, frontendName)
	default:
		return fmt.Sprintf("Unknown operation on capture %s in frontend '%s'", identifier, frontendName)
	}
}

// describeTCPRequestRule generates a human-readable description for a TCP request rule operation.
func describeTCPRequestRule(opType OperationType, rule *models.TCPRequestRule, parentType, parentName string, index int) string {
	identifier := rule.Type
	if identifier == "" {
		identifier = fmt.Sprintf("at index %d", index)
	} else {
		identifier = fmt.Sprintf("(%s)", identifier)
	}
	switch opType {
	case OperationCreate:
		return fmt.Sprintf("Create TCP request rule %s in %s '%s'", identifier, parentType, parentName)
	case OperationUpdate:
		return fmt.Sprintf("Update TCP request rule %s in %s '%s'", identifier, parentType, parentName)
	case OperationDelete:
		return fmt.Sprintf("Delete TCP request rule %s from %s '%s'", identifier, parentType, parentName)
	default:
		return fmt.Sprintf("Unknown operation on TCP request rule %s in %s '%s'", identifier, parentType, parentName)
	}
}

// describeTCPResponseRule generates a human-readable description for a TCP response rule operation.
// TCP response rules can only exist in backends.
func describeTCPResponseRule(opType OperationType, rule *models.TCPResponseRule, parentName string, index int) string {
	identifier := rule.Type
	if identifier == "" {
		identifier = fmt.Sprintf("at index %d", index)
	} else {
		identifier = fmt.Sprintf("(%s)", identifier)
	}
	switch opType {
	case OperationCreate:
		return fmt.Sprintf("Create TCP response rule %s in backend '%s'", identifier, parentName)
	case OperationUpdate:
		return fmt.Sprintf("Update TCP response rule %s in backend '%s'", identifier, parentName)
	case OperationDelete:
		return fmt.Sprintf("Delete TCP response rule %s from backend '%s'", identifier, parentName)
	default:
		return fmt.Sprintf("Unknown operation on TCP response rule %s in backend '%s'", identifier, parentName)
	}
}

// describeHTTPCheck generates a human-readable description for an HTTP check operation.
func describeHTTPCheck(opType OperationType, check *models.HTTPCheck, backendName string, index int) string {
	identifier := check.Type
	if identifier == "" {
		identifier = fmt.Sprintf("at index %d", index)
	} else {
		identifier = fmt.Sprintf("(%s)", identifier)
	}
	switch opType {
	case OperationCreate:
		return fmt.Sprintf("Create HTTP check %s in backend '%s'", identifier, backendName)
	case OperationUpdate:
		return fmt.Sprintf("Update HTTP check %s in backend '%s'", identifier, backendName)
	case OperationDelete:
		return fmt.Sprintf("Delete HTTP check %s from backend '%s'", identifier, backendName)
	default:
		return fmt.Sprintf("Unknown operation on HTTP check %s in backend '%s'", identifier, backendName)
	}
}

// describeTCPCheck generates a human-readable description for a TCP check operation.
func describeTCPCheck(opType OperationType, check *models.TCPCheck, backendName string, index int) string {
	identifier := check.Action
	if identifier == "" {
		identifier = fmt.Sprintf("at index %d", index)
	} else {
		identifier = fmt.Sprintf("(%s)", identifier)
	}
	switch opType {
	case OperationCreate:
		return fmt.Sprintf("Create TCP check %s in backend '%s'", identifier, backendName)
	case OperationUpdate:
		return fmt.Sprintf("Update TCP check %s in backend '%s'", identifier, backendName)
	case OperationDelete:
		return fmt.Sprintf("Delete TCP check %s from backend '%s'", identifier, backendName)
	default:
		return fmt.Sprintf("Unknown operation on TCP check %s in backend '%s'", identifier, backendName)
	}
}

// describeStickRule generates a human-readable description for a stick rule operation.
func describeStickRule(opType OperationType, rule *models.StickRule, backendName string, index int) string {
	identifier := rule.Type
	if identifier == "" {
		identifier = fmt.Sprintf("at index %d", index)
	} else {
		identifier = fmt.Sprintf("(%s)", identifier)
	}
	switch opType {
	case OperationCreate:
		return fmt.Sprintf("Create stick rule %s in backend '%s'", identifier, backendName)
	case OperationUpdate:
		return fmt.Sprintf("Update stick rule %s in backend '%s'", identifier, backendName)
	case OperationDelete:
		return fmt.Sprintf("Delete stick rule %s from backend '%s'", identifier, backendName)
	default:
		return fmt.Sprintf("Unknown operation on stick rule %s in backend '%s'", identifier, backendName)
	}
}

// describeHTTPAfterResponseRule generates a human-readable description for an HTTP after response rule operation.
func describeHTTPAfterResponseRule(opType OperationType, rule *models.HTTPAfterResponseRule, backendName string, index int) string {
	identifier := rule.Type
	if identifier == "" {
		identifier = fmt.Sprintf("at index %d", index)
	} else {
		identifier = fmt.Sprintf("(%s)", identifier)
	}
	switch opType {
	case OperationCreate:
		return fmt.Sprintf("Create HTTP after response rule %s in backend '%s'", identifier, backendName)
	case OperationUpdate:
		return fmt.Sprintf("Update HTTP after response rule %s in backend '%s'", identifier, backendName)
	case OperationDelete:
		return fmt.Sprintf("Delete HTTP after response rule %s from backend '%s'", identifier, backendName)
	default:
		return fmt.Sprintf("Unknown operation on HTTP after response rule %s in backend '%s'", identifier, backendName)
	}
}

// NewTCPRequestRuleFrontendCreate creates an operation to create a TCP request rule in a frontend.
func NewTCPRequestRuleFrontendCreate(frontendName string, rule *models.TCPRequestRule, index int) Operation {
	return NewIndexChildOp(
		OperationCreate,
		"tcp_request_rule",
		PriorityRule,
		frontendName,
		index,
		rule,
		IdentityTCPRequestRule,
		executors.TCPRequestRuleFrontendCreate(),
		func() string { return describeTCPRequestRule(OperationCreate, rule, "frontend", frontendName, index) },
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
		IdentityTCPRequestRule,
		executors.TCPRequestRuleFrontendUpdate(),
		func() string { return describeTCPRequestRule(OperationUpdate, rule, "frontend", frontendName, index) },
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
		NilTCPRequestRule,
		executors.TCPRequestRuleFrontendDelete(),
		func() string { return describeTCPRequestRule(OperationDelete, rule, "frontend", frontendName, index) },
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
		IdentityTCPRequestRule,
		executors.TCPRequestRuleBackendCreate(),
		func() string { return describeTCPRequestRule(OperationCreate, rule, "backend", backendName, index) },
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
		IdentityTCPRequestRule,
		executors.TCPRequestRuleBackendUpdate(),
		func() string { return describeTCPRequestRule(OperationUpdate, rule, "backend", backendName, index) },
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
		NilTCPRequestRule,
		executors.TCPRequestRuleBackendDelete(),
		func() string { return describeTCPRequestRule(OperationDelete, rule, "backend", backendName, index) },
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
		IdentityTCPResponseRule,
		executors.TCPResponseRuleBackendCreate(),
		func() string { return describeTCPResponseRule(OperationCreate, rule, backendName, index) },
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
		IdentityTCPResponseRule,
		executors.TCPResponseRuleBackendUpdate(),
		func() string { return describeTCPResponseRule(OperationUpdate, rule, backendName, index) },
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
		NilTCPResponseRule,
		executors.TCPResponseRuleBackendDelete(),
		func() string { return describeTCPResponseRule(OperationDelete, rule, backendName, index) },
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
		IdentityStickRule,
		executors.StickRuleBackendCreate(),
		func() string { return describeStickRule(OperationCreate, rule, backendName, index) },
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
		IdentityStickRule,
		executors.StickRuleBackendUpdate(),
		func() string { return describeStickRule(OperationUpdate, rule, backendName, index) },
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
		NilStickRule,
		executors.StickRuleBackendDelete(),
		func() string { return describeStickRule(OperationDelete, rule, backendName, index) },
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
		IdentityHTTPAfterResponseRule,
		executors.HTTPAfterResponseRuleBackendCreate(),
		func() string { return describeHTTPAfterResponseRule(OperationCreate, rule, backendName, index) },
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
		IdentityHTTPAfterResponseRule,
		executors.HTTPAfterResponseRuleBackendUpdate(),
		func() string { return describeHTTPAfterResponseRule(OperationUpdate, rule, backendName, index) },
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
		NilHTTPAfterResponseRule,
		executors.HTTPAfterResponseRuleBackendDelete(),
		func() string { return describeHTTPAfterResponseRule(OperationDelete, rule, backendName, index) },
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
		IdentityHTTPCheck,
		executors.HTTPCheckBackendCreate(),
		func() string { return describeHTTPCheck(OperationCreate, check, backendName, index) },
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
		IdentityHTTPCheck,
		executors.HTTPCheckBackendUpdate(),
		func() string { return describeHTTPCheck(OperationUpdate, check, backendName, index) },
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
		NilHTTPCheck,
		executors.HTTPCheckBackendDelete(),
		func() string { return describeHTTPCheck(OperationDelete, check, backendName, index) },
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
		IdentityTCPCheck,
		executors.TCPCheckBackendCreate(),
		func() string { return describeTCPCheck(OperationCreate, check, backendName, index) },
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
		IdentityTCPCheck,
		executors.TCPCheckBackendUpdate(),
		func() string { return describeTCPCheck(OperationUpdate, check, backendName, index) },
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
		NilTCPCheck,
		executors.TCPCheckBackendDelete(),
		func() string { return describeTCPCheck(OperationDelete, check, backendName, index) },
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
		IdentityCapture,
		executors.DeclareCaptureFrontendCreate(),
		func() string { return describeCapture(OperationCreate, capture, frontendName, index) },
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
		IdentityCapture,
		executors.DeclareCaptureFrontendUpdate(),
		func() string { return describeCapture(OperationUpdate, capture, frontendName, index) },
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
		NilCapture,
		executors.DeclareCaptureFrontendDelete(),
		func() string { return describeCapture(OperationDelete, capture, frontendName, index) },
	)
}
