# pkg/controller/testrunner - Validation Test Runner

Development context for the validation test runner component.

## When to Work Here

Modify this package when:
- Changing test execution logic
- Adding new assertion types
- Modifying fixture processing
- Improving test result formatting
- Fixing test runner bugs

**DO NOT** modify this package for:
- CLI command implementation → Use `cmd/controller`
- Webhook integration → Use `pkg/controller/dryrunvalidator`
- Template rendering → Use `pkg/templating`
- HAProxy validation → Use `pkg/dataplane`

## Package Purpose

This package implements a pure test runner component that executes embedded validation tests defined in HAProxyTemplateConfig CRDs. It's designed to be called directly from:

1. **CLI** (`controller validate` command) - For local development and CI/CD
2. **Webhook** (via DryRunValidator) - For admission control validation

**Key Design Principle**: Pure component with no EventBus dependency. This allows direct function calls without event coordination overhead.

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                     Test Runner (Pure)                      │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  1. Fixture Processing                                     │
│     - Parse test fixtures from CRD                         │
│     - Create resource stores with indexing                 │
│     - Populate stores with test data                       │
│                                                             │
│  2. Template Rendering                                     │
│     - Build rendering context with fixture stores          │
│     - Render HAProxy config + auxiliary files              │
│     - Handle rendering errors                              │
│                                                             │
│  3. Assertion Execution                                    │
│     - Run all assertions for each test                     │
│     - Collect pass/fail results                            │
│     - Capture detailed error messages                      │
│                                                             │
│  4. Result Aggregation                                     │
│     - Aggregate test results                               │
│     - Calculate pass/fail counts                           │
│     - Format output for CLI/webhook                        │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

## Files Overview

### runner.go - Main Test Orchestration

**Key Functions:**
- `RunTests()` - Executes all or specific tests
- `runSingleTest()` - Executes one test with fixtures
- `renderWithStores()` - Renders config using fixture stores
- `buildRenderingContext()` - Wraps stores with StoreWrapper
- `renderAuxiliaryFiles()` - Renders maps, files, certificates

**Follows DryRunValidator Pattern**: The rendering logic mirrors `DryRunValidator.renderWithOverlayStores()` to ensure consistency.

### fixtures.go - Fixture Store Creation

**Key Functions:**
- `createStoresFromFixtures()` - Converts test fixtures to stores
- `buildTemplateContext()` - Creates context for backward compatibility

**Implementation Details:**
- Uses `indexer.New()` to create indexers for each resource type
- Creates `store.NewMemoryStore()` instances for fast in-memory access
- Extracts index keys using `indexer.ExtractKeys()`
- Ensures resources have proper TypeMeta (APIVersion, Kind)

### http_fixtures.go - HTTP Fixture Handling

**Key Types:**
- `FixtureHTTPStoreWrapper` - Template-callable wrapper for HTTP fixtures

**Key Functions:**
- `NewFixtureHTTPStoreWrapper()` - Creates wrapper with pre-loaded fixtures
- `Fetch()` - Returns fixture content or error if URL not found
- `createHTTPStoreFromFixtures()` - Creates HTTPStore with fixtures pre-loaded
- `mergeHTTPFixtures()` - Merges global and test-specific HTTP fixtures

**Behavior:**
- Always returns fixture content for known URLs
- Fails with descriptive error for unknown URLs (no network requests)
- Test-specific fixtures override global fixtures for same URL

### assertions.go - Assertion Types

Implements 5 assertion types:

1. **haproxy_valid** - Validates config syntax with HAProxy binary
2. **contains** - Regex pattern matching in target content
3. **not_contains** - Ensures pattern is NOT in target content
4. **equals** - Exact value comparison
5. **jsonpath** - JSONPath queries against template context

**Target Resolution:**
- `haproxy.cfg` - Main HAProxy configuration
- `map:<name>` - Map file content
- `file:<name>` - General file content
- `cert:<name>` - SSL certificate content

### output.go - Result Formatting

Formats test results in three modes:
- **Summary** - Human-readable with ✓/✗ symbols
- **JSON** - Structured output for CI/CD tools
- **YAML** - Structured output for readability

## Testing Strategy

