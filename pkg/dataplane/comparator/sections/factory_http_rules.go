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

// httpRequestRuleIdentifier extracts the type identifier from an HTTPRequestRule model.
func httpRequestRuleIdentifier(rule *models.HTTPRequestRule) string {
	if rule != nil {
		return rule.Type
	}
	return ""
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

// CRUD builders for HTTP request and response rules.
var (
	HTTPRequestRuleFrontendOps  = NewIndexChildCRUD[*models.HTTPRequestRule]("http_request_rule", "HTTP request rule", "frontend", httpRequestRuleIdentifier)
	HTTPRequestRuleBackendOps   = NewIndexChildCRUD[*models.HTTPRequestRule]("http_request_rule", "HTTP request rule", "backend", httpRequestRuleIdentifier)
	HTTPResponseRuleFrontendOps = NewIndexChildCRUD[*models.HTTPResponseRule]("http_response_rule", "HTTP response rule", "frontend", httpResponseRuleIdentifier)
	HTTPResponseRuleBackendOps  = NewIndexChildCRUD[*models.HTTPResponseRule]("http_response_rule", "HTTP response rule", "backend", httpResponseRuleIdentifier)
)
