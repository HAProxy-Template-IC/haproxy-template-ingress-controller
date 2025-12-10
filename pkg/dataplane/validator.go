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
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/haproxytech/client-native/v6/models"

	"haproxy-template-ic/pkg/dataplane/client"
	"haproxy-template-ic/pkg/dataplane/parser"
	v30 "haproxy-template-ic/pkg/generated/dataplaneapi/v30"
	v31 "haproxy-template-ic/pkg/generated/dataplaneapi/v31"
	v32 "haproxy-template-ic/pkg/generated/dataplaneapi/v32"
)

// haproxyCheckMutex serializes HAProxy validation to work around issues with
// concurrent haproxy -c execution. Without this, concurrent validations can
// interfere with each other even though they use isolated temp directories.
var haproxyCheckMutex sync.Mutex

// Cached OpenAPI specs - parsed once and reused for performance.
// GetSwagger() parses the embedded spec on every call (~500ms), so caching is critical.
var (
	cachedSpecV30 *openapi3.T
	cachedSpecV31 *openapi3.T
	cachedSpecV32 *openapi3.T
	specOnceV30   sync.Once
	specOnceV31   sync.Once
	specOnceV32   sync.Once
	specErrV30    error
	specErrV31    error
	specErrV32    error
)

// =============================================================================
// Version-aware Model Conversion (using centralized client converters)
// =============================================================================

// versionMinor extracts the minor version from a Version pointer.
// Returns 0 (v3.0) if version is nil, which is the safest default.
func versionMinor(version *Version) int {
	if version == nil {
		return 0
	}
	return version.Minor
}

// validateModel validates a client-native model against the OpenAPI schema.
// Uses generic type parameters for type-safe version conversion.
// This uses the centralized conversion functions from pkg/dataplane/client.
func validateModel[TV32, TV31, TV30 any](spec *openapi3.T, version *Version, schemaName string, model interface{}) error {
	// Use centralized marshal + metadata transformation
	jsonData, err := client.MarshalForVersion(model)
	if err != nil {
		return err
	}

	// Use centralized generic conversion
	apiModel, err := client.ConvertToVersioned[TV32, TV31, TV30](jsonData, versionMinor(version))
	if err != nil {
		return fmt.Errorf("failed to convert to API model: %w", err)
	}

	return validateAgainstSchema(spec, schemaName, apiModel)
}

// ValidationPaths holds the filesystem paths for HAProxy validation.
// These paths must match the HAProxy Dataplane API server's resource configuration.
type ValidationPaths struct {
	MapsDir           string
	SSLCertsDir       string
	CRTListDir        string // Directory for CRT-list files (may differ from SSLCertsDir on HAProxy < 3.2)
	GeneralStorageDir string
	ConfigFile        string
}

// ValidateConfiguration performs three-phase HAProxy configuration validation.
//
// Phase 1: Syntax validation using client-native parser
// Phase 1.5: API schema validation using OpenAPI spec (patterns, formats, required fields)
// Phase 2: Semantic validation using haproxy binary (-c flag)
//
// The validation writes files to the directories specified in paths. Callers must ensure
// that paths are isolated (e.g., per-worker temp directories) to allow parallel execution.
//
// Parameters:
//   - mainConfig: The rendered HAProxy configuration (haproxy.cfg content)
//   - auxFiles: All auxiliary files (maps, certificates, general files)
//   - paths: Filesystem paths for validation (must be isolated for parallel execution)
//   - version: HAProxy/DataPlane API version for schema selection (nil uses default v3.0)
//   - skipDNSValidation: If true, adds -dr flag to skip DNS resolution failures. Use true for
//     runtime validation (permissive, prevents blocking when DNS fails) and false for webhook
//     validation (strict, catches DNS issues before resource admission).
//
// Returns:
//   - nil if validation succeeds
//   - ValidationError with phase information if validation fails
func ValidateConfiguration(mainConfig string, auxFiles *AuxiliaryFiles, paths *ValidationPaths, version *Version, skipDNSValidation bool) error {
	// Timing variables for phase breakdown
	var syntaxMs, schemaMs, semanticMs int64
	startTime := time.Now()

	// Phase 1: Syntax validation with client-native parser
	// This also returns the parsed configuration for Phase 1.5
	syntaxStart := time.Now()
	parsedConfig, err := validateSyntax(mainConfig)
	syntaxMs = time.Since(syntaxStart).Milliseconds()
	if err != nil {
		return &ValidationError{
			Phase:   "syntax",
			Message: "configuration has syntax errors",
			Err:     err,
		}
	}

	// Phase 1.5: API schema validation with OpenAPI spec
	schemaStart := time.Now()
	if err := validateAPISchema(parsedConfig, version); err != nil {
		return &ValidationError{
			Phase:   "schema",
			Message: "configuration violates API schema constraints",
			Err:     err,
		}
	}
	schemaMs = time.Since(schemaStart).Milliseconds()

	// Phase 2: Semantic validation with haproxy binary
	semanticStart := time.Now()
	if err := validateSemantics(mainConfig, auxFiles, paths, skipDNSValidation); err != nil {
		return &ValidationError{
			Phase:   "semantic",
			Message: "configuration has semantic errors",
			Err:     err,
		}
	}
	semanticMs = time.Since(semanticStart).Milliseconds()

	// Log timing breakdown for visibility when debugging
	slog.Debug("validation phase timing breakdown",
		"total_ms", time.Since(startTime).Milliseconds(),
		"syntax_ms", syntaxMs,
		"schema_ms", schemaMs,
		"semantic_ms", semanticMs,
	)

	return nil
}

