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
	"reflect"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/helpers"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/names"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/renderer"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testrunner"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/typebootstrap"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// compileTemplatesForBenchmark compiles templates with optional profiling. It
// folds the typebootstrap result into the engine's declarations (the same
// BuildAdditionalDeclarations path validate/production use) so typed-resource
// access (`gateways`, typed `resources.<name>.List()`) compiles; typedResult is
// empty when no --schema-dir was supplied, in which case a typed-access template
// fails at compile time with a clear pointer back to --schema-dir, exactly as
// validate does.
func compileTemplatesForBenchmark(cfg *config.Config, typedResult *typebootstrap.Result) (templating.Engine, error) {
	// Benchmark doesn't need currentConfig type registration.
	additionalDeclarations := helpers.BuildAdditionalDeclarations(cfg, typedResult)
	return helpers.NewEngineFromConfigWithOptions(cfg, nil, nil, additionalDeclarations, helpers.EngineOptions{
		EnableProfiling: benchmarkProfileIncludes,
	})
}

// renderSingleTemplate renders a single template and returns timing and profiling stats.
func renderSingleTemplate(
	ctx context.Context,
	engine templating.Engine,
	templateName string,
	displayName string,
	renderCtx map[string]any,
) (FileRenderResult, []templating.IncludeStats, error) {
	start := time.Now()
	var stats []templating.IncludeStats
	ctx = templating.WithIncrementalScope(ctx, templateName)

	if benchmarkProfileIncludes {
		_, profileStats, err := engine.RenderWithProfiling(ctx, templateName, renderCtx)
		if err != nil {
			return FileRenderResult{}, nil, err
		}
		stats = profileStats
	} else {
		_, err := engine.Render(ctx, templateName, renderCtx)
		if err != nil {
			return FileRenderResult{}, nil, err
		}
	}

	return FileRenderResult{
		Name:     displayName,
		Duration: time.Since(start),
	}, stats, nil
}

// renderMainConfig renders and assembles haproxy.cfg, returning its timing and
// profiling stats.
func renderMainConfig(ctx context.Context, engine templating.Engine, bctx *rendercontext.BuildResult) (FileRenderResult, []templating.IncludeStats, error) {
	start := time.Now()
	ctx = templating.WithIncrementalScope(ctx, names.MainTemplateName)
	main, err := rendercontext.RenderMain(ctx, engine, bctx.Context, bctx.PlanRegistry, benchmarkProfileIncludes)
	if err != nil {
		return FileRenderResult{}, nil, err
	}
	return FileRenderResult{
		Name:     names.MainTemplateName,
		Duration: time.Since(start),
	}, main.IncludeStats, nil
}

// templateGroup defines a group of templates to render with a display prefix.
type templateGroup struct {
	names  []string // sorted template names
	prefix string   // display prefix (e.g., "map:", "file:", "cert:")
}

