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

package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/conversion"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/names"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testrunner"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/schemafetcher"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"
)

var (
	validateConfigFiles     []string
	validateTestName        string
	validateOutputFormat    string
	validateVerbose         bool
	validateDumpRendered    bool
	validateTraceTemplates  bool
	validateDebugFilters    bool
	validateProfileIncludes bool
	validateDumpMerged      bool
	validateWorkers         int
	// validateSchemaDir is the kubeconform-style schema directory the
	// offline type-bootstrap reads from. Required for typed-access in
	// templates; without it, watched resources fall back entirely to
	// the untyped resources["<name>"] path and any template that reaches
	// for a typed top-level global (e.g. `gateways[i].Spec.Listeners`)
	// fails at engine compile time with a clear "no schema for X"
	// pointer back to --schema-dir / HAPTIC_SCHEMA_DIR. Full CRDs
	// (apiextensions.k8s.io/v1) also auto-populate the offline GVK
	// resolver from their spec.names.plural, so users who add CRDs to
	// the directory don't have to also register the (apiVersion, plural)
	// mapping
	// in code.
	validateSchemaDir string
)

// validateCmd represents the validate command.
var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate HAProxyTemplateConfig with embedded tests",
	Long: `Validate a HAProxyTemplateConfig CRD by running its embedded validation tests.

This command loads a HAProxyTemplateConfig from a file, compiles its templates,
and executes all validation tests (or a specific test if --test is specified).

The validation tests can assert:
- HAProxy configuration is syntactically valid
- Configuration contains expected patterns
- Configuration does not contain forbidden patterns
- Exact value matching
- JSONPath queries against template context

Example usage:
  # Run all validation tests
  controller validate -f config.yaml

  # Run a specific test
  controller validate -f config.yaml --test "test-frontend-routing"

  # Output results as JSON
  controller validate -f config.yaml --output json

  # Show include timing statistics
  controller validate -f config.yaml --profile-includes`,
	RunE: runValidate,
}

func init() {
	validateCmd.Flags().StringArrayVarP(&validateConfigFiles, "file", "f", nil,
		"Path to a HAProxyTemplateConfig YAML file (required). Repeatable, and each file may hold several documents; "+
			"all of them are merged in order, later wins — the same merge the controller performs over its CRD_NAME list.")
	validateCmd.Flags().StringVar(&validateTestName, "test", "", "Run specific test by name (optional)")
	validateCmd.Flags().StringVarP(&validateOutputFormat, "output", "o", "summary", "Output format: summary, json, yaml")
	validateCmd.Flags().BoolVar(&validateVerbose, "verbose", false, "Show rendered content preview for failed assertions")
	validateCmd.Flags().BoolVar(&validateDumpRendered, "dump-rendered", false, "Dump all rendered content (haproxy.cfg, maps, files)")
	validateCmd.Flags().BoolVar(&validateTraceTemplates, "trace-templates", false, "Show template execution trace (top-level only; use with --profile-includes for full call tree)")
	validateCmd.Flags().BoolVar(&validateDebugFilters, "debug-filters", false, "Show filter operation debugging (sort comparisons, etc.)")
	validateCmd.Flags().BoolVar(&validateProfileIncludes, "profile-includes", false, "Show include timing statistics (top 20 slowest)")
	validateCmd.Flags().IntVar(&validateWorkers, "workers", 0, "Number of parallel test workers (0=auto-detect CPUs, 1=sequential)")
	validateCmd.Flags().BoolVar(&validateDumpMerged, "dump-merged", false,
		"Print the merged spec as YAML and exit, without running any test. Shows exactly what the controller "+
			"assembles from its CRD_NAME list.")
	// --schema-dir / HAPTIC_SCHEMA_DIR — kubeconform-style local
	// schema directory. Accepts full CRD YAMLs (the wire form
	// `kubectl get crd X -o yaml` produces) or bare OpenAPI v3
	// spec.Schema files with an x-kubernetes-group-version-kind
	// extension. Required for typed-access in templates; without it,
	// watched resources fall back entirely to the untyped
	// resources["<name>"] path. Templates that reach for typed top-
	// level globals (e.g. `gateways[i].Spec.Listeners`) without
	// --schema-dir fail at engine compile time with a clear error
	// pointing back here.
	validateCmd.Flags().StringVar(&validateSchemaDir, "schema-dir", os.Getenv("HAPTIC_SCHEMA_DIR"),
		"Directory of schema files to use for typed-resource access during validation "+
			"(accepts CustomResourceDefinition YAMLs or bare OpenAPI v3 schemas). "+
			"Required for typed-access in templates; falls through to untyped resources[\"name\"].List() if unset. "+
			"Also reads HAPTIC_SCHEMA_DIR.")

	_ = validateCmd.MarkFlagRequired("file")
}

