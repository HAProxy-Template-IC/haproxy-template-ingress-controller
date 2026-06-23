# HAProxy Config Generation

## ADDED Requirements

### Requirement: Pipeline Result Includes Status Patches

The Pipeline SHALL return status patches alongside the HAProxy configuration and auxiliary files in PipelineResult. The PipelineResult SHALL include a `StatusPatches` field containing the slice of StatusPatch entries collected during rendering. The Coordinator SHALL propagate status patches through `TemplateRenderedEvent` so that downstream components (StatusApplier) can access them. The ContentChecksum SHALL continue to cover only HAProxy config and auxiliary files (status patches are not part of the data plane content).

#### Scenario: Pipeline returns status patches

- **WHEN** the render phase completes and templates registered 5 status patches
- **THEN** the PipelineResult SHALL contain a `StatusPatches` slice with 5 entries

#### Scenario: TemplateRenderedEvent carries status patches

- **WHEN** the Coordinator publishes a TemplateRenderedEvent after successful pipeline execution
- **THEN** the event SHALL include the StatusPatches from the PipelineResult

#### Scenario: ContentChecksum excludes status patches

- **WHEN** the same HAProxy config and auxiliary files are produced but status patches differ (e.g., different observedGeneration)
- **THEN** the ContentChecksum SHALL remain the same (status patches do not affect deployment decisions)

### Requirement: Phase-Based Variant Selection in Coordinator

The Coordinator SHALL select the appropriate status patch variant based on pipeline outcome. On successful render (before deployment), the Coordinator SHALL make the `rendered` variants available. On successful deployment, the Coordinator SHALL signal the StatusApplier to apply `deployed` variants. On render failure, the Coordinator SHALL signal the StatusApplier to apply `renderFailed` variants from the last successfully rendered status patches. On deployment failure, the Coordinator SHALL signal the StatusApplier to apply `deployFailed` variants from the current render's status patches. The Coordinator SHALL cache the latest successfully rendered status patches for use in render failure scenarios.

#### Scenario: Render success triggers rendered variant

- **WHEN** the pipeline render phase succeeds
- **THEN** the Coordinator SHALL publish an event or signal causing the StatusApplier to apply `rendered` variants

#### Scenario: Deploy success triggers deployed variant

- **WHEN** deployment completes successfully
- **THEN** the Coordinator SHALL publish an event or signal causing the StatusApplier to apply `deployed` variants (superseding previously applied `rendered` variants)

#### Scenario: Render failure triggers renderFailed from cached patches

- **WHEN** the pipeline render phase fails and previous status patches are cached
- **THEN** the Coordinator SHALL signal the StatusApplier to apply `renderFailed` variants from the cached patches

#### Scenario: Deploy failure triggers deployFailed variant

- **WHEN** deployment fails and the current render produced status patches
- **THEN** the Coordinator SHALL signal the StatusApplier to apply `deployFailed` variants from the current render's patches
