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
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/haproxytech/client-native/v6/models"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/validators"
)

// haproxyCheckMutex serializes HAProxy validation to work around issues with
// concurrent haproxy -c execution. Without this, concurrent validations can
// interfere with each other even though they use isolated temp directories.
var haproxyCheckMutex sync.Mutex

// syntaxParser is a package-level singleton parser for syntax validation.
// Uses sync.Once to ensure it's only created once and reused across all calls
// to validateSyntax(). The parser is already protected by parserMutex in the
// parser package, so sharing is thread-safe.
var (
	syntaxParser     *parser.Parser
	syntaxParserOnce sync.Once
	syntaxParserErr  error
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
// Uses a package-level singleton parser to avoid re-initializing parser internals
// on every call.
func validateSyntax(config string) (*parser.StructuredConfig, error) {
	// Get or create singleton parser
	syntaxParserOnce.Do(func() {
		syntaxParser, syntaxParserErr = parser.New()
	})
	if syntaxParserErr != nil {
		return nil, fmt.Errorf("failed to create parser: %w", syntaxParserErr)
	}

	// Parse configuration - this validates syntax
	parsed, err := syntaxParser.ParseFromString(config)
	if err != nil {
		return nil, fmt.Errorf("syntax error: %w", err)
	}

	return parsed, nil
}

// cachedValidator provides zero-allocation validation with caching.
// It is initialized lazily on first use.
var (
	cachedValidatorV30 *validators.CachedValidator
	cachedValidatorV31 *validators.CachedValidator
	cachedValidatorV32 *validators.CachedValidator
	validatorOnceV30   sync.Once
	validatorOnceV31   sync.Once
	validatorOnceV32   sync.Once
)

// getCachedValidatorForVersion returns the cached validator for a HAProxy version.
func getCachedValidatorForVersion(version *Version) *validators.CachedValidator {
	if version == nil {
		// Default to v3.0
		validatorOnceV30.Do(func() {
			cachedValidatorV30 = validators.NewCachedValidator(3, 0)
		})
		return cachedValidatorV30
	}

	if version.Major == 3 {
		switch {
		case version.Minor >= 2:
			validatorOnceV32.Do(func() {
				cachedValidatorV32 = validators.NewCachedValidator(3, 2)
			})
			return cachedValidatorV32
		case version.Minor >= 1:
			validatorOnceV31.Do(func() {
				cachedValidatorV31 = validators.NewCachedValidator(3, 1)
			})
			return cachedValidatorV31
		default:
			validatorOnceV30.Do(func() {
				cachedValidatorV30 = validators.NewCachedValidator(3, 0)
			})
			return cachedValidatorV30
		}
	}

	if version.Major > 3 {
		validatorOnceV32.Do(func() {
			cachedValidatorV32 = validators.NewCachedValidator(3, 2)
		})
		return cachedValidatorV32
	}

	validatorOnceV30.Do(func() {
		cachedValidatorV30 = validators.NewCachedValidator(3, 0)
	})
	return cachedValidatorV30
}

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
