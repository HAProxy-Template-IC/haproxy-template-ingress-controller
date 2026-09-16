# pkg/controller/proposalvalidator

Validates hypothetical configuration changes by rendering live stores with temporary overlays. `Service` owns validation; `Component` connects it to proposal events.

## Validation policies

| Caller | Entry point | Failure policy |
|--------|-------------|----------------|
| Admission webhook | `Service.ValidateSyncWithAdmissionSubject` | May admit unchanged invalid output after an exact comparison with the live baseline |
| Background HTTP refresh | `Component` handling `ProposalValidationRequestedEvent` | Rejects invalid proposed output, including output identical to an invalid baseline |

Cancellation and unavailable published files reject admission. Neither can use the unchanged-invalid exception. `CurrentFilesProvider` supplies one snapshot per decision, shared by the proposed and baseline renders.

## Admission service

```go
service := proposalvalidator.NewService(&proposalvalidator.ServiceConfig{
    Pipeline:             admissionPipeline,
    BaseStoreProvider:    storeProvider,
    CurrentFilesProvider: currentFilesProvider,
    Logger:               logger,
})
pipelineResult, result := service.ValidateSync(ctx, overlays)
```

The service has no event subscription or lifecycle. `ValidateSync` returns `(*pipeline.PipelineResult, *validation.ValidationResult)`. An admitted result includes the proposed rendered output, including when admission uses the unchanged-invalid exception. Rejections include the failing phase, error, and any warnings.

## HTTP-content event adapter

```go
adapter := proposalvalidator.New(eventBus, &proposalvalidator.ServiceConfig{
    Pipeline:             proposalPipeline,
    BaseStoreProvider:    storeProvider,
    CurrentFilesProvider: currentFilesProvider,
    Logger:               logger,
})
eventBus.Start()
go adapter.Start(ctx)
```

Construct every subscriber before starting the event bus. The adapter publishes a verdict carrying the request ID, including when validation panics. Cancelling its lifecycle context cancels active validation.

## Related packages

- [`pipeline`](../pipeline/) — render and validation execution
- [`dryrunvalidator`](../dryrunvalidator/) — admission overlays and error reporting
- [`httpstore`](../httpstore/) — pending HTTP-content promotion
- [`stores`](../../stores/) — temporary resource and HTTP overlays
