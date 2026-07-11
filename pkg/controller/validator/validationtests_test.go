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
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// configWithValidationTest builds a minimal valid config carrying a single
// `contains` validationTest asserting that the rendered haproxy.cfg matches
// pattern. A pattern the render produces ⇒ the test passes; one it doesn't ⇒
// the test (and therefore the gate) fails.
func configWithValidationTest(pattern string) *coreconfig.Config {
	cfg := &coreconfig.Config{
		PodSelector: coreconfig.PodSelector{MatchLabels: map[string]string{"app": "haproxy"}},
		Logging:     coreconfig.LoggingConfig{Level: "INFO"},
		HAProxyConfig: coreconfig.HAProxyConfig{
			Template: "frontend http\n  bind *:80\n",
		},
		ValidationTests: map[string]coreconfig.ValidationTest{
			"test-frontend-present": {
				Description: "the rendered config contains the http frontend",
				Assertions: []coreconfig.ValidationAssertion{
					{
						Type:        "contains",
						Target:      "haproxy.cfg",
						Pattern:     pattern,
						Description: "frontend present",
					},
				},
			},
		},
	}
	coreconfig.SetDefaults(cfg)
	return cfg
}

// runValidationTestsValidator drives one HandleRequest through the validator and
// returns the published ConfigValidationResponse.
func runValidationTestsValidator(t *testing.T, cfg *coreconfig.Config) *events.ConfigValidationResponse {
	t.Helper()
	return runValidationTestsValidatorWithTimeout(t, cfg, 0)
}

// runValidationTestsValidatorWithTimeout is like runValidationTestsValidator but
// overrides the test-execution timeout (0 = leave the production default).
func runValidationTestsValidatorWithTimeout(t *testing.T, cfg *coreconfig.Config, runTimeout time.Duration) *events.ConfigValidationResponse {
	t.Helper()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	bus := busevents.NewEventBus(16)
	responses := bus.Subscribe("test-collector", 4)
	bus.Start()

	v := NewValidationTestsValidator(bus, logger, stubTypeBootstrapper)
	if runTimeout > 0 {
		// Fixed override bypassing the suite-size scaling, so the
		// fail-closed-on-timeout path is exercisable with a tiny budget.
		v.budgetFor = func(int) time.Duration { return runTimeout }
	}
	v.HandleRequest(events.NewConfigValidationRequest(cfg, "v-test"))

	select {
	case ev := <-responses:
		resp, ok := ev.(*events.ConfigValidationResponse)
		if !ok {
			t.Fatalf("expected *ConfigValidationResponse, got %T", ev)
		}
		if resp.ValidatorName != ValidatorNameValidationTests {
			t.Fatalf("expected responder %q, got %q", ValidatorNameValidationTests, resp.ValidatorName)
		}
		return resp
	case <-time.After(30 * time.Second):
		t.Fatal("timeout waiting for ConfigValidationResponse")
		return nil
	}
}

func TestValidationTestsValidator_PassingTestsAccepted(t *testing.T) {
	resp := runValidationTestsValidator(t, configWithValidationTest("frontend http"))
	if !resp.Valid {
		t.Fatalf("expected config with passing validationTests to be accepted, errors: %v", resp.Errors)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("expected no errors on accept, got: %v", resp.Errors)
	}
}

func TestValidationTestsValidator_FailingTestsRejected(t *testing.T) {
	// The render never emits this string, so the contains assertion fails and
	// the gate must reject the config.
	resp := runValidationTestsValidator(t, configWithValidationTest("this-string-is-never-rendered"))
	if resp.Valid {
		t.Fatal("expected config with a failing validationTest to be rejected")
	}
	if len(resp.Errors) == 0 {
		t.Fatal("expected at least one error describing the failing test")
	}
	// The failing test name must surface so operators know what broke.
	if !strings.Contains(strings.Join(resp.Errors, "\n"), "test-frontend-present") {
		t.Fatalf("expected the failing test name in the errors, got: %v", resp.Errors)
	}
}

