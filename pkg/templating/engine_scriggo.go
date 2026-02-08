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

package templating

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"gitlab.com/haproxy-haptic/scriggo"
	"gitlab.com/haproxy-haptic/scriggo/native"
)

// ScriggoEngine provides template compilation and rendering capabilities using Scriggo.
// It pre-compiles all templates at initialization for optimal runtime performance
// and early detection of syntax errors.
//
// Scriggo uses Go template syntax, which is different from Jinja2:
//   - Loops: {% for x := range items %}...{% end %}
//   - Conditionals: {% if cond %}...{% else if other %}...{% end %}
//   - Variables: {{ .name }} or {{ name }} when in globals
//
// This engine offers excellent performance and low memory usage with Go-style
// template syntax.
type ScriggoEngine struct {
	engineType        EngineType
	rawTemplates      map[string]string
	compiledTemplates map[string]*scriggo.Template
	postProcessors    map[string][]PostProcessor
	tracing           *scriggoTracingConfig
	globals           native.Declarations

	// Profiling support using Scriggo's built-in profiler
	profilingEnabled bool
	lastProfile      *scriggo.Profile
	profilingMu      sync.Mutex // Protects lastProfile
}

// Verify ScriggoEngine implements Engine interface at compile time.
var _ Engine = (*ScriggoEngine)(nil)

// New creates a new template engine of the specified type.
// Currently only EngineTypeScriggo is supported.
//
// All provided templates are compiled as entry points. This is the simplest API
// for creating a template engine. For more control over which templates are
// compiled vs discovered on-demand, use NewScriggo directly.
//
// Parameters:
//   - engineType: The type of engine to create (must be EngineTypeScriggo)
//   - templates: All template content (all are compiled as entry points)
//   - customFilters: Additional filters beyond built-in ones (can be nil)
//   - customFunctions: Additional global functions beyond built-in ones (can be nil)
//   - postProcessorConfigs: Post-processor configurations (can be nil)
//
// Returns an error if an unsupported engine type is specified.
//
// For domain-specific type declarations (e.g., currentConfig), use NewScriggoWithDeclarations.
func New(engineType EngineType, templates map[string]string, customFilters map[string]FilterFunc, customFunctions map[string]GlobalFunc, postProcessorConfigs map[string][]PostProcessorConfig) (Engine, error) {
	if engineType != EngineTypeScriggo {
		return nil, NewUnsupportedEngineError(engineType)
	}
	// Extract all template names as entry points (compile everything)
	entryPoints := make([]string, 0, len(templates))
	for name := range templates {
		entryPoints = append(entryPoints, name)
	}
	return NewScriggo(templates, entryPoints, customFilters, customFunctions, postProcessorConfigs)
}

// NewScriggo creates a new Scriggo (Go template syntax) template engine.
// This engine offers better performance but requires different template syntax.
//
// Parameters:
//   - templates: All template content (entry points + snippets)
//   - entryPoints: Template names to compile explicitly (others discovered via render calls)
//   - customFilters: Additional filters beyond built-in ones (can be nil)
//   - customFunctions: Additional global functions beyond built-in ones (can be nil)
//   - postProcessorConfigs: Post-processor configurations (can be nil)
//
// With inherit_context support: Only entryPoints are compiled explicitly. Template
// snippets are compiled automatically when referenced via render/render_glob statements.
//
// For domain-specific type declarations (e.g., currentConfig), use NewScriggoWithDeclarations.
func NewScriggo(templates map[string]string, entryPoints []string, customFilters map[string]FilterFunc, customFunctions map[string]GlobalFunc, postProcessorConfigs map[string][]PostProcessorConfig) (*ScriggoEngine, error) {
	return newScriggoEngine(templates, entryPoints, customFilters, customFunctions, postProcessorConfigs, nil, false)
}

// NewScriggoWithDeclarations creates a Scriggo engine with domain-specific type declarations.
// Use this when templates need access to types from other packages (e.g., currentConfig
// for slot-aware server assignment).
//
// Parameters:
//   - templates: All template content (entry points + snippets)
//   - entryPoints: Template names to compile explicitly (others discovered via render calls)
//   - customFilters: Additional filters beyond built-in ones (can be nil)
//   - customFunctions: Additional global functions beyond built-in ones (can be nil)
//   - postProcessorConfigs: Post-processor configurations (can be nil)
//   - additionalDeclarations: Domain-specific type declarations for Scriggo (can be nil)
//
// With inherit_context support: Only entryPoints are compiled explicitly. Template
// snippets are compiled automatically when referenced via render/render_glob statements.
func NewScriggoWithDeclarations(templates map[string]string, entryPoints []string, customFilters map[string]FilterFunc, customFunctions map[string]GlobalFunc, postProcessorConfigs map[string][]PostProcessorConfig, additionalDeclarations map[string]any) (*ScriggoEngine, error) {
	return newScriggoEngine(templates, entryPoints, customFilters, customFunctions, postProcessorConfigs, additionalDeclarations, false)
}

