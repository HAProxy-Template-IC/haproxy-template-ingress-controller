// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package deployer

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

const (
	// runtimeBypassTimeout bounds a single endpoint's runtime-raw apply. The
	// apply is a fast path (one skip_reload raw push carrying the shared render
	// diff's runtime actions, ~1-20ms in practice); this ceiling only guards
	// against a hung dataplane API so a stuck goroutine can't outlive the
	// reconcile that spawned it.
	runtimeBypassTimeout = 5 * time.Second
)

var errRuntimeBypassClosed = errors.New("runtime bypass is closed")
var errRuntimeBypassAuthorityChanged = errors.New("runtime bypass endpoint authority changed")

// runtimeSyncer is the narrow slice of *dataplane.Client the bypass needs.
// Declared at the use site so tests can substitute a fake.
type runtimeSyncer interface {
	SyncRuntimeFast(ctx context.Context, updates *dataplane.RuntimeServerUpdates, body string, opts *dataplane.SyncOptions) (*dataplane.SyncResult, error)
	Close() error
}

// runtimeBypass applies runtime-eligible server changes (pod IP / port /
// admin-state) to each endpoint via a single skip_reload raw push that carries
// the precomputed render diff's runtime `set server` actions. It serves two
// callers: the pure runtime-raw lane dispatch (dispatchPending, `partial=false`,
// authoritative — it IS the complete deploy, so it publishes), and the
// fast-track apply of a pending render's runtime subset at either deploy-loop
// wait point (applyRuntimeSubset, `partial=true`). The `partial` flag suppresses
// the deploy-owning publishes (DeployedConfigPublishRequest /
// ConfigAppliedToPodEvent) — only the fast-path counter still
// fires — because whoever owns the deploy (the eventual authoritative dispatch,
// or an in-flight/just-completed structural deploy) publishes the CR/status.
//
// applyRuntimeRaw is SYNCHRONOUS (it waits for every endpoint). A partial apply may
// run CONCURRENTLY with a separate structural deploy's reload (awaitCompletion) or
// during its post-completion interval settle: a `set server` may briefly land on a
// worker the reload then replaces, but the push retries across the reload (master
// socket briefly down → connection refused / not found) onto the post-reload worker,
// and the next structural deploy re-renders the body WITH the new address — so the
// address is never permanently lost (config-driven; no server-state-file — ADR-0011).
// Overlapping an in-flight deploy is only safe because the caller patches that
// deploy's OWN dispatched config (see applyRuntimeSubset) — patching the older
// activated one rolls its disk write back (issue #84 mode A). Every failure here is
// swallowed to a debug log — the scheduled deploy converges the pod regardless.
//
// Clients are persistent per endpoint authority (see clientFor). Their HTTP
// requests use the dataplane package's process-wide keep-alive transport.
type runtimeBypass struct {
	logger    *slog.Logger
	newSyncer func(ctx context.Context, ep *dataplane.Endpoint) (runtimeSyncer, error)
	eventBus  *busevents.EventBus // publishes ConfigAppliedToPodEvent for a complete (pure runtime-raw lane) apply; nil in tests

	// recordFastPath reports one fast-path fire to the metrics registry. A
	// direct call rather than an event: the only subscriber was the metrics
	// component, and it only incremented a counter (ADR-0001). Nil in tests.
	recordFastPath func(serverUpdates int, failed bool)

	// recordActivation reports what an apply proved about the endpoint's running
	// config: the checksum on success, "" to clear the proof. Clearing on
	// failure is load-bearing — a skip_version push writes its body to disk even
	// when the runtime actions fail, so a failed apply leaves content on disk
	// that no worker ever loaded. Leaving a stale proof behind would let the
	// next sync short-circuit an empty diff over that parked content, which is
	// the #112 stall. Nil in tests.
	recordActivation func(endpoint *dataplane.Endpoint, proof string)

	// retainAuthorities evicts structural-sync observations for retired
	// endpoint authorities. Nil in isolated tests.
	retainAuthorities func([]dataplane.Endpoint)

	mu             sync.Mutex
	clients        map[endpointAuthority]runtimeSyncer
	authorities    map[endpointAuthority]struct{}
	authoritiesSet bool
	closed         bool
	epoch          uint64
}

