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
	httpRequestRuleFrontendOps = NewIndexChildCRUD[*models.HTTPRequestRule](
		"http_request_rule", "HTTP request rule", "frontend", PriorityRule, httpRequestRuleIdentifier,
		executors.HTTPRequestRuleFrontendCreate(), executors.HTTPRequestRuleFrontendUpdate(), executors.HTTPRequestRuleFrontendDelete(),
	)
	httpRequestRuleBackendOps = NewIndexChildCRUD[*models.HTTPRequestRule](
		"http_request_rule", "HTTP request rule", "backend", PriorityRule, httpRequestRuleIdentifier,
		executors.HTTPRequestRuleBackendCreate(), executors.HTTPRequestRuleBackendUpdate(), executors.HTTPRequestRuleBackendDelete(),
	)
	httpResponseRuleFrontendOps = NewIndexChildCRUD[*models.HTTPResponseRule](
		"http_response_rule", "HTTP response rule", "frontend", PriorityRule, httpResponseRuleIdentifier,
		executors.HTTPResponseRuleFrontendCreate(), executors.HTTPResponseRuleFrontendUpdate(), executors.HTTPResponseRuleFrontendDelete(),
	)
	httpResponseRuleBackendOps = NewIndexChildCRUD[*models.HTTPResponseRule](
		"http_response_rule", "HTTP response rule", "backend", PriorityRule, httpResponseRuleIdentifier,
		executors.HTTPResponseRuleBackendCreate(), executors.HTTPResponseRuleBackendUpdate(), executors.HTTPResponseRuleBackendDelete(),
	)
)

// NewHTTPRequestRuleFrontendCreate creates an operation to create an HTTP request rule in a frontend.
func NewHTTPRequestRuleFrontendCreate(frontendName string, rule *models.HTTPRequestRule, index int) Operation {
	return httpRequestRuleFrontendOps.Create(frontendName, rule, index)
}

// NewHTTPRequestRuleFrontendUpdate creates an operation to update an HTTP request rule in a frontend.
func NewHTTPRequestRuleFrontendUpdate(frontendName string, rule *models.HTTPRequestRule, index int) Operation {
	return httpRequestRuleFrontendOps.Update(frontendName, rule, index)
}

// NewHTTPRequestRuleFrontendDelete creates an operation to delete an HTTP request rule from a frontend.
func NewHTTPRequestRuleFrontendDelete(frontendName string, rule *models.HTTPRequestRule, index int) Operation {
	return httpRequestRuleFrontendOps.Delete(frontendName, rule, index)
}

// NewHTTPRequestRuleBackendCreate creates an operation to create an HTTP request rule in a backend.
func NewHTTPRequestRuleBackendCreate(backendName string, rule *models.HTTPRequestRule, index int) Operation {
	return httpRequestRuleBackendOps.Create(backendName, rule, index)
}

// NewHTTPRequestRuleBackendUpdate creates an operation to update an HTTP request rule in a backend.
func NewHTTPRequestRuleBackendUpdate(backendName string, rule *models.HTTPRequestRule, index int) Operation {
	return httpRequestRuleBackendOps.Update(backendName, rule, index)
}

// NewHTTPRequestRuleBackendDelete creates an operation to delete an HTTP request rule from a backend.
func NewHTTPRequestRuleBackendDelete(backendName string, rule *models.HTTPRequestRule, index int) Operation {
	return httpRequestRuleBackendOps.Delete(backendName, rule, index)
}

// NewHTTPResponseRuleFrontendCreate creates an operation to create an HTTP response rule in a frontend.
func NewHTTPResponseRuleFrontendCreate(frontendName string, rule *models.HTTPResponseRule, index int) Operation {
	return httpResponseRuleFrontendOps.Create(frontendName, rule, index)
}

// NewHTTPResponseRuleFrontendUpdate creates an operation to update an HTTP response rule in a frontend.
func NewHTTPResponseRuleFrontendUpdate(frontendName string, rule *models.HTTPResponseRule, index int) Operation {
	return httpResponseRuleFrontendOps.Update(frontendName, rule, index)
}

// NewHTTPResponseRuleFrontendDelete creates an operation to delete an HTTP response rule from a frontend.
func NewHTTPResponseRuleFrontendDelete(frontendName string, rule *models.HTTPResponseRule, index int) Operation {
	return httpResponseRuleFrontendOps.Delete(frontendName, rule, index)
}

// NewHTTPResponseRuleBackendCreate creates an operation to create an HTTP response rule in a backend.
func NewHTTPResponseRuleBackendCreate(backendName string, rule *models.HTTPResponseRule, index int) Operation {
	return httpResponseRuleBackendOps.Create(backendName, rule, index)
}

// NewHTTPResponseRuleBackendUpdate creates an operation to update an HTTP response rule in a backend.
func NewHTTPResponseRuleBackendUpdate(backendName string, rule *models.HTTPResponseRule, index int) Operation {
	return httpResponseRuleBackendOps.Update(backendName, rule, index)
}

// NewHTTPResponseRuleBackendDelete creates an operation to delete an HTTP response rule from a backend.
func NewHTTPResponseRuleBackendDelete(backendName string, rule *models.HTTPResponseRule, index int) Operation {
	return httpResponseRuleBackendOps.Delete(backendName, rule, index)
}
