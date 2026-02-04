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
	"crypto/sha256"
	"encoding/hex"
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

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
	v30 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v30"
	v31 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v31"
	v32 "gitlab.com/haproxy-haptic/haptic/pkg/generated/dataplaneapi/v32"
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

// Resolved schema caches - avoids repeated allOf resolution during validation.
// Each validation call was creating new merged schema objects, causing massive
// memory allocation churn. These caches store resolved schemas per spec version.
var (
	resolvedSchemaCacheV30 = make(map[string]*openapi3.Schema)
	resolvedSchemaCacheV31 = make(map[string]*openapi3.Schema)
	resolvedSchemaCacheV32 = make(map[string]*openapi3.Schema)
	resolvedSchemaMu       sync.RWMutex
)

// validationResultCache caches the result of the last successful validation.
// Since validation is expensive (parsing + schema validation + haproxy -c),
// we skip it when the same config is validated again.
type validationResultCache struct {
	mu              sync.RWMutex
	lastConfigHash  string
	lastAuxHash     string
	lastVersionHash string
}

var validationCache = &validationResultCache{}

// =============================================================================
// Version-aware Model Conversion (using centralized client converters)
// =============================================================================

// ValidationPaths holds the filesystem paths for HAProxy validation.
// These paths must match the HAProxy Dataplane API server's resource configuration.
type ValidationPaths struct {
	// TempDir is the root temp directory for validation files.
	// The validator is responsible for cleaning this up after validation completes.
	// This prevents race conditions where the renderer's cleanup runs before
	// the async validator can use the validation files.
	TempDir           string
	MapsDir           string
	SSLCertsDir       string
	CRTListDir        string // Directory for CRT-list files (may differ from SSLCertsDir on HAProxy < 3.2)
	GeneralStorageDir string
	ConfigFile        string
}

// ValidateSyntaxAndSchema performs syntax and schema validation on HAProxy configuration.
//
// This function runs Phase 1 (syntax validation) and Phase 1.5 (API schema validation)
// but NOT Phase 2 (semantic validation with haproxy binary).
//
// Use this when you need to parse the config without needing file I/O or the haproxy binary.
// The primary use case is when you need to parse the original config (before path modifications)
// for downstream reuse, while semantic validation is done separately with a modified config.
//
// Parameters:
//   - config: The HAProxy configuration content to validate
//   - version: HAProxy/DataPlane API version for schema selection (nil uses default v3.0)
//
// Returns:
//   - *parser.StructuredConfig: The parsed configuration
//   - error: ValidationError with phase information if validation fails
func ValidateSyntaxAndSchema(config string, version *Version) (*parser.StructuredConfig, error) {
	// Phase 1: Syntax validation with client-native parser
	parsedConfig, err := validateSyntax(config)
	if err != nil {
		return nil, &ValidationError{
			Phase:   "syntax",
			Message: "configuration has syntax errors",
			Cause:   err,
		}
	}

	// Phase 1.5: API schema validation with OpenAPI spec
	if err := validateAPISchema(parsedConfig, version); err != nil {
		return nil, &ValidationError{
			Phase:   "schema",
			Message: "configuration violates API schema constraints",
			Cause:   err,
		}
	}

	return parsedConfig, nil
}

