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

package renderer

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/currentconfigstore"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/httpstore"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// RenderResult contains the output of a render operation.
type RenderResult struct {
	// HAProxyConfig is the rendered HAProxy configuration.
	HAProxyConfig string

	// AuxiliaryFiles contains all rendered auxiliary files (maps, certs, general).
	AuxiliaryFiles *dataplane.AuxiliaryFiles

	// DurationMs is the total render duration in milliseconds.
	DurationMs int64

	// AuxFileCount is the total number of auxiliary files.
	AuxFileCount int
}

// RenderService is a pure service that transforms stores into HAProxy configuration.
//
// This service uses absolute paths from the config's Dataplane settings to ensure
// rendered configs reference files at the correct locations where DataPlane API
// stores auxiliary files.
//
// Resources in stores are already converted (floats to ints) at storage time,
// so the service simply passes through store data without additional processing.
type RenderService struct {
	engine       templating.Engine
	config       *config.Config
	pathResolver *templating.PathResolver
	logger       *slog.Logger

	// capabilities defines which features are available for the local HAProxy version.
	capabilities dataplane.Capabilities

	// capabilitiesMap is the pre-computed map representation of capabilities.
	// Cached at construction time to avoid creating the same map on every render.
	capabilitiesMap map[string]interface{}

	// Optional dependencies for building render context
	haproxyPodStore    stores.Store
	httpStoreComponent *httpstore.Component
	currentConfigStore *currentconfigstore.Store
}

// RenderServiceConfig contains configuration for creating a RenderService.
type RenderServiceConfig struct {
	// Engine is the template engine to use for rendering.
	Engine templating.Engine

	// Config is the controller configuration.
	Config *config.Config

	// Logger is the structured logger for logging.
	Logger *slog.Logger

	// Capabilities defines HAProxy version capabilities.
	Capabilities dataplane.Capabilities

	// HAProxyPodStore is the store containing HAProxy pods (optional).
	HAProxyPodStore stores.Store

	// HTTPStoreComponent is the HTTP store for dynamic content (optional).
	HTTPStoreComponent *httpstore.Component

	// CurrentConfigStore is the store for current deployed config (optional).
	CurrentConfigStore *currentconfigstore.Store
}

// NewRenderService creates a new RenderService.
//
// The service uses relative paths derived from the config's Dataplane settings.
// The directory names are extracted using filepath.Base() to get just the final
// directory component (e.g., /etc/haproxy/maps → maps).
//
// These relative paths are resolved by HAProxy using the `default-path origin <baseDir>`
// directive in the global section, which makes HAProxy resolve paths from the specified
// base directory regardless of where the config file is located. This works for:
//   - Local validation: ValidationService replaces baseDir with temp directory
//   - DataPlane API deployment: baseDir points to where files are stored (e.g., /etc/haproxy)
func NewRenderService(cfg *RenderServiceConfig) *RenderService {
	// Create path resolver with relative paths derived from config.
	// Use filepath.Base() to extract just the directory name from absolute paths.
	// Use filepath.Dir() to get the base directory from any absolute path.
	sslDir := filepath.Base(cfg.Config.Dataplane.SSLCertsDir)
	generalDir := filepath.Base(cfg.Config.Dataplane.GeneralStorageDir)

	// BaseDir is the parent of the auxiliary directories (e.g., /etc/haproxy).
	// This is used with "default-path origin" to resolve relative paths.
	baseDir := filepath.Dir(cfg.Config.Dataplane.MapsDir)

	// CRT-list files are always stored in general file storage, regardless of HAProxy version.
	// This is because the native CRT-list API (POST ssl_crt_lists) triggers a reload without
	// supporting skip_reload, while general file storage returns 201 without triggering reloads.
	// See: pkg/dataplane/auxiliaryfiles/crtlist.go
	crtListDir := generalDir

	pathResolver := &templating.PathResolver{
		BaseDir:    baseDir,
		MapsDir:    filepath.Base(cfg.Config.Dataplane.MapsDir),
		SSLDir:     sslDir,
		CRTListDir: crtListDir,
		GeneralDir: generalDir,
	}

	// Pre-compute capabilities map to avoid creating it on every render.
	// Capabilities never change during controller lifetime.
	capabilitiesMap := rendercontext.CapabilitiesToMap(&cfg.Capabilities)

	return &RenderService{
		engine:             cfg.Engine,
		config:             cfg.Config,
		pathResolver:       pathResolver,
		logger:             cfg.Logger,
		capabilities:       cfg.Capabilities,
		capabilitiesMap:    capabilitiesMap,
		haproxyPodStore:    cfg.HAProxyPodStore,
		httpStoreComponent: cfg.HTTPStoreComponent,
		currentConfigStore: cfg.CurrentConfigStore,
	}
}

