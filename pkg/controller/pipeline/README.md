# pkg/controller/pipeline

Synchronous rendering, validation, and external-input acceptance. The pipeline
has no event-bus dependency; callers provide a resource store and render mode.

## Validation ownership

| Caller | Synchronous checks | HAProxy render gate |
|--------|--------------------|---------------------|
| Admission and HTTP-store proposals | HAProxy and configured output validators | Not needed for the proposal verdict |
| Leader reconciliation | Configured output validators; full validation before accepting new HTTP inputs | Checks the deployed plan asynchronously |
| Follower warmer | Full validation before accepting new HTTP inputs | The leader owns deployment checks |

`PipelineConfig.Validator` enables synchronous HAProxy checks.
`OutputValidator` checks auxiliary formats. `CommitValidator` gates the acceptance
of new external inputs when the normal synchronous validator is absent. Only
`Renderer` is required; `New` panics if it's nil.

## Usage

Given configured rendering and validation services:

```go
import (
    "gitlab.com/haproxy-haptic/haptic/pkg/controller/pipeline"
    "gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
)

pl := pipeline.New(&pipeline.PipelineConfig{
    Renderer:        renderSvc,
    Validator:       validateSvc,
    OutputValidator: pluggableValidators,
    Logger:          logger,
})

result, err := pl.Execute(ctx, storeProvider, rendercontext.RenderModeAdmission)
```

`ExecuteWithResult` also returns the validation result, including phase details
and warnings on failure. Render mode is an explicit argument; an overlay store
doesn't choose it for the caller.

## Results and errors

`PipelineResult` carries authenticated snapshots of the configuration, plan,
auxiliary files, status patches, Events, and owned resources, together with
`PlanID`, `ContentChecksum`, and phase timings. Mutable compatibility fields stay
nil in production; use the `Materialize*` methods for detached copies.

Failures return `*PipelineError` with `Phase` (`render` or `validation`) and a
validation subphase when applicable. Use `errors.As` to inspect it. Cancellation
is checked between phases and before success and preserves its cause.

Checks invoked by this pipeline run synchronously. The separate render gate
tracks HAProxy verdicts by plan identity; see
[ADR-0022](../../../docs/adr/0022-haptic-agent.md).

## Related packages

- [`renderer`](../renderer/) — produces the configuration and immutable render result
- [`validation`](../validation/) — runs HAProxy checks
- [`pluggablevalidator`](../pluggablevalidator/) — checks auxiliary formats
- [`proposalvalidator`](../proposalvalidator/) — validates proposed inputs
- [`rendergate`](../rendergate/) — leader-side HAProxy verdict tracking
