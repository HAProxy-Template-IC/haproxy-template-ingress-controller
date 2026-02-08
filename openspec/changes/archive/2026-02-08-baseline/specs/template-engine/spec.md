# Template Engine

Scriggo-based template engine providing pre-compilation, concurrent rendering, profiling, tracing, and a resource-agnostic function library for generating HAProxy configurations.

## ADDED Requirements

### Requirement: Pre-Compilation and Engine Lifecycle

The engine SHALL compile all entry-point templates at initialization time using Scriggo's BuildTemplate. Template snippets not listed as entry points SHALL be discovered and compiled automatically by Scriggo when referenced via render or render_glob statements. Compilation errors SHALL be reported as CompilationError with the template name, the first 200 characters of template content as TemplateSnippet, and the underlying cause. The New constructor SHALL reject unsupported engine types with UnsupportedEngineError.

#### Scenario: Templates compiled at initialization

WHEN NewScriggo is called with a set of templates and entry points
THEN all entry-point templates SHALL be compiled before the constructor returns, and compilation errors SHALL be detected at this point rather than at render time.

#### Scenario: CompilationError includes template snippet

WHEN a template with invalid syntax is compiled and its content exceeds 200 characters
THEN the resulting CompilationError SHALL contain a TemplateSnippet field with the first 200 characters followed by "...".

#### Scenario: Unsupported engine type rejected

WHEN New is called with an EngineType other than EngineTypeScriggo
THEN it SHALL return an UnsupportedEngineError containing the invalid engine type.

### Requirement: Thread-Safe Concurrent Rendering

The engine SHALL support concurrent rendering from multiple goroutines without requiring external synchronization. Each Render call SHALL operate on its own execution context. The compiled templates SHALL be immutable after initialization and shared across all renders.

#### Scenario: Concurrent renders produce correct isolated output

WHEN multiple goroutines call Render simultaneously with different template contexts
THEN each render SHALL produce output based on its own context without interference from other concurrent renders.

### Requirement: Render Method Contract

The Render method SHALL accept a context.Context for cancellation and timeout control, a template name, and a template context map. It SHALL return RenderError on execution failure, RenderTimeoutError when the context deadline is exceeded, and TemplateNotFoundError (including a sorted list of available template names) when the requested template does not exist. Render output SHALL always end with a newline character. A nil template context SHALL be treated as an empty map with a default SharedContext.

#### Scenario: Rendering a non-existent template

WHEN Render is called with a template name that does not exist
THEN it SHALL return a TemplateNotFoundError whose AvailableTemplates field contains all compiled template names sorted alphabetically.

#### Scenario: Context cancellation produces RenderTimeoutError

WHEN Render is called with a context that is cancelled or has its deadline exceeded during execution
THEN it SHALL return a RenderTimeoutError wrapping the context error.

#### Scenario: Output ends with newline

WHEN Render produces output that does not end with a newline
THEN the engine SHALL append a newline character to the output before returning.

#### Scenario: Nil context treated as empty map

WHEN Render is called with a nil templateContext
THEN the engine SHALL create a new map with a default SharedContext and render successfully.

### Requirement: Profiling Support

The ProfilingRenderer interface SHALL provide RenderWithProfiling, which returns the rendered output and an aggregated slice of IncludeStats. Stats SHALL be aggregated by template name so that multiple renders of the same template produce a single entry with count > 1. Stats SHALL be sorted by TotalMs descending. When profiling is not enabled (engine created via NewScriggo), RenderWithProfiling SHALL return nil stats. Profiling SHALL be enabled by constructing the engine with NewScriggoWithProfiling or NewScriggoWithProfilingAndDeclarations.

#### Scenario: IncludeStats aggregated by template name

WHEN a template renders the same sub-template 3 times via render statements and profiling is enabled
THEN RenderWithProfiling SHALL return an IncludeStats entry for that sub-template with Count=3 and TotalMs equal to the sum of all three renders.

#### Scenario: Profiling disabled returns nil stats

WHEN an engine is created via NewScriggo (not NewScriggoWithProfiling) and RenderWithProfiling is called
THEN the stats slice SHALL be nil.

### Requirement: Template Introspection

The TemplateIntrospector interface SHALL provide: TemplateNames returning all compiled template names sorted alphabetically; HasTemplate returning true if a compiled template exists; GetRawTemplate returning the original template string or TemplateNotFoundError; TemplateCount returning the number of compiled templates.

#### Scenario: TemplateNames returns sorted list

WHEN an engine is created with templates named "c", "a", "b"
THEN TemplateNames SHALL return ["a", "b", "c"].

