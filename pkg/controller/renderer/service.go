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
	"path"
	"reflect"
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

	// Events contains Kubernetes Events templates asked to emit via recordEvent()
	// (e.g. a RouteConflict Warning on an Ingress whose route lost to an older
	// one). Resource-agnostic — each carries its own apiVersion/kind/namespace/name.
	Events []templating.RenderedEvent

	// RenderedResources contains full Kubernetes resources the templates declared
	// the controller should own and reconcile (e.g. an auxiliary Service or other
	// object a template emits alongside the HAProxy config). The applier compares
	// each against the last-applied checksum and skips unchanged entries to avoid
	// hammering the API server.
	RenderedResources []templating.RenderedResource

	// DurationMs is the total render duration in milliseconds.
	DurationMs int64

	// IncludeStats holds per-snippet render counts/timing for the main template.
	// Populated only when the engine was built with profiling enabled (nil in
	// production); consumed by the browser playground's render-trace view.
	IncludeStats []templating.IncludeStats

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

	// Optional dependencies for building render context
	haproxyPodStore         stores.Store
	httpStoreComponent      *httpstore.Component
	currentConfigStore      *currentconfigstore.Store
	currentAuxFilesProvider func() map[string]string

	// typedResourceTypes maps watched-resource user-names to the
	// generated Go type produced by pkg/k8s/typegen at iteration
	// start (see pkg/controller/typebootstrap). When non-empty,
	// buildRenderingContext emits one *[]*<generated-struct>
	// top-level context entry per type — the value Scriggo's
	// type-checker pairs with the typed global declared via
	// helpers.NewEngineFromConfigWithOptions.
	//
	// Optional: a nil / empty map means no typed access is
	// available and templates use the untyped resources["<name>"]
	// path as today.
	typedResourceTypes map[string]reflect.Type
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

	// CurrentAuxFilesProvider returns the currently-deployed general aux files
	// (filename → content), exposed to templates as `currentFiles`. Optional;
	// nil (e.g. webhook dry-run) yields an empty map. Lets a template read its
	// own prior output — the basis for self-rotating TLS session-ticket keys.
	CurrentAuxFilesProvider func() map[string]string

	// TypedResourceTypes carries the generated Go types produced
	// by pkg/controller/typebootstrap at iteration start. The
	// renderer emits one *[]*<generated-struct> top-level context
	// entry per type at render time, matching the typed-global
	// declarations the engine constructor received.
	//
	// Optional. nil or empty means typed-resource access isn't
	// available and templates fall back to the untyped
	// resources["<name>"] path.
	TypedResourceTypes map[string]reflect.Type
}

