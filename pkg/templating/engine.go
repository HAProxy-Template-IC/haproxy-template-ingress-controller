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
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nikolalohinski/gonja/v2/builtins"
	"github.com/nikolalohinski/gonja/v2/config"
	"github.com/nikolalohinski/gonja/v2/exec"
	"github.com/nikolalohinski/gonja/v2/loaders"
	"github.com/nikolalohinski/gonja/v2/nodes"
	"github.com/nikolalohinski/gonja/v2/parser"
	"github.com/nikolalohinski/gonja/v2/tokens"
	"github.com/pkg/errors"
)

// FilterFunc is a custom filter function that can be registered with the template engine.
// It receives the input value and optional arguments, and returns the filtered value or an error.
//
// Example:
//
//	func uppercase(in interface{}, args ...interface{}) (interface{}, error) {
//	    str, ok := in.(string)
//	    if !ok {
//	        return nil, fmt.Errorf("uppercase: expected string, got %T", in)
//	    }
//	    return strings.ToUpper(str), nil
//	}
type FilterFunc func(in interface{}, args ...interface{}) (interface{}, error)

// GlobalFunc is a custom global function that can be called from templates.
// It receives variadic arguments and returns a result or an error.
//
// Example:
//
//	func fail(args ...interface{}) (interface{}, error) {
//	    if len(args) != 1 {
//	        return nil, fmt.Errorf("fail() requires exactly one string argument")
//	    }
//	    message, ok := args[0].(string)
//	    if !ok {
//	        return nil, fmt.Errorf("fail() argument must be a string")
//	    }
//	    return nil, fmt.Errorf(message)
//	}
type GlobalFunc func(args ...interface{}) (interface{}, error)

// TemplateEngine provides template compilation and rendering capabilities.
// It pre-compiles all templates at initialization for optimal runtime performance
// and early detection of syntax errors.
type TemplateEngine struct {
	// engineType is the template engine used for rendering
	engineType EngineType

	// rawTemplates stores the original template strings by name
	rawTemplates map[string]string

	// compiledTemplates stores pre-compiled templates by name
	compiledTemplates map[string]*exec.Template

	// postProcessors stores post-processors by template name
	// Each template can have a chain of post-processors applied after rendering
	postProcessors map[string][]PostProcessor

	// tracing controls template execution tracing
	tracing *tracingConfig
}

// tracingConfig holds template tracing configuration.
// Per-render trace state (depth, builder) is stored in the execution context to ensure thread-safety.
type tracingConfig struct {
	enabled      bool
	debugFilters bool // Enable detailed filter operation logging
	mu           sync.Mutex
	traces       []string // Accumulated trace outputs from all renders
}

// Template tag keyword constants.
const (
	keywordWith = "with"
)

// recordFilter records a filter operation if tracing is enabled.
// This is called by filters to add entries to the trace output.
// ctx should be the execution context from exec.Evaluator.Environment.Context.
func (tc *tracingConfig) recordFilter(ctx *exec.Context, filterName, inputType string, inputSize int, params []string) {
	if ctx == nil {
		return
	}

	// Check if tracing is enabled for this render (stored in context)
	if enabled, ok := ctx.Get("_trace_enabled"); !ok || !enabled.(bool) {
		return
	}

	// Get depth from context (per-render state)
	depth := 0
	if d, ok := ctx.Get("_trace_depth"); ok {
		depth = d.(int)
	}

	// Get builder from context (per-render state)
	var builder *strings.Builder
	if b, ok := ctx.Get("_trace_builder"); ok {
		builder = b.(*strings.Builder)
	}
	if builder == nil {
		return
	}

	// Create indentation based on depth
	indent := strings.Repeat("  ", depth)

	// Format filter operation
	var msg string
	if len(params) > 0 {
		msg = fmt.Sprintf("Filter: %s(%s, %d items) [%s]", filterName, inputType, inputSize, strings.Join(params, ", "))
	} else {
		msg = fmt.Sprintf("Filter: %s(%s, %d items)", filterName, inputType, inputSize)
	}

	fmt.Fprintf(builder, "%s%s\n", indent, msg)
}

// testInFixed implements a fixed "in" test that compares string values for lists.
//
// This fixes a Gonja limitation where the built-in "in" test uses Go's interface{} equality,
// which compares object identity rather than values. Each template expression with the ~
// concatenation operator creates a NEW *exec.Value object, so even identical string values
// fail equality checks.
//
// For lists/arrays: Iterates and compares using String() method (value comparison).
// For other types: Delegates to Contains() which works correctly for maps and strings.
func testInFixed(ctx *exec.Context, in *exec.Value, params *exec.VarArgs) (bool, error) {
	seq := params.First()

	// Use getResolvedValue() helper to properly dereference pointers
	// This mirrors the logic in Value.Contains()
	resolved := seq.Val
	if resolved.IsValid() && resolved.Kind() == reflect.Ptr {
		resolved = resolved.Elem()
	}

	// For lists/arrays: compare using String() method (value comparison)
	if resolved.Kind() == reflect.Slice || resolved.Kind() == reflect.Array {
		inStr := in.String()
		for i := 0; i < resolved.Len(); i++ {
			// Get item from list and wrap in Value
			item := exec.ToValue(resolved.Index(i))
			itemStr := item.String()
			if inStr == itemStr {
				return true, nil
			}
		}
		return false, nil
	}

	// For other types (maps, strings): delegate to Contains() which works correctly
	return seq.Contains(in), nil
}

// New creates a new TemplateEngine with the specified engine type, templates,
// custom filters, and custom global functions.
//
// All templates are compiled during initialization. Returns an error if any
// template fails to compile or if the engine type is not supported.
//
// Custom filters and functions are optional - pass nil if not needed.
//
// The engine automatically includes a fixed "in" test that compares string values
// instead of object identity for list membership checks, solving a Gonja limitation.
//
// Example with custom filters and functions:
//
//	filters := map[string]templating.FilterFunc{
//	    "uppercase": func(in interface{}, args ...interface{}) (interface{}, error) {
//	        return strings.ToUpper(in.(string)), nil
//	    },
//	}
//	functions := map[string]templating.GlobalFunc{
//	    "fail": func(args ...interface{}) (interface{}, error) {
//	        return nil, fmt.Errorf("%v", args[0])
//	    },
//	}
//	engine, err := templating.New(templating.EngineTypeGonja, templates, filters, functions)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
// Example without custom filters/functions:
//
//	engine, err := templating.New(templating.EngineTypeGonja, templates, nil, nil, nil)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
// Example with post-processors for indentation normalization:
//
//	postProcessors := map[string][]templating.PostProcessorConfig{
//	    "haproxycfg": {
//	        {
//	            Type: templating.PostProcessorTypeRegexReplace,
//	            Params: map[string]string{
//	                "pattern": "^[ ]+",
//	                "replace": "  ",
//	            },
//	        },
//	    },
//	}
//	engine, err := templating.New(templating.EngineTypeGonja, templates, nil, nil, postProcessors)
func New(engineType EngineType, templates map[string]string, customFilters map[string]FilterFunc, customFunctions map[string]GlobalFunc, postProcessorConfigs map[string][]PostProcessorConfig) (*TemplateEngine, error) {
	// Validate engine type
	if engineType != EngineTypeGonja {
		return nil, NewUnsupportedEngineError(engineType)
	}

	engine := &TemplateEngine{
		engineType:        engineType,
		rawTemplates:      make(map[string]string, len(templates)),
		compiledTemplates: make(map[string]*exec.Template, len(templates)),
		postProcessors:    make(map[string][]PostProcessor),
		tracing: &tracingConfig{
			enabled: false,
			traces:  make([]string, 0),
		},
	}

	// Create template loader and config
	loader := NewSimpleLoader(templates)
	cfg := createGonjaConfig()

	// Build Gonja environment with custom extensions
	// Note: compiledTemplates is passed to enable the cached include parser.
	// At this point the map is empty, but the parser stores a reference to it.
	// Templates will be added to the map during compileTemplates().
	environment := buildEnvironment(customFilters, customFunctions, engine.compiledTemplates)

	// Compile all templates
	if err := compileTemplates(engine, templates, cfg, loader, environment); err != nil {
		return nil, err
	}

	// Build post-processors
	if err := buildPostProcessors(engine, postProcessorConfigs); err != nil {
		return nil, err
	}

	return engine, nil
}

// createGonjaConfig creates the Gonja configuration with whitespace control enabled.
func createGonjaConfig() *config.Config {
	// TrimBlocks removes the first newline after a block (e.g., {% if %})
	// LeftStripBlocks strips leading spaces/tabs before a block
	// Note: LeftStripBlocks also sets RemoveTrailingWhiteSpaceFromLastLine on Data nodes,
	// but this can be overridden using {%+ instead of {% on specific blocks
	return &config.Config{
		BlockStartString:    "{%",
		BlockEndString:      "%}",
		VariableStartString: "{{",
		VariableEndString:   "}}",
		CommentStartString:  "{#",
		CommentEndString:    "#}",
		AutoEscape:          false,
		StrictUndefined:     false,
		TrimBlocks:          true, // Remove newlines after blocks for cleaner output
		LeftStripBlocks:     true, // Strip leading spaces before blocks for proper indentation
	}
}