// newRuntimeBypass builds a runtimeBypass that opens real dataplane clients.
func newRuntimeBypass(logger *slog.Logger, eventBus *busevents.EventBus) *runtimeBypass {
	return &runtimeBypass{
		logger:   logger.With("path", "runtime-bypass"),
		eventBus: eventBus,
		newSyncer: func(ctx context.Context, ep *dataplane.Endpoint) (runtimeSyncer, error) {
			return dataplane.NewClient(ctx, ep)
		},
		clients:     make(map[endpointAuthority]runtimeSyncer),
		authorities: make(map[endpointAuthority]struct{}),
	}
}

type runtimeAuthorityLease struct {
	epoch uint64
}

// bypassPush bundles the per-apply parameters shared by every endpoint of one
// applyRuntimeRaw call.
type bypassPush struct {
	// body is the config the skip_version push writes to disk. It MUST be
	// derived from the last reload-ACTIVATED config (issue #84): the
	// authoritative runtime-raw lane passes the render itself (structurally
	// identical to the activated baseline by lane construction); the partial
	// fast-track apply passes the baseline patched with only the
	// runtime-eligible server lines (BuildRuntimeBypassBody) — never the
	// pending render, whose structural content would clobber a concurrent
	// force_reload deploy's disk write or park un-activated on disk.
	body string
	// partial marks a fast-track apply whose deploy is owned by someone else —
	// the eventual authoritative dispatch of this pending, or an
	// in-flight/just-completed structural deploy (applyRuntimeSubset): it
	// suppresses the deploy-owning publishes so that owner remains the single
	// authority for completion + CR status.
	partial bool
	// superseded (nil = never) reports that a newer render now exists; the
	// push's retry-across-reload loop abandons when it returns true so a
	// superseded body can't storm identical pushes across a reload window.
	superseded func() bool
	// unproven marks a body carrying structural content no worker has loaded yet
	// (patched onto an in-flight deploy's dispatched config). The push says nothing
	// about the running state, so a successful apply CLEARS the activation proof
	// instead of recording one — the next sync must reload rather than trust an
	// empty diff over parked content (issue #76).
	unproven bool
}

// applyRuntimeRaw fans out one goroutine per endpoint to apply the runtime-raw
// change and WAITS for all of them (sync.WaitGroup), so the deploy loop sees this
// call as synchronous — the runtime-raw apply completes before the loop advances.
//
// dep carries the precomputed render diff (dep.runtimeUpdates, NOT recomputed
// here); push.body is the config body the push carries (see bypassPush). The
// diff is render-vs-render, identical for every pod, so diff and body are
// shared across the per-pod pushes. parentCtx is the scheduler's loop context,
// so applies are cancelled on shutdown.
//
// The sole non-partial caller is the runtime-raw lane dispatch
// (dispatchPending), where this apply IS the complete deploy and publishes per
// the lane gate.
func (b *runtimeBypass) applyRuntimeRaw(parentCtx context.Context, dep *scheduledDeployment, push bypassPush) bool {
	lease, ok := b.acquireAuthorityLease(dep.endpoints)
	if !ok {
		return false
	}

	var wg sync.WaitGroup
	var successes atomic.Int32
	var incomplete atomic.Bool
	for i := range dep.endpoints {
		if parentCtx.Err() != nil {
			incomplete.Store(true)
			break
		}
		ep := dep.endpoints[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					incomplete.Store(true)
					b.recordEndpointFailure(parentCtx, lease, &ep)
					b.logger.Debug("Bypass apply panicked; structural deploy will converge",
						"endpoint", ep.URL, "panic", r)
				}
			}()
			if !b.applyToEndpoint(parentCtx, dep, &ep, &successes, push, lease) {
				incomplete.Store(true)
			}
		}()
	}
	wg.Wait()

	// Pure runtime-raw lane only, and only for an AUTHORITATIVE (non-partial) apply:
	// this apply IS the complete deploy (no reload), so publish the deployed config
	// as spec — the same invariant the structural path enforces — making the per-pod
	// checksum (stamped by publishConfigApplied) observable as a published
	// spec.Checksum. Excluded: the pre-interval partial apply of a STRUCTURAL render
	// (laneStructural, gated by lane), and the in-flight partial apply (partial=true)
	// whose owning structural deploy publishes after its reload.
	current := b.commitLease(parentCtx, lease, func() {
		if !push.partial && dep.lane == laneRuntimeRaw && successes.Load() > 0 {
			b.publishDeployedConfig(dep)
		}
	})
	return current && !incomplete.Load()
}

