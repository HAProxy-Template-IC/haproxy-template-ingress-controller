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

// Package testrunner implements validation test execution for HAProxyTemplateConfig.
//
// This package provides a test runner that executes embedded validation tests
// defined in HAProxyTemplateConfig CRDs. It can be used both by the CLI
// (controller validate command) and by the admission webhook for validation.
//
// The test runner:
//   - Creates resource stores from test fixtures
//   - Renders templates with fixture context
//   - Runs assertions against rendered output
//   - Returns structured test results
//
// This is a pure component with no EventBus dependency - it's called directly
// by the CLI and by the DryRunValidator component.
package testrunner

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"

	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/logging"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// Runner executes validation tests for HAProxyTemplateConfig.
//
// It's a pure component with no EventBus dependency, designed to be called
// directly from the CLI or from the DryRunValidator.
type Runner struct {
	// engineTemplate is a pre-compiled template engine WITHOUT path filters.
	// Workers will create their own engines with worker-specific paths.
	engineTemplate  templating.Engine
	validationPaths *dataplane.ValidationPaths // Base paths (used to create worker-specific paths)
	config          *config.Config
	logger          *slog.Logger
	workers         int
	debugFilters    bool                   // Enable detailed filter operation logging
	traceTemplates  bool                   // Enable template execution tracing
	profileIncludes bool                   // Enable include timing profiling
	capabilities    dataplane.Capabilities // HAProxy/DataPlane API capabilities
}

// testEntry is a tuple of test name and test definition for worker processing.
type testEntry struct {
	name string
	test config.ValidationTest
}

// Options configures the test runner.
type Options struct {
	// TestName filters tests to run. If empty, all tests run.
	TestName string

	// Logger for structured logging. If nil, uses default logger.
	Logger *slog.Logger

	// Workers is the number of parallel workers for test execution.
	// Default: 4
	// Set to 1 for sequential execution.
	Workers int

	// DebugFilters enables detailed filter operation logging.
	// When enabled, each sort comparison is logged with values and results.
	DebugFilters bool

	// ProfileIncludes enables include timing profiling.
	// When enabled, shows which included templates take the most time.
	ProfileIncludes bool

	// Capabilities defines which features are available for the local HAProxy version.
	// Used to determine path resolution (e.g., CRT-list paths fallback when not supported).
	Capabilities dataplane.Capabilities
}

// TestResults contains the results of running validation tests.
type TestResults struct {
	// TotalTests is the total number of tests executed.
	TotalTests int

	// PassedTests is the number of tests that passed all assertions.
	PassedTests int

	// FailedTests is the number of tests with at least one failed assertion.
	FailedTests int

	// TestResults contains detailed results for each test.
	TestResults []TestResult

	// Duration is the total time taken to run all tests.
	Duration time.Duration
}

// AllPassed returns true if all tests passed.
func (r *TestResults) AllPassed() bool {
	return r.FailedTests == 0 && r.TotalTests > 0
}

// TestResult contains the result of running a single validation test.
type TestResult struct {
	// TestName is the name of the test.
	TestName string

	// Description is the test description.
	Description string

	// Passed is true if all assertions passed.
	Passed bool

	// Duration is the time taken to run this test.
	Duration time.Duration

	// Assertions contains results for each assertion.
	Assertions []AssertionResult

	// RenderError is set if template rendering failed.
	RenderError string

	// RenderedConfig contains the rendered HAProxy configuration (for --dump-rendered).
	RenderedConfig string `json:"renderedConfig,omitempty" yaml:"renderedConfig,omitempty"`

	// RenderedMaps contains rendered map files (for --dump-rendered).
	RenderedMaps map[string]string `json:"renderedMaps,omitempty" yaml:"renderedMaps,omitempty"`

	// RenderedFiles contains rendered general files (for --dump-rendered).
	RenderedFiles map[string]string `json:"renderedFiles,omitempty" yaml:"renderedFiles,omitempty"`

	// RenderedCerts contains rendered SSL certificates (for --dump-rendered).
	RenderedCerts map[string]string `json:"renderedCerts,omitempty" yaml:"renderedCerts,omitempty"`

	// IncludeStats contains timing statistics for included templates (for --profile-includes).
	IncludeStats []templating.IncludeStats `json:"includeStats,omitempty" yaml:"includeStats,omitempty"`
}

