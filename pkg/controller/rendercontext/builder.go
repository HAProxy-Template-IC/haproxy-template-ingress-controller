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

// Package rendercontext provides a centralized builder for template rendering contexts.
//
// This package consolidates the previously duplicated context creation logic from
// renderer, testrunner, benchmark, and dryrunvalidator into a single, reusable builder.
//
// Usage:
//
//	builder := rendercontext.NewBuilder(
//	    cfg,
//	    pathResolver,
//	    logger,
//	    rendercontext.WithStores(stores),
//	    rendercontext.WithCapabilities(capabilities),
//	)
//	res := builder.Build()
//	ctx := res.Context
package rendercontext

import (
	"log/slog"
	"maps"
	"reflect"
	"runtime"
	"slices"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/names"
	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/typegen"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

// Builder constructs template rendering contexts with consistent structure.
// Use NewBuilder() to create a builder and functional options to configure it.
type Builder struct {
	// Required dependencies
	config       *config.Config
	pathResolver *templating.PathResolver
	logger       *slog.Logger

	// Optional dependencies (set via options)
	stores             map[string]stores.Store
	haproxyPodStore    stores.Store
	httpFetcher        templating.HTTPFetcher
	capabilities       *dataplane.Capabilities
	currentConfig      *parserconfig.StructuredConfig
	typedResourceTypes map[string]reflect.Type
}

// Option configures a Builder.
type Option func(*Builder)

// WithStores sets the resource stores for the template context.
// Each store is wrapped in a StoreWrapper to provide template-friendly methods.
func WithStores(storeMap map[string]stores.Store) Option {
	return func(b *Builder) {
		b.stores = storeMap
	}
}

// WithHAProxyPodStore sets the HAProxy pod store for controller.haproxy_pods.
// This enables templates to access HAProxy pod count for calculations.
func WithHAProxyPodStore(store stores.Store) Option {
	return func(b *Builder) {
		b.haproxyPodStore = store
	}
}

// WithHTTPFetcher sets the HTTP fetcher for http.Fetch() calls in templates.
// Pass nil to disable HTTP fetching capability.
func WithHTTPFetcher(fetcher templating.HTTPFetcher) Option {
	return func(b *Builder) {
		b.httpFetcher = fetcher
	}
}

// WithCapabilities sets the HAProxy capabilities for conditional template generation.
// If nil, no capabilities map is added to the context.
func WithCapabilities(caps *dataplane.Capabilities) Option {
	return func(b *Builder) {
		b.capabilities = caps
	}
}

// WithCurrentConfig sets the current deployed HAProxy config for templates.
// This enables slot-aware server assignment and other config-aware features.
// The config is parsed from the HAProxyCfg CRD's spec.content field.
// If nil, templates receive nil currentConfig (first deployment case).
func WithCurrentConfig(cfg *parserconfig.StructuredConfig) Option {
	return func(b *Builder) {
		b.currentConfig = cfg
	}
}

// WithTypedResources supplies the per-resource generated Go types
// produced by typebootstrap (pkg/controller/typebootstrap). When set,
// Build emits one *additional* top-level context entry per supplied
// type: the resource's name maps to a *[]*<generated-struct> value
// populated by wrapping the matching store's snapshot through
// typegen.WrapSlice.
//
// The typed entries coexist with the existing map-keyed
// resources["<name>"] access — chart templates can adopt the typed
// shape per snippet without breaking templates that still use the
// untyped path. The two access paths may load their snapshots a
// few microseconds apart; templates are expected to use ONE shape
// or the OTHER for a given resource within a single render
// (mixing wouldn't compile anyway — the untyped variable is `any`).
//
// Resources whose names appear in `types` but for which no store
// is registered (via WithStores) are silently skipped. That keeps
// the option safe to use even when typebootstrap successfully
// generated a type for a resource the local controller doesn't
// happen to watch — a common case in tests.
func WithTypedResources(types map[string]reflect.Type) Option {
	return func(b *Builder) {
		b.typedResourceTypes = types
	}
}

// NewBuilder creates a new context builder with required dependencies.
//
// Parameters:
//   - cfg: Controller configuration (required)
//   - pathResolver: Path resolver for file paths (required)
//   - logger: Structured logger (required)
//   - opts: Optional configuration via functional options
func NewBuilder(cfg *config.Config, pathResolver *templating.PathResolver, logger *slog.Logger, opts ...Option) *Builder {
	b := &Builder{
		config:       cfg,
		pathResolver: pathResolver,
		logger:       logger,
	}

	for _, opt := range opts {
		opt(b)
	}

	return b
}

// BuildResult is the bundle returned from Build(). Callers that only need a
// subset (e.g. just the context map for a benchmark or test fixture) read the
// relevant field; the unused collectors are then garbage-collected with the
// result struct.
type BuildResult struct {
	Context                   map[string]any
	FileRegistry              *FileRegistry
	StatusPatchCollector      *templating.StatusPatchCollector
	RenderedResourceCollector *templating.RenderedResourceCollector
}

// Build creates the template rendering context, file registry, status patch
// collector, and rendered resource collector.
//
// The context structure is:
//
//	{
//	  "resources": map of StoreWrappers,
//	  "controller": {"haproxy_pods": StoreWrapper},
//	  "templateSnippets": []string,
//	  "fileRegistry": FileRegistry,
//	  "statusPatchCollector": StatusPatchCollector,
//	  "renderedResourceCollector": RenderedResourceCollector,
//	  "pathResolver": PathResolver,
//	  "dataplane": Config.Dataplane,
//	  "capabilities": map[string]bool (if set),
//	  "currentConfig": *StructuredConfig (nil on first deployment),
//	  "shared": map[string]any,
//	  "runtimeEnvironment": RuntimeEnvironment,
//	  "http": HTTPFetcher (if set),
//	  "extraContext": map from config,
//	}
func (b *Builder) Build() *BuildResult {
	// Create resources map with typed ResourceStore values. Each wrapper
	// gets the IndexBy that the watcher used to build the underlying
	// store; the wrapper uses it to build its per-render snapshot index
	// so List/Fetch/GetSingle on one wrapper instance all observe the
	// same store state (see StoreWrapper docs).
	resources := make(map[string]templating.ResourceStore)
	if b.stores != nil {
		for resourceTypeName, store := range b.stores {
			b.logger.Debug("wrapping store for rendering context",
				"resource_type", resourceTypeName)
			var indexBy []string
			if wr, ok := b.config.WatchedResources[resourceTypeName]; ok {
				indexBy = wr.IndexBy
			}
			resources[resourceTypeName] = &StoreWrapper{
				Store:        store,
				ResourceType: resourceTypeName,
				Logger:       b.logger,
				IndexBy:      indexBy,
			}
		}
	}

	// Create controller namespace with typed ResourceStore values. The
	// haproxy-pods watcher is auto-injected by ResourceWatcherComponent
	// with a fixed IndexBy of ["metadata.namespace", "metadata.name"]
	// (see pkg/controller/resourcewatcher/watcher.go) — mirror that here
	// so the wrapper's snapshot index agrees with the underlying store.
	controller := make(map[string]templating.ResourceStore)
	if b.haproxyPodStore != nil {
		b.logger.Debug("wrapping HAProxy pods store for rendering context")
		controller["haproxy_pods"] = &StoreWrapper{
			Store:        b.haproxyPodStore,
			ResourceType: names.HAProxyPodsResourceType,
			Logger:       b.logger,
			IndexBy:      []string{"metadata.namespace", "metadata.name"},
		}
	}

	// Sort template snippet names alphabetically
	snippetNames := SortSnippetNames(b.config.TemplateSnippets)

	// Create file registry for dynamic auxiliary file registration
	fileRegistry := NewFileRegistry(b.pathResolver)

	// Create status patch collector for template-driven status updates
	statusPatchCollector := templating.NewStatusPatchCollector()

	// Create rendered resource collector for template-driven owned-resource
	// reconciliation (per-Gateway Services for SupportGatewayStaticAddresses,
	// Listener-set membership, etc. — anything where the chart needs the
	// controller to spawn / update / prune Kubernetes resources). The
	// collector is resource-agnostic: templates pass any apiVersion / kind.
	renderedResourceCollector := templating.NewRenderedResourceCollector()

	b.logger.Debug("rendering context built",
		"resource_count", len(resources),
		"controller_fields", len(controller),
		"snippet_count", len(snippetNames))

	// Build final context
	templateContext := map[string]any{
		"resources":                 resources,
		"controller":                controller,
		"templateSnippets":          snippetNames,
		"fileRegistry":              fileRegistry,
		"statusPatchCollector":      statusPatchCollector,
		"renderedResourceCollector": renderedResourceCollector,
		"pathResolver":              b.pathResolver,
		"dataplane":                 b.config.Dataplane,
		"shared":                    templating.NewSharedContext(),
		"runtimeEnvironment": &templating.RuntimeEnvironment{
			GOMAXPROCS: runtime.GOMAXPROCS(0),
		},
	}

	// Add capabilities if provided
	if b.capabilities != nil {
		templateContext["capabilities"] = CapabilitiesToMap(b.capabilities)
	}

	// Add current config if provided (NOT added when nil - Scriggo panics with nil pointer initializers)
	// This enables slot-aware server assignment during rolling deployments
	// Templates should use isNil(currentConfig) to check if it's available
	if b.currentConfig != nil {
		templateContext["currentConfig"] = b.currentConfig
	}

	// Add HTTP fetcher if provided
	if b.httpFetcher != nil {
		b.logger.Debug("http object added to template context")
		templateContext["http"] = b.httpFetcher
	}

	// Add typed top-level globals for resources whose schemas
	// resolved at boot (typebootstrap produced a generated Go
	// type). Each entry wraps the matching store's snapshot in a
	// *[]*<generated-struct> shape — same shape Scriggo type-checks
	// the corresponding global declaration against (see
	// pkg/controller/typebootstrap.BuildEngineDeclarations).
	//
	// Resources whose Wrap fails (a single resource emitting a
	// shape inconsistent with the declared type — rare, would
	// indicate a watcher regression) are logged at warn and the
	// typed entry is omitted. Templates that compile against the
	// declared shape will then see a nil typed view; chart authors
	// fall back to the untyped resources["<name>"] for that one
	// resource. The chart still renders.
	b.addTypedResources(templateContext)

	// Merge extraContext variables into top-level context
	MergeExtraContextInto(templateContext, b.config)

	if b.config.TemplatingSettings.ExtraContext != nil {
		b.logger.Debug("added extra context variables to template context",
			"variable_count", len(b.config.TemplatingSettings.ExtraContext))
	}

	return &BuildResult{
		Context:                   templateContext,
		FileRegistry:              fileRegistry,
		StatusPatchCollector:      statusPatchCollector,
		RenderedResourceCollector: renderedResourceCollector,
	}
}

// addTypedResources populates the templateContext with one typed
// entry per [Builder.typedResourceTypes] mapping that has a matching
// underlying store registered. The entries are *[]*<generated-struct>
// values mirroring what
// [typebootstrap.BuildEngineDeclarations] declared.
//
// Method receiver rather than free function so the type-conversion
// loop can read b.stores / b.logger without threading them as
// arguments. Pulled out of Build() to keep that function under the
// per-function statement budget.
func (b *Builder) addTypedResources(ctx map[string]any) {
	if len(b.typedResourceTypes) == 0 || b.stores == nil {
		return
	}
	for name, t := range b.typedResourceTypes {
		store, ok := b.stores[name]
		if !ok {
			// The bootstrap produced a type for a resource the
			// local controller doesn't watch. Skip silently —
			// the unmatched type just doesn't show up in the
			// render context. The engine's declared global for
			// this name stays at its zero value (typed nil
			// pointer); templates that range over it iterate
			// zero items, which is the correct fail-open
			// behaviour. Common in tests; would only happen in
			// production if the watcher build raced ahead of
			// the bootstrap, which the iteration ordering
			// prevents.
			continue
		}

		items, err := store.List()
		if err != nil {
			b.logger.Warn("typed resource: store List failed; omitting typed view",
				"resource", name, "error", err)
			continue
		}
		typedSlice, err := typegen.WrapSlice(items, t)
		if err != nil {
			b.logger.Warn("typed resource: WrapSlice failed; omitting typed view",
				"resource", name, "error", err)
			continue
		}
		// Wrap the slice in a pointer for the *[]*T shape Scriggo
		// expects (matches BuildEngineDeclarations's declaration).
		holder := reflect.New(typedSlice.Type())
		holder.Elem().Set(typedSlice)
		ctx[name] = holder.Interface()
	}
}

// SortSnippetNames sorts template snippet names alphabetically.
// Returns a slice of snippet names in sorted order.
//
// Snippet ordering is controlled by encoding priority in the snippet name
// (e.g., "features-050-ssl" for priority 50). This is required because
// render_glob sorts templates alphabetically.
func SortSnippetNames(snippets map[string]config.TemplateSnippet) []string {
	sorted := make([]string, 0, len(snippets))
	for name := range snippets {
		sorted = append(sorted, name)
	}
	slices.Sort(sorted)
	return sorted
}

// MergeExtraContextInto merges the extraContext variables from the config into the provided template context.
//
// This allows templates to access custom variables directly (e.g., {{ debug.enabled }})
// instead of wrapping them in a "config" object.
//
// The extraContext key is always populated (with an empty map if nil) to prevent
// nil pointer dereferences in templates that use extraContext | dig("key") | fallback(default).
func MergeExtraContextInto(renderCtx map[string]any, cfg *config.Config) {
	if cfg.TemplatingSettings.ExtraContext != nil {
		// Merge at top level
		maps.Copy(renderCtx, cfg.TemplatingSettings.ExtraContext)
		// Also populate the extraContext map for Scriggo templates
		// Scriggo requires compile-time variable declarations, so templates
		// access extraContext values via: extraContext | dig("key") | fallback(default)
		renderCtx["extraContext"] = cfg.TemplatingSettings.ExtraContext
	} else {
		// Always set extraContext, even if empty, to prevent nil pointer dereferences
		// when templates use: extraContext | dig("key") | fallback(default)
		renderCtx["extraContext"] = map[string]any{}
	}
}

// CapabilitiesToMap converts a Capabilities struct to a template-friendly map.
// The map uses snake_case keys matching the Capabilities struct field names
// (e.g., "supports_waf" for SupportsWAF) for consistency with template conventions.
func CapabilitiesToMap(caps *dataplane.Capabilities) map[string]any {
	if caps == nil {
		return map[string]any{}
	}

	return map[string]any{
		// Storage capabilities
		"supports_crt_list":        caps.SupportsCrtList,
		"supports_map_storage":     caps.SupportsMapStorage,
		"supports_general_storage": caps.SupportsGeneralStorage,

		// Configuration capabilities
		"supports_http2": caps.SupportsHTTP2,
		"supports_quic":  caps.SupportsQUIC,

		// Runtime capabilities
		"supports_runtime_maps":    caps.SupportsRuntimeMaps,
		"supports_runtime_servers": caps.SupportsRuntimeServers,

		// Enterprise-only capabilities
		"supports_waf":                     caps.SupportsWAF,
		"supports_waf_global":              caps.SupportsWAFGlobal,
		"supports_waf_profiles":            caps.SupportsWAFProfiles,
		"supports_udp_lb_acls":             caps.SupportsUDPLBACLs,
		"supports_udp_lb_server_switching": caps.SupportsUDPLBServerSwitchingRules,
		"supports_keepalived":              caps.SupportsKeepalived,
		"supports_udp_load_balancing":      caps.SupportsUDPLoadBalancing,
		"supports_bot_management":          caps.SupportsBotManagement,
		"supports_git_integration":         caps.SupportsGitIntegration,
		"supports_dynamic_update":          caps.SupportsDynamicUpdate,
		"supports_aloha":                   caps.SupportsALOHA,
		"supports_advanced_logging":        caps.SupportsAdvancedLogging,
		"supports_ping":                    caps.SupportsPing,

		// Edition detection (convenience flags)
		"is_enterprise": caps.SupportsWAF, // Any enterprise capability indicates Enterprise edition
	}
}