#### Scenario: GetRawTemplate returns original content

WHEN GetRawTemplate is called for a template that exists
THEN it SHALL return the exact template string provided at construction time.

#### Scenario: GetRawTemplate for unknown template

WHEN GetRawTemplate is called for a template name that does not exist
THEN it SHALL return a TemplateNotFoundError.

### Requirement: Execution Tracing

The TracingController interface SHALL provide EnableTracing, DisableTracing, IsTracingEnabled, and GetTraceOutput. When tracing is enabled, each Render call SHALL record rendering start, nested include hierarchy with indentation, and completion with duration. GetTraceOutput SHALL return all accumulated traces and clear the buffer. Traces from multiple concurrent renders SHALL be collected without data races. AppendTraces SHALL aggregate traces from another engine instance into the current engine's buffer.

#### Scenario: Trace output shows nested rendering hierarchy

WHEN tracing is enabled and a template renders sub-templates
THEN GetTraceOutput SHALL contain "Rendering:" and "Completed:" entries with indentation reflecting the nesting depth.

#### Scenario: GetTraceOutput clears the buffer

WHEN GetTraceOutput is called
THEN it SHALL return the accumulated traces and subsequent calls SHALL return empty until new renders occur.

#### Scenario: Concurrent renders produce non-corrupted traces

WHEN tracing is enabled and multiple goroutines render concurrently
THEN GetTraceOutput SHALL contain traces from all renders without interleaved or corrupted entries.

### Requirement: Filter Debug Logging

The FilterDebugController interface SHALL provide EnableFilterDebug, DisableFilterDebug, and IsFilterDebugEnabled. When filter debug is enabled, the sort_by filter SHALL log each comparison with structured fields: criterion, valA, valA_type, valB, valB_type, and result.

#### Scenario: sort_by logs comparisons when debug is enabled

WHEN filter debug is enabled and sort_by sorts items
THEN each comparison SHALL produce a structured log entry with the criterion expression, both compared values and their types, and the comparison result.

### Requirement: VM Pool Management

The ResourceManager interface SHALL provide ClearVMPool, which releases pooled Scriggo VMs to allow garbage collection. ClearVMPool SHALL be safe to call at any time, including during active renders. VMs currently in use by goroutines SHALL not be affected.

#### Scenario: ClearVMPool during idle period

WHEN ClearVMPool is called after all renders have completed
THEN pooled VMs SHALL be released for garbage collection and subsequent renders SHALL allocate new VMs as needed.

### Requirement: Runtime Variables via Nil Pointer Pattern

Runtime variables (pathResolver, resources, controller, templateSnippets, fileRegistry, shared, http, runtimeEnvironment, and others) SHALL be declared with nil pointers in the Scriggo globals so Scriggo knows the type at compile time. Actual values SHALL be provided at render time via the template context map. Methods on runtime variable types (e.g., pathResolver.GetPath, fileRegistry.Register, shared.ComputeIfAbsent) SHALL be callable in templates.

#### Scenario: Runtime variable method call in template

WHEN a template calls pathResolver.GetPath("host.map", "map") and pathResolver is provided in the render context
THEN the method SHALL execute on the runtime value and return the resolved path.

#### Scenario: SharedContext provided automatically

WHEN Render is called without a "shared" key in the template context
THEN the engine SHALL automatically create and inject a new SharedContext, enabling cache functions to work.

### Requirement: Dynamic Includes

The engine SHALL support three forms of template inclusion: render with a string literal for static includes, render with a variable for dynamic computed includes, and render_glob with a glob pattern to render all matching templates in alphabetical order. The inherit_context modifier SHALL make the calling scope's local variables available to the rendered template.

#### Scenario: render_glob renders matching templates in alphabetical order

WHEN render_glob "backend-*" is evaluated and templates "backend-c", "backend-a", "backend-b" exist
THEN all three templates SHALL be rendered in the order backend-a, backend-b, backend-c.

#### Scenario: inherit_context passes local variables

WHEN a template sets a local variable and renders another template with inherit_context
THEN the rendered template SHALL have access to that local variable.

### Requirement: Post-Processing Pipeline

The engine SHALL support per-template post-processor chains configured at construction time. Post-processors SHALL be applied in sequence after rendering completes. The regex_replace post-processor type SHALL apply a compiled regular expression find/replace to the output. An indentation normalization fast path SHALL be used when the pattern is "^[ ]+".

#### Scenario: Regex replace post-processor applied

WHEN a template has a regex_replace post-processor configured with pattern "^[ ]+" and replace "  "
THEN the rendered output SHALL have all leading whitespace on each line normalized to two spaces.

