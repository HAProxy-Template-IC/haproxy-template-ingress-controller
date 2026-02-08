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
	"fmt"
	"os"
	"path/filepath"

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
		ConfigFile: filepath.Join(testDir, "haproxy.cfg"),
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
			return nil, fmt.Errorf("failed to create test directory %s: %w", dir, err)
		}
	}

	return resolvedPaths.ToValidationPaths(), nil
}

// renderWithStores renders HAProxy configuration using test fixture stores and worker-specific engine.
//
// This follows the same pattern as DryRunValidator.renderWithOverlayStores.
// When profileIncludes is enabled, it returns timing statistics for included templates.
// The currentConfig parameter enables slot-aware server assignment testing (nil for first deployment).
// The testExtraContext parameter allows test-specific extraContext values to override global ones.
func (r *Runner) renderWithStores(engine templating.Engine, storeMap map[string]stores.Store, validationPaths *dataplane.ValidationPaths, httpStore *FixtureHTTPStoreWrapper, currentConfig *parserconfig.StructuredConfig, testExtraContext map[string]interface{}) (string, *dataplane.AuxiliaryFiles, []templating.IncludeStats, error) {
	// Build rendering context with fixture stores
	renderCtx := r.buildRenderingContext(storeMap, validationPaths, httpStore, currentConfig)

	// Merge test-specific extraContext (overrides global extraContext values)
	// IMPORTANT: Make a copy to avoid modifying the shared config map which would
	// cause state leakage between parallel test runs.
	if testExtraContext != nil {
		globalExtraContext := renderCtx["extraContext"].(map[string]interface{})
		mergedExtraContext := make(map[string]interface{}, len(globalExtraContext)+len(testExtraContext))
		for key, value := range globalExtraContext {
			mergedExtraContext[key] = value
		}
		for key, value := range testExtraContext {
			mergedExtraContext[key] = value
			// Also merge into top-level context for direct access
			renderCtx[key] = value
		}
		renderCtx["extraContext"] = mergedExtraContext
	}

	// Render main HAProxy configuration using worker-specific engine
	var haproxyConfig string
	var includeStats []templating.IncludeStats
	var err error

	if r.profileIncludes {
		haproxyConfig, includeStats, err = engine.RenderWithProfiling(context.Background(), "haproxy.cfg", renderCtx)
	} else {
		haproxyConfig, err = engine.Render(context.Background(), "haproxy.cfg", renderCtx)
	}
	if err != nil {
		return "", nil, nil, fmt.Errorf("failed to render haproxy.cfg: %w", err)
	}

	// Render auxiliary files using worker-specific engine (pre-declared files)
	staticFiles, err := r.renderAuxiliaryFiles(engine, renderCtx, validationPaths)
	if err != nil {
		return "", nil, nil, fmt.Errorf("failed to render auxiliary files: %w", err)
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

	return haproxyConfig, auxiliaryFiles, includeStats, nil
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
func (r *Runner) buildRenderingContext(storeMap map[string]stores.Store, validationPaths *dataplane.ValidationPaths, httpStore *FixtureHTTPStoreWrapper, currentConfig *parserconfig.StructuredConfig) map[string]interface{} {
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

	renderCtx, _ := builder.Build()
	return renderCtx
}

// renderAuxiliaryFiles renders all auxiliary files (maps, general files, SSL certificates) using worker-specific engine.
func (r *Runner) renderAuxiliaryFiles(engine templating.Engine, renderCtx map[string]interface{}, validationPaths *dataplane.ValidationPaths) (*dataplane.AuxiliaryFiles, error) {
	auxFiles := &dataplane.AuxiliaryFiles{}

	// Render map files using worker-specific engine
	for name := range r.config.Maps {
		rendered, err := engine.Render(context.Background(), name, renderCtx)
		if err != nil {
			return nil, fmt.Errorf("failed to render map file %s: %w", name, err)
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
			return nil, fmt.Errorf("failed to render general file %s: %w", name, err)
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
			return nil, fmt.Errorf("failed to render SSL certificate %s: %w", name, err)
		}

		auxFiles.SSLCertificates = append(auxFiles.SSLCertificates, auxiliaryfiles.SSLCertificate{
			Path:    name,
			Content: rendered,
		})
	}

	return auxFiles, nil
}
