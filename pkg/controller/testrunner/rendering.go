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
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"strings"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/names"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"

	"gitlab.com/haproxy-haptic/haptic/pkg/core/logging"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// createTestPaths creates per-test temp directories for isolated HAProxy validation.
//
// This creates a subdirectory structure under the base temp directory:
//
//	<base>/worker-<workerID>/test-<testNum>/maps/
//	<base>/worker-<workerID>/test-<testNum>/ssl/
//	<base>/worker-<workerID>/test-<testNum>/files/
//	<base>/worker-<workerID>/test-<testNum>/haproxy.cfg
//
// Each test gets its own isolated directories to prevent file conflicts during
// parallel test execution, even when multiple tests are processed by the same worker.
func (r *Runner) createTestPaths(workerID, testNum int) (*dataplane.ValidationPaths, error) {
	// Extract base temp directory from the shared validation paths
	baseTempDir := filepath.Dir(r.validationPaths.ConfigFile)

	// Create test-specific subdirectory within worker space
	testDir := filepath.Join(baseTempDir, fmt.Sprintf("worker-%d", workerID), fmt.Sprintf("test-%d", testNum))

	// Create base path configuration
	// IMPORTANT: Subdirectory names are derived from configured dataplane paths
	// using path.Base() (slash-only — the configured dirs are HAProxy target
	// paths) to ensure consistency between production and validation.
	// HAProxy requires absolute paths to locate files, so we create absolute paths
	// within the isolated test directory (e.g., /tmp/haproxy-validate-12345/worker-0/test-1/maps).
	basePaths := dataplane.PathConfig{
		MapsDir:    filepath.Join(testDir, path.Base(r.config.Dataplane.MapsDir)),
		SSLDir:     filepath.Join(testDir, path.Base(r.config.Dataplane.SSLCertsDir)),
		GeneralDir: filepath.Join(testDir, path.Base(r.config.Dataplane.GeneralStorageDir)),
		ConfigFile: filepath.Join(testDir, names.MainTemplateName),
	}

	// Use centralized path resolution to get capability-aware paths
	// This ensures CRTListDir is set correctly for HAProxy < 3.2
	resolvedPaths := dataplane.ResolvePaths(basePaths)

	// Browser/WASM: no writable filesystem. Nothing writes to these paths when
	// binary validation is skipped (render is in-memory; haproxy_valid uses the
	// pure-Go check), so return the resolved path strings without MkdirAll.
	if r.skipBinaryValidation {
		return resolvedPaths.ToValidationPaths(), nil
	}

	// Create all directories (CRTListDir may be same as GeneralDir or SSLDir)
	dirsToCreate := []string{resolvedPaths.MapsDir, resolvedPaths.SSLDir, resolvedPaths.GeneralDir}
	if resolvedPaths.CRTListDir != resolvedPaths.SSLDir && resolvedPaths.CRTListDir != resolvedPaths.GeneralDir {
		dirsToCreate = append(dirsToCreate, resolvedPaths.CRTListDir)
	}

	for _, dir := range dirsToCreate {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, fmt.Errorf("creating test directory %s: %w", dir, err)
		}
	}

	return resolvedPaths.ToValidationPaths(), nil
}

// RenderOutput bundles every artifact produced by a single test render so
// callers don't have to thread six positional return values.
type RenderOutput struct {
	HAProxyConfig  string
	AuxiliaryFiles *dataplane.AuxiliaryFiles
	K8sResources   map[string]string
	StatusPatches  map[string]string
	// Events is the newline-joined serialization of the Kubernetes Events the
	// templates recorded via recordEvent(), one per line, so validation tests
	// can assert on them with the `target: events` resolver.
	Events       string
	IncludeStats []templating.IncludeStats
}