// validateSyntax performs syntax validation using client-native parser.
// Returns the parsed configuration for use in Phase 1.5 (API schema validation).
func validateSyntax(config string) (*parser.StructuredConfig, error) {
	// Create parser
	p, err := parser.New()
	if err != nil {
		return nil, fmt.Errorf("failed to create parser: %w", err)
	}

	// Parse configuration - this validates syntax
	parsed, err := p.ParseFromString(config)
	if err != nil {
		return nil, fmt.Errorf("syntax error: %w", err)
	}

	return parsed, nil
}

// getSwaggerForVersion returns the cached OpenAPI spec for the given HAProxy/DataPlane API version.
// If version is nil, defaults to v3.0 (safest, most compatible).
// Specs are parsed once and cached for subsequent calls (~500ms → ~0ms after first call).
func getSwaggerForVersion(version *Version) (*openapi3.T, error) {
	if version == nil {
		// Default to v3.0 - safest default when version is unknown
		return getCachedSwaggerV30()
	}

	// Select OpenAPI spec based on major.minor version
	if version.Major == 3 {
		switch {
		case version.Minor >= 2:
			return getCachedSwaggerV32()
		case version.Minor >= 1:
			return getCachedSwaggerV31()
		default:
			return getCachedSwaggerV30()
		}
	}

	// For versions > 3.x, use the latest available spec
	if version.Major > 3 {
		return getCachedSwaggerV32()
	}

	// For versions < 3.0, use v3.0 as fallback
	return getCachedSwaggerV30()
}

// getCachedSwaggerV30 returns the cached v3.0 OpenAPI spec, parsing it on first call.
func getCachedSwaggerV30() (*openapi3.T, error) {
	specOnceV30.Do(func() {
		cachedSpecV30, specErrV30 = v30.GetSwagger()
	})
	return cachedSpecV30, specErrV30
}

// getCachedSwaggerV31 returns the cached v3.1 OpenAPI spec, parsing it on first call.
func getCachedSwaggerV31() (*openapi3.T, error) {
	specOnceV31.Do(func() {
		cachedSpecV31, specErrV31 = v31.GetSwagger()
	})
	return cachedSpecV31, specErrV31
}

// getCachedSwaggerV32 returns the cached v3.2 OpenAPI spec, parsing it on first call.
func getCachedSwaggerV32() (*openapi3.T, error) {
	specOnceV32.Do(func() {
		cachedSpecV32, specErrV32 = v32.GetSwagger()
	})
	return cachedSpecV32, specErrV32
}

// validateAPISchema performs API schema validation using OpenAPI spec.
// This validates parsed configuration models against the Dataplane API's OpenAPI
// schema constraints (patterns, formats, required fields).
func validateAPISchema(parsed *parser.StructuredConfig, version *Version) error {
	// Load OpenAPI specification for the detected version
	spec, err := getSwaggerForVersion(version)
	if err != nil {
		return fmt.Errorf("failed to load OpenAPI spec: %w", err)
	}

	// Validate all sections that have schema constraints
	var validationErrors []string

	// Validate backend sections
	validationErrors = append(validationErrors, validateBackendSections(spec, version, parsed.Backends)...)

	// Validate frontend sections
	validationErrors = append(validationErrors, validateFrontendSections(spec, version, parsed.Frontends)...)

	// If there were any validation errors, return them
	if len(validationErrors) > 0 {
		return fmt.Errorf("API schema validation failed:\n  - %s",
			strings.Join(validationErrors, "\n  - "))
	}

	return nil
}

// validateBackendSections validates all configuration elements within backends.
func validateBackendSections(spec *openapi3.T, version *Version, backends []*models.Backend) []string {
	var errors []string
	for i := range backends {
		backend := backends[i]
		errors = append(errors, validateBackendServers(spec, version, backend)...)
		errors = append(errors, validateBackendRules(spec, version, backend)...)
		errors = append(errors, validateBackendChecks(spec, version, backend)...)
	}
	return errors
}

// validateFrontendSections validates all configuration elements within frontends.
func validateFrontendSections(spec *openapi3.T, version *Version, frontends []*models.Frontend) []string {
	var errors []string
	for i := range frontends {
		frontend := frontends[i]
		errors = append(errors, validateFrontendBinds(spec, version, frontend)...)
		errors = append(errors, validateFrontendRules(spec, version, frontend)...)
		errors = append(errors, validateFrontendElements(spec, version, frontend)...)
	}
	return errors
}