// AssertionResult contains the result of running a single assertion.
type AssertionResult struct {
	// Type is the assertion type (haproxy_valid, contains, etc).
	Type string

	// Description is the assertion description.
	Description string

	// Passed is true if the assertion passed.
	Passed bool

	// Error contains the failure message if assertion failed.
	Error string

	// Target is the assertion target (e.g., "haproxy.cfg", "map:path-prefix.map").
	Target string `json:"target,omitempty" yaml:"target,omitempty"`

	// TargetSize is the size of the target content in bytes.
	TargetSize int `json:"targetSize,omitempty" yaml:"targetSize,omitempty"`

	// TargetPreview is a preview of the target content (first 200 chars, only for failed assertions).
	TargetPreview string `json:"targetPreview,omitempty" yaml:"targetPreview,omitempty"`
}

// New creates a new test runner.
//
// Parameters:
//   - cfg: The internal config containing templates and validation tests
//   - engine: Pre-compiled template engine
//   - validationPaths: Filesystem paths for HAProxy validation
//   - options: Runner options
//
// Returns:
//   - A new Runner instance ready to execute tests
func New(
	cfg *config.Config,
	engine templating.Engine,
	validationPaths *dataplane.ValidationPaths,
	options Options,
) *Runner {
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}

	workers := options.Workers
	if workers <= 0 {
		workers = runtime.NumCPU() // Default to number of CPUs
	}

	// Capture tracing state from template engine
	traceTemplates := engine.IsTracingEnabled()

	return &Runner{
		engineTemplate:  engine,
		validationPaths: validationPaths,
		config:          cfg,
		logger:          logger.With("component", "test-runner"),
		workers:         workers,
		debugFilters:    options.DebugFilters,
		traceTemplates:  traceTemplates,
		profileIncludes: options.ProfileIncludes,
		capabilities:    options.Capabilities,
	}
}

// createTestPaths creates per-test temp directories for isolated HAProxy validation.
//
// This creates a subdirectory structure under the base temp directory:
//
//	<base>/worker-<workerID>/test-<testNum>/maps/
//	<base>/worker-<workerID>/test-<testNum>/ssl/
//	<base>/worker-<workerID>/test-<testNum>/files/
//	<base>/worker-<workerID>/test-<testNum>/haproxy.cfg
//
// Each test gets its own isolated directories to prevent file conflicts during
// parallel test execution, even when multiple tests are processed by the same worker.
func (r *Runner) createTestPaths(workerID, testNum int) (*dataplane.ValidationPaths, error) {
	// Extract base temp directory from the shared validation paths
	baseTempDir := filepath.Dir(r.validationPaths.ConfigFile)

	// Create test-specific subdirectory within worker space
	testDir := filepath.Join(baseTempDir, fmt.Sprintf("worker-%d", workerID), fmt.Sprintf("test-%d", testNum))

	// Create base path configuration
	// IMPORTANT: Subdirectory names are derived from configured dataplane paths
	// using filepath.Base() to ensure consistency between production and validation.
	// HAProxy requires absolute paths to locate files, so we create absolute paths
	// within the isolated test directory (e.g., /tmp/haproxy-validate-12345/worker-0/test-1/maps).
	basePaths := dataplane.PathConfig{
		MapsDir:    filepath.Join(testDir, filepath.Base(r.config.Dataplane.MapsDir)),
		SSLDir:     filepath.Join(testDir, filepath.Base(r.config.Dataplane.SSLCertsDir)),
		GeneralDir: filepath.Join(testDir, filepath.Base(r.config.Dataplane.GeneralStorageDir)),
		ConfigFile: filepath.Join(testDir, "haproxy.cfg"),
	}

	// Use centralized path resolution to get capability-aware paths
	// This ensures CRTListDir is set correctly for HAProxy < 3.2
	resolvedPaths := dataplane.ResolvePaths(basePaths, r.capabilities)

	// Create all directories (CRTListDir may be same as GeneralDir or SSLDir)
	dirsToCreate := []string{resolvedPaths.MapsDir, resolvedPaths.SSLDir, resolvedPaths.GeneralDir}
	if resolvedPaths.CRTListDir != resolvedPaths.SSLDir && resolvedPaths.CRTListDir != resolvedPaths.GeneralDir {
		dirsToCreate = append(dirsToCreate, resolvedPaths.CRTListDir)
	}

	for _, dir := range dirsToCreate {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("failed to create test directory %s: %w", dir, err)
		}
	}

	return resolvedPaths.ToValidationPaths(), nil
}

