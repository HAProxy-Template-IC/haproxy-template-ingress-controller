// Package sections provides factory functions for creating HAProxy configuration operations.
//
// These factory functions use generic operation types to eliminate repetitive boilerplate
// while maintaining type safety and compile-time verification.
package sections

import (
	"context"
	"fmt"

	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections/executors"
)

// Operation defines the interface for all HAProxy configuration operations.
// This interface is implemented by all generic operation types (TopLevelOp, IndexChildOp, etc.)
type Operation interface {
	// Type returns the operation type (Create, Update, Delete)
	Type() OperationType

	// Section returns the configuration section this operation affects
	Section() string

	// Priority returns the execution priority (lower = first for creates, higher = first for deletes)
	Priority() int

	// Execute performs the operation via the Dataplane API
	Execute(ctx context.Context, c *client.DataplaneClient, txID string) error

	// Describe returns a human-readable description of the operation
	Describe() string
}

// RuntimeReloadTracker is an optional interface for operations that can track
// whether they triggered a HAProxy reload during runtime execution.
// Only server update operations implement this interface since they can be
// executed via the runtime API without a transaction.
type RuntimeReloadTracker interface {
	// TriggeredReload returns true if the operation triggered a HAProxy reload
	// during its last execution. This is only meaningful for runtime-eligible
	// operations (server updates with empty transaction ID).
	TriggeredReload() bool
}

// ptrStr safely dereferences a string pointer, returning empty string if nil.
func ptrStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// unknownIdentifier is the fallback identifier used when a model field is empty.
const unknownIdentifier = "<unknown>"

// =============================================================================
// Backend Factory Functions
// =============================================================================

// NewBackendCreate creates an operation to create a backend.
func NewBackendCreate(backend *models.Backend) Operation {
	return NewTopLevelOp(
		OperationCreate,
		"backend",
		PriorityBackend,
		backend,
		IdentityBackend,
		BackendName,
		executors.BackendCreate(),
		DescribeTopLevel(OperationCreate, "backend", backend.Name),
	)
}

// NewBackendUpdate creates an operation to update a backend.
func NewBackendUpdate(backend *models.Backend) Operation {
	return NewTopLevelOp(
		OperationUpdate,
		"backend",
		PriorityBackend,
		backend,
		IdentityBackend,
		BackendName,
		executors.BackendUpdate(),
		DescribeTopLevel(OperationUpdate, "backend", backend.Name),
	)
}

// NewBackendDelete creates an operation to delete a backend.
func NewBackendDelete(backend *models.Backend) Operation {
	return NewTopLevelOp(
		OperationDelete,
		"backend",
		PriorityBackend,
		backend,
		NilBackend,
		BackendName,
		executors.BackendDelete(),
		DescribeTopLevel(OperationDelete, "backend", backend.Name),
	)
}

// =============================================================================
// Frontend Factory Functions
// =============================================================================

// NewFrontendCreate creates an operation to create a frontend.
func NewFrontendCreate(frontend *models.Frontend) Operation {
	return NewTopLevelOp(
		OperationCreate,
		"frontend",
		PriorityFrontend,
		frontend,
		IdentityFrontend,
		FrontendName,
		executors.FrontendCreate(),
		DescribeTopLevel(OperationCreate, "frontend", frontend.Name),
	)
}

// NewFrontendUpdate creates an operation to update a frontend.
func NewFrontendUpdate(frontend *models.Frontend) Operation {
	return NewTopLevelOp(
		OperationUpdate,
		"frontend",
		PriorityFrontend,
		frontend,
		IdentityFrontend,
		FrontendName,
		executors.FrontendUpdate(),
		DescribeTopLevel(OperationUpdate, "frontend", frontend.Name),
	)
}

// NewFrontendDelete creates an operation to delete a frontend.
func NewFrontendDelete(frontend *models.Frontend) Operation {
	return NewTopLevelOp(
		OperationDelete,
		"frontend",
		PriorityFrontend,
		frontend,
		NilFrontend,
		FrontendName,
		executors.FrontendDelete(),
		DescribeTopLevel(OperationDelete, "frontend", frontend.Name),
	)
}

// =============================================================================
// Defaults Factory Functions
// =============================================================================

// NewDefaultsCreate creates an operation to create a defaults section.
func NewDefaultsCreate(defaults *models.Defaults) Operation {
	return NewTopLevelOp(
		OperationCreate,
		"defaults",
		PriorityDefaults,
		defaults,
		IdentityDefaults,
		DefaultsName,
		executors.DefaultsCreate(),
		DescribeTopLevel(OperationCreate, "defaults section", defaults.Name),
	)
}

// NewDefaultsUpdate creates an operation to update a defaults section.
func NewDefaultsUpdate(defaults *models.Defaults) Operation {
	return NewTopLevelOp(
		OperationUpdate,
		"defaults",
		PriorityDefaults,
		defaults,
		IdentityDefaults,
		DefaultsName,
		executors.DefaultsUpdate(),
		DescribeTopLevel(OperationUpdate, "defaults section", defaults.Name),
	)
}

// NewDefaultsDelete creates an operation to delete a defaults section.
func NewDefaultsDelete(defaults *models.Defaults) Operation {
	return NewTopLevelOp(
		OperationDelete,
		"defaults",
		PriorityDefaults,
		defaults,
		NilDefaults,
		DefaultsName,
		executors.DefaultsDelete(),
		DescribeTopLevel(OperationDelete, "defaults section", defaults.Name),
	)
}

// =============================================================================
// Global Factory Functions (Singleton)
// =============================================================================

// NewGlobalUpdate creates an operation to update the global section.
func NewGlobalUpdate(global *models.Global) Operation {
	return NewSingletonOp(
		OperationUpdate,
		"global",
		PriorityGlobal,
		global,
		IdentityGlobal,
		executors.GlobalUpdate(),
		func() string { return "Update global section" },
	)
}

// =============================================================================
// ACL Factory Functions (Index-based child)
// =============================================================================

// NewACLFrontendCreate creates an operation to create an ACL in a frontend.
func NewACLFrontendCreate(frontendName string, acl *models.ACL, index int) Operation {
	return NewIndexChildOp(
		OperationCreate,
		"acl",
		PriorityACL,
		frontendName,
		index,
		acl,
		IdentityACL,
		executors.ACLFrontendCreate(),
		DescribeACL(OperationCreate, acl.ACLName, "frontend", frontendName),
	)
}

// NewACLFrontendUpdate creates an operation to update an ACL in a frontend.
func NewACLFrontendUpdate(frontendName string, acl *models.ACL, index int) Operation {
	return NewIndexChildOp(
		OperationUpdate,
		"acl",
		PriorityACL,
		frontendName,
		index,
		acl,
		IdentityACL,
		executors.ACLFrontendUpdate(),
		DescribeACL(OperationUpdate, acl.ACLName, "frontend", frontendName),
	)
}

// NewACLFrontendDelete creates an operation to delete an ACL from a frontend.
func NewACLFrontendDelete(frontendName string, acl *models.ACL, index int) Operation {
	return NewIndexChildOp(
		OperationDelete,
		"acl",
		PriorityACL,
		frontendName,
		index,
		acl,
		NilACL,
		executors.ACLFrontendDelete(),
		DescribeACL(OperationDelete, acl.ACLName, "frontend", frontendName),
	)
}

// NewACLBackendCreate creates an operation to create an ACL in a backend.
func NewACLBackendCreate(backendName string, acl *models.ACL, index int) Operation {
	return NewIndexChildOp(
		OperationCreate,
		"acl",
		PriorityACL,
		backendName,
		index,
		acl,
		IdentityACL,
		executors.ACLBackendCreate(),
		DescribeACL(OperationCreate, acl.ACLName, "backend", backendName),
	)
}

