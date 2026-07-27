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
	"sync"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/configtest"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
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
//
// This default binds only the LIVE change gate. The load-path gate
// (controller.validateInitialConfigValidationTests) passes its own, much
// larger budget: at startup there is no scatter-gather deadline, the outer
// bound is the startup probe, and a cold contended node legitimately needs
// more than 25s for engine compile + the haproxy -c sweep.
const validationTestsRunTimeout = 25 * time.Second

// suitePerTestBudget is the per-test increment SuiteRunBudget adds above the
// floor for large suites. ~100ms comfortably covers one test's engine render +
// `haproxy -c` on a contended CI node (observed: the chart's 362-test suite
// needs 26-28s under 4-shard contention ≈ 75ms/test).
const suitePerTestBudget = 100 * time.Millisecond

// SuiteRunBudget returns the live-gate test-execution budget for a suite of
// the given size: the validationTestsRunTimeout floor PLUS suitePerTestBudget
// per test. A fixed 25s cap rejected the chart's all-passing 362-test suite on
// contended CI nodes (issue #77: 362/362 passed in 27.8s, cancelled at 25s,
// config rejected as "partially-validated"). Scaling with suite size keeps
// small suites on a tight bound while a legitimately large suite gets time
// proportional to its work. Exported so configchange can derive its
// scatter-gather envelope from the SAME formula — the envelope must stay
// strictly larger than this budget (see SuiteValidationEnvelope).
//
// The floor is ADDED, not max()'d. Taking the larger of the two pinned every
// suite below 250 tests to exactly 25s, so the floor — chosen as headroom for
// a *small* suite on a cold node — silently became the whole budget for a
// suite doing ten times the work. That reintroduced #77's false rejection at
// the crossover: the chart's ~249-test effective suite (post-`requires`
// stripping on a cluster without Gateway CRDs) needed 27.3s against a budget
// that computed to 24.9s and therefore clamped to the 25s floor, and a
// perfectly good default config was rejected as partially-validated. Adding
// the floor is also what the doc comment on suitePerTestBudget and the
// shipped changelog ("25s floor + ~100ms per test") always described.
func SuiteRunBudget(testCount int) time.Duration {
	return validationTestsRunTimeout + time.Duration(testCount)*suitePerTestBudget
}

// suiteEnvelopeMargin is the fixed part of what SuiteValidationEnvelope adds
// on top of the run budget: bootstrap (≤5s) + base engine setup + 10s slack —
// preserving the 45s = 25s + 20s relationship the fixed constants had for
// small suites.
const suiteEnvelopeMargin = 20 * time.Second

// suitePerTestSetup is the per-test envelope allowance for the parts of the
// validator's wall time OUTSIDE the ctx-bounded run: engine compilation of
// each test's declarations grows with suite size and is not cancellable, so
// a fixed margin alone would let a huge suite eat the slack and miss the
// coordinator's deadline — the exact false-rejection this formula exists to
// prevent. Compilation is far cheaper than execution, hence 20ms vs the
// 100ms run increment.
const suitePerTestSetup = 20 * time.Millisecond

// SuiteValidationEnvelope returns the scatter-gather timeout for validating a
// config with the given suite size. Every component that grows with the suite
// (run budget, engine compile) scales, so the envelope is strictly larger
// than the validator's worst-case wall time for every testCount and the
// validationtests validator always self-reports (pass, or "did not complete")
// before the coordinator declares it a missing responder.
func SuiteValidationEnvelope(testCount int) time.Duration {
	return SuiteRunBudget(testCount) + suiteEnvelopeMargin + time.Duration(testCount)*suitePerTestSetup
}

