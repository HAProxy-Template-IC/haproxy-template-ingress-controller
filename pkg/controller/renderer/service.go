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

// cachedListResult holds the unwrapped List() result for a store.
// The items slice is immutable after creation and can be safely shared.
type cachedListResult struct {
	modCount uint64        // Store modCount when this cache entry was created
	items    []interface{} // Immutable slice of unwrapped resources
}

// RenderService is a pure service that transforms stores into HAProxy configuration.
//
// This service uses absolute paths from the config's Dataplane settings to ensure
// rendered configs reference files at the correct locations where DataPlane API
// stores auxiliary files.
//
// The service caches unwrapped List() results across reconciliations to avoid
// repeatedly unwrapping resources when store content hasn't changed. The cache
// is invalidated based on each store's modification counter.
type RenderService struct {
	engine       templating.Engine
	config       *config.Config
	pathResolver *templating.PathResolver
	logger       *slog.Logger

	// capabilities defines which features are available for the local HAProxy version.
	capabilities dataplane.Capabilities

	// Optional dependencies for building render context
	haproxyPodStore    stores.Store
	httpStoreComponent *httpstore.Component
	currentConfigStore *currentconfigstore.Store

	// listCache caches unwrapped List() results keyed by store name.
	// Protected by listCacheMu for concurrent access.
	listCacheMu sync.RWMutex
	listCache   map[string]*cachedListResult
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

	return &RenderService{
		engine:             cfg.Engine,
		config:             cfg.Config,
		pathResolver:       pathResolver,
		logger:             cfg.Logger,
		capabilities:       cfg.Capabilities,
		haproxyPodStore:    cfg.HAProxyPodStore,
		httpStoreComponent: cfg.HTTPStoreComponent,
		currentConfigStore: cfg.CurrentConfigStore,
		listCache:          make(map[string]*cachedListResult),
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

// isValidationMode returns true if the provider is in validation mode (dry-run with overlays).
// In validation mode, we skip the list cache because overlays may contain proposed changes
// that aren't reflected in the underlying stores.
func (s *RenderService) isValidationMode(provider stores.StoreProvider) bool {
	overlay, ok := provider.(*stores.OverlayStoreProvider)
	return ok && overlay.IsValidationMode()
}

// ClearListCache clears the unwrapped List() result cache.
// This should be called when stores may have been recreated (e.g., on config reload)
// or when leadership is lost (to ensure fresh state on leadership regain).
func (s *RenderService) ClearListCache() {
	s.listCacheMu.Lock()
	s.listCache = make(map[string]*cachedListResult)
	s.listCacheMu.Unlock()
	s.logger.Debug("list cache cleared")
}

// prepopulateListCache updates the list cache for all stores that support ModCount.
// This is called before creating StoreWrappers to ensure cache entries are up-to-date.
// The method holds a write lock during the entire operation to prevent TOCTOU races.
func (s *RenderService) prepopulateListCache(_ context.Context, provider stores.StoreProvider) {
	s.listCacheMu.Lock()
	defer s.listCacheMu.Unlock()

	for _, name := range provider.StoreNames() {
		store := provider.GetStore(name)
		if store == nil {
			continue
		}

		// Check if store supports ModCount
		modCounter, ok := store.(stores.ModCounter)
		if !ok {
			continue
		}

		modCount, supported := modCounter.ModCount()
		if !supported {
			// Store doesn't support modification tracking - don't cache
			continue
		}

		// Check if cache is valid
		if cached, exists := s.listCache[name]; exists && cached.modCount == modCount {
			s.logger.Debug("list cache hit",
				"resource_type", name,
				"mod_count", modCount)
			continue
		}

		// Cache miss: unwrap now while holding lock
		rawList, err := store.List()
		if err != nil {
			s.logger.Warn("failed to list store for cache",
				"store", name,
				"error", err)
			continue
		}

		// Unwrap unstructured resources to maps
		items := make([]interface{}, len(rawList))
		for i, item := range rawList {
			items[i] = rendercontext.UnwrapUnstructured(item)
		}

		s.listCache[name] = &cachedListResult{
			modCount: modCount,
			items:    items,
		}

		s.logger.Debug("list cache populated",
			"resource_type", name,
			"mod_count", modCount,
			"items", len(items))
	}
}

// getCachedList returns the cached list for a store, or nil if not cached.
func (s *RenderService) getCachedList(name string) []interface{} {
	s.listCacheMu.RLock()
	defer s.listCacheMu.RUnlock()
	if cached, exists := s.listCache[name]; exists {
		return cached.items
	}
	return nil
}

// buildRenderingContext constructs the template rendering context from stores.
func (s *RenderService) buildRenderingContext(ctx context.Context, provider stores.StoreProvider) (map[string]interface{}, *rendercontext.FileRegistry) {
	renderContext := make(map[string]interface{})

	// Add path resolver for file path resolution in templates
	renderContext["pathResolver"] = s.pathResolver

	// Determine if we should use the cache (skip for validation mode)
	useCache := !s.isValidationMode(provider)

	// Pre-populate cache before creating wrappers (single-threaded, no races)
	if useCache {
		s.prepopulateListCache(ctx, provider)
	}

	// Build resources map from stores
	resources := make(map[string]templating.ResourceStore)
	for _, name := range provider.StoreNames() {
		store := provider.GetStore(name)
		if store != nil {
			wrapper := &rendercontext.StoreWrapper{
				Store:        store,
				ResourceType: name,
				Logger:       s.logger,
			}
			// Inject precomputed list if cache hit
			if useCache {
				wrapper.PrecomputedList = s.getCachedList(name)
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
	renderContext["capabilities"] = rendercontext.CapabilitiesToMap(&s.capabilities)

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
	auxFiles := &dataplane.AuxiliaryFiles{}

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
