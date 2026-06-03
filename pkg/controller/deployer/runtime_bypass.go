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

// runtimeSyncer is the narrow slice of *dataplane.Client the bypass needs.
// Declared at the use site so tests can substitute a fake.
type runtimeSyncer interface {
	SyncRuntimeFast(ctx context.Context, updates *dataplane.RuntimeServerUpdates, desiredConfig string, opts *dataplane.SyncOptions) (*dataplane.SyncResult, error)
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
// ConfigAppliedToPodEvent) — only the RuntimeFastPathResultEvent metric still
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
// Every failure here is swallowed to a debug log — the scheduled deploy converges the pod
// regardless.
//
// Clients are persistent per endpoint (see clientFor): the dataplane client —
// and the keep-alive HTTP connection underneath it — is opened once and reused
// across applies, rather than reallocated per apply. This keeps the fast path's
// latency dominated by the actual runtime call, not connection setup.
type runtimeBypass struct {
	logger    *slog.Logger
	newSyncer func(ctx context.Context, ep *dataplane.Endpoint) (runtimeSyncer, error)
	eventBus  *busevents.EventBus // publishes RuntimeFastPathResultEvent always, and ConfigAppliedToPodEvent for a complete (pure runtime-raw lane) apply; nil in tests

	mu      sync.Mutex
	clients map[string]runtimeSyncer // keyed by endpoint URL; persistent across applies
}

// newRuntimeBypass builds a runtimeBypass that opens real dataplane clients.
func newRuntimeBypass(logger *slog.Logger, eventBus *busevents.EventBus) *runtimeBypass {
	return &runtimeBypass{
		logger:   logger.With("path", "runtime-bypass"),
		eventBus: eventBus,
		newSyncer: func(ctx context.Context, ep *dataplane.Endpoint) (runtimeSyncer, error) {
			return dataplane.NewClient(ctx, ep)
		},
		clients: make(map[string]runtimeSyncer),
	}
}

// applyRuntimeRaw fans out one goroutine per endpoint to apply the runtime-raw
// change and WAITS for all of them (sync.WaitGroup), so the deploy loop sees this
// call as synchronous — the runtime-raw apply completes before the loop advances.
//
// dep carries the precomputed render diff (dep.runtimeUpdates, NOT recomputed
// here) and the desired config body (dep.config); the diff is render-vs-render,
// identical for every pod, so it is shared across the per-pod pushes. parentCtx
// is the scheduler's loop context, so applies are cancelled on shutdown.
//
// partial marks a fast-track apply whose deploy is owned by someone else — the
// eventual authoritative dispatch of this pending, or an in-flight/just-completed
// structural deploy (applyRuntimeSubset): it suppresses the deploy-owning publishes
// so that owner remains the single authority for completion + CR status. The sole
// non-partial caller is the runtime-raw lane dispatch (dispatchPending), where this
// apply IS the complete deploy and publishes per the lane gate.
func (b *runtimeBypass) applyRuntimeRaw(parentCtx context.Context, dep *scheduledDeployment, partial bool) {
	b.evictStaleClients(dep.endpoints)

	var wg sync.WaitGroup
	var successes atomic.Int32
	for i := range dep.endpoints {
		if parentCtx.Err() != nil {
			break
		}
		ep := dep.endpoints[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					b.logger.Error("bypass apply panicked; scheduled deploy will converge",
						"endpoint", ep.URL, "panic", r)
				}
			}()
			b.applyToEndpoint(parentCtx, dep, &ep, &successes, partial)
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
	if !partial && dep.lane == laneRuntimeRaw && successes.Load() > 0 {
		b.publishDeployedConfig(dep)
	}
}

// applyToEndpoint runs one endpoint's runtime-raw apply under a bounded timeout.
// dep.runtimeUpdates is the shared precomputed render diff; dep.config is the
// desired render body the raw push carries (no per-pod fetch).
func (b *runtimeBypass) applyToEndpoint(parentCtx context.Context, dep *scheduledDeployment, ep *dataplane.Endpoint, successes *atomic.Int32, partial bool) {
	ctx, cancel := context.WithTimeout(parentCtx, runtimeBypassTimeout)
	defer cancel()

	syncer, err := b.clientFor(ctx, ep)
	if err != nil {
		b.logger.Debug("bypass client open failed; scheduled deploy will converge",
			"endpoint", ep.URL, "error", err)
		return
	}

	opts := dataplane.DefaultSyncOptions()
	opts.Timeout = runtimeBypassTimeout

	result, err := syncer.SyncRuntimeFast(ctx, dep.runtimeUpdates, dep.config, opts)
	if err != nil {
		b.publishResult(0, true)
		b.logger.Debug("bypass apply failed; scheduled deploy will converge",
			"endpoint", ep.URL, "error", err)
		return
	}
	ops := 0
	if result != nil {
		ops = len(result.AppliedOperations)
	}
	b.publishResult(ops, false)
	b.publishConfigApplied(dep, ep, result, partial)
	successes.Add(1)
	if ops > 0 {
		b.logger.Info("bypass applied runtime server changes ahead of scheduled deploy",
			"endpoint", ep.URL,
			"ops", ops,
			"duration_ms", result.Duration.Milliseconds())
	}
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

// publishResult emits a RuntimeFastPathResultEvent so the metrics component can
// track the fire-vs-apply distinction. No-op when eventBus is nil (tests).
func (b *runtimeBypass) publishResult(serverUpdates int, failed bool) {
	if b.eventBus != nil {
		b.eventBus.Publish(events.NewRuntimeFastPathResultEvent(serverUpdates, failed))
	}
}

// clientFor returns the persistent client for ep, opening and caching it on
// first use. The dataplane client (and its keep-alive HTTP connection) is reused
// across applies. Uses a double-checked pattern so the dataplane open — which
// may do a version-detect round-trip — runs without holding the lock.
func (b *runtimeBypass) clientFor(ctx context.Context, ep *dataplane.Endpoint) (runtimeSyncer, error) {
	b.mu.Lock()
	if c, ok := b.clients[ep.URL]; ok {
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
	if existing, ok := b.clients[ep.URL]; ok {
		// Another goroutine opened one first; discard ours, use theirs.
		_ = c.Close()
		return existing, nil
	}
	b.clients[ep.URL] = c
	return c, nil
}

// evictStaleClients closes and drops cached clients whose endpoint is no longer
// present (the pod was deleted). Closing is safe even against an in-flight apply
// because dataplane.Client.Close is a no-op on the keep-alive transport; and an
// apply to a now-absent endpoint would fail regardless.
func (b *runtimeBypass) evictStaleClients(endpoints []dataplane.Endpoint) {
	live := make(map[string]struct{}, len(endpoints))
	for i := range endpoints {
		live[endpoints[i].URL] = struct{}{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for url, c := range b.clients {
		if _, ok := live[url]; !ok {
			_ = c.Close()
			delete(b.clients, url)
		}
	}
}

// Close shuts down all persistent clients. Called when the scheduler stops or
// loses leadership, so the bypass holds no clients while not the active writer.
func (b *runtimeBypass) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for url, c := range b.clients {
		_ = c.Close()
		delete(b.clients, url)
	}
}