// buildFilters creates a filter set with builtin, custom, and generic filters.
func buildFilters(customFilters map[string]FilterFunc) *exec.FilterSet {
	// Clone builtin filters to avoid modifying global state when adding custom filters.
	// The builtins.Filters.Update() method modifies the FilterSet in-place, which causes
	// race conditions when multiple engines are created concurrently with different custom filters.
	filters := cloneFilterSet(builtins.Filters)

	// Register custom filters if provided
	if len(customFilters) > 0 {
		filterMap := make(map[string]exec.FilterFunction)
		for name, customFilter := range customFilters {
			filterMap[name] = wrapCustomFilter(customFilter)
		}
		customFilterSet := exec.NewFilterSet(filterMap)
		filters = filters.Update(customFilterSet)
	}

	// Always register generic data manipulation filters
	genericFilterMap := map[string]exec.FilterFunction{
		"sort_by":    sortByFilter,
		"group_by":   groupByFilter,
		"transform":  transformFilter,
		"extract":    extractFilter,
		"glob_match": globMatchFilter,
		"debug":      debugFilter,
		"eval":       evalFilter,
		"strip":      stripFilter,
		"trim":       trimFilter, // Override builtin trim to pass through errors
	}
	genericFilterSet := exec.NewFilterSet(genericFilterMap)
	return filters.Update(genericFilterSet)
}

// buildGlobalFunctions creates a context with builtin, fail, and custom global functions.
func buildGlobalFunctions(customFunctions map[string]GlobalFunc) *exec.Context {
	globalFunctions := builtins.GlobalFunctions

	// Always register the fail() function (used for template validation)
	failFunctionMap := make(map[string]interface{})
	failFunctionMap["fail"] = func(args ...interface{}) (interface{}, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("fail() requires exactly one argument (error message)")
		}
		message, ok := args[0].(string)
		if !ok {
			message = fmt.Sprint(args[0])
		}
		return nil, fmt.Errorf("%s", message)
	}
	failFunctionContext := exec.NewContext(failFunctionMap)
	globalFunctions = globalFunctions.Update(failFunctionContext)

	// Register custom global functions if provided
	if len(customFunctions) > 0 {
		functionMap := make(map[string]interface{})
		for name, customFunc := range customFunctions {
			functionMap[name] = wrapGlobalFunction(customFunc)
		}
		customFunctionContext := exec.NewContext(functionMap)
		globalFunctions = globalFunctions.Update(customFunctionContext)
	}

	return globalFunctions
}

// buildEnvironment creates a Gonja environment with all custom extensions.
//
// The compiledTemplates parameter is used to create a cached include parser that
// looks up templates from the pre-compiled cache instead of re-parsing. This is
// critical for thread-safety when rendering templates concurrently.
func buildEnvironment(customFilters map[string]FilterFunc, customFunctions map[string]GlobalFunc, compiledTemplates map[string]*exec.Template) *exec.Environment {
	filters := buildFilters(customFilters)
	globalFunctions := buildGlobalFunctions(customFunctions)

	// Always override the "in" test with our fixed version and add generic tests
	testMap := map[string]exec.TestFunction{
		"in":           testInFixed,
		"conflicts_by": conflictsByTest,
	}
	customTestSet := exec.NewTestSet(testMap)
	tests := builtins.Tests.Update(customTestSet)

	customMethods := createCustomMethods()

	// Register custom control structures (tags)
	// Note: "include", "import", and "from" override Gonja's builtins to use cached templates
	// for thread-safety. Gonja's builtins call exec.NewTemplate() on every invocation,
	// which re-parses templates and is not thread-safe for concurrent access.
	customControlStructures := map[string]parser.ControlStructureParser{
		"compute_once": computeOnceParser,
		"include":      createCachedIncludeParser(compiledTemplates),
		"import":       createCachedImportParser(compiledTemplates),
		"from":         createCachedFromParser(compiledTemplates),
	}
	// Build fresh control structure set by copying builtins + adding customs.
	// IMPORTANT: Do NOT use builtins.ControlStructures.Update() as it mutates global state.
	// When multiple engines are created (e.g., one per test), the custom parsers with
	// their unique compiledTemplates references would pollute the global state.
	controlStructureMap := map[string]parser.ControlStructureParser{}

	// builtinNames lists Gonja's builtin control structures.
	// NOTE: This list must be kept in sync with Gonja's builtins.ControlStructures.
	// If Gonja adds new control structures, they won't be available until added here.
	// Last verified against github.com/nikolalohinski/gonja/v2 v2.x
	builtinNames := []string{
		"autoescape", "block", "extends", "filter", "for", "from",
		"if", "import", "include", "macro", "raw", "set", "with",
	}
	for _, name := range builtinNames {
		if p, ok := builtins.ControlStructures.Get(name); ok {
			controlStructureMap[name] = p
		}
	}

	// Override with custom parsers (include, import, from use cached templates)
	for name, p := range customControlStructures {
		controlStructureMap[name] = p
	}

	controlStructures := exec.NewControlStructureSet(controlStructureMap)

	return &exec.Environment{
		Filters:           filters,
		Tests:             tests,
		ControlStructures: controlStructures,
		Methods:           customMethods,
		Context:           globalFunctions,
	}
}

// compileTemplates compiles all templates and stores them in the engine.
func compileTemplates(engine *TemplateEngine, templates map[string]string, cfg *config.Config, loader loaders.Loader, environment *exec.Environment) error {
	for name, content := range templates {
		engine.rawTemplates[name] = content

		compiled, err := exec.NewTemplate(name, cfg, loader, environment)
		if err != nil {
			return NewCompilationError(name, content, err)
		}

		engine.compiledTemplates[name] = compiled
	}
	return nil
}

// buildPostProcessors creates post-processors from configuration and stores them in the engine.
func buildPostProcessors(engine *TemplateEngine, postProcessorConfigs map[string][]PostProcessorConfig) error {
	for templateName, configs := range postProcessorConfigs {
		processors := make([]PostProcessor, 0, len(configs))
		for _, config := range configs {
			processor, err := NewPostProcessor(config)
			if err != nil {
				return fmt.Errorf("failed to create post-processor for template %q: %w", templateName, err)
			}
			processors = append(processors, processor)
		}
		engine.postProcessors[templateName] = processors
	}
	return nil
}

// createCustomMethods creates custom method sets for list and dict types with fixed behavior.
func createCustomMethods() exec.Methods {
	return exec.Methods{
		Bool:  builtins.Methods.Bool,
		Str:   builtins.Methods.Str,
		Int:   builtins.Methods.Int,
		Float: builtins.Methods.Float,
		Dict:  createCustomDictMethods(),
		List:  createCustomListMethods(),
	}
}

// createCustomListMethods creates custom list method set with fixed append() that returns modified list.
func createCustomListMethods() *exec.MethodSet[[]interface{}] {
	return exec.NewMethodSet[[]interface{}](map[string]exec.Method[[]interface{}]{
		"append": func(_ []interface{}, selfValue *exec.Value, arguments *exec.VarArgs) (interface{}, error) {
			var x interface{}
			if err := arguments.Take(
				exec.PositionalArgument("x", nil, exec.AnyArgument(&x)),
			); err != nil {
				return nil, exec.ErrInvalidCall(err)
			}

			// Modify list in-place (same as builtin)
			*selfValue = *exec.ToValue(reflect.Append(selfValue.Val, reflect.ValueOf(exec.ToValue(x))))

			// RETURN the modified list instead of nil (our fix)
			return selfValue.Interface(), nil
		},

		// Copy other builtin list methods unchanged from Gonja
		"reverse": func(_ []interface{}, selfValue *exec.Value, arguments *exec.VarArgs) (interface{}, error) {
			if err := arguments.Take(); err != nil {
				return nil, exec.ErrInvalidCall(err)
			}
			reversed := reflect.MakeSlice(selfValue.Val.Type(), 0, 0)
			for i := selfValue.Val.Len() - 1; i >= 0; i-- {
				reversed = reflect.Append(reversed, selfValue.Val.Index(i))
			}
			for i := 0; i < selfValue.Val.Len(); i++ {
				selfValue.Val.Index(i).Set(reversed.Index(i))
			}
			return selfValue.Interface(), nil
		},

		"copy": func(self []interface{}, selfValue *exec.Value, arguments *exec.VarArgs) (interface{}, error) {
			if err := arguments.Take(); err != nil {
				return nil, exec.ErrInvalidCall(err)
			}
			return self, nil
		},
	})
}

