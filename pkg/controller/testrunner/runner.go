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
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/logging"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
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
		// GOMAXPROCS, not NumCPU: inside the controller's pod NumCPU reports the
		// node's cores, so a CPU-limited pod ran that many workers against a
		// fraction of one and the suite missed its budget, failing the load gate.
		workers = runtime.GOMAXPROCS(0)
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
		checkWithoutBinary: options.CheckWithoutBinary,
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
		// "_global" carries shared fixtures AND a shared extraContext baseline
		// merged into every test (see the _global lookup in runSingleTest); it
		// is never executed as a standalone test. The benchmark path excludes it
		// the same way (cmd/haptic/benchmark.go).
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

	// A gate the workers share so their `haproxy -c` runs go wide instead of
	// serializing behind dataplane's single-slot default gate. Bound concurrent
	// checks by the CPU allocation (GOMAXPROCS, which automaxprocs sets from the
	// cgroup limit) so a CPU-limited controller pod doesn't oversubscribe with
	// haproxy subprocesses during the startup/reinit load gate.
	gateSlots := numWorkers
	if p := runtime.GOMAXPROCS(0); p > 0 && p < gateSlots {
		gateSlots = p
	}
	r.checkGate = dataplane.NewCheckGateN(gateSlots, 0)

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
			result, incomplete := r.runSingleTest(ctx, entry.name, &entry.test, testEngine, testPaths)

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

			if !incomplete || !result.Passed {
				results <- result
			}
			if incomplete {
				return
			}

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
func (r *Runner) runSingleTest(ctx context.Context, testName string, test *config.ValidationTest, engine templating.Engine, validationPaths *dataplane.ValidationPaths) (TestResult, bool) {
	startTime := time.Now()

	result := TestResult{
		TestName:    testName,
		Description: test.Description,
		Passed:      true,
		Assertions:  make([]AssertionResult, 0),
	}

	// 1.-4. Fixture stores, HTTP fixtures and the previously-deployed servers
	inputs, inputErr := r.renderInputs(testName, test)
	if inputErr != "" {
		result.Passed = false
		result.RenderError = inputErr
		result.Duration = time.Since(startTime)
		return result, false
	}
	fixtureStores, httpStore := inputs.Stores, inputs.HTTPStore
	currentConfig, effectiveExtraContext := inputs.CurrentConfig, inputs.ExtraContext

	// 5. Render HAProxy configuration and auxiliary files (using worker-specific engine)
	rendered, err := r.renderWithStores(ctx, engine, fixtureStores, validationPaths, httpStore, currentConfig, test.CurrentFiles, effectiveExtraContext)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil && errors.Is(err, ctxErr) {
			result.Duration = time.Since(startTime)
			return result, true
		}
		recordRenderFailure(&result, err)
		if isResourceInputError(err) {
			result.Passed = false
			result.Duration = time.Since(startTime)
			return result, false
		}
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
	templateContext := r.buildRenderingContext(ctx, fixtureStores, validationPaths, httpStore, currentConfig, test.CurrentFiles).Context

	// 7. Create render dependencies for deterministic assertion (if needed)
	renderDeps := &RenderDependencies{
		Engine:          engine,
		Stores:          fixtureStores,
		ValidationPaths: validationPaths,
		HTTPStore:       httpStore,
		CurrentConfig:   currentConfig,
		CurrentFiles:    test.CurrentFiles,
		// effectiveExtraContext (not test.ExtraContext): the deterministic
		// assertion re-renders through this, and must use the same _global-merged
		// baseline as the first render — otherwise the second render loses the
		// _global default-cert pin and diverges (or fails) against production.
		ExtraContext: effectiveExtraContext,
	}

	// 8. Run all assertions (whether rendering succeeded or failed)
	incomplete := r.executeAssertions(ctx, &result, test, rendered.HAProxyConfig, rendered.AuxiliaryFiles, rendered.K8sResources, rendered.StatusPatches, rendered.Events, templateContext, validationPaths, renderDeps)

	// Test passes if either:
	// - Rendering succeeded AND all assertions passed
	// - Rendering failed BUT test has rendering_error assertions that passed
	if result.RenderError != "" && !hasRenderingErrorAssertions(test.Assertions) {
		result.Passed = false
	}

	result.Duration = time.Since(startTime)
	return result, incomplete
}

func recordRenderFailure(result *TestResult, err error) {
	result.RenderError = dataplane.SimplifyRenderingError(err)
	result.Assertions = append(result.Assertions, AssertionResult{
		Type:        "rendering",
		Description: "Template rendering failed",
		Passed:      false,
		Error:       result.RenderError,
	})
}

func isResourceInputError(err error) bool {
	var resourceInputErr *rendercontext.ResourceInputError
	return errors.As(err, &resourceInputErr)
}

