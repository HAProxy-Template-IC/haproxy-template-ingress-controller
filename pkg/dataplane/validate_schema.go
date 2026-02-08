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

package dataplane

import (
	"fmt"
	"strings"

	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/validators"
)

// validateAPISchema performs API schema validation using generated validators.
// This validates parsed configuration models against the Dataplane API's OpenAPI
// schema constraints (patterns, formats, required fields).
//
// Uses zero-allocation generated validators instead of the generic kin-openapi
// validator to eliminate the ~25GB allocation overhead from JSON conversions.
// Uses pointer indexes from StructuredConfig for zero-copy iteration over nested elements.
func validateAPISchema(parsed *parser.StructuredConfig, version *Version) error {
	cv := getCachedValidatorForVersion(version)

	var validationErrors []string

	// Validate backend sections using pointer indexes
	validationErrors = append(validationErrors, validateBackendSectionsGenerated(cv, parsed.Backends, parsed.ServerIndex, parsed.ServerTemplateIndex)...)

	// Validate frontend sections using pointer indexes
	validationErrors = append(validationErrors, validateFrontendSectionsGenerated(cv, parsed.Frontends, parsed.BindIndex)...)

	if len(validationErrors) > 0 {
		return fmt.Errorf("API schema validation failed:\n  - %s",
			strings.Join(validationErrors, "\n  - "))
	}

	return nil
}

// validateBackendSectionsGenerated validates backend sections using generated validators.
// Uses pointer indexes for zero-copy iteration over servers and server templates.
func validateBackendSectionsGenerated(cv *validators.CachedValidator, backends []*models.Backend, serverIndex map[string]map[string]*models.Server, serverTemplateIndex map[string]map[string]*models.ServerTemplate) []string {
	var errors []string
	for i := range backends {
		backend := backends[i]
		errors = append(errors, validateBackendServersGenerated(cv, backend.Name, serverIndex, serverTemplateIndex)...)
		errors = append(errors, validateBackendRulesGenerated(cv, backend)...)
		errors = append(errors, validateBackendChecksGenerated(cv, backend)...)
	}
	return errors
}

// validateFrontendSectionsGenerated validates frontend sections using generated validators.
// Uses pointer indexes for zero-copy iteration over binds.
func validateFrontendSectionsGenerated(cv *validators.CachedValidator, frontends []*models.Frontend, bindIndex map[string]map[string]*models.Bind) []string {
	var errors []string
	for i := range frontends {
		frontend := frontends[i]
		errors = append(errors, validateFrontendBindsGenerated(cv, frontend.Name, bindIndex)...)
		errors = append(errors, validateFrontendRulesGenerated(cv, frontend)...)
		errors = append(errors, validateFrontendElementsGenerated(cv, frontend)...)
	}
	return errors
}

// validateBackendServersGenerated validates servers and server templates in a backend.
// Uses pointer indexes for zero-copy iteration - servers and templates are already pointers.
func validateBackendServersGenerated(cv *validators.CachedValidator, backendName string, serverIndex map[string]map[string]*models.Server, serverTemplateIndex map[string]map[string]*models.ServerTemplate) []string {
	servers := serverIndex[backendName]
	templates := serverTemplateIndex[backendName]
	errors := make([]string, 0, len(servers)+len(templates))

	// Validate servers using pointer index - no copies
	for serverName, server := range servers {
		if err := cv.ValidateServer(server); err != nil {
			errors = append(errors, fmt.Sprintf("backend %s, server %s: %v", backendName, serverName, err))
		}
	}

	// Validate server templates using pointer index - no copies
	for templateName, template := range templates {
		if err := cv.ValidateServerTemplate(template); err != nil {
			errors = append(errors, fmt.Sprintf("backend %s, server template %s: %v", backendName, templateName, err))
		}
	}
	return errors
}

// validateBackendRulesGenerated validates various rule types in a backend.
func validateBackendRulesGenerated(cv *validators.CachedValidator, backend *models.Backend) []string {
	name := "backend " + backend.Name
	errors := validateBackendHTTPRules(cv, backend, name)
	errors = append(errors, validateBackendTCPRules(cv, backend, name)...)
	errors = append(errors, validateBackendMiscRules(cv, backend, name)...)
	return errors
}