// NewACLBackendUpdate creates an operation to update an ACL in a backend.
func NewACLBackendUpdate(backendName string, acl *models.ACL, index int) Operation {
	return NewIndexChildOp(
		OperationUpdate,
		"acl",
		PriorityACL,
		backendName,
		index,
		acl,
		IdentityACL,
		executors.ACLBackendUpdate(),
		DescribeACL(OperationUpdate, acl.ACLName, "backend", backendName),
	)
}

// NewACLBackendDelete creates an operation to delete an ACL from a backend.
func NewACLBackendDelete(backendName string, acl *models.ACL, index int) Operation {
	return NewIndexChildOp(
		OperationDelete,
		"acl",
		PriorityACL,
		backendName,
		index,
		acl,
		NilACL,
		executors.ACLBackendDelete(),
		DescribeACL(OperationDelete, acl.ACLName, "backend", backendName),
	)
}

// =============================================================================
// HTTP Request Rule Factory Functions (Index-based child)
// =============================================================================

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
		IdentityHTTPRequestRule,
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
		IdentityHTTPRequestRule,
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
		NilHTTPRequestRule,
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
		IdentityHTTPRequestRule,
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
		IdentityHTTPRequestRule,
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
		NilHTTPRequestRule,
		executors.HTTPRequestRuleBackendDelete(),
		func() string { return describeHTTPRequestRule(OperationDelete, rule, "backend", backendName, index) },
	)
}

// =============================================================================
// HTTP Response Rule Factory Functions (Index-based child)
// =============================================================================

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
		IdentityHTTPResponseRule,
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
		IdentityHTTPResponseRule,
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
		NilHTTPResponseRule,
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
		IdentityHTTPResponseRule,
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
		IdentityHTTPResponseRule,
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
		NilHTTPResponseRule,
		executors.HTTPResponseRuleBackendDelete(),
		func() string { return describeHTTPResponseRule(OperationDelete, rule, "backend", backendName) },
	)
}

// =============================================================================
// Backend Switching Rule Factory Functions (Index-based child)
// =============================================================================

// describeBackendSwitchingRule creates a descriptive string for backend switching rule operations.
// Uses the rule's Name field which contains the condition expression, falls back to index if not available.
func describeBackendSwitchingRule(opType OperationType, rule *models.BackendSwitchingRule, frontendName string, index int) string {
	identifier := fmt.Sprintf("at index %d", index)
	if rule != nil && rule.Name != "" {
		identifier = fmt.Sprintf("(%s)", rule.Name)
	}

	switch opType {
	case OperationCreate:
		return fmt.Sprintf("Create backend switching rule %s in frontend '%s'", identifier, frontendName)
	case OperationUpdate:
		return fmt.Sprintf("Update backend switching rule %s in frontend '%s'", identifier, frontendName)
	case OperationDelete:
		return fmt.Sprintf("Delete backend switching rule %s from frontend '%s'", identifier, frontendName)
	default:
		return fmt.Sprintf("Unknown operation on backend switching rule %s in frontend '%s'", identifier, frontendName)
	}
}

// NewBackendSwitchingRuleFrontendCreate creates an operation to create a backend switching rule.
func NewBackendSwitchingRuleFrontendCreate(frontendName string, rule *models.BackendSwitchingRule, index int) Operation {
	return NewIndexChildOp(
		OperationCreate,
		"backend_switching_rule",
		PriorityBackendSwitchingRule,
		frontendName,
		index,
		rule,
		IdentityBackendSwitchingRule,
		executors.BackendSwitchingRuleCreate(),
		func() string { return describeBackendSwitchingRule(OperationCreate, rule, frontendName, index) },
	)
}

// NewBackendSwitchingRuleFrontendUpdate creates an operation to update a backend switching rule.
func NewBackendSwitchingRuleFrontendUpdate(frontendName string, rule *models.BackendSwitchingRule, index int) Operation {
	return NewIndexChildOp(
		OperationUpdate,
		"backend_switching_rule",
		PriorityBackendSwitchingRule,
		frontendName,
		index,
		rule,
		IdentityBackendSwitchingRule,
		executors.BackendSwitchingRuleUpdate(),
		func() string { return describeBackendSwitchingRule(OperationUpdate, rule, frontendName, index) },
	)
}

// NewBackendSwitchingRuleFrontendDelete creates an operation to delete a backend switching rule.
func NewBackendSwitchingRuleFrontendDelete(frontendName string, rule *models.BackendSwitchingRule, index int) Operation {
	return NewIndexChildOp(
		OperationDelete,
		"backend_switching_rule",
		PriorityBackendSwitchingRule,
		frontendName,
		index,
		rule,
		NilBackendSwitchingRule,
		executors.BackendSwitchingRuleDelete(),
		func() string { return describeBackendSwitchingRule(OperationDelete, rule, frontendName, index) },
	)
}

// =============================================================================
// Filter Factory Functions (Index-based child)
// =============================================================================

// NewFilterFrontendCreate creates an operation to create a filter in a frontend.
func NewFilterFrontendCreate(frontendName string, filter *models.Filter, index int) Operation {
	return NewIndexChildOp(
		OperationCreate,
		"filter",
		PriorityFilter,
		frontendName,
		index,
		filter,
		IdentityFilter,
		executors.FilterFrontendCreate(),
		func() string { return describeFilter(OperationCreate, filter, "frontend", frontendName, index) },
	)
}

// NewFilterFrontendUpdate creates an operation to update a filter in a frontend.
func NewFilterFrontendUpdate(frontendName string, filter *models.Filter, index int) Operation {
	return NewIndexChildOp(
		OperationUpdate,
		"filter",
		PriorityFilter,
		frontendName,
		index,
		filter,
		IdentityFilter,
		executors.FilterFrontendUpdate(),
		func() string { return describeFilter(OperationUpdate, filter, "frontend", frontendName, index) },
	)
}

// NewFilterFrontendDelete creates an operation to delete a filter from a frontend.
func NewFilterFrontendDelete(frontendName string, filter *models.Filter, index int) Operation {
	return NewIndexChildOp(
		OperationDelete,
		"filter",
		PriorityFilter,
		frontendName,
		index,
		filter,
		NilFilter,
		executors.FilterFrontendDelete(),
		func() string { return describeFilter(OperationDelete, filter, "frontend", frontendName, index) },
	)
}

// NewFilterBackendCreate creates an operation to create a filter in a backend.
func NewFilterBackendCreate(backendName string, filter *models.Filter, index int) Operation {
	return NewIndexChildOp(
		OperationCreate,
		"filter",
		PriorityFilter,
		backendName,
		index,
		filter,
		IdentityFilter,
		executors.FilterBackendCreate(),
		func() string { return describeFilter(OperationCreate, filter, "backend", backendName, index) },
	)
}

// NewFilterBackendUpdate creates an operation to update a filter in a backend.
func NewFilterBackendUpdate(backendName string, filter *models.Filter, index int) Operation {
	return NewIndexChildOp(
		OperationUpdate,
		"filter",
		PriorityFilter,
		backendName,
		index,
		filter,
		IdentityFilter,
		executors.FilterBackendUpdate(),
		func() string { return describeFilter(OperationUpdate, filter, "backend", backendName, index) },
	)
}

