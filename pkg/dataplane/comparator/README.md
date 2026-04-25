# pkg/dataplane/comparator

Section-by-section diff between two parsed HAProxy configurations. Given a current `*parser.StructuredConfig` and a desired one, the comparator returns the ordered list of `Operation`s the synchronizer must apply to bring the current into line with the desired (plus a `DiffSummary` for quick decisions like "should we even start a transaction?").

The orchestrator (`pkg/dataplane.Sync` / `DryRun`) drives this — most callers use it via the higher-level entry points rather than instantiating a `Comparator` directly.

## Usage

```go
import (
    "gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator"
    "gitlab.com/haproxy-haptic/haptic/pkg/dataplane/parser"
)

p, _ := parser.New()
current, _ := p.ParseFromString(currentHAProxyConfig)
desired, _ := p.ParseFromString(desiredHAProxyConfig)

comp := comparator.New()
diff, err := comp.Compare(current, desired)
if err != nil {
    return err
}

if !diff.Summary.HasChanges() {
    return nil // nothing to do
}

for _, op := range diff.Operations {
    fmt.Printf("[%s] %s\n", op.Type(), op.Describe())
}
```

`Compare` returns a `*ConfigDiff` whose `Operations` slice is already ordered for execution (sections first, child resources after, with priority numbers from each section's comparator). The `Summary` carries pre-aggregated counters (`TotalCreates`, `TotalUpdates`, `TotalDeletes`, plus per-section booleans like `GlobalChanged`) — handy for log lines and the `RawPushThreshold` decision in the orchestrator.

## What This Package Is Not

- **Not a parser.** Take a `*parser.StructuredConfig` from [`pkg/dataplane/parser`](../parser/).
- **Not an executor.** Operations describe *what* to do; running them inside a Dataplane API transaction is [`pkg/dataplane/synchronizer`](../synchronizer/)'s job.
- **Not a validator.** No semantic checks here; bad configs come in as parser errors before this package even runs.

## See Also

- [`pkg/dataplane`](../) — `Sync` / `DryRun` / `Diff` entry points that wrap this comparator
- [`pkg/dataplane/comparator/sections`](./sections/) — section-specific comparison logic (one file per HAProxy section type)
- `pkg/dataplane/CLAUDE.md` — comparator design notes, adding a new section comparator

## License

Apache-2.0 — see root `LICENSE`.