// Render transforms the stores into HAProxy configuration.
//
// The render mode (production vs validation) is determined automatically:
//   - If provider is *OverlayStoreProvider with HTTP overlay: validation mode
//   - Otherwise: production mode
//
// Parameters:
//   - ctx: Context for cancellation
//   - provider: StoreProvider for accessing resource stores
//
// Returns:
//   - RenderResult containing the rendered configuration and auxiliary files
//   - Error if rendering fails
func (s *RenderService) Render(ctx context.Context, provider stores.StoreProvider) (*RenderResult, error) {
	startTime := time.Now()

	// Build rendering context from stores
	renderContext, fileRegistry := s.buildRenderingContext(ctx, provider)

	// Render main HAProxy config
	haproxyConfig, err := s.engine.Render("haproxy.cfg", renderContext)
	if err != nil {
		return nil, fmt.Errorf("failed to render haproxy.cfg: %w", err)
	}

	// Render auxiliary files
	staticFiles, err := s.renderAuxiliaryFiles(ctx, renderContext)
	if err != nil {
		return nil, err
	}

	// Merge static and dynamic (FileRegistry) auxiliary files
	dynamicFiles := fileRegistry.GetFiles()
	auxiliaryFiles := rendercontext.MergeAuxiliaryFiles(staticFiles, dynamicFiles)

	auxFileCount := len(auxiliaryFiles.MapFiles) +
		len(auxiliaryFiles.GeneralFiles) +
		len(auxiliaryFiles.SSLCertificates) +
		len(auxiliaryFiles.CRTListFiles)

	return &RenderResult{
		HAProxyConfig:  haproxyConfig,
		AuxiliaryFiles: auxiliaryFiles,
		DurationMs:     time.Since(startTime).Milliseconds(),
		AuxFileCount:   auxFileCount,
	}, nil
}

// buildRenderingContext constructs the template rendering context from stores.
func (s *RenderService) buildRenderingContext(ctx context.Context, provider stores.StoreProvider) (map[string]interface{}, *rendercontext.FileRegistry) {
	renderContext := make(map[string]interface{})

	// Add path resolver for file path resolution in templates
	renderContext["pathResolver"] = s.pathResolver

	// Build resources map from stores
	// Resources are already converted (floats to ints) at storage time
	resources := make(map[string]templating.ResourceStore)
	for _, name := range provider.StoreNames() {
		store := provider.GetStore(name)
		if store != nil {
			wrapper := &rendercontext.StoreWrapper{
				Store:        store,
				ResourceType: name,
				Logger:       s.logger,
			}
			resources[name] = wrapper
		}
	}
	renderContext["resources"] = resources

	// Add controller context with typed ResourceStore map
	// Must match the type expected by templates: map[string]templating.ResourceStore
	controller := make(map[string]templating.ResourceStore)
	if s.haproxyPodStore != nil {
		controller["haproxy_pods"] = &rendercontext.StoreWrapper{
			Store:        s.haproxyPodStore,
			ResourceType: "haproxy-pods",
			Logger:       s.logger,
		}
	}
	renderContext["controller"] = controller

	// Add capabilities at top level (not inside controller)
	// Use pre-computed map to avoid creating it on every render
	renderContext["capabilities"] = s.capabilitiesMap

	// Add dataplane config at top level
	renderContext["dataplane"] = s.config.Dataplane

	// Add current config if available (for slot preservation)
	// Note: Must check for nil value - Scriggo panics with nil pointer initializers
	if s.currentConfigStore != nil {
		currentConfig := s.currentConfigStore.Get()
		if currentConfig != nil {
			renderContext["currentConfig"] = currentConfig
		}
	}

	// Create file registry for dynamic file registration
	fileRegistry := rendercontext.NewFileRegistry(s.pathResolver)
	renderContext["fileRegistry"] = fileRegistry

	// Create shared cache for cross-template data sharing
	renderContext["shared"] = templating.NewSharedContext()

	// Add template snippets list (sorted)
	templateSnippets := rendercontext.SortSnippetNames(s.config.TemplateSnippets)
	renderContext["templateSnippets"] = templateSnippets

	// Add runtime environment
	renderContext["runtimeEnvironment"] = &templating.RuntimeEnvironment{
		GOMAXPROCS: runtime.GOMAXPROCS(0),
	}

	// Add extra context from config if provided
	if s.config.TemplatingSettings.ExtraContext != nil {
		renderContext["extraContext"] = s.config.TemplatingSettings.ExtraContext
	}

	// Add HTTP fetcher if available
	// Detection of validation mode is automatic based on provider type:
	// - If provider is OverlayStoreProvider with HTTP overlay: validation mode
	// - Otherwise: production mode (accepted content only)
	if s.httpStoreComponent != nil {
		var httpOverlay stores.HTTPContentOverlay

		// Check if provider is OverlayStoreProvider and extract HTTP overlay
		if overlayProvider, ok := provider.(*stores.OverlayStoreProvider); ok {
			httpOverlay = overlayProvider.GetHTTPOverlay()
		}

		httpFetcher := httpstore.NewHTTPStoreWrapper(ctx, s.httpStoreComponent, s.logger, httpOverlay)
		renderContext["http"] = httpFetcher
	}

	return renderContext, fileRegistry
}

