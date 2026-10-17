# ADR-0031: Reuse auxiliary files by content identity

## Decision

A committed auxiliary set still consists of one atomic `HAProxyCfg` status
update containing every child reference. New publications mark their set ID
with `content-sha256:`. Each child name carries a SHA-256 digest of its kind,
full path, checksum of the original content, and CA role, followed by any
publication suffix. A separate name component scopes the object to its parent
name and UID. An unchanged child keeps its name and object across publications,
including its per-pod status.

Readers verify every referenced child's identity before accepting the complete
set. Map, general-file, and certificate-list contents are decompressed before
hashing. Secret watchers retain metadata only: the path and checksum annotations
bind the reference without retaining certificate or private-key bytes. The
publisher checks Secret data against the desired content on every authoritative
republish, as before. Older set-ID-bearing and legacy publications retain their
existing checks during migration.

## Cleanup and reuse

The existing publication fence remains before every deletion. A child that
was absent from the previously committed references receives a fresh claim
before the new references commit. A child already referenced keeps its claim.
Deletion uses the listed child's UID and resource version. If reuse races a
deletion, the new claim changes the resource version; cleanup must not adopt
that claim when retrying a conflict. If deletion wins first, publication must
create the child again before committing its references.

Each publication reads its children with one live, label-selected list per
kind before the first write. That list answers each child's first read and
supplies the deletion candidates; a conflict or `AlreadyExists` retry reads the
child live. A child created after the list is pruned by the next publication
(issue #356).

This closes the A-to-B-to-A reuse race without updating every unchanged child.
Status-only writes can still be retried because they preserve the claim. A
terminating child cannot satisfy publication. Owner checks and stamp-cache
invalidation on recreation remain in force.

## Per-pod status

Each pod applies its entry to the `HAProxyCfg` and every referenced child with
its own field manager. Between drift-prevention rounds a child apply identical
to the pod's last successful one is not sent (issue #163); the `HAProxyCfg`
entry is always applied. A drift-prevention round
reads the `HAProxyCfg` and one label-selected list per child kind live. It
re-applies an entry unless the object still shows exactly what that pod's last
identical apply left on it: the same UID, the same entry, and the same managed
fields for the pod's field manager. Any other write to them, such as a removed
or edited entry, a recreated object, or an ownership change, makes the round
re-apply. A failed or missing read re-applies unverified, and a new leader
re-applies everything once (issue #376).

## Validation delta

The atomic completeness check remains. For new publications, a checked digest
of the actual non-secret content, path, kind, and role replaces the child's
assertion that it belongs to a set. Secret metadata is bound to the committed
name instead of only matching a set annotation. Missing children, malformed
content, identity mismatches, and certificate/CA aliasing still reject the
whole candidate snapshot. Readers retain the previous complete snapshot while
a replacement is incomplete.

The publisher's up-to-date, ownership, and deletion-state checks run on listed
objects. A list without a `resourceVersion` is a quorum read like a GET and
returns the same fields, including Secret data, so those checks keep their
strength.

A drift-prevention round still re-applies, in that round, every per-pod status
entry whose object no longer shows the pod's last identical apply. Its reads are
quorum reads taken after the pod's drift result, and an apply it skips would
have stored nothing.

After accepting the new format, a reader rejects a downgrade to older identity
rules until a new-format publication arrives or that reader restarts. During a
rolling upgrade, old replicas keep their last complete snapshot until replaced.

## Consequences

One changed map creates and prunes only that map. Other children and their pod
status no longer churn with the set ID. Authoritative refresh still checks all
children and repairs externally changed or deleted resources. The protocol uses
existing names, annotations, and status fields; no CRD schema change is needed.

Tracked in [issue #285](https://github.com/haproxy-haptic/HAPTIC/issues/285).