### Unit Tests (runner_test.go)

**Coverage Areas:**
- Basic rendering with assertions
- Test filtering by name
- Mixed pass/fail results
- Fixtures used in templates
- Rendering error handling
- Edge cases

**Testing Pattern:**
```go
func TestRunner_Feature(t *testing.T) {
    // 1. Create test config with CRD spec
    config := &v1alpha1.HAProxyTemplateConfigSpec{
        HAProxyConfig: v1alpha1.HAProxyConfig{
            Template: "...",
        },
        ValidationTests: []v1alpha1.ValidationTest{...},
    }

    // 2. Create template engine
    engine, err := templating.New(templating.EngineTypeGonja, templates)

    // 3. Create test runner
    runner := New(config, engine, validationPaths, Options{})

    // 4. Run tests
    results, err := runner.RunTests(ctx, "")

    // 5. Verify results
    assert.Equal(t, expectedPassed, results.PassedTests)
}
```

### Integration Tests (Future)

Should test:
- CLI command execution with real CRD files
- Webhook validation with embedded tests
- Full validation flow with HAProxy binary

## Observability Features

The test runner provides rich observability to help debug failing tests, both via CLI flags and programmatically.

### Content Preview in Assertions

All assertions populate target metadata for observability:

```go
type AssertionResult struct {
    Type        string
    Description string
    Passed      bool
    Error       string

    // Observability fields
    Target        string  // e.g., "map:path-prefix.map"
    TargetSize    int     // Content size in bytes
    TargetPreview string  // First 200 chars (failed assertions only)
}
```

**Implementation:**
- `populateTargetMetadata()` called by all assertion methods
- Preview only for failed assertions (keeps output manageable)
- Truncated to 200 chars to prevent huge outputs

**Usage in assertions:**
```go
// Example from assertContains
func (r *Runner) assertContains(...) AssertionResult {
    target := r.resolveTarget(assertion.Target, haproxyConfig, auxiliaryFiles, renderError)

    matched, err := regexp.MatchString(assertion.Pattern, target)
    if !matched {
        result.Passed = false
        result.Error = fmt.Sprintf("pattern %q not found in %s (target size: %d bytes). Hint: Use --verbose to see content preview",
            assertion.Pattern, assertion.Target, len(target))
    }

    // Populate target metadata for observability
    r.populateTargetMetadata(&result, target, assertion.Target, !matched)

    return result
}
```

### Rendered Content Storage

Test results include complete rendered content for debugging:

```go
type TestResult struct {
    // ... existing fields ...

    // Rendered content (for --dump-rendered)
    RenderedConfig string              // HAProxy configuration
    RenderedMaps   map[string]string   // Map files (path → content)
    RenderedFiles  map[string]string   // General files (filename → content)
    RenderedCerts  map[string]string   // SSL certificates (path → content)
}
```

**Populated in `runSingleTest()`:**
- Captured immediately after successful rendering
- Only populated on successful render (empty if render fails)
- Maps use file path/name as key
- Available in JSON/YAML output formats

**Example population:**
```go
// After successful rendering
if err == nil {
    result.RenderedConfig = haproxyConfig

    if len(auxiliaryFiles.MapFiles) > 0 {
        result.RenderedMaps = make(map[string]string)
        for _, mapFile := range auxiliaryFiles.MapFiles {
            result.RenderedMaps[mapFile.Path] = mapFile.Content
        }
    }
}
```

### Verbose Mode

Verbose mode shows target metadata for failed assertions:

```go
output, err := testrunner.FormatResults(results, testrunner.OutputOptions{
    Format:  testrunner.OutputFormatSummary,
    Verbose: true,  // Enable verbose mode
})
```

**Output formatting** (`formatSummary()` in output.go):
- Shows target name and size for all failed assertions
- Shows content preview if available
- Adds hint about --dump-rendered for large targets (>200 chars)

**Example verbose output:**
```
✗ Path map must use MULTIBACKEND qualifier
  Error: pattern "..." not found in map:path-prefix.map (target size: 61 bytes)
  Target: map:path-prefix.map (61 bytes)
  Content preview:
    split.example.com/app MULTIBACKEND:0:default_split-route_0/
  Hint: Use --dump-rendered to see full content
```

