# Scriggo fork maintenance

HAPTIC renders operator-authored templates through a fork of Scriggo,
`gitlab.com/haproxy-haptic/scriggo`. The fork is a long-lived component, not a
temporary patch: it carries a large, HAPTIC-specific feature that upstream is
unlikely to accept, so it needs a documented, repeatable maintenance process.
This page records why the fork exists, how far it has drifted from upstream, and
the process for keeping it current and secure. It's the maintenance counterpart
to the sandbox posture and fork notes in
[`pkg/templating/README.md`](https://gitlab.com/haproxy-haptic/haptic/-/blob/main/pkg/templating/README.md).

Origin: [issue #143](https://gitlab.com/haproxy-haptic/haptic/-/issues/143), an
architecture review of the forked runtime as a component.

## Why the fork exists

The fork adds parallel template rendering — the expression-form constructs
`{{ go Macro(...) }}`, `go render`, and `render_glob` — which the bundled chart
uses to shard backend rendering across goroutines. Upstream Scriggo has no
equivalent, and the feature touches the compiler, the emitter, and the virtual
machine, so it can't be carried as a small out-of-tree patch.

Parallel rendering is separate from the `go` (goroutine) *statement*. HAPTIC sets
`AllowGoStmt: false` in
[`pkg/templating/engine_scriggo.go`](https://gitlab.com/haproxy-haptic/haptic/-/blob/main/pkg/templating/engine_scriggo.go),
which disables the statement form `{% go f() %}` for operator templates. The
expression form above compiles to a different node (`OpGoRender`) that
`AllowGoStmt` doesn't gate, so parallel rendering stays available with the
statement form off. The full sandbox posture — what template execution can and
can't reach — lives in the
[sandbox posture table](https://gitlab.com/haproxy-haptic/haptic/-/blob/main/pkg/templating/README.md#sandbox-posture)
in `pkg/templating/README.md`. Keep that table and this page consistent.

## Divergence from upstream

Sizing the drift needs the recorded fork point below plus network access to both
remotes. Re-measure it at each sync and update these numbers. The figures below
are from the [#143 audit](https://gitlab.com/haproxy-haptic/haptic/-/issues/143)
(2026-08-22):

- **Fork point:** upstream [`open2b/scriggo`](https://github.com/open2b/scriggo)
  `main` at commit `ceb639fd` (2025-12-01).
- **Ahead:** the fork is 255 commits ahead of that point.
- **Behind:** upstream has advanced 59 commits that the fork hasn't merged.
- **Net diff** (fork point to fork `HEAD`, excluding the vendor tree and
  `compare` test fixtures): 160 files changed, 18,771 insertions, 1,816
  deletions.

The divergence is concentrated in the parallel-render feature — the code upstream
never reviews and HAPTIC depends on most:

| Area | Location | Roughly | What lives there |
|---|---|---|---|
| Runtime | `internal/runtime/run.go` | +1,338 | `OpGoRender`, `OpGoRenderIndirect`, `OpGoRenderWait` |
| Virtual machine | `internal/runtime/vm.go` | +1,117 | parallel machinery, pooled VM context |
| Renderer | `renderer.go` | +690 | render futures and segments |
| Emitter | emitter parallel paths | — | `OpGoRender` emission, `AllowGoStmt` gating |
| Wildcard access | `for range` `[*]` | +293 | wildcard slice access in for-range |
| Pipeline lowering | typed-collection lowering, checker | +312 / +1,054 | typed-collection pipeline desugaring |

Secondary changes cover tooling and builtins. When a sync forces a re-review of
this code, sweep the parallel-render paths for the class of bug
[#169](https://gitlab.com/haproxy-haptic/haptic/-/issues/169) found: an `int8`
slice index that truncates silently past 256 targets. That fix bounded the index
with a build-time limit error and swept the adjacent `NativeFunctions`
occurrence; re-check every `int8(len(slice))` index in `emitter_func_store.go`
and `builder*.go` against its guard.

## Upstream sync cadence

Merge upstream `open2b/scriggo` on a **quarterly** sweep, and **out of band**
whenever upstream publishes a security advisory (see
[Security advisory watch](#security-advisory-watch)). Prioritize correctness and
security fixes over feature parity.

Run these steps in a clone of the fork:

1. Add the upstream remote (once per clone).

   ```bash
   git remote add upstream https://github.com/open2b/scriggo.git
   git fetch upstream
   ```

2. Merge upstream into the fork's default branch and resolve conflicts. Conflicts
   cluster in the diverged files above (`run.go`, `vm.go`, `renderer.go`, the
   emitter), so review those against the divergence map rather than accepting
   either side wholesale.

3. Run the fork's full test suite with the race detector.

   ```bash
   go test -race ./...
   ```

4. Push the merge. Merging to the fork's default branch requires the fork's own
   CI to be green — see [How HAPTIC pins the fork](#how-haptic-pins-the-fork).

5. Re-pin HAPTIC to the new fork commit. Because the fork ships as a
   pseudo-version with no tag, this is a `go get` of the fork at the new commit,
   which rewrites the pseudo-version in `go.mod` and the checksum in `go.sum`.

   ```bash
   go get gitlab.com/haproxy-haptic/scriggo@<new-commit>
   go mod tidy
   ```

6. Run the HAPTIC template tests, including the race detector and the benchmarks,
   and add a regression test for anything the sync fixed. The re-pin for #169
   ([`!1686`](https://gitlab.com/haproxy-haptic/haptic/-/merge_requests/1686)) is
   the reference shape: a `go.mod`/`go.sum` bump plus a regression test
   (`pkg/templating/gorender_limit_test.go`) and a changelog line.

## Security advisory watch

Renovate surfaces a new fork commit as a pseudo-version (digest) bump, but a
digest bump carries no security meaning — it's just a newer commit. Nothing in
HAPTIC's tooling links the fork back to upstream Scriggo advisories, because
`govulncheck` keys on the module path: an advisory filed against
`github.com/open2b/scriggo` never matches `gitlab.com/haproxy-haptic/scriggo`.
The upstream watch is therefore separate and manual:

- Subscribe to `open2b/scriggo`
  [releases](https://github.com/open2b/scriggo/releases) (on GitHub: **Watch →
  Custom → Releases**).
- Subscribe to the repository's
  [security advisories](https://github.com/open2b/scriggo/security/advisories).
- On any advisory that touches the compiler, the VM, or template execution,
  trigger an out-of-band sync instead of waiting for the quarterly sweep. An
  advisory is a CVE-class signal that the digest watch alone can't provide.

## How HAPTIC pins the fork

HAPTIC consumes the fork as a normal `require` in `go.mod` — no `replace`
directive — pinned to a pseudo-version. Read the live pin from `go.mod` rather
than copying a value out of this page; it drifts as the fork advances.

```bash
grep gitlab.com/haproxy-haptic/scriggo go.mod
```

- **Renovate tracks the pin.** The built-in Go-module manager proposes a digest
  update whenever the fork's default branch advances — the recurring
  `renovate/…-scriggo-digest` merge requests. A `packageRule` in
  [`renovate.json`](https://gitlab.com/haproxy-haptic/haptic/-/blob/main/renovate.json)
  marks these updates for human review rather than auto-merge, because a fork
  bump can carry an upstream security fix that a reviewer must recognize and note
  against the [security advisory watch](#security-advisory-watch).
- **The fork's CI gates the merge.** A sync merges to the fork only when the
  fork's own pipeline is green. That pipeline runs on GitLab shared runners, so
  shared-runner availability for the `haproxy-haptic/scriggo` project is an
  operational dependency of every sync: a runner outage blocks taking an upstream
  security fix.

## Ownership and cadence

The fork belongs to the HAPTIC maintainer, so naming a fork-maintenance owner is
a human decision. Confirm an owner as a team, and adopt (or adjust) the cadence
above — a quarterly sweep plus advisory-triggered out-of-band syncs. Record the
decision here alongside the divergence numbers at the first sync.