// createCustomDictMethods creates custom dict method set with fixed update() that returns modified dict.
func createCustomDictMethods() *exec.MethodSet[map[string]interface{}] {
	return exec.NewMethodSet[map[string]interface{}](map[string]exec.Method[map[string]interface{}]{
		// Copy builtin dict methods unchanged from Gonja
		"keys": func(self map[string]interface{}, selfValue *exec.Value, arguments *exec.VarArgs) (interface{}, error) {
			if err := arguments.Take(); err != nil {
				return nil, exec.ErrInvalidCall(err)
			}
			keys := make([]string, 0)
			for key := range self {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			return keys, nil
		},

		"items": func(self map[string]interface{}, selfValue *exec.Value, arguments *exec.VarArgs) (interface{}, error) {
			if err := arguments.Take(); err != nil {
				return nil, exec.ErrInvalidCall(err)
			}
			items := make([]interface{}, 0)
			for _, item := range self {
				items = append(items, item)
			}
			return items, nil
		},

		// Custom update() method that returns the modified dict
		"update": func(self map[string]interface{}, selfValue *exec.Value, arguments *exec.VarArgs) (interface{}, error) {
			var otherAny interface{}
			if err := arguments.Take(
				exec.PositionalArgument("other", nil, exec.AnyArgument(&otherAny)),
			); err != nil {
				return nil, exec.ErrInvalidCall(err)
			}

			// Handle both map[string]interface{} and *exec.Dict
			var pairs map[string]interface{}

			switch v := otherAny.(type) {
			case map[string]interface{}:
				pairs = v
			case *exec.Dict:
				// Convert *exec.Dict to map[string]interface{}
				pairs = make(map[string]interface{})
				for _, pair := range v.Pairs {
					pairs[pair.Key.String()] = pair.Value.Interface()
				}
			default:
				return nil, fmt.Errorf("update() expects a dict, got %T", otherAny)
			}

			// Update dict in-place
			for k, v := range pairs {
				self[k] = v
			}

			// RETURN the modified dict instead of nil (our fix)
			return self, nil
		},
	})
}

// Render executes the named template with the provided context and returns the
// rendered output. Returns an error if the template does not exist or if
// rendering fails.
//
// Example:
//
//	context := map[string]interface{}{
//	    "name": "World",
//	}
//	output, err := engine.Render("greeting", context)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	fmt.Println(output) // Output: Hello World!
func (e *TemplateEngine) Render(templateName string, context map[string]interface{}) (string, error) {
	renderStart := time.Now()

	// Look up the compiled template
	template, exists := e.compiledTemplates[templateName]
	if !exists {
		return "", e.templateNotFoundError(templateName)
	}

	// Create execution context - MUST copy the input map!
	// Gonja's NewContext() shares the input map directly, and Context.Set()
	// writes to that shared map. When multiple goroutines render in parallel
	// with the same context map, each creates a Context with its own mutex,
	// but they all write to the SAME underlying map, causing "concurrent map writes".
	// Solution: create a shallow copy so each render has its own map.
	contextCopy := make(map[string]interface{}, len(context))
	for k, v := range context {
		contextCopy[k] = v
	}

	ctx := exec.NewContext(contextCopy)

	// Setup tracing if enabled
	cleanup := e.setupTracing(ctx, templateName)
	if cleanup != nil {
		defer cleanup()
	}

	// Execute the template with the provided context
	executeStart := time.Now()
	output, err := template.ExecuteToString(ctx)
	executeMs := time.Since(executeStart).Milliseconds()
	if err != nil {
		return "", NewRenderError(templateName, err)
	}

	// Apply post-processors if configured for this template
	postProcessStart := time.Now()
	output, err = e.applyPostProcessors(templateName, output)
	postProcessMs := time.Since(postProcessStart).Milliseconds()
	if err != nil {
		return "", err
	}

	totalMs := time.Since(renderStart).Milliseconds()

	// Log timing for slow renders (>50ms) or the main haproxy.cfg template
	if totalMs > 50 || templateName == "haproxy.cfg" {
		slog.Debug("template render timing",
			"template", templateName,
			"total_ms", totalMs,
			"execute_ms", executeMs,
			"postprocess_ms", postProcessMs,
			"output_bytes", len(output),
		)
	}

	return output, nil
}

// templateNotFoundError creates a TemplateNotFoundError with available template names.
func (e *TemplateEngine) templateNotFoundError(templateName string) error {
	availableNames := make([]string, 0, len(e.compiledTemplates))
	for name := range e.compiledTemplates {
		availableNames = append(availableNames, name)
	}
	return NewTemplateNotFoundError(templateName, availableNames)
}

// setupTracing initializes tracing for a template render if tracing is enabled.
// Returns a cleanup function that must be called via defer, or nil if tracing is disabled.
func (e *TemplateEngine) setupTracing(ctx *exec.Context, templateName string) func() {
	// Check if tracing is enabled (thread-safe snapshot)
	e.tracing.mu.Lock()
	tracingEnabled := e.tracing.enabled
	e.tracing.mu.Unlock()

	// Store tracing config reference in context for filters to access
	ctx.Set("_tracing_config", e.tracing)

	if !tracingEnabled {
		return nil
	}

	// Initialize per-render trace state
	traceBuilder := &strings.Builder{}
	ctx.Set("_trace_depth", 0)
	ctx.Set("_trace_builder", traceBuilder)
	ctx.Set("_trace_enabled", true)

	// Log render start
	e.tracef(ctx, "Rendering: %s", templateName)
	if depth, ok := ctx.Get("_trace_depth"); ok {
		ctx.Set("_trace_depth", depth.(int)+1)
	}

	startTime := time.Now()

	// Return cleanup function
	return func() {
		duration := time.Since(startTime)
		// Decrement depth
		if depth, ok := ctx.Get("_trace_depth"); ok {
			ctx.Set("_trace_depth", depth.(int)-1)
		}
		e.tracef(ctx, "Completed: %s (%.3fms)", templateName, float64(duration.Microseconds())/1000.0)

		// Store completed trace
		if traceBuilder.Len() > 0 {
			e.tracing.mu.Lock()
			e.tracing.traces = append(e.tracing.traces, traceBuilder.String())
			e.tracing.mu.Unlock()
		}
	}
}

// applyPostProcessors applies configured post-processors to the template output.
func (e *TemplateEngine) applyPostProcessors(templateName, output string) (string, error) {
	processors, exists := e.postProcessors[templateName]
	if !exists {
		return output, nil
	}

	var err error
	for _, processor := range processors {
		output, err = processor.Process(output)
		if err != nil {
			return "", fmt.Errorf("post-processor failed for template %q: %w", templateName, err)
		}
	}

	return output, nil
}

// EngineType returns the template engine type used by this instance.
func (e *TemplateEngine) EngineType() EngineType {
	return e.engineType
}

// TemplateNames returns a list of all available template names.
func (e *TemplateEngine) TemplateNames() []string {
	names := make([]string, 0, len(e.rawTemplates))
	for name := range e.rawTemplates {
		names = append(names, name)
	}
	return names
}

// HasTemplate returns true if a template with the given name exists.
func (e *TemplateEngine) HasTemplate(templateName string) bool {
	_, exists := e.compiledTemplates[templateName]
	return exists
}

// GetRawTemplate returns the original (uncompiled) template string for the given name.
// Returns an error if the template does not exist.
func (e *TemplateEngine) GetRawTemplate(templateName string) (string, error) {
	template, exists := e.rawTemplates[templateName]
	if !exists {
		availableNames := make([]string, 0, len(e.rawTemplates))
		for name := range e.rawTemplates {
			availableNames = append(availableNames, name)
		}
		return "", NewTemplateNotFoundError(templateName, availableNames)
	}
	return template, nil
}

// TemplateCount returns the number of templates in this engine.
func (e *TemplateEngine) TemplateCount() int {
	return len(e.compiledTemplates)
}

// String returns a string representation of the engine for debugging.
func (e *TemplateEngine) String() string {
	return fmt.Sprintf("TemplateEngine{type=%s, templates=%d}", e.engineType, e.TemplateCount())
}

// Generic template functions for advanced data manipulation

// sortByFilter implements multi-field sorting with JSONPath-like expressions.
// Supports special operators:
// - ":desc" for descending order
// - ":exists" for boolean existence check
// - "| length" for array/string length
// Usage: items | sort_by(["$.path.type", "$.path.value | length:desc"]).
func sortByFilter(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	// Extract items array
	items := in.Interface()
	itemsSlice, ok := convertToSlice(items)
	if !ok {
		return exec.AsValue(fmt.Errorf("sort_by: expected array/slice, got %T", items))
	}
	if len(itemsSlice) == 0 {
		return in
	}

	// Extract criteria from params
	var criteria []string
	criteriaArg := params.First()
	if criteriaArg == nil {
		return exec.AsValue(fmt.Errorf("sort_by: missing criteria argument"))
	}

	criteriaSlice, ok := convertToSlice(criteriaArg.Interface())
	if !ok {
		return exec.AsValue(fmt.Errorf("sort_by: criteria must be an array of strings"))
	}

	for _, c := range criteriaSlice {
		switch v := c.(type) {
		case string:
			criteria = append(criteria, v)
		case *exec.Value:
			// Handle *exec.Value wrapped strings (from template literals)
			criteria = append(criteria, v.String())
		default:
			return exec.AsValue(fmt.Errorf("sort_by: criteria must be strings, got %T", c))
		}
	}

	// Get tracing config from evaluator's environment context (if available)
	var tracingCfg *tracingConfig
	if e.Environment != nil && e.Environment.Context != nil {
		if cfg, ok := e.Environment.Context.Get("_tracing_config"); ok {
			if tc, ok := cfg.(*tracingConfig); ok {
				tracingCfg = tc

				// Record filter operation in trace
				tc.recordFilter(e.Environment.Context, "sort_by", fmt.Sprintf("%T", items), len(itemsSlice), criteria)
			}
		}
	}

	// Create a sortable wrapper
	sortable := &sortableItems{
		items:    itemsSlice,
		criteria: criteria,
		tracing:  tracingCfg,
	}

	// Sort using the criteria
	sort.Stable(sortable)

	return exec.AsValue(sortable.items)
}

// groupByFilter groups items by evaluated key expression.
// Usage: items | group_by("$.hostname ~ '|' ~ $.path.type ~ '|' ~ $.path.value").
func groupByFilter(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	// Extract items array
	items := in.Interface()
	itemsSlice, ok := convertToSlice(items)
	if !ok {
		return exec.AsValue(fmt.Errorf("group_by: expected array/slice, got %T", items))
	}

	// Extract key expression
	keyExpr := params.First()
	if keyExpr == nil {
		return exec.AsValue(fmt.Errorf("group_by: missing key expression"))
	}
	keyExprStr, ok := keyExpr.Interface().(string)
	if !ok {
		return exec.AsValue(fmt.Errorf("group_by: key expression must be string, got %T", keyExpr.Interface()))
	}

	// Record filter operation in trace if tracing is enabled
	if e.Environment != nil && e.Environment.Context != nil {
		if cfg, ok := e.Environment.Context.Get("_tracing_config"); ok {
			if tc, ok := cfg.(*tracingConfig); ok {
				tc.recordFilter(e.Environment.Context, "group_by", fmt.Sprintf("%T", items), len(itemsSlice), []string{keyExprStr})
			}
		}
	}

	// Group items
	// Use map[string]interface{} for better Gonja compatibility
	groups := make(map[string]interface{})
	for _, item := range itemsSlice {
		key := evaluateExpression(item, keyExprStr)
		keyStr := fmt.Sprint(key)
		if existing, ok := groups[keyStr]; ok {
			// Append to existing group
			if existingSlice, ok := existing.([]interface{}); ok {
				groups[keyStr] = append(existingSlice, item)
			}
		} else {
			// Create new group
			groups[keyStr] = []interface{}{item}
		}
	}

	return exec.AsValue(groups)
}

// transformFilter batch transforms objects with computed fields.
// Usage: items | transform({"path_key": "$.hostname ~ $.path.value", "priority": "$.headers | length"}).
func transformFilter(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	// Extract items array
	items := in.Interface()
	itemsSlice, ok := convertToSlice(items)
	if !ok {
		return exec.AsValue(fmt.Errorf("transform: expected array/slice, got %T", items))
	}

	// Extract transforms map
	transformsArg := params.First()
	if transformsArg == nil {
		return exec.AsValue(fmt.Errorf("transform: missing transforms argument"))
	}

	transforms, ok := convertToMap(transformsArg.Interface())
	if !ok {
		return exec.AsValue(fmt.Errorf("transform: transforms must be a map/dict"))
	}

	// Record filter operation in trace if tracing is enabled
	transformKeys := make([]string, 0, len(transforms))
	for key := range transforms {
		transformKeys = append(transformKeys, key)
	}
	if e.Environment != nil && e.Environment.Context != nil {
		if cfg, ok := e.Environment.Context.Get("_tracing_config"); ok {
			if tc, ok := cfg.(*tracingConfig); ok {
				tc.recordFilter(e.Environment.Context, "transform", fmt.Sprintf("%T", items), len(itemsSlice), transformKeys)
			}
		}
	}

	// Transform each item
	result := make([]interface{}, len(itemsSlice))
	for i, item := range itemsSlice {
		// Deep copy the item
		newItem := deepCopyValue(item)

		// Ensure it's a map
		itemMap, ok := convertToMap(newItem)
		if !ok {
			itemMap = make(map[string]interface{})
			itemMap["_original"] = newItem
		}

		// Apply transforms
		for fieldName, expression := range transforms {
			exprStr, ok := expression.(string)
			if !ok {
				return exec.AsValue(fmt.Errorf("transform: expression must be string, got %T", expression))
			}
			value := evaluateExpression(item, exprStr)
			itemMap[fieldName] = value
		}

		result[i] = itemMap
	}

	return exec.AsValue(result)
}

// extractFilter extracts values using JSONPath-like expressions.
// Usage: routes | extract("$.rules[*].matches[*].method").
func extractFilter(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	// Get the path expression
	pathArg := params.First()
	if pathArg == nil {
		return exec.AsValue(fmt.Errorf("extract: missing path argument"))
	}
	pathStr, ok := pathArg.Interface().(string)
	if !ok {
		return exec.AsValue(fmt.Errorf("extract: path must be string, got %T", pathArg.Interface()))
	}

	items := in.Interface()
	recordExtractFilterTrace(e, items, pathStr)

	// Handle single item vs array
	if itemsSlice, ok := convertToSlice(items); ok {
		return exec.AsValue(extractFromSlice(itemsSlice, pathStr))
	}

	// Single item
	return exec.AsValue(evaluateExpression(items, pathStr))
}

// recordExtractFilterTrace records extract filter operation if tracing is enabled.
func recordExtractFilterTrace(e *exec.Evaluator, items interface{}, pathStr string) {
	if e.Environment == nil || e.Environment.Context == nil {
		return
	}

	cfg, ok := e.Environment.Context.Get("_tracing_config")
	if !ok {
		return
	}

	tc, ok := cfg.(*tracingConfig)
	if !ok {
		return
	}

	itemCount := 1
	if itemsSlice, ok := convertToSlice(items); ok {
		itemCount = len(itemsSlice)
	}
	tc.recordFilter(e.Environment.Context, "extract", fmt.Sprintf("%T", items), itemCount, []string{pathStr})
}

// extractFromSlice extracts values from a slice using a JSONPath expression.
func extractFromSlice(items []interface{}, pathStr string) []interface{} {
	result := []interface{}{}
	for _, item := range items {
		extracted := evaluateExpression(item, pathStr)
		if extracted == nil {
			continue
		}

		// If extracted is also a slice, flatten it
		if extractedSlice, ok := convertToSlice(extracted); ok {
			result = append(result, extractedSlice...)
		} else {
			result = append(result, extracted)
		}
	}
	return result
}

// globMatchFilter filters a list of strings by glob pattern.
// Usage: template_snippets | glob_match("map-entry-*").
func globMatchFilter(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	// Get the glob pattern
	patternArg := params.First()
	if patternArg == nil {
		return exec.AsValue(fmt.Errorf("glob_match: missing pattern argument"))
	}
	pattern, ok := patternArg.Interface().(string)
	if !ok {
		return exec.AsValue(fmt.Errorf("glob_match: pattern must be string, got %T", patternArg.Interface()))
	}

	// Get input list
	items := in.Interface()
	itemsSlice, ok := convertToSlice(items)
	if !ok {
		return exec.AsValue(fmt.Errorf("glob_match: expected array/slice, got %T", items))
	}

	// Filter items matching glob pattern
	var result []interface{}
	for _, item := range itemsSlice {
		itemStr := fmt.Sprint(item)
		matched, err := filepath.Match(pattern, itemStr)
		if err != nil {
			// Invalid pattern
			return exec.AsValue(fmt.Errorf("glob_match: invalid pattern %q: %w", pattern, err))
		}
		if matched {
			result = append(result, item)
		}
	}

	return exec.AsValue(result)
}

// debugFilter outputs the structure of a variable as formatted JSON comments.
// Useful for debugging template data during development.
// Usage: {{ routes | debug }} or {{ routes | debug("label") }}.
func debugFilter(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	// Get optional label
	label := ""
	if params != nil && len(params.Args) > 0 {
		if labelArg := params.First(); labelArg != nil {
			label = labelArg.String()
		}
	}

	// Marshal to JSON with indentation
	data, err := json.MarshalIndent(in.Interface(), "# ", "  ")
	if err != nil {
		// Fallback to simple string representation
		data = []byte(fmt.Sprintf("%v", in.Interface()))
	}

	// Format as HAProxy comments
	var output string
	if label != "" {
		output = fmt.Sprintf("# DEBUG %s:\n# %s\n", label, string(data))
	} else {
		output = fmt.Sprintf("# DEBUG:\n# %s\n", string(data))
	}

	return exec.AsValue(output)
}

// evalFilter evaluates a JSONPath expression and returns the result with type information.
// Useful for testing sort_by criteria and debugging data extraction.
// Usage: {{ route | eval("$.match.method:exists:desc") }}.
func evalFilter(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	// Get expression
	if params == nil || len(params.Args) == 0 {
		return exec.AsValue(fmt.Errorf("eval: missing expression argument"))
	}

	exprArg := params.First()
	if exprArg == nil {
		return exec.AsValue(fmt.Errorf("eval: expression cannot be nil"))
	}

	expr := exprArg.String()

	// Evaluate expression
	result := evaluateExpression(in.Interface(), expr)

	// Return result with type info for debugging
	return exec.AsValue(fmt.Sprintf("%v (%T)", result, result))
}

// stripFilter removes leading and trailing whitespace from a string.
// Usage: {{ value | strip }}.
func stripFilter(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	// Convert input to string
	str := in.String()

	// Strip whitespace
	stripped := strings.TrimSpace(str)

	return exec.AsValue(stripped)
}

// trimFilter is a custom trim filter that passes through errors instead of masking them.
// This is critical for proper error reporting when templates fail inside include_matching().
//
// The builtin Gonja trim filter wraps errors with "Wrong signature for 'trim'", which
// hides the actual error (e.g., from fail() function). Our version checks if the input
// is an error and returns it unchanged, allowing the real error to propagate.
//
// Usage: {{ include_matching("pattern-*") | trim }}.
//
// Supports optional 'chars' parameter like Gonja's trim:
// {{ "  hello  " | trim }}  → "hello".
// {{ "xxhelloxx" | trim(chars="x") }}  → "hello".
func trimFilter(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
	// Pass through errors unchanged (don't mask them)
	if in.IsError() {
		return in
	}

	// Extract 'chars' parameter (defaults to whitespace)
	charsParam := exec.KwArg{
		Name:    "chars",
		Default: " \t\n\r",
	}
	p := params.ExpectKwArgs([]*exec.KwArg{&charsParam})
	if p.IsError() {
		return exec.AsValue(errors.Wrap(p, "Wrong signature for 'trim'"))
	}

	// Convert input to string
	str := in.String()

	// Get chars to trim
	chars := p.GetKeywordArgument(charsParam.Name, charsParam.Default).String()

	// Perform trim operation
	trimmed := strings.Trim(str, chars)

	return exec.AsValue(trimmed)
}

// conflictsByTest detects conflicts when grouped by key.
// Usage: routes | conflicts_by("$.path", "$.method ~ $.headers").
func conflictsByTest(ctx *exec.Context, in *exec.Value, params *exec.VarArgs) (bool, error) {
	// Extract items array
	items := in.Interface()
	itemsSlice, ok := convertToSlice(items)
	if !ok {
		return false, fmt.Errorf("conflicts_by: expected array/slice, got %T", items)
	}

	// Extract group expression and compare expression
	args := params.Args
	if len(args) < 2 {
		return false, fmt.Errorf("conflicts_by: requires 2 arguments, got %d", len(args))
	}

	// Get group expression (first arg)
	groupExpr := args[0].String()

	// Get compare expression (second arg)
	compareExpr := args[1].String()

	// Group items
	groups := make(map[string][]interface{})
	for _, item := range itemsSlice {
		key := evaluateExpression(item, groupExpr)
		keyStr := fmt.Sprint(key)
		groups[keyStr] = append(groups[keyStr], item)
	}

	// Check for conflicts within each group
	for _, group := range groups {
		if len(group) <= 1 {
			continue
		}

		// Check if items in group have different comparison values
		firstValue := evaluateExpression(group[0], compareExpr)
		firstStr := fmt.Sprint(firstValue)

		for _, item := range group[1:] {
			value := evaluateExpression(item, compareExpr)
			valueStr := fmt.Sprint(value)
			if firstStr != valueStr {
				return true, nil // Conflict found
			}
		}
	}

	return false, nil
}

// Helper types and functions for the generic functions

type sortableItems struct {
	items    []interface{}
	criteria []string
	tracing  *tracingConfig // For filter debug logging
}

func (s *sortableItems) Len() int {
	return len(s.items)
}

func (s *sortableItems) Less(i, j int) bool {
	for _, criterion := range s.criteria {
		cmp := compareByExpressionWithDebug(s.items[i], s.items[j], criterion, s.tracing)
		if cmp != 0 {
			return cmp < 0
		}
	}
	return false
}

func (s *sortableItems) Swap(i, j int) {
	s.items[i], s.items[j] = s.items[j], s.items[i]
}

// evaluateExpression evaluates a JSONPath-like expression against an item.
// Supports:
// - Basic paths: "$.field.subfield"
// - Array access: "$.items[0]" or "$.items[*]"
// - Length operator: "$.items | length"
// - Concatenation: "$.field1 ~ '-' ~ $.field2"
// - Existence check: "$.field:exists".
func evaluateExpression(item interface{}, expr string) interface{} {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil
	}

	// Don't unwrap *exec.Value here - let navigateJSONPath handle it
	// using Gonja's GetItem/GetAttribute methods for proper field access

	// Handle concatenation operator ~
	if strings.Contains(expr, " ~ ") {
		return handleConcatenation(item, expr)
	}

	// Handle pipe operator |
	if strings.Contains(expr, " | ") {
		return handlePipeOperator(item, expr)
	}

	// Handle existence check
	if strings.HasSuffix(expr, ":exists") {
		return handleExistenceCheck(item, expr)
	}

	// Handle JSONPath-like navigation
	if strings.HasPrefix(expr, "$.") || expr == "$" {
		return navigateJSONPath(item, expr)
	}

	// Treat as field name without $. prefix
	return navigateJSONPath(item, "$."+expr)
}

