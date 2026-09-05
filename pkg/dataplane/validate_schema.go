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

//go:build playground

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
	validator := getValidatorForVersion(version)

	backendErrors := validateBackendSectionsGenerated(validator, parsed.Backends, parsed.ServerIndex, parsed.ServerTemplateIndex)
	frontendErrors := validateFrontendSectionsGenerated(validator, parsed.Frontends, parsed.BindIndex)
	validationErrors := make([]string, 0, len(backendErrors)+len(frontendErrors))

	// Validate backend sections using pointer indexes
	validationErrors = append(validationErrors, backendErrors...)

	// Validate frontend sections using pointer indexes
	validationErrors = append(validationErrors, frontendErrors...)

	if len(validationErrors) > 0 {
		return fmt.Errorf("API schema validation failed:\n  - %s",
			strings.Join(validationErrors, "\n  - "))
	}

	return nil
}

// validateBackendSectionsGenerated validates backend sections using generated validators.
// Uses pointer indexes for zero-copy iteration over servers and server templates.
func validateBackendSectionsGenerated(validator *validators.ValidatorSet, backends []*models.Backend, serverIndex map[string]map[string]*models.Server, serverTemplateIndex map[string]map[string]*models.ServerTemplate) []string {
	errors := make([]string, 0, len(backends))
	for i := range backends {
		backend := backends[i]
		errors = append(errors, validateBackendServersGenerated(validator, backend.Name, serverIndex, serverTemplateIndex)...)
		errors = append(errors, validateBackendRulesGenerated(validator, backend)...)
		errors = append(errors, validateBackendChecksGenerated(validator, backend)...)
	}
	return errors
}

// validateFrontendSectionsGenerated validates frontend sections using generated validators.
// Uses pointer indexes for zero-copy iteration over binds.
func validateFrontendSectionsGenerated(validator *validators.ValidatorSet, frontends []*models.Frontend, bindIndex map[string]map[string]*models.Bind) []string {
	errors := make([]string, 0, len(frontends))
	for i := range frontends {
		frontend := frontends[i]
		errors = append(errors, validateFrontendBindsGenerated(validator, frontend.Name, bindIndex)...)
		errors = append(errors, validateFrontendRulesGenerated(validator, frontend)...)
		errors = append(errors, validateFrontendElementsGenerated(validator, frontend)...)
	}
	return errors
}

// validateBackendServersGenerated validates servers and server templates in a backend.
// Uses pointer indexes for zero-copy iteration - servers and templates are already pointers.
func validateBackendServersGenerated(validator *validators.ValidatorSet, backendName string, serverIndex map[string]map[string]*models.Server, serverTemplateIndex map[string]map[string]*models.ServerTemplate) []string {
	servers := serverIndex[backendName]
	templates := serverTemplateIndex[backendName]
	errors := make([]string, 0, len(servers)+len(templates))

	// Validate servers using pointer index - no copies
	for serverName, server := range servers {
		if err := validator.ValidateServer(server); err != nil {
			errors = append(errors, fmt.Sprintf("backend %s, server %s: %v", backendName, serverName, err))
		}
	}

	// Validate server templates using pointer index - no copies
	for templateName, template := range templates {
		if err := validator.ValidateServerTemplate(template); err != nil {
			errors = append(errors, fmt.Sprintf("backend %s, server template %s: %v", backendName, templateName, err))
		}
	}
	return errors
}

// validateBackendRulesGenerated validates various rule types in a backend.
func validateBackendRulesGenerated(validator *validators.ValidatorSet, backend *models.Backend) []string {
	name := "backend " + backend.Name
	errors := validateBackendHTTPRules(validator, backend, name)
	errors = append(errors, validateBackendTCPRules(validator, backend, name)...)
	errors = append(errors, validateBackendMiscRules(validator, backend, name)...)
	return errors
}

// validateBackendHTTPRules validates HTTP-related rules in a backend.
func validateBackendHTTPRules(validator *validators.ValidatorSet, backend *models.Backend, name string) []string {
	var errors []string
	errors = appendIndexedErrors(errors, backend.HTTPRequestRuleList, validator.ValidateHTTPRequestRule, name, "http-request rule")
	errors = appendIndexedErrors(errors, backend.HTTPResponseRuleList, validator.ValidateHTTPResponseRule, name, "http-response rule")
	errors = appendIndexedErrors(errors, backend.HTTPAfterResponseRuleList, validator.ValidateHTTPAfterResponseRule, name, "http-after-response rule")
	errors = appendIndexedErrors(errors, backend.HTTPErrorRuleList, validator.ValidateHTTPErrorRule, name, "http-error rule")
	return errors
}

// validateBackendTCPRules validates TCP-related rules in a backend.
func validateBackendTCPRules(validator *validators.ValidatorSet, backend *models.Backend, name string) []string {
	var errors []string
	errors = appendIndexedErrors(errors, backend.TCPRequestRuleList, validator.ValidateTCPRequestRule, name, "tcp-request rule")
	errors = appendIndexedErrors(errors, backend.TCPResponseRuleList, validator.ValidateTCPResponseRule, name, "tcp-response rule")
	return errors
}

