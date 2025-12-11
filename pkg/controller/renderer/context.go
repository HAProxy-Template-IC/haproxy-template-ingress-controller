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
	"sort"

	"haproxy-template-ic/pkg/controller/httpstore"
	"haproxy-template-ic/pkg/core/config"
	"haproxy-template-ic/pkg/templating"
)

// buildRenderingContext wraps stores for template access and builds the template context.
//
// The context structure is:
//
//	{
//	  "resources": {
//	    "ingresses": StoreWrapper,  // Provides List(), Fetch(), and GetSingle() methods
//	    "services": StoreWrapper,
//	    "secrets": StoreWrapper,
//	    // ... other watched resources
//	  },
//	  "controller": {
//	    "haproxy_pods": StoreWrapper,  // HAProxy controller pods for pod-maxconn calculations
//	  },
//	  "template_snippets": ["snippet1", "snippet2", ...]  // Sorted by priority
//	  "config": Config,  // Controller configuration (e.g., config.debug.headers.enabled)
//	  "file_registry": FileRegistry,  // For dynamic auxiliary file registration
//	  "pathResolver": PathResolver,  // For resolving file paths (e.g., {{ pathResolver.GetPath("cert.pem", "cert") }})
//	  "capabilities": {  // HAProxy/DataPlane API capabilities
//	    "supports_waf": true,           // WAF (Enterprise only)
//	    "supports_keepalived": true,    // Keepalived/VRRP (Enterprise only)
//	    "supports_crt_list": true,      // CRT-list storage (v3.2+)
//	    // ... other capability flags
//	  },
//	}
//
// Templates can access resources:
//
//	{% for ingress in resources.ingresses.List() %}
//	  {{ ingress.metadata.name }}
//	{% endfor %}
//
// Templates can access controller metadata:
//
//	{%- set pod_count = controller.haproxy_pods.List() | length %}
//	{%- if pod_count > 0 %}
//	  {# Distribute load across {{ pod_count }} HAProxy replicas #}
//	{%- endif %}
//
// Iterate over matching template snippets:
//
//	{%- set matching = template_snippets | glob_match("backend-annotation-*") %}
//	{%- for snippet_name in matching %}
//	  {% include snippet_name %}
//	{%- endfor %}
//
// And access controller configuration:
//
//	{%- if config.debug.headers.enabled | default(false) %}
//	  http-response set-header X-Debug-Info %[var(txn.backend_name)]
//	{%- endif %}
//
// And dynamically register auxiliary files:
//
//	{%- set ca_content = secret.data["ca.crt"] | b64decode %}
//	{%- set ca_path = file_registry.Register("cert", "ca.pem", ca_content) %}
//	server backend:443 ssl ca-file {{ ca_path }} verify required
//
// And resolve static file paths:
//
//	use_backend {{ backend_name }} if { req.hdr(host) -f {{ pathResolver.GetPath("host.map", "map") }} }
//
// And conditionally generate Enterprise-specific configuration:
//
//	{%- if capabilities.supports_waf %}
//	  # Enterprise WAF configuration
//	  filter spoe engine modsecurity
//	{%- endif %}
func (c *Component) buildRenderingContext(ctx context.Context, pathResolver *templating.PathResolver, isValidation bool) (map[string]interface{}, *FileRegistry) {
	// Create resources map with wrapped stores
	resources := make(map[string]interface{})

	// Wrap each store to provide template-friendly methods
	for resourceTypeName, store := range c.stores {
		c.logger.Debug("wrapping store for rendering context",
			"resource_type", resourceTypeName)
		resources[resourceTypeName] = &StoreWrapper{
			Store:        store,
			ResourceType: resourceTypeName,
			Logger:       c.logger,
		}
	}

	// Create controller namespace with HAProxy pods store
	controller := make(map[string]interface{})
	if c.haproxyPodStore != nil {
		c.logger.Debug("wrapping HAProxy pods store for rendering context")
		controller["haproxy_pods"] = &StoreWrapper{
			Store:        c.haproxyPodStore,
			ResourceType: "haproxy-pods",
			Logger:       c.logger,
		}
	} else {
		c.logger.Warn("HAProxy pods store is nil, controller.haproxy_pods will not be available")
	}

	// Sort template snippets by priority for template access
	snippetNames := sortSnippetsByPriority(c.config.TemplateSnippets)

	// Create file registry for dynamic auxiliary file registration
	fileRegistry := NewFileRegistry(pathResolver)

	c.logger.Debug("rendering context built",
		"resource_count", len(resources),
		"controller_fields", len(controller),
		"snippet_count", len(snippetNames))

	// Build final context
	templateContext := map[string]interface{}{
		"resources":         resources,
		"controller":        controller,
		"template_snippets": snippetNames,
		"file_registry":     fileRegistry,
		"pathResolver":      pathResolver,
		"dataplane":         c.config.Dataplane,           // Add dataplane config for absolute path access
		"capabilities":      c.capabilitiesToMap(),        // Add HAProxy/DataPlane API capabilities
		"shared":            make(map[string]interface{}), // Shared namespace for cross-template data
	}

	// Add HTTP store wrapper if available
	if c.httpStoreComponent != nil {
		httpWrapper := httpstore.NewHTTPStoreWrapper(
			c.httpStoreComponent,
			c.logger,
			isValidation,
			ctx,
		)
		c.logger.Debug("http object added to template context",
			"http_wrapper_type", fmt.Sprintf("%T", httpWrapper))
		templateContext["http"] = httpWrapper
	} else {
		c.logger.Warn("httpStoreComponent is nil, http.Fetch() will not be available in templates")
	}

	// Merge extraContext variables into top-level context
	MergeExtraContextInto(templateContext, c.config)

	if c.config.TemplatingSettings.ExtraContext != nil {
		c.logger.Debug("added extra context variables to template context",
			"variable_count", len(c.config.TemplatingSettings.ExtraContext))
	}

	return templateContext, fileRegistry
}