// handleConcatenation processes the ~ concatenation operator.
func handleConcatenation(item interface{}, expr string) interface{} {
	parts := strings.Split(expr, " ~ ")
	var result strings.Builder

	for _, part := range parts {
		part = strings.TrimSpace(part)

		// Check if it's a literal string
		if (strings.HasPrefix(part, "'") && strings.HasSuffix(part, "'")) ||
			(strings.HasPrefix(part, "\"") && strings.HasSuffix(part, "\"")) {
			// Remove quotes and append literal
			result.WriteString(strings.Trim(part, "'\""))
		} else {
			// Evaluate as expression
			value := evaluateExpression(item, part)
			if value != nil {
				result.WriteString(fmt.Sprint(value))
			}
		}
	}

	return result.String()
}

// handlePipeOperator processes the | pipe operator for filters.
func handlePipeOperator(item interface{}, expr string) interface{} {
	parts := strings.Split(expr, " | ")
	value := evaluateExpression(item, strings.TrimSpace(parts[0]))

	for i := 1; i < len(parts); i++ {
		value = applyPipeOperation(value, strings.TrimSpace(parts[i]))
	}

	return value
}

// applyPipeOperation applies a single pipe operation to a value.
func applyPipeOperation(value interface{}, operation string) interface{} {
	// Handle length operator
	if operation == "length" {
		return calculateLength(value)
	}

	// Handle default operator
	if strings.HasPrefix(operation, "default(") && strings.HasSuffix(operation, ")") {
		if value == nil || value == "" {
			defaultValue := strings.TrimSuffix(strings.TrimPrefix(operation, "default("), ")")
			return strings.Trim(defaultValue, "'\"")
		}
	}

	return value
}

