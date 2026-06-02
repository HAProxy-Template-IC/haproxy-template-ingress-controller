# pkg/controller/testrunner

Pure component that executes the embedded validation tests under `spec.validationTests` in a `HAProxyTemplateConfig`. No EventBus dependency — it's a library that CLI and webhook paths both call directly.

## What It Does

For each test case:

1. Build a fixture-driven render context (the test's `fixtures` are injected as a parallel resource store, no cluster calls).
2. Render every template in the config.
3. Evaluate each assertion in the test (`haproxy_valid`, `contains`, `not_contains`, `match_count`, `equals`, `jsonpath`, `match_order`, `deterministic`). The `haproxy_valid` assertion type runs `haproxy -c` against the rendered output using the supplied `ValidationPaths`; other assertion types do not invoke the HAProxy binary.
5. Collect timing, rendered content, and assertion results into a `TestResult`.

The whole suite runs in a worker pool (`Options.Workers`, defaults to `runtime.NumCPU`). `TestResults` aggregates pass/fail/skip counts and optional summary timings.

## Minimal Usage

```go
import (
    "context"
    "path/filepath"

    "gitlab.com/haproxy-haptic/haptic/pkg/controller/testrunner"
    "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
    "gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
    "gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

cfg, _ := config.LoadConfig(configYAML)
config.SetDefaults(cfg)

engine, _ := templating.New(templating.EngineTypeScriggo, buildTemplates(cfg), nil, nil, nil)

paths := &dataplane.ValidationPaths{
    TempDir:           tempDir,                               // created + cleaned up by the caller
    MapsDir:           filepath.Join(tempDir, "maps"),
    SSLCertsDir:       filepath.Join(tempDir, "ssl"),
    CRTListDir:        filepath.Join(tempDir, "crt-list"),    // on HAProxy < 3.2 this may equal SSLCertsDir
    GeneralStorageDir: filepath.Join(tempDir, "general"),
    ConfigFile:        filepath.Join(tempDir, "haproxy.cfg"),
}

runner := testrunner.New(cfg, engine, paths, testrunner.Options{
    Workers:         0,            // 0 → NumCPU
    DebugFilters:    false,        // set for `--debug-filters`
    ProfileIncludes: false,        // set for `--profile-includes`
    Capabilities:    capabilities, // from dataplane.CapabilitiesFromVersion
    HAProxyVersion:  haproxyVer,
})

results, err := runner.RunTests(context.Background(), "") // "" = all tests

out, _ := testrunner.FormatResults(results, testrunner.OutputOptions{
    Format:       testrunner.OutputFormatSummary, // or OutputFormatJSON / OutputFormatYAML
    Verbose:      false,
    DumpRendered: false,
})
fmt.Println(out)
```

The HAProxy binary is looked up via `exec.LookPath("haproxy")` by `pkg/dataplane` — there's no field on `ValidationPaths` for it. If no binary is on `$PATH`, tests whose only assertion is `haproxy_valid` are *skipped* (not failed) and surface as `SkipReason: "haproxy binary not found"` in the results.

## API Surface

```go
func New(
    cfg *config.Config,
    engine templating.Engine,
    validationPaths *dataplane.ValidationPaths,
    options Options,
) *Runner

func (r *Runner) RunTests(ctx context.Context, testName string) (*TestResults, error)

func FormatResults(results *TestResults, options OutputOptions) (string, error)
```

`testName == ""` runs every test; otherwise the runner filters by exact name and returns a `TestResults` containing just that one (errors if the name doesn't exist). Context cancellation aborts in-flight tests and returns whatever completed.

## Options

```go
type Options struct {
    TestName        string                 // filter; empty = all tests (alternative to RunTests' second arg)
    Logger          *slog.Logger           // defaults to slog.Default()
    Workers         int                    // 0 → NumCPU; 1 → sequential
    DebugFilters    bool                   // wired to --debug-filters
    ProfileIncludes bool                   // wired to --profile-includes
    Capabilities    dataplane.Capabilities // value, not pointer; gates tests that need specific DP API features
    HAProxyVersion  *dataplane.Version
}
```

`DebugFilters` and `ProfileIncludes` are passed through to per-worker engine configuration — every worker gets its own `templating.Engine` clone so tracing and debug output don't cross-contaminate between parallel tests.

## Result Types

- `TestResults` — suite-level: totals, duration, slice of `TestResult`.
- `TestResult` — per-test: name, pass/fail, duration, skip reason, rendered content (populated for `--dump-rendered`), auxiliary files, assertion results.
- `AssertionResult` — per-assertion: type, target (`haproxy.cfg`, `map:<name>`, `file:<name>`, `cert:<name>`, `crt-list:<name>`, `rendering_error`), pass/fail, human-readable error message, target size in bytes, and a 200-char preview of the target on failure.

See `pkg/controller/testrunner/types.go` for the full schema; `FormatResults` can render any of them as `summary`, `json`, or `yaml`.

## Concurrency Model

- Each worker holds its own cloned `templating.Engine` (sharing the parent compiled templates but with independent trace/debug state).
- Fixtures are in-memory overlays built per-test; no shared mutable state between tests.
- `RunTests` fans tests out over a channel, fans results back in, and preserves deterministic ordering in the result slice.
- Safe to call `RunTests` multiple times on the same `Runner` — per-run state (rendered content, stores) is re-created each invocation.

## Error Simplification

Rendering and validation errors pass through `dataplane.SimplifyRenderingError` / `dataplane.SimplifyValidationError` before landing in `TestResult.RenderError` / `TestResult.Error`. This surfaces the user-relevant message (`Service 'api' not found in namespace 'default'`) instead of the full Go error chain, matching what the admission webhook emits — so CLI and webhook failure output stay consistent.

## See Also

- [`docs/controller/docs/validation-tests.md`](../../../docs/controller/docs/validation-tests.md) — user-facing authoring guide (fixtures, assertion types, CLI flags)
- [`pkg/dataplane/validator.go`](../../dataplane/validator.go) — `ValidationPaths` definition + the `haproxy -c` semantic validator
- [`pkg/controller/rendercontext`](../rendercontext/) — how fixtures become render contexts
- [`cmd/controller/validate.go`](../../../cmd/controller/validate.go) — CLI wiring (populates `Options` from flags)
- `pkg/controller/testrunner/CLAUDE.md` — developer context (adding new assertion types, fixture processing internals)

## License

Apache-2.0 — see root `LICENSE`.
