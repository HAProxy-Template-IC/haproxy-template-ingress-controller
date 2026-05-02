# ADR-0005: Path matching order is a values flag, not a library variant

## Status

Accepted.

## Context

The chart formerly shipped a `libraries/path-regex-last.yaml` library whose sole purpose was to override `base.yaml`'s `frontend-routing-logic` snippet so regex path matching ran last instead of in its default position. The library overrode exactly one snippet (38 lines of substantive HAProxy config) and contributed only that variation; the variants differed in 4 lines of `http-request set-var`.

Around this single override sat the full library scaffold: a top-level entry in the merge order, an enable flag in `values.yaml`, a load block in `_helpers.tpl`'s merge function, a dedicated documentation page, a row in the library hierarchy diagram. The deletion test fired cleanly: removing the library and replacing it with a values flag concentrated nothing — it simply removed scaffolding that exceeded the behaviour it carried.

## Decision

Demote the variant from a library to a values flag. The choice is expressed as `controller.config.routing.regexMatchOrder` with values `default` (current order) or `last` (regex evaluated after exact and prefix matches), consumed inside `base.yaml`'s `frontend-routing-logic` snippet via a small conditional around the path-matching `set-var` statements.

Delete `libraries/path-regex-last.yaml`, its load entry in `_helpers.tpl`, the `controller.templateLibraries.pathRegexLast.enabled` flag, and `docs/libraries/path-regex-last.md`. Document the migration (renamed flag, removed library) in `charts/haptic/CHANGELOG.md`.

## Consequences

- One library disappears from the merge order and the hierarchy diagram. Operators using the previous flag must rename it on upgrade — a breaking change tolerable at the chart's current alpha versioning.
- The path-matching-order decision lives next to the path-matching code, not one library-merge hop away.
- Future routing-strategy alternatives are added as additional `if` branches or as `regexMatchOrder` enum values until there is a concrete second strategy that needs to layer in via the plugin pattern.

## Do not re-suggest

A future reviewer may notice that path matching order is configured via a values flag and propose extracting the variants back into separate libraries "to use the plugin pattern consistently." **Do not — yet.** The plugin pattern earns its keep when there are multiple distinct implementations to swap; with one variant of one decision, the scaffold costs more than the behaviour it carries. **If a second concrete routing-strategy variant emerges (a distinct algorithm, not just another values toggle), reopen this ADR and reintroduce the library family with both members landing together.**