// validateRuleSlice validates a slice of rules using generic type parameters.
// TV32, TV31, TV30 are the version-specific API types for the rule.
func validateRuleSlice[T any, TV32, TV31, TV30 any](spec *openapi3.T, version *Version, schemaName, parentName, ruleType string, rules []T) []string {
	var errors []string
	for idx, rule := range rules {
		if err := validateModel[TV32, TV31, TV30](spec, version, schemaName, rule); err != nil {
			errors = append(errors, fmt.Sprintf("%s, %s %d: %v", parentName, ruleType, idx, err))
		}
	}
	return errors
}

// validateBackendServers validates servers and server templates in a backend.
func validateBackendServers(spec *openapi3.T, version *Version, backend *models.Backend) []string {
	var errors []string
	for serverName := range backend.Servers {
		server := backend.Servers[serverName]
		if err := validateModel[v32.Server, v31.Server, v30.Server](spec, version, "server", &server); err != nil {
			errors = append(errors, fmt.Sprintf("backend %s, server %s: %v", backend.Name, serverName, err))
		}
	}
	for templateName := range backend.ServerTemplates {
		template := backend.ServerTemplates[templateName]
		if err := validateModel[v32.ServerTemplate, v31.ServerTemplate, v30.ServerTemplate](spec, version, "server_template", &template); err != nil {
			errors = append(errors, fmt.Sprintf("backend %s, server template %s: %v", backend.Name, templateName, err))
		}
	}
	return errors
}

// validateBackendRules validates various rule types in a backend.
func validateBackendRules(spec *openapi3.T, version *Version, backend *models.Backend) []string {
	var errors []string
	name := "backend " + backend.Name

	errors = append(errors, validateRuleSlice[*models.HTTPRequestRule, v32.HttpRequestRule, v31.HttpRequestRule, v30.HttpRequestRule](spec, version, "http_request_rule", name, "http-request rule", backend.HTTPRequestRuleList)...)
	errors = append(errors, validateRuleSlice[*models.HTTPResponseRule, v32.HttpResponseRule, v31.HttpResponseRule, v30.HttpResponseRule](spec, version, "http_response_rule", name, "http-response rule", backend.HTTPResponseRuleList)...)
	errors = append(errors, validateRuleSlice[*models.TCPRequestRule, v32.TcpRequestRule, v31.TcpRequestRule, v30.TcpRequestRule](spec, version, "tcp_request_rule", name, "tcp-request rule", backend.TCPRequestRuleList)...)
	errors = append(errors, validateRuleSlice[*models.TCPResponseRule, v32.TcpResponseRule, v31.TcpResponseRule, v30.TcpResponseRule](spec, version, "tcp_response_rule", name, "tcp-response rule", backend.TCPResponseRuleList)...)
	errors = append(errors, validateRuleSlice[*models.HTTPAfterResponseRule, v32.HttpAfterResponseRule, v31.HttpAfterResponseRule, v30.HttpAfterResponseRule](spec, version, "http_after_response_rule", name, "http-after-response rule", backend.HTTPAfterResponseRuleList)...)
	errors = append(errors, validateRuleSlice[*models.HTTPErrorRule, v32.HttpErrorRule, v31.HttpErrorRule, v30.HttpErrorRule](spec, version, "http_error_rule", name, "http-error rule", backend.HTTPErrorRuleList)...)
	errors = append(errors, validateRuleSlice[*models.ServerSwitchingRule, v32.ServerSwitchingRule, v31.ServerSwitchingRule, v30.ServerSwitchingRule](spec, version, "server_switching_rule", name, "server switching rule", backend.ServerSwitchingRuleList)...)
	errors = append(errors, validateRuleSlice[*models.StickRule, v32.StickRule, v31.StickRule, v30.StickRule](spec, version, "stick_rule", name, "stick rule", backend.StickRuleList)...)

	errors = append(errors, validateACLList(spec, version, name, backend.ACLList)...)
	errors = append(errors, validateFilterList(spec, version, name, backend.FilterList)...)
	errors = append(errors, validateLogTargetList(spec, version, name, backend.LogTargetList)...)
	return errors
}

// validateBackendChecks validates health checks in a backend.
func validateBackendChecks(spec *openapi3.T, version *Version, backend *models.Backend) []string {
	var errors []string
	name := "backend " + backend.Name

	errors = append(errors, validateRuleSlice[*models.HTTPCheck, v32.HttpCheck, v31.HttpCheck, v30.HttpCheck](spec, version, "http_check", name, "http-check", backend.HTTPCheckList)...)
	errors = append(errors, validateRuleSlice[*models.TCPCheck, v32.TcpCheck, v31.TcpCheck, v30.TcpCheck](spec, version, "tcp_check", name, "tcp-check", backend.TCPCheckRuleList)...)
	return errors
}