// ValidateSemantics performs semantic validation using the haproxy binary (-c flag).
//
// This function runs only Phase 2 (semantic validation) and assumes syntax/schema validation
// has already been done. Use this after ValidateSyntaxAndSchema() when you need to validate
// a modified config (e.g., with temp paths) separately from parsing.
//
// Parameters:
//   - mainConfig: The HAProxy configuration content (may have modified paths for temp directory)
//   - auxFiles: All auxiliary files (maps, certificates, general files)
//   - paths: Filesystem paths for validation (must be isolated for parallel execution)
//   - skipDNSValidation: If true, adds -dr flag to skip DNS resolution failures
//
// Returns:
//   - error: ValidationError with phase "semantic" if validation fails
func ValidateSemantics(mainConfig string, auxFiles *AuxiliaryFiles, paths *ValidationPaths, skipDNSValidation bool) error {
	if err := validateSemantics(mainConfig, auxFiles, paths, skipDNSValidation); err != nil {
		return &ValidationError{
			Phase:   "semantic",
			Message: "configuration has semantic errors",
			Cause:   err,
		}
	}
	return nil
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
// Validation result caching: If the same config (main + aux files + version) has been
// successfully validated before, the cached result is returned immediately. This is safe
// because HAProxy config validation is deterministic - the same inputs always produce
// the same result.
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
//   - *parser.StructuredConfig: The pre-parsed configuration from syntax validation (nil on cache hit or error)
//   - error: nil if validation succeeds, ValidationError with phase information if validation fails
func ValidateConfiguration(mainConfig string, auxFiles *AuxiliaryFiles, paths *ValidationPaths, version *Version, skipDNSValidation bool) (*parser.StructuredConfig, error) {
	// Check validation cache first - skip validation if same config already validated
	configHash := hashValidationInput(mainConfig)
	auxHash := hashAuxFiles(auxFiles)
	versionHash := hashVersion(version)

	if isValidationCached(configHash, auxHash, versionHash) {
		slog.Debug("validation cache hit, skipping validation")
		return nil, ErrValidationCacheHit // Cache hit - caller should use parser cache if parsed config needed
	}

	// Timing variables for phase breakdown
	var syntaxMs, schemaMs, semanticMs int64
	startTime := time.Now()

	// Phase 1: Syntax validation with client-native parser
	// This also returns the parsed configuration for Phase 1.5
	syntaxStart := time.Now()
	parsedConfig, err := validateSyntax(mainConfig)
	syntaxMs = time.Since(syntaxStart).Milliseconds()
	if err != nil {
		return nil, &ValidationError{
			Phase:   "syntax",
			Message: "configuration has syntax errors",
			Cause:   err,
		}
	}

	// Phase 1.5: API schema validation with OpenAPI spec
	schemaStart := time.Now()
	if err := validateAPISchema(parsedConfig, version); err != nil {
		return nil, &ValidationError{
			Phase:   "schema",
			Message: "configuration violates API schema constraints",
			Cause:   err,
		}
	}
	schemaMs = time.Since(schemaStart).Milliseconds()

	// Phase 2: Semantic validation with haproxy binary
	semanticStart := time.Now()
	if err := validateSemantics(mainConfig, auxFiles, paths, skipDNSValidation); err != nil {
		return nil, &ValidationError{
			Phase:   "semantic",
			Message: "configuration has semantic errors",
			Cause:   err,
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

	// Cache successful validation result for future checks
	cacheValidationResult(configHash, auxHash, versionHash)

	return parsedConfig, nil
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
	validationErrors = append(validationErrors, validateBackendSections(spec, parsed.Backends)...)

	// Validate frontend sections
	validationErrors = append(validationErrors, validateFrontendSections(spec, parsed.Frontends)...)

	// If there were any validation errors, return them
	if len(validationErrors) > 0 {
		return fmt.Errorf("API schema validation failed:\n  - %s",
			strings.Join(validationErrors, "\n  - "))
	}

	return nil
}

// validateBackendSections validates all configuration elements within backends.
func validateBackendSections(spec *openapi3.T, backends []*models.Backend) []string {
	var errors []string
	for i := range backends {
		backend := backends[i]
		errors = append(errors, validateBackendServers(spec, backend)...)
		errors = append(errors, validateBackendRules(spec, backend)...)
		errors = append(errors, validateBackendChecks(spec, backend)...)
	}
	return errors
}

// validateFrontendSections validates all configuration elements within frontends.
func validateFrontendSections(spec *openapi3.T, frontends []*models.Frontend) []string {
	var errors []string
	for i := range frontends {
		frontend := frontends[i]
		errors = append(errors, validateFrontendBinds(spec, frontend)...)
		errors = append(errors, validateFrontendRules(spec, frontend)...)
		errors = append(errors, validateFrontendElements(spec, frontend)...)
	}
	return errors
}

// validateSlice validates a slice of models against the OpenAPI schema.
func validateSlice[T any](spec *openapi3.T, schemaName, parentName, itemType string, items []T) []string {
	var errors []string
	for idx, item := range items {
		if err := validateModelOptimized(spec, schemaName, item); err != nil {
			errors = append(errors, fmt.Sprintf("%s, %s %d: %v", parentName, itemType, idx, err))
		}
	}
	return errors
}

// validateBackendServers validates servers and server templates in a backend.
func validateBackendServers(spec *openapi3.T, backend *models.Backend) []string {
	// Pre-allocate with estimated capacity
	errors := make([]string, 0, len(backend.Servers)+len(backend.ServerTemplates))
	for serverName := range backend.Servers {
		server := backend.Servers[serverName]
		if err := validateModelOptimized(spec, "server", &server); err != nil {
			errors = append(errors, fmt.Sprintf("backend %s, server %s: %v", backend.Name, serverName, err))
		}
	}
	for templateName := range backend.ServerTemplates {
		template := backend.ServerTemplates[templateName]
		if err := validateModelOptimized(spec, "server_template", &template); err != nil {
			errors = append(errors, fmt.Sprintf("backend %s, server template %s: %v", backend.Name, templateName, err))
		}
	}
	return errors
}

// validateBackendRules validates various rule types in a backend.
func validateBackendRules(spec *openapi3.T, backend *models.Backend) []string {
	var errors []string
	name := "backend " + backend.Name

	errors = append(errors, validateSlice(spec, "http_request_rule", name, "http-request rule", backend.HTTPRequestRuleList)...)
	errors = append(errors, validateSlice(spec, "http_response_rule", name, "http-response rule", backend.HTTPResponseRuleList)...)
	errors = append(errors, validateSlice(spec, "tcp_request_rule", name, "tcp-request rule", backend.TCPRequestRuleList)...)
	errors = append(errors, validateSlice(spec, "tcp_response_rule", name, "tcp-response rule", backend.TCPResponseRuleList)...)
	errors = append(errors, validateSlice(spec, "http_after_response_rule", name, "http-after-response rule", backend.HTTPAfterResponseRuleList)...)
	errors = append(errors, validateSlice(spec, "http_error_rule", name, "http-error rule", backend.HTTPErrorRuleList)...)
	errors = append(errors, validateSlice(spec, "server_switching_rule", name, "server switching rule", backend.ServerSwitchingRuleList)...)
	errors = append(errors, validateSlice(spec, "stick_rule", name, "stick rule", backend.StickRuleList)...)

	errors = append(errors, validateSlice(spec, "acl", name, "ACL", backend.ACLList)...)
	errors = append(errors, validateSlice(spec, "filter", name, "filter", backend.FilterList)...)
	errors = append(errors, validateSlice(spec, "log_target", name, "log target", backend.LogTargetList)...)
	return errors
}

// validateBackendChecks validates health checks in a backend.
func validateBackendChecks(spec *openapi3.T, backend *models.Backend) []string {
	var errors []string
	name := "backend " + backend.Name

	errors = append(errors, validateSlice(spec, "http_check", name, "http-check", backend.HTTPCheckList)...)
	errors = append(errors, validateSlice(spec, "tcp_check", name, "tcp-check", backend.TCPCheckRuleList)...)
	return errors
}

// validateFrontendBinds validates bind configurations in a frontend.
func validateFrontendBinds(spec *openapi3.T, frontend *models.Frontend) []string {
	errors := make([]string, 0, len(frontend.Binds))
	for bindName := range frontend.Binds {
		bind := frontend.Binds[bindName]
		if err := validateModelOptimized(spec, "bind", &bind); err != nil {
			errors = append(errors, fmt.Sprintf("frontend %s, bind %s: %v", frontend.Name, bindName, err))
		}
	}
	return errors
}

// validateFrontendRules validates various rule types in a frontend.
func validateFrontendRules(spec *openapi3.T, frontend *models.Frontend) []string {
	var errors []string
	name := "frontend " + frontend.Name

	errors = append(errors, validateSlice(spec, "http_request_rule", name, "http-request rule", frontend.HTTPRequestRuleList)...)
	errors = append(errors, validateSlice(spec, "http_response_rule", name, "http-response rule", frontend.HTTPResponseRuleList)...)
	errors = append(errors, validateSlice(spec, "tcp_request_rule", name, "tcp-request rule", frontend.TCPRequestRuleList)...)
	errors = append(errors, validateSlice(spec, "http_after_response_rule", name, "http-after-response rule", frontend.HTTPAfterResponseRuleList)...)
	errors = append(errors, validateSlice(spec, "http_error_rule", name, "http-error rule", frontend.HTTPErrorRuleList)...)
	errors = append(errors, validateSlice(spec, "backend_switching_rule", name, "backend switching rule", frontend.BackendSwitchingRuleList)...)

	errors = append(errors, validateSlice(spec, "acl", name, "ACL", frontend.ACLList)...)
	return errors
}

// validateFrontendElements validates other frontend elements (filters, log targets, captures).
func validateFrontendElements(spec *openapi3.T, frontend *models.Frontend) []string {
	var errors []string
	name := "frontend " + frontend.Name

	errors = append(errors, validateSlice(spec, "filter", name, "filter", frontend.FilterList)...)
	errors = append(errors, validateSlice(spec, "log_target", name, "log target", frontend.LogTargetList)...)
	errors = append(errors, validateSlice(spec, "capture", name, "capture", frontend.CaptureList)...)
	return errors
}

// removeNullValuesInPlace recursively removes null values from a map in place.
// This avoids the allocation overhead of creating a new map, which is significant
// when validating thousands of models per reconciliation.
func removeNullValuesInPlace(obj map[string]interface{}) {
	for k, v := range obj {
		if v == nil {
			delete(obj, k)
			continue
		}

		// Recursively clean nested maps
		if nested, ok := v.(map[string]interface{}); ok {
			removeNullValuesInPlace(nested)
			if len(nested) == 0 {
				delete(obj, k)
			}
		}
	}
}

// filterToSchemaProperties removes fields from obj that are not defined in the schema.
// This replaces the typed conversion step (ConvertToVersioned) which filtered fields
// by unmarshaling into version-specific structs. Working directly with maps is faster.
//
// The schema's Properties map contains all valid field names. Any field in obj that
// doesn't appear in the schema is removed. This ensures validation only checks fields
// that are valid for the target API version.
func filterToSchemaProperties(obj map[string]interface{}, schema *openapi3.Schema) {
	if schema == nil || schema.Properties == nil {
		return
	}

	for key := range obj {
		if _, exists := schema.Properties[key]; !exists {
			delete(obj, key)
		}
	}
}

// validateModelOptimized validates a client-native model using an optimized pipeline.
// Reduces JSON operations from 6 to 2 by working directly with maps and filtering
// based on schema properties instead of using typed intermediate conversion.
//
// This function is used for high-volume validation (servers, binds) where the
// allocation reduction is most impactful.
//
// The spec parameter should be the version-specific OpenAPI spec obtained via
// getSwaggerForVersion(). The version information is encoded in the spec's schema
// definitions, so no separate version parameter is needed.
func validateModelOptimized(spec *openapi3.T, schemaName string, model interface{}) error {
	// Step 1: Marshal model to JSON (1 JSON op)
	jsonData, err := json.Marshal(model)
	if err != nil {
		return fmt.Errorf("failed to marshal model: %w", err)
	}

	// Step 2: Unmarshal to map (1 JSON op)
	var obj map[string]interface{}
	if err := json.Unmarshal(jsonData, &obj); err != nil {
		return fmt.Errorf("failed to unmarshal model: %w", err)
	}

	// Step 3: Transform metadata in place (no JSON ops)
	// This converts client-native flat metadata to API nested format
	client.TransformMetadataForValidation(obj)

	// Step 4: Get schema and filter to only schema properties (no JSON ops)
	// This replaces ConvertToVersioned which filtered by unmarshaling to typed structs
	schema, err := getResolvedSchema(spec, schemaName)
	if err != nil {
		return err
	}
	filterToSchemaProperties(obj, schema)

	// Step 5: Remove null values in place (no JSON ops)
	removeNullValuesInPlace(obj)

	// Step 6: Validate against schema
	if err := schema.VisitJSON(obj); err != nil {
		return fmt.Errorf("schema validation failed: %w", err)
	}

	return nil
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

// selectSchemaCache returns the appropriate cache for the given spec.
func selectSchemaCache(spec *openapi3.T) map[string]*openapi3.Schema {
	switch spec {
	case cachedSpecV32:
		return resolvedSchemaCacheV32
	case cachedSpecV31:
		return resolvedSchemaCacheV31
	default:
		return resolvedSchemaCacheV30
	}
}

// getResolvedSchema returns a cached resolved schema, resolving and caching if needed.
// This eliminates repeated allOf resolution for the same schema, which was causing
// massive memory allocation churn (100+ GB per 6 minutes in production).
func getResolvedSchema(spec *openapi3.T, schemaName string) (*openapi3.Schema, error) {
	cache := selectSchemaCache(spec)

	// Fast path: check if already cached (read lock)
	resolvedSchemaMu.RLock()
	if cached, ok := cache[schemaName]; ok {
		resolvedSchemaMu.RUnlock()
		return cached, nil
	}
	resolvedSchemaMu.RUnlock()

	// Slow path: resolve and cache (write lock)
	resolvedSchemaMu.Lock()
	defer resolvedSchemaMu.Unlock()

	// Double-check after acquiring write lock
	if cached, ok := cache[schemaName]; ok {
		return cached, nil
	}

	// Get the schema from the spec
	schemaRef, ok := spec.Components.Schemas[schemaName]
	if !ok {
		return nil, fmt.Errorf("schema %s not found in OpenAPI spec", schemaName)
	}

	// Resolve allOf if present
	schema := schemaRef.Value
	if len(schema.AllOf) > 0 {
		resolved, err := resolveAllOf(spec, schema)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve allOf for %s: %w", schemaName, err)
		}
		schema = resolved
	}

	// Cache the resolved schema
	cache[schemaName] = schema
	return schema, nil
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
		if err := clearDirectory(dir); err != nil {
			return err
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

// clearDirectory creates a directory and removes all its contents.
// Uses retry logic to handle race conditions where the directory is deleted
// between MkdirAll and ReadDir (e.g., by concurrent cleanup).
func clearDirectory(dir string) error {
	var entries []os.DirEntry
	for attempt := 0; attempt < 2; attempt++ {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}

		var err error
		entries, err = os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) && attempt == 0 {
				// Directory was deleted between MkdirAll and ReadDir
				// (race with concurrent cleanup), retry once
				continue
			}
			return fmt.Errorf("failed to read directory %s: %w", dir, err)
		}
		break // Success
	}

	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("failed to remove %s: %w", path, err)
		}
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
	// Use file.Path when set (contains full relative path like "ssl/ca-bundle.pem" for
	// CA files). Fall back to file.Filename for backward compatibility with code that
	// only sets Filename.
	for _, file := range auxFiles.GeneralFiles {
		pathToUse := file.Path
		if pathToUse == "" {
			pathToUse = file.Filename
		}
		filePath := resolveAuxiliaryFilePath(pathToUse, configDir, paths.GeneralStorageDir)
		if err := writeFileWithDir(filePath, file.Content, "general file "+pathToUse); err != nil {
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

	// Write SSL CA files (stored in same directory as SSL certificates)
	for _, caFile := range auxFiles.SSLCaFiles {
		caPath := resolveAuxiliaryFilePath(caFile.Path, configDir, paths.SSLCertsDir)
		if err := writeFileWithDir(caPath, caFile.Content, "SSL CA file "+caFile.Path); err != nil {
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

// =============================================================================
// Validation Result Caching
// =============================================================================

// hashValidationInput computes a SHA256 hash of the main config content.
func hashValidationInput(config string) string {
	h := sha256.Sum256([]byte(config))
	return hex.EncodeToString(h[:])
}

// hashAuxFiles computes a combined hash of all auxiliary files.
// The hash includes file paths and contents to detect any changes.
func hashAuxFiles(auxFiles *AuxiliaryFiles) string {
	if auxFiles == nil {
		return ""
	}

	h := sha256.New()

	// Hash map files
	for _, f := range auxFiles.MapFiles {
		h.Write([]byte(f.Path))
		h.Write([]byte(f.Content))
	}

	// Hash general files
	for _, f := range auxFiles.GeneralFiles {
		h.Write([]byte(f.Path))
		h.Write([]byte(f.Content))
	}

	// Hash SSL certificates
	for _, f := range auxFiles.SSLCertificates {
		h.Write([]byte(f.Path))
		h.Write([]byte(f.Content))
	}

	// Hash SSL CA files
	for _, f := range auxFiles.SSLCaFiles {
		h.Write([]byte(f.Path))
		h.Write([]byte(f.Content))
	}

	// Hash CRT-list files
	for _, f := range auxFiles.CRTListFiles {
		h.Write([]byte(f.Path))
		h.Write([]byte(f.Content))
	}

	return hex.EncodeToString(h.Sum(nil))
}

// hashVersion computes a hash string for the version to include in cache key.
func hashVersion(version *Version) string {
	if version == nil {
		return "nil"
	}
	return fmt.Sprintf("%d.%d", version.Major, version.Minor)
}

// isValidationCached checks if the given config combination was already validated successfully.
func isValidationCached(configHash, auxHash, versionHash string) bool {
	validationCache.mu.RLock()
	defer validationCache.mu.RUnlock()

	return validationCache.lastConfigHash == configHash &&
		validationCache.lastAuxHash == auxHash &&
		validationCache.lastVersionHash == versionHash &&
		validationCache.lastConfigHash != "" // Ensure cache is not empty
}

// cacheValidationResult stores the successful validation result for future checks.
func cacheValidationResult(configHash, auxHash, versionHash string) {
	validationCache.mu.Lock()
	defer validationCache.mu.Unlock()

	validationCache.lastConfigHash = configHash
	validationCache.lastAuxHash = auxHash
	validationCache.lastVersionHash = versionHash
}
