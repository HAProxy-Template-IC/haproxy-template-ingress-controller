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
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"slices"
	"time"

	"github.com/spf13/cobra"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/conversion"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testrunner"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/schemafetcher"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

var (
	benchmarkConfigFile      string
	benchmarkTestNames       []string
	benchmarkIterations      int
	benchmarkProfileIncludes bool
	benchmarkSchemaDir       string
)

// benchmarkCmd represents the benchmark command.
var benchmarkCmd = &cobra.Command{
	Use:   "benchmark",
	Short: "Benchmark template rendering performance",
	Long: `Benchmark template rendering performance for a specific validation test.

This command measures template compilation time separately from render time,
allowing accurate comparison of cold vs warm renders.

The benchmark:
  1. Loads and parses the config file
  2. Compiles all templates (timed)
  3. Builds the render context from test fixtures
  4. Renders the same templates N times with the same context (timed individually)
  5. Reports compilation time, per-render times, and statistics

Example usage:
  # Run all validation tests with 5 iterations each
  controller benchmark -f config.yaml

  # Run specific tests
  controller benchmark -f config.yaml --test benchmark-ingress-100 --test benchmark-httproute-100

  # Run 10 iterations
  controller benchmark -f config.yaml --test benchmark-test --iterations 10

  # Profile include timing (identify slow template snippets)
  controller benchmark -f config.yaml --profile-includes`,
	RunE: runBenchmark,
}

func init() {
	benchmarkCmd.Flags().StringVarP(&benchmarkConfigFile, "file", "f", "", "Path to HAProxyTemplateConfig YAML file (required)")
	benchmarkCmd.Flags().StringSliceVar(&benchmarkTestNames, "test", nil, "Validation test name(s) to benchmark (omit to run all tests)")
	benchmarkCmd.Flags().IntVar(&benchmarkIterations, "iterations", 2, "Number of render iterations")
	benchmarkCmd.Flags().BoolVar(&benchmarkProfileIncludes, "profile-includes", false, "Show include timing statistics (top 20 slowest)")
	benchmarkCmd.Flags().StringVar(&benchmarkSchemaDir, "schema-dir", os.Getenv("HAPTIC_SCHEMA_DIR"),
		"Directory of schema files for typed-resource access during rendering (CustomResourceDefinition YAMLs or bare OpenAPI v3 schemas). "+
			"Required to benchmark configs that use typed access; falls through to untyped resources[\"name\"].List() if unset. Also reads HAPTIC_SCHEMA_DIR.")

	_ = benchmarkCmd.MarkFlagRequired("file")
}

// BenchmarkResult holds the results of a single test's benchmark run.
type BenchmarkResult struct {
	TestName   string
	Iterations []IterationResult
}

// IterationResult holds render times for a single benchmark iteration.
type IterationResult struct {
	TotalTime    time.Duration
	FileResults  []FileRenderResult
	IncludeStats []templating.IncludeStats // Profiling data for included templates
}

// FileRenderResult holds render time for a single file.
type FileRenderResult struct {
	Name     string
	Duration time.Duration
}

func runBenchmark(cmd *cobra.Command, _ []string) error {
	// Setup logging (minimal)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	}))
	slog.SetDefault(logger)

	configSpec, err := loadConfigFromFiles([]string{benchmarkConfigFile})
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	typedResult, err := bootstrapBenchmarkTypes(configSpec, logger)
	if err != nil {
		return err
	}

	// Convert to internal config (after effective-spec resolution, so the benchmark
	// runs against the same effective test set the load gate would).
	cfg, err := conversion.ConvertSpec(configSpec)
	if err != nil {
		return fmt.Errorf("converting config: %w", err)
	}

	// If no tests specified, run all (except _global)
	if len(benchmarkTestNames) == 0 {
		for name := range cfg.ValidationTests {
			if name != "_global" {
				benchmarkTestNames = append(benchmarkTestNames, name)
			}
		}
		slices.Sort(benchmarkTestNames) // Deterministic order
	}

	// Validate all specified tests exist
	for _, testName := range benchmarkTestNames {
		if _, exists := cfg.ValidationTests[testName]; !exists {
			return fmt.Errorf("test %q not found in config", testName)
		}
	}

	if len(benchmarkTestNames) == 0 {
		return errNoValidationTests
	}

	validationPaths, _, _, cleanupFunc, err := setupValidationPaths(cmd.Context(), configSpec)
	if err != nil {
		return err
	}
	defer cleanupFunc()

	// Step 1: Compile templates (timed) - ONCE for all tests
	fmt.Println("Compiling templates...")
	compileStart := time.Now()
	engine, err := compileTemplatesForBenchmark(cfg, typedResult)
	if err != nil {
		return fmt.Errorf("compiling templates: %w", err)
	}
	compilationTime := time.Since(compileStart)

	// Step 2: Run benchmark for each test
	results := make([]*BenchmarkResult, 0, len(benchmarkTestNames))

	for _, testName := range benchmarkTestNames {
		result, err := runSingleTestBenchmark(cfg, engine, testName, validationPaths, typedResult.Types, logger)
		if err != nil {
			return fmt.Errorf("benchmark for test %q failed: %w", testName, err)
		}
		results = append(results, result)
	}

	// Step 3: Output results for all tests
	outputAllBenchmarkResults(results, compilationTime)

	return nil
}

