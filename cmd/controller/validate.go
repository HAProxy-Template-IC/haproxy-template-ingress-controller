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
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"haproxy-template-ic/pkg/apis/haproxytemplate/v1alpha1"
	"haproxy-template-ic/pkg/controller/conversion"
	"haproxy-template-ic/pkg/controller/testrunner"
	"haproxy-template-ic/pkg/dataplane"
	"haproxy-template-ic/pkg/templating"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"sigs.k8s.io/yaml"
)

var (
	validateConfigFile      string
	validateTestName        string
	validateOutputFormat    string
	validateHAProxyBinary   string
	validateVerbose         bool
	validateDumpRendered    bool
	validateTraceTemplates  bool
	validateDebugFilters    bool
	validateProfileIncludes bool
	validateWorkers         int
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

  # Use custom HAProxy binary location
  controller validate -f config.yaml --haproxy-binary /usr/local/bin/haproxy`,
	RunE: runValidate,
}

func init() {
	validateCmd.Flags().StringVarP(&validateConfigFile, "file", "f", "", "Path to HAProxyTemplateConfig YAML file (required)")
	validateCmd.Flags().StringVar(&validateTestName, "test", "", "Run specific test by name (optional)")
	validateCmd.Flags().StringVarP(&validateOutputFormat, "output", "o", "summary", "Output format: summary, json, yaml")
	validateCmd.Flags().StringVar(&validateHAProxyBinary, "haproxy-binary", "haproxy", "Path to HAProxy binary for validation")
	validateCmd.Flags().BoolVar(&validateVerbose, "verbose", false, "Show rendered content preview for failed assertions")
	validateCmd.Flags().BoolVar(&validateDumpRendered, "dump-rendered", false, "Dump all rendered content (haproxy.cfg, maps, files)")
	validateCmd.Flags().BoolVar(&validateTraceTemplates, "trace-templates", false, "Show template execution trace")
	validateCmd.Flags().BoolVar(&validateDebugFilters, "debug-filters", false, "Show filter operation debugging (sort comparisons, etc.)")
	validateCmd.Flags().BoolVar(&validateProfileIncludes, "profile-includes", false, "Show include timing statistics (top 20 slowest)")
	validateCmd.Flags().IntVar(&validateWorkers, "workers", 0, "Number of parallel test workers (0=auto-detect CPUs, 1=sequential)")

	_ = validateCmd.MarkFlagRequired("file")
}

func runValidate(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// Setup logging
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	// Setup validation environment
	setup, err := setupValidation(logger)
	if err != nil {
		return err
	}
	defer setup.Cleanup()

	// Run tests
	results, err := runValidationTests(ctx, setup.ConfigSpec, setup.Engine, setup.ValidationPaths, setup.Capabilities, logger)
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
	Engine          *templating.TemplateEngine
	ValidationPaths *dataplane.ValidationPaths
	Capabilities    dataplane.Capabilities
	Cleanup         func()
}

// setupValidation loads config, creates engine, and sets up validation paths.
func setupValidation(logger *slog.Logger) (*ValidationSetup, error) {
	// Load HAProxyTemplateConfig from file
	configSpec, err := loadConfigFromFile(validateConfigFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	// Check if config has validation tests
	if len(configSpec.ValidationTests) == 0 {
		return nil, fmt.Errorf("no validation tests found in config")
	}

	// Setup validation paths in temp directory
	// Pass configSpec so setupValidationPaths can derive subdirectory names from dataplane configuration
	validationPaths, capabilities, cleanupFunc, err := setupValidationPaths(configSpec)
	if err != nil {
		return nil, err
	}

	// Create template engine with custom filters
	engine, err := createTemplateEngine(configSpec, logger)
	if err != nil {
		cleanupFunc()
		return nil, err
	}

	// Enable template tracing if requested
	if validateTraceTemplates {
		engine.EnableTracing()
	}

	return &ValidationSetup{
		ConfigSpec:      configSpec,
		Engine:          engine,
		ValidationPaths: validationPaths,
		Capabilities:    capabilities,
		Cleanup:         cleanupFunc,
	}, nil
}

// runValidationTests executes the validation test suite.
func runValidationTests(
	ctx context.Context,
	configSpec *v1alpha1.HAProxyTemplateConfigSpec,
	engine *templating.TemplateEngine,
	validationPaths *dataplane.ValidationPaths,
	capabilities dataplane.Capabilities,
	logger *slog.Logger,
) (*testrunner.TestResults, error) {
	// Convert CRD spec to internal config format
	cfg, err := conversion.ConvertSpec(configSpec)
	if err != nil {
		return nil, fmt.Errorf("failed to convert config: %w", err)
	}

	// Create test runner
	runner := testrunner.New(
		cfg,
		engine,
		validationPaths,
		testrunner.Options{
			Logger:          logger,
			Workers:         validateWorkers,
			DebugFilters:    validateDebugFilters,
			ProfileIncludes: validateProfileIncludes,
			Capabilities:    capabilities,
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
func outputResults(results *testrunner.TestResults, engine *templating.TemplateEngine) error {
	// Format output
	output, err := testrunner.FormatResults(results, testrunner.OutputOptions{
		Format:  testrunner.OutputFormat(validateOutputFormat),
		Verbose: validateVerbose,
	})
	if err != nil {
		return fmt.Errorf("failed to format results: %w", err)
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
	fmt.Println("\n" + strings.Repeat("=", 80))
	fmt.Println("RENDERED CONTENT")
	fmt.Println(strings.Repeat("=", 80))

	for i := range results.TestResults {
		test := &results.TestResults[i]
		fmt.Printf("\n## Test: %s\n\n", test.TestName)

		if test.RenderedConfig != "" {
			fmt.Println("### haproxy.cfg")
			fmt.Println(strings.Repeat("-", 80))
			fmt.Println(test.RenderedConfig)
			fmt.Println(strings.Repeat("-", 80))
		}

		if len(test.RenderedMaps) > 0 {
			fmt.Println("\n### Map Files")
			for name, content := range test.RenderedMaps {
				fmt.Printf("\n#### %s\n", name)
				fmt.Println(strings.Repeat("-", 80))
				fmt.Println(content)
				fmt.Println(strings.Repeat("-", 80))
			}
		}

		if len(test.RenderedFiles) > 0 {
			fmt.Println("\n### General Files")
			for name, content := range test.RenderedFiles {
				fmt.Printf("\n#### %s\n", name)
				fmt.Println(strings.Repeat("-", 80))
				fmt.Println(content)
				fmt.Println(strings.Repeat("-", 80))
			}
		}

		if len(test.RenderedCerts) > 0 {
			fmt.Println("\n### SSL Certificates")
			for name, content := range test.RenderedCerts {
				fmt.Printf("\n#### %s\n", name)
				fmt.Println(strings.Repeat("-", 80))
				fmt.Println(content)
				fmt.Println(strings.Repeat("-", 80))
			}
		}
	}
}

// outputTemplateTrace prints template execution trace if available.
func outputTemplateTrace(engine *templating.TemplateEngine) {
	trace := engine.GetTraceOutput()
	if trace != "" {
		fmt.Println("\n" + strings.Repeat("=", 80))
		fmt.Println("TEMPLATE EXECUTION TRACE")
		fmt.Println(strings.Repeat("=", 80))
		fmt.Println(trace)
	}
}

// outputIncludeProfile prints include timing statistics from test results.
func outputIncludeProfile(results *testrunner.TestResults) {
	stats := aggregateIncludeStats(results)
	if len(stats) == 0 {
		return
	}

	printIncludeProfile(stats)
}

// aggregateIncludeStats collects and aggregates include statistics from all test results.
func aggregateIncludeStats(results *testrunner.TestResults) []templating.IncludeStats {
	aggregated := make(map[string]*templating.IncludeStats)

	for i := range results.TestResults {
		test := &results.TestResults[i]
		for _, stat := range test.IncludeStats {
			mergeIncludeStat(aggregated, stat)
		}
	}

	// Convert to slice and calculate averages
	stats := make([]templating.IncludeStats, 0, len(aggregated))
	for _, stat := range aggregated {
		if stat.Count > 0 {
			stat.AvgMs = stat.TotalMs / float64(stat.Count)
		}
		stats = append(stats, *stat)
	}

	// Sort by total time (slowest first)
	sortIncludeStatsByTotalTime(stats)

	return stats
}

// mergeIncludeStat merges a single include stat into the aggregation map.
func mergeIncludeStat(aggregated map[string]*templating.IncludeStats, stat templating.IncludeStats) {
	if existing, ok := aggregated[stat.Name]; ok {
		existing.Count += stat.Count
		existing.TotalMs += stat.TotalMs
		if stat.MaxMs > existing.MaxMs {
			existing.MaxMs = stat.MaxMs
		}
	} else {
		aggregated[stat.Name] = &templating.IncludeStats{
			Name:    stat.Name,
			Count:   stat.Count,
			TotalMs: stat.TotalMs,
			MaxMs:   stat.MaxMs,
		}
	}
}

// sortIncludeStatsByTotalTime sorts include stats by total time (slowest first).
func sortIncludeStatsByTotalTime(stats []templating.IncludeStats) {
	for i := 0; i < len(stats)-1; i++ {
		for j := i + 1; j < len(stats); j++ {
			if stats[j].TotalMs > stats[i].TotalMs {
				stats[i], stats[j] = stats[j], stats[i]
			}
		}
	}
}

// printIncludeProfile prints the formatted include timing profile.
func printIncludeProfile(stats []templating.IncludeStats) {
	fmt.Println("\n" + strings.Repeat("=", 80))
	fmt.Println("INCLUDE TIMING PROFILE (Top 20 slowest)")
	fmt.Println(strings.Repeat("=", 80))
	fmt.Printf("%-45s %8s %10s %10s %10s\n", "Include", "Count", "Total(ms)", "Avg(ms)", "Max(ms)")
	fmt.Println(strings.Repeat("-", 80))

	limit := 20
	if len(stats) < limit {
		limit = len(stats)
	}

	for i := 0; i < limit; i++ {
		stat := stats[i]
		fmt.Printf("%-45s %8d %10.2f %10.2f %10.2f\n",
			stat.Name, stat.Count, stat.TotalMs, stat.AvgMs, stat.MaxMs)
	}

	// Summary
	var totalTime float64
	var totalCalls int
	for _, stat := range stats {
		totalTime += stat.TotalMs
		totalCalls += stat.Count
	}
	fmt.Println(strings.Repeat("-", 80))
	fmt.Printf("%-45s %8d %10.2f\n", "TOTAL", totalCalls, totalTime)
}

// loadConfigFromFile loads a HAProxyTemplateConfig from a YAML file.
func loadConfigFromFile(filePath string) (*v1alpha1.HAProxyTemplateConfigSpec, error) {
	// Clean the file path to prevent path traversal attacks
	cleanPath := filepath.Clean(filePath)

	// Read file
	data, err := os.ReadFile(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

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
		return nil, fmt.Errorf("file does not contain HAProxyTemplateConfig")
	}

	// Fallback: Try parsing as raw YAML (for spec-only files)
	var spec v1alpha1.HAProxyTemplateConfigSpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	return &spec, nil
}

// createTemplateEngine creates and compiles the template engine from config spec with custom filters.
func createTemplateEngine(configSpec *v1alpha1.HAProxyTemplateConfigSpec, logger *slog.Logger) (*templating.TemplateEngine, error) {
	// Extract all template sources
	templates := make(map[string]string)

	// Main HAProxy config template
	templates["haproxy.cfg"] = configSpec.HAProxyConfig.Template

	// Template snippets
	for name, snippet := range configSpec.TemplateSnippets {
		templates[name] = snippet.Template
	}

	// Map files
	for name, mapFile := range configSpec.Maps {
		templates[name] = mapFile.Template
	}

	// General files
	for name, file := range configSpec.Files {
		templates[name] = file.Template
	}

	// SSL certificates
	for name, cert := range configSpec.SSLCertificates {
		templates[name] = cert.Template
	}

	// Register custom filters
	// Note: pathResolver is now passed via rendering context by TestRunner
	filters := map[string]templating.FilterFunc{
		"glob_match": templating.GlobMatch,
		"b64decode":  templating.B64Decode,
	}

	// Register custom global functions
	functions := map[string]templating.GlobalFunc{
		"fail": func(args ...interface{}) (interface{}, error) {
			if len(args) == 0 {
				return nil, fmt.Errorf("template evaluation failed")
			}
			return nil, fmt.Errorf("%v", args[0])
		},
	}

	// Compile all templates with custom filters and functions
	logger.Info("Compiling templates", "template_count", len(templates))
	engine, err := templating.New(templating.EngineTypeGonja, templates, filters, functions, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to compile templates: %w", err)
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
	cleanup func(),
	err error,
) {
	// Detect local HAProxy version to determine capabilities
	// CRT-list storage is only available in HAProxy 3.2+
	localVersion, err := dataplane.DetectLocalVersion()
	if err != nil {
		return nil, dataplane.Capabilities{}, nil, fmt.Errorf("failed to detect local HAProxy version: %w\nHint: Ensure 'haproxy' is in PATH", err)
	}
	capabilities = dataplane.CapabilitiesFromVersion(localVersion)

	// Create temporary directory
	tempDir, err := os.MkdirTemp("", "haproxy-validate-*")
	if err != nil {
		return nil, dataplane.Capabilities{}, nil, fmt.Errorf("failed to create temp dir: %w", err)
	}

	// Convert CRD spec to internal config format to get dataplane configuration with defaults applied
	cfg, err := conversion.ConvertSpec(configSpec)
	if err != nil {
		_ = os.RemoveAll(tempDir)
		return nil, dataplane.Capabilities{}, nil, fmt.Errorf("failed to convert config spec: %w", err)
	}

	// Derive subdirectory names from configured dataplane paths using filepath.Base()
	// This extracts the final directory name (e.g., "/etc/haproxy/maps" → "maps")
	// and maintains consistency with production while using relative paths for validation
	basePaths := dataplane.PathConfig{
		MapsDir:    filepath.Join(tempDir, filepath.Base(cfg.Dataplane.MapsDir)),
		SSLDir:     filepath.Join(tempDir, filepath.Base(cfg.Dataplane.SSLCertsDir)),
		GeneralDir: filepath.Join(tempDir, filepath.Base(cfg.Dataplane.GeneralStorageDir)),
		ConfigFile: filepath.Join(tempDir, "haproxy.cfg"),
	}

	// Use centralized path resolution to get capability-aware paths
	// This ensures CRTListDir is set correctly for HAProxy < 3.2
	resolvedPaths := dataplane.ResolvePaths(basePaths, capabilities)

	// Create directories (include CRTListDir which may be same as GeneralDir)
	dirsToCreate := []string{resolvedPaths.MapsDir, resolvedPaths.SSLDir, resolvedPaths.GeneralDir}
	if resolvedPaths.CRTListDir != resolvedPaths.SSLDir && resolvedPaths.CRTListDir != resolvedPaths.GeneralDir {
		dirsToCreate = append(dirsToCreate, resolvedPaths.CRTListDir)
	}

	for _, dir := range dirsToCreate {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			_ = os.RemoveAll(tempDir)
			return nil, dataplane.Capabilities{}, nil, fmt.Errorf("failed to create directory: %w", err)
		}
	}

	cleanup = func() {
		_ = os.RemoveAll(tempDir)
	}

	return resolvedPaths.ToValidationPaths(), capabilities, cleanup, nil
}
