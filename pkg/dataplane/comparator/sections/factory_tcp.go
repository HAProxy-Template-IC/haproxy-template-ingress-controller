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

// Index-child CRUD builders for TCP/HTTP rules, checks, sticks and captures.
var (
	tcpRequestRuleFrontendOps        = NewIndexChildCRUD[*models.TCPRequestRule]("tcp_request_rule", "TCP request rule", "frontend", tcpRequestRuleIdentifier)
	tcpRequestRuleBackendOps         = NewIndexChildCRUD[*models.TCPRequestRule]("tcp_request_rule", "TCP request rule", "backend", tcpRequestRuleIdentifier)
	tcpResponseRuleBackendOps        = NewIndexChildCRUD[*models.TCPResponseRule]("tcp_response_rule", "TCP response rule", "backend", tcpResponseRuleIdentifier)
	stickRuleBackendOps              = NewIndexChildCRUD[*models.StickRule]("stick_rule", "stick rule", "backend", stickRuleIdentifier)
	httpAfterResponseRuleBackendOps  = NewIndexChildCRUD[*models.HTTPAfterResponseRule]("http_after_response_rule", "HTTP after response rule", "backend", httpAfterResponseRuleIdentifier)
	httpAfterResponseRuleFrontendOps = NewIndexChildCRUD[*models.HTTPAfterResponseRule]("http_after_response_rule", "HTTP after response rule", "frontend", httpAfterResponseRuleIdentifier)
	httpCheckBackendOps              = NewIndexChildCRUD[*models.HTTPCheck]("http_check", "HTTP check", "backend", httpCheckIdentifier)
	tcpCheckBackendOps               = NewIndexChildCRUD[*models.TCPCheck]("tcp_check", "TCP check", "backend", tcpCheckIdentifier)
	captureFrontendOps               = NewIndexChildCRUD[*models.Capture]("capture", "capture", "frontend", captureIdentifier)
)

// NewTCPRequestRuleFrontendCreate creates an operation to create a TCP request rule in a frontend.
func NewTCPRequestRuleFrontendCreate(frontendName string, rule *models.TCPRequestRule, index int) Operation {
	return tcpRequestRuleFrontendOps.Create(frontendName, rule, index)
}

// NewTCPRequestRuleFrontendUpdate creates an operation to update a TCP request rule in a frontend.
func NewTCPRequestRuleFrontendUpdate(frontendName string, rule *models.TCPRequestRule, index int) Operation {
	return tcpRequestRuleFrontendOps.Update(frontendName, rule, index)
}

// NewTCPRequestRuleFrontendDelete creates an operation to delete a TCP request rule from a frontend.
func NewTCPRequestRuleFrontendDelete(frontendName string, rule *models.TCPRequestRule, index int) Operation {
	return tcpRequestRuleFrontendOps.Delete(frontendName, rule, index)
}

// NewTCPRequestRuleBackendCreate creates an operation to create a TCP request rule in a backend.
func NewTCPRequestRuleBackendCreate(backendName string, rule *models.TCPRequestRule, index int) Operation {
	return tcpRequestRuleBackendOps.Create(backendName, rule, index)
}

// NewTCPRequestRuleBackendUpdate creates an operation to update a TCP request rule in a backend.
func NewTCPRequestRuleBackendUpdate(backendName string, rule *models.TCPRequestRule, index int) Operation {
	return tcpRequestRuleBackendOps.Update(backendName, rule, index)
}

// NewTCPRequestRuleBackendDelete creates an operation to delete a TCP request rule from a backend.
func NewTCPRequestRuleBackendDelete(backendName string, rule *models.TCPRequestRule, index int) Operation {
	return tcpRequestRuleBackendOps.Delete(backendName, rule, index)
}

// NewTCPResponseRuleBackendCreate creates an operation to create a TCP response rule in a backend.
func NewTCPResponseRuleBackendCreate(backendName string, rule *models.TCPResponseRule, index int) Operation {
	return tcpResponseRuleBackendOps.Create(backendName, rule, index)
}

// NewTCPResponseRuleBackendUpdate creates an operation to update a TCP response rule in a backend.
func NewTCPResponseRuleBackendUpdate(backendName string, rule *models.TCPResponseRule, index int) Operation {
	return tcpResponseRuleBackendOps.Update(backendName, rule, index)
}

// NewTCPResponseRuleBackendDelete creates an operation to delete a TCP response rule from a backend.
func NewTCPResponseRuleBackendDelete(backendName string, rule *models.TCPResponseRule, index int) Operation {
	return tcpResponseRuleBackendOps.Delete(backendName, rule, index)
}

// NewStickRuleBackendCreate creates an operation to create a stick rule in a backend.
func NewStickRuleBackendCreate(backendName string, rule *models.StickRule, index int) Operation {
	return stickRuleBackendOps.Create(backendName, rule, index)
}

