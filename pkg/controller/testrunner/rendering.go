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
	"path/filepath"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/names"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"

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
	// using filepath.Base() to ensure consistency between production and validation.
	// HAProxy requires absolute paths to locate files, so we create absolute paths
	// within the isolated test directory (e.g., /tmp/haproxy-validate-12345/worker-0/test-1/maps).
	basePaths := dataplane.PathConfig{
		MapsDir:    filepath.Join(testDir, filepath.Base(r.config.Dataplane.MapsDir)),
		SSLDir:     filepath.Join(testDir, filepath.Base(r.config.Dataplane.SSLCertsDir)),
		GeneralDir: filepath.Join(testDir, filepath.Base(r.config.Dataplane.GeneralStorageDir)),
		ConfigFile: filepath.Join(testDir, names.MainTemplateName),
	}

	// Use centralized path resolution to get capability-aware paths
	// This ensures CRTListDir is set correctly for HAProxy < 3.2
	resolvedPaths := dataplane.ResolvePaths(basePaths, r.capabilities)

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
	IncludeStats   []templating.IncludeStats
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
func (r *Runner) renderWithStores(engine templating.Engine, storeMap map[string]stores.Store, validationPaths *dataplane.ValidationPaths, httpStore *FixtureHTTPStoreWrapper, currentConfig *parserconfig.StructuredConfig, testExtraContext map[string]any) (RenderOutput, error) {
	// Build rendering context with fixture stores
	renderCtx := r.buildRenderingContext(storeMap, validationPaths, httpStore, currentConfig)

	mergeTestExtraContext(renderCtx, testExtraContext)

	// Render main HAProxy configuration using worker-specific engine
	var haproxyConfig string
	var includeStats []templating.IncludeStats
	var err error

	if r.profileIncludes {
		haproxyConfig, includeStats, err = engine.RenderWithProfiling(context.Background(), names.MainTemplateName, renderCtx)
	} else {
		haproxyConfig, err = engine.Render(context.Background(), names.MainTemplateName, renderCtx)
	}
	if err != nil {
		return RenderOutput{}, fmt.Errorf("rendering %s: %w", names.MainTemplateName, err)
	}

	// Render auxiliary files using worker-specific engine (pre-declared files)
	staticFiles, err := r.renderAuxiliaryFiles(engine, renderCtx, validationPaths)
	if err != nil {
		return RenderOutput{}, fmt.Errorf("rendering auxiliary files: %w", err)
	}

	// Render k8sResources templates using the worker-specific engine. These
	// are surfaced into the test result so assertions can target them via
	// `target: k8s:<template-name>` and the --dump-rendered flag can show
	// them alongside haproxy.cfg / map files.
	k8sResources := make(map[string]string, len(r.config.K8sResources))
	for name := range r.config.K8sResources {
		rendered, err := engine.Render(context.Background(), name, renderCtx)
		if err != nil {
			return RenderOutput{}, fmt.Errorf("rendering k8sResources %s: %w", name, err)
		}
		k8sResources[name] = rendered
	}

	statusPatches, err := collectStatusPatches(renderCtx)
	if err != nil {
		return RenderOutput{}, err
	}

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
		IncludeStats:   includeStats,
	}, nil
}

// mergeTestExtraContext folds a per-test extraContext map into the rendering
// context built from the global config. The merge is destructive on a fresh
// per-test copy (never the shared global map) so parallel test workers don't
// leak state into each other.
func mergeTestExtraContext(renderCtx, testExtraContext map[string]any) {
	if testExtraContext == nil {
		return
	}
	globalExtraContext := renderCtx["extraContext"].(map[string]any)
	merged := make(map[string]any, len(globalExtraContext)+len(testExtraContext))
	maps.Copy(merged, globalExtraContext)
	for key, value := range testExtraContext {
		merged[key] = value
		// Also merge into top-level context for direct access.
		renderCtx[key] = value
	}
	renderCtx["extraContext"] = merged
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

// buildRenderingContext builds the template rendering context using fixture stores.
//
// This method delegates to the centralized rendercontext.Builder to ensure consistent
// context creation across all usages (renderer, testrunner, benchmark, dryrunvalidator).
//
// Special handling for TestRunner:
//   - Creates PathResolver from ValidationPaths (not from config.Dataplane)
//   - Separates haproxy-pods store from resource stores
//   - Accepts optional currentConfig for slot-aware server assignment testing
func (r *Runner) buildRenderingContext(storeMap map[string]stores.Store, validationPaths *dataplane.ValidationPaths, httpStore *FixtureHTTPStoreWrapper, currentConfig *parserconfig.StructuredConfig) map[string]any {
	// Create PathResolver from ValidationPaths
	pathResolver := rendercontext.PathResolverFromValidationPaths(validationPaths)

	// Separate haproxy-pods from resource stores (goes in controller namespace)
	resourceStores, haproxyPodStore := rendercontext.SeparateHAProxyPodStore(storeMap)
	if haproxyPodStore != nil {
		r.logger.Log(context.Background(), logging.LevelTrace, "wrapping haproxy-pods store for rendering context")
	}

	// Build context using centralized builder
	builder := rendercontext.NewBuilder(
		r.config,
		pathResolver,
		r.logger,
		rendercontext.WithStores(resourceStores),
		rendercontext.WithHAProxyPodStore(haproxyPodStore),
		rendercontext.WithHTTPFetcher(httpStore),
		rendercontext.WithCurrentConfig(currentConfig),
	)

	return builder.Build().Context
}

// renderAuxiliaryFiles renders all auxiliary files (maps, general files, SSL certificates) using worker-specific engine.
func (r *Runner) renderAuxiliaryFiles(engine templating.Engine, renderCtx map[string]any, validationPaths *dataplane.ValidationPaths) (*dataplane.AuxiliaryFiles, error) {
	auxFiles := &dataplane.AuxiliaryFiles{}

	// Render map files using worker-specific engine
	for name := range r.config.Maps {
		rendered, err := engine.Render(context.Background(), name, renderCtx)
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
		rendered, err := engine.Render(context.Background(), name, renderCtx)
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
		rendered, err := engine.Render(context.Background(), name, renderCtx)
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
