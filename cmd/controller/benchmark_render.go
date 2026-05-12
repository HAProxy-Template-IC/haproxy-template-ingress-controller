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
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/names"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testrunner"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// compileTemplatesForBenchmark compiles templates with optional profiling.
func compileTemplatesForBenchmark(cfg *config.Config) (templating.Engine, error) {
	// Benchmark doesn't need currentConfig type registration
	return helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, nil, helpers.EngineOptions{
		EnableProfiling: benchmarkProfileIncludes,
	})
}

// renderSingleTemplate renders a single template and returns timing and profiling stats.
func renderSingleTemplate(
	engine templating.Engine,
	templateName string,
	displayName string,
	renderCtx map[string]any,
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

// templateGroup defines a group of templates to render with a display prefix.
type templateGroup struct {
	names  []string // sorted template names
	prefix string   // display prefix (e.g., "map:", "file:", "cert:")
}

// renderAllFiles renders all templates (haproxy.cfg + maps + files + certs) and returns timing for each.
func renderAllFiles(engine templating.Engine, cfg *config.Config, renderCtx map[string]any) (IterationResult, error) {
	var result IterationResult
	totalStart := time.Now()

	// Collect all include stats across renders when profiling is enabled
	var allIncludeStats []templating.IncludeStats

	// Render haproxy.cfg
	fileResult, stats, err := renderSingleTemplate(engine, names.MainTemplateName, names.MainTemplateName, renderCtx)
	if err != nil {
		return result, fmt.Errorf("rendering %s: %w", names.MainTemplateName, err)
	}
	result.FileResults = append(result.FileResults, fileResult)
	allIncludeStats = append(allIncludeStats, stats...)

	// Render auxiliary files (maps, general files, SSL certificates) in sorted order
	groups := []templateGroup{
		{names: sortedKeys(cfg.Maps), prefix: "map:"},
		{names: sortedKeys(cfg.Files), prefix: "file:"},
		{names: sortedKeys(cfg.SSLCertificates), prefix: "cert:"},
	}
	for _, group := range groups {
		for _, name := range group.names {
			fileResult, stats, err := renderSingleTemplate(engine, name, group.prefix+name, renderCtx)
			if err != nil {
				return result, fmt.Errorf("rendering %s%s: %w", group.prefix, name, err)
			}
			result.FileResults = append(result.FileResults, fileResult)
			allIncludeStats = append(allIncludeStats, stats...)
		}
	}

	// Store aggregated include stats in result
	result.IncludeStats = allIncludeStats

	result.TotalTime = time.Since(totalStart)
	return result, nil
}

// createStoresForBenchmark creates resource stores from test fixtures.
func createStoresForBenchmark(cfg *config.Config, engine templating.Engine, fixtures map[string][]any) (map[string]stores.Store, error) {
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
) map[string]any {
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

	return builder.Build().Context
}
