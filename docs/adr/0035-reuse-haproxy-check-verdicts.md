# ADR-0035: Reuse `haproxy -c` verdicts for identical input sets

## Status

Accepted 2026-10-16. Amends ADR-0032's "does not reuse a validation verdict".

## Problem

Since ADR-0032 every reconcile render passes input acceptance, a synchronous
`haproxy -c -dr`. The leader's render gate (ADR-0022) then checks the same
render again, with the same flags and the same file layout. Both checks read
identical bytes, so the leader ran two `haproxy -c` processes per render. On a
fleet with about 12,800 watched objects and 74 renders per minute, those
processes cost more CPU than the controller itself (issue #335).

## Decision

The `-dr` validation services of one controller iteration (input acceptance,
HTTP content, render gate, retained recovery) share a `dataplane.CheckVerdicts`
store. Before running `haproxy -c`, the check computes a SHA-256 over:

- the executor installed in the process and the `haproxy` binary's path, size,
  and modification time
- the command line, including `-dr`
- the controller's environment, which HAProxy expands in `${VAR}` and `.if`
- the layout of the validation tree
- the config, split at each occurrence of the tree's own name
- every auxiliary file's path within the tree and its content, in write order

When the store holds a verdict for that key, the check returns it instead of
starting a process. Only passes and HAProxy refusals are stored. A check that
couldn't run, or was cancelled, is never stored and runs again next time.

The check computes the key itself; a caller can't supply one.
The render's own content checksum, a truncated 64-bit digest, never selects a
verdict.

## Validation delta

Every check still validates the exact bytes it's given. A verdict is reused only
when the inputs HAProxy reads are byte-identical to inputs HAProxy judged earlier
in the same process, with the same binary and flags. `haproxy -c` gives the same
answer for identical inputs, so the reused verdict is the one a new process would
return. No check is removed for any deployable output. The render gate still
judges every render, and an output it didn't receive from input acceptance still
runs `haproxy -c`.

The key doesn't cover every input:

- **DNS.** A strict check (no `-dr`) resolves server names, so its answer
  depends on time. Admission and validation tests run strict checks and never
  use the store.
- **Files outside the validation tree.** A config that names an absolute path
  reads the controller's own filesystem, which isn't what an HAProxy pod reads.
  Such a path already escapes validation's isolation. The controller image is
  immutable, so its files don't change between two checks in one process.
- **A validation tree with leftover files.** Validation creates a fresh tree for
  each check. A check over a tree that isn't empty always runs.

Followers keep their input acceptance check. Its verdict decides which inputs the
follower accepts, and admission and a later failover use that accepted state, so
the follower's check isn't redundant. ADR-0036 limits how often it runs.

`haptic_haproxy_checks_total{validator,result}` counts each `haproxy -c` run on
every replica, including followers. `haptic_haproxy_checks_reused_total` counts
the runs a stored verdict answered.