### Requirement: sort_by Filter

The sort_by filter SHALL accept a slice of items and a slice of string criteria. Each criterion SHALL be a JSONPath-like expression (prefixed with "$." or treated as a field name). Supported modifiers: ":desc" for descending order, ":asc" or no modifier for ascending order, ":exists" to sort by field existence (exists first in ascending). The "| length" operator within an expression SHALL sort by the length of the value. Sorting SHALL be stable (preserving original order for equal elements). Nil values SHALL sort to the end in ascending order. sort_by SHALL operate on a copy, never modifying the original slice.

#### Scenario: Multi-criteria sort with desc modifier

WHEN sort_by is called with criteria ["$.priority:desc", "$.name"]
THEN items SHALL be sorted by priority descending first, then by name ascending for equal priorities.

#### Scenario: Exists modifier sorts by field presence

WHEN sort_by is called with criterion "$.method:exists:desc"
THEN items with a non-nil method field SHALL appear before items without one.

#### Scenario: Length operator sorts by value length

WHEN sort_by is called with criterion "$.path | length:desc"
THEN items SHALL be sorted by the length of their path value in descending order.

#### Scenario: Original slice not modified

WHEN sort_by is called on a slice
THEN the original slice SHALL remain in its original order and a new sorted copy SHALL be returned.

### Requirement: glob_match Filter

The glob_match filter SHALL accept a list ([]string or []interface{}) and a glob pattern string. It SHALL return only items matching the pattern using filepath.Match semantics (supporting * and ? wildcards). Non-string items in []interface{} input SHALL be skipped. Items in maps with a "name" field SHALL be matched by that field.

#### Scenario: Glob pattern filters template names

WHEN glob_match is called with ["backend-ingress", "frontend-http", "backend-gateway"] and pattern "backend-*"
THEN it SHALL return ["backend-ingress", "backend-gateway"].

### Requirement: Cache Functions

The functions has_cached, get_cached, and set_cached SHALL provide per-render caching isolated between Render calls. has_cached SHALL return true if a key has been stored, get_cached SHALL return the stored value or nil, and set_cached SHALL store a value. The cache SHALL be backed by the SharedContext which uses singleflight for thread-safe compute-once semantics.

#### Scenario: Cache isolation between renders

WHEN set_cached("key", "value") is called during one Render invocation
THEN a subsequent independent Render invocation SHALL NOT see that cached value (each Render gets a fresh or distinct SharedContext unless the caller reuses one).

#### Scenario: Compute-once via SharedContext

WHEN multiple concurrent template sections call has_cached/set_cached for the same key
THEN the underlying SharedContext.ComputeIfAbsent SHALL ensure the computation runs exactly once.

### Requirement: Navigation and Type Functions

The dig function SHALL navigate nested maps using a sequence of string keys, returning nil if any key is missing. The fast path SHALL handle map[string]interface{} without reflection. fallback (alias: coalesce) SHALL return the first non-nil argument. tostring, toint, tofloat, toStringSlice, and toSlice SHALL perform lenient type conversions. isNil SHALL detect typed nil pointers using reflection. keys SHALL return sorted map keys.

#### Scenario: dig navigates nested map

WHEN dig is called with obj and keys "metadata", "namespace" and the nested value is "default"
THEN dig SHALL return "default".

#### Scenario: dig returns nil for missing key

WHEN dig is called with obj and keys "metadata", "nonexistent"
THEN dig SHALL return nil.

#### Scenario: isNil detects typed nil pointer

WHEN isNil is called with a (*PathResolver)(nil) value
THEN it SHALL return true, even though a plain nil comparison would return false.

### Requirement: String Manipulation Functions

The engine SHALL provide: strings_contains, strings_split, strings_splitn, strings_trim, strings_lower, strings_replace (replaces all occurrences), title, sanitize_regex (escaping regex special characters), regex_search (returning bool), isdigit (checking all characters are digits), and b64decode (base64 standard decoding). All string functions SHALL accept interface{} inputs and perform lenient string conversion.

#### Scenario: b64decode decodes Kubernetes secret value

WHEN b64decode is called with the base64 encoding of "Hello World"
THEN it SHALL return "Hello World".

#### Scenario: sanitize_regex escapes special characters

WHEN sanitize_regex is called with "api.example.com"
THEN it SHALL return "api\\.example\\.com".

### Requirement: Collection Functions