// bootstrapBenchmarkTypes loads the --schema-dir schema set, resolves the
// effective spec, and runs the offline type bootstrap — mirroring `validate` so
// the benchmark renders typed-access templates identically to production.
// Resolving each watched resource's apiVersions candidate list against the schema
// dir is what lets typebootstrap generate the typed globals. Returns an empty
// result when no --schema-dir was supplied, in which case a typed-access template
// fails at compile time with a clear pointer back to --schema-dir.
func bootstrapBenchmarkTypes(configSpec *v1alpha1.HAProxyTemplateConfigSpec, logger *slog.Logger) (*typebootstrap.Result, error) {
	var dirFetcher *schemafetcher.DirFetcher
	if benchmarkSchemaDir != "" {
		var err error
		dirFetcher, err = schemafetcher.NewDirFetcher(benchmarkSchemaDir)
		if err != nil {
			return nil, fmt.Errorf("loading schema directory %q: %w", benchmarkSchemaDir, err)
		}
		logger.Info("Offline type bootstrap: loaded schema directory",
			"path", benchmarkSchemaDir, "schemas", dirFetcher.Len())
	}
	served, fieldServed := dirServedCheckers(dirFetcher)
	if _, err := conversion.ResolveEffectiveSpec(configSpec, served, fieldServed, logger); err != nil {
		return nil, fmt.Errorf("resolving effective config: %w", err)
	}
	typedResult, err := runOfflineTypeBootstrap(configSpec, dirFetcher, logger)
	if err != nil {
		return nil, fmt.Errorf("offline type bootstrap: %w", err)
	}
	return typedResult, nil
}

// runSingleTestBenchmark runs the benchmark for a single test.
func runSingleTestBenchmark(
	cfg *config.Config,
	engine templating.Engine,
	testName string,
	validationPaths *dataplane.ValidationPaths,
	typedResourceTypes map[string]reflect.Type,
	logger *slog.Logger,
) (*BenchmarkResult, error) {
	test := cfg.ValidationTests[testName]

	// Merge global fixtures if present
	fixtures := test.Fixtures
	httpFixtures := test.HTTPFixtures
	if globalTest, hasGlobal := cfg.ValidationTests["_global"]; hasGlobal {
		fixtures = testrunner.MergeFixtures(globalTest.Fixtures, test.Fixtures)
		httpFixtures = testrunner.MergeHTTPFixtures(globalTest.HTTPFixtures, test.HTTPFixtures)
	}

	storeMap, err := createStoresForBenchmark(cfg, engine, fixtures)
	if err != nil {
		return nil, fmt.Errorf("creating fixture stores: %w", err)
	}

	httpStore := createHTTPStoreForBenchmark(httpFixtures, logger)

	renderCtx := buildBenchmarkContext(cfg, storeMap, validationPaths, httpStore, typedResourceTypes, logger)

	// Fold in the _global + per-test extraContext baseline so the benchmark
	// renders each test exactly as the load gate does (e.g. against the isolated
	// synthetic default certificate) instead of the deployment's production
	// extraContext alone — otherwise a custom defaultSSLCertificate name makes
	// the render fail here just as it did at the load gate.
	testrunner.ApplyTestExtraContext(renderCtx, cfg, test.ExtraContext)

	// Warm up (one render to eliminate any JIT effects)
	_, err = renderAllFiles(engine, cfg, renderCtx)
	if err != nil {
		return nil, fmt.Errorf("warm-up render failed: %w", err)
	}

	// Run benchmark iterations
	result := &BenchmarkResult{
		TestName:   testName,
		Iterations: make([]IterationResult, 0, benchmarkIterations),
	}

	for i := 0; i < benchmarkIterations; i++ {
		iterResult, err := renderAllFiles(engine, cfg, renderCtx)
		if err != nil {
			return nil, fmt.Errorf("render iteration %d failed: %w", i+1, err)
		}
		result.Iterations = append(result.Iterations, iterResult)
	}

	return result, nil
}