// RunTests executes all validation tests (or a specific test if filtered).
//
// This method:
//  1. Filters tests if a specific test name was requested
//  2. For each test:
//     - Creates resource stores from fixtures
//     - Renders HAProxy configuration
//     - Runs all assertions
//  3. Aggregates and returns results
//
// Parameters:
//   - ctx: Context for cancellation and timeouts
//
// Returns:
//   - TestResults containing results for all executed tests
//   - error if a fatal error occurred (not test failures)
func (r *Runner) RunTests(ctx context.Context, testName string) (*TestResults, error) {
	startTime := time.Now()

	results := &TestResults{
		TestResults: make([]TestResult, 0),
	}

	// Filter tests if specific test requested
	testsToRun := r.config.ValidationTests
	if testName != "" {
		testsToRun = r.filterTests(r.config.ValidationTests, testName)
		if len(testsToRun) == 0 {
			return nil, fmt.Errorf("test %q not found", testName)
		}
	}

	results.TotalTests = len(testsToRun)

	if len(testsToRun) == 0 {
		r.logger.Info("No tests to run")
		return results, nil
	}

	// Determine number of workers (use 1 worker if only 1 test)
	numWorkers := r.workers
	if len(testsToRun) < numWorkers {
		numWorkers = len(testsToRun)
	}

	r.logger.Log(context.Background(), logging.LevelTrace, "Starting test execution",
		"total_tests", len(testsToRun),
		"workers", numWorkers)

	// Create channels for work distribution
	testChan := make(chan testEntry, len(testsToRun))
	resultChan := make(chan TestResult, len(testsToRun))

	// Start worker pool
	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go r.testWorker(ctx, i, testChan, resultChan, &wg)
	}

	// Send tests to workers
	for name, test := range testsToRun {
		testChan <- testEntry{name: name, test: test}
	}
	close(testChan)

	// Wait for all workers to finish in background
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Collect results
	for result := range resultChan {
		results.TestResults = append(results.TestResults, result)
		if result.Passed {
			results.PassedTests++
		} else {
			results.FailedTests++
		}
	}

	results.Duration = time.Since(startTime)

	r.logger.Info("Test run completed",
		"total", results.TotalTests,
		"passed", results.PassedTests,
		"failed", results.FailedTests,
		"duration", results.Duration)

	return results, nil
}

// testWorker is a worker goroutine that processes tests from the test channel.
// Each test gets its own isolated temp directory and template engine to prevent file conflicts.
func (r *Runner) testWorker(ctx context.Context, workerID int, tests <-chan testEntry, results chan<- TestResult, wg *sync.WaitGroup) {
	defer wg.Done()

	r.logger.Log(context.Background(), logging.LevelTrace, "Worker started", "worker_id", workerID)

	testNum := 0
	for entry := range tests {
		select {
		case <-ctx.Done():
			// Context cancelled, stop processing
			return
		default:
			testStartTime := time.Now()
			r.logger.Log(context.Background(), logging.LevelTrace, "Worker processing test",
				"worker_id", workerID,
				"test_num", testNum,
				"test", entry.name)

			// Create unique temp directory for this specific test
			dirCreateStart := time.Now()
			testPaths, err := r.createTestPaths(workerID, testNum)
			dirCreateDuration := time.Since(dirCreateStart)

			if err != nil {
				r.logger.Error("Failed to create test paths",
					"worker_id", workerID,
					"test_num", testNum,
					"test", entry.name,
					"error", err,
					"duration_ms", dirCreateDuration.Milliseconds())
				results <- TestResult{
					TestName:    entry.name,
					Description: entry.test.Description,
					Passed:      false,
					RenderError: fmt.Sprintf("failed to create test temp directory: %v", err),
				}
				testNum++
				continue
			}

			r.logger.Log(context.Background(), logging.LevelTrace, "Created test paths",
				"worker_id", workerID,
				"test_num", testNum,
				"test", entry.name,
				"config_file", testPaths.ConfigFile,
				"duration_ms", dirCreateDuration.Milliseconds())

			// Reuse pre-compiled template engine
			// The engine is thread-safe for concurrent renders and has no per-test mutable state.
			// Filter state is per-render (stored in context), not per-engine.
			testEngine := r.engineTemplate

			// Run test with isolated paths and engine
			result := r.runSingleTest(ctx, entry.name, &entry.test, testEngine, testPaths)

			testDuration := time.Since(testStartTime)
			r.logger.Log(context.Background(), logging.LevelTrace, "Test completed",
				"worker_id", workerID,
				"test_num", testNum,
				"test", entry.name,
				"passed", result.Passed,
				"total_duration_ms", testDuration.Milliseconds())

			// Append traces from worker engine to main engine (for --trace-templates output)
			if r.traceTemplates {
				r.engineTemplate.AppendTraces(testEngine)
			}

			results <- result

			testNum++
		}
	}
}

