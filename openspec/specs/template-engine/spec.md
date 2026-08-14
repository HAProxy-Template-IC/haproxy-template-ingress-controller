# Template Engine

## Purpose

Scriggo-based template engine providing pre-compilation, concurrent rendering, profiling, tracing, and a resource-agnostic function library for generating HAProxy configurations.

## Requirements

### Requirement: Pre-Compilation and Engine Lifecycle

The engine SHALL compile all entry-point templates at initialization time using Scriggo's BuildTemplate. Template snippets not listed as entry points SHALL be discovered and compiled automatically by Scriggo when referenced via render or render_glob statements. Compilation errors SHALL be reported as CompilationError with the template name, the first 200 characters of template content as TemplateSnippet, and the underlying cause.

#### Scenario: Templates compiled at initialization

WHEN New is called with a set of templates and (optionally) entry points via Options.EntryPoints
THEN all entry-point templates SHALL be compiled before the constructor returns, and compilation errors SHALL be detected at this point rather than at render time.

#### Scenario: CompilationError includes template snippet

WHEN a template with invalid syntax is compiled and its content exceeds 200 characters
THEN the resulting CompilationError SHALL contain a TemplateSnippet field with the first 200 characters followed by "...".

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

The Engine interface SHALL provide RenderWithProfiling, which returns the rendered output and an aggregated slice of IncludeStats. Stats SHALL be aggregated by template name so that multiple renders of the same template produce a single entry with count > 1. Stats SHALL be sorted by TotalMs descending. When profiling is not enabled (engine created without Options.Profiling set), RenderWithProfiling SHALL return nil stats. Profiling SHALL be enabled by constructing the engine with Options.Profiling set to true.

#### Scenario: IncludeStats aggregated by template name

WHEN a template renders the same sub-template 3 times via render statements and profiling is enabled
THEN RenderWithProfiling SHALL return an IncludeStats entry for that sub-template with Count=3 and TotalMs equal to the sum of all three renders.

#### Scenario: Profiling disabled returns nil stats

WHEN an engine is created without Options.Profiling (left at its false zero value) and RenderWithProfiling is called
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
THEN the engine SHALL automatically create and inject a new SharedContext, enabling SharedContext caching (ComputeIfAbsent/Get) and first_seen to work.

### Requirement: Dynamic Includes

The engine SHALL support three forms of template inclusion: render with a string literal for static includes, render with a variable for dynamic computed includes, and render_glob with a glob pattern to render all matching templates in alphabetical order. The inherit_context modifier SHALL make the calling scope's local variables available to the rendered template.

#### Scenario: render_glob renders matching templates in alphabetical order

WHEN render_glob "backend-*" is evaluated and templates "backend-c", "backend-a", "backend-b" exist
THEN all three templates SHALL be rendered in the order backend-a, backend-b, backend-c.

#### Scenario: inherit_context passes local variables

WHEN a template sets a local variable and renders another template with inherit_context
THEN the rendered template SHALL have access to that local variable.

### Requirement: Post-Processing Pipeline

The engine SHALL support per-template post-processor chains configured at construction time. Post-processors SHALL be applied in sequence after rendering completes. Two post-processor types SHALL exist. The regex_replace type SHALL apply a compiled regular expression find/replace to the output, processing the output line by line so that line-anchored patterns (such as "^[ ]+" for indentation normalization) behave predictably. The template type SHALL run the rendered output through a second Scriggo pass: the post-processor template is compiled at engine construction (fail-fast on syntax errors) with all standard engine globals plus an `input` string variable that receives the prior output at processing time, and the second pass's output becomes the new rendered content. The NewPostProcessor factory SHALL construct only regex_replace processors (erroring on other types); the template type is wired during engine construction because it needs the engine's globals.

#### Scenario: Regex replace post-processor applied

WHEN a template has a regex_replace post-processor configured with pattern "^[ ]+" and replace "  "
THEN the rendered output SHALL have all leading whitespace on each line normalized to two spaces.

#### Scenario: Template post-processor transforms via input variable

- **WHEN** a template has a template post-processor whose source reads the `input` variable
- **THEN** the post-processor SHALL receive the previously rendered output as `input` and its own output SHALL replace the rendered content.

#### Scenario: Template post-processor compile error is fail-fast