// NewScriggoWithProfiling creates a new Scriggo template engine with profiling enabled.
// When profiling is enabled, Scriggo's built-in profiler collects timing data for
// function calls, macros, and includes during execution.
//
// Parameters:
//   - templates: All template content (entry points + snippets)
//   - entryPoints: Template names to compile explicitly (others discovered via render calls)
//   - customFilters: Additional filters beyond built-in ones (can be nil)
//   - customFunctions: Additional global functions beyond built-in ones (can be nil)
//   - postProcessorConfigs: Post-processor configurations (can be nil)
//
// Note: Profiling adds minimal runtime overhead. Use NewScriggo for production without profiling.
//
// With inherit_context support: Only entryPoints are compiled explicitly. Template
// snippets are compiled automatically when referenced via render/render_glob statements.
//
// For domain-specific type declarations with profiling, use NewScriggoWithProfilingAndDeclarations.
func NewScriggoWithProfiling(templates map[string]string, entryPoints []string, customFilters map[string]FilterFunc, customFunctions map[string]GlobalFunc, postProcessorConfigs map[string][]PostProcessorConfig) (*ScriggoEngine, error) {
	return newScriggoEngine(templates, entryPoints, customFilters, customFunctions, postProcessorConfigs, nil, true)
}

// NewScriggoWithProfilingAndDeclarations creates a Scriggo engine with both profiling
// and domain-specific type declarations enabled.
//
// Parameters:
//   - templates: All template content (entry points + snippets)
//   - entryPoints: Template names to compile explicitly (others discovered via render calls)
//   - customFilters: Additional filters beyond built-in ones (can be nil)
//   - customFunctions: Additional global functions beyond built-in ones (can be nil)
//   - postProcessorConfigs: Post-processor configurations (can be nil)
//   - additionalDeclarations: Domain-specific type declarations for Scriggo (can be nil)
//
// With inherit_context support: Only entryPoints are compiled explicitly. Template
// snippets are compiled automatically when referenced via render/render_glob statements.
func NewScriggoWithProfilingAndDeclarations(templates map[string]string, entryPoints []string, customFilters map[string]FilterFunc, customFunctions map[string]GlobalFunc, postProcessorConfigs map[string][]PostProcessorConfig, additionalDeclarations map[string]any) (*ScriggoEngine, error) {
	return newScriggoEngine(templates, entryPoints, customFilters, customFunctions, postProcessorConfigs, additionalDeclarations, true)
}

// newScriggoEngine is the internal constructor that handles both profiling and non-profiling modes.
//
// Parameters:
//   - templates: All template content (entry points + snippets) for the filesystem
//   - entryPoints: Template names to compile explicitly
//   - customFilters, customFunctions, postProcessorConfigs: Standard engine options
//   - additionalDeclarations: Domain-specific type declarations for Scriggo (can be nil)
//   - enableProfiling: Whether to enable Scriggo's built-in profiler
//
// Only entryPoints are compiled explicitly. Template snippets in templates but not in
// entryPoints are discovered and compiled automatically by Scriggo when referenced
// via render/render_glob statements with inherit_context.
func newScriggoEngine(templates map[string]string, entryPoints []string, customFilters map[string]FilterFunc, customFunctions map[string]GlobalFunc, postProcessorConfigs map[string][]PostProcessorConfig, additionalDeclarations map[string]any, enableProfiling bool) (*ScriggoEngine, error) {
	engine := &ScriggoEngine{
		engineType:        EngineTypeScriggo,
		rawTemplates:      make(map[string]string, len(templates)),
		compiledTemplates: make(map[string]*scriggo.Template, len(entryPoints)),
		postProcessors:    make(map[string][]PostProcessor),
		tracing: &scriggoTracingConfig{
			enabled: false,
			traces:  make([]string, 0),
		},
		profilingEnabled: enableProfiling,
	}

	// Build globals (filters become functions in Scriggo)
	engine.globals = buildScriggoGlobals(customFilters, customFunctions, additionalDeclarations)

	// Store raw templates (all templates, not just entry points)
	for name, content := range templates {
		engine.rawTemplates[name] = content
	}

	// Compile only entry points (snippets compiled on-demand by Scriggo)
	if err := engine.compileTemplates(templates, entryPoints); err != nil {
		return nil, err
	}

	// Build post-processors
	if err := buildScriggoPostProcessors(engine, postProcessorConfigs); err != nil {
		return nil, err
	}

	return engine, nil
}