func runValidate(_ *cobra.Command, _ []string) error {
	ctx := context.Background()

	// Setup logging
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	if validateDumpMerged {
		return dumpMergedSpec()
	}

	// Setup validation environment
	setup, err := setupValidation(logger)
	if err != nil {
		return err
	}
	defer setup.Cleanup()

	// Run tests
	results, err := runValidationTests(ctx, setup.ConfigSpec, setup.Engine, setup.ValidationPaths, setup.Capabilities, setup.HAProxyVersion, setup.TypedResourceTypes, logger)
	if err != nil {
		return err
	}

	// Output results and optional content
	if err := outputResults(results, setup.Engine); err != nil {
		return err
	}

	// Exit with error code if tests failed
	if !results.AllPassed() {
		return fmt.Errorf("validation tests failed: %d/%d tests passed", results.PassedTests, results.TotalTests)
	}

	return nil
}

// ValidationSetup contains all components needed for validation test execution.
type ValidationSetup struct {
	ConfigSpec      *v1alpha1.HAProxyTemplateConfigSpec
	Engine          templating.Engine
	ValidationPaths *dataplane.ValidationPaths
	Capabilities    dataplane.Capabilities
	HAProxyVersion  *dataplane.Version

	// TypedResourceTypes is the per-resource generated Go type produced
	// by typebootstrap against schemas loaded from --schema-dir /
	// HAPTIC_SCHEMA_DIR. Populated for any watched resource whose
	// (apiVersion, resources-plural) pair has both a schema in the
	// supplied directory AND a GVK entry registered from the same
	// directory (full CRDs auto-register; bare OpenAPI v3 schemas with
	// an x-kubernetes-group-version-kind extension also auto-register).
	// Resources without typed support — including the entire `nil` case
	// when --schema-dir is not supplied — map to nothing here and fall
	// through to the untyped resources["<name>"] path via dig().
	TypedResourceTypes map[string]reflect.Type

	Cleanup func()
}

// setupValidation loads config, creates engine, and sets up validation paths.
func setupValidation(logger *slog.Logger) (*ValidationSetup, error) {
	// Load HAProxyTemplateConfig from file
	configSpec, err := loadConfigFromFiles(validateConfigFiles)
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}

	// The same structural gate the controller applies on load. Several of these
	// requirements can no longer live in the CRD schema (a single object of a
	// merged set is legitimately incomplete), so checking here is what keeps
	// `validate` an honest pre-apply gate.
	structural, err := conversion.ConvertSpec(configSpec)
	if err != nil {
		return nil, fmt.Errorf("converting config: %w", err)
	}
	if err := coreconfig.ValidateMergedCompleteness(structural); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	// Load the schema directory once; it doubles as the offline availability
	// signal for effective-config resolution and as the typebootstrap schema
	// source below.
	var dirFetcher *schemafetcher.DirFetcher
	if validateSchemaDir != "" {
		dirFetcher, err = schemafetcher.NewDirFetcher(validateSchemaDir)
		if err != nil {
			return nil, fmt.Errorf("loading schema directory %q: %w", validateSchemaDir, err)
		}
		logger.Info("Offline type bootstrap: loaded schema directory",
			"path", validateSchemaDir,
			"schemas", dirFetcher.Len())
	}

	// Mirror the controller's effective-config resolution offline: resolve
	// apiVersions candidate lists against the schema directory and strip
	// features whose optional resources are absent from it, plus every
	// validation test whose requiresFields names a field the resolved
	// schema generation lacks. This is what makes degraded cluster
	// profiles unit-testable — point --schema-dir at an old-release CRD
	// bundle and the same code path strips the same features a live
	// cluster of that vintage would.
	served, fieldServed := dirServedCheckers(dirFetcher)
	specResolution, err2 := conversion.ResolveEffectiveSpec(configSpec, served, fieldServed, logger)
	if err2 != nil {
		return nil, fmt.Errorf("resolving effective config: %w", err2)
	}
	printStrippedTests(specResolution)

	// Check if config has validation tests
	if len(configSpec.ValidationTests) == 0 {
		return nil, errNoValidationTests
	}

	// Setup validation paths in temp directory
	// Pass configSpec so setupValidationPaths can derive subdirectory names from dataplane configuration
	validationPaths, capabilities, haproxyVersion, cleanupFunc, err := setupValidationPaths(configSpec)
	if err != nil {
		return nil, err
	}

	// Run the offline type-bootstrap pipeline (typebootstrap against the
	// schemas in --schema-dir / HAPTIC_SCHEMA_DIR) so the engine is
	// constructed with typed `gateways` etc. globals declared. Without
	// --schema-dir, this returns an empty Result and chart templates
	// that use the typed shape get a clear engine-compile-time error
	// pointing at the missing global — surfacing offline-vs-production
	// drift the validate CLI exists to catch.
	typedResult, err := runOfflineTypeBootstrap(configSpec, dirFetcher, logger)
	if err != nil {
		cleanupFunc()
		return nil, fmt.Errorf("offline type bootstrap: %w", err)
	}

	// Create template engine with custom filters + typed declarations
	engine, err := createTemplateEngine(configSpec, typedResult, logger)
	if err != nil {
		cleanupFunc()
		return nil, err
	}

	// Enable template tracing if requested
	if validateTraceTemplates {
		engine.EnableTracing()
	}

	// Enable filter debugging if requested
	if validateDebugFilters {
		engine.EnableFilterDebug()
	}

	return &ValidationSetup{
		ConfigSpec:         configSpec,
		Engine:             engine,
		ValidationPaths:    validationPaths,
		Capabilities:       capabilities,
		HAProxyVersion:     haproxyVersion,
		TypedResourceTypes: typedResult.Types,
		Cleanup:            cleanupFunc,
	}, nil
}