// applyToEndpoint runs one endpoint's runtime-raw apply under a bounded timeout.
// dep.runtimeUpdates is the shared precomputed render diff; push.body is the
// baseline-derived config body the raw push carries (no per-pod fetch).
func (b *runtimeBypass) applyToEndpoint(parentCtx context.Context, dep *scheduledDeployment, ep *dataplane.Endpoint, successes *atomic.Int32, push bypassPush, lease runtimeAuthorityLease) bool {
	ctx, cancel := context.WithTimeout(parentCtx, runtimeBypassTimeout)
	defer cancel()

	syncer, err := b.clientForLease(ctx, ep, lease)
	if err != nil {
		b.recordEndpointFailure(parentCtx, lease, ep)
		b.logger.Debug("Bypass client open failed; scheduled deploy will converge",
			"endpoint", ep.URL, "error", err)
		return false
	}

	opts := dataplane.DefaultSyncOptions()
	opts.Timeout = runtimeBypassTimeout
	// Only the AUTHORITATIVE runtime-raw lane apply may re-stamp the config's
	// version header after the skip_version push: the deploy loop dispatches
	// it strictly outside any in-flight structural deploy, so the re-stamped
	// header truthfully claims "disk == running state". A partial fast-track
	// apply can race an in-flight structural reload (its `set server` may land
	// on the worker the reload replaces) and must leave the config headerless
	// so the next structural sync force-reloads instead of trusting an empty
	// diff (see SyncOptions.RestampVersionHeader).
	opts.RestampVersionHeader = !push.partial
	opts.RenderSuperseded = push.superseded

	result, err := syncer.SyncRuntimeFast(ctx, dep.runtimeUpdates, push.body, opts)
	if err != nil {
		// The push may have reached disk regardless (the dataplane writes the
		// body even when the runtime actions 500), so this endpoint's running
		// state is no longer provable. Drop the proof and let the next sync
		// force a reload rather than trust an empty diff.
		if !b.commitEndpointLease(parentCtx, lease, ep, func() {
			b.noteActivation(ep, "")
			b.publishResult(0, true)
		}) {
			return false
		}
		b.logger.Debug("Bypass apply failed; activation proof cleared, scheduled deploy will converge",
			"endpoint", ep.URL, "error", err)
		return false
	}
	if result == nil || !result.Success {
		b.recordEndpointFailure(parentCtx, lease, ep)
		b.logger.Debug("Bypass apply returned no success proof; structural deploy will converge",
			"endpoint", ep.URL)
		return false
	}
	ops := len(result.AppliedOperations)
	if !b.commitEndpointLease(parentCtx, lease, ep, func() {
		proof := result.ActivatedConfigChecksum
		if push.unproven {
			proof = ""
		}
		b.noteActivation(ep, proof)
		b.publishResult(ops, false)
		b.publishConfigApplied(dep, ep, result, push.partial)
		successes.Add(1)
	}) {
		return false
	}
	if ops > 0 {
		b.logger.Debug("Bypass applied runtime server changes ahead of scheduled deploy",
			"endpoint", ep.URL,
			"ops", ops,
			"duration_ms", result.Duration.Milliseconds())
	}
	return true
}