The engine SHALL provide: append (handling nil slices and interface{} types), merge (returning a new map with updates overriding originals), sort_strings (sorting []interface{} as strings), first_seen (returning true only on the first call for a given composite key within a render, thread-safe via SharedContext), selectattr (filtering items by attribute existence, equality, inequality, or membership), join (joining []string or []interface{} with a separator), join_key (building composite key strings), shard_slice (dividing a slice into N shards returning the portion for a given index), seq (generating integer sequences 0..n-1), ceil, and namespace (creating mutable maps for loop state).

#### Scenario: first_seen deduplicates across parallel renders

WHEN first_seen("backends", "default", "my-svc") is called from two parallel template goroutines
THEN exactly one call SHALL return true and the other SHALL return false.

#### Scenario: shard_slice distributes items evenly

WHEN shard_slice is called with 10 items, shard index 0, and 3 total shards
THEN it SHALL return the first 4 items (10/3 = 3 with remainder 1, first shard gets the extra).

#### Scenario: selectattr filters by attribute equality

WHEN selectattr is called with items, attribute "pathType", test "eq", and value "Exact"
THEN it SHALL return only items whose pathType equals "Exact".

### Requirement: Debug Filter

The debug filter SHALL format any value as JSON-indented HAProxy comments (lines prefixed with "#"). An optional label string SHALL be included in the output header. If JSON marshaling fails, the filter SHALL fall back to fmt.Sprintf representation.

#### Scenario: Debug output formatted as HAProxy comments

WHEN debug is called with a map {"key": "value"} and label "test"
THEN the output SHALL start with "# DEBUG test:" and contain JSON-formatted content prefixed with "#" on each line.

### Requirement: Indent Filter

The indent filter SHALL indent each line of a string. It SHALL accept an optional width argument (int for spaces, string for custom prefix, default 4 spaces), an optional first argument (bool, default false to skip first line), and an optional blank argument (bool, default false to skip blank lines).

#### Scenario: Default indent skips first line

WHEN indent is called with "line1\nline2" and no arguments
THEN the output SHALL be "line1\n    line2" (4-space indent on second line, first line unchanged).

### Requirement: fail Function

The fail function SHALL halt template execution immediately with the provided error message using Scriggo's native Env.Stop mechanism. It SHALL be usable in expression context ({{ fail("message") }}).

#### Scenario: fail aborts rendering with message

WHEN a template executes fail("Service not found")
THEN the Render method SHALL return a RenderError whose cause contains the message "Service not found".

### Requirement: PathResolver

The PathResolver SHALL resolve filenames to paths based on file type ("map", "file", "cert", "crt-list") using configured directory prefixes. For "cert" and "crt-list" types, filenames SHALL be sanitized (non-alphanumeric characters except underscore and hyphen replaced with underscores in the basename, preserving the extension). GetBaseDir SHALL return the configured base directory. GetPath with an empty filename SHALL return the base directory for that file type.

#### Scenario: Map file path resolution

WHEN GetPath is called with filename "host.map" and type "map" and MapsDir is "maps"
THEN it SHALL return "maps/host.map".

#### Scenario: SSL certificate filename sanitization

WHEN GetPath is called with filename "api.example.com.pem" and type "cert" and SSLDir is "ssl"
THEN it SHALL return "ssl/api_example_com.pem".

#### Scenario: Empty filename returns directory

WHEN GetPath is called with an empty filename and type "cert" and SSLDir is "ssl"
THEN it SHALL return "ssl".

### Requirement: Multiple Engine Constructors

The package SHALL provide: New (generic constructor accepting EngineType, compiles all templates as entry points), NewScriggo (explicit entry points, non-entry templates discovered on demand), NewScriggoWithDeclarations (adds domain-specific type declarations), NewScriggoWithProfiling (enables profiling), and NewScriggoWithProfilingAndDeclarations (both profiling and declarations). All constructors SHALL accept customFilters, customFunctions, and postProcessorConfigs parameters. The fail function in customFunctions SHALL be skipped in favor of the Scriggo-native implementation.

#### Scenario: NewScriggoWithDeclarations registers additional types

WHEN NewScriggoWithDeclarations is called with additionalDeclarations containing a domain-specific type
THEN templates SHALL be able to reference that type in variable declarations and macro signatures.

### Requirement: Resource-Agnostic Design

Template functions and filters SHALL NOT contain knowledge of specific Kubernetes resource structures (Ingress, HTTPRoute, Service, etc.). Resource-specific logic SHALL be implemented as template macros in library files. Functions SHALL be generic utilities (dig, fallback, toSlice, sort_by, etc.) usable with any data structure.

#### Scenario: No Kubernetes-specific functions in engine

WHEN the engine's registered functions and filters are enumerated
THEN none SHALL reference specific Kubernetes resource types, fields, or API versions in their implementation.