// printStrippedTests lists every validation test the effective-config
// resolution stripped, one "⊘ <name> stripped: <reason>" line each, sorted
// by name. The degraded-profile harness (scripts/test-templates.sh) greps
// these lines and asserts the exact set against a per-bundle allowlist —
// the symmetric counterpart of the "✗ <name>" failure lines.
func printStrippedTests(res *conversion.SpecResolution) {
	if res == nil || len(res.StrippedTests) == 0 {
		return
	}
	testNames := make([]string, 0, len(res.StrippedTests))
	for name := range res.StrippedTests {
		testNames = append(testNames, name)
	}
	sort.Strings(testNames)
	for _, name := range testNames {
		fmt.Printf("⊘ %s stripped: %s\n", name, res.StrippedTests[name])
	}
}

// dirServedCheckers builds the served / fieldServed callbacks for
// conversion.ResolveEffectiveSpec from a --schema-dir fetcher. With a nil
// fetcher both callbacks are nil, which ResolveEffectiveSpec treats as
// "everything served" (the lenient offline fall-through). Shared between
// the validate and migrate-check CLIs.
func dirServedCheckers(dirFetcher *schemafetcher.DirFetcher) (
	served func(apiVersion, resources string) bool,
	fieldServed func(apiVersion, resources, fieldPath string) (bool, error),
) {
	if dirFetcher == nil {
		return nil, nil
	}
	plurals := dirFetcher.PluralsFor()
	served = func(apiVersion, resources string) bool {
		_, ok := plurals[apiVersion][resources]
		return ok
	}
	fieldServed = func(apiVersion, resources, fieldPath string) (bool, error) {
		gvk, ok := plurals[apiVersion][resources]
		if !ok {
			// The schema dir doesn't bundle this resource at all —
			// same leniency as the untyped fall-through everywhere
			// else offline: don't judge fields we have no schema for.
			return true, nil
		}
		sch, components, err := dirFetcher.Fetch(context.Background(), gvk)
		if err != nil {
			return false, fmt.Errorf("loading schema for %s/%s: %w", apiVersion, resources, err)
		}
		return schemafetcher.SchemaHasField(sch, components, fieldPath), nil
	}
	return served, fieldServed
}

