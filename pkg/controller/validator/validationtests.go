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

package validator

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/names"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testrunner"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// validationTestsBootstrapTimeout caps the wall-clock cost of schema
// resolution during validationTests validation, mirroring
// templateValidatorBootstrapTimeout. The deadline only fires when the cluster
// is degraded — at which point validation correctly fails with a clear
// "schema acquisition failed" reason.
const validationTestsBootstrapTimeout = 5 * time.Second

// validationTestsRunTimeout is the test-execution budget: a suite may take up
// to this long before the gate gives up. It bounds a slow (or, pathologically,
// wedged) suite so it can neither run orphaned past the scatter-gather deadline
// nor permanently wedge this validator's event loop. This is the *binding*
// suite limit; the configchange scatter-gather budget
// (configValidationTimeout) is deliberately set strictly larger so that —
// accounting for the bootstrap (≤5s) and engine/paths setup — the validator
// always self-reports its result (pass, or "invalid: did not complete") BEFORE
// the coordinator gives up on it (which would otherwise read as a missing
// responder). The runner checks ctx between tests, so a slow-but-progressing
// suite stops cleanly at the boundary. Note: a single `haproxy -c` is not itself
// cancellable (the shared dataplane validation path takes no context), but those
// checks are sub-second, so the bound holds in practice.
const validationTestsRunTimeout = 25 * time.Second

// maxReportedFailures bounds how many failed-test names are folded into the
// validation response so a config with a broken shared snippet (which can fail
// hundreds of tests at once) produces an actionable, not overwhelming, message.
const maxReportedFailures = 10

// ValidationTestsValidator runs the config's embedded validationTests — the
// exact suite the `controller validate` CLI runs — as a scatter-gather
// validator, so the running controller never accepts (at startup or on a live
// change) a config whose tests fail. On failure the config-validation
// aggregation publishes ConfigInvalidEvent and the last-good config keeps
// serving; at startup the controller stays unready until a passing config is
// present.
//
// It builds a throwaway engine from the candidate config (using the same
// live-schema TypeBootstrapper the TemplateValidator uses, so typed access in
// tests compiles identically to production) plus a temporary HAProxy
// validation tree for `haproxy_valid` assertions, runs the suite, and reports
// the outcome. This runs only on config load/change — never on the resource
// reconciliation hot path.
type ValidationTestsValidator struct {
	*BaseValidator
	eventBus  *busevents.EventBus
	logger    *slog.Logger
	bootstrap TypeBootstrapper
	// runTimeout bounds the test-execution phase. Defaults to
	// validationTestsRunTimeout; overridable in tests to exercise the
	// fail-closed-on-timeout path without waiting the full budget.
	runTimeout time.Duration

	// lifecycleCtx is the iteration/shutdown context captured in Start, so the
	// (potentially multi-second) bootstrap + test run abort promptly on
	// controller shutdown instead of running to their own timeout. component.Base
	// doesn't pass a context to HandleEvent, so we capture it here rather than
	// changing the shared validator interface.
	mu           sync.RWMutex
	lifecycleCtx context.Context
}

// NewValidationTestsValidator creates the validationTests validator.
//
// bootstrap MUST be non-nil — without real typed reflect.Types the engine
// compile (and therefore any test exercising typed Spec/Status access) would
// false-positively fail. Production passes a closure around
// controller.runTypeBootstrap; tests pass a stub returning an in-memory Result.
func NewValidationTestsValidator(eventBus *busevents.EventBus, logger *slog.Logger, bootstrap TypeBootstrapper) *ValidationTestsValidator {
	if bootstrap == nil {
		panic("validator: NewValidationTestsValidator requires non-nil TypeBootstrapper " +
			"— without real schemas, validationTests using typed access would be " +
			"false-positively rejected")
	}
	v := &ValidationTestsValidator{
		eventBus:   eventBus,
		logger:     logger,
		bootstrap:  bootstrap,
		runTimeout: validationTestsRunTimeout,
	}
	v.BaseValidator = NewBaseValidator(eventBus, logger, ValidatorNameValidationTests, v)
	return v
}

// Start captures the lifecycle context (so an in-flight bootstrap/run is
// cancelled on shutdown) and then runs the embedded validator event loop.
func (v *ValidationTestsValidator) Start(ctx context.Context) error {
	v.mu.Lock()
	v.lifecycleCtx = ctx
	v.mu.Unlock()
	return v.BaseValidator.Start(ctx)
}