// validateBackendHTTPRules validates HTTP-related rules in a backend.
func validateBackendHTTPRules(cv *validators.CachedValidator, backend *models.Backend, name string) []string {
	var errors []string
	for idx, rule := range backend.HTTPRequestRuleList {
		if err := cv.ValidateHTTPRequestRule(rule); err != nil {
			errors = append(errors, fmt.Sprintf("%s, http-request rule %d: %v", name, idx, err))
		}
	}
	for idx, rule := range backend.HTTPResponseRuleList {
		if err := cv.ValidateHTTPResponseRule(rule); err != nil {
			errors = append(errors, fmt.Sprintf("%s, http-response rule %d: %v", name, idx, err))
		}
	}
	for idx, rule := range backend.HTTPAfterResponseRuleList {
		if err := cv.ValidateHTTPAfterResponseRule(rule); err != nil {
			errors = append(errors, fmt.Sprintf("%s, http-after-response rule %d: %v", name, idx, err))
		}
	}
	for idx, rule := range backend.HTTPErrorRuleList {
		if err := cv.ValidateHTTPErrorRule(rule); err != nil {
			errors = append(errors, fmt.Sprintf("%s, http-error rule %d: %v", name, idx, err))
		}
	}
	return errors
}

// validateBackendTCPRules validates TCP-related rules in a backend.
func validateBackendTCPRules(cv *validators.CachedValidator, backend *models.Backend, name string) []string {
	var errors []string
	for idx, rule := range backend.TCPRequestRuleList {
		if err := cv.ValidateTCPRequestRule(rule); err != nil {
			errors = append(errors, fmt.Sprintf("%s, tcp-request rule %d: %v", name, idx, err))
		}
	}
	for idx, rule := range backend.TCPResponseRuleList {
		if err := cv.ValidateTCPResponseRule(rule); err != nil {
			errors = append(errors, fmt.Sprintf("%s, tcp-response rule %d: %v", name, idx, err))
		}
	}
	return errors
}

// validateBackendMiscRules validates switching rules, ACLs, filters, and log targets.
func validateBackendMiscRules(cv *validators.CachedValidator, backend *models.Backend, name string) []string {
	var errors []string
	for idx, rule := range backend.ServerSwitchingRuleList {
		if err := cv.ValidateServerSwitchingRule(rule); err != nil {
			errors = append(errors, fmt.Sprintf("%s, server switching rule %d: %v", name, idx, err))
		}
	}
	for idx, rule := range backend.StickRuleList {
		if err := cv.ValidateStickRule(rule); err != nil {
			errors = append(errors, fmt.Sprintf("%s, stick rule %d: %v", name, idx, err))
		}
	}
	for idx, acl := range backend.ACLList {
		if err := cv.ValidateACL(acl); err != nil {
			errors = append(errors, fmt.Sprintf("%s, ACL %d: %v", name, idx, err))
		}
	}
	for idx, filter := range backend.FilterList {
		if err := cv.ValidateFilter(filter); err != nil {
			errors = append(errors, fmt.Sprintf("%s, filter %d: %v", name, idx, err))
		}
	}
	for idx, target := range backend.LogTargetList {
		if err := cv.ValidateLogTarget(target); err != nil {
			errors = append(errors, fmt.Sprintf("%s, log target %d: %v", name, idx, err))
		}
	}
	return errors
}

// validateBackendChecksGenerated validates health checks in a backend.
func validateBackendChecksGenerated(cv *validators.CachedValidator, backend *models.Backend) []string {
	var errors []string
	name := "backend " + backend.Name

	for idx, check := range backend.HTTPCheckList {
		if err := cv.ValidateHTTPCheck(check); err != nil {
			errors = append(errors, fmt.Sprintf("%s, http-check %d: %v", name, idx, err))
		}
	}
	for idx, check := range backend.TCPCheckRuleList {
		if err := cv.ValidateTCPCheck(check); err != nil {
			errors = append(errors, fmt.Sprintf("%s, tcp-check %d: %v", name, idx, err))
		}
	}
	return errors
}