// runOfflineTypeBootstrap drives the type-bootstrap pipeline against
// schemas supplied via --schema-dir / HAPTIC_SCHEMA_DIR. Mirrors what
// pkg/controller/typebootstrap_wiring.go's runTypeBootstrap does in the
// production controller, but with two substitutions:
//
//   - Schema source is the user-supplied directory instead of the cluster's
//     OpenAPI / CRD endpoints.
//   - GVK resolution comes from CRDs / OpenAPI v3 documents in that same
//     directory instead of a RESTMapper built from the cluster's
//     discovery (no API server is reachable).
//
// When --schema-dir is empty (or unset and no HAPTIC_SCHEMA_DIR env var),
// no resources receive typed support and the whole chart validates through
// the untyped resources["<name>"] path via dig(). This is the right default:
// `controller validate` of a tiny config with no typed-access usage doesn't
// need to download schemas. Configs whose templates reach for typed access
// (`gateways[i].Spec.Listeners`, …) get a compile-time error from the
// engine pointing at the missing global, which the operator resolves by
// passing --schema-dir.
//
// Resources whose (apiVersion, resources-plural) pair isn't in the schema
// directory are skipped silently — they keep working through the untyped
// resources["<name>"] path. Resources whose GVK *does* resolve but whose
// schema is malformed are fail-closed: typebootstrap.Bootstrap aborts the
// run with a hard error on the first such resource (see
// pkg/controller/typebootstrap/bootstrap.go).
//
// Always returns a non-nil *Result so callers can range/index without
// nil checks.
func runOfflineTypeBootstrap(
	configSpec *v1alpha1.HAProxyTemplateConfigSpec,
	dirFetcher *schemafetcher.DirFetcher,
	logger *slog.Logger,
) (*typebootstrap.Result, error) {
	resolver := typebootstrap.NewOfflineGVKResolver()

	// Without --schema-dir there's no schema source. Return an empty
	// Result; the chart still validates through the untyped resources
	// path. This is the deliberate fall-through for configs that
	// don't exercise typed access — the engine will surface a
	// compile-time error pointing at any unbound typed global the
	// templates actually reach for.
	if dirFetcher == nil {
		return &typebootstrap.Result{}, nil
	}

	// Auto-extend the offline GVK resolver from full CRDs in the dir
	// (PluralsFor surfaces every CRD's spec.names.plural → GVK and
	// every bare OpenAPI v3 schema's x-kubernetes-group-version-kind
	// → GVK mapping). Operators who drop in their CRD YAMLs don't have
	// to also patch any code to map plural → GVK.
	for apiVersion, plurals := range dirFetcher.PluralsFor() {
		for plural, gvk := range plurals {
			resolver.Register(apiVersion, plural, gvk)
		}
	}

	// Iterate by name then index back into the map by reference so we
	// don't copy each ~128-byte WatchedResource value on every loop
	// (gocritic rangeValCopy).
	resources := make([]typebootstrap.Resource, 0, len(configSpec.WatchedResources))
	for name := range configSpec.WatchedResources {
		wr := configSpec.WatchedResources[name]
		gvk, err := resolver.Resolve(wr.APIVersion, wr.Resources)
		if err != nil {
			// Unknown GVK in the offline resolver: skip without
			// warning. Most watched resources won't have typed
			// support yet; only the ones the operator has supplied
			// a schema for via --schema-dir.
			logger.Debug("Offline type bootstrap: no GVK mapping; skipping typed support",
				"resource", name, "apiVersion", wr.APIVersion, "resources", wr.Resources)
			continue
		}
		resources = append(resources, typebootstrap.Resource{Name: name, GVK: gvk})
	}

	return typebootstrap.Bootstrap(context.Background(), typebootstrap.Config{
		Resources:          resources,
		GlobalIgnoreFields: configSpec.WatchedResourcesIgnoreFields,
		Fetcher:            dirFetcher,
		Logger:             logger,
	})
}