// validateFrontendBinds validates bind configurations in a frontend.
func validateFrontendBinds(spec *openapi3.T, version *Version, frontend *models.Frontend) []string {
	var errors []string
	for bindName := range frontend.Binds {
		bind := frontend.Binds[bindName]
		if err := validateModel[v32.Bind, v31.Bind, v30.Bind](spec, version, "bind", &bind); err != nil {
			errors = append(errors, fmt.Sprintf("frontend %s, bind %s: %v", frontend.Name, bindName, err))
		}
	}
	return errors
}

// validateFrontendRules validates various rule types in a frontend.
func validateFrontendRules(spec *openapi3.T, version *Version, frontend *models.Frontend) []string {
	var errors []string
	name := "frontend " + frontend.Name

	errors = append(errors, validateRuleSlice[*models.HTTPRequestRule, v32.HttpRequestRule, v31.HttpRequestRule, v30.HttpRequestRule](spec, version, "http_request_rule", name, "http-request rule", frontend.HTTPRequestRuleList)...)
	errors = append(errors, validateRuleSlice[*models.HTTPResponseRule, v32.HttpResponseRule, v31.HttpResponseRule, v30.HttpResponseRule](spec, version, "http_response_rule", name, "http-response rule", frontend.HTTPResponseRuleList)...)
	errors = append(errors, validateRuleSlice[*models.TCPRequestRule, v32.TcpRequestRule, v31.TcpRequestRule, v30.TcpRequestRule](spec, version, "tcp_request_rule", name, "tcp-request rule", frontend.TCPRequestRuleList)...)
	errors = append(errors, validateRuleSlice[*models.HTTPAfterResponseRule, v32.HttpAfterResponseRule, v31.HttpAfterResponseRule, v30.HttpAfterResponseRule](spec, version, "http_after_response_rule", name, "http-after-response rule", frontend.HTTPAfterResponseRuleList)...)
	errors = append(errors, validateRuleSlice[*models.HTTPErrorRule, v32.HttpErrorRule, v31.HttpErrorRule, v30.HttpErrorRule](spec, version, "http_error_rule", name, "http-error rule", frontend.HTTPErrorRuleList)...)
	errors = append(errors, validateRuleSlice[*models.BackendSwitchingRule, v32.BackendSwitchingRule, v31.BackendSwitchingRule, v30.BackendSwitchingRule](spec, version, "backend_switching_rule", name, "backend switching rule", frontend.BackendSwitchingRuleList)...)

	errors = append(errors, validateACLList(spec, version, name, frontend.ACLList)...)
	return errors
}

// validateFrontendElements validates other frontend elements (filters, log targets, captures).
func validateFrontendElements(spec *openapi3.T, version *Version, frontend *models.Frontend) []string {
	var errors []string
	name := "frontend " + frontend.Name

	errors = append(errors, validateFilterList(spec, version, name, frontend.FilterList)...)
	errors = append(errors, validateLogTargetList(spec, version, name, frontend.LogTargetList)...)
	errors = append(errors, validateRuleSlice[*models.Capture, v32.Capture, v31.Capture, v30.Capture](spec, version, "capture", name, "capture", frontend.CaptureList)...)
	return errors
}

// validateACLList validates ACL configurations.
func validateACLList(spec *openapi3.T, version *Version, parentName string, aclList []*models.ACL) []string {
	var errors []string
	for idx, acl := range aclList {
		if err := validateModel[v32.Acl, v31.Acl, v30.Acl](spec, version, "acl", acl); err != nil {
			errors = append(errors, fmt.Sprintf("%s, ACL %d: %v", parentName, idx, err))
		}
	}
	return errors
}

// validateFilterList validates filter configurations.
func validateFilterList(spec *openapi3.T, version *Version, parentName string, filterList []*models.Filter) []string {
	var errors []string
	for idx, filter := range filterList {
		if err := validateModel[v32.Filter, v31.Filter, v30.Filter](spec, version, "filter", filter); err != nil {
			errors = append(errors, fmt.Sprintf("%s, filter %d: %v", parentName, idx, err))
		}
	}
	return errors
}

// validateLogTargetList validates log target configurations.
func validateLogTargetList(spec *openapi3.T, version *Version, parentName string, logTargetList []*models.LogTarget) []string {
	var errors []string
	for idx, logTarget := range logTargetList {
		if err := validateModel[v32.LogTarget, v31.LogTarget, v30.LogTarget](spec, version, "log_target", logTarget); err != nil {
			errors = append(errors, fmt.Sprintf("%s, log target %d: %v", parentName, idx, err))
		}
	}
	return errors
}

// cleanJSON removes null and empty values from JSON to match API behavior.
// When Go marshals structs, it includes null fields for nil pointers.
// The Dataplane API omits these fields, so we remove them before validation
// to match the actual API request structure.
func cleanJSON(data []byte) ([]byte, error) {
	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, err
	}

	cleaned := removeNullValues(obj)
	return json.Marshal(cleaned)
}