// calculateLength returns the length of a value.
func calculateLength(value interface{}) int {
	switch v := value.(type) {
	case string:
		return len(v)
	default:
		if slice, ok := convertToSlice(value); ok {
			return len(slice)
		}
		if m, ok := convertToMap(value); ok {
			return len(m)
		}
		return 0
	}
}

// handleExistenceCheck checks if a value exists (is not nil).
func handleExistenceCheck(item interface{}, expr string) interface{} {
	expr = strings.TrimSuffix(expr, ":exists")
	value := evaluateExpression(item, expr)
	return value != nil
}

// navigateJSONPath navigates through an object using JSONPath-like syntax.
func navigateJSONPath(item interface{}, path string) interface{} {
	// Remove leading $. if present
	if strings.HasPrefix(path, "$.") {
		path = strings.TrimPrefix(path, "$.")
	} else if path == "$" {
		return item
	}

	if path == "" {
		return item
	}

	// Split path into segments, handling array indices
	segments := parsePathSegments(path)
	current := item

	for i, segment := range segments {
		if current == nil {
			return nil
		}

		// Handle array index access
		if strings.HasPrefix(segment, "[") && strings.HasSuffix(segment, "]") {
			result, done := processArrayIndex(current, segment, segments, i)
			if done {
				return result
			}
			current = result
		} else {
			// Regular field access
			current = processFieldAccess(current, segment)
		}
	}

	// Unwrap final result if it's a Gonja Value
	if execVal, ok := current.(*exec.Value); ok {
		return execVal.Interface()
	}

	return current
}

// processArrayIndex handles array index access including wildcards and numeric indices.
func processArrayIndex(current interface{}, segment string, segments []string, segmentIndex int) (interface{}, bool) {
	indexStr := strings.TrimPrefix(strings.TrimSuffix(segment, "]"), "[")

	// Handle wildcard [*]
	if indexStr == "*" {
		return processWildcardIndex(current, segments, segmentIndex)
	}

	// Handle numeric index
	return processNumericIndex(current, indexStr)
}

// processWildcardIndex handles wildcard array access [*].
func processWildcardIndex(current interface{}, segments []string, segmentIndex int) (interface{}, bool) {
	slice, ok := convertToSlice(current)
	if !ok {
		return nil, true
	}

	// If there are more segments, collect results from all elements
	if segmentIndex < len(segments)-1 {
		remainingPath := strings.Join(segments[segmentIndex+1:], ".")
		var results []interface{}
		for _, elem := range slice {
			result := navigateJSONPath(elem, "$."+remainingPath)
			if result != nil {
				if resultSlice, ok := convertToSlice(result); ok {
					results = append(results, resultSlice...)
				} else {
					results = append(results, result)
				}
			}
		}
		return results, true
	}

	// Just return all elements
	return slice, true
}

// processNumericIndex handles numeric array index access.
func processNumericIndex(current interface{}, indexStr string) (interface{}, bool) {
	index, err := strconv.Atoi(indexStr)
	if err != nil {
		return current, false // Not a numeric index, continue
	}

	slice, ok := convertToSlice(current)
	if !ok {
		return nil, true
	}

	if index >= 0 && index < len(slice) {
		return slice[index], false
	}

	return nil, true
}

// processFieldAccess handles regular field access.
func processFieldAccess(current interface{}, segment string) interface{} {
	// If current is a Gonja Value, use its GetItem method for proper access
	if execVal, ok := current.(*exec.Value); ok {
		if val, found := execVal.GetItem(segment); found {
			return val
		}
		// Also try GetAttribute for struct fields
		if val, found := execVal.GetAttribute(segment); found {
			return val
		}
		return nil
	}

	if m, ok := convertToMap(current); ok {
		return m[segment]
	}

	// Try reflection for struct fields
	return getFieldByReflection(current, segment)
}

// parsePathSegments parses a JSONPath into navigable segments.
func parsePathSegments(path string) []string {
	var segments []string
	var current strings.Builder
	inBracket := false

	for i, char := range path {
		switch {
		case char == '[':
			segments, inBracket = processLeftBracket(segments, &current)
		case char == ']':
			segments, inBracket = processRightBracket(segments, &current, inBracket)
		case char == '.' && !inBracket:
			segments = processDot(segments, &current)
		default:
			current.WriteRune(char)
		}

		// Handle last segment
		if i == len(path)-1 && current.Len() > 0 {
			segments = append(segments, current.String())
		}
	}

	return segments
}

// processLeftBracket handles '[' character when parsing path segments.
func processLeftBracket(segments []string, current *strings.Builder) ([]string, bool) {
	if current.Len() > 0 {
		segments = append(segments, current.String())
		current.Reset()
	}
	current.WriteByte('[')
	return segments, true
}

// processRightBracket handles ']' character when parsing path segments.
func processRightBracket(segments []string, current *strings.Builder, inBracket bool) ([]string, bool) {
	current.WriteByte(']')
	if inBracket {
		segments = append(segments, current.String())
		current.Reset()
		return segments, false
	}
	return segments, inBracket
}

// processDot handles '.' character when parsing path segments.
func processDot(segments []string, current *strings.Builder) []string {
	if current.Len() > 0 {
		segments = append(segments, current.String())
		current.Reset()
	}
	return segments
}