// runValidationTests executes the validation test suite.
// typedResourceTypes carries the typed reflect.Types from the offline
// type-bootstrap so the test runner's render context can declare the same
// `gateways` / `httproutes` / … top-level globals the engine compiled
// against (see setupValidation → runOfflineTypeBootstrap).
func runValidationTests(
	ctx context.Context,
	configSpec *v1alpha1.HAProxyTemplateConfigSpec,
	engine templating.Engine,
	validationPaths *dataplane.ValidationPaths,
	capabilities dataplane.Capabilities,
	haproxyVersion *dataplane.Version,
	typedResourceTypes map[string]reflect.Type,
	logger *slog.Logger,
) (*testrunner.TestResults, error) {
	// Convert CRD spec to internal config format
	cfg, err := conversion.ConvertSpec(configSpec)
	if err != nil {
		return nil, fmt.Errorf("converting config: %w", err)
	}

	// Create test runner
	runner := testrunner.New(
		cfg,
		engine,
		validationPaths,
		&testrunner.Options{
			Logger:             logger,
			Workers:            validateWorkers,
			DebugFilters:       validateDebugFilters,
			ProfileIncludes:    validateProfileIncludes,
			Capabilities:       capabilities,
			HAProxyVersion:     haproxyVersion,
			TypedResourceTypes: typedResourceTypes,
		},
	)

	// Run tests
	logger.Info("Running validation tests",
		"total_tests", len(cfg.ValidationTests),
		"filter", validateTestName)

	results, err := runner.RunTests(ctx, validateTestName)
	if err != nil {
		return nil, fmt.Errorf("test execution failed: %w", err)
	}

	return results, nil
}

// outputResults formats and prints test results, and optionally dumps rendered content and trace.
func outputResults(results *testrunner.TestResults, engine templating.Engine) error {
	// Format output
	output, err := testrunner.FormatResults(results, testrunner.OutputOptions{
		Format:  testrunner.OutputFormat(validateOutputFormat),
		Verbose: validateVerbose,
	})
	if err != nil {
		return fmt.Errorf("formatting results: %w", err)
	}

	// Print results to stdout
	fmt.Print(output)

	// Dump rendered content if requested
	if validateDumpRendered {
		dumpRenderedContent(results)
	}

	// Output template trace if requested
	if validateTraceTemplates {
		outputTemplateTrace(engine)
	}

	// Output include profile if requested
	if validateProfileIncludes {
		outputIncludeProfile(results)
	}

	return nil
}

// dumpRenderedContent prints all rendered content from test results.
func dumpRenderedContent(results *testrunner.TestResults) {
	fmt.Println("\n" + separatorDouble)
	fmt.Println("RENDERED CONTENT")
	fmt.Println(separatorDouble)

	for i := range results.TestResults {
		test := &results.TestResults[i]
		fmt.Printf("\n## Test: %s\n\n", test.TestName)

		if test.RenderedConfig != "" {
			fmt.Println("### haproxy.cfg")
			fmt.Println(separatorSingle)
			fmt.Println(test.RenderedConfig)
			fmt.Println(separatorSingle)
		}

		dumpRenderedNamedContent("Map Files", test.RenderedMaps)
		dumpRenderedNamedContent("General Files", test.RenderedFiles)
		dumpRenderedNamedContent("SSL Certificates", test.RenderedCerts)
	}
}

// dumpRenderedNamedContent prints a labelled section listing each name/content
// pair from the supplied map, separated by separatorSingle. It is a no-op when
// the map is empty so callers do not need to gate the call.
func dumpRenderedNamedContent(label string, items map[string]string) {
	if len(items) == 0 {
		return
	}
	fmt.Println("\n### " + label)
	for name, content := range items {
		fmt.Printf("\n#### %s\n", name)
		fmt.Println(separatorSingle)
		fmt.Println(content)
		fmt.Println(separatorSingle)
	}
}

// outputTemplateTrace prints template execution trace if available.
func outputTemplateTrace(engine templating.Engine) {
	trace := engine.GetTraceOutput()
	if trace != "" {
		fmt.Println("\n" + separatorDouble)
		fmt.Println("TEMPLATE EXECUTION TRACE")
		fmt.Println(separatorDouble)
		fmt.Println(trace)
	}
}

// outputIncludeProfile prints include timing statistics from test results.
func outputIncludeProfile(results *testrunner.TestResults) {
	statSlices := make([][]templating.IncludeStats, len(results.TestResults))
	for i := range results.TestResults {
		statSlices[i] = results.TestResults[i].IncludeStats
	}

	stats := aggregateIncludeStatsFromSlices(statSlices)
	if len(stats) == 0 {
		return
	}

	printIncludeProfile(stats)
}