// filterTests filters validation tests by name.
func (r *Runner) filterTests(tests map[string]config.ValidationTest, name string) map[string]config.ValidationTest {
	filtered := make(map[string]config.ValidationTest)
	if test, exists := tests[name]; exists {
		filtered[name] = test
	}
	return filtered
}

// runSingleTest executes a single validation test using worker-specific engine and validation paths.
func (r *Runner) runSingleTest(ctx context.Context, testName string, test *config.ValidationTest, engine templating.Engine, validationPaths *dataplane.ValidationPaths) TestResult {
	startTime := time.Now()

	result := TestResult{
		TestName:    testName,
		Description: test.Description,
		Passed:      true,
		Assertions:  make([]AssertionResult, 0),
	}

	// 1. Merge global fixtures with test-specific fixtures
	fixtures := test.Fixtures
	httpFixtures := test.HTTPFixtures

	// Check for global fixtures in validationTests._global
	if globalTest, hasGlobal := r.config.ValidationTests["_global"]; hasGlobal {
		r.logger.Log(context.Background(), logging.LevelTrace, "Merging global fixtures with test fixtures",
			"test", testName,
			"global_fixture_types", len(globalTest.Fixtures),
			"test_fixture_types", len(test.Fixtures),
			"global_http_fixtures", len(globalTest.HTTPFixtures),
			"test_http_fixtures", len(test.HTTPFixtures))

		fixtures = MergeFixtures(globalTest.Fixtures, test.Fixtures)
		httpFixtures = MergeHTTPFixtures(globalTest.HTTPFixtures, test.HTTPFixtures)

		r.logger.Log(context.Background(), logging.LevelTrace, "Fixture merge completed",
			"test", testName,
			"merged_fixture_types", len(fixtures),
			"merged_http_fixtures", len(httpFixtures))
	}

	// 2. Create resource stores from merged fixtures
	stores, err := r.CreateStoresFromFixtures(fixtures)
	if err != nil {
		result.Passed = false
		result.RenderError = fmt.Sprintf("failed to create fixture stores: %v", err)
		result.Duration = time.Since(startTime)
		return result
	}

	// 3. Create HTTP store from HTTP fixtures
	// Always create the wrapper so that http.Fetch() fails gracefully when a fixture is missing
	store := CreateHTTPStoreFromFixtures(httpFixtures, r.logger)
	httpStore := NewFixtureHTTPStoreWrapper(store, r.logger)
	r.logger.Log(context.Background(), logging.LevelTrace, "Created HTTP fixture store",
		"test", testName,
		"fixture_count", len(httpFixtures))

	// 4. Parse current config if provided (for slot-aware server assignment testing)
	var currentConfig *parserconfig.StructuredConfig
	if test.CurrentConfig != "" {
		p, err := parser.New()
		if err != nil {
			result.Passed = false
			result.RenderError = fmt.Sprintf("failed to create parser for currentConfig: %v", err)
			result.Duration = time.Since(startTime)
			return result
		}
		currentConfig, err = p.ParseFromString(test.CurrentConfig)
		if err != nil {
			result.Passed = false
			result.RenderError = fmt.Sprintf("failed to parse currentConfig: %v", err)
			result.Duration = time.Since(startTime)
			return result
		}
		r.logger.Log(context.Background(), logging.LevelTrace, "Parsed currentConfig for test",
			"test", testName,
			"backends", len(currentConfig.Backends))
	}

	// 5. Render HAProxy configuration and auxiliary files (using worker-specific engine)
	haproxyConfig, auxiliaryFiles, includeStats, err := r.renderWithStores(engine, stores, validationPaths, httpStore, currentConfig, test.ExtraContext)
	if err != nil {
		result.RenderError = dataplane.SimplifyRenderingError(err)

		// Add rendering failure as assertion for completeness
		result.Assertions = append(result.Assertions, AssertionResult{
			Type:        "rendering",
			Description: "Template rendering failed",
			Passed:      false,
			Error:       result.RenderError,
		})
		// Don't return early - continue to run assertions
		// Some tests expect rendering to fail (negative tests with rendering_error assertions)
	} else {
		// Store rendered content for --dump-rendered flag
		result.RenderedConfig = haproxyConfig
		r.storeAuxiliaryFiles(&result, auxiliaryFiles)
		// Store include stats for --profile-includes flag
		result.IncludeStats = includeStats
	}

	// 6. Build template context for JSONPath assertions
	templateContext := r.buildRenderingContext(stores, validationPaths, httpStore, currentConfig)

	// 7. Run all assertions (whether rendering succeeded or failed)
	r.executeAssertions(ctx, &result, test, haproxyConfig, auxiliaryFiles, templateContext, validationPaths)

	// Test passes if either:
	// - Rendering succeeded AND all assertions passed
	// - Rendering failed BUT test has rendering_error assertions that passed
	if result.RenderError != "" && !hasRenderingErrorAssertions(test.Assertions) {
		result.Passed = false
	}

	result.Duration = time.Since(startTime)
	return result
}