func (b *runtimeBypass) recordEndpointFailure(ctx context.Context, lease runtimeAuthorityLease, endpoint *dataplane.Endpoint) {
	b.commitEndpointLease(ctx, lease, endpoint, func() {
		b.noteActivation(endpoint, "")
		b.publishResult(0, true)
	})
}

// noteActivation forwards an activation proof (or its removal) to the deployer's
// cache, if one is wired.
func (b *runtimeBypass) noteActivation(endpoint *dataplane.Endpoint, proof string) {
	if b.recordActivation == nil {
		return
	}
	b.recordActivation(endpoint, proof)
}

// publishConfigApplied advances the pod's status.deployedToPods[].Checksum after
// a successful runtime-raw apply — but ONLY for an AUTHORITATIVE pure runtime-raw
// lane apply (partial=false), where the apply (skip_reload, no reload) IS the
// complete deploy. It deliberately stays silent for:
//   - the pre-interval apply of a STRUCTURAL render's runtime subset
//     (dep.lane == laneStructural): its reload is still pending; and
//   - the in-flight partial apply (partial=true): the separate in-flight structural
//     deploy owns the per-pod status and publishes it after its reload.
//
// Reporting the pod at this render's checksum in either case would falsely claim
// the full config is live. The owning structural deploy publishes the truthful
// per-pod status after its reload completes (deployment.go). No-op when eventBus is
// nil (tests) or the HAProxyCfg identity wasn't resolved.
func (b *runtimeBypass) publishConfigApplied(dep *scheduledDeployment, ep *dataplane.Endpoint, result *dataplane.SyncResult, partial bool) {
	if b.eventBus == nil || partial || dep.lane != laneRuntimeRaw {
		return
	}
	if dep.runtimeConfigName == "" || dep.runtimeConfigNamespace == "" {
		return
	}
	b.eventBus.Publish(events.NewConfigAppliedToPodEvent(
		dep.runtimeConfigName,
		dep.runtimeConfigNamespace,
		ep.PodName,
		ep.PodNamespace,
		ep.PodUID,
		ep.PodRuntimeID,
		dep.contentChecksum,
		false, // an actual runtime apply, not a drift check
		syncResultToMetadata(result),
	))
}

// publishDeployedConfig asks the config-publisher to publish the just-applied
// config as the HAProxyCfg spec, so the runtime-raw lane's checksum (stamped
// per-pod by publishConfigApplied) is observable as a published spec.Checksum —
// the same CR-consistency invariant deployment.go enforces for structural
// deploys. Called once per applyRuntimeRaw, only for the pure runtime-raw lane
// on success. No-op when eventBus is nil (tests) or the identity/checksum is
// unresolved.
func (b *runtimeBypass) publishDeployedConfig(dep *scheduledDeployment) {
	if b.eventBus == nil || dep.runtimeConfigName == "" || dep.runtimeConfigNamespace == "" || dep.contentChecksum == "" {
		return
	}
	b.eventBus.Publish(events.NewDeployedConfigPublishRequest(
		dep.runtimeConfigName,
		dep.runtimeConfigNamespace,
		dep.config,
		dep.auxFiles,
		dep.contentChecksum,
	))
}

// publishResult records the fire-vs-apply distinction. No-op when the recorder
// is nil (tests).
func (b *runtimeBypass) publishResult(serverUpdates int, failed bool) {
	if b.recordFastPath != nil {
		b.recordFastPath(serverUpdates, failed)
	}
}