// baseCtx returns the captured lifecycle context, or Background if Start hasn't
// run yet (e.g. in unit tests that call HandleRequest directly).
func (v *ValidationTestsValidator) baseCtx() context.Context {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if v.lifecycleCtx != nil {
		return v.lifecycleCtx
	}
	return context.Background()
}

// HandleRequest runs the embedded validationTests for the candidate config and
// publishes a ConfigValidationResponse. A config with no validationTests is a
// no-op pass (the gate adds zero cost when the chart ships no tests).
func (v *ValidationTestsValidator) HandleRequest(req *events.ConfigValidationRequest) {
	start := time.Now()

	cfg, ok := v.assertConfigType(req)
	if !ok {
		return
	}

	if len(cfg.ValidationTests) == 0 {
		v.respond(req, true, nil)
		return
	}

	v.logger.Debug("Running validationTests", "version", req.Version, "test_count", len(cfg.ValidationTests))

	results, err := v.runTests(cfg)
	if err != nil {
		v.logger.Error("validationTests could not run",
			"version", req.Version, "error", err)
		v.respond(req, false, []string{err.Error()})
		return
	}

	duration := time.Since(start)
	if results.FailedTests == 0 {
		v.logger.Debug("validationTests passed",
			"version", req.Version,
			"total", results.TotalTests,
			"skipped", results.SkippedTests,
			"duration_ms", duration.Milliseconds())
		v.respond(req, true, nil)
		return
	}

	failures := summarizeTestFailures(results)
	v.logger.Error("validationTests failed",
		"version", req.Version,
		"failed", results.FailedTests,
		"total", results.TotalTests,
		"duration_ms", duration.Milliseconds(),
		"failures", failures)
	v.respond(req, false, failures)
}

// runTests builds the engine + temporary validation tree and executes the
// suite. Any setup error is returned so the caller can fail validation with a
// clear reason rather than silently skipping the gate.
func (v *ValidationTestsValidator) runTests(cfg *coreconfig.Config) (*testrunner.TestResults, error) {
	bootstrapCtx, cancel := context.WithTimeout(v.baseCtx(), validationTestsBootstrapTimeout)
	defer cancel()
	bootstrapResult, err := v.bootstrap(bootstrapCtx, cfg)
	if err != nil {
		return nil, fmt.Errorf("schema acquisition failed (typed validationTests cannot run without real schemas): %w", err)
	}

	engine, err := helpers.NewEngineFromConfigWithOptions(
		cfg, nil, nil,
		helpers.BuildAdditionalDeclarations(cfg, bootstrapResult),
		helpers.EngineOptions{},
	)
	if err != nil {
		return nil, fmt.Errorf("building validation engine: %w", err)
	}

	paths, capabilities, haproxyVersion, cleanup, err := buildTestValidationPaths(cfg)
	if err != nil {
		return nil, fmt.Errorf("preparing HAProxy validation environment: %w", err)
	}
	defer cleanup()

	runner := testrunner.New(cfg, engine, paths, &testrunner.Options{
		Logger:             v.logger,
		Capabilities:       capabilities,
		HAProxyVersion:     haproxyVersion,
		TypedResourceTypes: bootstrapResult.Types,
	})

	// Bound the run so a slow/wedged suite can't run orphaned past the
	// scatter-gather deadline or wedge this validator's event loop (see
	// validationTestsRunTimeout).
	runCtx, cancel := context.WithTimeout(v.baseCtx(), v.runTimeout)
	defer cancel()
	results, err := runner.RunTests(runCtx, "")
	if err != nil {
		return nil, fmt.Errorf("executing validationTests: %w", err)
	}

	// CRITICAL: RunTests does NOT report context cancellation as an error. On
	// timeout its workers stop early and the un-run tests are silently absent
	// from the results — counted in neither PassedTests nor FailedTests. So a
	// timed-out run looks like "0 failures" and would be fail-OPEN (accepting a
	// config we never finished validating). Treat any incomplete run as a hard
	// rejection: fail-closed is the whole point of the gate.
	if runCtx.Err() != nil {
		return nil, fmt.Errorf(
			"validationTests did not complete within %s — config rejected to avoid accepting a partially-validated config: %w",
			v.runTimeout, runCtx.Err())
	}

	return results, nil
}

// respond publishes the validator's ConfigValidationResponse.
func (v *ValidationTestsValidator) respond(req *events.ConfigValidationRequest, valid bool, errs []string) {
	v.eventBus.Publish(events.NewConfigValidationResponse(
		req.RequestID(),
		ValidatorNameValidationTests,
		valid,
		errs,
	))
}