- **WHEN** a template post-processor source contains a syntax error
- **THEN** engine construction SHALL fail rather than deferring the error to render time.

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

### Requirement: SharedContext Caching API

The SharedContext runtime variable SHALL provide per-render caching isolated between Render calls via two methods: ComputeIfAbsent(key, compute) and Get(key). ComputeIfAbsent SHALL return the value for the key (computing and storing it via the supplied function only if absent) together with a wasComputed boolean that is true only for the caller that actually ran the computation. Get SHALL return the stored value or nil for read-only access. There SHALL be no mutating Set method, so check-then-act races are structurally prevented. The cache SHALL use singleflight for thread-safe compute-once semantics across concurrent renders.

#### Scenario: Cache isolation between renders

WHEN ComputeIfAbsent("key", ...) stores a value during one Render invocation
THEN a subsequent independent Render invocation SHALL NOT see that cached value (each Render gets a fresh or distinct SharedContext unless the caller reuses one).

#### Scenario: Compute-once via SharedContext

WHEN multiple concurrent template sections call ComputeIfAbsent for the same key
THEN the supplied compute function SHALL run exactly once and exactly one caller SHALL observe wasComputed == true.

### Requirement: Navigation and Type Functions

The dig function SHALL navigate nested maps using a sequence of string keys, returning nil if any key is missing. The fast path SHALL handle map[string]interface{} without reflection. dig SHALL also navigate typed structs (the typed-watched-resources shape) by matching each key against the fields' json tags via reflection, and generic string-keyed maps of other value types; an optional (omitempty-tagged) struct field holding its zero value SHALL normalise to nil so dig-plus-fallback behaves identically across typed and untyped shapes. fallback (alias: coalesce) SHALL return the first non-nil argument. tostring, toint, tofloat, toStringSlice, and toSlice SHALL perform lenient type conversions. isNil SHALL detect typed nil pointers using reflection. keys SHALL return sorted map keys.

#### Scenario: dig navigates nested map

WHEN dig is called with obj and keys "metadata", "namespace" and the nested value is "default"
THEN dig SHALL return "default".

#### Scenario: dig returns nil for missing key

WHEN dig is called with obj and keys "metadata", "nonexistent"
THEN dig SHALL return nil.

#### Scenario: dig navigates a typed struct by json tag

- **WHEN** dig is called on a typed watched-resource pointer with keys "metadata", "name"
- **THEN** it SHALL resolve the fields by their json tags and return the name value, and an unpopulated optional field SHALL yield nil rather than the type's zero value.

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

### Requirement: Bounded Regex Search Compilation Cache

Each engine SHALL retain a successful `regex_search` compilation when its pattern is at most 256 bytes, its bounded complexity cost is at most 256, and the cache still has capacity when the decision is committed. Empty, no-match, zero-width, and wildcard operators cost one; literal cost is `max(1, 2 * rune count)`; character-class cost is one plus its rune-table size; capture and star add two to their child; plus and question add one; concatenation sums its children; alternation also adds one per branch after the first; finite repetition costs `max * child + max - min`; zero-minimum unbounded repetition adds two to its child; and positive-minimum unbounded repetition costs `min * child + 1`. All arithmetic SHALL saturate above 256.

The engine SHALL retain at most 64 `regex_search` decisions. Invalid or longer patterns and patterns encountered after the cache reaches capacity SHALL use the existing compile path without being retained. A valid high-complexity pattern MAY retain its bounded key and a rejection decision, but not its compiled program. Invalid patterns SHALL still abort the render.

#### Scenario: Repeated patterns preserve results

- **WHEN** `regex_search` receives a pattern whose successful compilation was admitted to its cache
- **THEN** later calls SHALL return the same result as a fresh compilation while the engine reuses that compiled pattern.

#### Scenario: Uncached patterns preserve errors

- **WHEN** a `regex_search` pattern is invalid, longer than 256 bytes, has a bounded complexity cost above 256, or arrives after the cache reaches capacity
- **THEN** compilation and error behavior SHALL remain unchanged, and the engine SHALL NOT retain its compiled program.

### Requirement: String Aliases and Fused Accessors