// compileTemplates compiles entry point templates using Scriggo.
//
// Only templates listed in entryPoints are compiled explicitly. Template snippets
// are discovered and compiled automatically by Scriggo when referenced via
// render/render_glob statements with inherit_context.
//
// Parameters:
//   - allTemplates: All template content (for filesystem - includes snippets)
//   - entryPoints: Template names to compile explicitly
//
// The filesystem contains ALL templates so Scriggo can discover snippets, but only
// entryPoints are compiled into e.compiledTemplates.
//
// If profiling is enabled, BuildOptions.EnableProfiling is set to enable Scriggo's built-in profiler.
func (e *ScriggoEngine) compileTemplates(allTemplates map[string]string, entryPoints []string) error {
	// Create filesystem with ALL templates (so Scriggo can discover snippets)
	fsys := &scriggoTemplateFS{templates: allTemplates}

	// Only compile entry points
	for _, name := range entryPoints {
		opts := &scriggo.BuildOptions{
			Globals:         e.globals,
			EnableProfiling: e.profilingEnabled,
			AllowGoStmt:     true, // Enable parallel template rendering (go MacroName(), go render)
		}

		compiled, err := scriggo.BuildTemplate(fsys, name, opts)
		if err != nil {
			return NewCompilationError(name, allTemplates[name], err)
		}

		e.compiledTemplates[name] = compiled
	}

	return nil
}

// Render executes a template with the given context and returns the output.
func (e *ScriggoEngine) Render(ctx context.Context, templateName string, templateContext map[string]interface{}) (string, error) {
	template, exists := e.compiledTemplates[templateName]
	if !exists {
		return "", e.templateNotFoundError(templateName)
	}

	// Ensure template context exists with shared context for cross-template caching.
	// This allows first_seen and other cache functions to work even when caller
	// passes nil context (e.g., in tests).
	if templateContext == nil {
		templateContext = make(map[string]interface{})
	}
	if _, ok := templateContext["shared"]; !ok {
		templateContext["shared"] = NewSharedContext()
	}

	// Setup tracing if enabled
	e.tracing.mu.Lock()
	tracingEnabled := e.tracing.enabled
	e.tracing.mu.Unlock()

	var traceBuilder *strings.Builder
	var startTime time.Time
	if tracingEnabled {
		traceBuilder = &strings.Builder{}
		startTime = time.Now()
		fmt.Fprintf(traceBuilder, "Rendering: %s\n", templateName)
	}

	// Add render context (globals) for resource accessor functions like first_seen
	ctx = context.WithValue(ctx, RenderContextContextKey, templateContext)

	// Setup run options with profiling and parallelism settings
	runOpts := &scriggo.RunOptions{
		Context: ctx,
	}

	// Create profile receiver if profiling is enabled
	var profile *scriggo.Profile
	if e.profilingEnabled {
		profile = &scriggo.Profile{}
		runOpts.Profile = profile
	}

	// Execute template
	var output strings.Builder
	err := template.Run(&output, templateContext, runOpts)
	if err != nil {
		if ctx.Err() != nil {
			return "", &RenderTimeoutError{TemplateName: templateName, Cause: ctx.Err()}
		}
		return "", NewRenderError(templateName, err)
	}

	result := output.String()

	// Ensure output ends with a newline (required by HAProxy)
	if result == "" || result[len(result)-1] != '\n' {
		result += "\n"
	}

	// Apply post-processors
	result, err = e.applyPostProcessors(templateName, result)
	if err != nil {
		return "", err
	}

	// Complete tracing - build trace from profile if available
	if tracingEnabled {
		e.storeTraceOutput(templateName, profile, traceBuilder, time.Since(startTime))
	}

	// Store profile for retrieval
	if profile != nil {
		e.profilingMu.Lock()
		e.lastProfile = profile
		e.profilingMu.Unlock()
	}

	return result, nil
}