// validateFrontendBindsGenerated validates bind configurations in a frontend.
// Uses pointer indexes for zero-copy iteration - binds are already pointers.
func validateFrontendBindsGenerated(cv *validators.CachedValidator, frontendName string, bindIndex map[string]map[string]*models.Bind) []string {
	binds := bindIndex[frontendName]
	errors := make([]string, 0, len(binds))

	// Validate binds using pointer index - no copies
	for bindName, bind := range binds {
		if err := cv.ValidateBind(bind); err != nil {
			errors = append(errors, fmt.Sprintf("frontend %s, bind %s: %v", frontendName, bindName, err))
		}
	}
	return errors
}

// validateFrontendRulesGenerated validates various rule types in a frontend.
func validateFrontendRulesGenerated(cv *validators.CachedValidator, frontend *models.Frontend) []string {
	name := "frontend " + frontend.Name
	errors := validateFrontendHTTPRules(cv, frontend, name)
	errors = append(errors, validateFrontendOtherRules(cv, frontend, name)...)
	return errors
}

// validateFrontendHTTPRules validates HTTP-related rules in a frontend.
func validateFrontendHTTPRules(cv *validators.CachedValidator, frontend *models.Frontend, name string) []string {
	var errors []string
	for idx, rule := range frontend.HTTPRequestRuleList {
		if err := cv.ValidateHTTPRequestRule(rule); err != nil {
			errors = append(errors, fmt.Sprintf("%s, http-request rule %d: %v", name, idx, err))
		}
	}
	for idx, rule := range frontend.HTTPResponseRuleList {
		if err := cv.ValidateHTTPResponseRule(rule); err != nil {
			errors = append(errors, fmt.Sprintf("%s, http-response rule %d: %v", name, idx, err))
		}
	}
	for idx, rule := range frontend.HTTPAfterResponseRuleList {
		if err := cv.ValidateHTTPAfterResponseRule(rule); err != nil {
			errors = append(errors, fmt.Sprintf("%s, http-after-response rule %d: %v", name, idx, err))
		}
	}
	for idx, rule := range frontend.HTTPErrorRuleList {
		if err := cv.ValidateHTTPErrorRule(rule); err != nil {
			errors = append(errors, fmt.Sprintf("%s, http-error rule %d: %v", name, idx, err))
		}
	}
	return errors
}

// validateFrontendOtherRules validates TCP rules, backend switching rules, and ACLs.
func validateFrontendOtherRules(cv *validators.CachedValidator, frontend *models.Frontend, name string) []string {
	var errors []string
	for idx, rule := range frontend.TCPRequestRuleList {
		if err := cv.ValidateTCPRequestRule(rule); err != nil {
			errors = append(errors, fmt.Sprintf("%s, tcp-request rule %d: %v", name, idx, err))
		}
	}
	for idx, rule := range frontend.BackendSwitchingRuleList {
		if err := cv.ValidateBackendSwitchingRule(rule); err != nil {
			errors = append(errors, fmt.Sprintf("%s, backend switching rule %d: %v", name, idx, err))
		}
	}
	for idx, acl := range frontend.ACLList {
		if err := cv.ValidateACL(acl); err != nil {
			errors = append(errors, fmt.Sprintf("%s, ACL %d: %v", name, idx, err))
		}
	}
	return errors
}

// validateFrontendElementsGenerated validates other frontend elements (filters, log targets, captures).
func validateFrontendElementsGenerated(cv *validators.CachedValidator, frontend *models.Frontend) []string {
	var errors []string
	name := "frontend " + frontend.Name

	for idx, filter := range frontend.FilterList {
		if err := cv.ValidateFilter(filter); err != nil {
			errors = append(errors, fmt.Sprintf("%s, filter %d: %v", name, idx, err))
		}
	}
	for idx, target := range frontend.LogTargetList {
		if err := cv.ValidateLogTarget(target); err != nil {
			errors = append(errors, fmt.Sprintf("%s, log target %d: %v", name, idx, err))
		}
	}
	for idx, capture := range frontend.CaptureList {
		if err := cv.ValidateCapture(capture); err != nil {
			errors = append(errors, fmt.Sprintf("%s, capture %d: %v", name, idx, err))
		}
	}
	return errors
}