// renderAllFiles renders every production root and returns timing for each.
func renderAllFiles(
	engine templating.Engine,
	cfg *config.Config,
	bctx *rendercontext.BuildResult,
	storeMap map[string]stores.Store,
	typedResourceTypes map[string]reflect.Type,
	logger *slog.Logger,
) (IterationResult, error) {
	var result IterationResult
	totalStart := time.Now()
	renderCtx := bctx.Context
	ctx := context.Background()
	coldRender, err := renderer.NewColdIncrementalRender(ctx, &renderer.ColdIncrementalRenderConfig{
		Config:             cfg,
		Engine:             engine,
		StoreProvider:      stores.NewRealStoreProvider(storeMap),
		Mode:               rendercontext.RenderModeReconcile,
		TemplateContext:    renderCtx,
		ResourceErrors:     bctx.ResourceErrors,
		Logger:             logger,
		TypedResourceTypes: typedResourceTypes,
	})
	if err != nil {
		return result, fmt.Errorf("starting cold incremental benchmark render: %w", err)
	}
	ctx = coldRender.Context(ctx)

	// Collect all include stats across renders when profiling is enabled
	var allIncludeStats []templating.IncludeStats

	// Render and assemble haproxy.cfg through the production path, so the
	// benchmark measures what the controller actually does per render.
	fileResult, stats, err := renderMainConfig(ctx, engine, bctx)
	if err != nil {
		return result, fmt.Errorf("rendering %s: %w", names.MainTemplateName, err)
	}
	result.FileResults = append(result.FileResults, fileResult)
	allIncludeStats = append(allIncludeStats, stats...)

	// Render the remaining roots in sorted order.
	groups := []templateGroup{
		{names: sortedKeys(cfg.Maps), prefix: "map:"},
		{names: sortedKeys(cfg.Files), prefix: "file:"},
		{names: sortedKeys(cfg.SSLCertificates), prefix: "cert:"},
		{names: sortedKeys(cfg.K8sResources), prefix: "k8s:"},
	}
	for _, group := range groups {
		for _, name := range group.names {
			fileResult, stats, err := renderSingleTemplate(ctx, engine, name, group.prefix+name, renderCtx)
			if err != nil {
				return result, fmt.Errorf("rendering %s%s: %w", group.prefix, name, err)
			}
			result.FileResults = append(result.FileResults, fileResult)
			allIncludeStats = append(allIncludeStats, stats...)
		}
	}
	if err := coldRender.ValidateIncrementalCalls(); err != nil {
		return result, err
	}

	// Store aggregated include stats in result
	result.IncludeStats = allIncludeStats

	result.TotalTime = time.Since(totalStart)
	return result, nil
}

// createStoresForBenchmark creates resource stores from test fixtures.
func createStoresForBenchmark(cfg *config.Config, engine templating.Engine, fixtures map[string][]any) (map[string]stores.Store, error) {
	// Create a minimal runner just to use its fixture processing
	runner := testrunner.New(cfg, engine, nil, &testrunner.Options{})
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
	typedResourceTypes map[string]reflect.Type,
	logger *slog.Logger,
) *rendercontext.BuildResult {
	// Create PathResolver from ValidationPaths
	pathResolver := rendercontext.PathResolverFromValidationPaths(validationPaths)

	// Separate haproxy-pods from resource stores (goes in controller namespace)
	resourceStores, haproxyPodStore := rendercontext.SeparateHAProxyPodStore(storeMap)

	// Build context using centralized builder. typedResourceTypes wires the same
	// typed store wrappers (`resources.<name>.List()` returning typed pointers)
	// the engine compiled against, so typed-access templates render identically
	// to production; it's empty when no --schema-dir was supplied.
	builder := rendercontext.NewBuilder(
		context.Background(),
		cfg,
		pathResolver,
		logger,
		rendercontext.WithStores(resourceStores),
		rendercontext.WithHAProxyPodStore(haproxyPodStore),
		rendercontext.WithHTTPFetcher(httpStore),
		rendercontext.WithTypedResources(typedResourceTypes),
	)

	return builder.Build()
}

// freshBenchmarkContext builds a render context for one render. Each render must
// get its own: a reused context keeps the first_seen() dedup cache, so a sharded
// section emitted once is skipped as "already seen" on the next render (#165).
// Matches production, which builds a fresh context per reconcile. Stores are
// reused; only render-scoped state (SharedContext, PlanRegistry, collectors) resets.
func freshBenchmarkContext(
	cfg *config.Config,
	testExtra map[string]any,
	storeMap map[string]stores.Store,
	validationPaths *dataplane.ValidationPaths,
	httpStore *testrunner.FixtureHTTPStoreWrapper,
	typedResourceTypes map[string]reflect.Type,
	logger *slog.Logger,
) *rendercontext.BuildResult {
	bctx := buildBenchmarkContext(cfg, storeMap, validationPaths, httpStore, typedResourceTypes, logger)
	// Fold in the _global + per-test extraContext baseline so the benchmark
	// renders each test exactly as the load gate does (against the isolated
	// synthetic default certificate), not the deployment's production
	// extraContext — otherwise a custom defaultSSLCertificate name fails here.
	testrunner.ApplyTestExtraContext(bctx.Context, cfg, testExtra)
	return bctx
}