// removeNullValues recursively removes null values from maps.
// This ensures validation matches the actual API behavior where null fields are omitted.
func removeNullValues(obj map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range obj {
		if v == nil {
			continue // Skip null values
		}

		// Recursively clean nested maps
		if nested, ok := v.(map[string]interface{}); ok {
			cleaned := removeNullValues(nested)
			if len(cleaned) > 0 {
				result[k] = cleaned
			}
			continue
		}

		// Keep non-null values
		result[k] = v
	}
	return result
}

// resolveRef resolves a $ref reference path to its schema.
// Handles references like "#/components/schemas/server_params".
func resolveRef(spec *openapi3.T, ref string) (*openapi3.Schema, error) {
	// Only handle component schema references for now
	const componentsPrefix = "#/components/schemas/"
	if !strings.HasPrefix(ref, componentsPrefix) {
		return nil, fmt.Errorf("unsupported $ref format: %s", ref)
	}

	schemaName := strings.TrimPrefix(ref, componentsPrefix)
	schemaRef, ok := spec.Components.Schemas[schemaName]
	if !ok {
		return nil, fmt.Errorf("schema %s not found", schemaName)
	}

	return schemaRef.Value, nil
}

// resolveAllOf recursively resolves allOf composition by merging all referenced schemas.
// Returns a flattened schema with all properties and required fields combined.
func resolveAllOf(spec *openapi3.T, schema *openapi3.Schema) (*openapi3.Schema, error) {
	if len(schema.AllOf) == 0 {
		return schema, nil
	}

	merged := createMergedSchema(schema)

	for _, ref := range schema.AllOf {
		subSchema, err := resolveSchemaRef(spec, ref)
		if err != nil {
			return nil, err
		}

		mergeSchemaProperties(merged, subSchema)
	}

	merged.Required = deduplicateRequired(merged.Required)
	return merged, nil
}

// createMergedSchema initializes a merged schema with base properties.
func createMergedSchema(schema *openapi3.Schema) *openapi3.Schema {
	objectType := openapi3.Types{"object"}
	merged := &openapi3.Schema{
		Type:       &objectType,
		Properties: make(openapi3.Schemas),
		Required:   append([]string{}, schema.Required...),
	}

	for propName, propSchema := range schema.Properties {
		merged.Properties[propName] = propSchema
	}

	return merged
}

// resolveSchemaRef resolves a schema reference (either $ref or inline).
func resolveSchemaRef(spec *openapi3.T, ref *openapi3.SchemaRef) (*openapi3.Schema, error) {
	var subSchema *openapi3.Schema

	if ref.Ref != "" {
		resolved, err := resolveRef(spec, ref.Ref)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve $ref %s: %w", ref.Ref, err)
		}
		subSchema = resolved
	} else {
		subSchema = ref.Value
	}

	// Recursively resolve if schema has allOf
	if len(subSchema.AllOf) > 0 {
		return resolveAllOf(spec, subSchema)
	}

	return subSchema, nil
}

// mergeSchemaProperties merges properties and required fields from source to target.
func mergeSchemaProperties(target, source *openapi3.Schema) {
	for propName, propSchema := range source.Properties {
		target.Properties[propName] = propSchema
	}
	target.Required = append(target.Required, source.Required...)
}

// deduplicateRequired removes duplicate entries from required fields list.
func deduplicateRequired(required []string) []string {
	seen := make(map[string]bool, len(required))
	unique := make([]string, 0, len(required))

	for _, field := range required {
		if !seen[field] {
			seen[field] = true
			unique = append(unique, field)
		}
	}

	return unique
}

// validateAgainstSchema validates a model against an OpenAPI schema.
func validateAgainstSchema(spec *openapi3.T, schemaName string, model interface{}) error {
	// Get the schema from the spec
	schemaRef, ok := spec.Components.Schemas[schemaName]
	if !ok {
		return fmt.Errorf("schema %s not found in OpenAPI spec", schemaName)
	}

	// Resolve allOf composition if present
	// kin-openapi's VisitJSON doesn't properly merge allOf schemas before
	// checking additionalProperties: false, so we manually resolve them.
	schema := schemaRef.Value
	if len(schema.AllOf) > 0 {
		var err error
		schema, err = resolveAllOf(spec, schema)
		if err != nil {
			return fmt.Errorf("failed to resolve allOf: %w", err)
		}
	}

	// Convert model to JSON
	data, err := json.Marshal(model)
	if err != nil {
		return fmt.Errorf("failed to marshal model: %w", err)
	}

	// Clean JSON to remove null values (matches API behavior)
	cleanedData, err := cleanJSON(data)
	if err != nil {
		return fmt.Errorf("failed to clean JSON: %w", err)
	}

	// Parse cleaned JSON for validation
	var value interface{}
	if err := json.Unmarshal(cleanedData, &value); err != nil {
		return fmt.Errorf("failed to unmarshal for validation: %w", err)
	}

	// Validate against resolved schema
	if err := schema.VisitJSON(value); err != nil {
		return fmt.Errorf("schema validation failed: %w", err)
	}

	return nil
}

