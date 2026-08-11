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

package configtest

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// cfgWithContainsTest builds a minimal valid config with a single `contains`
// validationTest asserting `pattern` against the rendered haproxy.cfg.
func cfgWithContainsTest(t *testing.T, pattern string) (*coreconfig.Config, templating.Engine) {
	t.Helper()
	cfg := &coreconfig.Config{
		PodSelector:   coreconfig.PodSelector{MatchLabels: map[string]string{"app": "haproxy"}},
		Logging:       coreconfig.LoggingConfig{Level: "INFO"},
		HAProxyConfig: coreconfig.HAProxyConfig{Template: "frontend http\n  bind *:80\n"},
		ValidationTests: map[string]coreconfig.ValidationTest{
			"test-canary": {
				Description: "smoke",
				Fixtures:    map[string][]any{},
				Assertions: []coreconfig.ValidationAssertion{
					{Type: "contains", Target: "haproxy.cfg", Pattern: pattern, Description: "d"},
				},
			},
		},
	}
	coreconfig.SetDefaults(cfg)
	engine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, nil, helpers.EngineOptions{})
	if err != nil {
		t.Fatalf("building engine: %v", err)
	}
	return cfg, engine
}

func TestRunValidationTests_NoTests(t *testing.T) {
	cfg := &coreconfig.Config{HAProxyConfig: coreconfig.HAProxyConfig{Template: "frontend http\n"}}
	coreconfig.SetDefaults(cfg)
	res, err := RunValidationTests(context.Background(), cfg, nil, nil, time.Minute, discardLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Passed {
		t.Fatal("a config with no validationTests should pass trivially")
	}
}

func TestRunValidationTests_Passing(t *testing.T) {
	cfg, engine := cfgWithContainsTest(t, "frontend http")
	res, err := RunValidationTests(context.Background(), cfg, engine, nil, time.Minute, discardLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Passed || len(res.Failures) != 0 || res.Incomplete {
		t.Fatalf("expected pass, got %+v", res)
	}
}

func TestRunValidationTests_Failing(t *testing.T) {
	cfg, engine := cfgWithContainsTest(t, "NEVER_RENDERED_xyz")
	res, err := RunValidationTests(context.Background(), cfg, engine, nil, time.Minute, discardLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Passed || res.Incomplete {
		t.Fatalf("expected a clean failure, got %+v", res)
	}
	if !strings.Contains(strings.Join(res.Failures, "\n"), "test-canary") {
		t.Fatalf("expected the failing test name in failures, got %v", res.Failures)
	}
}

func TestRunValidationTests_IncompleteOnTimeout(t *testing.T) {
	cfg, engine := cfgWithContainsTest(t, "frontend http")
	// 1ns budget guarantees the run is cut short; must surface as Incomplete
	// (not a false "0 failures" pass) so callers can fail-closed.
	res, err := RunValidationTests(context.Background(), cfg, engine, nil, time.Nanosecond, discardLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Incomplete {
		t.Fatalf("expected Incomplete on a cut-short run, got %+v", res)
	}
	if res.Passed {
		t.Fatal("an incomplete run must not report Passed")
	}
}

func TestRunValidationTests_TimeoutCancelsActiveRender(t *testing.T) {
	cfg, _ := cfgWithContainsTest(t, "never reached")
	cfg.HAProxyConfig.Template = `{%%
  var total = 0
  for i := 0; i < 2000000000; i++ { total = total + i }
%%}{{ total }}`
	engine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, nil, helpers.EngineOptions{})
	if err != nil {
		t.Fatalf("building engine: %v", err)
	}

	type outcome struct {
		result Result
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, runErr := RunValidationTests(context.Background(), cfg, engine, nil, 20*time.Millisecond, discardLogger())
		done <- outcome{result: result, err: runErr}
	}()

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("unexpected error: %v", got.err)
		}
		if !got.result.Incomplete || got.result.Passed || len(got.result.Failures) != 0 {
			t.Fatalf("expected an incomplete run without a configuration failure, got %+v", got.result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("validation timeout did not cancel the active template render")
	}
}