### Enhanced Error Messages

All assertion methods produce enhanced error messages by default:

**Pattern not found:**
```
pattern "X" not found in map:path-prefix.map (target size: 61 bytes).
Hint: Use --verbose to see content preview
```

**Match count:**
```
expected 2 matches, got 0 matches of pattern "X" in map:path-prefix.map (target size: 61 bytes).
Hint: Use --verbose to see content preview
```

**HAProxy validation:**
```
HAProxy validation failed (config size: 1234 bytes): maxconn: integer expected
```

**Benefits:**
- Users immediately see target size without flags
- Clear hint about --verbose flag
- Context included in all error messages
- Discoverability of debugging features

### Template Tracing Integration

If the template engine has tracing enabled, render operations are traced:

```go
engine.EnableTracing()

// All Render() calls are traced
runner := testrunner.New(cfg, engine, paths, options)
results, _ := runner.RunTests(ctx, "")

// Get trace output
trace := engine.GetTraceOutput()
fmt.Println(trace)
```

**Trace output shows:**
- Which templates were rendered
- Render duration in milliseconds
- Nesting depth (for includes)

**Example trace:**
```
Rendering: haproxy.cfg
Completed: haproxy.cfg (0.007ms)
Rendering: path-prefix.map
Completed: path-prefix.map (3.347ms)
```

### Programmatic Usage

**Enable verbose output:**
```go
results, err := runner.RunTests(ctx, "")

output, err := testrunner.FormatResults(results, testrunner.OutputOptions{
    Format:  testrunner.OutputFormatSummary,
    Verbose: true,
})
```

**Access rendered content:**
```go
for _, test := range results.TestResults {
    if !test.Passed {
        fmt.Printf("Test %s failed\n", test.TestName)
        fmt.Printf("Rendered config:\n%s\n", test.RenderedConfig)

        for mapName, content := range test.RenderedMaps {
            fmt.Printf("Map %s:\n%s\n", mapName, content)
        }
    }
}
```

**Access assertion metadata:**
```go
for _, assertion := range test.Assertions {
    if !assertion.Passed {
        fmt.Printf("Assertion failed: %s\n", assertion.Description)
        fmt.Printf("Target: %s (%d bytes)\n", assertion.Target, assertion.TargetSize)
        if assertion.TargetPreview != "" {
            fmt.Printf("Preview: %s\n", assertion.TargetPreview)
        }
    }
}
```

## Common Debugging Patterns

### Debugging Empty Map Files

```bash
# 1. Check what was rendered
controller validate -f config.yaml --dump-rendered

# 2. See if template executed
controller validate -f config.yaml --trace-templates

# 3. Look for template errors in verbose output
controller validate -f config.yaml --verbose
```

**Common causes:**
- Empty loops (no resources match filters)
- Incorrect variable names in templates
- Missing `| default([])` filters on arrays
- Conditional logic preventing execution

### Debugging Pattern Mismatches

```bash
# See actual content vs expected pattern
controller validate -f config.yaml --verbose
```

**Look for:**
- Whitespace differences (extra newlines, trailing spaces)
- Case sensitivity issues
- Regex special characters that need escaping
- Multiline patterns missing `(?m)` flag

**Example:**
```
Expected: "backend foo"
Got:      " backend foo"  (extra leading space)
```

### Debugging Slow Tests

```bash
# See template render times
controller validate -f config.yaml --trace-templates
```

**Templates taking >10ms may need optimization:**
- Simplify complex loops
- Reduce nested includes
- Avoid expensive filters in loops
- Cache repeated computations

**Example trace showing slow template:**
```
Rendering: haproxy.cfg (0.005ms)
Rendering: backends.cfg (45.123ms)  ← Needs optimization
```

### Debugging Test Fixtures

```bash
# Dump rendered content to see if fixtures loaded correctly
controller validate -f config.yaml --dump-rendered
```

**Common fixture issues:**
- Missing `apiVersion` or `kind` fields
- Incorrect index keys (resource not findable)
- Wrong namespace or name in fixture data

## Common Patterns

### Running All Tests