// NewRenderService creates a new RenderService.
//
// The service uses relative paths derived from the config's Dataplane settings.
// The directory names are extracted using path.Base() to get just the final
// directory component (e.g., /etc/haproxy/maps → maps).
//
// These relative paths are resolved by HAProxy using the `default-path origin <baseDir>`
// directive in the global section, which makes HAProxy resolve paths from the specified
// base directory regardless of where the config file is located. This works for:
//   - Local validation: ValidationService replaces baseDir with temp directory
//   - DataPlane API deployment: baseDir points to where files are stored (e.g., /etc/haproxy)
func NewRenderService(cfg *RenderServiceConfig) *RenderService {
	// Create path resolver with relative paths derived from config.
	// Use path.Base() to extract just the directory name from absolute paths.
	// Use path.Dir() to get the base directory from any absolute path.
	// The slash-only path package is used (not filepath) because these are
	// HAProxy target paths, always slash-separated regardless of host OS.
	sslDir := path.Base(cfg.Config.Dataplane.SSLCertsDir)
	generalDir := path.Base(cfg.Config.Dataplane.GeneralStorageDir)

	// BaseDir is the parent of the auxiliary directories (e.g., /etc/haproxy).
	// This is used with "default-path origin" to resolve relative paths.
	baseDir := path.Dir(cfg.Config.Dataplane.MapsDir)

	// CRT-list files are always stored in general file storage, regardless of HAProxy version.
	// This is because the native CRT-list API (POST ssl_crt_lists) triggers a reload without
	// supporting skip_reload, while general file storage returns 201 without triggering reloads.
	// See: pkg/dataplane/auxiliaryfiles/crtlist.go
	crtListDir := generalDir

	pathResolver := &templating.PathResolver{
		BaseDir:    baseDir,
		MapsDir:    path.Base(cfg.Config.Dataplane.MapsDir),
		SSLDir:     sslDir,
		CRTListDir: crtListDir,
		GeneralDir: generalDir,
	}

	return &RenderService{
		engine:                  cfg.Engine,
		config:                  cfg.Config,
		pathResolver:            pathResolver,
		logger:                  cfg.Logger,
		renderTimeout:           cfg.Config.TemplatingSettings.GetRenderTimeout(),
		capabilities:            cfg.Capabilities,
		haproxyPodStore:         cfg.HAProxyPodStore,
		httpStoreComponent:      cfg.HTTPStoreComponent,
		currentConfigStore:      cfg.CurrentConfigStore,
		currentAuxFilesProvider: cfg.CurrentAuxFilesProvider,
		typedResourceTypes:      cfg.TypedResourceTypes,
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
func (s *RenderService) Render(ctx context.Context, provider stores.StoreProvider, mode rendercontext.RenderMode, extraOpts ...rendercontext.Option) (*RenderResult, error) {
	startTime := time.Now()

	// Apply render timeout if configured
	if s.renderTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.renderTimeout)
		defer cancel()
	}

	// Build rendering context from stores
	bctx := s.buildRenderingContext(ctx, provider, mode, extraOpts...)
	renderContext, fileRegistry := bctx.Context, bctx.FileRegistry
	statusPatchCollector, renderedResourceCollector := bctx.StatusPatchCollector, bctx.RenderedResourceCollector
	eventCollector := bctx.EventCollector

	// Render main HAProxy config. RenderWithProfiling is a superset of Render:
	// it renders identically and, only when the engine was built with profiling
	// enabled, additionally returns per-snippet include stats (nil otherwise), so
	// this is behaviour-neutral in production.
	haproxyConfig, includeStats, err := s.engine.RenderWithProfiling(ctx, names.MainTemplateName, renderContext)
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
		Events:            eventCollector.Events(),
		RenderedResources: renderedResourceCollector.Resources(),
		DurationMs:        time.Since(startTime).Milliseconds(),
		AuxFileCount:      auxFileCount,
		IncludeStats:      includeStats,
	}, nil
}

