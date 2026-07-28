# ADR-0002: Helm library load rules are decentralized

## Status

Accepted. **Partly superseded by ADR-0014**: the decentralized `_helm_load`
loader described here is unchanged and remains the convention, but the chart no
longer merges the libraries into one object. `haptic.mergeLibraries` is now
`haptic.prepareLibraries`, which returns the libraries prepared but separate;
each is rendered as its own `HAProxyTemplateConfig` and the controller merges the
set. Read the loader pipeline below as ending at "strip `_helm_load`" rather than
at `mustMergeOverwrite`.

## Context

The chart merges nine template-library YAML files (`charts/haptic/libraries/*.yaml`) into a single HAProxyTemplateConfig at Helm render time. Each library may need per-library mutations applied after `Files.Get → fromYaml` but before merge: dynamic `fieldSelector` / `labelSelector` injection (driven by chart values like `ingressClass.name`) and conditional test filtering. Historically these mutations lived as nine open-coded blocks inside `_helpers.tpl`'s `haptic.mergeLibraries`, growing whenever a new library needed special treatment.

The shape was shallow: the *interface* to "load a library" was "edit a 100-line function and remember which libraries need which ceremony." Adding a library required picking the right pattern from prior art and replicating it; deleting a library required identifying every block that referred to it.

## Decision

Each library file carries a top-level `_helm_load:` metadata block (sibling to `templateSnippets`, `watchedResources`, etc.) declaring its own enable predicate and any injections it needs. `_helpers.tpl` keeps only the ordered list of library file paths and a single generic loader: `Files.Get → fromYaml → eval _helm_load.enable → apply _helm_load.inject items → apply haptic.filterTests universally → strip _helm_load → mustMergeOverwrite`.

The merge order (a system property, not a library-internal property) stays centralised. The mutations (a library-internal concern) move next to the resources they parameterize. `_helm_load` joins `_helm_skip_test` as the established convention for chart-time-only metadata; both are stripped before the merged config is rendered.

## Consequences

- Adding a library is a new file plus one entry in the ordered list — not a new branch in the merge function.
- The rule "ingress filters by `ingressClassName`" lives in `ingress.yaml`, next to `watchedResources.ingresses`, instead of in a far-away helpers file.
- `haptic.filterTests` is applied to every library unconditionally (it is a no-op for libraries without `_helm_skip_test`). The previous per-library decision was an unnecessary special case.
- `_helpers.tpl`'s merge function shrinks from ~100 lines to ~15.

## Do not re-suggest

A future reviewer may notice that loading rules are scattered across nine library files and propose centralising them back in `_helpers.tpl` "for visibility, so the whole loading contract is in one place." **Do not.** The visibility argument is real but locality wins: each library declares the rules for the resources it ships, and `_helpers.tpl` stays unaware of library internals. If a centralised view is needed for documentation or onboarding, generate one from the `_helm_load` blocks rather than relocating them.