// compareByExpressionWithDebug wraps compareByExpression and adds debug logging when enabled.
func compareByExpressionWithDebug(a, b interface{}, criterion string, tracing *tracingConfig) int {
	// Check if debug is enabled
	var debugEnabled bool
	if tracing != nil {
		tracing.mu.Lock()
		debugEnabled = tracing.debugFilters
		tracing.mu.Unlock()
	}

	// If debug not enabled, just call compareByExpression
	if !debugEnabled {
		return compareByExpression(a, b, criterion)
	}

	// Debug enabled - clean expression and evaluate for logging
	// Parse modifiers to get clean expression (same logic as compareByExpression)
	checkExists := false
	checkLength := false

	parts := strings.Split(criterion, ":")
	expr := parts[0]

	for i := 1; i < len(parts); i++ {
		modifier := strings.TrimSpace(parts[i])
		if modifier == "exists" {
			checkExists = true
		}
	}

	// Check for length operator
	if strings.Contains(expr, " | length") {
		checkLength = true
		expr = strings.Split(expr, " | ")[0]
	}

	// Evaluate cleaned expression
	valA := evaluateExpression(a, expr)
	valB := evaluateExpression(b, expr)

	// Apply modifiers for debug display
	if checkExists {
		valA = (valA != nil)
		valB = (valB != nil)
	} else if checkLength {
		valA = getLength(valA)
		valB = getLength(valB)
	}

	// Call comparison
	result := compareByExpression(a, b, criterion)

	// Log the comparison at Debug level (this is a debug feature)
	slog.Debug("SORT comparison",
		"criterion", criterion,
		"valA", valA,
		"valA_type", fmt.Sprintf("%T", valA),
		"valB", valB,
		"valB_type", fmt.Sprintf("%T", valB),
		"result", result)

	return result
}

// compareByExpression compares two items based on an expression with optional modifiers.
func compareByExpression(a, b interface{}, criterion string) int {
	// Parse modifiers
	descending := false
	checkExists := false
	checkLength := false

	parts := strings.Split(criterion, ":")
	expr := parts[0]

	for i := 1; i < len(parts); i++ {
		modifier := strings.TrimSpace(parts[i])
		switch modifier {
		case "desc":
			descending = true
		case "exists":
			checkExists = true
		}
	}

	// Check for length operator
	if strings.Contains(expr, " | length") {
		checkLength = true
		expr = strings.Split(expr, " | ")[0]
	}

	// Evaluate expressions
	valA := evaluateExpression(a, expr)
	valB := evaluateExpression(b, expr)

	// Handle special checks
	if checkExists {
		existsA := valA != nil
		existsB := valB != nil
		result := 0
		if existsA && !existsB {
			result = -1 // A exists, B doesn't → A comes first
		} else if !existsA && existsB {
			result = 1 // B exists, A doesn't → B comes first
		}
		// For :exists modifier, the descending flag means "items with field first"
		// which is already the natural ordering above (exists = -1, not exists = 1).
		// Don't negate - the semantics of :exists:desc are already captured.
		return result
	}

	if checkLength {
		lenA := getLength(valA)
		lenB := getLength(valB)
		result := lenA - lenB
		if descending {
			result = -result
		}
		return result
	}

	// Standard comparison
	result := compareValues(valA, valB)
	if descending {
		result = -result
	}
	return result
}

// compareValues compares two values of potentially different types.
func compareValues(a, b interface{}) int {
	// Handle nil values
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return 1 // nil is considered greater (sorts to end)
	}
	if b == nil {
		return -1
	}

	// Try numeric comparison first
	if numA, okA := toFloat64(a); okA {
		if numB, okB := toFloat64(b); okB {
			if numA < numB {
				return -1
			} else if numA > numB {
				return 1
			}
			return 0
		}
	}

	// Fall back to string comparison
	strA := fmt.Sprint(a)
	strB := fmt.Sprint(b)
	return strings.Compare(strA, strB)
}

// Helper functions

func convertToSlice(v interface{}) ([]interface{}, bool) {
	if v == nil {
		return nil, false
	}

	// Direct slice conversion
	if slice, ok := v.([]interface{}); ok {
		return slice, true
	}

	// Use reflection for other slice types
	val := reflect.ValueOf(v)
	if val.Kind() == reflect.Slice || val.Kind() == reflect.Array {
		result := make([]interface{}, val.Len())
		for i := 0; i < val.Len(); i++ {
			result[i] = val.Index(i).Interface()
		}
		return result, true
	}

	return nil, false
}

func convertToMap(v interface{}) (map[string]interface{}, bool) {
	if v == nil {
		return nil, false
	}

	// Direct map conversion
	if m, ok := v.(map[string]interface{}); ok {
		return m, true
	}

	// Try reflection for other map types
	val := reflect.ValueOf(v)
	if val.Kind() == reflect.Map {
		result := make(map[string]interface{})
		for _, key := range val.MapKeys() {
			keyStr := fmt.Sprint(key.Interface())
			result[keyStr] = val.MapIndex(key).Interface()
		}
		return result, true
	}

	return nil, false
}

func getFieldByReflection(v interface{}, field string) interface{} {
	if v == nil {
		return nil
	}

	val := reflect.ValueOf(v)

	// Dereference pointers
	for val.Kind() == reflect.Ptr {
		if val.IsNil() {
			return nil
		}
		val = val.Elem()
	}

	if val.Kind() != reflect.Struct {
		return nil
	}

	// Try to find field (case-insensitive)
	typ := val.Type()
	for i := 0; i < val.NumField(); i++ {
		fieldType := typ.Field(i)
		if strings.EqualFold(fieldType.Name, field) {
			fieldVal := val.Field(i)
			if fieldVal.CanInterface() {
				return fieldVal.Interface()
			}
		}
	}

	return nil
}

func getLength(v interface{}) int {
	if v == nil {
		return 0
	}

	if s, ok := v.(string); ok {
		return len(s)
	}

	if slice, ok := convertToSlice(v); ok {
		return len(slice)
	}

	if m, ok := convertToMap(v); ok {
		return len(m)
	}

	// Use reflection as fallback
	val := reflect.ValueOf(v)
	if val.Kind() == reflect.Slice || val.Kind() == reflect.Array || val.Kind() == reflect.Map {
		return val.Len()
	}

	return 0
}

func toFloat64(v interface{}) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case float32:
		return float64(val), true
	case int:
		return float64(val), true
	case int8:
		return float64(val), true
	case int16:
		return float64(val), true
	case int32:
		return float64(val), true
	case int64:
		return float64(val), true
	case uint:
		return float64(val), true
	case uint8:
		return float64(val), true
	case uint16:
		return float64(val), true
	case uint32:
		return float64(val), true
	case uint64:
		return float64(val), true
	case string:
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

func deepCopyValue(v interface{}) interface{} {
	if v == nil {
		return nil
	}

	// Use JSON marshal/unmarshal for deep copy
	// This is simple and handles most cases correctly
	data, err := json.Marshal(v)
	if err != nil {
		// Fallback to shallow copy
		return v
	}

	var result interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		// Fallback to shallow copy
		return v
	}

	return result
}

// cloneFilterSet creates a deep copy of a FilterSet to avoid modifying global state.
// This is necessary because gonja's FilterSet.Update() modifies the filter map in-place,
// which causes race conditions when multiple engines are created concurrently.
func cloneFilterSet(original *exec.FilterSet) *exec.FilterSet {
	// Create empty FilterSet and populate it by updating with original.
	// This creates a new instance with copied filters.
	filterMap := make(map[string]exec.FilterFunction)
	cloned := exec.NewFilterSet(filterMap)
	cloned.Update(original)
	return cloned
}

// wrapCustomFilter wraps a FilterFunc into Gonja's FilterFunction signature.
// This adapter converts between our simple FilterFunc interface and Gonja's
// more complex signature that includes the evaluator and typed values.
//
// IMPORTANT: This wrapper passes through errors unchanged to preserve proper
// error messages when templates fail inside include_matching() or other macros.
func wrapCustomFilter(customFilter FilterFunc) exec.FilterFunction {
	return func(e *exec.Evaluator, in *exec.Value, params *exec.VarArgs) *exec.Value {
		// Pass through errors unchanged (don't call the filter on errors)
		// This is critical for proper error propagation from fail() and missing secrets
		if in.IsError() {
			return in
		}

		// Extract the input value as Go interface{}
		inputValue := in.Interface()

		// Extract arguments from VarArgs as interface{} slice
		var args []interface{}
		if params != nil && len(params.Args) > 0 {
			// Get all positional arguments from Args slice
			for _, arg := range params.Args {
				args = append(args, arg.Interface())
			}
		}

		// Call the custom filter
		result, err := customFilter(inputValue, args...)
		if err != nil {
			// Return error wrapped in Value
			return exec.AsValue(err)
		}

		// Return result as Value
		return exec.AsValue(result)
	}
}

// wrapGlobalFunction wraps a GlobalFunc into a function callable from Gonja templates.
// This adapter converts between our simple GlobalFunc interface and the signature
// expected by Gonja's global function system.
func wrapGlobalFunction(customFunc GlobalFunc) func(_ *exec.Evaluator, params *exec.VarArgs) *exec.Value {
	return func(_ *exec.Evaluator, params *exec.VarArgs) *exec.Value {
		// Extract arguments from VarArgs as interface{} slice
		var args []interface{}
		if params != nil && len(params.Args) > 0 {
			for _, arg := range params.Args {
				args = append(args, arg.Interface())
			}
		}

		// Call the custom global function
		result, err := customFunc(args...)
		if err != nil {
			// Return error wrapped in Value using ErrInvalidCall
			// This will cause template rendering to fail with the error message
			return exec.AsValue(exec.ErrInvalidCall(err))
		}

		// Return result as Value
		return exec.AsValue(result)
	}
}

