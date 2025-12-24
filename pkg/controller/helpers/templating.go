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

// Package helpers provides shared utility functions for the controller layer.
package helpers

import (
	"fmt"

	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/core/config"
	"gitlab.com/haproxy-template-ic/haproxy-template-ingress-controller/pkg/templating"
)

// EngineOptions configures template engine creation.
type EngineOptions struct {
	// EnableProfiling enables include timing profiling for the engine.
	// When true, RenderWithProfiling() returns actual timing statistics.
	EnableProfiling bool
}

// TemplateExtraction contains templates and entry points extracted from configuration.
//
// For Scriggo engines with inherit_context support, only entry points are compiled explicitly.
// Snippet templates are discovered and compiled automatically by Scriggo when referenced
// via render/render_glob statements.
type TemplateExtraction struct {
	// AllTemplates contains all template content (entry points + snippets).
	// This is provided to the template engine's filesystem so all templates are accessible.
	AllTemplates map[string]string

	// EntryPoints lists template names that should be compiled explicitly.
	// Only these are compiled; snippets are compiled on-demand.
	EntryPoints []string
}

// NewEngineFromConfig creates a template engine from configuration.
//
// This is a convenience function that handles the common pattern of:
//  1. Extracting templates from configuration
//  2. Parsing the engine type from configuration
//  3. Creating the template engine
//
// All standard filters (sort_by, glob_match, b64decode, strip, trim, debug) and the
// fail() function are registered internally by each engine. Callers only need to
// pass custom filters/functions if they have additional ones beyond the standard set.
//
// Parameters:
//   - cfg: Configuration containing templates and engine settings
//   - globalFunctions: Optional custom global functions. Can be nil (fail is auto-registered)
//   - postProcessorConfigs: Optional post-processor configurations. Can be nil
//
// Returns the initialized Engine or an error if initialization fails.
func NewEngineFromConfig(
	cfg *config.Config,
	globalFunctions map[string]templating.GlobalFunc,
	postProcessorConfigs map[string][]templating.PostProcessorConfig,
) (templating.Engine, error) {
	return NewEngineFromConfigWithOptions(cfg, globalFunctions, postProcessorConfigs, EngineOptions{})
}

// NewEngineFromConfigWithOptions creates a template engine with additional options.
//
// This is the full-featured version of NewEngineFromConfig that accepts EngineOptions
// for controlling profiling and other engine-specific settings.
//
// If postProcessorConfigs is nil, post-processors are automatically extracted from
// the configuration. Pass an explicit empty map to disable post-processing.
//
// For Scriggo engines: Only entry points are compiled explicitly. Template snippets
// are discovered and compiled automatically when referenced via render/render_glob
// with inherit_context.
func NewEngineFromConfigWithOptions(
	cfg *config.Config,
	globalFunctions map[string]templating.GlobalFunc,
	postProcessorConfigs map[string][]templating.PostProcessorConfig,
	options EngineOptions,
) (templating.Engine, error) {
	// Extract templates and entry points from config
	extraction := ExtractTemplatesFromConfig(cfg)

	// Extract post-processor configs if not explicitly provided
	if postProcessorConfigs == nil {
		postProcessorConfigs = ExtractPostProcessorConfigs(cfg)
	}

	// Parse engine type (defaults to Scriggo if not specified)
	_, err := templating.ParseEngineType(cfg.TemplatingSettings.Engine)
	if err != nil {
		return nil, fmt.Errorf("invalid template engine type: %w", err)
	}

	// Create engine with all components
	// Note: All standard filters and the fail() function are registered internally.
	// We pass nil for filters since none are needed beyond the engine's built-in set.
	var engine templating.Engine
	if options.EnableProfiling {
		engine, err = templating.NewScriggoWithProfiling(
			extraction.AllTemplates,
			extraction.EntryPoints,
			nil,
			globalFunctions,
			postProcessorConfigs,
		)
	} else {
		engine, err = templating.NewScriggo(
			extraction.AllTemplates,
			extraction.EntryPoints,
			nil,
			globalFunctions,
			postProcessorConfigs,
		)
	}
	if err != nil {
		return nil, err
	}

	return engine, nil
}