// validateSemantics performs semantic validation using haproxy binary.
// This writes files to actual /etc/haproxy/ directories and runs haproxy -c.
// If skipDNSValidation is true, the -dr flag is passed to HAProxy to skip DNS resolution
// failures (servers with unresolvable hostnames start in DOWN state instead of failing).
func validateSemantics(mainConfig string, auxFiles *AuxiliaryFiles, paths *ValidationPaths, skipDNSValidation bool) error {
	// Timing for file I/O setup vs haproxy check
	var clearMs, writeAuxMs, writeConfigMs, haproxyCheckMs int64

	// Clear validation directories to remove any pre-existing files
	clearStart := time.Now()
	if err := clearValidationDirectories(paths); err != nil {
		return fmt.Errorf("failed to clear validation directories: %w", err)
	}
	clearMs = time.Since(clearStart).Milliseconds()

	// Write auxiliary files to their respective directories
	writeAuxStart := time.Now()
	if err := writeAuxiliaryFiles(auxFiles, paths); err != nil {
		return fmt.Errorf("failed to write auxiliary files: %w", err)
	}
	writeAuxMs = time.Since(writeAuxStart).Milliseconds()

	// Write main configuration to ConfigFile path
	writeConfigStart := time.Now()
	if err := os.WriteFile(paths.ConfigFile, []byte(mainConfig), 0o600); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}
	writeConfigMs = time.Since(writeConfigStart).Milliseconds()

	// Run haproxy -c -f <ConfigFile>
	haproxyCheckStart := time.Now()
	if err := runHAProxyCheck(paths.ConfigFile, mainConfig, skipDNSValidation); err != nil {
		return err
	}
	haproxyCheckMs = time.Since(haproxyCheckStart).Milliseconds()

	// Log semantic validation timing breakdown
	slog.Debug("semantic validation timing breakdown",
		"clear_dirs_ms", clearMs,
		"write_aux_ms", writeAuxMs,
		"write_config_ms", writeConfigMs,
		"haproxy_check_ms", haproxyCheckMs,
	)

	return nil
}

// clearValidationDirectories removes all files from validation directories.
// This ensures no pre-existing files interfere with validation.
// It clears both the traditional validation directories (for absolute/simple paths)
// and subdirectories in the config directory (for relative paths with subdirectories).
func clearValidationDirectories(paths *ValidationPaths) error {
	configDir := filepath.Dir(paths.ConfigFile)

	// Clear traditional validation directories (for absolute paths and simple filenames)
	dirs := []string{
		paths.MapsDir,
		paths.SSLCertsDir,
		paths.GeneralStorageDir,
	}

	for _, dir := range dirs {
		// Create directory if it doesn't exist
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}

		// Remove all files in directory
		entries, err := os.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("failed to read directory %s: %w", dir, err)
		}

		for _, entry := range entries {
			path := filepath.Join(dir, entry.Name())
			if err := os.RemoveAll(path); err != nil {
				return fmt.Errorf("failed to remove %s: %w", path, err)
			}
		}
	}

	// Create config directory if it doesn't exist
	// (No need to clear it - we already cleared the specific validation directories above)
	if err := os.MkdirAll(configDir, 0o750); err != nil {
		return fmt.Errorf("failed to create config directory %s: %w", configDir, err)
	}

	// Remove old config file if it exists
	if err := os.Remove(paths.ConfigFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove old config file: %w", err)
	}

	return nil
}

// resolveAuxiliaryFilePath determines the full path for an auxiliary file.
// It handles three cases:
// - Absolute paths: Extract filename and use fallback directory (for validation with temp directories).
// - Relative paths with subdirectories (e.g., "maps/hosts.map"): resolved relative to config directory.
// - Simple filenames: written to the specified fallback directory.
func resolveAuxiliaryFilePath(filePath, configDir, fallbackDir string) string {
	if filepath.IsAbs(filePath) {
		// Absolute path - extract filename and use fallback directory
		// This allows validation to work with temp directories instead of production paths
		// Example: /etc/haproxy/ssl/cert.pem → <tmpdir>/ssl/cert.pem
		return filepath.Join(fallbackDir, filepath.Base(filePath))
	}

	if strings.Contains(filePath, string(filepath.Separator)) {
		// Relative path with subdirectory - resolve relative to config directory
		return filepath.Join(configDir, filePath)
	}

	// Just a filename - write to fallback directory
	return filepath.Join(fallbackDir, filePath)
}

// writeFileWithDir writes a file to disk, creating parent directories if needed.
func writeFileWithDir(path, content, fileType string) error {
	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("failed to create directory for %s: %w", fileType, err)
	}

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return fmt.Errorf("failed to write %s: %w", fileType, err)
	}

	return nil
}