// storeAuxiliaryFiles stores rendered auxiliary files in the test result for --dump-rendered flag.
func (r *Runner) storeAuxiliaryFiles(result *TestResult, auxiliaryFiles *dataplane.AuxiliaryFiles) {
	if auxiliaryFiles == nil {
		return
	}

	// Store rendered maps
	if len(auxiliaryFiles.MapFiles) > 0 {
		result.RenderedMaps = make(map[string]string)
		for _, mapFile := range auxiliaryFiles.MapFiles {
			result.RenderedMaps[mapFile.Path] = mapFile.Content
		}
	}

	// Store rendered general files
	if len(auxiliaryFiles.GeneralFiles) > 0 {
		result.RenderedFiles = make(map[string]string)
		for _, file := range auxiliaryFiles.GeneralFiles {
			result.RenderedFiles[file.Filename] = file.Content
		}
	}

	// Store rendered SSL certificates
	if len(auxiliaryFiles.SSLCertificates) > 0 {
		result.RenderedCerts = make(map[string]string)
		for _, cert := range auxiliaryFiles.SSLCertificates {
			result.RenderedCerts[cert.Path] = cert.Content
		}
	}
}

// executeAssertions runs all assertions for a test and updates the result.
func (r *Runner) executeAssertions(
	ctx context.Context,
	result *TestResult,
	test *config.ValidationTest,
	haproxyConfig string,
	auxiliaryFiles *dataplane.AuxiliaryFiles,
	templateContext map[string]interface{},
	validationPaths *dataplane.ValidationPaths,
) {
	for i := range test.Assertions {
		assertionResult := r.runAssertion(ctx, &test.Assertions[i], haproxyConfig, auxiliaryFiles, templateContext, result.RenderError, validationPaths)
		result.Assertions = append(result.Assertions, assertionResult)

		if !assertionResult.Passed {
			result.Passed = false
		}
	}
}

// hasRenderingErrorAssertions checks if the test has any assertions targeting rendering_error.
// This is used to determine if a test expects rendering to fail (negative test).
func hasRenderingErrorAssertions(assertions []config.ValidationAssertion) bool {
	for _, assertion := range assertions {
		if assertion.Target == "rendering_error" {
			return true
		}
	}
	return false
}

