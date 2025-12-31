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
//	ctx, fileRegistry := builder.Build()
package rendercontext

import (
	"log/slog"
	"runtime"
	"sort"

	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser/parserconfig"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
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
	stores          map[string]types.Store
	haproxyPodStore types.Store
	httpFetcher     templating.HTTPFetcher
	capabilities    *dataplane.Capabilities
	currentConfig   *parserconfig.StructuredConfig
}

// Option configures a Builder.
type Option func(*Builder)

// WithStores sets the resource stores for the template context.
// Each store is wrapped in a StoreWrapper to provide template-friendly methods.
func WithStores(stores map[string]types.Store) Option {
	return func(b *Builder) {
		b.stores = stores
	}
}

// WithHAProxyPodStore sets the HAProxy pod store for controller.haproxy_pods.
// This enables templates to access HAProxy pod count for calculations.
func WithHAProxyPodStore(store types.Store) Option {
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

// Build creates the template rendering context and file registry.
//
// The context structure is:
//
//	{
//	  "resources": map of StoreWrappers,
//	  "controller": {"haproxy_pods": StoreWrapper},
//	  "templateSnippets": []string,
//	  "fileRegistry": FileRegistry,
//	  "pathResolver": PathResolver,
//	  "dataplane": Config.Dataplane,
//	  "capabilities": map[string]bool (if set),
//	  "currentConfig": *StructuredConfig (nil on first deployment),
//	  "shared": map[string]interface{},
//	  "runtimeEnvironment": RuntimeEnvironment,
//	  "http": HTTPFetcher (if set),
//	  "extraContext": map from config,
//	}
func (b *Builder) Build() (map[string]interface{}, *FileRegistry) {
	// Create resources map with typed ResourceStore values
	resources := make(map[string]templating.ResourceStore)
	if b.stores != nil {
		for resourceTypeName, store := range b.stores {
			b.logger.Debug("wrapping store for rendering context",
				"resource_type", resourceTypeName)
			resources[resourceTypeName] = &StoreWrapper{
				Store:        store,
				ResourceType: resourceTypeName,
				Logger:       b.logger,
			}
		}
	}

	// Create controller namespace with typed ResourceStore values
	controller := make(map[string]templating.ResourceStore)
	if b.haproxyPodStore != nil {
		b.logger.Debug("wrapping HAProxy pods store for rendering context")
		controller["haproxy_pods"] = &StoreWrapper{
			Store:        b.haproxyPodStore,
			ResourceType: "haproxy-pods",
			Logger:       b.logger,
		}
	}

	// Sort template snippet names alphabetically
	snippetNames := SortSnippetNames(b.config.TemplateSnippets)

	// Create file registry for dynamic auxiliary file registration
	fileRegistry := NewFileRegistry(b.pathResolver)

	b.logger.Debug("rendering context built",
		"resource_count", len(resources),
		"controller_fields", len(controller),
		"snippet_count", len(snippetNames))

	// Build final context
	templateContext := map[string]interface{}{
		"resources":        resources,
		"controller":       controller,
		"templateSnippets": snippetNames,
		"fileRegistry":     fileRegistry,
		"pathResolver":     b.pathResolver,
		"dataplane":        b.config.Dataplane,
		"shared":           templating.NewSharedContext(),
		"runtimeEnvironment": &templating.RuntimeEnvironment{
			GOMAXPROCS: runtime.GOMAXPROCS(0),
		},
	}

	// Add capabilities if provided
	if b.capabilities != nil {
		templateContext["capabilities"] = capabilitiesToMap(b.capabilities)
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

	// Merge extraContext variables into top-level context
	MergeExtraContextInto(templateContext, b.config)

	if b.config.TemplatingSettings.ExtraContext != nil {
		b.logger.Debug("added extra context variables to template context",
			"variable_count", len(b.config.TemplatingSettings.ExtraContext))
	}

	return templateContext, fileRegistry
}

// SortSnippetNames sorts template snippet names alphabetically.
// Returns a slice of snippet names in sorted order.
//
// Note: Snippet ordering is now controlled by encoding priority in the snippet name
// (e.g., "features-050-ssl" for priority 50). This is required because render_glob
// sorts templates alphabetically.
func SortSnippetNames(snippets map[string]config.TemplateSnippet) []string {
	names := make([]string, 0, len(snippets))
	for name := range snippets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// MergeExtraContextInto merges the extraContext variables from the config into the provided template context.
//
// This allows templates to access custom variables directly (e.g., {{ debug.enabled }})
// instead of wrapping them in a "config" object.
//
// The extraContext key is always populated (with an empty map if nil) to prevent
// nil pointer dereferences in templates that use extraContext | dig("key") | fallback(default).
func MergeExtraContextInto(renderCtx map[string]interface{}, cfg *config.Config) {
	if cfg.TemplatingSettings.ExtraContext != nil {
		// Merge at top level
		for key, value := range cfg.TemplatingSettings.ExtraContext {
			renderCtx[key] = value
		}
		// Also populate the extraContext map for Scriggo templates
		// Scriggo requires compile-time variable declarations, so templates
		// access extraContext values via: extraContext | dig("key") | fallback(default)
		renderCtx["extraContext"] = cfg.TemplatingSettings.ExtraContext
	} else {
		// Always set extraContext, even if empty, to prevent nil pointer dereferences
		// when templates use: extraContext | dig("key") | fallback(default)
		renderCtx["extraContext"] = map[string]interface{}{}
	}
}

// capabilitiesToMap converts the Capabilities struct to a template-friendly map.
//
// The map uses snake_case keys matching the Capabilities struct field names
// (e.g., "supports_waf" for SupportsWAF) for consistency with template conventions.
func capabilitiesToMap(caps *dataplane.Capabilities) map[string]interface{} {
	if caps == nil {
		return map[string]interface{}{}
	}

	return map[string]interface{}{
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