// NewFilterBackendDelete creates an operation to delete a filter from a backend.
func NewFilterBackendDelete(backendName string, filter *models.Filter, index int) Operation {
	return NewIndexChildOp(
		OperationDelete,
		"filter",
		PriorityFilter,
		backendName,
		index,
		filter,
		NilFilter,
		executors.FilterBackendDelete(),
		func() string { return describeFilter(OperationDelete, filter, "backend", backendName, index) },
	)
}

// =============================================================================
// Log Target Factory Functions (Index-based child)
// =============================================================================

// NewLogTargetFrontendCreate creates an operation to create a log target in a frontend.
func NewLogTargetFrontendCreate(frontendName string, logTarget *models.LogTarget, index int) Operation {
	return NewIndexChildOp(
		OperationCreate,
		"log_target",
		PriorityLogTarget,
		frontendName,
		index,
		logTarget,
		IdentityLogTarget,
		executors.LogTargetFrontendCreate(),
		func() string { return describeLogTarget(OperationCreate, logTarget, "frontend", frontendName, index) },
	)
}

// NewLogTargetFrontendUpdate creates an operation to update a log target in a frontend.
func NewLogTargetFrontendUpdate(frontendName string, logTarget *models.LogTarget, index int) Operation {
	return NewIndexChildOp(
		OperationUpdate,
		"log_target",
		PriorityLogTarget,
		frontendName,
		index,
		logTarget,
		IdentityLogTarget,
		executors.LogTargetFrontendUpdate(),
		func() string { return describeLogTarget(OperationUpdate, logTarget, "frontend", frontendName, index) },
	)
}

// NewLogTargetFrontendDelete creates an operation to delete a log target from a frontend.
func NewLogTargetFrontendDelete(frontendName string, logTarget *models.LogTarget, index int) Operation {
	return NewIndexChildOp(
		OperationDelete,
		"log_target",
		PriorityLogTarget,
		frontendName,
		index,
		logTarget,
		NilLogTarget,
		executors.LogTargetFrontendDelete(),
		func() string { return describeLogTarget(OperationDelete, logTarget, "frontend", frontendName, index) },
	)
}

// NewLogTargetBackendCreate creates an operation to create a log target in a backend.
func NewLogTargetBackendCreate(backendName string, logTarget *models.LogTarget, index int) Operation {
	return NewIndexChildOp(
		OperationCreate,
		"log_target",
		PriorityLogTarget,
		backendName,
		index,
		logTarget,
		IdentityLogTarget,
		executors.LogTargetBackendCreate(),
		func() string { return describeLogTarget(OperationCreate, logTarget, "backend", backendName, index) },
	)
}

// NewLogTargetBackendUpdate creates an operation to update a log target in a backend.
func NewLogTargetBackendUpdate(backendName string, logTarget *models.LogTarget, index int) Operation {
	return NewIndexChildOp(
		OperationUpdate,
		"log_target",
		PriorityLogTarget,
		backendName,
		index,
		logTarget,
		IdentityLogTarget,
		executors.LogTargetBackendUpdate(),
		func() string { return describeLogTarget(OperationUpdate, logTarget, "backend", backendName, index) },
	)
}

// NewLogTargetBackendDelete creates an operation to delete a log target from a backend.
func NewLogTargetBackendDelete(backendName string, logTarget *models.LogTarget, index int) Operation {
	return NewIndexChildOp(
		OperationDelete,
		"log_target",
		PriorityLogTarget,
		backendName,
		index,
		logTarget,
		NilLogTarget,
		executors.LogTargetBackendDelete(),
		func() string { return describeLogTarget(OperationDelete, logTarget, "backend", backendName, index) },
	)
}

// =============================================================================
// Bind Factory Functions (Name-based child)
// =============================================================================

// describeBindWithSSL creates a description for bind operations that includes SSL info.
// This matches the legacy behavior where bind descriptions included SSL certificate paths.
func describeBindWithSSL(opType OperationType, bind *models.Bind, frontendName string) string {
	// Format bind description based on address and port
	bindDesc := ""
	if bind.Address != "" && bind.Port != nil {
		bindDesc = fmt.Sprintf("%s:%d", bind.Address, *bind.Port)
	} else if bind.Port != nil {
		bindDesc = fmt.Sprintf("*:%d", *bind.Port)
	} else if bind.Name != "" {
		bindDesc = bind.Name
	}

	// Add SSL info if present
	if bind.Ssl {
		sslInfo := " ssl"
		if bind.SslCertificate != "" {
			sslInfo += fmt.Sprintf(" crt %s", bind.SslCertificate)
		}
		bindDesc += sslInfo
	}

	// Use appropriate verb based on operation type
	switch opType {
	case OperationCreate:
		return fmt.Sprintf("Create bind '%s' in frontend '%s'", bindDesc, frontendName)
	case OperationUpdate:
		return fmt.Sprintf("Update bind '%s' in frontend '%s'", bindDesc, frontendName)
	case OperationDelete:
		return fmt.Sprintf("Delete bind '%s' from frontend '%s'", bindDesc, frontendName)
	default:
		return fmt.Sprintf("Unknown operation on bind '%s' in frontend '%s'", bindDesc, frontendName)
	}
}

// describeLogTarget generates a human-readable description for a log target operation.
// Uses the Address field if available (e.g., "ring@myring"), falls back to index.
func describeLogTarget(opType OperationType, logTarget *models.LogTarget, parentType, parentName string, index int) string {
	// Use Address if available (e.g., "ring@myring", "127.0.0.1:514")
	identifier := logTarget.Address
	if identifier == "" {
		identifier = fmt.Sprintf("at index %d", index)
	} else {
		identifier = fmt.Sprintf("(%s)", identifier)
	}

	// Use appropriate verb based on operation type
	switch opType {
	case OperationCreate:
		return fmt.Sprintf("Create log target %s in %s '%s'", identifier, parentType, parentName)
	case OperationUpdate:
		return fmt.Sprintf("Update log target %s in %s '%s'", identifier, parentType, parentName)
	case OperationDelete:
		return fmt.Sprintf("Delete log target %s from %s '%s'", identifier, parentType, parentName)
	default:
		return fmt.Sprintf("Unknown operation on log target %s in %s '%s'", identifier, parentType, parentName)
	}
}

// describeFilter generates a human-readable description for a filter operation.
func describeFilter(opType OperationType, filter *models.Filter, parentType, parentName string, index int) string {
	identifier := filter.Type
	if identifier == "" {
		identifier = fmt.Sprintf("at index %d", index)
	} else {
		identifier = fmt.Sprintf("(%s)", identifier)
	}
	switch opType {
	case OperationCreate:
		return fmt.Sprintf("Create filter %s in %s '%s'", identifier, parentType, parentName)
	case OperationUpdate:
		return fmt.Sprintf("Update filter %s in %s '%s'", identifier, parentType, parentName)
	case OperationDelete:
		return fmt.Sprintf("Delete filter %s from %s '%s'", identifier, parentType, parentName)
	default:
		return fmt.Sprintf("Unknown operation on filter %s in %s '%s'", identifier, parentType, parentName)
	}
}

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

