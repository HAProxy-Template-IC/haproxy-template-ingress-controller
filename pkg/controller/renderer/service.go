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
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
	"gopkg.in/yaml.v3"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/currentconfigstore"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/httpstore"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/names"
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

	// StatusPatches contains status patches registered by templates during rendering.
	// Each patch targets a Kubernetes resource and contains outcome-keyed variants.
	StatusPatches []templating.StatusPatch

	// RenderedResources contains full Kubernetes resources the templates declared
	// the controller should own and reconcile (e.g. per-Gateway LoadBalancer
	// Services for SupportGatewayStaticAddresses). The applier compares each
	// against the last-applied checksum and skips unchanged entries to avoid
	// hammering the API server.
	RenderedResources []templating.RenderedResource

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

	// renderTimeout is the maximum time allowed for rendering a single template.
	renderTimeout time.Duration

	// capabilities defines which features are available for the local HAProxy version.
	capabilities dataplane.Capabilities

	// capabilitiesMap is the pre-computed map representation of capabilities.
	// Cached at construction time to avoid creating the same map on every render.
	capabilitiesMap map[string]any

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
		renderTimeout:      cfg.Config.TemplatingSettings.GetRenderTimeout(),
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

	// Apply render timeout if configured
	if s.renderTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.renderTimeout)
		defer cancel()
	}

	// Build rendering context from stores
	renderContext, fileRegistry, statusPatchCollector, renderedResourceCollector := s.buildRenderingContext(ctx, provider)

	// Render main HAProxy config
	haproxyConfig, err := s.engine.Render(ctx, names.MainTemplateName, renderContext)
	if err != nil {
		return nil, fmt.Errorf("rendering %s: %w", names.MainTemplateName, err)
	}

	// Render auxiliary files
	staticFiles, err := s.renderAuxiliaryFiles(ctx, renderContext)
	if err != nil {
		return nil, err
	}

	// Render Kubernetes resource templates (`spec.k8sResources`). Each
	// template's output is one or more YAML documents; every doc gets
	// parsed and registered with the same RenderedResourceCollector
	// the runtime renderResource() filter populated previously, so
	// downstream consumers (resourceapplier) see no shape change.
	if err := s.renderK8sResources(ctx, renderContext, renderedResourceCollector); err != nil {
		return nil, err
	}

	// Merge static and dynamic (FileRegistry) auxiliary files
	dynamicFiles := fileRegistry.GetFiles()
	auxiliaryFiles := rendercontext.MergeAuxiliaryFiles(staticFiles, dynamicFiles)

	// Defensive consistency check: fail the render if the config references
	// map files the renderer did not register. Without this, a chart-side
	// inconsistency where a snippet emits a map_str(...) reference but
	// skips its fileRegistry.Register call would silently push a config
	// whose post-config-delete phase deletes the missing map, breaking
	// every subsequent HAProxy reload until the offending Ingress is
	// removed.
	if err := validateAuxiliaryFilesConsistency(haproxyConfig, auxiliaryFiles); err != nil {
		return nil, fmt.Errorf("rendering %s: %w", names.MainTemplateName, err)
	}

	auxFileCount := len(auxiliaryFiles.MapFiles) +
		len(auxiliaryFiles.GeneralFiles) +
		len(auxiliaryFiles.SSLCertificates) +
		len(auxiliaryFiles.SSLCaFiles) +
		len(auxiliaryFiles.CRTListFiles)

	// Validate rendered resources before surfacing them. Any structural
	// problem aborts the render so the deployment scheduler doesn't get a
	// half-formed payload.
	if err := renderedResourceCollector.Validate(); err != nil {
		return nil, fmt.Errorf("rendering %s: %w", names.MainTemplateName, err)
	}

	return &RenderResult{
		HAProxyConfig:     haproxyConfig,
		AuxiliaryFiles:    auxiliaryFiles,
		StatusPatches:     statusPatchCollector.Patches(),
		RenderedResources: renderedResourceCollector.Resources(),
		DurationMs:        time.Since(startTime).Milliseconds(),
		AuxFileCount:      auxFileCount,
	}, nil
}