```go
runner := testrunner.New(
    config,
    engine,
    validationPaths,
    testrunner.Options{
        Logger: logger,
    },
)

results, err := runner.RunTests(ctx, "")
if err != nil {
    return err
}

if !results.AllPassed() {
    // Handle test failures
}
```

### Running Specific Test

```go
results, err := runner.RunTests(ctx, "my-test-name")
if err != nil {
    return fmt.Errorf("test %q failed: %w", "my-test-name", err)
}
```

### Custom Validation Paths

```go
validationPaths := dataplane.ValidationPaths{
    HAProxyBinary:    "/usr/local/bin/haproxy",
    TempDir:          "/tmp/haproxy-validation",
    AuxiliaryFileDir: "/tmp/haproxy-validation/aux",
}

runner := testrunner.New(config, engine, validationPaths, options)
```

## Error Handling

### Rendering Errors

Rendering errors are simplified using `dataplane.SimplifyRenderingError()`:

```go
haproxyConfig, auxiliaryFiles, err := r.renderWithStores(stores)
if err != nil {
    result.RenderError = dataplane.SimplifyRenderingError(err)
    // Result is marked as failed, error is user-friendly
}
```

**Example**:
- Raw: `failed to render template 'haproxy.cfg': unable to execute template: failed to call function 'fail': Service 'api' not found`
- Simplified: `Service 'api' not found`

### Validation Errors

HAProxy validation errors are simplified using `dataplane.SimplifyValidationError()`:

```go
err := dataplane.ValidateConfiguration(haproxyConfig, auxiliaryFiles, r.validationPaths)
if err != nil {
    result.Error = dataplane.SimplifyValidationError(err)
}
```

**Example**:
- Raw: `[ALERT] 350/123456 (12345) : parsing [/tmp/haproxy.cfg:15] : 'maxconn' : integer expected, got 'invalid' (line 15, column 12)`
- Simplified: `maxconn: integer expected, got 'invalid' (line 15)`

## Fixture Processing

### Creating Stores from Fixtures

Fixtures are converted to resource stores for template rendering:

```go
stores, err := r.createStoresFromFixtures(test.Fixtures)
// stores["services"] contains a types.Store with indexed service resources
```

### Index Key Extraction

Uses the same indexing as production watchers:

```go
// From CRD spec
watchedResource := r.config.WatchedResources["services"]
// IndexBy: ["metadata.namespace", "metadata.name"]

// Extract keys using indexer
idx, _ := indexer.New(indexer.Config{
    IndexBy: watchedResource.IndexBy,
})
keys, _ := idx.ExtractKeys(&resource)
// keys: ["default", "my-service"]

// Add to store with keys
store.Add(&resource, keys)
```

### TypeMeta Inference

Fixtures may omit TypeMeta fields. The runner infers them:

```go
if resource.GetAPIVersion() == "" {
    resource.SetAPIVersion(watchedResource.APIVersion)
}
if resource.GetKind() == "" {
    kind := resourcestore.SingularizeResourceType(watchedResource.Resources)
    resource.SetKind(kind)
}
```

**Example**: `"services"` → `"Service"`

## Template Context Building

### StoreWrapper Usage

Fixtures are wrapped with `renderer.StoreWrapper` for template access:

```go
resources := make(map[string]interface{})
for resourceTypeName, store := range stores {
    resources[resourceTypeName] = &renderer.StoreWrapper{
        Store:        store,
        ResourceType: resourceTypeName,
        Logger:       r.logger,
    }
}
```

**Template Usage**:
```gonja
{% for svc in resources.services.List() %}
  {{ svc.metadata.name }}
{% endfor %}
```

### Context Structure

```go
context := map[string]interface{}{
    "resources": map[string]*renderer.StoreWrapper{
        "services": ...,
        "ingresses": ...,
    },
    "template_snippets": []string{"snippet1", "snippet2"},
}
```

## Assertion Implementation

### Pattern Matching (contains, not_contains)

Uses Go's regexp package:

```go
matched, err := regexp.MatchString(assertion.Pattern, target)
if err != nil {
    result.Error = fmt.Sprintf("invalid regex pattern: %v", err)
}
```

### Exact Comparison (equals)

Direct string comparison with truncation for long values:

