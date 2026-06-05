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
const validationTestsRunTimeout = 25 * time.Second

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

	result, err := v.runTests(cfg)
	if err != nil {
		v.logger.Error("validationTests could not run",
			"version", req.Version, "error", err)
		v.respond(req, false, []string{err.Error()})
		return
	}

	duration := time.Since(start)
	switch {
	case result.Incomplete:
		// Daemon load gate fails CLOSED on an incomplete run: never accept a
		// config we didn't finish validating.
		v.logger.Error("validationTests did not complete in time",
			"version", req.Version, "run_timeout", v.runTimeout, "duration_ms", duration.Milliseconds())
		v.respond(req, false, []string{fmt.Sprintf(
			"validationTests did not complete within %s — config rejected to avoid accepting a partially-validated config", v.runTimeout)})
	case result.Passed:
		v.logger.Debug("validationTests passed",
			"version", req.Version, "duration_ms", duration.Milliseconds())
		v.respond(req, true, nil)
	default:
		v.logger.Error("validationTests failed",
			"version", req.Version, "duration_ms", duration.Milliseconds(), "failures", result.Failures)
		v.respond(req, false, result.Failures)
	}
}

// runTests resolves typed schemas, builds the engine, and runs the suite via the
// shared configtest helper (the same one the admission webhook uses, so the two
// gates can't drift). Any setup error is returned so the caller can fail
// validation with a clear reason rather than silently skipping the gate.
func (v *ValidationTestsValidator) runTests(cfg *coreconfig.Config) (configtest.Result, error) {
	bootstrapCtx, cancel := context.WithTimeout(v.baseCtx(), validationTestsBootstrapTimeout)
	defer cancel()
	bootstrapResult, err := v.bootstrap(bootstrapCtx, cfg)
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

	return configtest.RunValidationTests(v.baseCtx(), cfg, engine, bootstrapResult.Types, v.runTimeout, v.logger)
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