func (b *runtimeBypass) clientForLease(ctx context.Context, ep *dataplane.Endpoint, lease runtimeAuthorityLease) (runtimeSyncer, error) {
	identity := endpointAuthorityOf(ep)
	b.mu.Lock()
	if !b.endpointLeaseCurrentLocked(lease, &identity) {
		b.mu.Unlock()
		return nil, errRuntimeBypassAuthorityChanged
	}
	if c, ok := b.clients[identity]; ok {
		b.mu.Unlock()
		return c, nil
	}
	b.mu.Unlock()

	c, err := b.newSyncer(ctx, ep)
	if err != nil {
		return nil, err
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.endpointLeaseCurrentLocked(lease, &identity) {
		_ = c.Close()
		if b.closed {
			return nil, errRuntimeBypassClosed
		}
		return nil, errRuntimeBypassAuthorityChanged
	}
	if existing, ok := b.clients[identity]; ok {
		// Another goroutine opened one first; discard ours, use theirs.
		_ = c.Close()
		return existing, nil
	}
	b.clients[identity] = c
	return c, nil
}

func (b *runtimeBypass) beginLeadershipTerm() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for identity, c := range b.clients {
		_ = c.Close()
		delete(b.clients, identity)
	}
	b.closed = false
	b.authorities = make(map[endpointAuthority]struct{})
	b.authoritiesSet = false
	b.epoch++
}

// replaceEndpointAuthorities installs the scheduler's complete fleet view and
// retires clients and observations whose authority is no longer current.
func (b *runtimeBypass) replaceEndpointAuthorities(endpoints []dataplane.Endpoint) bool {
	live := endpointAuthoritySet(endpoints)
	b.mu.Lock()
	for identity, c := range b.clients {
		if _, ok := live[identity]; !ok {
			_ = c.Close()
			delete(b.clients, identity)
		}
	}
	changed := !b.authoritiesSet || !equalEndpointAuthoritySets(b.authorities, live)
	if changed {
		b.authorities = live
		b.authoritiesSet = true
		b.epoch++
	}
	b.mu.Unlock()

	if b.retainAuthorities != nil {
		b.retainAuthorities(endpoints)
	}
	return changed
}

func (b *runtimeBypass) acquireAuthorityLease(endpoints []dataplane.Endpoint) (runtimeAuthorityLease, bool) {
	wanted := endpointAuthoritySet(endpoints)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return runtimeAuthorityLease{}, false
	}
	if !b.authoritiesSet {
		b.authorities = wanted
		b.authoritiesSet = true
		b.epoch++
	}
	if !equalEndpointAuthoritySets(b.authorities, wanted) {
		return runtimeAuthorityLease{}, false
	}
	return runtimeAuthorityLease{epoch: b.epoch}, true
}

func (b *runtimeBypass) endpointLeaseCurrentLocked(lease runtimeAuthorityLease, authority *endpointAuthority) bool {
	if b.closed || b.epoch != lease.epoch {
		return false
	}
	_, ok := b.authorities[*authority]
	return ok
}

func (b *runtimeBypass) commitEndpointLease(ctx context.Context, lease runtimeAuthorityLease, endpoint *dataplane.Endpoint, commit func()) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	authority := endpointAuthorityOf(endpoint)
	if ctx.Err() != nil || !b.endpointLeaseCurrentLocked(lease, &authority) {
		return false
	}
	commit()
	return true
}

func (b *runtimeBypass) commitLease(ctx context.Context, lease runtimeAuthorityLease, commit func()) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ctx.Err() != nil || b.closed || b.epoch != lease.epoch {
		return false
	}
	commit()
	return true
}

// Close shuts down all persistent clients. Called when the scheduler stops or
// loses leadership, so the bypass holds no clients while not the active writer.
func (b *runtimeBypass) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	b.epoch++
	b.authorities = make(map[endpointAuthority]struct{})
	b.authoritiesSet = false
	for identity, c := range b.clients {
		_ = c.Close()
		delete(b.clients, identity)
	}
}