// renderWithStores renders HAProxy configuration using test fixture stores and worker-specific engine.
//
// This follows the same pattern as DryRunValidator.renderWithOverlayStores.
// When profileIncludes is enabled, it returns timing statistics for included templates.
// The currentConfig parameter enables slot-aware server assignment testing (nil for first deployment).
// The testExtraContext parameter allows test-specific extraContext values to override global ones.
//
// Returns rendered haproxy.cfg, auxiliary files, k8sResources (template name → YAML),
// status patches (key `<ns>/<name>:<phase>` → JSON-marshalled status content), and
// include-stats (when profiling) bundled in a RenderOutput, plus the render error.
func (r *Runner) renderWithStores(ctx context.Context, engine templating.Engine, storeMap map[string]stores.Store, validationPaths *dataplane.ValidationPaths, httpStore *FixtureHTTPStoreWrapper, currentConfig *parserconfig.StructuredConfig, currentFiles map[string]string, testExtraContext map[string]any) (RenderOutput, error) {
	// Build rendering context with fixture stores
	renderCtx := r.buildRenderingContext(ctx, storeMap, validationPaths, httpStore, currentConfig, currentFiles)

	mergeTestExtraContext(renderCtx, testExtraContext)

	// Render main HAProxy configuration using worker-specific engine
	var haproxyConfig string
	var includeStats []templating.IncludeStats
	var err error

	if r.profileIncludes {
		haproxyConfig, includeStats, err = engine.RenderWithProfiling(ctx, names.MainTemplateName, renderCtx)
	} else {
		haproxyConfig, err = engine.Render(ctx, names.MainTemplateName, renderCtx)
	}
	if err != nil {
		return RenderOutput{}, fmt.Errorf("rendering %s: %w", names.MainTemplateName, err)
	}

	// Render auxiliary files using worker-specific engine (pre-declared files)
	staticFiles, err := r.renderAuxiliaryFiles(ctx, engine, renderCtx, validationPaths)
	if err != nil {
		return RenderOutput{}, fmt.Errorf("rendering auxiliary files: %w", err)
	}

	// Render k8sResources templates using the worker-specific engine. These
	// are surfaced into the test result so assertions can target them via
	// `target: k8s:<template-name>` and the --dump-rendered flag can show
	// them alongside haproxy.cfg / map files.
	k8sResources := make(map[string]string, len(r.config.K8sResources))
	for name := range r.config.K8sResources {
		rendered, err := engine.Render(ctx, name, renderCtx)
		if err != nil {
			return RenderOutput{}, fmt.Errorf("rendering k8sResources %s: %w", name, err)
		}
		k8sResources[name] = rendered
	}

	statusPatches, err := collectStatusPatches(renderCtx)
	if err != nil {
		return RenderOutput{}, err
	}

	renderedEvents := collectEvents(renderCtx)

	// Extract dynamic files registered during template rendering
	fileRegistry := renderCtx["fileRegistry"].(*rendercontext.FileRegistry)
	dynamicFiles := fileRegistry.GetFiles()

	// Merge static (pre-declared) and dynamic (registered) files
	auxiliaryFiles := rendercontext.MergeAuxiliaryFiles(staticFiles, dynamicFiles)

	// Debug logging
	staticCount := len(staticFiles.MapFiles) + len(staticFiles.GeneralFiles) + len(staticFiles.SSLCertificates) + len(staticFiles.CRTListFiles)
	dynamicCount := len(dynamicFiles.MapFiles) + len(dynamicFiles.GeneralFiles) + len(dynamicFiles.SSLCertificates) + len(dynamicFiles.CRTListFiles)
	if dynamicCount > 0 {
		r.logger.Log(context.Background(), logging.LevelTrace, "Merged auxiliary files",
			"static_count", staticCount,
			"dynamic_count", dynamicCount)
	}

	return RenderOutput{
		HAProxyConfig:  haproxyConfig,
		AuxiliaryFiles: auxiliaryFiles,
		K8sResources:   k8sResources,
		StatusPatches:  statusPatches,
		Events:         renderedEvents,
		IncludeStats:   includeStats,
	}, nil
}

// mergeTestExtraContext folds a per-test extraContext map into the rendering
// context built from the global config. Nested maps merge recursively with
// per-test leaves winning — the same mergeOverwrite semantics the chart uses
// for extraContext — so a test overriding one key of a subtree (for example
// tls.hsts.enabled) doesn't clobber sibling keys the chart set (for example
// tls.defaultCertificate). The merge builds fresh maps along every merged
// path (never mutating the shared global map) so parallel test workers don't
// leak state into each other.
func mergeTestExtraContext(renderCtx, testExtraContext map[string]any) {
	if testExtraContext == nil {
		return
	}
	globalExtraContext := renderCtx["extraContext"].(map[string]any)
	merged := deepMergeMaps(globalExtraContext, testExtraContext)
	for key := range testExtraContext {
		// Also merge into top-level context for direct access.
		renderCtx[key] = merged[key]
	}
	renderCtx["extraContext"] = merged
}

