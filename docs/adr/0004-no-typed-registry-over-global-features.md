# ADR-0004: No typed registry over `globalFeatures` while it stays small

## Status

Accepted.

## Context

`globalFeatures` (alias `gf`) is an untyped `map[string]any` used for cross-library shared state during template rendering. As of writing, it holds four keys: `tlsCertificates`, `sslPassthroughBackends`, `sslRedirectHosts`, `addresses`. Each library that touches a key independently nil-checks and lazy-initialises it; a typo silently splits data into two slots; key conventions live only in `charts/CLAUDE.md` as documentation.

A typed registry — one declaration site for all keys plus typed accessor macros (`GFAppend` / `GFRead`) — would surface typos at render time and provide a self-documenting list of cross-library state. The interface to the cross-library channel would deepen.

## Decision

Do not introduce a registry at this time. Four keys, no recorded incident of a typo bug, manageable nil-init boilerplate. The friction does not warrant the new module.

## Consequences

- Cross-library state continues to use the existing `gf["key"]` convention with manual nil-init by the libraries that read each key.
- New keys must follow the camelCase convention documented in `charts/CLAUDE.md`. Reviewers must call out new keys in code review.
- This decision is explicitly contingent on scale.

## Do not re-suggest

A future architecture review may notice the untyped map and propose a typed registry "for safety and documentation." **Do not — yet.** The trigger conditions for revisiting are concrete: `globalFeatures` grows to roughly 6–8 keys, **or** a typo-induced silent failure is observed in production or tests. Without one of those, the registry is a module that adds machinery for risks that have not materialised. If you propose this change, the proposal must cite the trigger.