Alongside the strings_* forms, the engine SHALL register the short aliases strip and trim (whitespace removal), toLower, replace (overriding the builtin to support the three-argument form that replaces all occurrences), hasPrefix, and hasSuffix. The engine SHALL also provide dig_string, fusing the dig-fallback-tostring chain into one call — `value | dig_string(defaultStr, keys...)` SHALL be equivalent to `value | dig(keys...) | fallback(defaultStr) | tostring()` — for string access at polymorphic value boundaries such as annotation lookups.

#### Scenario: replace supports three-argument replace-all

- **WHEN** replace is called with ("a-b-c", "-", "_")
- **THEN** it SHALL return "a_b_c" with every occurrence replaced.

#### Scenario: dig_string falls back and stringifies

- **WHEN** dig_string is called with a default of "" and keys that do not resolve on the input
- **THEN** it SHALL return "" as a string rather than nil.

### Requirement: Shape Normalization and Numeric Sorting Functions

The to_str_map function SHALL normalise any string-keyed map — map[string]string (the typed-watched-resources shape for labels, matchLabels, and annotations), map[string]any (the untyped store shape), or a generic string-keyed map — into a uniform map[string]string for template iteration, coercing non-string values via tostring; nil input SHALL return nil. It is the sanctioned replacement for map type assertions on label-shaped fields, which panic against the typed shape. The sort_ints function SHALL sort a []any of integer values numerically (where lexicographic sort_strings would misorder, e.g. "10" before "2"); non-integer entries SHALL be coerced via toint, so unparseable values become 0 and sort to the front. The basename function SHALL return the final element of a path.

#### Scenario: to_str_map handles both map shapes

- **WHEN** to_str_map is called once with a map[string]string and once with a map[string]any holding a non-string value
- **THEN** both calls SHALL return map[string]string, with the non-string value coerced via tostring.

#### Scenario: sort_ints sorts numerically

- **WHEN** sort_ints is called with the values 10, 2, and 1
- **THEN** the result order SHALL be 1, 2, 10.

#### Scenario: basename extracts the file name

- **WHEN** basename is called with "maps/host.map"
- **THEN** it SHALL return "host.map".

### Requirement: GUID and Version Functions

The make_guid function SHALL join its arguments with ":" to build an HAProxy GUID. When the joined result exceeds 127 characters (the hard HAProxy GUID length limit), the function SHALL truncate it and append "." plus the first 8 hex characters of the SHA-256 hash of the full GUID, producing a result of exactly 127 characters that stays unique per input; results at or under the limit SHALL be returned unchanged. The semver_gte function SHALL report whether a version is at least a minimum version, comparing major and minor components only (patch is ignored), tolerating a leading "v", and returning false when either argument is empty or unparseable.

#### Scenario: Over-long GUID truncated with hash suffix

- **WHEN** make_guid produces a joined string longer than 127 characters
- **THEN** the result SHALL be exactly 127 characters, ending in "." followed by 8 hex characters derived from the full untruncated GUID.

#### Scenario: semver_gte ignores the patch component

- **WHEN** semver_gte is called with version "3.3.9" and minimum "3.3"
- **THEN** it SHALL return true, and comparing "3.2.99" against "3.3" SHALL return false.

#### Scenario: Unparseable version is false

- **WHEN** semver_gte is called with an empty or non-numeric version string
- **THEN** it SHALL return false.

### Requirement: Collection Functions

The engine SHALL provide: append (handling nil slices and interface{} types), merge (returning a new map with updates overriding originals), sort_strings (sorting []interface{} as strings), first_seen (returning true only on the first call for a given composite key within a render, thread-safe via SharedContext), selectattr (filtering items by attribute existence, equality, inequality, or membership), join (joining []string or []interface{} with a separator), join_key (building composite key strings), shard_slice (dividing a slice into N shards returning the portion for a given index), seq (generating integer sequences 0..n-1), ceil, and namespace (creating mutable maps for loop state). shard_slice SHALL be type-preserving: declared as an adaptive native function, its static return type at each call site equals the input slice's static type, so sharding a typed watched-resource slice keeps typed element access through the shard call.

#### Scenario: first_seen deduplicates across parallel renders

WHEN first_seen("backends", "default", "my-svc") is called from two parallel template goroutines
THEN exactly one call SHALL return true and the other SHALL return false.

#### Scenario: shard_slice distributes items evenly