// loadConfigFromFiles loads every HAProxyTemplateConfig document across the
// given files — in file order, and within a file in document order — and
// merges them the way the controller does at startup.
//
// A single file holding a single document is the common case and still accepts
// a bare spec (no apiVersion/kind), which is how hand-written fixtures are
// written. Anything beyond that must be complete objects, because merge order
// and precedence are only meaningful between identifiable configs.
func loadConfigFromFiles(filePaths []string) (*v1alpha1.HAProxyTemplateConfigSpec, error) {
	merged, bareSpec, testDocs, err := mergeConfigFiles(filePaths)
	if err != nil {
		return nil, err
	}
	if bareSpec != nil {
		return bareSpec, nil
	}

	config := &v1alpha1.HAProxyTemplateConfig{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(merged.Object, config); err != nil {
		return nil, fmt.Errorf("converting merged config: %w", err)
	}
	if err := unionFileValidationTests(&config.Spec, testDocs); err != nil {
		return nil, err
	}
	return &config.Spec, nil
}

// unionFileValidationTests folds tests carried by companion objects into the
// spec, so validating rendered chart output offline exercises the same suite the
// controller runs in the cluster.
func unionFileValidationTests(spec *v1alpha1.HAProxyTemplateConfigSpec, testDocs []*unstructured.Unstructured) error {
	if len(testDocs) == 0 {
		return nil
	}

	sources := []conversion.ValidationTestSource{{
		Origin: "HAProxyTemplateConfig spec.validationTests",
		Tests:  spec.ValidationTests,
	}}
	for _, doc := range testDocs {
		typed := &v1alpha1.HAProxyValidationTests{}
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(doc.Object, typed); err != nil {
			return fmt.Errorf("reading HAProxyValidationTests %s: %w", doc.GetName(), err)
		}
		sources = append(sources, conversion.ValidationTestSource{
			Origin: "HAProxyValidationTests/" + doc.GetName(),
			Tests:  typed.Spec.ValidationTests,
		})
	}

	union, err := conversion.UnionValidationTests(sources)
	if err != nil {
		return err
	}
	spec.ValidationTests = union
	return nil
}

// mergeConfigFiles reads every HAProxyTemplateConfig document across the given
// files and merges them. Exactly one of the two results is non-nil: the merged
// object, or — for a lone file holding no identifiable object — the bare spec
// it parsed instead.
func mergeConfigFiles(filePaths []string) (
	merged *unstructured.Unstructured,
	bareSpec *v1alpha1.HAProxyTemplateConfigSpec,
	testDocs []*unstructured.Unstructured,
	err error,
) {
	var sources []*unstructured.Unstructured
	for _, filePath := range filePaths {
		// Clean the file path to prevent path traversal attacks
		cleanPath := filepath.Clean(filePath)

		data, readErr := os.ReadFile(cleanPath)
		if readErr != nil {
			return nil, nil, nil, fmt.Errorf("reading file: %w", readErr)
		}

		documents, splitErr := splitConfigDocuments(data)
		if splitErr != nil {
			return nil, nil, nil, fmt.Errorf("reading %s: %w", filePath, splitErr)
		}

		if len(documents) == 0 && len(filePaths) == 1 {
			spec, parseErr := parseConfigSpec(data)
			return nil, spec, nil, parseErr
		}
		for _, doc := range documents {
			if doc.GetKind() == "HAProxyValidationTests" {
				testDocs = append(testDocs, doc)
				continue
			}
			sources = append(sources, doc)
		}
	}

	if len(sources) == 0 {
		return nil, nil, nil, fmt.Errorf("no HAProxyTemplateConfig documents in %s", strings.Join(filePaths, ", "))
	}

	merged, _, err = conversion.MergeSpecs(sources)
	if err != nil {
		return nil, nil, nil, err
	}
	return merged, nil, testDocs, nil
}

// dumpMergedSpec prints the merged spec and returns. It is the only way to see
// what a set of configs actually assembles into without a cluster, so it is
// also what pins the controller's merge against the chart's rendering.
//
// It prints the merge result verbatim rather than round-tripping through the
// typed spec: the typed form adds zero values for every field the YAML omits
// (`logging: {}`, `extraContext: null`), which would drown any real difference.
func dumpMergedSpec() error {
	merged, bareSpec, testDocs, err := mergeConfigFiles(validateConfigFiles)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	var payload any = bareSpec
	if merged != nil {
		spec, _ := merged.Object["spec"].(map[string]any)
		// Fold companion tests in, so what this prints is the whole suite the
		// controller would run. Without it a consumer of the dump — the
		// playground's presets, for one — would silently get a config with no
		// tests at all.
		if len(testDocs) > 0 && spec != nil {
			tests, unionErr := unionDumpedTests(spec, testDocs)
			if unionErr != nil {
				return unionErr
			}
			spec["validationTests"] = tests
		}
		payload = merged.Object["spec"]
	}
	out, err := yaml.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshalling merged spec: %w", err)
	}
	fmt.Print(string(out))
	return nil
}