// renderAuxiliaryFiles renders all auxiliary files in parallel.
// It respects the caller's context for cancellation.
func (s *RenderService) renderAuxiliaryFiles(ctx context.Context, renderCtx map[string]interface{}) (*dataplane.AuxiliaryFiles, error) {
	totalFiles := len(s.config.Maps) + len(s.config.Files) + len(s.config.SSLCertificates)
	if totalFiles == 0 {
		return &dataplane.AuxiliaryFiles{}, nil
	}

	var mu sync.Mutex
	// Pre-allocate slices with known capacity to avoid grow-from-zero
	auxFiles := &dataplane.AuxiliaryFiles{
		MapFiles:        make([]auxiliaryfiles.MapFile, 0, len(s.config.Maps)),
		GeneralFiles:    make([]auxiliaryfiles.GeneralFile, 0, len(s.config.Files)),
		SSLCertificates: make([]auxiliaryfiles.SSLCertificate, 0, len(s.config.SSLCertificates)),
	}

	// Create errgroup for parallel rendering. We discard the derived context because:
	// 1. Template rendering is CPU-bound and doesn't benefit from early cancellation
	// 2. errgroup still coordinates completion and returns the first error via Wait()
	// 3. The caller's ctx is available for overall timeout/cancellation if needed
	g, _ := errgroup.WithContext(ctx)

	// Render map files in parallel
	for name := range s.config.Maps {
		g.Go(func() error {
			rendered, err := s.engine.Render(name, renderCtx)
			if err != nil {
				return fmt.Errorf("failed to render map %s: %w", name, err)
			}
			mu.Lock()
			auxFiles.MapFiles = append(auxFiles.MapFiles, auxiliaryfiles.MapFile{
				Path:    name,
				Content: rendered,
			})
			mu.Unlock()
			return nil
		})
	}

	// Render general files in parallel
	for name := range s.config.Files {
		g.Go(func() error {
			rendered, err := s.engine.Render(name, renderCtx)
			if err != nil {
				return fmt.Errorf("failed to render file %s: %w", name, err)
			}
			mu.Lock()
			auxFiles.GeneralFiles = append(auxFiles.GeneralFiles, auxiliaryfiles.GeneralFile{
				Filename: name,
				Path:     filepath.Join(s.pathResolver.GeneralDir, name),
				Content:  rendered,
			})
			mu.Unlock()
			return nil
		})
	}

	// Render SSL certificates in parallel
	for name := range s.config.SSLCertificates {
		g.Go(func() error {
			rendered, err := s.engine.Render(name, renderCtx)
			if err != nil {
				return fmt.Errorf("failed to render SSL certificate %s: %w", name, err)
			}
			mu.Lock()
			auxFiles.SSLCertificates = append(auxFiles.SSLCertificates, auxiliaryfiles.SSLCertificate{
				Path:    name,
				Content: rendered,
			})
			mu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	return auxFiles, nil
}
