# config-publishing Specification

## Purpose

Defines how the controller makes rendered HAProxy configuration observable inside the cluster: the leader-only ConfigPublisher component that writes the rendered config and auxiliary files as output CRDs after validation (throttled, deduplicated, deploy-aware), and the shapes of those CRDs — HAProxyCfg, HAProxyMapFile, HAProxyGeneralFile, and HAProxyCRTListFile. These resources exist for kubectl inspection, GitOps diffs, and audit; they are read-only observability artifacts, NOT the path by which configuration reaches HAProxy pods (that is deployment-scheduling plus dataplane-sync).

## Requirements

### Requirement: Post-Validation Publishing

The leader-only ConfigPublisher SHALL publish the rendered configuration and its auxiliary files as output CRDs only after successful validation. Rendered configs SHALL be cached keyed by correlation ID so each ValidationCompletedEvent is matched to exactly the TemplateRenderedEvent of its own reconciliation cycle, even when events from multiple cycles interleave; an event whose correlation ID has no cached render (or arriving before the template config is cached) SHALL be skipped with a warning. Kubernetes API work SHALL run on async workers so the event loop never blocks on the API: publish operations under a 30-second timeout, per-pod status operations under a 10-second timeout. Before its first API mutation, the pure publisher SHALL canonicalize identical auxiliary definitions and reject conflicting definitions of one Dataplane API storage identity. A publication is successful only after the HAProxyCfg, every required auxiliary resource, the HAProxyCfg's complete auxiliary-reference status, and removal of obsolete owned auxiliary resources have succeeded. Cleanup SHALL begin only after the complete desired set and its references exist. The parent and every desired child SHALL carry the same auxiliary-set identity; cleanup SHALL recheck both that identity and the committed auxiliary references before each deletion and use object-version preconditions, so a late cleanup from a retired desired set cannot delete a replacement publication. An existing auxiliary name managed by another HAProxyCfg SHALL never be taken over; the publisher SHALL use a stable owner-scoped name instead. An incomplete publication SHALL retry its immutable request with bounded exponential backoff until it completes, its validation generation is superseded, or its leader lifecycle context is cancelled. Superseding a generation SHALL interrupt its pending backoff immediately. A permanent API rejection SHALL terminate only that work item so later publications can proceed. After a successful API call, the publisher SHALL recheck the validation generation and leader term before atomically recording the checksum and emitting ConfigPublishedEvent; a retired call SHALL do neither. The HAProxyCfg resource name SHALL be derived deterministically from the template-config name (suffix "-haproxycfg") and SHALL remain a valid Kubernetes name for every valid template-config name.

#### Scenario: Interleaved cycles publish the matching render

- **WHEN** renders A and B are cached under distinct correlation IDs and B's ValidationCompletedEvent arrives first
- **THEN** the publisher SHALL publish render B's bytes, not render A's

#### Scenario: Validation completed without cached render

- **WHEN** a ValidationCompletedEvent arrives whose correlation ID has no cached rendered config
- **THEN** the publisher SHALL log a warning and publish nothing

#### Scenario: Auxiliary write fails transiently

- **WHEN** the HAProxyCfg write succeeds but one required auxiliary write fails
- **THEN** the publisher SHALL emit no ConfigPublishedEvent and SHALL retry the same generation until the child and its status reference exist

#### Scenario: Ambiguous auxiliary set reaches the pure publisher

- **WHEN** two requested files collapse to one Dataplane API storage identity with conflicting definitions
- **THEN** the publisher SHALL reject the request without creating or updating the HAProxyCfg

#### Scenario: Auxiliary resource leaves the desired set

- **WHEN** a complete desired set and its references no longer include a child owned by the HAProxyCfg
- **THEN** the publisher SHALL delete that child before reporting publication success

#### Scenario: Retired cleanup reaches a newer desired set

- **WHEN** a late cleanup observes that the HAProxyCfg now carries a different auxiliary-set identity
- **THEN** it SHALL delete nothing and SHALL NOT report the retired publication as complete

#### Scenario: Another runtime config owns the readable child name

- **WHEN** an auxiliary resource's readable name already belongs to a different HAProxyCfg
- **THEN** the publisher SHALL retain the existing resource and publish its own child under a stable owner-scoped name

#### Scenario: New generation supersedes an incomplete validation publish

- **WHEN** validation generation B arrives while generation A waits to retry an incomplete publication
- **THEN** generation A SHALL stop retrying and generation B SHALL publish from its own immutable snapshot

#### Scenario: Successful API call returns after authority expires

- **WHEN** a publish API call succeeds after its validation generation is superseded or its leader context is cancelled
- **THEN** it SHALL NOT advance content deduplication or emit ConfigPublishedEvent

#### Scenario: Permanent rejection does not starve the queue

- **WHEN** one publication receives a permanent Kubernetes API rejection
- **THEN** that item SHALL stop without success and the next queued publication SHALL run