// writeAuxiliaryFiles writes all auxiliary files to their respective directories.
func writeAuxiliaryFiles(auxFiles *AuxiliaryFiles, paths *ValidationPaths) error {
	if auxFiles == nil {
		return nil // No auxiliary files to write
	}

	configDir := filepath.Dir(paths.ConfigFile)

	// Write map files
	for _, mapFile := range auxFiles.MapFiles {
		mapPath := resolveAuxiliaryFilePath(mapFile.Path, configDir, paths.MapsDir)
		if err := writeFileWithDir(mapPath, mapFile.Content, "map file "+mapFile.Path); err != nil {
			return err
		}
	}

	// Write general files
	for _, file := range auxFiles.GeneralFiles {
		filePath := resolveAuxiliaryFilePath(file.Filename, configDir, paths.GeneralStorageDir)
		if err := writeFileWithDir(filePath, file.Content, "general file "+file.Filename); err != nil {
			return err
		}
	}

	// Write SSL certificates
	for _, cert := range auxFiles.SSLCertificates {
		certPath := resolveAuxiliaryFilePath(cert.Path, configDir, paths.SSLCertsDir)
		if err := writeFileWithDir(certPath, cert.Content, "SSL certificate "+cert.Path); err != nil {
			return err
		}
	}

	// Write CRT-list files
	// Use CRTListDir which may differ from SSLCertsDir on HAProxy < 3.2
	for _, crtList := range auxFiles.CRTListFiles {
		crtListPath := resolveAuxiliaryFilePath(crtList.Path, configDir, paths.CRTListDir)
		if err := writeFileWithDir(crtListPath, crtList.Content, "CRT-list file "+crtList.Path); err != nil {
			return err
		}
	}

	return nil
}

// runHAProxyCheck runs haproxy binary with -c flag to validate configuration.
// The configuration can reference auxiliary files using relative paths
// (e.g., maps/host.map) which will be resolved relative to the config file directory.
//
// If skipDNSValidation is true, the -dr flag is passed to HAProxy. This causes HAProxy
// to append "none" to all server resolution methods, allowing startup/validation to
// proceed even when DNS resolution fails. Servers with unresolvable hostnames will
// start in RMAINT (DOWN) state instead of causing validation failure.
func runHAProxyCheck(configPath, configContent string, skipDNSValidation bool) error {
	// Serialize HAProxy execution to work around concurrent execution issues
	haproxyCheckMutex.Lock()
	defer haproxyCheckMutex.Unlock()

	// Find haproxy binary
	haproxyBin, err := exec.LookPath("haproxy")
	if err != nil {
		return fmt.Errorf("haproxy binary not found: %w", err)
	}

	// Get absolute path for config file
	absConfigPath, err := filepath.Abs(configPath)
	if err != nil {
		return fmt.Errorf("failed to get absolute config path: %w", err)
	}

	// Build haproxy command arguments
	// -c: check configuration and exit
	// -f: path to configuration file
	// -dr: (optional) skip DNS resolution failures - servers start in DOWN state instead of failing
	var args []string
	if skipDNSValidation {
		args = []string{"-dr", "-c", "-f", filepath.Base(absConfigPath)}
	} else {
		args = []string{"-c", "-f", filepath.Base(absConfigPath)}
	}

	// Run haproxy with the constructed arguments
	// Set working directory to config file directory so relative paths work
	cmd := exec.Command(haproxyBin, args...)
	cmd.Dir = filepath.Dir(absConfigPath)

	// Capture both stdout and stderr
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Parse and format HAProxy error output with config file context
		errorMsg := parseHAProxyError(string(output), configContent)
		return fmt.Errorf("haproxy validation failed: %s", errorMsg)
	}

	return nil
}

// parseHAProxyError parses HAProxy's error output to extract meaningful error messages with context.
// HAProxy outputs errors with [ALERT] prefix and line numbers. This function:
// 1. Captures 3 lines before/after each [ALERT] from HAProxy's output
// 2. Parses line numbers from [ALERT] messages (e.g., [haproxy.cfg:90])
// 3. Extracts and shows the corresponding lines from the config file.
func parseHAProxyError(output, configContent string) string {
	lines := strings.Split(output, "\n")

	// Find all meaningful [ALERT] line indices (skip summary alerts)
	alertIndices := findAlertIndices(lines)
	if len(alertIndices) == 0 {
		return strings.TrimSpace(output)
	}

	// Split config content into lines for context extraction
	configLines := strings.Split(configContent, "\n")

	// Extract context for each alert
	errorBlocks := extractErrorBlocks(lines, alertIndices, configLines, configContent)
	if len(errorBlocks) == 0 {
		return strings.TrimSpace(output)
	}

	// Join multiple error blocks with blank line separator
	return strings.Join(errorBlocks, "\n\n")
}

// findAlertIndices finds all meaningful [ALERT] line indices, skipping summary alerts.
func findAlertIndices(lines []string) []int {
	alertIndices := make([]int, 0, 5) // Pre-allocate for typical case of few alerts
	for i, line := range lines {
		if isRelevantAlert(line) {
			alertIndices = append(alertIndices, i)
		}
	}
	return alertIndices
}