WHEN shard_slice is called with 10 items, shard index 0, and 3 total shards
THEN it SHALL return the first 4 items (10/3 = 3 with remainder 1, first shard gets the extra).

#### Scenario: shard_slice preserves the input slice type

- **WHEN** shard_slice is called on a slice of typed watched-resource pointers
- **THEN** the call site's static return type SHALL be the same typed slice, not a slice of any.

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

### Requirement: Engine Constructor

The package SHALL provide a single constructor, New(templates map[string]string, opts *Options), that builds the Scriggo engine. A nil opts (or a zero-value Options) SHALL compile every template as an entry point with no custom filters, functions, post-processors, type declarations, or profiling. The Options struct SHALL expose all optional behaviour as fields: EntryPoints (templates compiled explicitly; the remainder are snippets discovered on demand via render/render_glob), Filters and Functions (custom filters and global functions merged over the built-in set), PostProcessors (per-template post-processing config), Declarations (domain-specific type declarations registered with Scriggo), and Profiling (enables Scriggo's built-in profiler). The fail function supplied in Functions SHALL be skipped in favor of the Scriggo-native implementation.

#### Scenario: Declarations register additional types

WHEN New is called with Options.Declarations containing a domain-specific type
THEN templates SHALL be able to reference that type in variable declarations and macro signatures.

### Requirement: Resource-Agnostic Design

Template functions and filters SHALL NOT contain knowledge of specific Kubernetes resource structures (Ingress, HTTPRoute, Service, etc.). Resource-specific logic SHALL be implemented as template macros in library files. Functions SHALL be generic utilities (dig, fallback, toSlice, sort_by, etc.) usable with any data structure.

#### Scenario: No Kubernetes-specific functions in engine

WHEN the engine's registered functions and filters are enumerated
THEN none SHALL reference specific Kubernetes resource types, fields, or API versions in their implementation.

### Requirement: StatusPatchCollector in Render Context

The render context SHALL include a `statusPatchCollector` key containing a StatusPatchCollector instance. The collector SHALL be created fresh for each render cycle (same lifecycle as FileRegistry). After rendering completes, the caller SHALL retrieve collected patches via `statusPatchCollector.Patches()`. The StatusPatchCollector SHALL be a new type in `pkg/templating` (or `pkg/controller/rendercontext`) implementing thread-safe collection with `sync.Mutex`.

#### Scenario: StatusPatchCollector available in templates

- **WHEN** a template accesses `statusPatchCollector` from the render context
- **THEN** it SHALL receive a non-nil StatusPatchCollector instance

#### Scenario: Fresh collector per render cycle

- **WHEN** two consecutive render cycles execute
- **THEN** each cycle SHALL have its own StatusPatchCollector instance with no patches carried over from the previous cycle

### Requirement: toJSON Filter Registration

The template engine SHALL register a `toJSON` filter function (also accessible as `to_json`) that serializes any Go value to a JSON string using `encoding/json.Marshal`. The filter SHALL be usable both as a piped filter (`value | toJSON`) and as a standalone function (`toJSON(value)`).

#### Scenario: toJSON registered as filter

- **WHEN** a template uses `{{ myMap | toJSON }}`
- **THEN** the engine SHALL produce the JSON-serialized representation of myMap

### Requirement: Watched Resource Metadata in Render Context

The render context's per-resource surface (`resources.<name>`) SHALL expose the resolved API version of each watched resource via an `APIVersion()` accessor returning the group/version string the controller actually watches. The accessor SHALL be generic watch-set metadata, identical in shape for every watched resource, and SHALL reflect runtime resolution (not the configuration literal) when an ordered candidate list is in use.

#### Scenario: Templates read the resolved version

- **WHEN** a watched resource configured with `apiVersions: [example.io/v1, example.io/v1beta1]` resolves to `example.io/v1beta1` and a template evaluates `resources.<name>.APIVersion()`
- **THEN** the expression SHALL yield `example.io/v1beta1`.

#### Scenario: Status patches target a served version

- **WHEN** a status-patch macro passes `resources.<name>.APIVersion()` as the statusPatch apiVersion argument
- **THEN** the emitted patch SHALL target the version the cluster serves, and the status applier SHALL apply it without a version-mapping error.
