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
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/conversion"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/names"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testrunner"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

var (
	benchmarkConfigFile      string
	benchmarkTestNames       []string
	benchmarkIterations      int
	benchmarkProfileIncludes bool
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

func runBenchmark(_ *cobra.Command, _ []string) error {
	// Setup logging (minimal)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	}))
	slog.SetDefault(logger)

	// Load config
	configSpec, err := loadConfigFromFile(benchmarkConfigFile)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Convert to internal config
	cfg, err := conversion.ConvertSpec(configSpec)
	if err != nil {
		return fmt.Errorf("failed to convert config: %w", err)
	}

	// If no tests specified, run all (except _global)
	if len(benchmarkTestNames) == 0 {
		for name := range cfg.ValidationTests {
			if name != "_global" {
				benchmarkTestNames = append(benchmarkTestNames, name)
			}
		}
		sort.Strings(benchmarkTestNames) // Deterministic order
	}

	// Validate all specified tests exist
	for _, testName := range benchmarkTestNames {
		if _, exists := cfg.ValidationTests[testName]; !exists {
			return fmt.Errorf("test %q not found in config", testName)
		}
	}

	if len(benchmarkTestNames) == 0 {
		return fmt.Errorf("no validation tests found in config")
	}

	// Setup validation paths
	validationPaths, _, cleanupFunc, err := setupValidationPaths(configSpec)
	if err != nil {
		return err
	}
	defer cleanupFunc()

	// Step 1: Compile templates (timed) - ONCE for all tests
	fmt.Println("Compiling templates...")
	compileStart := time.Now()
	engine, err := compileTemplatesForBenchmark(cfg)
	if err != nil {
		return fmt.Errorf("failed to compile templates: %w", err)
	}
	compilationTime := time.Since(compileStart)

	// Step 2: Run benchmark for each test
	results := make([]*BenchmarkResult, 0, len(benchmarkTestNames))

	for _, testName := range benchmarkTestNames {
		result, err := runSingleTestBenchmark(cfg, engine, testName, validationPaths, logger)
		if err != nil {
			return fmt.Errorf("benchmark for test %q failed: %w", testName, err)
		}
		results = append(results, result)
	}

	// Step 3: Output results for all tests
	outputAllBenchmarkResults(results, compilationTime, cfg)

	return nil
}

// runSingleTestBenchmark runs the benchmark for a single test.
func runSingleTestBenchmark(
	cfg *config.Config,
	engine templating.Engine,
	testName string,
	validationPaths *dataplane.ValidationPaths,
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

	// Create stores from fixtures
	storeMap, err := createStoresForBenchmark(cfg, engine, fixtures)
	if err != nil {
		return nil, fmt.Errorf("failed to create fixture stores: %w", err)
	}

	// Create HTTP store
	httpStore := createHTTPStoreForBenchmark(httpFixtures, logger)

	// Build render context
	renderCtx := buildBenchmarkContext(cfg, storeMap, validationPaths, httpStore, logger)

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

// compileTemplatesForBenchmark compiles templates with optional profiling.
func compileTemplatesForBenchmark(cfg *config.Config) (templating.Engine, error) {
	// Enable profiling if --profile-includes flag is set
	options := helpers.EngineOptions{
		EnableProfiling: benchmarkProfileIncludes,
	}
	// Benchmark doesn't need currentConfig type registration
	engine, err := helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, nil, options)
	if err != nil {
		return nil, err
	}

	return engine, nil
}

// renderSingleTemplate renders a single template and returns timing and profiling stats.
func renderSingleTemplate(
	engine templating.Engine,
	templateName string,
	displayName string,
	renderCtx map[string]interface{},
) (FileRenderResult, []templating.IncludeStats, error) {
	start := time.Now()
	var stats []templating.IncludeStats

	if benchmarkProfileIncludes {
		_, profileStats, err := engine.RenderWithProfiling(context.Background(), templateName, renderCtx)
		if err != nil {
			return FileRenderResult{}, nil, err
		}
		stats = profileStats
	} else {
		_, err := engine.Render(context.Background(), templateName, renderCtx)
		if err != nil {
			return FileRenderResult{}, nil, err
		}
	}

	return FileRenderResult{
		Name:     displayName,
		Duration: time.Since(start),
	}, stats, nil
}

// renderAllFiles renders all templates (haproxy.cfg + maps + files + certs) and returns timing for each.
func renderAllFiles(engine templating.Engine, cfg *config.Config, renderCtx map[string]interface{}) (IterationResult, error) {
	result := IterationResult{
		FileResults: make([]FileRenderResult, 0),
	}
	totalStart := time.Now()

	// Collect all include stats across renders when profiling is enabled
	var allIncludeStats []templating.IncludeStats

	// Render haproxy.cfg
	fileResult, stats, err := renderSingleTemplate(engine, names.MainTemplateName, names.MainTemplateName, renderCtx)
	if err != nil {
		return result, fmt.Errorf("failed to render %s: %w", names.MainTemplateName, err)
	}
	result.FileResults = append(result.FileResults, fileResult)
	allIncludeStats = append(allIncludeStats, stats...)

	// Render map files (sorted for consistent output order)
	mapNames := sortedMapKeys(cfg.Maps)
	for _, name := range mapNames {
		fileResult, stats, err := renderSingleTemplate(engine, name, "map:"+name, renderCtx)
		if err != nil {
			return result, fmt.Errorf("failed to render map %s: %w", name, err)
		}
		result.FileResults = append(result.FileResults, fileResult)
		allIncludeStats = append(allIncludeStats, stats...)
	}

	// Render general files (sorted for consistent output order)
	fileNames := sortedFileKeys(cfg.Files)
	for _, name := range fileNames {
		fileResult, stats, err := renderSingleTemplate(engine, name, "file:"+name, renderCtx)
		if err != nil {
			return result, fmt.Errorf("failed to render file %s: %w", name, err)
		}
		result.FileResults = append(result.FileResults, fileResult)
		allIncludeStats = append(allIncludeStats, stats...)
	}

	// Render SSL certificates (sorted for consistent output order)
	certNames := sortedCertKeys(cfg.SSLCertificates)
	for _, name := range certNames {
		fileResult, stats, err := renderSingleTemplate(engine, name, "cert:"+name, renderCtx)
		if err != nil {
			return result, fmt.Errorf("failed to render cert %s: %w", name, err)
		}
		result.FileResults = append(result.FileResults, fileResult)
		allIncludeStats = append(allIncludeStats, stats...)
	}

	// Store aggregated include stats in result
	result.IncludeStats = allIncludeStats

	result.TotalTime = time.Since(totalStart)
	return result, nil
}

// sortedMapKeys returns sorted keys from a map config.
func sortedMapKeys(maps map[string]config.MapFile) []string {
	keys := make([]string, 0, len(maps))
	for name := range maps {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	return keys
}

// sortedFileKeys returns sorted keys from a file config.
func sortedFileKeys(files map[string]config.GeneralFile) []string {
	keys := make([]string, 0, len(files))
	for name := range files {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	return keys
}

// sortedCertKeys returns sorted keys from a certificate config.
func sortedCertKeys(certs map[string]config.SSLCertificate) []string {
	keys := make([]string, 0, len(certs))
	for name := range certs {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	return keys
}

// createStoresForBenchmark creates resource stores from test fixtures.
func createStoresForBenchmark(cfg *config.Config, engine templating.Engine, fixtures map[string][]interface{}) (map[string]stores.Store, error) {
	// Create a minimal runner just to use its fixture processing
	runner := testrunner.New(cfg, engine, nil, testrunner.Options{})
	return runner.CreateStoresFromFixtures(fixtures)
}

// createHTTPStoreForBenchmark creates an HTTP fixture store.
func createHTTPStoreForBenchmark(httpFixtures []config.HTTPResourceFixture, logger *slog.Logger) *testrunner.FixtureHTTPStoreWrapper {
	store := testrunner.CreateHTTPStoreFromFixtures(httpFixtures, logger)
	return testrunner.NewFixtureHTTPStoreWrapper(store, logger)
}

// buildBenchmarkContext builds the template rendering context.
//
// This method delegates to the centralized rendercontext.Builder to ensure consistent
// context creation across all usages (renderer, testrunner, benchmark, dryrunvalidator).
func buildBenchmarkContext(
	cfg *config.Config,
	storeMap map[string]stores.Store,
	validationPaths *dataplane.ValidationPaths,
	httpStore *testrunner.FixtureHTTPStoreWrapper,
	logger *slog.Logger,
) map[string]interface{} {
	// Create PathResolver from ValidationPaths
	pathResolver := rendercontext.PathResolverFromValidationPaths(validationPaths)

	// Separate haproxy-pods from resource stores (goes in controller namespace)
	resourceStores, haproxyPodStore := rendercontext.SeparateHAProxyPodStore(storeMap)

	// Build context using centralized builder
	builder := rendercontext.NewBuilder(
		cfg,
		pathResolver,
		logger,
		rendercontext.WithStores(resourceStores),
		rendercontext.WithHAProxyPodStore(haproxyPodStore),
		rendercontext.WithHTTPFetcher(httpStore),
	)

	renderCtx, _ := builder.Build()
	return renderCtx
}

// tableLayout holds the calculated column widths for benchmark table output.
type tableLayout struct {
	timeWidth    int // Width for each time value
	fileColWidth int // File name column width
	iterColWidth int // Width for iterations group per test
}

// outputAllBenchmarkResults formats and prints benchmark results as a table.
func outputAllBenchmarkResults(results []*BenchmarkResult, compilationTime time.Duration, _ *config.Config) {
	if len(results) == 0 {
		fmt.Println("No benchmark results to display")
		return
	}

	fmt.Println()
	fmt.Println("BENCHMARK RESULTS")
	fmt.Println(strings.Repeat("=", 80))
	fmt.Printf("\nCompilation: %.3fms\n\n", float64(compilationTime.Microseconds())/1000)

	fileNames := extractFileNames(results)
	layout := calculateTableLayout(results)

	printTableHeader(results, layout)
	printTableSeparator(results, layout)
	printTableDataRows(results, fileNames, layout)
	printTableSeparator(results, layout)
	printTableTotalRow(results, layout)

	fmt.Println()

	// Output include profile if profiling was enabled
	if benchmarkProfileIncludes {
		outputBenchmarkIncludeProfile(results)
	}
}

// extractFileNames gets file names from the first result (all tests render same files).
func extractFileNames(results []*BenchmarkResult) []string {
	fileNames := make([]string, 0)
	if len(results[0].Iterations) > 0 {
		for _, fr := range results[0].Iterations[0].FileResults {
			fileNames = append(fileNames, fr.Name)
		}
	}
	return fileNames
}

// calculateTableLayout determines column widths for the table.
func calculateTableLayout(results []*BenchmarkResult) tableLayout {
	layout := tableLayout{
		timeWidth:    7,
		fileColWidth: 30,
	}
	layout.iterColWidth = layout.timeWidth*benchmarkIterations + benchmarkIterations - 1

	for _, r := range results {
		shortName := shortenTestName(r.TestName)
		if len(shortName) > layout.iterColWidth {
			layout.iterColWidth = len(shortName)
		}
	}
	return layout
}

// printTableHeader prints the two header rows (test names and iteration numbers).
func printTableHeader(results []*BenchmarkResult, layout tableLayout) {
	// Row 1: test names
	fmt.Printf("%-*s", layout.fileColWidth, "")
	for _, r := range results {
		fmt.Printf(" | %-*s", layout.iterColWidth, shortenTestName(r.TestName))
	}
	fmt.Println(" |")

	// Row 2: iteration numbers
	fmt.Printf("%-*s", layout.fileColWidth, "File")
	for range results {
		printIterationHeaders(layout)
	}
	fmt.Println(" |")
}

// printIterationHeaders prints the iteration column headers for one test.
func printIterationHeaders(layout tableLayout) {
	fmt.Print(" | ")
	for i := 0; i < benchmarkIterations; i++ {
		if i > 0 {
			fmt.Print(" ")
		}
		fmt.Printf("%*s", layout.timeWidth, fmt.Sprintf("It%d", i+1))
	}
	printColumnPadding(layout)
}

// printTableSeparator prints a separator line.
func printTableSeparator(results []*BenchmarkResult, layout tableLayout) {
	fmt.Print(strings.Repeat("-", layout.fileColWidth))
	for range results {
		fmt.Print("-|-")
		fmt.Print(strings.Repeat("-", layout.iterColWidth))
	}
	fmt.Println("-|")
}

// printTableDataRows prints the data rows for each file.
func printTableDataRows(results []*BenchmarkResult, fileNames []string, layout tableLayout) {
	for fileIdx, fileName := range fileNames {
		fmt.Printf("%-*s", layout.fileColWidth, truncateString(fileName, layout.fileColWidth))
		for _, r := range results {
			printFileTimings(r, fileIdx, layout)
		}
		fmt.Println(" |")
	}
}

// printFileTimings prints the timing values for one file across all iterations of a test.
func printFileTimings(result *BenchmarkResult, fileIdx int, layout tableLayout) {
	fmt.Print(" | ")
	for i, iter := range result.Iterations {
		if i > 0 {
			fmt.Print(" ")
		}
		if fileIdx < len(iter.FileResults) {
			fmt.Printf("%*.2f", layout.timeWidth, float64(iter.FileResults[fileIdx].Duration.Microseconds())/1000)
		} else {
			fmt.Printf("%*s", layout.timeWidth, "-")
		}
	}
	printColumnPadding(layout)
}

// printTableTotalRow prints the TOTAL row with iteration totals.
func printTableTotalRow(results []*BenchmarkResult, layout tableLayout) {
	fmt.Printf("%-*s", layout.fileColWidth, "TOTAL")
	for _, r := range results {
		printTotalTimings(r, layout)
	}
	fmt.Println(" |")
}

// printTotalTimings prints the total timing values for all iterations of a test.
func printTotalTimings(result *BenchmarkResult, layout tableLayout) {
	fmt.Print(" | ")
	for i, iter := range result.Iterations {
		if i > 0 {
			fmt.Print(" ")
		}
		fmt.Printf("%*.2f", layout.timeWidth, float64(iter.TotalTime.Microseconds())/1000)
	}
	printColumnPadding(layout)
}

// printColumnPadding prints padding to align columns.
func printColumnPadding(layout tableLayout) {
	padding := layout.iterColWidth - (layout.timeWidth*benchmarkIterations + benchmarkIterations - 1)
	if padding > 0 {
		fmt.Print(strings.Repeat(" ", padding))
	}
}

// shortenTestName removes common prefixes for compact display.
func shortenTestName(name string) string {
	name = strings.TrimPrefix(name, "benchmark-")
	return name
}

// truncateString truncates a string to maxLen, adding "..." if truncated.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// outputBenchmarkIncludeProfile outputs aggregated include timing statistics.
func outputBenchmarkIncludeProfile(results []*BenchmarkResult) {
	// Aggregate stats across all tests and iterations
	stats := aggregateBenchmarkIncludeStats(results)
	if len(stats) == 0 {
		return
	}

	fmt.Println(strings.Repeat("=", 80))
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
	fmt.Println()
}

// aggregateBenchmarkIncludeStats collects and aggregates include statistics from all benchmark results.
func aggregateBenchmarkIncludeStats(results []*BenchmarkResult) []templating.IncludeStats {
	aggregated := make(map[string]*templating.IncludeStats)

	for _, result := range results {
		for _, iter := range result.Iterations {
			for _, stat := range iter.IncludeStats {
				mergeBenchmarkIncludeStat(aggregated, stat)
			}
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
	sort.Slice(stats, func(i, j int) bool {
		return stats[i].TotalMs > stats[j].TotalMs
	})

	return stats
}

// mergeBenchmarkIncludeStat merges a single include stat into the aggregation map.
func mergeBenchmarkIncludeStat(aggregated map[string]*templating.IncludeStats, stat templating.IncludeStats) {
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