### Requirement: Deploy-Driven Publishing

A DeployedConfigPublishRequest — carrying inline the exact bytes and content checksum a deployment just applied — SHALL be processed through a dedicated ordered queue and a dedicated pending-throttle path, separate from validation-driven publishes, so a validation publish can never coalesce away a deployed checksum. A deploy-driven item SHALL NOT supersede a validation generation. Each throttle window SHALL publish at most one item, and the complete deployed queue SHALL be flushed in arrival order before the latest buffered validation publish. This guarantees every checksum stamped into per-pod status is observable as a published spec checksum even when the validation-driven publish for that render was throttled or coalesced away. Buffered deploy-driven publishes SHALL be dropped on lost leadership.

#### Scenario: Deployed checksum survives coalescing

- **WHEN** a deploy-driven publish and a newer validation-driven publish are both buffered inside a throttle window
- **THEN** all queued deploy-driven items SHALL be flushed one per window before the validation-driven item

### Requirement: Dual Leading-Edge Throttles

The publisher SHALL gate spec writes and status-subresource writes through two SEPARATE leading-edge throttles, both at the config-publish interval (default 10 seconds; the value 0 disables throttling). Leading-edge semantics: the first write after an idle period fires immediately; writes submitted inside the refractory window are buffered (latest wins per slot for spec writes, per-pod coalescing for status writes) and flushed once when the window expires. Status writes need their own throttle because each status update writes the full object to etcd even though only the status changed. On lost leadership or shutdown, buffered work SHALL be discarded and no API write SHALL outlive the cancelled lifecycle context. A later leadership term SHALL use fresh queues, throttles, retry scheduling, and readiness signalling.

#### Scenario: First publish after idle is immediate

- **WHEN** a publish is submitted and no publish fired within the last interval
- **THEN** it SHALL execute immediately

#### Scenario: Burst collapses to one write per window

- **WHEN** five renders publish within one 10 s window
- **THEN** the first SHALL fire immediately and the remaining four SHALL collapse to a single flush of the latest when the window expires

#### Scenario: Leadership is reacquired

- **WHEN** the component starts again after a completed leader term
- **THEN** it SHALL signal readiness for the new subscription and publish through fresh worker timing state

### Requirement: Content Deduplication

The publisher SHALL fast-skip a deploy-driven publish when its content checksum equals the checksum of the last completely published config, dropping the consumed render cache entry. A validation-driven repeat SHALL reconcile the complete desired resource set so a deleted or drifted output heals even when its content checksum is unchanged; an already-correct set SHALL produce no Kubernetes write or duplicate ConfigPublishedEvent. A partial parent or child write SHALL NOT advance the checksum. An empty checksum SHALL never match. The deploy-driven check SHALL run both before throttle buffering and again at flush time. The last-published checksum SHALL be cleared on lost leadership.

#### Scenario: Identical content is not republished

- **WHEN** a deploy-driven publish carries the same content checksum as the last successful publish
- **THEN** no Kubernetes API write SHALL occur

#### Scenario: Same-checksum output is incomplete

- **WHEN** a validation-driven repeat finds a required output resource missing or drifted
- **THEN** it SHALL repair the desired state without emitting a duplicate completion event

### Requirement: Invalid-Config Publishing

When validation fails, the publisher SHALL publish the failed render as a separate HAProxyCfg under the runtime config name plus an "-invalid" suffix, with the status ValidationError field set to a summary of the validation errors (first error plus a count of the rest). Its auxiliary resources SHALL use the same suffix so an invalid render cannot overwrite the last valid render's resources. Distinct auxiliary file identities that produce the same readable Kubernetes name SHALL receive stable disambiguated names rather than overwrite one another. Invalid configs SHALL never be deployed; the -invalid resource exists so operators can inspect exactly what was rejected and why.

#### Scenario: Failed render observable under -invalid name

- **WHEN** a render fails validation
- **THEN** an HAProxyCfg with the "-invalid" name suffix SHALL be published carrying the failed content and the validation error summary

### Requirement: Per-Pod Status Coalescing and Per-Pod Field Managers

Per-pod deployment status updates (from ConfigAppliedToPodEvent) SHALL coalesce in a pending map keyed by namespace/runtime-config-name/pod-name, so multiple updates for the same pod collapse to the newest. On flush the worker SHALL fan out one goroutine per pod, each popping the LATEST pending entry for its pod at the moment its apply starts, and each applying via Server-Side Apply under its own per-pod field manager (prefix "haptic-pod-status-" plus the pod name) so concurrent per-pod updates merge at the apiserver instead of conflicting.

#### Scenario: Rapid updates for one pod collapse

- **WHEN** three ConfigAppliedToPodEvents for the same pod arrive before the status worker runs
- **THEN** only the newest update SHALL be applied

#### Scenario: Distinct pods update concurrently

- **WHEN** status updates for several pods are pending
- **THEN** each pod's update SHALL be applied in its own goroutine under its own field manager