// EnableTracing enables template execution tracing.
// Trace output can be retrieved with GetTraceOutput().
// Tracing is thread-safe - concurrent Render() calls will each produce independent traces.
func (e *TemplateEngine) EnableTracing() {
	e.tracing.mu.Lock()
	e.tracing.enabled = true
	e.tracing.traces = make([]string, 0)
	e.tracing.mu.Unlock()
}

// IsTracingEnabled returns true if template tracing is currently enabled.
func (e *TemplateEngine) IsTracingEnabled() bool {
	e.tracing.mu.Lock()
	defer e.tracing.mu.Unlock()
	return e.tracing.enabled
}

// DisableTracing disables template execution tracing.
func (e *TemplateEngine) DisableTracing() {
	e.tracing.mu.Lock()
	e.tracing.enabled = false
	e.tracing.mu.Unlock()
}

// EnableFilterDebug enables detailed filter operation logging.
// This logs each sort comparison, showing the compared values and results.
// Useful for debugging sort_by behavior and understanding why items are ordered as they are.
func (e *TemplateEngine) EnableFilterDebug() {
	e.tracing.mu.Lock()
	e.tracing.debugFilters = true
	e.tracing.mu.Unlock()
}

// DisableFilterDebug disables detailed filter operation logging.
func (e *TemplateEngine) DisableFilterDebug() {
	e.tracing.mu.Lock()
	e.tracing.debugFilters = false
	e.tracing.mu.Unlock()
}

// GetTraceOutput returns the accumulated trace output from all renders and resets the trace buffer.
// When multiple renders execute concurrently, traces are aggregated in the order they complete.
func (e *TemplateEngine) GetTraceOutput() string {
	e.tracing.mu.Lock()
	defer e.tracing.mu.Unlock()

	if len(e.tracing.traces) == 0 {
		return ""
	}

	output := strings.Join(e.tracing.traces, "")
	e.tracing.traces = make([]string, 0)
	return output
}

// AppendTraces appends traces from another engine to this engine's trace buffer.
// This is useful for aggregating traces from multiple worker engines.
func (e *TemplateEngine) AppendTraces(other *TemplateEngine) {
	if other == nil {
		return
	}

	// Get traces from the other engine (this clears its buffer)
	traces := other.GetTraceOutput()
	if traces == "" {
		return
	}

	// Append to this engine's trace buffer
	e.tracing.mu.Lock()
	e.tracing.traces = append(e.tracing.traces, traces)
	e.tracing.mu.Unlock()
}

// tracef logs a trace message with proper indentation based on nesting depth.
// The depth and builder are read from the execution context for thread-safety.
func (e *TemplateEngine) tracef(ctx *exec.Context, format string, args ...interface{}) {
	// Check if tracing is enabled for this render (stored in context)
	if enabled, ok := ctx.Get("_trace_enabled"); !ok || !enabled.(bool) {
		return
	}

	// Get depth from context (per-render state)
	depth := 0
	if d, ok := ctx.Get("_trace_depth"); ok {
		depth = d.(int)
	}

	// Get builder from context (per-render state)
	var builder *strings.Builder
	if b, ok := ctx.Get("_trace_builder"); ok {
		builder = b.(*strings.Builder)
	}
	if builder == nil {
		return
	}

	// Create indentation based on depth
	indent := strings.Repeat("  ", depth)

	// Format and write the trace message
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(builder, "%s%s\n", indent, msg)
}

// ============================================================================
// Custom Gonja Tag: compute_once
// ============================================================================

// ComputeOnceControlStructure implements a custom Gonja tag that executes expensive
// template computations only once per render, caching the result for subsequent includes.
//
// Syntax - the variable must be created in the parent context BEFORE compute_once:
//
//	{%-  set analysis = namespace(path_groups={}, sorted_routes=[], all_routes=[]) %}
//	{%- compute_once analysis %}
//	  {%- from "analyze_routes" import analyze_routes %}
//	  {{- analyze_routes(analysis, resources) -}}
//	{% endcompute_once %}
//
// The tag checks if 'variable_name' has been modified (has any attribute set).
// - If modified: skips the body (already computed)
// - If not modified: executes the body which modifies the namespace
//
// Why this design: Gonja's {% set %} inside blocks creates local variables that don't
// persist to the parent context. By requiring the namespace to be created BEFORE
// compute_once, we work with Gonja's scoping rules instead of against them.
//
// Use case: Gateway library's analyze_routes macro is called 4 times per render.
// With compute_once, it runs only once and the result is reused across all includes.
type ComputeOnceControlStructure struct {
	location *tokens.Token
	varName  string         // Variable name to check before executing body
	wrapper  *nodes.Wrapper // Template body to execute
}

// Position returns the token position for error reporting.
func (cs *ComputeOnceControlStructure) Position() *tokens.Token {
	return cs.location
}

// String returns a string representation for debugging.
func (cs *ComputeOnceControlStructure) String() string {
	t := cs.Position()
	return fmt.Sprintf("ComputeOnceControlStructure(var=%s, Line=%d Col=%d)",
		cs.varName, t.Line, t.Col)
}

// Execute implements the compute_once logic.
func (cs *ComputeOnceControlStructure) Execute(r *exec.Renderer, tag *nodes.ControlStructureBlock) error {
	// Use a marker variable to track if computation has been done
	// Marker is named "_computed_<varname>" and stored in the context
	markerName := "_computed_" + cs.varName

	// Check if computation already happened
	if r.Environment.Context.Has(markerName) {
		// Marker exists - computation already done, skip body
		return nil
	}

	// Get the variable value - it MUST exist before compute_once
	_, exists := r.Environment.Context.Get(cs.varName)
	if !exists {
		return fmt.Errorf("compute_once: variable '%s' must be created before compute_once block (use {%% set %s = namespace(...) %%} before compute_once)", cs.varName, cs.varName)
	}

	// Variable exists and computation hasn't been done - execute body
	err := r.ExecuteWrapper(cs.wrapper)
	if err != nil {
		return err
	}

	// Mark computation as done
	r.Environment.Context.Set(markerName, true)

	return nil
}

// computeOnceParser parses the compute_once tag syntax.
//
// Expected syntax: {% compute_once variable_name %}.
func computeOnceParser(p, args *parser.Parser) (nodes.ControlStructure, error) {
	cs := &ComputeOnceControlStructure{
		location: p.Current(),
	}

	// Parse variable name
	varToken := args.Match(tokens.Name)
	if varToken == nil {
		return nil, args.Error("compute_once requires variable name", nil)
	}
	cs.varName = varToken.Val

	// Ensure no extra arguments
	if !args.Stream().End() {
		return nil, args.Error("compute_once takes only variable name, no additional arguments", nil)
	}

	// Parse body until {% endcompute_once %}
	wrapper, _, err := p.WrapUntil("endcompute_once")
	if err != nil {
		return nil, err
	}
	cs.wrapper = wrapper

	return cs, nil
}

// ============================================================================
// Custom Gonja Tag: include (cached)
// ============================================================================

// CachedIncludeControlStructure implements a thread-safe include that uses pre-compiled templates.
// Unlike Gonja's builtin include which re-parses templates on every call, this version
// looks up templates from the engine's compiledTemplates cache.
//
// This is critical for thread-safety: Gonja's builtin include calls exec.NewTemplate()
// which re-lexes and re-parses the template. The lexer/parser have internal mutable state
// and are not thread-safe for concurrent access. With GOMAXPROCS > 2, parallel template
// rendering can cause race conditions and crashes.
//
// By using pre-compiled templates, we avoid the re-parsing entirely and only execute
// the pre-compiled AST, which is thread-safe (each render creates a new Renderer).
//
// Note: The `withContext` option is parsed for Jinja2 compatibility but not implemented,
// matching Gonja's behavior where this option is also ignored.
type CachedIncludeControlStructure struct {
	location           *tokens.Token
	filenameExpression nodes.Expression
	ignoreMissing      bool
	withContext        bool // Parsed but not used (matches Gonja's behavior)

	// Reference to the engine's compiled templates (set during environment creation)
	compiledTemplates map[string]*exec.Template
}

// Position returns the token position for error reporting.
func (cs *CachedIncludeControlStructure) Position() *tokens.Token {
	return cs.location
}

// String returns a string representation for debugging.
func (cs *CachedIncludeControlStructure) String() string {
	t := cs.Position()
	return fmt.Sprintf("CachedIncludeControlStructure(Line=%d Col=%d)", t.Line, t.Col)
}

// Execute looks up the pre-compiled template and executes it.
func (cs *CachedIncludeControlStructure) Execute(r *exec.Renderer, tag *nodes.ControlStructureBlock) error {
	// Evaluate filename expression
	filenameValue := r.Eval(cs.filenameExpression)
	if filenameValue.IsError() {
		return errors.Wrap(filenameValue, "unable to evaluate include filename")
	}

	filename := filenameValue.String()

	// Look up from pre-compiled cache (thread-safe read)
	template, exists := cs.compiledTemplates[filename]
	if !exists {
		if cs.ignoreMissing {
			return nil
		}
		return fmt.Errorf("template '%s' not found in cache (available: %d templates)", filename, len(cs.compiledTemplates))
	}

	// Execute with inherited config (creates new renderer - thread-safe)
	return exec.NewRenderer(r.Environment, r.Output, r.Config.Inherit(), r.Loader, template).Execute()
}