// splitConfigDocuments returns the HAProxyTemplateConfig documents in a YAML
// stream, in order. Non-HAProxyTemplateConfig documents are skipped, so the
// raw output of `helm template` can be passed straight through.
func splitConfigDocuments(data []byte) ([]*unstructured.Unstructured, error) {
	var documents []*unstructured.Unstructured
	reader := utilyaml.NewYAMLReader(bufio.NewReader(bytes.NewReader(data)))
	for {
		chunk, err := reader.Read()
		if errors.Is(err, io.EOF) {
			return documents, nil
		}
		if err != nil {
			return nil, err
		}
		if len(bytes.TrimSpace(chunk)) == 0 {
			continue
		}

		object := map[string]any{}
		if err := yaml.Unmarshal(chunk, &object); err != nil {
			return nil, err
		}
		document := &unstructured.Unstructured{Object: object}
		// Both kinds are kept. Dropping the tests objects would make this
		// command — and scripts/test-templates.sh, the gate CI runs — report
		// success having executed none of their tests, because an empty suite
		// passes unconditionally.
		switch document.GetKind() {
		case "HAProxyTemplateConfig", "HAProxyValidationTests":
			documents = append(documents, document)
		}
	}
}

// parseConfigSpec decodes HAProxyTemplateConfig YAML — either the full
// Kubernetes resource form or a bare spec — into its spec. Shared between
// loadConfigFromFile and the migrate-check CLI's in-process chart render.
func parseConfigSpec(data []byte) (*v1alpha1.HAProxyTemplateConfigSpec, error) {
	// Parse as Kubernetes resource
	scheme := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(scheme)
	codecs := serializer.NewCodecFactory(scheme)

	// First try to parse as structured Kubernetes resource
	obj, _, err := codecs.UniversalDeserializer().Decode(data, nil, nil)
	if err == nil {
		// Successfully decoded as typed object
		if config, ok := obj.(*v1alpha1.HAProxyTemplateConfig); ok {
			return &config.Spec, nil
		}
		return nil, errors.New("file does not contain HAProxyTemplateConfig")
	}

	// Fallback: Try parsing as raw YAML (for spec-only files)
	var spec v1alpha1.HAProxyTemplateConfigSpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("parsing YAML: %w", err)
	}

	return &spec, nil
}

// createTemplateEngine creates and compiles the template engine from config
// spec with custom filters. typedResult carries the typed reflect.Types from
// the offline type-bootstrap pipeline; its
// typebootstrap.BuildEngineDeclarations output is merged with the static
// `currentConfig` declaration before being handed to the engine, so chart
// templates that use typed `gateways` / `httproutes` / … globals compile
// against the same surface the production renderer provides.
func createTemplateEngine(
	configSpec *v1alpha1.HAProxyTemplateConfigSpec,
	typedResult *typebootstrap.Result,
	logger *slog.Logger,
) (templating.Engine, error) {
	// Convert CRD spec to internal config
	cfg, err := conversion.ConvertSpec(configSpec)
	if err != nil {
		return nil, fmt.Errorf("converting config spec: %w", err)
	}

	// Log template compilation
	templates := helpers.ExtractTemplatesFromConfig(cfg)
	logger.Info("Compiling templates", "template_count", len(templates.AllTemplates), "engine", cfg.TemplatingSettings.Engine)

	// Create engine using helper (handles template extraction, filters, engine type parsing)
	// Note: The fail() function is auto-registered by the Scriggo engine
	// Pass profiling option from CLI flag so the same engine can be reused for all tests.
	options := helpers.EngineOptions{
		EnableProfiling: validateProfileIncludes,
	}

	// Single source of truth for the engine's additionalDeclarations.
	// Folds currentConfig + per-watchedResource typed globals using
	// the same helper every other engine consumer in this controller
	// uses, so an offline-vs-production drift is impossible by
	// construction.
	additionalDeclarations := helpers.BuildAdditionalDeclarations(cfg, typedResult)

	engine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, additionalDeclarations, options)
	if err != nil {
		return nil, fmt.Errorf("compiling templates: %w", err)
	}

	return engine, nil
}

