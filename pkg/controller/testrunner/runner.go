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
	"runtime"
	"sync"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/logging"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// New creates a new test runner.
//
// Parameters:
//   - cfg: The internal config containing templates and validation tests
//   - engine: Pre-compiled template engine
//   - validationPaths: Filesystem paths for HAProxy validation
//   - options: Runner options (pointer to avoid hugeParam copy — the
//     embedded Capabilities + new typed-resource-types map push the
//     struct past 80 bytes). Pass nil to use defaults.
//
// Returns:
//   - A new Runner instance ready to execute tests
func New(
	cfg *config.Config,
	engine templating.Engine,
	validationPaths *dataplane.ValidationPaths,
	options *Options,
) *Runner {
	if options == nil {
		options = &Options{}
	}

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
		engineTemplate:     engine,
		validationPaths:    validationPaths,
		config:             cfg,
		logger:             logger.With("component", "test-runner"),
		workers:            workers,
		debugFilters:       options.DebugFilters,
		traceTemplates:     traceTemplates,
		profileIncludes:    options.ProfileIncludes,
		capabilities:       options.Capabilities,
		haproxyVersion:     options.HAProxyVersion,
		typedResourceTypes: options.TypedResourceTypes,
	}
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

	// Separate tests into runnable and skipped based on HAProxy version requirements
	runnableTests := make(map[string]config.ValidationTest, len(testsToRun))
	for name := range testsToRun {
		test := testsToRun[name]
		// "_global" carries shared fixtures merged into every test (see the
		// _global lookup in runSingleTest); it is never executed as a standalone
		// test. The benchmark path excludes it the same way (cmd/controller/benchmark.go).
		if name == "_global" {
			continue
		}
		if reason := r.shouldSkipTest(&test); reason != "" {
			results.SkippedTests++
			results.TestResults = append(results.TestResults, TestResult{
				TestName:    name,
				Description: test.Description,
				Skipped:     true,
				SkipReason:  reason,
			})
		} else {
			runnableTests[name] = test
		}
	}

	results.TotalTests = len(runnableTests)

	if len(runnableTests) == 0 {
		if results.SkippedTests > 0 {
			r.logger.Debug("All tests skipped", "skipped", results.SkippedTests)
		} else {
			r.logger.Debug("No tests to run")
		}
		results.Duration = time.Since(startTime)
		return results, nil
	}

	// Determine number of workers (use 1 worker if only 1 test)
	numWorkers := min(len(runnableTests), r.workers)

	r.logger.Log(context.Background(), logging.LevelTrace, "Starting test execution",
		"total_tests", len(runnableTests),
		"skipped_tests", results.SkippedTests,
		"workers", numWorkers)

	// Create channels for work distribution
	testChan := make(chan testEntry, len(runnableTests))
	resultChan := make(chan TestResult, len(runnableTests))

	// Start worker pool
	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go r.testWorker(ctx, i, testChan, resultChan, &wg)
	}

	// Send tests to workers
	for name := range runnableTests {
		testChan <- testEntry{name: name, test: runnableTests[name]}
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

	r.logger.Debug("Test run completed",
		"total", results.TotalTests,
		"passed", results.PassedTests,
		"failed", results.FailedTests,
		"skipped", results.SkippedTests,
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
					RenderError: fmt.Sprintf("creating test temp directory: %v", err),
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

// shouldSkipTest checks whether a test should be skipped based on version requirements.
// Returns the skip reason if the test should be skipped, or empty string if it should run.
func (r *Runner) shouldSkipTest(test *config.ValidationTest) string {
	if test.MinHAProxyVersion == "" {
		return ""
	}

	if r.haproxyVersion == nil {
		return fmt.Sprintf("requires HAProxy >= %s but version is unknown", test.MinHAProxyVersion)
	}

	minVersion, err := dataplane.ParseVersionString(test.MinHAProxyVersion)
	if err != nil {
		r.logger.Warn("Invalid min_haproxy_version, running test anyway",
			"min_haproxy_version", test.MinHAProxyVersion,
			"error", err)
		return ""
	}

	if r.haproxyVersion.Compare(minVersion) < 0 {
		return fmt.Sprintf("requires HAProxy >= %s (detected %s)", test.MinHAProxyVersion, r.haproxyVersion.Full)
	}

	return ""
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
	fixtureStores, err := r.CreateStoresFromFixtures(fixtures)
	if err != nil {
		result.Passed = false
		result.RenderError = fmt.Sprintf("creating fixture stores: %v", err)
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
	currentConfig, parseErr := r.parseCurrentConfig(testName, test.CurrentConfig)
	if parseErr != "" {
		result.Passed = false
		result.RenderError = parseErr
		result.Duration = time.Since(startTime)
		return result
	}

	// 5. Render HAProxy configuration and auxiliary files (using worker-specific engine)
	rendered, err := r.renderWithStores(engine, fixtureStores, validationPaths, httpStore, currentConfig, test.ExtraContext)
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
		result.RenderedConfig = rendered.HAProxyConfig
		r.storeAuxiliaryFiles(&result, rendered.AuxiliaryFiles)
		if len(rendered.K8sResources) > 0 {
			result.RenderedK8sResources = rendered.K8sResources
		}
		if len(rendered.StatusPatches) > 0 {
			result.RenderedStatusPatches = rendered.StatusPatches
		}
		// Store include stats for --profile-includes flag
		result.IncludeStats = rendered.IncludeStats
	}

	// 6. Build template context for JSONPath assertions
	templateContext := r.buildRenderingContext(fixtureStores, validationPaths, httpStore, currentConfig)

	// 7. Create render dependencies for deterministic assertion (if needed)
	renderDeps := &RenderDependencies{
		Engine:          engine,
		Stores:          fixtureStores,
		ValidationPaths: validationPaths,
		HTTPStore:       httpStore,
		CurrentConfig:   currentConfig,
		ExtraContext:    test.ExtraContext,
	}

	// 8. Run all assertions (whether rendering succeeded or failed)
	r.executeAssertions(ctx, &result, test, rendered.HAProxyConfig, rendered.AuxiliaryFiles, rendered.K8sResources, rendered.StatusPatches, templateContext, validationPaths, renderDeps)

	// Test passes if either:
	// - Rendering succeeded AND all assertions passed
	// - Rendering failed BUT test has rendering_error assertions that passed
	if result.RenderError != "" && !hasRenderingErrorAssertions(test.Assertions) {
		result.Passed = false
	}

	result.Duration = time.Since(startTime)
	return result
}

// parseCurrentConfig parses the optional `currentConfig` block from a test
// definition. Returns the parsed config (or nil if the test didn't supply
// one) and a non-empty error message string when parsing failed; the message
// is intended to land directly in TestResult.RenderError so the caller can
// stop with a uniform short-circuit shape.
func (r *Runner) parseCurrentConfig(testName, raw string) (cfg *parserconfig.StructuredConfig, errMsg string) {
	if raw == "" {
		return nil, ""
	}
	p, err := parser.New()
	if err != nil {
		return nil, fmt.Sprintf("creating parser for currentConfig: %v", err)
	}
	cfg, err = p.ParseFromString(raw)
	if err != nil {
		return nil, fmt.Sprintf("parsing currentConfig: %v", err)
	}
	r.logger.Log(context.Background(), logging.LevelTrace, "Parsed currentConfig for test",
		"test", testName,
		"backends", len(cfg.Backends))
	return cfg, ""
}

// storeAuxiliaryFiles stores rendered auxiliary files in the test result for --dump-rendered flag.
func (r *Runner) storeAuxiliaryFiles(result *TestResult, auxiliaryFiles *dataplane.AuxiliaryFiles) {
	if auxiliaryFiles == nil {
		return
	}

	result.RenderedMaps = collectByKey(auxiliaryFiles.MapFiles,
		func(m auxiliaryfiles.MapFile) string { return m.Path },
		func(m auxiliaryfiles.MapFile) string { return m.Content })
	result.RenderedFiles = collectByKey(auxiliaryFiles.GeneralFiles,
		func(f auxiliaryfiles.GeneralFile) string { return f.Filename },
		func(f auxiliaryfiles.GeneralFile) string { return f.Content })
	result.RenderedCerts = collectByKey(auxiliaryFiles.SSLCertificates,
		func(c auxiliaryfiles.SSLCertificate) string { return c.Path },
		func(c auxiliaryfiles.SSLCertificate) string { return c.Content })
}

// collectByKey returns a fresh map keyed by key(item) with values content(item),
// or nil for empty input so callers can leave their pointer fields unset rather
// than holding empty maps. The three RenderedMaps/RenderedFiles/RenderedCerts
// fields all share this "iterate, key by one field, value by another" shape.
func collectByKey[T any](items []T, key, content func(T) string) map[string]string {
	if len(items) == 0 {
		return nil
	}
	out := make(map[string]string, len(items))
	for _, item := range items {
		out[key(item)] = content(item)
	}
	return out
}