// NewStickRuleBackendUpdate creates an operation to update a stick rule in a backend.
func NewStickRuleBackendUpdate(backendName string, rule *models.StickRule, index int) Operation {
	return stickRuleBackendOps.Update(backendName, rule, index)
}

// NewStickRuleBackendDelete creates an operation to delete a stick rule from a backend.
func NewStickRuleBackendDelete(backendName string, rule *models.StickRule, index int) Operation {
	return stickRuleBackendOps.Delete(backendName, rule, index)
}

// NewHTTPAfterResponseRuleBackendCreate creates an operation to create an HTTP after-response rule in a backend.
func NewHTTPAfterResponseRuleBackendCreate(backendName string, rule *models.HTTPAfterResponseRule, index int) Operation {
	return httpAfterResponseRuleBackendOps.Create(backendName, rule, index)
}

// NewHTTPAfterResponseRuleBackendUpdate creates an operation to update an HTTP after-response rule in a backend.
func NewHTTPAfterResponseRuleBackendUpdate(backendName string, rule *models.HTTPAfterResponseRule, index int) Operation {
	return httpAfterResponseRuleBackendOps.Update(backendName, rule, index)
}

// NewHTTPAfterResponseRuleBackendDelete creates an operation to delete an HTTP after-response rule from a backend.
func NewHTTPAfterResponseRuleBackendDelete(backendName string, rule *models.HTTPAfterResponseRule, index int) Operation {
	return httpAfterResponseRuleBackendOps.Delete(backendName, rule, index)
}

// NewHTTPAfterResponseRuleFrontendCreate creates an operation to create an HTTP after-response rule in a frontend.
func NewHTTPAfterResponseRuleFrontendCreate(frontendName string, rule *models.HTTPAfterResponseRule, index int) Operation {
	return httpAfterResponseRuleFrontendOps.Create(frontendName, rule, index)
}

// NewHTTPAfterResponseRuleFrontendUpdate creates an operation to update an HTTP after-response rule in a frontend.
func NewHTTPAfterResponseRuleFrontendUpdate(frontendName string, rule *models.HTTPAfterResponseRule, index int) Operation {
	return httpAfterResponseRuleFrontendOps.Update(frontendName, rule, index)
}

// NewHTTPAfterResponseRuleFrontendDelete creates an operation to delete an HTTP after-response rule from a frontend.
func NewHTTPAfterResponseRuleFrontendDelete(frontendName string, rule *models.HTTPAfterResponseRule, index int) Operation {
	return httpAfterResponseRuleFrontendOps.Delete(frontendName, rule, index)
}

// NewHTTPCheckBackendCreate creates an operation to create an HTTP check in a backend.
func NewHTTPCheckBackendCreate(backendName string, check *models.HTTPCheck, index int) Operation {
	return httpCheckBackendOps.Create(backendName, check, index)
}

// NewHTTPCheckBackendUpdate creates an operation to update an HTTP check in a backend.
func NewHTTPCheckBackendUpdate(backendName string, check *models.HTTPCheck, index int) Operation {
	return httpCheckBackendOps.Update(backendName, check, index)
}

// NewHTTPCheckBackendDelete creates an operation to delete an HTTP check from a backend.
func NewHTTPCheckBackendDelete(backendName string, check *models.HTTPCheck, index int) Operation {
	return httpCheckBackendOps.Delete(backendName, check, index)
}

// NewTCPCheckBackendCreate creates an operation to create a TCP check in a backend.
func NewTCPCheckBackendCreate(backendName string, check *models.TCPCheck, index int) Operation {
	return tcpCheckBackendOps.Create(backendName, check, index)
}

// NewTCPCheckBackendUpdate creates an operation to update a TCP check in a backend.
func NewTCPCheckBackendUpdate(backendName string, check *models.TCPCheck, index int) Operation {
	return tcpCheckBackendOps.Update(backendName, check, index)
}

// NewTCPCheckBackendDelete creates an operation to delete a TCP check from a backend.
func NewTCPCheckBackendDelete(backendName string, check *models.TCPCheck, index int) Operation {
	return tcpCheckBackendOps.Delete(backendName, check, index)
}

// NewCaptureFrontendCreate creates an operation to create a capture declaration in a frontend.
func NewCaptureFrontendCreate(frontendName string, capture *models.Capture, index int) Operation {
	return captureFrontendOps.Create(frontendName, capture, index)
}

// NewCaptureFrontendUpdate creates an operation to update a capture declaration in a frontend.
func NewCaptureFrontendUpdate(frontendName string, capture *models.Capture, index int) Operation {
	return captureFrontendOps.Update(frontendName, capture, index)
}

// NewCaptureFrontendDelete creates an operation to delete a capture declaration from a frontend.
func NewCaptureFrontendDelete(frontendName string, capture *models.Capture, index int) Operation {
	return captureFrontendOps.Delete(frontendName, capture, index)
}