// RenderWithProfiling renders a template and returns profiling statistics.
//
// When profiling is enabled (via NewScriggoWithProfiling), this method returns
// aggregated include timing statistics. When profiling is disabled, returns nil
// for the stats slice.
func (e *ScriggoEngine) RenderWithProfiling(ctx context.Context, templateName string, templateContext map[string]interface{}) (string, []IncludeStats, error) {
	output, err := e.Render(ctx, templateName, templateContext)
	if err != nil {
		return "", nil, err
	}

	// If profiling is not enabled, return nil stats
	if !e.profilingEnabled {
		return output, nil, nil
	}

	// Get the last profile and convert to IncludeStats
	e.profilingMu.Lock()
	profile := e.lastProfile
	e.profilingMu.Unlock()

	if profile == nil || len(profile.Calls) == 0 {
		return output, nil, nil
	}

	stats := aggregateScriggoProfile(profile)
	return output, stats, nil
}

// templateNotFoundError creates a TemplateNotFoundError with available templates.
func (e *ScriggoEngine) templateNotFoundError(templateName string) error {
	available := make([]string, 0, len(e.compiledTemplates))
	for name := range e.compiledTemplates {
		available = append(available, name)
	}
	sort.Strings(available)
	return NewTemplateNotFoundError(templateName, available)
}

// applyPostProcessors applies the post-processor chain to the output.
func (e *ScriggoEngine) applyPostProcessors(templateName, output string) (string, error) {
	processors, exists := e.postProcessors[templateName]
	if !exists || len(processors) == 0 {
		return output, nil
	}

	result := output
	for _, processor := range processors {
		var err error
		result, err = processor.Process(result)
		if err != nil {
			return "", NewRenderError(templateName, err)
		}
	}
	return result, nil
}

// EngineType returns the type of this engine.
func (e *ScriggoEngine) EngineType() EngineType {
	return e.engineType
}

// TemplateNames returns the names of all available templates, sorted alphabetically.
func (e *ScriggoEngine) TemplateNames() []string {
	names := make([]string, 0, len(e.compiledTemplates))
	for name := range e.compiledTemplates {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// HasTemplate checks if a template with the given name exists.
func (e *ScriggoEngine) HasTemplate(templateName string) bool {
	_, exists := e.compiledTemplates[templateName]
	return exists
}

// GetRawTemplate returns the original template string for the given name.
func (e *ScriggoEngine) GetRawTemplate(templateName string) (string, error) {
	raw, exists := e.rawTemplates[templateName]
	if !exists {
		return "", e.templateNotFoundError(templateName)
	}
	return raw, nil
}

// TemplateCount returns the number of templates in the engine.
func (e *ScriggoEngine) TemplateCount() int {
	return len(e.compiledTemplates)
}

// IsProfilingEnabled returns whether profiling is enabled for this engine.
func (e *ScriggoEngine) IsProfilingEnabled() bool {
	return e.profilingEnabled
}

// ClearVMPool releases pooled Scriggo VMs to allow garbage collection.
// Call after rendering completes to reduce memory from parallel rendering spikes.
//
// This is safe to call at any time - VMs currently in use are not affected
// (they're held by goroutines, not in the pool). Only pooled VMs waiting for
// reuse are released.
func (e *ScriggoEngine) ClearVMPool() {
	scriggo.ClearVMPool()
}

// GetProfilingResults returns profiling data from the last render operation.
// Returns nil if profiling is disabled or no render has occurred.
// The results contain include/render call records from Scriggo's built-in profiler.
func (e *ScriggoEngine) GetProfilingResults() []ProfilingEntry {
	e.profilingMu.Lock()
	defer e.profilingMu.Unlock()

	if e.lastProfile == nil {
		return nil
	}

	// Convert scriggo.Profile calls to ProfilingEntry slice
	// Filter to macro/render operations
	// Note: In Scriggo, `render` statements compile to OpCallMacro (CallKindMacro)
	var entries []ProfilingEntry
	for _, call := range e.lastProfile.Calls {
		if call.Kind == scriggo.CallKindMacro {
			name := call.TemplatePath
			if name == "" {
				name = call.Name
			}
			entries = append(entries, ProfilingEntry{
				Name:     name,
				Path:     call.File,
				Duration: call.Duration,
			})
		}
	}
	return entries
}

// buildScriggoPostProcessors builds post-processors for the Scriggo engine.
func buildScriggoPostProcessors(engine *ScriggoEngine, configs map[string][]PostProcessorConfig) error {
	for templateName, procConfigs := range configs {
		processors := make([]PostProcessor, 0, len(procConfigs))
		for _, cfg := range procConfigs {
			processor, err := NewPostProcessor(cfg)
			if err != nil {
				return err
			}
			processors = append(processors, processor)
		}
		engine.postProcessors[templateName] = processors
	}
	return nil
}