func TestValidationTestsValidator_TimeoutFailsClosed(t *testing.T) {
	// A 1ns run budget guarantees the run is cut short before tests complete.
	// RunTests reports no error and "0 failures" in that case, so the validator
	// must NOT mistake an incomplete run for a pass — it must reject (fail-closed).
	resp := runValidationTestsValidatorWithTimeout(t, configWithValidationTest("frontend http"), time.Nanosecond)
	if resp.Valid {
		t.Fatal("expected a timed-out (incomplete) validationTests run to be REJECTED, not accepted")
	}
	if len(resp.Errors) == 0 || !strings.Contains(strings.Join(resp.Errors, "\n"), "did not complete") {
		t.Fatalf("expected a 'did not complete' rejection reason, got: %v", resp.Errors)
	}
}

func TestValidationTestsValidator_NoTestsIsNoOpPass(t *testing.T) {
	cfg := configWithValidationTest("frontend http")
	cfg.ValidationTests = nil
	resp := runValidationTestsValidator(t, cfg)
	if !resp.Valid {
		t.Fatalf("expected config with no validationTests to pass trivially, errors: %v", resp.Errors)
	}
}

// RunValidationTestsSync backs both the live validator and the controller's
// startup load gate; these pin its behaviour directly, independent of the
// event-driven validator.

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestRunValidationTestsSync_Passes(t *testing.T) {
	result, err := RunValidationTestsSync(context.Background(), configWithValidationTest("frontend http"), stubTypeBootstrapper, 0, testLogger())
	if err != nil {
		t.Fatalf("unexpected setup error: %v", err)
	}
	if !result.Passed || result.Incomplete {
		t.Fatalf("expected a passing complete run, got %+v", result)
	}
}

func TestRunValidationTestsSync_Fails(t *testing.T) {
	result, err := RunValidationTestsSync(context.Background(), configWithValidationTest("this-string-is-never-rendered"), stubTypeBootstrapper, 0, testLogger())
	if err != nil {
		t.Fatalf("unexpected setup error: %v", err)
	}
	if result.Passed {
		t.Fatal("expected a failing validationTest to NOT pass")
	}
	if !strings.Contains(strings.Join(result.Failures, "\n"), "test-frontend-present") {
		t.Fatalf("expected the failing test name in the failures, got: %v", result.Failures)
	}
}

func TestRunValidationTestsSync_NoTestsZeroCostPass(t *testing.T) {
	cfg := configWithValidationTest("frontend http")
	cfg.ValidationTests = nil
	// A nil bootstrap proves the no-tests path short-circuits before any setup.
	result, err := RunValidationTestsSync(context.Background(), cfg, nil, 0, testLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Passed {
		t.Fatalf("expected zero-cost pass with no validationTests, got %+v", result)
	}
}

func TestRunValidationTestsSync_BootstrapErrorSurfaces(t *testing.T) {
	boom := func(context.Context, *coreconfig.Config) (*typebootstrap.Result, error) {
		return nil, errors.New("schema server unreachable")
	}
	_, err := RunValidationTestsSync(context.Background(), configWithValidationTest("frontend http"), boom, 0, testLogger())
	if err == nil {
		t.Fatal("expected a setup error when the bootstrap fails")
	}
	if !strings.Contains(err.Error(), "schema acquisition failed") {
		t.Fatalf("expected a wrapped schema-acquisition error, got: %v", err)
	}
}

// TestSuiteRunBudget pins the suite-size scaling (#77): the 25s floor holds
// for small suites, and large suites get time proportional to their work —
// the chart's 362-test suite (which legitimately needs 26-28s on a contended
// CI node) must fit its budget. The envelope must stay strictly larger than
// the run budget for any size, or the coordinator would declare the
// validationtests validator a missing responder instead of receiving its
// self-reported verdict.
func TestSuiteRunBudget(t *testing.T) {
	if got := SuiteRunBudget(0); got != 25*time.Second {
		t.Fatalf("zero-suite budget must be the 25s floor, got %s", got)
	}
	if got := SuiteRunBudget(100); got != 25*time.Second {
		t.Fatalf("100 tests (10s scaled) must keep the 25s floor, got %s", got)
	}
	if got := SuiteRunBudget(362); got != 36200*time.Millisecond {
		t.Fatalf("the incident's 362-test suite must scale to 36.2s, got %s", got)
	}
	for _, n := range []int{0, 1, 100, 250, 362, 1000} {
		if SuiteValidationEnvelope(n) <= SuiteRunBudget(n) {
			t.Fatalf("envelope must be strictly larger than the run budget for %d tests", n)
		}
	}
}