// buildRenderingContext constructs the template rendering context from stores.
func (s *RenderService) buildRenderingContext(ctx context.Context, provider stores.StoreProvider) (map[string]any, *rendercontext.FileRegistry, *templating.StatusPatchCollector, *templating.RenderedResourceCollector) {
	renderContext := make(map[string]any)

	// Add path resolver for file path resolution in templates
	renderContext["pathResolver"] = s.pathResolver

	// Build resources map from stores. Each wrapper gets the IndexBy
	// the watcher used to build the underlying store; the wrapper uses
	// it to build its per-render snapshot index so List/Fetch/GetSingle
	// on one wrapper instance all observe the same store state. Without
	// IndexBy, Fetch/GetSingle fall back to a live store read that can
	// observe a state diverging from List() — the root cause of the
	// conformance-suite flakes tracked in issue #45 (parallel resource
	// creation racing the chart's per-render reads).
	resources := make(map[string]templating.ResourceStore)
	for _, name := range provider.StoreNames() {
		store := provider.GetStore(name)
		if store != nil {
			var indexBy []string
			if wr, ok := s.config.WatchedResources[name]; ok {
				indexBy = wr.IndexBy
			}
			wrapper := &rendercontext.StoreWrapper{
				Store:        store,
				ResourceType: name,
				Logger:       s.logger,
				IndexBy:      indexBy,
			}
			resources[name] = wrapper
		}
	}
	renderContext["resources"] = resources

	// Add controller context with typed ResourceStore map. The
	// haproxy-pods watcher is auto-injected by ResourceWatcherComponent
	// with a fixed IndexBy of ["metadata.namespace", "metadata.name"]
	// (see pkg/controller/resourcewatcher/watcher.go) — mirror that
	// here so the wrapper's snapshot index agrees with the underlying
	// store, same reason as the resources loop above.
	controller := make(map[string]templating.ResourceStore)
	if s.haproxyPodStore != nil {
		controller["haproxy_pods"] = &rendercontext.StoreWrapper{
			Store:        s.haproxyPodStore,
			ResourceType: names.HAProxyPodsResourceType,
			Logger:       s.logger,
			IndexBy:      []string{"metadata.namespace", "metadata.name"},
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

	// Create status patch collector for template-driven status updates
	statusPatchCollector := templating.NewStatusPatchCollector()
	renderContext["statusPatchCollector"] = statusPatchCollector

	// Create rendered resource collector for template-driven owned-resource
	// reconciliation. Same shape as statusPatchCollector but for whole
	// resources instead of status-only updates. Resource-agnostic by design
	// (the controller never names "Service" or "Gateway" in code — it
	// applies whatever the template emits via SSA).
	renderedResourceCollector := templating.NewRenderedResourceCollector()
	renderContext["renderedResourceCollector"] = renderedResourceCollector

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

	return renderContext, fileRegistry, statusPatchCollector, renderedResourceCollector
}

// renderAuxiliaryFiles renders all auxiliary files in parallel.
// It respects the caller's context for cancellation.
func (s *RenderService) renderAuxiliaryFiles(ctx context.Context, renderCtx map[string]any) (*dataplane.AuxiliaryFiles, error) {
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
	renderAuxGroup(ctx, g, &mu, s.engine, renderCtx,
		s.config.Maps, "map", &auxFiles.MapFiles,
		func(name, content string) auxiliaryfiles.MapFile {
			return auxiliaryfiles.MapFile{Path: name, Content: content}
		})

	// Render general files in parallel
	renderAuxGroup(ctx, g, &mu, s.engine, renderCtx,
		s.config.Files, "file", &auxFiles.GeneralFiles,
		func(name, content string) auxiliaryfiles.GeneralFile {
			return auxiliaryfiles.GeneralFile{
				Filename: name,
				Path:     filepath.Join(s.pathResolver.GeneralDir, name),
				Content:  content,
			}
		})

	// Render SSL certificates in parallel
	renderAuxGroup(ctx, g, &mu, s.engine, renderCtx,
		s.config.SSLCertificates, "SSL certificate", &auxFiles.SSLCertificates,
		func(name, content string) auxiliaryfiles.SSLCertificate {
			return auxiliaryfiles.SSLCertificate{Path: name, Content: content}
		})

	if err := g.Wait(); err != nil {
		return nil, err
	}

	return auxFiles, nil
}

// ClearVMPool releases pooled template engine VMs.
// Call after rendering completes to reduce memory from parallel rendering spikes.
func (s *RenderService) ClearVMPool() {
	if s.engine != nil {
		s.engine.ClearVMPool()
	}
}

// renderK8sResources renders every entry in spec.k8sResources in parallel,
// parses the rendered output as one or more YAML documents (multi-doc
// supported via `---` separators), and registers each document with the
// supplied RenderedResourceCollector. The collector is the same input
// downstream consumers (resourceapplier) read off RenderResult.
//
// Each YAML document must declare apiVersion, kind, and metadata.name
// (plus metadata.namespace for namespaced kinds). A bad document aborts
// the render with an error scoped to the offending template name so
// authors can locate it.
func (s *RenderService) renderK8sResources(ctx context.Context, renderCtx map[string]any, collector *templating.RenderedResourceCollector) error {
	if len(s.config.K8sResources) == 0 {
		return nil
	}
	g, _ := errgroup.WithContext(ctx)
	for name := range s.config.K8sResources {
		g.Go(func() error {
			rendered, err := s.engine.Render(ctx, name, renderCtx)
			if err != nil {
				return fmt.Errorf("rendering k8sResources %s: %w", name, err)
			}
			return registerK8sResourceDocs(name, rendered, collector)
		})
	}
	return g.Wait()
}

// registerK8sResourceDocs parses rendered YAML (one or more documents
// separated by `---`), validates each, and adds it to the collector.
func registerK8sResourceDocs(templateName, rendered string, collector *templating.RenderedResourceCollector) error {
	if strings.TrimSpace(rendered) == "" {
		// Empty render is a valid "no resources to emit this cycle"
		// signal — common when a template gates its output on a
		// resource state that doesn't currently exist.
		return nil
	}
	dec := yaml.NewDecoder(strings.NewReader(rendered))
	docIdx := 0
	for {
		var doc map[string]any
		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("parsing k8sResources %s document %d: %w", templateName, docIdx, err)
		}
		docIdx++
		if len(doc) == 0 {
			continue
		}
		apiVersion, _ := doc["apiVersion"].(string)
		kind, _ := doc["kind"].(string)
		metadata, _ := doc["metadata"].(map[string]any)
		var name, namespace string
		if metadata != nil {
			name, _ = metadata["name"].(string)
			namespace, _ = metadata["namespace"].(string)
		}
		if apiVersion == "" || kind == "" || name == "" {
			return fmt.Errorf("k8sResources %s document %d: apiVersion, kind, and metadata.name are required", templateName, docIdx)
		}
		// Strip the identifying fields before handing the object to
		// Register — Register re-injects them from the explicit
		// arguments, and leaving them in would have Register copy
		// them back over no-ops. metadata is intentionally kept
		// since templates may add labels / annotations / ownerRefs
		// the applier then merges with the resource it sends.
		if err := collector.Register(apiVersion, kind, namespace, name, doc); err != nil {
			return fmt.Errorf("k8sResources %s document %d: %w", templateName, docIdx, err)
		}
	}
}

// renderAuxGroup renders one auxiliary-file group in parallel via g. For each
// name in sources it submits a render goroutine that, on success, appends the
// per-item value built by build to *out under mu. Render errors are wrapped
// with label so the eventual g.Wait() failure makes clear which group failed
// (e.g. "map", "file", "SSL certificate"). The map values are unused — only
// the keys (template names) drive the rendering.
func renderAuxGroup[V any, T any](
	ctx context.Context,
	g *errgroup.Group,
	mu *sync.Mutex,
	engine templating.Engine,
	renderCtx map[string]any,
	sources map[string]V,
	label string,
	out *[]T,
	build func(name, content string) T,
) {
	for name := range sources {
		g.Go(func() error {
			rendered, err := engine.Render(ctx, name, renderCtx)
			if err != nil {
				return fmt.Errorf("rendering %s %s: %w", label, name, err)
			}
			mu.Lock()
			*out = append(*out, build(name, rendered))
			mu.Unlock()
			return nil
		})
	}
}