// ValidationTestsValidator runs the config's embedded validationTests — the
// exact suite the `controller validate` CLI runs — as a scatter-gather
// validator. It guards live config changes: when a CRD update's tests fail the
// aggregation publishes ConfigInvalidEvent and the last-good config keeps
// serving.
//
// The load path is guarded separately by the same suite: controller.runIteration
// calls RunValidationTestsSync on the initial config and crash-loops the pod if
// it fails (see that function and the startup gate in iteration.go). Both gates
// share RunValidationTestsSync so a config's tests behave identically whether
// they run on a live change or at load — the controller never serves a config
// whose own tests fail.
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
	// budgetFor returns the test-execution budget for a suite of the given
	// size. Defaults to SuiteRunBudget (a 25s floor scaled by suite size, so a
	// large all-passing suite isn't rejected on a contended node — issue #77);
	// overridable in tests to exercise the fail-closed-on-timeout path without
	// waiting a real budget.
	budgetFor func(testCount int) time.Duration

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
		eventBus:  eventBus,
		logger:    logger,
		bootstrap: bootstrap,
		budgetFor: SuiteRunBudget,
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

	budget := v.budgetFor(len(cfg.ValidationTests))
	v.logger.Debug("Running validationTests",
		"version", req.Version, "test_count", len(cfg.ValidationTests), "run_budget", budget)

	result, err := v.runTests(cfg, budget)
	if err != nil {
		v.logger.Error("ValidationTests could not run",
			"version", req.Version, "error", err)
		v.respond(req, false, []string{err.Error()})
		return
	}

	duration := time.Since(start)
	switch {
	case result.Incomplete:
		// Daemon load gate fails CLOSED on an incomplete run: never accept a
		// config we didn't finish validating.
		v.logger.Error("ValidationTests did not complete in time",
			"version", req.Version, "run_budget", budget, "duration_ms", duration.Milliseconds())
		v.respond(req, false, []string{fmt.Sprintf(
			"validationTests did not complete within %s — config rejected to avoid accepting a partially-validated config", budget)})
	case result.Passed:
		v.logger.Debug("ValidationTests passed",
			"version", req.Version, "duration_ms", duration.Milliseconds())
		v.respond(req, true, nil)
	default:
		v.logger.Error("ValidationTests failed",
			"version", req.Version, "duration_ms", duration.Milliseconds(), "failures", result.Failures)
		v.respond(req, false, result.Failures)
	}
}

// runTests delegates to RunValidationTestsSync with this validator's bootstrap,
// the suite-size-scaled run budget, and lifecycle context.
func (v *ValidationTestsValidator) runTests(cfg *coreconfig.Config, budget time.Duration) (configtest.Result, error) {
	return RunValidationTestsSync(v.baseCtx(), cfg, v.bootstrap, budget, v.logger)
}

// RunValidationTestsSync resolves typed schemas, builds a throwaway engine, and
// runs the config's embedded validationTests via the shared configtest helper
// (the same one the admission webhook uses, so the gates can't drift). It is the
// shared core behind both the live scatter-gather gate (the validator's
// HandleRequest) and the startup load gate (controller.runIteration) — so a
// config's tests are bootstrapped, compiled, and executed identically whether
// they run on a live change or at controller load.
//
// A config with no validationTests is a zero-cost pass. A runTimeout <= 0 uses
// the default suite budget (validationTestsRunTimeout). Any setup error is
// returned so the caller can fail validation with a clear reason rather than
// silently skipping the gate.
func RunValidationTestsSync(ctx context.Context, cfg *coreconfig.Config, bootstrap TypeBootstrapper, runTimeout time.Duration, logger *slog.Logger) (configtest.Result, error) {
	if len(cfg.ValidationTests) == 0 {
		return configtest.Result{Passed: true}, nil
	}
	if runTimeout <= 0 {
		runTimeout = validationTestsRunTimeout
	}

	bootstrapCtx, cancel := context.WithTimeout(ctx, validationTestsBootstrapTimeout)
	defer cancel()
	bootstrapResult, err := bootstrap(bootstrapCtx, cfg)
	if err != nil {
		return configtest.Result{}, fmt.Errorf("schema acquisition failed (typed validationTests cannot run without real schemas): %w", err)
	}

	engine, err := helpers.NewEngineFromConfigWithOptions(
		cfg, nil, nil,
		helpers.BuildAdditionalDeclarations(cfg, bootstrapResult),
		helpers.EngineOptions{},
	)
	if err != nil {
		return configtest.Result{}, fmt.Errorf("building validation engine: %w", err)
	}

	return configtest.RunValidationTests(ctx, cfg, engine, bootstrapResult.Types, runTimeout, logger)
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