// createCachedIncludeParser returns a parser function that creates CachedIncludeControlStructure
// with access to the pre-compiled templates cache.
//
// Parser syntax matches Gonja's include:
//
//	{% include "template.html" %}
//	{% include "template.html" ignore missing %}
//	{% include "template.html" with context %}
//	{% include "template.html" without context %}
func createCachedIncludeParser(compiledTemplates map[string]*exec.Template) parser.ControlStructureParser {
	return func(p *parser.Parser, args *parser.Parser) (nodes.ControlStructure, error) {
		cs := &CachedIncludeControlStructure{
			location:          p.Current(),
			compiledTemplates: compiledTemplates,
		}

		// Parse filename expression (required)
		filenameExpr, err := args.ParseExpression()
		if err != nil {
			return nil, err
		}
		cs.filenameExpression = filenameExpr

		// Parse optional "ignore missing"
		if args.MatchName("ignore") != nil {
			if args.MatchName("missing") != nil {
				cs.ignoreMissing = true
			} else {
				args.Stream().Backup()
			}
		}

		// Parse optional "with/without context" (parsed for compatibility, not used)
		if tok := args.MatchName("with", "without"); tok != nil {
			if args.MatchName("context") != nil {
				cs.withContext = tok.Val == keywordWith
			} else {
				args.Stream().Backup()
			}
		}

		// Ensure no trailing arguments
		if !args.End() {
			return nil, args.Error("malformed include tag arguments", nil)
		}

		return cs, nil
	}
}

// ============================================================================
// Custom Gonja Tag: import (cached)
// ============================================================================

// CachedImportControlStructure implements a thread-safe import that uses pre-compiled templates.
// Unlike Gonja's builtin import which re-parses templates on every call, this version
// looks up templates from the engine's compiledTemplates cache.
//
// Syntax: {% import "template.html" as macros %}
// This makes all macros from template.html available as macros.macro_name().
type CachedImportControlStructure struct {
	location           *tokens.Token
	filenameExpression nodes.Expression
	as                 string
	withContext        bool // Parsed but not used (matches Gonja's behavior)

	compiledTemplates map[string]*exec.Template
}

// Position returns the token position for error reporting.
func (cs *CachedImportControlStructure) Position() *tokens.Token {
	return cs.location
}

// String returns a string representation for debugging.
func (cs *CachedImportControlStructure) String() string {
	t := cs.Position()
	return fmt.Sprintf("CachedImportControlStructure(Line=%d Col=%d)", t.Line, t.Col)
}

// Execute looks up the pre-compiled template and imports all its macros.
func (cs *CachedImportControlStructure) Execute(r *exec.Renderer, tag *nodes.ControlStructureBlock) error {
	// Evaluate filename expression
	filenameValue := r.Eval(cs.filenameExpression)
	if filenameValue.IsError() {
		return errors.Wrap(filenameValue, "unable to evaluate import filename")
	}

	filename := filenameValue.String()

	// Look up from pre-compiled cache (thread-safe read)
	template, exists := cs.compiledTemplates[filename]
	if !exists {
		return fmt.Errorf("template '%s' not found in cache for import (available: %d templates)", filename, len(cs.compiledTemplates))
	}

	// Convert all macros to functions and store as a map
	macros := map[string]exec.Macro{}
	for name, macro := range template.Macros() {
		fn, err := exec.MacroNodeToFunc(macro, r)
		if err != nil {
			return errors.Wrapf(err, "unable to import macro '%s'", name)
		}
		macros[name] = fn
	}

	// Make macros available as namespace: {% import "x" as ns %} -> ns.macro_name()
	r.Environment.Context.Set(cs.as, macros)

	return nil
}

// createCachedImportParser returns a parser function that creates CachedImportControlStructure.
//
// Parser syntax matches Gonja's import:
//
//	{% import "template.html" as macros %}
//	{% import "template.html" as macros with context %}
func createCachedImportParser(compiledTemplates map[string]*exec.Template) parser.ControlStructureParser {
	return func(p *parser.Parser, args *parser.Parser) (nodes.ControlStructure, error) {
		cs := &CachedImportControlStructure{
			location:          p.Current(),
			compiledTemplates: compiledTemplates,
		}

		if args.End() {
			return nil, args.Error("import requires a filename expression", nil)
		}

		// Parse filename expression
		expression, err := args.ParseExpression()
		if err != nil {
			return nil, err
		}
		cs.filenameExpression = expression

		// Parse "as alias" (required)
		if args.MatchName("as") == nil {
			return nil, args.Error("expected 'as' keyword", args.Current())
		}

		alias := args.Match(tokens.Name)
		if alias == nil {
			return nil, args.Error("expected alias name (identifier)", args.Current())
		}
		cs.as = alias.Val

		// Parse optional "with/without context" (parsed for compatibility, not used)
		if tok := args.MatchName("with", "without"); tok != nil {
			if args.MatchName("context") != nil {
				cs.withContext = tok.Val == keywordWith
			} else {
				args.Stream().Backup()
			}
		}

		if !args.End() {
			return nil, args.Error("malformed import tag arguments", nil)
		}

		return cs, nil
	}
}

// ============================================================================
// Custom Gonja Tag: from (cached)
// ============================================================================

// CachedFromImportControlStructure implements a thread-safe from-import that uses pre-compiled templates.
// Unlike Gonja's builtin from which re-parses templates on every call, this version
// looks up templates from the engine's compiledTemplates cache.
//
// Syntax: {% from "template.html" import macro1, macro2 as alias %}
// This makes specific macros available directly in the current scope.
type CachedFromImportControlStructure struct {
	location           *tokens.Token
	filenameExpression nodes.Expression
	withContext        bool              // Parsed but not used (matches Gonja's behavior)
	as                 map[string]string // alias -> original name

	compiledTemplates map[string]*exec.Template
}

// Position returns the token position for error reporting.
func (cs *CachedFromImportControlStructure) Position() *tokens.Token {
	return cs.location
}

// String returns a string representation for debugging.
func (cs *CachedFromImportControlStructure) String() string {
	t := cs.Position()
	return fmt.Sprintf("CachedFromImportControlStructure(Line=%d Col=%d)", t.Line, t.Col)
}

// Execute looks up the pre-compiled template and imports specific macros.
func (cs *CachedFromImportControlStructure) Execute(r *exec.Renderer, tag *nodes.ControlStructureBlock) error {
	// Evaluate filename expression
	filenameValue := r.Eval(cs.filenameExpression)
	if filenameValue.IsError() {
		return errors.Wrap(filenameValue, "unable to evaluate from-import filename")
	}

	filename := filenameValue.String()

	// Look up from pre-compiled cache (thread-safe read)
	template, exists := cs.compiledTemplates[filename]
	if !exists {
		return fmt.Errorf("template '%s' not found in cache for from-import (available: %d templates)", filename, len(cs.compiledTemplates))
	}

	// Get all macros from the template
	imported := template.Macros()

	// Import only the requested macros
	for alias, name := range cs.as {
		node := imported[name]
		if node == nil {
			return fmt.Errorf("macro '%s' not found in template '%s'", name, filename)
		}

		fn, err := exec.MacroNodeToFunc(node, r)
		if err != nil {
			return errors.Wrapf(err, "unable to import macro '%s'", name)
		}

		// Make macro available directly: {% from "x" import foo %} -> foo()
		r.Environment.Context.Set(alias, fn)
	}

	return nil
}

// parseMacroImports parses comma-separated macro names with optional aliases and context.
// Returns true if parsing should stop (end of args or context keyword found).
func (cs *CachedFromImportControlStructure) parseMacroImports(args *parser.Parser) error {
	for !args.End() {
		name := args.Match(tokens.Name)
		if name == nil {
			return args.Error("expected macro name (identifier)", args.Current())
		}

		// Check for "as alias"
		if args.MatchName("as") != nil {
			alias := args.Match(tokens.Name)
			if alias == nil {
				return args.Error("expected alias name (identifier)", nil)
			}
			cs.as[alias.Val] = name.Val
		} else {
			cs.as[name.Val] = name.Val
		}

		// Check for "with/without context" (ends parsing)
		if tok := args.MatchName("with", "without"); tok != nil {
			if args.MatchName("context") != nil {
				cs.withContext = tok.Val == keywordWith
				return nil
			}
			args.Stream().Backup()
		}

		if args.End() {
			return nil
		}

		// Expect comma for more macros
		if args.Match(tokens.Comma) == nil {
			return args.Error("expected ','", nil)
		}
	}
	return nil
}

// createCachedFromParser returns a parser function that creates CachedFromImportControlStructure.
//
// Parser syntax matches Gonja's from:
//
//	{% from "template.html" import macro_name %}
//	{% from "template.html" import macro_name as alias %}
//	{% from "template.html" import macro1, macro2, macro3 %}
//	{% from "template.html" import macro1 with context %}
func createCachedFromParser(compiledTemplates map[string]*exec.Template) parser.ControlStructureParser {
	return func(p *parser.Parser, args *parser.Parser) (nodes.ControlStructure, error) {
		cs := &CachedFromImportControlStructure{
			location:          p.Current(),
			compiledTemplates: compiledTemplates,
			as:                map[string]string{},
		}

		if args.End() {
			return nil, args.Error("from requires at least one macro to import", nil)
		}

		// Parse filename expression
		filename, err := args.ParseExpression()
		if err != nil {
			return nil, err
		}
		cs.filenameExpression = filename

		// Parse "import" keyword
		if args.MatchName("import") == nil {
			return nil, args.Error("expected 'import' keyword", args.Current())
		}

		// Parse macro names (comma-separated)
		if err := cs.parseMacroImports(args); err != nil {
			return nil, err
		}

		return cs, nil
	}
}
