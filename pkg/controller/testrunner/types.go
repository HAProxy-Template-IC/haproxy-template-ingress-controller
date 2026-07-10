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

package testrunner

import (
	"log/slog"
	"reflect"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
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
	haproxyVersion  *dataplane.Version     // Local HAProxy version for test skipping

	// typedResourceTypes carries the typed reflect.Types produced by
	// typebootstrap for the offline validate path. Mirrors what
	// renderer.RenderService receives in production. When non-empty,
	// buildRenderingContext forwards it to rendercontext.NewBuilder
	// via WithTypedResources so chart templates that use the typed
	// `gateways` (etc.) globals compile against the same shape they
	// would in production.
	typedResourceTypes map[string]reflect.Type

	// skipBinaryValidation runs tests without a filesystem or haproxy binary
	// (browser/WASM): createTestPaths skips MkdirAll and haproxy_valid falls
	// back to the pure-Go syntax+schema check. See Options.SkipBinaryValidation.
	skipBinaryValidation bool
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
	// Default (0): runtime.NumCPU().
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

	// HAProxyVersion is the detected local HAProxy version.
	// When set, tests with MinHAProxyVersion above this version are skipped.
	HAProxyVersion *dataplane.Version

	// SkipBinaryValidation runs without a real filesystem or the haproxy
	// binary: per-test temp directories are not created, and `haproxy_valid`
	// assertions fall back to the pure-Go syntax + schema check
	// (dataplane.ValidateSyntaxAndSchema) instead of `haproxy -c`. It exists
	// for the browser (WASM) playground, where neither a writable filesystem
	// nor the haproxy binary is available. Callers that set it must present
	// `haproxy_valid` results as syntax+schema-only, since the binary phase
	// (cross-references, unknown keywords, global/defaults checks) is not run.
	SkipBinaryValidation bool

	// TypedResourceTypes is the per-resource generated Go type the
	// engine was constructed with via typebootstrap. Same shape as
	// renderer.RenderServiceConfig.TypedResourceTypes — see
	// pkg/controller/renderer/typed_resources.go for the production
	// counterpart. Passed straight through to
	// rendercontext.WithTypedResources so the offline render context
	// includes the typed top-level globals the engine compiled
	// against. When nil/empty, the test runner behaves as before
	// (no typed globals injected — chart code keeps using
	// resources.<name>.List() through dig()).
	TypedResourceTypes map[string]reflect.Type
}

// TestResults contains the results of running validation tests.
type TestResults struct {
	// TotalTests is the total number of tests executed (excluding skipped).
	TotalTests int

	// PassedTests is the number of tests that passed all assertions.
	PassedTests int

	// FailedTests is the number of tests with at least one failed assertion.
	FailedTests int

	// SkippedTests is the number of tests skipped (e.g., due to HAProxy version requirements).
	SkippedTests int

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

	// Skipped is true if the test was skipped (e.g., HAProxy version too low).
	Skipped bool

	// SkipReason explains why the test was skipped.
	SkipReason string

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

	// RenderedK8sResources contains rendered output of every
	// `spec.k8sResources` template (template name → rendered YAML).
	// Asserted via `target: k8s:<template-name>`.
	RenderedK8sResources map[string]string `json:"renderedK8sResources,omitempty" yaml:"renderedK8sResources,omitempty"`

	// RenderedStatusPatches contains the JSON-marshalled status payload
	// emitted by every `statusPatch()` call during the haproxy.cfg
	// render, keyed by `<namespace>/<name>:<phase>` (cluster-scoped
	// resources use `:<phase>` with empty namespace). Asserted via
	// `target: status:<namespace>/<name>:<phase>`.
	RenderedStatusPatches map[string]string `json:"renderedStatusPatches,omitempty" yaml:"renderedStatusPatches,omitempty"`

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