// setupValidationPaths creates temporary directories for HAProxy validation.
// Returns the validation paths, capabilities, and a cleanup function.
// IMPORTANT: Subdirectory names are derived from the HAProxyTemplateConfig's dataplane configuration
// to ensure consistency between production and validation environments.
func setupValidationPaths(configSpec *v1alpha1.HAProxyTemplateConfigSpec) (
	paths *dataplane.ValidationPaths,
	capabilities dataplane.Capabilities,
	haproxyVersion *dataplane.Version,
	cleanup func(),
	err error,
) {
	// Detect local HAProxy version to determine capabilities
	// CRT-list storage is only available in HAProxy 3.2+
	localVersion, err := dataplane.DetectLocalVersion()
	if err != nil {
		return nil, dataplane.Capabilities{}, nil, nil, fmt.Errorf("detecting local HAProxy version: %w\nHint: Ensure 'haproxy' is in PATH", err)
	}
	capabilities = dataplane.CapabilitiesFromVersion(localVersion)

	// Create temporary directory
	tempDir, err := os.MkdirTemp("", "haproxy-validate-*")
	if err != nil {
		return nil, dataplane.Capabilities{}, nil, nil, fmt.Errorf("creating temp dir: %w", err)
	}

	// Convert CRD spec to internal config format to get dataplane configuration with defaults applied
	cfg, err := conversion.ConvertSpec(configSpec)
	if err != nil {
		_ = os.RemoveAll(tempDir)
		return nil, dataplane.Capabilities{}, nil, nil, fmt.Errorf("converting config spec: %w", err)
	}

	// Derive subdirectory names from configured dataplane paths using path.Base()
	// (slash-only — the configured dirs are HAProxy target paths regardless of host OS).
	// This extracts the final directory name (e.g., "/etc/haproxy/maps" → "maps")
	// and maintains consistency with production while using relative paths for validation
	basePaths := dataplane.PathConfig{
		MapsDir:    filepath.Join(tempDir, path.Base(cfg.Dataplane.MapsDir)),
		SSLDir:     filepath.Join(tempDir, path.Base(cfg.Dataplane.SSLCertsDir)),
		GeneralDir: filepath.Join(tempDir, path.Base(cfg.Dataplane.GeneralStorageDir)),
		ConfigFile: filepath.Join(tempDir, names.MainTemplateName),
	}

	// Use centralized path resolution to get capability-aware paths
	// This ensures CRTListDir is set correctly for HAProxy < 3.2
	resolvedPaths := dataplane.ResolvePaths(basePaths)

	// Create directories (include CRTListDir which may be same as GeneralDir)
	dirsToCreate := []string{resolvedPaths.MapsDir, resolvedPaths.SSLDir, resolvedPaths.GeneralDir}
	if resolvedPaths.CRTListDir != resolvedPaths.SSLDir && resolvedPaths.CRTListDir != resolvedPaths.GeneralDir {
		dirsToCreate = append(dirsToCreate, resolvedPaths.CRTListDir)
	}

	for _, dir := range dirsToCreate {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			_ = os.RemoveAll(tempDir)
			return nil, dataplane.Capabilities{}, nil, nil, fmt.Errorf("creating directory: %w", err)
		}
	}

	cleanup = func() {
		_ = os.RemoveAll(tempDir)
	}

	return resolvedPaths.ToValidationPaths(), capabilities, localVersion, cleanup, nil
}

// unionDumpedTests folds companion tests into the verbatim merged spec for
// --dump-merged. It goes through the typed union rather than merging the
// unstructured maps directly, so the dump obeys the same collision and _global
// rules the controller does instead of a second, quietly different set.
func unionDumpedTests(spec map[string]any, testDocs []*unstructured.Unstructured) (map[string]any, error) {
	inline := &v1alpha1.HAProxyTemplateConfigSpec{}
	if raw, ok := spec["validationTests"]; ok {
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(
			map[string]any{"validationTests": raw}, inline); err != nil {
			return nil, fmt.Errorf("reading inline validationTests: %w", err)
		}
	}
	if err := unionFileValidationTests(inline, testDocs); err != nil {
		return nil, err
	}

	out, err := runtime.DefaultUnstructuredConverter.ToUnstructured(inline)
	if err != nil {
		return nil, fmt.Errorf("re-encoding merged validationTests: %w", err)
	}
	tests, _ := out["validationTests"].(map[string]any)
	return tests, nil
}
