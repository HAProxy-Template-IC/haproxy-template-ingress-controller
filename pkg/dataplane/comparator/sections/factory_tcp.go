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
	TCPRequestRuleFrontendOps        = NewIndexChildCRUD[*models.TCPRequestRule]("tcp_request_rule", "TCP request rule", "frontend", tcpRequestRuleIdentifier)
	TCPRequestRuleBackendOps         = NewIndexChildCRUD[*models.TCPRequestRule]("tcp_request_rule", "TCP request rule", "backend", tcpRequestRuleIdentifier)
	TCPResponseRuleBackendOps        = NewIndexChildCRUD[*models.TCPResponseRule]("tcp_response_rule", "TCP response rule", "backend", tcpResponseRuleIdentifier)
	StickRuleBackendOps              = NewIndexChildCRUD[*models.StickRule]("stick_rule", "stick rule", "backend", stickRuleIdentifier)
	HTTPAfterResponseRuleBackendOps  = NewIndexChildCRUD[*models.HTTPAfterResponseRule]("http_after_response_rule", "HTTP after response rule", "backend", httpAfterResponseRuleIdentifier)
	HTTPAfterResponseRuleFrontendOps = NewIndexChildCRUD[*models.HTTPAfterResponseRule]("http_after_response_rule", "HTTP after response rule", "frontend", httpAfterResponseRuleIdentifier)
	HTTPCheckBackendOps              = NewIndexChildCRUD[*models.HTTPCheck]("http_check", "HTTP check", "backend", httpCheckIdentifier)
	TCPCheckBackendOps               = NewIndexChildCRUD[*models.TCPCheck]("tcp_check", "TCP check", "backend", tcpCheckIdentifier)
	CaptureFrontendOps               = NewIndexChildCRUD[*models.Capture]("capture", "capture", "frontend", captureIdentifier)
)