// describeServerSwitchingRule generates a human-readable description for a server switching rule operation.
func describeServerSwitchingRule(opType OperationType, rule *models.ServerSwitchingRule, backendName string, index int) string {
	identifier := rule.TargetServer
	if identifier == "" {
		identifier = fmt.Sprintf("at index %d", index)
	} else {
		identifier = fmt.Sprintf("(%s)", identifier)
	}
	switch opType {
	case OperationCreate:
		return fmt.Sprintf("Create server switching rule %s in backend '%s'", identifier, backendName)
	case OperationUpdate:
		return fmt.Sprintf("Update server switching rule %s in backend '%s'", identifier, backendName)
	case OperationDelete:
		return fmt.Sprintf("Delete server switching rule %s from backend '%s'", identifier, backendName)
	default:
		return fmt.Sprintf("Unknown operation on server switching rule %s in backend '%s'", identifier, backendName)
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

// NewBindFrontendCreate creates an operation to create a bind in a frontend.
func NewBindFrontendCreate(frontendName, bindName string, bind *models.Bind) Operation {
	return NewNameChildOp(
		OperationCreate,
		"bind",
		PriorityBind,
		frontendName,
		bindName,
		bind,
		IdentityBind,
		executors.BindFrontendCreate(frontendName),
		func() string { return describeBindWithSSL(OperationCreate, bind, frontendName) },
	)
}

// NewBindFrontendUpdate creates an operation to update a bind in a frontend.
func NewBindFrontendUpdate(frontendName, bindName string, bind *models.Bind) Operation {
	return NewNameChildOp(
		OperationUpdate,
		"bind",
		PriorityBind,
		frontendName,
		bindName,
		bind,
		IdentityBind,
		executors.BindFrontendUpdate(frontendName),
		func() string { return describeBindWithSSL(OperationUpdate, bind, frontendName) },
	)
}

// NewBindFrontendDelete creates an operation to delete a bind from a frontend.
func NewBindFrontendDelete(frontendName, bindName string, bind *models.Bind) Operation {
	return NewNameChildOp(
		OperationDelete,
		"bind",
		PriorityBind,
		frontendName,
		bindName,
		bind,
		NilBind,
		executors.BindFrontendDelete(frontendName),
		func() string { return describeBindWithSSL(OperationDelete, bind, frontendName) },
	)
}

// =============================================================================
// User Factory Functions (Container child)
// =============================================================================

// NewUserCreate creates an operation to create a user in a userlist.
func NewUserCreate(userlistName string, user *models.User) Operation {
	return NewContainerChildOp(
		OperationCreate,
		"user",
		PriorityUser,
		userlistName,
		user,
		IdentityUser,
		UserName,
		executors.UserCreate(userlistName),
		DescribeContainerChild(OperationCreate, "user", user.Username, "userlist", userlistName),
	)
}

// NewUserUpdate creates an operation to update a user in a userlist.
func NewUserUpdate(userlistName string, user *models.User) Operation {
	return NewContainerChildOp(
		OperationUpdate,
		"user",
		PriorityUser,
		userlistName,
		user,
		IdentityUser,
		UserName,
		executors.UserUpdate(userlistName),
		DescribeContainerChild(OperationUpdate, "user", user.Username, "userlist", userlistName),
	)
}

// NewUserDelete creates an operation to delete a user from a userlist.
func NewUserDelete(userlistName string, user *models.User) Operation {
	return NewContainerChildOp(
		OperationDelete,
		"user",
		PriorityUser,
		userlistName,
		user,
		NilUser,
		UserName,
		executors.UserDelete(userlistName),
		DescribeContainerChild(OperationDelete, "user", user.Username, "userlist", userlistName),
	)
}

// =============================================================================
// Mailer Entry Factory Functions (Container child)
// =============================================================================

// NewMailerEntryCreate creates an operation to create a mailer entry.
func NewMailerEntryCreate(mailersName string, entry *models.MailerEntry) Operation {
	return NewContainerChildOp(
		OperationCreate,
		"mailer_entry",
		PriorityMailerEntry,
		mailersName,
		entry,
		IdentityMailerEntry,
		MailerEntryName,
		executors.MailerEntryCreate(mailersName),
		DescribeContainerChild(OperationCreate, "mailer entry", entry.Name, "mailers section", mailersName),
	)
}

// NewMailerEntryUpdate creates an operation to update a mailer entry.
func NewMailerEntryUpdate(mailersName string, entry *models.MailerEntry) Operation {
	return NewContainerChildOp(
		OperationUpdate,
		"mailer_entry",
		PriorityMailerEntry,
		mailersName,
		entry,
		IdentityMailerEntry,
		MailerEntryName,
		executors.MailerEntryUpdate(mailersName),
		DescribeContainerChild(OperationUpdate, "mailer entry", entry.Name, "mailers section", mailersName),
	)
}

// NewMailerEntryDelete creates an operation to delete a mailer entry.
func NewMailerEntryDelete(mailersName string, entry *models.MailerEntry) Operation {
	return NewContainerChildOp(
		OperationDelete,
		"mailer_entry",
		PriorityMailerEntry,
		mailersName,
		entry,
		NilMailerEntry,
		MailerEntryName,
		executors.MailerEntryDelete(mailersName),
		DescribeContainerChild(OperationDelete, "mailer entry", entry.Name, "mailers section", mailersName),
	)
}

// =============================================================================
// Peer Entry Factory Functions (Container child)
// =============================================================================

// NewPeerEntryCreate creates an operation to create a peer entry.
func NewPeerEntryCreate(peerSectionName string, entry *models.PeerEntry) Operation {
	return NewContainerChildOp(
		OperationCreate,
		"peer_entry",
		PriorityPeerEntry,
		peerSectionName,
		entry,
		IdentityPeerEntry,
		PeerEntryName,
		executors.PeerEntryCreate(peerSectionName),
		DescribeContainerChild(OperationCreate, "peer entry", entry.Name, "peer section", peerSectionName),
	)
}

// NewPeerEntryUpdate creates an operation to update a peer entry.
func NewPeerEntryUpdate(peerSectionName string, entry *models.PeerEntry) Operation {
	return NewContainerChildOp(
		OperationUpdate,
		"peer_entry",
		PriorityPeerEntry,
		peerSectionName,
		entry,
		IdentityPeerEntry,
		PeerEntryName,
		executors.PeerEntryUpdate(peerSectionName),
		DescribeContainerChild(OperationUpdate, "peer entry", entry.Name, "peer section", peerSectionName),
	)
}

// NewPeerEntryDelete creates an operation to delete a peer entry.
func NewPeerEntryDelete(peerSectionName string, entry *models.PeerEntry) Operation {
	return NewContainerChildOp(
		OperationDelete,
		"peer_entry",
		PriorityPeerEntry,
		peerSectionName,
		entry,
		NilPeerEntry,
		PeerEntryName,
		executors.PeerEntryDelete(peerSectionName),
		DescribeContainerChild(OperationDelete, "peer entry", entry.Name, "peer section", peerSectionName),
	)
}

// =============================================================================
// Nameserver Factory Functions (Container child)
// =============================================================================

// NewNameserverCreate creates an operation to create a nameserver in a resolver.
func NewNameserverCreate(resolverName string, nameserver *models.Nameserver) Operation {
	return NewContainerChildOp(
		OperationCreate,
		"nameserver",
		PriorityNameserver,
		resolverName,
		nameserver,
		IdentityNameserver,
		NameserverName,
		executors.NameserverCreate(resolverName),
		DescribeContainerChild(OperationCreate, "nameserver", nameserver.Name, "resolvers section", resolverName),
	)
}

// NewNameserverUpdate creates an operation to update a nameserver in a resolver.
func NewNameserverUpdate(resolverName string, nameserver *models.Nameserver) Operation {
	return NewContainerChildOp(
		OperationUpdate,
		"nameserver",
		PriorityNameserver,
		resolverName,
		nameserver,
		IdentityNameserver,
		NameserverName,
		executors.NameserverUpdate(resolverName),
		DescribeContainerChild(OperationUpdate, "nameserver", nameserver.Name, "resolvers section", resolverName),
	)
}

// NewNameserverDelete creates an operation to delete a nameserver from a resolver.
func NewNameserverDelete(resolverName string, nameserver *models.Nameserver) Operation {
	return NewContainerChildOp(
		OperationDelete,
		"nameserver",
		PriorityNameserver,
		resolverName,
		nameserver,
		NilNameserver,
		NameserverName,
		executors.NameserverDelete(resolverName),
		DescribeContainerChild(OperationDelete, "nameserver", nameserver.Name, "resolvers section", resolverName),
	)
}

// =============================================================================
// Server Factory Functions (Name-based child)
// =============================================================================

// NewServerCreate creates an operation to create a server in a backend.
func NewServerCreate(backendName string, server *models.Server) Operation {
	return NewNameChildOp(
		OperationCreate,
		"server",
		PriorityServer,
		backendName,
		server.Name,
		server,
		IdentityServer,
		executors.ServerCreate(backendName),
		DescribeNamedChild(OperationCreate, "server", server.Name, "backend", backendName),
	)
}

// ServerUpdateOp is a specialized operation for server updates that tracks
// whether the update triggered a HAProxy reload. This is needed because server
// updates can be executed via the runtime API (without transaction) and the
// DataPlane API returns 202 if a reload was required.
type ServerUpdateOp struct {
	backendName     string
	server          *models.Server
	reloadTriggered bool
}

// NewServerUpdate creates an operation to update a server in a backend.
// Unlike other operations, server updates use a specialized type that tracks
// reload status for runtime-eligible operations.
func NewServerUpdate(backendName string, server *models.Server) Operation {
	return &ServerUpdateOp{
		backendName: backendName,
		server:      server,
	}
}

func (op *ServerUpdateOp) Type() OperationType { return OperationUpdate }
func (op *ServerUpdateOp) Section() string     { return "server" }
func (op *ServerUpdateOp) Priority() int       { return PriorityServer * 1000 }
func (op *ServerUpdateOp) Describe() string {
	return DescribeNamedChild(OperationUpdate, "server", op.server.Name, "backend", op.backendName)()
}

// TriggeredReload implements RuntimeReloadTracker interface.
// Returns true if the last Execute call triggered a HAProxy reload.
func (op *ServerUpdateOp) TriggeredReload() bool {
	return op.reloadTriggered
}

// Execute performs the server update operation.
// When txID is empty (runtime execution), it tracks whether the operation triggered a reload.
func (op *ServerUpdateOp) Execute(ctx context.Context, c *client.DataplaneClient, txID string) error {
	reloaded, err := executors.ServerUpdateWithReloadTracking(ctx, c, op.backendName, op.server.Name, op.server, txID)
	op.reloadTriggered = reloaded
	return err
}

// NewServerDelete creates an operation to delete a server from a backend.
func NewServerDelete(backendName string, server *models.Server) Operation {
	return NewNameChildOp(
		OperationDelete,
		"server",
		PriorityServer,
		backendName,
		server.Name,
		server,
		NilServer,
		executors.ServerDelete(backendName),
		DescribeNamedChild(OperationDelete, "server", server.Name, "backend", backendName),
	)
}

// =============================================================================
// Server Template Factory Functions (Name-based child)
// =============================================================================

// NewServerTemplateCreate creates an operation to create a server template in a backend.
func NewServerTemplateCreate(backendName string, serverTemplate *models.ServerTemplate) Operation {
	return NewNameChildOp(
		OperationCreate,
		"server_template",
		PriorityServer, // Server templates use same priority as servers
		backendName,
		serverTemplate.Prefix,
		serverTemplate,
		IdentityServerTemplate,
		executors.ServerTemplateCreate(backendName),
		DescribeNamedChild(OperationCreate, "server template", serverTemplate.Prefix, "backend", backendName),
	)
}

// NewServerTemplateUpdate creates an operation to update a server template in a backend.
func NewServerTemplateUpdate(backendName string, serverTemplate *models.ServerTemplate) Operation {
	return NewNameChildOp(
		OperationUpdate,
		"server_template",
		PriorityServer, // Server templates use same priority as servers
		backendName,
		serverTemplate.Prefix,
		serverTemplate,
		IdentityServerTemplate,
		executors.ServerTemplateUpdate(backendName),
		DescribeNamedChild(OperationUpdate, "server template", serverTemplate.Prefix, "backend", backendName),
	)
}

// NewServerTemplateDelete creates an operation to delete a server template from a backend.
func NewServerTemplateDelete(backendName string, serverTemplate *models.ServerTemplate) Operation {
	return NewNameChildOp(
		OperationDelete,
		"server_template",
		PriorityServer, // Server templates use same priority as servers
		backendName,
		serverTemplate.Prefix,
		serverTemplate,
		NilServerTemplate,
		executors.ServerTemplateDelete(backendName),
		DescribeNamedChild(OperationDelete, "server template", serverTemplate.Prefix, "backend", backendName),
	)
}

// =============================================================================
// Cache Factory Functions
// =============================================================================

// NewCacheCreate creates an operation to create a cache section.
func NewCacheCreate(cache *models.Cache) Operation {
	return NewTopLevelOp(
		OperationCreate,
		"cache",
		PriorityCache,
		cache,
		IdentityCache,
		CacheName,
		executors.CacheCreate(),
		DescribeTopLevel(OperationCreate, "cache", CacheName(cache)),
	)
}

// NewCacheUpdate creates an operation to update a cache section.
func NewCacheUpdate(cache *models.Cache) Operation {
	return NewTopLevelOp(
		OperationUpdate,
		"cache",
		PriorityCache,
		cache,
		IdentityCache,
		CacheName,
		executors.CacheUpdate(),
		DescribeTopLevel(OperationUpdate, "cache", CacheName(cache)),
	)
}

// NewCacheDelete creates an operation to delete a cache section.
func NewCacheDelete(cache *models.Cache) Operation {
	return NewTopLevelOp(
		OperationDelete,
		"cache",
		PriorityCache,
		cache,
		NilCache,
		CacheName,
		executors.CacheDelete(),
		DescribeTopLevel(OperationDelete, "cache", CacheName(cache)),
	)
}

// =============================================================================
// HTTPErrorsSection Factory Functions
// =============================================================================

// NewHTTPErrorsSectionCreate creates an operation to create an http-errors section.
func NewHTTPErrorsSectionCreate(section *models.HTTPErrorsSection) Operation {
	return NewTopLevelOp(
		OperationCreate,
		"http_errors",
		PriorityHTTPErrors,
		section,
		IdentityHTTPErrorsSection,
		HTTPErrorsSectionName,
		executors.HTTPErrorsSectionCreate(),
		DescribeTopLevel(OperationCreate, "http-errors section", section.Name),
	)
}

// NewHTTPErrorsSectionUpdate creates an operation to update an http-errors section.
func NewHTTPErrorsSectionUpdate(section *models.HTTPErrorsSection) Operation {
	return NewTopLevelOp(
		OperationUpdate,
		"http_errors",
		PriorityHTTPErrors,
		section,
		IdentityHTTPErrorsSection,
		HTTPErrorsSectionName,
		executors.HTTPErrorsSectionUpdate(),
		DescribeTopLevel(OperationUpdate, "http-errors section", section.Name),
	)
}

// NewHTTPErrorsSectionDelete creates an operation to delete an http-errors section.
func NewHTTPErrorsSectionDelete(section *models.HTTPErrorsSection) Operation {
	return NewTopLevelOp(
		OperationDelete,
		"http_errors",
		PriorityHTTPErrors,
		section,
		NilHTTPErrorsSection,
		HTTPErrorsSectionName,
		executors.HTTPErrorsSectionDelete(),
		DescribeTopLevel(OperationDelete, "http-errors section", section.Name),
	)
}

// =============================================================================
// LogForward Factory Functions
// =============================================================================

// NewLogForwardCreate creates an operation to create a log-forward section.
func NewLogForwardCreate(logForward *models.LogForward) Operation {
	return NewTopLevelOp(
		OperationCreate,
		"log_forward",
		PriorityLogForward,
		logForward,
		IdentityLogForward,
		LogForwardName,
		executors.LogForwardCreate(),
		DescribeTopLevel(OperationCreate, "log-forward", logForward.Name),
	)
}

// NewLogForwardUpdate creates an operation to update a log-forward section.
func NewLogForwardUpdate(logForward *models.LogForward) Operation {
	return NewTopLevelOp(
		OperationUpdate,
		"log_forward",
		PriorityLogForward,
		logForward,
		IdentityLogForward,
		LogForwardName,
		executors.LogForwardUpdate(),
		DescribeTopLevel(OperationUpdate, "log-forward", logForward.Name),
	)
}

// NewLogForwardDelete creates an operation to delete a log-forward section.
func NewLogForwardDelete(logForward *models.LogForward) Operation {
	return NewTopLevelOp(
		OperationDelete,
		"log_forward",
		PriorityLogForward,
		logForward,
		NilLogForward,
		LogForwardName,
		executors.LogForwardDelete(),
		DescribeTopLevel(OperationDelete, "log-forward", logForward.Name),
	)
}

// =============================================================================
// MailersSection Factory Functions
// =============================================================================

// NewMailersSectionCreate creates an operation to create a mailers section.
func NewMailersSectionCreate(section *models.MailersSection) Operation {
	return NewTopLevelOp(
		OperationCreate,
		"mailers",
		PriorityMailers,
		section,
		IdentityMailersSection,
		MailersSectionName,
		executors.MailersSectionCreate(),
		DescribeTopLevel(OperationCreate, "mailers", section.Name),
	)
}

// NewMailersSectionUpdate creates an operation to update a mailers section.
func NewMailersSectionUpdate(section *models.MailersSection) Operation {
	return NewTopLevelOp(
		OperationUpdate,
		"mailers",
		PriorityMailers,
		section,
		IdentityMailersSection,
		MailersSectionName,
		executors.MailersSectionUpdate(),
		DescribeTopLevel(OperationUpdate, "mailers", section.Name),
	)
}

// NewMailersSectionDelete creates an operation to delete a mailers section.
func NewMailersSectionDelete(section *models.MailersSection) Operation {
	return NewTopLevelOp(
		OperationDelete,
		"mailers",
		PriorityMailers,
		section,
		NilMailersSection,
		MailersSectionName,
		executors.MailersSectionDelete(),
		DescribeTopLevel(OperationDelete, "mailers", section.Name),
	)
}

// =============================================================================
// PeerSection Factory Functions
// Note: Update operations return an error as the API doesn't support direct updates.
// =============================================================================

// NewPeerSectionCreate creates an operation to create a peer section.
func NewPeerSectionCreate(section *models.PeerSection) Operation {
	return NewTopLevelOp(
		OperationCreate,
		"peers",
		PriorityPeer,
		section,
		IdentityPeerSection,
		PeerSectionName,
		executors.PeerSectionCreate(),
		DescribeTopLevel(OperationCreate, "peer section", section.Name),
	)
}

// NewPeerSectionUpdate creates an operation to update a peer section.
// Note: This operation will fail at execution time as the HAProxy Dataplane API
// does not support updating peer sections directly.
func NewPeerSectionUpdate(section *models.PeerSection) Operation {
	return NewTopLevelOp(
		OperationUpdate,
		"peers",
		PriorityPeer,
		section,
		IdentityPeerSection,
		PeerSectionName,
		executors.PeerSectionUpdate(),
		DescribeTopLevel(OperationUpdate, "peer section", section.Name),
	)
}

// NewPeerSectionDelete creates an operation to delete a peer section.
func NewPeerSectionDelete(section *models.PeerSection) Operation {
	return NewTopLevelOp(
		OperationDelete,
		"peers",
		PriorityPeer,
		section,
		NilPeerSection,
		PeerSectionName,
		executors.PeerSectionDelete(),
		DescribeTopLevel(OperationDelete, "peer section", section.Name),
	)
}

// =============================================================================
// Program Factory Functions
// =============================================================================

// NewProgramCreate creates an operation to create a program section.
func NewProgramCreate(program *models.Program) Operation {
	return NewTopLevelOp(
		OperationCreate,
		"program",
		PriorityProgram,
		program,
		IdentityProgram,
		ProgramName,
		executors.ProgramCreate(),
		DescribeTopLevel(OperationCreate, "program", program.Name),
	)
}

// NewProgramUpdate creates an operation to update a program section.
func NewProgramUpdate(program *models.Program) Operation {
	return NewTopLevelOp(
		OperationUpdate,
		"program",
		PriorityProgram,
		program,
		IdentityProgram,
		ProgramName,
		executors.ProgramUpdate(),
		DescribeTopLevel(OperationUpdate, "program", program.Name),
	)
}

// NewProgramDelete creates an operation to delete a program section.
func NewProgramDelete(program *models.Program) Operation {
	return NewTopLevelOp(
		OperationDelete,
		"program",
		PriorityProgram,
		program,
		NilProgram,
		ProgramName,
		executors.ProgramDelete(),
		DescribeTopLevel(OperationDelete, "program", program.Name),
	)
}

// =============================================================================
// Resolver Factory Functions
// =============================================================================

// NewResolverCreate creates an operation to create a resolver section.
func NewResolverCreate(resolver *models.Resolver) Operation {
	return NewTopLevelOp(
		OperationCreate,
		"resolver",
		PriorityResolver,
		resolver,
		IdentityResolver,
		ResolverName,
		executors.ResolverCreate(),
		DescribeTopLevel(OperationCreate, "resolver", resolver.Name),
	)
}

// NewResolverUpdate creates an operation to update a resolver section.
func NewResolverUpdate(resolver *models.Resolver) Operation {
	return NewTopLevelOp(
		OperationUpdate,
		"resolver",
		PriorityResolver,
		resolver,
		IdentityResolver,
		ResolverName,
		executors.ResolverUpdate(),
		DescribeTopLevel(OperationUpdate, "resolver", resolver.Name),
	)
}

// NewResolverDelete creates an operation to delete a resolver section.
func NewResolverDelete(resolver *models.Resolver) Operation {
	return NewTopLevelOp(
		OperationDelete,
		"resolver",
		PriorityResolver,
		resolver,
		NilResolver,
		ResolverName,
		executors.ResolverDelete(),
		DescribeTopLevel(OperationDelete, "resolver", resolver.Name),
	)
}

// =============================================================================
// Ring Factory Functions
// =============================================================================

// NewRingCreate creates an operation to create a ring section.
func NewRingCreate(ring *models.Ring) Operation {
	return NewTopLevelOp(
		OperationCreate,
		"ring",
		PriorityRing,
		ring,
		IdentityRing,
		RingName,
		executors.RingCreate(),
		DescribeTopLevel(OperationCreate, "ring", ring.Name),
	)
}

// NewRingUpdate creates an operation to update a ring section.
func NewRingUpdate(ring *models.Ring) Operation {
	return NewTopLevelOp(
		OperationUpdate,
		"ring",
		PriorityRing,
		ring,
		IdentityRing,
		RingName,
		executors.RingUpdate(),
		DescribeTopLevel(OperationUpdate, "ring", ring.Name),
	)
}

// NewRingDelete creates an operation to delete a ring section.
func NewRingDelete(ring *models.Ring) Operation {
	return NewTopLevelOp(
		OperationDelete,
		"ring",
		PriorityRing,
		ring,
		NilRing,
		RingName,
		executors.RingDelete(),
		DescribeTopLevel(OperationDelete, "ring", ring.Name),
	)
}

// =============================================================================
// CrtStore Factory Functions
// =============================================================================

// NewCrtStoreCreate creates an operation to create a crt-store section.
func NewCrtStoreCreate(crtStore *models.CrtStore) Operation {
	return NewTopLevelOp(
		OperationCreate,
		"crt_store",
		PriorityCrtStore,
		crtStore,
		IdentityCrtStore,
		CrtStoreName,
		executors.CrtStoreCreate(),
		DescribeTopLevel(OperationCreate, "crt-store", crtStore.Name),
	)
}

// NewCrtStoreUpdate creates an operation to update a crt-store section.
func NewCrtStoreUpdate(crtStore *models.CrtStore) Operation {
	return NewTopLevelOp(
		OperationUpdate,
		"crt_store",
		PriorityCrtStore,
		crtStore,
		IdentityCrtStore,
		CrtStoreName,
		executors.CrtStoreUpdate(),
		DescribeTopLevel(OperationUpdate, "crt-store", crtStore.Name),
	)
}

// NewCrtStoreDelete creates an operation to delete a crt-store section.
func NewCrtStoreDelete(crtStore *models.CrtStore) Operation {
	return NewTopLevelOp(
		OperationDelete,
		"crt_store",
		PriorityCrtStore,
		crtStore,
		NilCrtStore,
		CrtStoreName,
		executors.CrtStoreDelete(),
		DescribeTopLevel(OperationDelete, "crt-store", crtStore.Name),
	)
}

// =============================================================================
// Userlist Factory Functions (Create/Delete only - no Update API)
// =============================================================================

// NewUserlistCreate creates an operation to create a userlist section.
func NewUserlistCreate(userlist *models.Userlist) Operation {
	return NewTopLevelOp(
		OperationCreate,
		"userlist",
		PriorityUserlist,
		userlist,
		IdentityUserlist,
		UserlistName,
		executors.UserlistCreate(),
		DescribeTopLevel(OperationCreate, "userlist", userlist.Name),
	)
}

// NewUserlistDelete creates an operation to delete a userlist section.
func NewUserlistDelete(userlist *models.Userlist) Operation {
	return NewTopLevelOp(
		OperationDelete,
		"userlist",
		PriorityUserlist,
		userlist,
		NilUserlist,
		UserlistName,
		executors.UserlistDelete(),
		DescribeTopLevel(OperationDelete, "userlist", userlist.Name),
	)
}

// =============================================================================
// FCGIApp Factory Functions
// =============================================================================

// NewFCGIAppCreate creates an operation to create a fcgi-app section.
func NewFCGIAppCreate(fcgiApp *models.FCGIApp) Operation {
	return NewTopLevelOp(
		OperationCreate,
		"fcgi_app",
		PriorityFCGIApp,
		fcgiApp,
		IdentityFCGIApp,
		FCGIAppName,
		executors.FCGIAppCreate(),
		DescribeTopLevel(OperationCreate, "fcgi-app", fcgiApp.Name),
	)
}

// NewFCGIAppUpdate creates an operation to update a fcgi-app section.
func NewFCGIAppUpdate(fcgiApp *models.FCGIApp) Operation {
	return NewTopLevelOp(
		OperationUpdate,
		"fcgi_app",
		PriorityFCGIApp,
		fcgiApp,
		IdentityFCGIApp,
		FCGIAppName,
		executors.FCGIAppUpdate(),
		DescribeTopLevel(OperationUpdate, "fcgi-app", fcgiApp.Name),
	)
}

// NewFCGIAppDelete creates an operation to delete a fcgi-app section.
func NewFCGIAppDelete(fcgiApp *models.FCGIApp) Operation {
	return NewTopLevelOp(
		OperationDelete,
		"fcgi_app",
		PriorityFCGIApp,
		fcgiApp,
		NilFCGIApp,
		FCGIAppName,
		executors.FCGIAppDelete(),
		DescribeTopLevel(OperationDelete, "fcgi-app", fcgiApp.Name),
	)
}

// =============================================================================
// TCP Request Rule Factory Functions (Index-based child)
// =============================================================================

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

// =============================================================================
// TCP Response Rule Factory Functions (Index-based child, Backend only)
// =============================================================================

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

// =============================================================================
// Stick Rule Factory Functions (Index-based child, Backend only)
// =============================================================================

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

// =============================================================================
// HTTP After Response Rule Factory Functions (Index-based child, Backend only)
// =============================================================================

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

// =============================================================================
// Server Switching Rule Factory Functions (Index-based child, Backend only)
// =============================================================================

// NewServerSwitchingRuleBackendCreate creates an operation to create a server switching rule in a backend.
func NewServerSwitchingRuleBackendCreate(backendName string, rule *models.ServerSwitchingRule, index int) Operation {
	return NewIndexChildOp(
		OperationCreate,
		"server_switching_rule",
		PriorityServerSwitchingRule,
		backendName,
		index,
		rule,
		IdentityServerSwitchingRule,
		executors.ServerSwitchingRuleBackendCreate(),
		func() string { return describeServerSwitchingRule(OperationCreate, rule, backendName, index) },
	)
}

// NewServerSwitchingRuleBackendUpdate creates an operation to update a server switching rule in a backend.
func NewServerSwitchingRuleBackendUpdate(backendName string, rule *models.ServerSwitchingRule, index int) Operation {
	return NewIndexChildOp(
		OperationUpdate,
		"server_switching_rule",
		PriorityServerSwitchingRule,
		backendName,
		index,
		rule,
		IdentityServerSwitchingRule,
		executors.ServerSwitchingRuleBackendUpdate(),
		func() string { return describeServerSwitchingRule(OperationUpdate, rule, backendName, index) },
	)
}

// NewServerSwitchingRuleBackendDelete creates an operation to delete a server switching rule from a backend.
func NewServerSwitchingRuleBackendDelete(backendName string, rule *models.ServerSwitchingRule, index int) Operation {
	return NewIndexChildOp(
		OperationDelete,
		"server_switching_rule",
		PriorityServerSwitchingRule,
		backendName,
		index,
		rule,
		NilServerSwitchingRule,
		executors.ServerSwitchingRuleBackendDelete(),
		func() string { return describeServerSwitchingRule(OperationDelete, rule, backendName, index) },
	)
}

// =============================================================================
// HTTP Check Factory Functions (Index-based child, Backend only)
// =============================================================================

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

// =============================================================================
// TCP Check Factory Functions (Index-based child, Backend only)
// =============================================================================

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

// =============================================================================
// Capture (DeclareCapture) Factory Functions (Index-based child, Frontend only)
// =============================================================================

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

// =============================================================================
// LogProfile Factory Functions (v3.1+ only)
// =============================================================================

// NewLogProfileCreate creates an operation to create a log-profile section.
// Log profiles are only available in HAProxy DataPlane API v3.1+.
func NewLogProfileCreate(logProfile *models.LogProfile) Operation {
	return NewTopLevelOp(
		OperationCreate,
		"log_profile",
		PriorityLogProfile,
		logProfile,
		IdentityLogProfile,
		LogProfileName,
		executors.LogProfileCreate(),
		DescribeTopLevel(OperationCreate, "log-profile", logProfile.Name),
	)
}

// NewLogProfileUpdate creates an operation to update a log-profile section.
// Log profiles are only available in HAProxy DataPlane API v3.1+.
func NewLogProfileUpdate(logProfile *models.LogProfile) Operation {
	return NewTopLevelOp(
		OperationUpdate,
		"log_profile",
		PriorityLogProfile,
		logProfile,
		IdentityLogProfile,
		LogProfileName,
		executors.LogProfileUpdate(),
		DescribeTopLevel(OperationUpdate, "log-profile", logProfile.Name),
	)
}

// NewLogProfileDelete creates an operation to delete a log-profile section.
// Log profiles are only available in HAProxy DataPlane API v3.1+.
func NewLogProfileDelete(logProfile *models.LogProfile) Operation {
	return NewTopLevelOp(
		OperationDelete,
		"log_profile",
		PriorityLogProfile,
		logProfile,
		NilLogProfile,
		LogProfileName,
		executors.LogProfileDelete(),
		DescribeTopLevel(OperationDelete, "log-profile", logProfile.Name),
	)
}

// =============================================================================
// Traces Factory Functions (v3.1+ only, Singleton)
// =============================================================================

// NewTracesUpdate creates an operation to update the traces section.
// The traces section is a singleton - it can be created or replaced.
// Traces configuration is only available in HAProxy DataPlane API v3.1+.
func NewTracesUpdate(traces *models.Traces) Operation {
	return NewSingletonOp(
		OperationUpdate,
		"traces",
		PriorityTraces,
		traces,
		IdentityTraces,
		executors.TracesUpdate(),
		func() string { return "Update traces section" },
	)
}

// =============================================================================
// AcmeProvider Factory Functions (v3.2+ only)
// =============================================================================

// NewAcmeProviderCreate creates an operation to create an acme section.
// ACME providers are only available in HAProxy DataPlane API v3.2+.
func NewAcmeProviderCreate(acmeProvider *models.AcmeProvider) Operation {
	return NewTopLevelOp(
		OperationCreate,
		"acme_provider",
		PriorityAcmeProvider,
		acmeProvider,
		IdentityAcmeProvider,
		AcmeProviderName,
		executors.AcmeProviderCreate(),
		DescribeTopLevel(OperationCreate, "acme-provider", acmeProvider.Name),
	)
}

// NewAcmeProviderUpdate creates an operation to update an acme section.
// ACME providers are only available in HAProxy DataPlane API v3.2+.
func NewAcmeProviderUpdate(acmeProvider *models.AcmeProvider) Operation {
	return NewTopLevelOp(
		OperationUpdate,
		"acme_provider",
		PriorityAcmeProvider,
		acmeProvider,
		IdentityAcmeProvider,
		AcmeProviderName,
		executors.AcmeProviderUpdate(),
		DescribeTopLevel(OperationUpdate, "acme-provider", acmeProvider.Name),
	)
}

// NewAcmeProviderDelete creates an operation to delete an acme section.
// ACME providers are only available in HAProxy DataPlane API v3.2+.
func NewAcmeProviderDelete(acmeProvider *models.AcmeProvider) Operation {
	return NewTopLevelOp(
		OperationDelete,
		"acme_provider",
		PriorityAcmeProvider,
		acmeProvider,
		NilAcmeProvider,
		AcmeProviderName,
		executors.AcmeProviderDelete(),
		DescribeTopLevel(OperationDelete, "acme-provider", acmeProvider.Name),
	)
}

// =============================================================================
// QUICInitialRule Factory Functions (v3.1+ only)
// =============================================================================

// NewQUICInitialRuleFrontendCreate creates an operation to create a QUIC initial rule in a frontend.
// QUIC initial rules are only available in HAProxy DataPlane API v3.1+.
func NewQUICInitialRuleFrontendCreate(frontendName string, rule *models.QUICInitialRule, index int) Operation {
	return NewIndexChildOp(
		OperationCreate,
		"quic_initial_rule",
		PriorityQUICInitialRule,
		frontendName,
		index,
		rule,
		IdentityQUICInitialRule,
		executors.QUICInitialRuleFrontendCreate(),
		DescribeIndexChild(OperationCreate, "quic-initial-rule", index, "frontend", frontendName),
	)
}

// NewQUICInitialRuleFrontendUpdate creates an operation to update a QUIC initial rule in a frontend.
// QUIC initial rules are only available in HAProxy DataPlane API v3.1+.
func NewQUICInitialRuleFrontendUpdate(frontendName string, rule *models.QUICInitialRule, index int) Operation {
	return NewIndexChildOp(
		OperationUpdate,
		"quic_initial_rule",
		PriorityQUICInitialRule,
		frontendName,
		index,
		rule,
		IdentityQUICInitialRule,
		executors.QUICInitialRuleFrontendUpdate(),
		DescribeIndexChild(OperationUpdate, "quic-initial-rule", index, "frontend", frontendName),
	)
}

// NewQUICInitialRuleFrontendDelete creates an operation to delete a QUIC initial rule from a frontend.
// QUIC initial rules are only available in HAProxy DataPlane API v3.1+.
func NewQUICInitialRuleFrontendDelete(frontendName string, rule *models.QUICInitialRule, index int) Operation {
	return NewIndexChildOp(
		OperationDelete,
		"quic_initial_rule",
		PriorityQUICInitialRule,
		frontendName,
		index,
		rule,
		NilQUICInitialRule,
		executors.QUICInitialRuleFrontendDelete(),
		DescribeIndexChild(OperationDelete, "quic-initial-rule", index, "frontend", frontendName),
	)
}

// NewQUICInitialRuleDefaultsCreate creates an operation to create a QUIC initial rule in defaults.
// QUIC initial rules are only available in HAProxy DataPlane API v3.1+.
func NewQUICInitialRuleDefaultsCreate(defaultsName string, rule *models.QUICInitialRule, index int) Operation {
	return NewIndexChildOp(
		OperationCreate,
		"quic_initial_rule",
		PriorityQUICInitialRule,
		defaultsName,
		index,
		rule,
		IdentityQUICInitialRule,
		executors.QUICInitialRuleDefaultsCreate(),
		DescribeIndexChild(OperationCreate, "quic-initial-rule", index, "defaults", defaultsName),
	)
}

// NewQUICInitialRuleDefaultsUpdate creates an operation to update a QUIC initial rule in defaults.
// QUIC initial rules are only available in HAProxy DataPlane API v3.1+.
func NewQUICInitialRuleDefaultsUpdate(defaultsName string, rule *models.QUICInitialRule, index int) Operation {
	return NewIndexChildOp(
		OperationUpdate,
		"quic_initial_rule",
		PriorityQUICInitialRule,
		defaultsName,
		index,
		rule,
		IdentityQUICInitialRule,
		executors.QUICInitialRuleDefaultsUpdate(),
		DescribeIndexChild(OperationUpdate, "quic-initial-rule", index, "defaults", defaultsName),
	)
}

// NewQUICInitialRuleDefaultsDelete creates an operation to delete a QUIC initial rule from defaults.
// QUIC initial rules are only available in HAProxy DataPlane API v3.1+.
func NewQUICInitialRuleDefaultsDelete(defaultsName string, rule *models.QUICInitialRule, index int) Operation {
	return NewIndexChildOp(
		OperationDelete,
		"quic_initial_rule",
		PriorityQUICInitialRule,
		defaultsName,
		index,
		rule,
		NilQUICInitialRule,
		executors.QUICInitialRuleDefaultsDelete(),
		DescribeIndexChild(OperationDelete, "quic-initial-rule", index, "defaults", defaultsName),
	)
}
