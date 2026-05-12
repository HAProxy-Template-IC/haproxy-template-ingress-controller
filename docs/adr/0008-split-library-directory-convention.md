# ADR-0008: Template libraries may live as a directory of fragments

## Status

Accepted.

## Context

The chart's library system (ADR-0002) treats each library as a single
YAML file under `charts/haptic/libraries/`. The largest library,
`gateway.yaml`, has grown past 16 thousand lines (60 `templateSnippets`,
94 `validationTests`, plus `watchedResources` and `k8sResources`). At
that size:

- Searching, reviewing, and `git blame`-ing across unrelated concerns
  (route analysis, status patches, validation tests) becomes a constant
  context-switching tax.
- Any non-trivial edit produces a diff against a 16k-line file, making
  the change intent harder to read.
- Editor performance on the file degrades (Scriggo template syntax,
  YAML indentation, and embedded HAProxy fragments compound the cost).

The fixed `$libraryFiles` list in `_libraries.tpl` is fine for libraries
that fit in one file; it's the *forced one-file-ness* that is the
problem.

## Decision

Each entry in `$libraryFiles` may now be either:

- A **flat-file path** (the original convention): `"libraries/foo.yaml"`.
  Loaded via `Files.Get → fromYaml` exactly as before.
- A **split-library directory path** ending in `/`:
  `"libraries/foo/"`. The loader treats the directory as one logical
  library spread across multiple fragment files:

  1. `_index.yaml` is required and acts as the **load-rule authority**
     — it carries the `_helm_load` block (enable predicate, injects,
     unsets, and `_helm_skip_test` markers if any) and typically the
     small structural pieces (`watchedResources`, top-level metadata).
  2. All other YAML files at the top level of the directory, plus any
     YAML files one level deep (e.g. `tests/foo.yaml`), are
     **fragment files**. Fragments declare the same top-level keys as
     a flat library (`templateSnippets`, `validationTests`,
     `k8sResources`, …) but contribute a *subset* of entries.
  3. Fragments must NOT carry their own `_helm_load` block. The
     loader fails fast if any do.
  4. The loader collects all fragment paths, sorts them
     lexicographically (numeric prefixes like `10-`, `20-` are the
     idiomatic ordering hint), and `mustMergeOverwrite`s each fragment
     into the per-library accumulator before applying inject / unset /
     filterTests / strip / cross-library merge.

Fragment-to-fragment merge is by `mustMergeOverwrite`, so duplicate
top-level keys in two fragments would have the lexicographically-later
file win. The convention is: **don't duplicate**. Each `templateSnippets`
entry, each `validationTests` entry, and each `k8sResources` entry must
be declared in exactly one fragment.

## Consequences

- **Phase 1** (this ADR): the loader gains a small directory-marker
  branch. Existing flat-file libraries are unaffected — `helm template`
  produces byte-identical output before and after the loader change.
  Tracked separately by the chart validation suite plus a yq-normalised
  diff during PR review.
- **Phase 2** (separate work): `gateway.yaml` is the first library to
  adopt the directory layout, splitting into ~15 cohesive fragments
  (~110 to ~2400 lines each). Other libraries that grow past ~3000
  lines may follow.
- The `_helm_load` decentralisation invariant from ADR-0002 holds:
  load rules still live next to the library, just relocated to
  `_index.yaml` instead of the top of the (monolithic) library file.
- Reviewers reading `charts/haptic/libraries/` see a flat listing of
  library names — flat files alongside directory entries — that
  preserves the "merge order is a system property" property of
  ADR-0002.

## Constraints

- **Glob depth**: the loader expands top-level `*.yaml` and one-level-
  deep `*/*.yaml` only. Helm's `Files.Glob` uses Go's `path.Match`,
  which doesn't support `**`. Subdirectories beyond one level are
  ignored. The convention is one optional `tests/` subdirectory per
  split library; deeper nesting is not supported and not needed.
- **No fragment-local load rules**: fragments cannot opt out of
  loading individually. The whole library is enabled or disabled by
  `_index.yaml`'s `_helm_load.enable`.
- **Lexicographic order is the merge order**: fragments are
  numerically prefixed by convention (`10-features.yaml`,
  `20-route-analysis.yaml`, …) so the order is visible from the file
  listing.

## Do not re-suggest

A future reviewer may notice that a long fragment (say, a 2000-line
status-patch macro) "should" be split further into smaller fragments
that contribute pieces of the same template snippet. **Do not.**
Splitting *inside* one `templateSnippets` entry would require a
fragment-level merge of strings (template bodies), which
`mustMergeOverwrite` does not provide; the lexicographically-later
fragment would silently overwrite the earlier one. The split-library
mechanism splits the library by entry, not by entry-internals. If a
single template snippet is too large to read, refactor it into
multiple snippets that call each other — a separate concern from this
file-layout change.