// sortSnippetsByPriority sorts template snippet names by priority, then alphabetically.
// Returns a slice of snippet names in the sorted order.
//
// Snippets without an explicit priority default to 500.
// Lower priority values are sorted first.
func sortSnippetsByPriority(snippets map[string]config.TemplateSnippet) []string {
	type snippetWithPriority struct {
		name     string
		priority int
	}

	// Build list with priorities
	list := make([]snippetWithPriority, 0, len(snippets))
	for name, snippet := range snippets {
		priority := snippet.Priority
		if priority == 0 {
			priority = 500 // Default priority
		}
		list = append(list, snippetWithPriority{name, priority})
	}

	// Sort by priority (ascending), then by name (alphabetically)
	sort.Slice(list, func(i, j int) bool {
		if list[i].priority != list[j].priority {
			return list[i].priority < list[j].priority
		}
		return list[i].name < list[j].name
	})

	// Extract sorted names
	names := make([]string, len(list))
	for i, item := range list {
		names[i] = item.name
	}

	return names
}

// MergeExtraContextInto merges the extraContext variables from the config into the provided template context.
//
// This allows templates to access custom variables directly (e.g., {{ debug.enabled }})
// instead of wrapping them in a "config" object.
//
// If extraContext is nil or empty, the context is left unchanged.
func MergeExtraContextInto(renderCtx map[string]interface{}, cfg *config.Config) {
	if cfg.TemplatingSettings.ExtraContext != nil {
		for key, value := range cfg.TemplatingSettings.ExtraContext {
			renderCtx[key] = value
		}
	}
}

// capabilitiesToMap converts the Capabilities struct to a template-friendly map.
//
// The map uses snake_case keys matching the Capabilities struct field names
// (e.g., "supports_waf" for SupportsWAF) for consistency with template conventions.
//
// This enables conditional template generation based on HAProxy/DataPlane API features:
//
//	{%- if capabilities.supports_waf %}
//	  # Enterprise WAF configuration
//	  filter spoe engine modsecurity
//	{%- endif %}
//
//	{%- if capabilities.supports_crt_list %}
//	  # Use CRT-list for SSL certificates (v3.2+)
//	{%- endif %}
func (c *Component) capabilitiesToMap() map[string]interface{} {
	caps := c.capabilities
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