// foldGlobalExtraContext folds a per-test extraContext onto the _global
// validationTest's shared extraContext baseline (baseline first, per-test wins),
// or returns testExtra unchanged when _global declares none. This is the single
// source of the production < _global < per-test precedence every validationTest
// render site relies on.
func foldGlobalExtraContext(cfg *config.Config, testExtra map[string]any) map[string]any {
	if globalTest, ok := cfg.ValidationTests["_global"]; ok && len(globalTest.ExtraContext) > 0 {
		return deepMergeMaps(globalTest.ExtraContext, testExtra)
	}
	return testExtra
}

// ApplyTestExtraContext folds the _global baseline and a per-test extraContext
// (pass the test's ExtraContext) into an already-built render context (whose
// "extraContext" key holds the deployment's production extraContext), matching
// runSingleTest's production < _global < per-test precedence. Render sites that
// build their own context outside the Runner — the benchmark path in
// cmd/controller — call this so they render each test exactly as the load gate does.
func ApplyTestExtraContext(renderCtx map[string]any, cfg *config.Config, testExtra map[string]any) {
	mergeTestExtraContext(renderCtx, foldGlobalExtraContext(cfg, testExtra))
}

// replaceSentinelKey, when present (with any truthy value) in a test
// extraContext map, makes that map REPLACE the deployment's map wholesale
// instead of deep-merging into it. The sentinel key itself is stripped from
// the result. This is the escape hatch for map-valued registries (e.g.
// extraContext.waf.policies.inline) where merge semantics would otherwise
// let deployment-defined sibling keys join a test's pinned set — a baked
// test that needs the EXACT key set pins it with:
//
//	inline:
//	  __replace__: true
//	  approved-policy: {}
const replaceSentinelKey = "__replace__"

// deepMergeMaps returns a new map with override folded into base: keys whose
// values are maps on both sides merge recursively, any other value replaces
// the base value. A nested override map carrying the __replace__ sentinel
// replaces the base map wholesale (sentinel stripped). Neither input map is
// mutated.
func deepMergeMaps(base, override map[string]any) map[string]any {
	merged := make(map[string]any, len(base)+len(override))
	maps.Copy(merged, base)
	for key, value := range override {
		baseMap, baseOk := merged[key].(map[string]any)
		overrideMap, overrideOk := value.(map[string]any)
		if overrideOk {
			if _, replace := overrideMap[replaceSentinelKey]; replace {
				merged[key] = stripReplaceSentinel(overrideMap)
				continue
			}
		}
		if baseOk && overrideOk {
			merged[key] = deepMergeMaps(baseMap, overrideMap)
			continue
		}
		merged[key] = value
	}
	return merged
}

// stripReplaceSentinel returns a copy of m without the __replace__ key,
// recursing into nested maps so a replaced subtree can itself contain
// further sentinels. The input map is not mutated.
func stripReplaceSentinel(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for key, value := range m {
		if key == replaceSentinelKey {
			continue
		}
		if nested, ok := value.(map[string]any); ok {
			out[key] = stripReplaceSentinel(nested)
			continue
		}
		out[key] = value
	}
	return out
}

// collectStatusPatches drains the StatusPatchCollector that the templates'
// statusPatch() calls populated during the haproxy.cfg render. Each patch's
// variants (rendered / deployed / renderFailed / deployFailed) flatten into
// one map entry per phase keyed by `<ns>/<name>:<phase>` (or `:<phase>` for
// cluster-scoped resources without a namespace, e.g. GatewayClass). Values
// are JSON-marshalled so chart validation tests can assert on substrings via
// the standard contains / not_contains machinery (see assertion_helpers.go's
// `target: status:` resolver).
func collectStatusPatches(renderCtx map[string]any) (map[string]string, error) {
	out := make(map[string]string)
	collector, ok := renderCtx["statusPatchCollector"].(*templating.StatusPatchCollector)
	if !ok || collector == nil {
		return out, nil
	}
	for _, patch := range collector.Patches() {
		keyPrefix := patch.Namespace + "/" + patch.Name
		for phase, payload := range patch.Variants {
			bytes, err := json.Marshal(payload)
			if err != nil {
				return nil, fmt.Errorf("marshalling status patch for %s/%s phase %s: %w", patch.Namespace, patch.Name, phase, err)
			}
			out[keyPrefix+":"+phase] = string(bytes)
		}
	}
	return out, nil
}