// buildRenderingContext constructs the template rendering context from stores.
//
// This goes through the shared rendercontext.Builder — the exact same path
// testrunner and the render benchmark use — so a template can't pass
// `controller validate` yet behave differently in production. The only
// production-specific plumbing is reading the live stores off the
// StoreProvider, resolving the current deployed config, and wiring the HTTP
// fetcher (whose overlay depends on the provider type); everything else is the
// Builder's responsibility.
func (s *RenderService) buildRenderingContext(ctx context.Context, provider stores.StoreProvider, mode rendercontext.RenderMode, extraOpts ...rendercontext.Option) *rendercontext.BuildResult {
	// Snapshot the live stores off the provider. The haproxy-pods store is
	// separated out by the Builder (WithHAProxyPodStore) into
	// controller.haproxy_pods; the rest land in `resources`.
	storesByName := make(map[string]stores.Store, len(provider.StoreNames()))
	for _, name := range provider.StoreNames() {
		if store := provider.GetStore(name); store != nil {
			storesByName[name] = store
		}
	}
	resourceStores, haproxyPodStore := rendercontext.SeparateHAProxyPodStore(storesByName)
	if haproxyPodStore == nil {
		// Production injects the haproxy-pods store directly (it may not be
		// registered with the provider under names.HAProxyPodsResourceType).
		haproxyPodStore = s.haproxyPodStore
	}

	opts := []rendercontext.Option{
		rendercontext.WithStores(resourceStores),
		rendercontext.WithHAProxyPodStore(haproxyPodStore),
		rendercontext.WithCapabilities(s.capabilities),
		rendercontext.WithTypedResources(s.typedResourceTypes),
		rendercontext.WithRenderMode(mode),
	}
	opts = append(opts, extraOpts...)

	// Add current config if available (for slot preservation). Passing a nil
	// *StructuredConfig is fine — the Builder omits the key (Scriggo panics on
	// nil pointer initializers).
	if s.currentConfigStore != nil {
		opts = append(opts, rendercontext.WithCurrentConfig(s.currentConfigStore.Get()))
	}

	// Add currently-deployed general aux files (for templates that read their
	// own prior output, e.g. self-rotating TLS session-ticket keys). nil
	// provider (webhook dry-run) → WithCurrentAuxFiles unset → empty map.
	if s.currentAuxFilesProvider != nil {
		opts = append(opts, rendercontext.WithCurrentAuxFiles(s.currentAuxFilesProvider()))
	}

	// Wire the HTTP fetcher. Validation mode is detected automatically from the
	// provider type: an OverlayStoreProvider carries the HTTP overlay (pending
	// content), production providers don't (accepted content only).
	if s.httpStoreComponent != nil {
		var httpOverlay stores.HTTPContentOverlay
		if overlayProvider, ok := provider.(*stores.OverlayStoreProvider); ok {
			httpOverlay = overlayProvider.GetHTTPOverlay()
		}
		httpFetcher := httpstore.NewHTTPStoreWrapper(ctx, s.httpStoreComponent, s.logger, httpOverlay)
		opts = append(opts, rendercontext.WithHTTPFetcher(httpFetcher))
	}

	return rendercontext.NewBuilder(s.config, s.pathResolver, s.logger, opts...).Build()
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
				Path:     path.Join(s.pathResolver.GeneralDir, name),
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

// sourceMapper is the optional engine capability RenderSourceMaps needs. The
// production ScriggoEngine implements it; this keeps the templating.Engine
// interface unchanged (source maps are a playground-only concern).
type sourceMapper interface {
	RenderWithSourceMap(ctx context.Context, name string, tctx map[string]any) (string, []templating.SourceSpan, error)
}

// TemplateSourceMap is the raw (pre-post-processing) render of one template plus
// its output-to-source spans. Length fields of Spans sum to len(Raw).
type TemplateSourceMap struct {
	Raw   string
	Spans []templating.SourceSpan
}

// RenderSourceMaps renders the main config and each map/file template a second
// time with source-map collection, over the same context Render builds, and
// returns name→source map. It is a playground-only pass (provenance); it returns
// nil if the engine doesn't support source maps. Names are the template registry
// keys: names.MainTemplateName for the config, and the map/file key otherwise.
func (s *RenderService) RenderSourceMaps(ctx context.Context, provider stores.StoreProvider) (map[string]TemplateSourceMap, error) {
	sm, ok := s.engine.(sourceMapper)
	if !ok {
		return map[string]TemplateSourceMap{}, nil
	}
	// Source-map introspection is read-only provenance, not enforcement — use
	// the lenient reconcile mode so it never fails on a conflict.
	renderCtx := s.buildRenderingContext(ctx, provider, rendercontext.RenderModeReconcile).Context
	out := make(map[string]TemplateSourceMap)
	add := func(name string) {
		if raw, spans, err := sm.RenderWithSourceMap(ctx, name, renderCtx); err == nil {
			out[name] = TemplateSourceMap{Raw: raw, Spans: spans}
		}
	}
	add(names.MainTemplateName)
	for name := range s.config.Maps {
		add(name)
	}
	for name := range s.config.Files {
		add(name)
	}
	for name := range s.config.SSLCertificates {
		add(name)
	}
	// k8sResources templates back the "applied" tab. Their rendered YAML is
	// re-marshaled for display (keys reordered), so the playground content-matches
	// displayed lines against this raw source map to attribute each one.
	for name := range s.config.K8sResources {
		add(name)
	}
	return out, nil
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