// isRelevantAlert checks if a line contains a relevant alert (not a summary).
func isRelevantAlert(line string) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "[ALERT]") {
		return false
	}

	// Skip summary [ALERT] lines
	lineLower := strings.ToLower(trimmed)
	return !strings.Contains(lineLower, "fatal errors found in configuration") &&
		!strings.Contains(lineLower, "error(s) found in configuration file")
}

// extractErrorBlocks extracts error context blocks for each alert.
func extractErrorBlocks(lines []string, alertIndices []int, configLines []string, configContent string) []string {
	var errorBlocks []string
	for _, alertIdx := range alertIndices {
		block := buildErrorBlock(lines, alertIdx, configLines, configContent)
		if len(block) > 0 {
			errorBlocks = append(errorBlocks, strings.Join(block, "\n"))
		}
	}
	return errorBlocks
}

// buildErrorBlock builds a single error context block for an alert.
func buildErrorBlock(lines []string, alertIdx int, configLines []string, configContent string) []string {
	startIdx, endIdx := calculateContextRange(alertIdx, len(lines))

	var block []string
	var alertLine string

	// Build HAProxy output context
	for i := startIdx; i < endIdx; i++ {
		line := strings.TrimRight(lines[i], " \t\r\n")
		if shouldSkipLine(line) {
			continue
		}

		// Add arrow marker for the alert line
		if i == alertIdx {
			block = append(block, "→ "+line)
			alertLine = line
		} else {
			block = append(block, "  "+line)
		}
	}

	// Add config context if available
	if alertLine != "" && configContent != "" {
		if configContext := extractConfigContext(alertLine, configLines); configContext != "" {
			block = append(block, "", "  Config context:", configContext)
		}
	}

	return block
}

// calculateContextRange calculates the start and end indices for context lines (3 before/after).
func calculateContextRange(alertIdx, totalLines int) (start, end int) {
	start = alertIdx - 3
	if start < 0 {
		start = 0
	}

	end = alertIdx + 4 // +4 because we want 3 lines after (inclusive range)
	if end > totalLines {
		end = totalLines
	}

	return start, end
}

// shouldSkipLine checks if a line should be skipped (empty or summary line).
func shouldSkipLine(line string) bool {
	if line == "" {
		return true
	}

	lineLower := strings.ToLower(line)
	return strings.Contains(lineLower, "fatal errors found in configuration") ||
		strings.Contains(lineLower, "error(s) found in configuration file")
}

// extractConfigContext extracts configuration file context around an error line.
// It parses the line number from an [ALERT] message like "[haproxy.cfg:90]"
// and returns 3 lines before/after that line with line numbers and an arrow marker.
func extractConfigContext(alertLine string, configLines []string) string {
	// Parse line number from [ALERT] message
	// Format: [ALERT] ... : config : [haproxy.cfg:90] : ...
	// or: [ALERT] ... : [haproxy.cfg:90] : ...

	// Find [filename:linenum] pattern - look for second [ (after [ALERT])
	firstBracket := strings.Index(alertLine, "[")
	if firstBracket == -1 {
		return ""
	}

	// Look for second bracket after [ALERT]
	remaining := alertLine[firstBracket+1:]
	secondBracket := strings.Index(remaining, "[")
	if secondBracket == -1 {
		return ""
	}

	// Now parse the [filename:line] part
	fileLinePart := remaining[secondBracket+1:]
	colonIdx := strings.Index(fileLinePart, ":")
	if colonIdx == -1 {
		return ""
	}

	bracketClose := strings.Index(fileLinePart, "]")
	if bracketClose == -1 || bracketClose < colonIdx {
		return ""
	}

	// Extract line number part (after the colon, before the bracket)
	lineNumStr := fileLinePart[colonIdx+1 : bracketClose]
	lineNum := 0
	if _, err := fmt.Sscanf(lineNumStr, "%d", &lineNum); err != nil {
		return ""
	}

	// Convert to 0-based index
	errorLineIdx := lineNum - 1
	if errorLineIdx < 0 || errorLineIdx >= len(configLines) {
		return ""
	}

	// Calculate context range (3 lines before and after)
	startIdx := errorLineIdx - 3
	if startIdx < 0 {
		startIdx = 0
	}

	endIdx := errorLineIdx + 4 // +4 because we want 3 lines after
	if endIdx > len(configLines) {
		endIdx = len(configLines)
	}

	// Build context block with line numbers
	var contextLines []string
	for i := startIdx; i < endIdx; i++ {
		lineContent := configLines[i]
		lineNumber := i + 1

		var formatted string
		if i == errorLineIdx {
			// Error line - add arrow marker
			formatted = fmt.Sprintf("  %4d → %s", lineNumber, lineContent)
		} else {
			formatted = fmt.Sprintf("  %4d   %s", lineNumber, lineContent)
		}

		// Trim trailing spaces for cleaner output
		contextLines = append(contextLines, strings.TrimRight(formatted, " "))
	}

	return strings.Join(contextLines, "\n")
}