// renderInput bundles everything a test's render reads besides the templates.
type renderInput struct {
	Stores        map[string]stores.Store
	HTTPStore     *FixtureHTTPStoreWrapper
	CurrentConfig *renderplan.CurrentConfig
	ExtraContext  map[string]any
}

// renderInputs assembles a test's render inputs: the _global-merged fixture
// stores and HTTP fixtures, the previously-deployed servers, and the
// extraContext baseline. Returns a non-empty message when an input is
// unusable — the message lands in TestResult.RenderError.
//
// _global contributes a shared extraContext baseline: the isolated, synthetic
// values every test renders against (e.g. a default SSL cert decoupled from
// the operator's real defaultSSLCertificate.*). Per-test extraContext overrides
// this baseline, and mergeTestExtraContext later folds the result over the
// deployment's production extraContext — so what a synthetic test resolves is
// the _global pin, never the operator's real secret names. Without this, a
// custom default-cert name leaks into every test and fails the fixture-store
// lookup (crash-looping the load gate).
func (r *Runner) renderInputs(testName string, test *config.ValidationTest) (inputs renderInput, failure string) {
	fixtures := test.Fixtures
	httpFixtures := test.HTTPFixtures
	if globalTest, hasGlobal := r.config.ValidationTests["_global"]; hasGlobal {
		fixtures = MergeFixtures(globalTest.Fixtures, test.Fixtures)
		httpFixtures = MergeHTTPFixtures(globalTest.HTTPFixtures, test.HTTPFixtures)
	}

	fixtureStores, err := r.CreateStoresFromFixtures(fixtures)
	if err != nil {
		return renderInput{}, fmt.Sprintf("creating fixture stores: %v", err)
	}

	// The wrapper is always created so http.Fetch() fails gracefully when a
	// fixture is missing.
	httpStore := NewFixtureHTTPStoreWrapper(CreateHTTPStoreFromFixtures(httpFixtures, r.logger), r.logger)

	r.logger.Log(context.Background(), logging.LevelTrace, "Assembled render inputs",
		"test", testName,
		"fixture_types", len(fixtures),
		"http_fixtures", len(httpFixtures))

	return renderInput{
		Stores:        fixtureStores,
		HTTPStore:     httpStore,
		CurrentConfig: r.currentServers(test),
		ExtraContext:  foldGlobalExtraContext(r.config, test.ExtraContext),
	}, ""
}

// Render renders one validation test and returns everything the render
// produced — including the plan the templates declared — without executing the
// test's assertions. It renders into the runner's base validation paths, which
// nothing writes to while no assertion runs.
func (r *Runner) Render(ctx context.Context, testName string) (RenderOutput, error) {
	test, found := r.config.ValidationTests[testName]
	if !found {
		return RenderOutput{}, fmt.Errorf("test %q not found", testName)
	}
	inputs, inputErr := r.renderInputs(testName, &test)
	if inputErr != "" {
		return RenderOutput{}, errors.New(inputErr)
	}
	return r.renderWithStores(ctx, r.engineTemplate, inputs.Stores, r.validationPaths,
		inputs.HTTPStore, inputs.CurrentConfig, test.CurrentFiles, inputs.ExtraContext)
}

// RenderWithoutFixtures renders the configuration against empty stores: no
// watched resource exists, no HTTP fixture answers, nothing was deployed
// before. It is the configuration's own skeleton — what `haptic diff` compares
// when the operator names no validationTest, so the answer is about the
// configuration change rather than about one test's fixtures.
func (r *Runner) RenderWithoutFixtures(ctx context.Context) (RenderOutput, error) {
	httpStore := NewFixtureHTTPStoreWrapper(CreateHTTPStoreFromFixtures(nil, r.logger), r.logger)
	return r.renderWithStores(ctx, r.engineTemplate, r.createEmptyStores(), r.validationPaths,
		httpStore, nil, nil, nil)
}

// currentServers resolves what a test declares about the previous deployment
// into the shape templates read as `currentConfig`. Returns nil when the test
// declares nothing.
func (r *Runner) currentServers(test *config.ValidationTest) *renderplan.CurrentConfig {
	if len(test.CurrentServers) == 0 {
		return nil
	}
	return currentConfigFromFixture(test.CurrentServers)
}

// currentConfigFromFixture projects the `currentServers` fixture into the
// template-facing shape.
func currentConfigFromFixture(fixture map[string]map[string]config.ServerAddr) *renderplan.CurrentConfig {
	index := make(map[string]map[string]renderplan.ServerAddr, len(fixture))
	for backend, servers := range fixture {
		addresses := make(map[string]renderplan.ServerAddr, len(servers))
		for name := range servers {
			addresses[name] = renderplan.ServerAddr{Address: servers[name].Address, Port: servers[name].Port}
		}
		index[backend] = addresses
	}
	return &renderplan.CurrentConfig{ServerIndex: index}
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