// renderWithStores renders HAProxy configuration using test fixture stores and worker-specific engine.
//
// This follows the same pattern as DryRunValidator.renderWithOverlayStores.
// When profileIncludes is enabled, it returns timing statistics for included templates.
// The currentConfig parameter enables slot-aware server assignment testing (nil for first deployment).
// The testExtraContext parameter allows test-specific extraContext values to override global ones.
func (r *Runner) renderWithStores(engine templating.Engine, stores map[string]types.Store, validationPaths *dataplane.ValidationPaths, httpStore *FixtureHTTPStoreWrapper, currentConfig *parserconfig.StructuredConfig, testExtraContext map[string]interface{}) (string, *dataplane.AuxiliaryFiles, []templating.IncludeStats, error) {
	// Build rendering context with fixture stores
	renderCtx := r.buildRenderingContext(stores, validationPaths, httpStore, currentConfig)

	// Merge test-specific extraContext (overrides global extraContext values)
	// IMPORTANT: Make a copy to avoid modifying the shared config map which would
	// cause state leakage between parallel test runs.
	if testExtraContext != nil {
		globalExtraContext := renderCtx["extraContext"].(map[string]interface{})
		mergedExtraContext := make(map[string]interface{}, len(globalExtraContext)+len(testExtraContext))
		for key, value := range globalExtraContext {
			mergedExtraContext[key] = value
		}
		for key, value := range testExtraContext {
			mergedExtraContext[key] = value
			// Also merge into top-level context for direct access
			renderCtx[key] = value
		}
		renderCtx["extraContext"] = mergedExtraContext
	}

	// Render main HAProxy configuration using worker-specific engine
	var haproxyConfig string
	var includeStats []templating.IncludeStats
	var err error

	if r.profileIncludes {
		haproxyConfig, includeStats, err = engine.RenderWithProfiling("haproxy.cfg", renderCtx)
	} else {
		haproxyConfig, err = engine.Render("haproxy.cfg", renderCtx)
	}
	if err != nil {
		return "", nil, nil, fmt.Errorf("failed to render haproxy.cfg: %w", err)
	}

	// Render auxiliary files using worker-specific engine (pre-declared files)
	staticFiles, err := r.renderAuxiliaryFiles(engine, renderCtx, validationPaths)
	if err != nil {
		return "", nil, nil, fmt.Errorf("failed to render auxiliary files: %w", err)
	}

	// Extract dynamic files registered during template rendering
	fileRegistry := renderCtx["fileRegistry"].(*rendercontext.FileRegistry)
	dynamicFiles := fileRegistry.GetFiles()

	// Merge static (pre-declared) and dynamic (registered) files
	auxiliaryFiles := rendercontext.MergeAuxiliaryFiles(staticFiles, dynamicFiles)

	// Debug logging
	staticCount := len(staticFiles.MapFiles) + len(staticFiles.GeneralFiles) + len(staticFiles.SSLCertificates) + len(staticFiles.CRTListFiles)
	dynamicCount := len(dynamicFiles.MapFiles) + len(dynamicFiles.GeneralFiles) + len(dynamicFiles.SSLCertificates) + len(dynamicFiles.CRTListFiles)
	if dynamicCount > 0 {
		r.logger.Log(context.Background(), logging.LevelTrace, "Merged auxiliary files",
			"static_count", staticCount,
			"dynamic_count", dynamicCount)
	}

	return haproxyConfig, auxiliaryFiles, includeStats, nil
}