```go
if target != assertion.Expected {
    targetPreview := truncateString(target, 100)
    expectedPreview := truncateString(assertion.Expected, 100)
    result.Error = fmt.Sprintf("expected %q, got %q", expectedPreview, targetPreview)
}
```

### JSONPath Queries (jsonpath)

Uses client-go's JSONPath implementation:

```go
jp := jsonpath.New("assertion")
jp.Parse(assertion.JSONPath)
results, _ := jp.FindResults(templateContext)

actualValue := fmt.Sprintf("%v", results[0][0].Interface())
if actualValue != assertion.Expected {
    result.Error = fmt.Sprintf("expected %q, got %q", assertion.Expected, actualValue)
}
```

## Common Pitfalls

### Using Wrong Store Type

**Problem**: Trying to use `store.ResourceStore` instead of `types.Store`.

```go
// Bad
var stores map[string]store.ResourceStore

// Good
var stores map[string]types.Store
```

**Why**: `types.Store` is the interface; implementations may vary (MemoryStore, CachedStore).

### Forgetting to Extract Index Keys

**Problem**: Adding resources without index keys.

```go
// Bad
store.Add(&resource)  // Missing keys parameter!

// Good
keys, _ := indexer.ExtractKeys(&resource)
store.Add(&resource, keys)
```

### Wrong Template Method Name

**Problem**: Using lowercase `list()` instead of `List()`.

```gonja
{# Bad #}
{% for svc in resources.services.list() %}

{# Good #}
{% for svc in resources.services.List() %}
```

**Why**: StoreWrapper methods are capitalized (Go convention).

### Not Handling Rendering Errors

**Problem**: Assuming rendering always succeeds.

```go
// Bad - doesn't check for rendering errors
result.Passed = true

// Good - checks for rendering errors
if result.RenderError != "" {
    result.Passed = false
    return result
}
```

## Adding New Assertion Types

### Checklist

1. Add assertion type constant to CRD
2. Implement assertion method in assertions.go
3. Add case in `runAssertion()` switch
4. Add unit tests
5. Document in user documentation

### Example: Adding "regex_match" Assertion

```go
// Step 1: Add to runner.go switch
case "regex_match":
    result = r.assertRegexMatch(haproxyConfig, auxiliaryFiles, assertion)

// Step 2: Implement assertion method
func (r *Runner) assertRegexMatch(
    haproxyConfig string,
    auxiliaryFiles *dataplane.AuxiliaryFiles,
    assertion v1alpha1.ValidationAssertion,
) AssertionResult {
    result := AssertionResult{
        Type:        "regex_match",
        Description: assertion.Description,
        Passed:      true,
    }

    target := r.resolveTarget(assertion.Target, haproxyConfig, auxiliaryFiles)

    re, err := regexp.Compile(assertion.Pattern)
    if err != nil {
        result.Passed = false
        result.Error = fmt.Sprintf("invalid regex: %v", err)
        return result
    }

    matches := re.FindAllString(target, -1)
    if len(matches) == 0 {
        result.Passed = false
        result.Error = "no matches found"
    }

    return result
}

// Step 3: Add unit tests
func TestRunner_RegexMatch(t *testing.T) {
    // Test implementation...
}
```

## Performance Considerations

### Memory Usage

- **Fixtures**: Stored in memory as unstructured resources (~1KB per resource)
- **Stores**: MemoryStore with O(1) lookups via composite keys
- **Rendering**: Single render per test (not cached across tests)

### Optimization Opportunities

1. **Parallel Test Execution**: Run independent tests concurrently
2. **Template Caching**: Cache compiled templates across tests
3. **Store Reuse**: Reuse stores for tests with identical fixtures

### Current Performance

- Single test execution: <10ms (without HAProxy validation)
- HAProxy validation adds: 50-200ms per test
- Memory: ~1-5MB per test depending on fixture size

## Resources

- API documentation: `pkg/controller/testrunner/README.md`
- User documentation: `docs/validation-tests.md`
- DryRunValidator pattern: `pkg/controller/dryrunvalidator/CLAUDE.md`
- StoreWrapper: `pkg/controller/renderer/CLAUDE.md`
- Architecture: `/docs/development/design.md`