// collectEvents drains the EventCollector that the templates' recordEvent()
// calls populated during rendering and serializes each Event to one line so
// validation tests can assert on them via the `target: events` resolver.
// Format: `<Type> <Reason> <apiVersion> <Kind> <ns>/<name>: <message>`.
func collectEvents(renderCtx map[string]any) string {
	collector, ok := renderCtx["recordEventCollector"].(*templating.EventCollector)
	if !ok || collector == nil {
		return ""
	}
	events := collector.Events()
	if len(events) == 0 {
		return ""
	}
	var b strings.Builder
	for _, e := range events {
		fmt.Fprintf(&b, "%s %s %s %s %s/%s: %s\n",
			e.Type, e.Reason, e.APIVersion, e.Kind, e.Namespace, e.Name, e.Message)
	}
	return b.String()
}

// buildRenderingContext builds the template rendering context using fixture stores.
//
// This method delegates to the centralized rendercontext.Builder to ensure consistent
// context creation across all usages (renderer, testrunner, benchmark, dryrunvalidator).
//
// Special handling for TestRunner:
//   - Creates PathResolver from ValidationPaths (not from config.Dataplane)
//   - Separates haproxy-pods store from resource stores
//   - Accepts optional currentConfig for slot-aware server assignment testing
func (r *Runner) buildRenderingContext(ctx context.Context, storeMap map[string]stores.Store, validationPaths *dataplane.ValidationPaths, httpStore *FixtureHTTPStoreWrapper, currentConfig *parserconfig.StructuredConfig, currentFiles map[string]string) map[string]any {
	// Create PathResolver from ValidationPaths
	pathResolver := rendercontext.PathResolverFromValidationPaths(validationPaths)

	// Separate haproxy-pods from resource stores (goes in controller namespace)
	resourceStores, haproxyPodStore := rendercontext.SeparateHAProxyPodStore(storeMap)
	if haproxyPodStore != nil {
		r.logger.Log(context.Background(), logging.LevelTrace, "wrapping haproxy-pods store for rendering context")
	}

	// Build context using centralized builder. typedResourceTypes is
	// nil unless the CLI wired typebootstrap (see cmd/controller/
	// validate.go) — when populated, the builder emits one *[]*T
	// top-level global per typed resource so chart templates that
	// use the typed shape compile against the same surface the
	// production renderer provides.
	builder := rendercontext.NewBuilder(
		ctx,
		r.config,
		pathResolver,
		r.logger,
		rendercontext.WithStores(resourceStores),
		rendercontext.WithHAProxyPodStore(haproxyPodStore),
		rendercontext.WithHTTPFetcher(httpStore),
		rendercontext.WithCurrentConfig(currentConfig),
		rendercontext.WithCurrentAuxFiles(currentFiles),
		rendercontext.WithTypedResources(r.typedResourceTypes),
		rendercontext.WithCapabilities(r.capabilities),
	)

	return builder.Build().Context
}

// renderAuxiliaryFiles renders all auxiliary files (maps, general files, SSL certificates) using worker-specific engine.
func (r *Runner) renderAuxiliaryFiles(ctx context.Context, engine templating.Engine, renderCtx map[string]any, validationPaths *dataplane.ValidationPaths) (*dataplane.AuxiliaryFiles, error) {
	auxFiles := &dataplane.AuxiliaryFiles{}

	// Render map files using worker-specific engine
	for name := range r.config.Maps {
		rendered, err := engine.Render(ctx, name, renderCtx)
		if err != nil {
			return nil, fmt.Errorf("rendering map file %s: %w", name, err)
		}

		auxFiles.MapFiles = append(auxFiles.MapFiles, auxiliaryfiles.MapFile{
			Path:    name,
			Content: rendered,
		})
	}

	// Render general files using worker-specific engine
	for name := range r.config.Files {
		rendered, err := engine.Render(ctx, name, renderCtx)
		if err != nil {
			return nil, fmt.Errorf("rendering general file %s: %w", name, err)
		}

		auxFiles.GeneralFiles = append(auxFiles.GeneralFiles, auxiliaryfiles.GeneralFile{
			Filename: name,
			Path:     filepath.Join(validationPaths.GeneralStorageDir, name),
			Content:  rendered,
		})
	}

	// Render SSL certificates using worker-specific engine
	for name := range r.config.SSLCertificates {
		rendered, err := engine.Render(ctx, name, renderCtx)
		if err != nil {
			return nil, fmt.Errorf("rendering SSL certificate %s: %w", name, err)
		}

		auxFiles.SSLCertificates = append(auxFiles.SSLCertificates, auxiliaryfiles.SSLCertificate{
			Path:    name,
			Content: rendered,
		})
	}

	return auxFiles, nil
}