// buildRenderingContext builds the template rendering context using fixture stores.
//
// This method delegates to the centralized rendercontext.Builder to ensure consistent
// context creation across all usages (renderer, testrunner, benchmark, dryrunvalidator).
//
// Special handling for TestRunner:
//   - Creates PathResolver from ValidationPaths (not from config.Dataplane)
//   - Separates haproxy-pods store from resource stores
//   - Accepts optional currentConfig for slot-aware server assignment testing
func (r *Runner) buildRenderingContext(stores map[string]types.Store, validationPaths *dataplane.ValidationPaths, httpStore *FixtureHTTPStoreWrapper, currentConfig *parserconfig.StructuredConfig) map[string]interface{} {
	// Create PathResolver from ValidationPaths
	pathResolver := rendercontext.PathResolverFromValidationPaths(validationPaths)

	// Separate haproxy-pods from resource stores (goes in controller namespace)
	resourceStores, haproxyPodStore := rendercontext.SeparateHAProxyPodStore(stores)
	if haproxyPodStore != nil {
		r.logger.Log(context.Background(), logging.LevelTrace, "wrapping haproxy-pods store for rendering context")
	}

	// Build context using centralized builder
	builder := rendercontext.NewBuilder(
		r.config,
		pathResolver,
		r.logger,
		rendercontext.WithStores(resourceStores),
		rendercontext.WithHAProxyPodStore(haproxyPodStore),
		rendercontext.WithHTTPFetcher(httpStore),
		rendercontext.WithCurrentConfig(currentConfig),
	)

	renderCtx, _ := builder.Build()
	return renderCtx
}

// renderAuxiliaryFiles renders all auxiliary files (maps, general files, SSL certificates) using worker-specific engine.
func (r *Runner) renderAuxiliaryFiles(engine templating.Engine, renderCtx map[string]interface{}, validationPaths *dataplane.ValidationPaths) (*dataplane.AuxiliaryFiles, error) {
	auxFiles := &dataplane.AuxiliaryFiles{}

	// Render map files using worker-specific engine
	for name := range r.config.Maps {
		rendered, err := engine.Render(name, renderCtx)
		if err != nil {
			return nil, fmt.Errorf("failed to render map file %s: %w", name, err)
		}

		auxFiles.MapFiles = append(auxFiles.MapFiles, auxiliaryfiles.MapFile{
			Path:    name,
			Content: rendered,
		})
	}

	// Render general files using worker-specific engine
	for name := range r.config.Files {
		rendered, err := engine.Render(name, renderCtx)
		if err != nil {
			return nil, fmt.Errorf("failed to render general file %s: %w", name, err)
		}

		auxFiles.GeneralFiles = append(auxFiles.GeneralFiles, auxiliaryfiles.GeneralFile{
			Filename: name,
			Path:     filepath.Join(validationPaths.GeneralStorageDir, name),
			Content:  rendered,
		})
	}

	// Render SSL certificates using worker-specific engine
	for name := range r.config.SSLCertificates {
		rendered, err := engine.Render(name, renderCtx)
		if err != nil {
			return nil, fmt.Errorf("failed to render SSL certificate %s: %w", name, err)
		}

		auxFiles.SSLCertificates = append(auxFiles.SSLCertificates, auxiliaryfiles.SSLCertificate{
			Path:    name,
			Content: rendered,
		})
	}

	return auxFiles, nil
}

// runAssertion executes a single assertion.
func (r *Runner) runAssertion(
	ctx context.Context,
	assertion *config.ValidationAssertion,
	haproxyConfig string,
	auxiliaryFiles *dataplane.AuxiliaryFiles,
	templateContext map[string]interface{},
	renderError string,
	validationPaths *dataplane.ValidationPaths,
) AssertionResult {
	result := AssertionResult{
		Type:        assertion.Type,
		Description: assertion.Description,
		Passed:      true,
	}

	switch assertion.Type {
	case "haproxy_valid":
		result = r.assertHAProxyValid(ctx, haproxyConfig, auxiliaryFiles, assertion, validationPaths)

	case "contains":
		result = r.assertContains(haproxyConfig, auxiliaryFiles, assertion, renderError)

	case "not_contains":
		result = r.assertNotContains(haproxyConfig, auxiliaryFiles, assertion, renderError)

	case "match_count":
		result = r.assertMatchCount(haproxyConfig, auxiliaryFiles, assertion, renderError)

	case "equals":
		result = r.assertEquals(haproxyConfig, auxiliaryFiles, assertion, renderError)

	case "jsonpath":
		result = r.assertJSONPath(templateContext, assertion)

	case "match_order":
		result = r.assertMatchOrder(haproxyConfig, auxiliaryFiles, assertion, renderError)

	default:
		result.Passed = false
		result.Error = fmt.Sprintf("unknown assertion type: %s", assertion.Type)
	}

	return result
}