// ExtractTemplatesFromConfig extracts all templates from the configuration structure.
//
// Returns:
//   - AllTemplates: All template content (entry points + snippets)
//   - EntryPoints: Templates that should be compiled explicitly (main config + auxiliary files)
//
// Entry points are:
//   - Main HAProxy configuration (haproxy.cfg)
//   - Map file templates
//   - General file templates
//   - SSL certificate templates
//
// Template snippets are NOT entry points - they are discovered and compiled automatically
// by Scriggo when referenced via render/render_glob statements with inherit_context.
func ExtractTemplatesFromConfig(cfg *config.Config) TemplateExtraction {
	extraction := TemplateExtraction{
		AllTemplates: make(map[string]string),
		EntryPoints:  []string{},
	}

	// Main HAProxy config (entry point)
	extraction.AllTemplates["haproxy.cfg"] = cfg.HAProxyConfig.Template
	extraction.EntryPoints = append(extraction.EntryPoints, "haproxy.cfg")

	// Template snippets (NOT entry points - discovered via render calls)
	for name, snippet := range cfg.TemplateSnippets {
		extraction.AllTemplates[name] = snippet.Template
	}

	// Map files (entry points)
	for name, mapDef := range cfg.Maps {
		extraction.AllTemplates[name] = mapDef.Template
		extraction.EntryPoints = append(extraction.EntryPoints, name)
	}

	// General files (entry points)
	for name, fileDef := range cfg.Files {
		extraction.AllTemplates[name] = fileDef.Template
		extraction.EntryPoints = append(extraction.EntryPoints, name)
	}

	// SSL certificates (entry points)
	for name, certDef := range cfg.SSLCertificates {
		extraction.AllTemplates[name] = certDef.Template
		extraction.EntryPoints = append(extraction.EntryPoints, name)
	}

	return extraction
}

// ExtractPostProcessorConfigs extracts post-processor configurations from all templates.
//
// This includes post-processors for:
//   - Main HAProxy configuration (haproxy.cfg)
//   - Map file templates
//   - General file templates
//   - SSL certificate templates
//
// Returns a map of template names to their post-processor configurations.
func ExtractPostProcessorConfigs(cfg *config.Config) map[string][]templating.PostProcessorConfig {
	configs := make(map[string][]templating.PostProcessorConfig)

	// Main HAProxy config
	if len(cfg.HAProxyConfig.PostProcessing) > 0 {
		configs["haproxy.cfg"] = convertPostProcessorConfigs(cfg.HAProxyConfig.PostProcessing)
	}

	// Map files
	for name, mapDef := range cfg.Maps {
		if len(mapDef.PostProcessing) > 0 {
			configs[name] = convertPostProcessorConfigs(mapDef.PostProcessing)
		}
	}

	// General files
	for name, fileDef := range cfg.Files {
		if len(fileDef.PostProcessing) > 0 {
			configs[name] = convertPostProcessorConfigs(fileDef.PostProcessing)
		}
	}

	// SSL certificates
	for name, certDef := range cfg.SSLCertificates {
		if len(certDef.PostProcessing) > 0 {
			configs[name] = convertPostProcessorConfigs(certDef.PostProcessing)
		}
	}

	return configs
}

// convertPostProcessorConfigs converts config.PostProcessorConfig to templating.PostProcessorConfig.
func convertPostProcessorConfigs(postProcessors []config.PostProcessorConfig) []templating.PostProcessorConfig {
	ppConfigs := make([]templating.PostProcessorConfig, len(postProcessors))
	for i, pp := range postProcessors {
		ppConfigs[i] = templating.PostProcessorConfig{
			Type:   templating.PostProcessorType(pp.Type),
			Params: pp.Params,
		}
	}
	return ppConfigs
}