// validateBackendMiscRules validates switching rules, ACLs, filters, and log targets.
func validateBackendMiscRules(validator *validators.ValidatorSet, backend *models.Backend, name string) []string {
	var errors []string
	errors = appendIndexedErrors(errors, backend.ServerSwitchingRuleList, validator.ValidateServerSwitchingRule, name, "server switching rule")
	errors = appendIndexedErrors(errors, backend.StickRuleList, validator.ValidateStickRule, name, "stick rule")
	errors = appendIndexedErrors(errors, backend.ACLList, validator.ValidateACL, name, "ACL")
	errors = appendIndexedErrors(errors, backend.FilterList, validator.ValidateFilter, name, "filter")
	errors = appendIndexedErrors(errors, backend.LogTargetList, validator.ValidateLogTarget, name, "log target")
	return errors
}

// validateBackendChecksGenerated validates health checks in a backend.
func validateBackendChecksGenerated(validator *validators.ValidatorSet, backend *models.Backend) []string {
	var errors []string
	name := "backend " + backend.Name
	errors = appendIndexedErrors(errors, backend.HTTPCheckList, validator.ValidateHTTPCheck, name, "http-check")
	errors = appendIndexedErrors(errors, backend.TCPCheckRuleList, validator.ValidateTCPCheck, name, "tcp-check")
	return errors
}

// validateFrontendBindsGenerated validates bind configurations in a frontend.
// Uses pointer indexes for zero-copy iteration - binds are already pointers.
func validateFrontendBindsGenerated(validator *validators.ValidatorSet, frontendName string, bindIndex map[string]map[string]*models.Bind) []string {
	binds := bindIndex[frontendName]
	errors := make([]string, 0, len(binds))

	// Validate binds using pointer index - no copies
	for bindName, bind := range binds {
		if err := validator.ValidateBind(bind); err != nil {
			errors = append(errors, fmt.Sprintf("frontend %s, bind %s: %v", frontendName, bindName, err))
		}
	}
	return errors
}

// validateFrontendRulesGenerated validates various rule types in a frontend.
func validateFrontendRulesGenerated(validator *validators.ValidatorSet, frontend *models.Frontend) []string {
	name := "frontend " + frontend.Name
	errors := validateFrontendHTTPRules(validator, frontend, name)
	errors = append(errors, validateFrontendOtherRules(validator, frontend, name)...)
	return errors
}

// validateFrontendHTTPRules validates HTTP-related rules in a frontend.
func validateFrontendHTTPRules(validator *validators.ValidatorSet, frontend *models.Frontend, name string) []string {
	var errors []string
	errors = appendIndexedErrors(errors, frontend.HTTPRequestRuleList, validator.ValidateHTTPRequestRule, name, "http-request rule")
	errors = appendIndexedErrors(errors, frontend.HTTPResponseRuleList, validator.ValidateHTTPResponseRule, name, "http-response rule")
	errors = appendIndexedErrors(errors, frontend.HTTPAfterResponseRuleList, validator.ValidateHTTPAfterResponseRule, name, "http-after-response rule")
	errors = appendIndexedErrors(errors, frontend.HTTPErrorRuleList, validator.ValidateHTTPErrorRule, name, "http-error rule")
	return errors
}

// validateFrontendOtherRules validates TCP rules, backend switching rules, and ACLs.
func validateFrontendOtherRules(validator *validators.ValidatorSet, frontend *models.Frontend, name string) []string {
	var errors []string
	errors = appendIndexedErrors(errors, frontend.TCPRequestRuleList, validator.ValidateTCPRequestRule, name, "tcp-request rule")
	errors = appendIndexedErrors(errors, frontend.BackendSwitchingRuleList, validator.ValidateBackendSwitchingRule, name, "backend switching rule")
	errors = appendIndexedErrors(errors, frontend.ACLList, validator.ValidateACL, name, "ACL")
	return errors
}

// validateFrontendElementsGenerated validates other frontend elements (filters, log targets, captures).
func validateFrontendElementsGenerated(validator *validators.ValidatorSet, frontend *models.Frontend) []string {
	var errors []string
	name := "frontend " + frontend.Name
	errors = appendIndexedErrors(errors, frontend.FilterList, validator.ValidateFilter, name, "filter")
	errors = appendIndexedErrors(errors, frontend.LogTargetList, validator.ValidateLogTarget, name, "log target")
	errors = appendIndexedErrors(errors, frontend.CaptureList, validator.ValidateCapture, name, "capture")
	return errors
}

// appendIndexedErrors validates each item in items via validate and appends a
// formatted error string for every failure to errors. The format mirrors the
// shape used by every list-style schema check in this file:
//
//	"<name>, <label> <idx>: <err>"
//
// e.g. "backend api, http-request rule 3: <err>". The constraint ~[]T accepts
// the named slice types from client-native (HTTPRequestRules, HTTPResponseRules,
// etc.) without an explicit conversion at the call site.
func appendIndexedErrors[L ~[]T, T any](errors []string, items L, validate func(T) error, name, label string) []string {
	for idx, item := range items {
		if err := validate(item); err != nil {
			errors = append(errors, fmt.Sprintf("%s, %s %d: %v", name, label, idx, err))
		}
	}
	return errors
}