// summarizeTestFailures turns the failed per-test results into a bounded,
// human-readable error list for the ConfigValidationResponse.
func summarizeTestFailures(results *testrunner.TestResults) []string {
	out := make([]string, 0, maxReportedFailures+1)
	reported := 0
	for i := range results.TestResults {
		tr := &results.TestResults[i]
		if tr.Passed || tr.Skipped {
			continue
		}
		if reported >= maxReportedFailures {
			out = append(out, fmt.Sprintf("... and %d more failing test(s)", results.FailedTests-reported))
			break
		}
		out = append(out, fmt.Sprintf("validationTest %q failed: %s", tr.TestName, firstFailureReason(tr)))
		reported++
	}
	if len(out) == 0 {
		// Defensive: FailedTests>0 but no per-test detail surfaced.
		out = append(out, fmt.Sprintf("%d validationTest(s) failed", results.FailedTests))
	}
	return out
}

// buildTestValidationPaths creates a temporary HAProxy validation tree mirroring
// the production layout (maps/ssl/general dirs + config-file path) so the
// testrunner's `haproxy_valid` assertions can run `haproxy -c`. The returned
// cleanup removes the temp tree. Mirrors cmd/controller's setupValidationPaths
// but operates on the already-converted config.
func buildTestValidationPaths(cfg *coreconfig.Config) (
	paths *dataplane.ValidationPaths,
	capabilities dataplane.Capabilities,
	haproxyVersion *dataplane.Version,
	cleanup func(),
	err error,
) {
	localVersion, err := dataplane.DetectLocalVersion()
	if err != nil {
		return nil, dataplane.Capabilities{}, nil, nil, fmt.Errorf("detecting local HAProxy version: %w", err)
	}
	capabilities = dataplane.CapabilitiesFromVersion(localVersion)

	tempDir, err := os.MkdirTemp("", "haproxy-cfgtest-*")
	if err != nil {
		return nil, dataplane.Capabilities{}, nil, nil, fmt.Errorf("creating temp dir: %w", err)
	}

	basePaths := dataplane.PathConfig{
		MapsDir:    filepath.Join(tempDir, filepath.Base(cfg.Dataplane.MapsDir)),
		SSLDir:     filepath.Join(tempDir, filepath.Base(cfg.Dataplane.SSLCertsDir)),
		GeneralDir: filepath.Join(tempDir, filepath.Base(cfg.Dataplane.GeneralStorageDir)),
		ConfigFile: filepath.Join(tempDir, names.MainTemplateName),
	}
	resolvedPaths := dataplane.ResolvePaths(basePaths, capabilities)

	dirsToCreate := []string{resolvedPaths.MapsDir, resolvedPaths.SSLDir, resolvedPaths.GeneralDir}
	if resolvedPaths.CRTListDir != resolvedPaths.SSLDir && resolvedPaths.CRTListDir != resolvedPaths.GeneralDir {
		dirsToCreate = append(dirsToCreate, resolvedPaths.CRTListDir)
	}
	for _, dir := range dirsToCreate {
		if mkErr := os.MkdirAll(dir, 0o750); mkErr != nil {
			_ = os.RemoveAll(tempDir)
			return nil, dataplane.Capabilities{}, nil, nil, fmt.Errorf("creating directory: %w", mkErr)
		}
	}

	cleanup = func() { _ = os.RemoveAll(tempDir) }
	return resolvedPaths.ToValidationPaths(), capabilities, localVersion, cleanup, nil
}

// firstFailureReason extracts a short reason from a failed test result, joining
// any per-assertion error messages (plus a render error, if the test failed
// because rendering itself errored).
func firstFailureReason(tr *testrunner.TestResult) string {
	reasons := assertionFailureMessages(tr)
	if len(reasons) == 0 {
		return "no assertion detail reported"
	}
	return strings.Join(reasons, "; ")
}

// assertionFailureMessages collects the failure messages from a failed test:
// the render error (if any) followed by each failed assertion's message.
func assertionFailureMessages(tr *testrunner.TestResult) []string {
	var msgs []string
	if tr.RenderError != "" {
		msgs = append(msgs, "render error: "+tr.RenderError)
	}
	for i := range tr.Assertions {
		a := &tr.Assertions[i]
		if a.Passed {
			continue
		}
		msg := a.Error
		if msg == "" {
			msg = a.Type + " assertion failed"
		}
		msgs = append(msgs, msg)
	}
	return msgs
}
