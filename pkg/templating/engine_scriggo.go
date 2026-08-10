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
	"maps"
	"slices"
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

// Options configures a template engine. The zero value (or a nil *Options)
// compiles every template as an entry point, with no custom filters,
// functions, post-processors, type declarations, or profiling.
type Options struct {
	// EntryPoints lists template names to compile explicitly; the remaining
	// templates are snippets, discovered and compiled automatically when
	// referenced via render/render_glob statements with inherit_context.
	// nil means every template is an entry point.
	EntryPoints []string
	// Filters are custom filters merged over the built-in set (can be nil).
	Filters map[string]FilterFunc
	// Functions are custom global functions merged over the built-in set
	// (can be nil).
	Functions map[string]GlobalFunc
	// PostProcessors configures per-template post-processing (can be nil).
	PostProcessors map[string][]PostProcessorConfig
	// Declarations registers domain-specific types with Scriggo (e.g.
	// currentConfig for slot-aware server assignment). Use this when
	// templates need access to types from other packages (can be nil).
	Declarations map[string]any
	// Profiling enables Scriggo's built-in profiler, which collects timing
	// data for function calls, macros, and includes during execution
	// (see RenderWithProfiling). Profiling adds minimal runtime overhead.
	Profiling bool
}

// New creates a Scriggo (Go template syntax) template engine.
//
// Only opts.EntryPoints are compiled explicitly — or every template, when
// opts is nil or opts.EntryPoints is nil — so syntax errors are caught
// early. Templates not listed as entry points are snippets, compiled
// automatically when referenced via render/render_glob statements with
// inherit_context.
func New(templates map[string]string, opts *Options) (*ScriggoEngine, error) {
	if opts == nil {
		opts = &Options{}
	}
	entryPoints := opts.EntryPoints
	if entryPoints == nil {
		entryPoints = make([]string, 0, len(templates))
		for name := range templates {
			entryPoints = append(entryPoints, name)
		}
	}
	return newScriggoEngine(templates, entryPoints, opts.Filters, opts.Functions, opts.PostProcessors, opts.Declarations, opts.Profiling)
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
		rawTemplates:      make(map[string]string, len(templates)),
		compiledTemplates: make(map[string]*scriggo.Template, len(entryPoints)),
		postProcessors:    make(map[string][]PostProcessor),
		tracing: &scriggoTracingConfig{
			enabled: false,
			traces:  nil,
		},
		profilingEnabled: enableProfiling,
	}

	// Build globals (filters become functions in Scriggo)
	engine.globals = buildScriggoGlobals(customFilters, customFunctions, additionalDeclarations)

	// Override sort_by with a debug-aware variant: when EnableFilterDebug()
	// (the `validate --debug-filters` flag) is active, it logs each comparison.
	// The flag is read dynamically so a post-construction toggle (the testrunner
	// builds worker engines and enables debug afterwards) is honored.
	//
	// This must stay an AdaptiveFunc to match the declaration
	// registerScriggoCustomFunctions installs — a plain func here would shadow
	// it and silently drop both the comparator call shape and type preservation.
	engine.globals[FilterSortBy] = sortByAdaptive(engine.IsFilterDebugEnabled)

	// Store raw templates (all templates, not just entry points)
	maps.Copy(engine.rawTemplates, templates)

	// Compile only entry points (snippets compiled on-demand by Scriggo)
	if err := engine.compileTemplates(templates, entryPoints); err != nil {
		return nil, err
	}

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

// SourceSpan / SourceFrame attribute a contiguous run of rendered output to the
// template include stack that produced it (see RenderWithSourceMap). They are
// aliases for the engine-level types so callers need not import the Scriggo package.
type SourceSpan = scriggo.SourceSpan
type SourceFrame = scriggo.SourceFrame

// RenderWithSourceMap renders a template like Render, but additionally returns a
// source map: one span per contiguous run of output attributing it to the
// template source (path + line) that produced it. The returned string is the
// RAW render output BEFORE post-processors run, so the span Length fields sum to
// its size exactly; callers that display the post-processed output must align
// the two (post-processors are whitespace-only for the bundled chart). Output
// from a parallel "{{ go … }}" render is attributed to the go-render call site.
func (e *ScriggoEngine) RenderWithSourceMap(ctx context.Context, templateName string, templateContext map[string]any) (raw string, spans []SourceSpan, err error) {
	template, exists := e.compiledTemplates[templateName]
	if !exists {
		return "", nil, e.templateNotFoundError(templateName)
	}
	if templateContext == nil {
		templateContext = make(map[string]any)
	}
	if _, ok := templateContext["shared"]; !ok {
		templateContext["shared"] = NewSharedContext()
	}
	ctx = context.WithValue(ctx, RenderContextContextKey, templateContext)

	runOpts := &scriggo.RunOptions{Context: ctx, CollectSourceMap: true}
	var output strings.Builder
	if err := template.Run(&output, templateContext, runOpts); err != nil {
		if ctx.Err() != nil {
			return "", nil, &RenderTimeoutError{TemplateName: templateName, Cause: ctx.Err()}
		}
		return "", nil, NewRenderError(templateName, err)
	}
	return output.String(), runOpts.SourceSpans, nil
}

// Render executes a template with the given context and returns the output.
func (e *ScriggoEngine) Render(ctx context.Context, templateName string, templateContext map[string]any) (string, error) {
	template, exists := e.compiledTemplates[templateName]
	if !exists {
		return "", e.templateNotFoundError(templateName)
	}

	// Ensure template context exists with shared context for cross-template caching.
	// This allows first_seen and other cache functions to work even when caller
	// passes nil context (e.g., in tests).
	if templateContext == nil {
		templateContext = make(map[string]any)
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
// When profiling is enabled (via Options.Profiling), this method returns
// aggregated include timing statistics. When profiling is disabled, returns nil
// for the stats slice.
func (e *ScriggoEngine) RenderWithProfiling(ctx context.Context, templateName string, templateContext map[string]any) (string, []IncludeStats, error) {
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
	slices.Sort(available)
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

// TemplateNames returns the names of all available templates, sorted alphabetically.
func (e *ScriggoEngine) TemplateNames() []string {
	names := make([]string, 0, len(e.compiledTemplates))
	for name := range e.compiledTemplates {
		names = append(names, name)
	}
	slices.Sort(names)
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

// buildScriggoPostProcessors builds post-processors for the Scriggo engine.
//
// The template post-processor type is handled here (not in NewPostProcessor) because
// it requires access to engine.globals for Scriggo compilation.
func buildScriggoPostProcessors(engine *ScriggoEngine, configs map[string][]PostProcessorConfig) error {
	for templateName, procConfigs := range configs {
		processors := make([]PostProcessor, 0, len(procConfigs))
		for _, cfg := range procConfigs {
			var processor PostProcessor
			var err error
			if cfg.Type == PostProcessorTypeTemplate {
				source := cfg.Params["source"]
				processor, err = NewTemplatePostProcessor(source, engine.globals)
			} else {
				processor, err = NewPostProcessor(cfg)
			}
			if err != nil {
				return err
			}
			processors = append(processors, processor)
		}
		engine.postProcessors[templateName] = processors
	}
	return nil
}