### Requirement: Status Requeue for Unpublished Configs

When a per-pod status update targets an HAProxyCfg that has not been published yet, the update SHALL be requeued and retried at 1-second intervals, up to 30 retries, after which it is dropped with a warning. A requeued item SHALL yield to any newer pending update for the same pod. This closes the startup race where the first deployment completes milliseconds before the initial HAProxyCfg publish lands.

#### Scenario: Startup race resolves via requeue

- **WHEN** a pod's first status update arrives before the initial HAProxyCfg publish
- **THEN** the update SHALL be requeued and applied once the resource exists

#### Scenario: Retry budget exhausts

- **WHEN** the target HAProxyCfg still does not exist after 30 retries
- **THEN** the update SHALL be dropped with a warning

### Requirement: Pod Lifecycle Reconciliation

Each deployedToPods entry SHALL carry the pod UID and container execution epoch that own its checksum proof. On HAProxyPodsDiscoveredEvent the publisher SHALL reconcile the deployedToPods status list against the currently authoritative pod identities, removing stale entries left by pods that terminated, restarted, or were replaced while the controller was down. On HAProxyPodTerminatedEvent it SHALL remove only the terminated UID's references, preserving a replacement that already reported status under the same name. A failed deployment SHALL preserve a prior checksum only when that checksum belongs to the same pod UID and container execution epoch; the first failure for a replacement or restart SHALL clear the predecessor's checksum. ConfigAppliedToPodEvent results from a UID or container execution epoch outside the current discovered fleet SHALL be ignored both when queued and immediately before the status write. On LostLeadershipEvent it SHALL clear all cached state: the template config, the rendered-config cache, the last-published checksum, endpoint authorities, and any buffered deploy-driven publish.

#### Scenario: Stale pod entries cleaned on discovery

- **WHEN** pods are discovered and the deployedToPods list contains an entry for a pod that no longer runs
- **THEN** that entry SHALL be removed

#### Scenario: Same-name replacement cannot inherit convergence

- **WHEN** a pod is replaced under the same namespace and name and its first deployment fails
- **THEN** deployedToPods SHALL identify the replacement UID with an empty checksum, and a late predecessor event SHALL NOT restore the predecessor's checksum

#### Scenario: In-place image replacement cannot inherit queued status

- **WHEN** a ConfigAppliedToPodEvent from the previous container execution epoch remains queued after the same pod UID is admitted with a new epoch
- **THEN** the publisher SHALL discard the queued event before writing status

#### Scenario: Lost leadership clears cached state

- **WHEN** the replica loses leadership
- **THEN** the template config, the rendered-config cache, and the last-published checksum SHALL be cleared

### Requirement: Output CRD Shapes

The output CRDs SHALL be read-only observability artifacts written only by the controller. HAProxyCfg spec SHALL carry the file path, the rendered content, a checksum, and a compressed flag; HAProxyMapFile, HAProxyGeneralFile, and HAProxyCRTListFile SHALL each carry their content or entries with the same checksum and compressed fields. The checksum format SHALL be "sha256:" followed by the hex digest of the UNCOMPRESSED content, so the checksum stays stable regardless of whether the stored content is compressed.

#### Scenario: Checksum computed over uncompressed content

- **WHEN** content is compressed for storage
- **THEN** the spec checksum SHALL still be the sha256 of the original uncompressed content

### Requirement: Content Compression

Output-CRD content SHALL be compressed as zstd wrapped in base64, governed by the compression threshold (CRD-configurable; default 1 MiB). A threshold of zero or below SHALL disable compression. Content SHALL be compressed only when it exceeds the threshold AND the compressed form is actually smaller than the original; otherwise the original content is stored with the compressed flag false. The compressed flag SHALL always agree with the stored content.

#### Scenario: Compression skipped when not beneficial

- **WHEN** content exceeds the threshold but compression does not reduce its size
- **THEN** the original content SHALL be stored with compressed=false

#### Scenario: Small content never compressed

- **WHEN** content is at or below the threshold
- **THEN** it SHALL be stored uncompressed

### Requirement: DeployedToPods Status Structure

HAProxyCfg status SHALL track per-pod deployment state in a DeployedToPods list declared listType=map keyed by podName, so each pod's Server-Side Apply lands as a merge of one map entry instead of last-write-wins on the whole list. Each entry SHALL carry the checksum deployed to that pod, the last sync error (cleared on success), and a consecutive-error counter (reset to zero on success). When every entry's checksum equals the published spec checksum, the fleet has converged on the current spec — this is the convergence signal operators and tests poll.

#### Scenario: Concurrent pod updates merge

- **WHEN** two pods' status updates apply concurrently under their per-pod field managers
- **THEN** both entries SHALL survive in DeployedToPods without overwriting each other

#### Scenario: Convergence observable via checksums

- **WHEN** all DeployedToPods entries carry the published spec checksum
- **THEN** an observer can conclude every pod serves the current configuration
