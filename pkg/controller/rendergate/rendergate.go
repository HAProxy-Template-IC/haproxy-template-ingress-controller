// Copyright 2026 Philipp Hossner
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

// Package rendergate runs the controller's own `haproxy -c` over each render,
// off the reconcile wall clock.
//
// The reconcile pipeline renders and dispatches; this component decides
// afterwards whether HAProxy would load what was dispatched. It is a two-state
// latch (ADR-0022):
//
//   - OPTIMISTIC (default): renders dispatch immediately. A pass names the plan
//     agents may promote their rollback baseline to; a refusal reverts the pods
//     that took the bad plan without loading it and flips the latch.
//   - PESSIMISTIC: the scheduler holds every render until this gate passes one.
//     A pass releases it and returns to OPTIMISTIC; a refusal pins the fleet on
//     the last config it accepted.
//
// Coverage is the same as the synchronous check it replaces. What changes is
// when it runs: while OPTIMISTIC the exposure window is one check plus one
// apply, closed by the scoped revert and the latch.
package rendergate

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"time"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/component"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/metrics"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercycle"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/validation"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderartifact"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderoutput"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/lifecycle"
)

const (
	// ComponentName is the unique identifier for this component.
	ComponentName = "rendergate"

	// EventBufferSize is the size of the event subscription buffer. Renders
	// arrive at most one per reconcile; pod ACKs are the higher-volume input.
	EventBufferSize = busevents.StandardSubscriberBuffer

	// validatorLabel is the haptic_config_rejected_total label this gate
	// records under: the validator that refused is HAProxy itself.
	validatorLabel = "haproxy"

	// maxRetainedRenders bounds the superseded renders kept in memory for the
	// stragglers check. A converging fleet holds one or two distinct plans; a
	// higher number means pods are stuck, which the deployer reports, not
	// something this gate should spend a 5 MB render on.
	maxRetainedRenders = 4

	// maxRememberedVerdicts bounds the verdicts kept per plan content. A
	// reconcile loop re-rendering the same plan produces one occurrence per
	// pass; the verdict belongs to the content, so each later occurrence
	// settles from memory instead of spending another `haproxy -c`.
	maxRememberedVerdicts = 16
)

// Checker runs the controller's own `haproxy -c` over one rendered file set.
// A nil error is a pass; dataplane.ErrHAProxyRefused in the chain means HAProxy
// judged the config, anything else means the check could not run.
type Checker interface {
	Check(ctx context.Context, config string, auxFiles *dataplane.AuxiliaryFiles, checksum string) error
}

// SnapshotChecker validates authenticated immutable auxiliary output directly.
type SnapshotChecker interface {
	CheckSnapshot(ctx context.Context, config string, snapshot *renderartifact.Snapshot, checksum string) error
}

// OutputChecker validates one authenticated complete render output directly.
type OutputChecker interface {
	CheckOutput(ctx context.Context, snapshot *renderoutput.Snapshot, checksum string) error
}

// verdict is HAProxy's answer for one plan's content.
type verdict struct {
	ok      bool
	refused bool
	message string
}

// render is one plan's bytes, kept until no pod needs a verdict on it.
type render struct {
	occurrence    *rendercycle.Occurrence
	output        *renderoutput.Snapshot
	planID        string
	renderProof   string
	checksum      string
	correlationID string
	causationID   string
	checked       bool
}

// Component is the render gate's event adapter.
//
// Event subscriptions:
//   - TemplateRenderedEvent: the render to check (always coalesced to newest)
//   - ConfigAppliedToPodEvent: which plan each pod holds, so a superseded plan
//     that pods still run is checked too
//   - HAProxyPodTerminatedEvent / HAProxyPodsDiscoveredEvent: prune that map
//   - LostLeadershipEvent: drop the latch and the fleet's plans
//
// It publishes RenderGateCompletedEvent.
type Component struct {
	*component.ReadySignal

	eventBus  *busevents.EventBus
	eventChan <-chan busevents.Event
	logger    *slog.Logger

	checker       Checker
	metrics       *metrics.Metrics
	checkTimeout  time.Duration
	healthTracker *lifecycle.HealthTracker

	// wake signals the worker that there is something to check. Capacity 1:
	// the worker re-reads the state, so one pending signal is enough.
	wake chan struct{}

	mu sync.Mutex
	// newest is the last render seen, the one the gate always checks first.
	newest *render
	// superseded holds renders some pod may still be running, oldest first.
	superseded []*render
	// appliedByPod is each pod's last reported applied render proof.
	appliedByPod map[string]string
	// pessimistic is the latch: true while renders are held for a verdict.
	pessimistic bool
	// verdicts remembers HAProxy's answer per plan content, newest last.
	verdicts     map[string]verdict
	verdictOrder []string
}

// Config contains the dependencies for the render gate.
type Config struct {
	EventBus *busevents.EventBus
	Logger   *slog.Logger
	Checker  Checker
	// Metrics records the rejection counter and the pinned gauge. Nil in tests.
	Metrics *metrics.Metrics
	// CheckTimeout bounds one check. Zero uses validation.DefaultValidationTimeout.
	CheckTimeout time.Duration
}

// New creates the render gate.
//
// It does NOT subscribe here: two of its inputs (pod discovery, pod
// termination) are published on every replica, so a construction-time
// subscription would fill a channel nobody drains on the standby and be
// reported as critical drops. Start subscribes once leadership is held.
func New(cfg *Config) *Component {
	timeout := cfg.CheckTimeout
	if timeout <= 0 {
		timeout = validation.DefaultValidationTimeout
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Component{
		ReadySignal:   component.NewReadySignal(),
		eventBus:      cfg.EventBus,
		logger:        logger.With("component", ComponentName),
		checker:       cfg.Checker,
		metrics:       cfg.Metrics,
		checkTimeout:  timeout,
		healthTracker: lifecycle.NewProcessingTracker(ComponentName, lifecycle.DefaultProcessingTimeout),
		wake:          make(chan struct{}, 1),
		appliedByPod:  map[string]string{},
		verdicts:      map[string]verdict{},
	}
}

// Logger exposes the component's logger, matching the other components' shape.
func (c *Component) Logger() *slog.Logger { return c.logger }

// Name returns the unique identifier for this component.
func (c *Component) Name() string { return ComponentName }

// HealthCheck implements lifecycle.HealthChecker.
func (c *Component) HealthCheck() error { return c.healthTracker.Check() }

// Start runs the event loop and the check worker until ctx is cancelled.
//
// A new leadership term starts OPTIMISTIC: the agents' own last-known-good set
// protects the fleet, so a fresh leader has nothing to gain from validating
// before it trusts its first render.
func (c *Component) Start(ctx context.Context) error {
	c.reset()
	defer c.Rearm()

	// Subscribe when starting (after leadership acquired), so the standby
	// never holds a channel for events it will not read.
	c.eventChan = c.eventBus.SubscribeTypesLeaderOnly(ComponentName, EventBufferSize,
		events.EventTypeTemplateRendered,
		events.EventTypeConfigAppliedToPod,
		events.EventTypeHAProxyPodTerminated,
		events.EventTypeHAProxyPodsDiscovered,
		events.EventTypeLostLeadership,
	)
	// Without this, every leadership re-acquisition stacks another
	// subscription whose orphaned channel fills and logs drops forever.
	defer c.eventBus.UnsubscribeTyped(c.eventChan)
	c.MarkReady()

	workerCtx, stopWorker := context.WithCancel(ctx)
	workerDone := make(chan struct{})
	go c.runChecks(workerCtx, workerDone)
	defer func() {
		stopWorker()
		<-workerDone
	}()

	for {
		select {
		case event := <-c.eventChan:
			c.handleEvent(event)
		case <-ctx.Done():
			c.logger.Info("Render gate shutting down", "reason", ctx.Err())
			return nil
		}
	}
}

// handleEvent routes one event from the subscription.
func (c *Component) handleEvent(event busevents.Event) {
	c.healthTracker.StartProcessing()
	defer c.healthTracker.EndProcessing()

	switch e := event.(type) {
	case *events.TemplateRenderedEvent:
		c.handleTemplateRendered(e)
	case *events.ConfigAppliedToPodEvent:
		c.handleConfigAppliedToPod(e)
	case *events.HAProxyPodTerminatedEvent:
		c.forgetPod(e.PodNamespace, e.PodName)
	case *events.HAProxyPodsDiscoveredEvent:
		c.retainPods(e)
	case *events.LostLeadershipEvent:
		c.reset()
	}
}

func (c *Component) reset() {
	c.mu.Lock()
	c.newest = nil
	c.superseded = nil
	c.appliedByPod = map[string]string{}
	c.pessimistic = false
	c.verdicts = map[string]verdict{}
	c.verdictOrder = nil
	c.mu.Unlock()
	c.setPinnedGauge(false)
}

func (c *Component) setPinnedGauge(pinned bool) {
	if c.metrics != nil {
		c.metrics.SetConfigPinned(pinned)
	}
}

// handleTemplateRendered records the render as the one to check next. A render
// carrying no plan cannot be reverted from and is not gated: the deployer
// refuses it anyway.
func (c *Component) handleTemplateRendered(event *events.TemplateRenderedEvent) {
	occurrence, err := event.RenderOccurrence()
	if err != nil {
		return
	}
	cycle, err := occurrence.Snapshot()
	if err != nil {
		return
	}
	output, err := cycle.OutputSnapshot()
	if err != nil {
		return
	}
	planID, err := output.PlanID()
	if err != nil || planID == "" {
		return
	}
	checksum, err := cycle.ContentChecksum()
	if err != nil {
		return
	}
	renderProof, err := occurrence.Proof()
	if err != nil {
		return
	}
	c.rememberRender(&render{
		occurrence: occurrence, output: output, planID: planID,
		renderProof: renderProof, checksum: checksum,
		correlationID: event.CorrelationID(), causationID: event.EventID(),
	})
}

func (c *Component) rememberRender(next *render) {
	c.mu.Lock()
	previous := c.newest
	c.newest = next
	if previous != nil && previous.renderProof != next.renderProof && !previous.checked {
		c.superseded = append(c.superseded, previous)
		c.trimSupersededLocked()
	}
	c.mu.Unlock()

	c.signal()
}

// handleConfigAppliedToPod records which plan a pod holds, so a plan the newest
// render superseded is still checked while some pod runs it.
func (c *Component) handleConfigAppliedToPod(event *events.ConfigAppliedToPodEvent) {
	renderProof := ""
	if event.SyncMetadata != nil {
		renderProof = event.SyncMetadata.AppliedRenderProof
	}
	c.mu.Lock()
	if renderProof != "" {
		c.appliedByPod[podKey(event.PodNamespace, event.PodName)] = renderProof
	}
	c.mu.Unlock()

	c.signal()
}

func (c *Component) forgetPod(namespace, name string) {
	c.mu.Lock()
	delete(c.appliedByPod, podKey(namespace, name))
	c.mu.Unlock()
}

// retainPods drops the plans of pods that are no longer part of the fleet, so
// a departed pod cannot keep a superseded render alive forever.
func (c *Component) retainPods(event *events.HAProxyPodsDiscoveredEvent) {
	live := make(map[string]struct{}, len(event.Endpoints))
	for i := range event.Endpoints {
		live[podKey(event.Endpoints[i].PodNamespace, event.Endpoints[i].PodName)] = struct{}{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for key := range c.appliedByPod {
		if _, ok := live[key]; !ok {
			delete(c.appliedByPod, key)
		}
	}
}

func podKey(namespace, name string) string { return namespace + "/" + name }

func (c *Component) signal() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// runChecks is the gate's only slow path: it drains the work the event loop
// recorded, one check at a time, so a render burst costs one check.
func (c *Component) runChecks(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	backoff := time.Duration(0)
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.wake:
		}
		for job := c.takeJob(); job != nil; job = c.takeJob() {
			if ctx.Err() != nil {
				return
			}
			if c.check(ctx, job) {
				backoff = 0
				continue
			}
			// No verdict: the check itself could not run. The render is
			// still unchecked, so retry it rather than leave the fleet
			// judged by a full disk — spaced out, since whatever broke is
			// unlikely to be fixed within the millisecond.
			backoff = nextBackoff(backoff)
			if !sleepUntil(ctx, backoff) {
				return
			}
		}
	}
}

// retryBackoff bounds how often a check that could not run is retried: long
// enough that a broken host is not hammered, short enough that the latch
// reopens promptly once it is repaired.
const (
	minRetryBackoff = time.Second
	maxRetryBackoff = 30 * time.Second
)

func nextBackoff(current time.Duration) time.Duration {
	if current <= 0 {
		return minRetryBackoff
	}
	if doubled := current * 2; doubled < maxRetryBackoff {
		return doubled
	}
	return maxRetryBackoff
}

// sleepUntil waits out the delay, reporting false when the term ended first.
func sleepUntil(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// takeJob returns the next render to check: the newest first, because that is
// the one the fleet is converging on (and, while pinned, the one the scheduler
// is holding), then any superseded render some pod still reports applied.
func (c *Component) takeJob() *render {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.newest != nil && !c.newest.checked {
		return c.newest
	}
	applied := c.appliedPlansLocked()
	for _, candidate := range c.superseded {
		if candidate.checked {
			continue
		}
		// A render no pod reports applied is nothing to answer for yet. It is
		// skipped, not dropped: the pod that took it may still be reporting.
		if _, stillRunning := applied[candidate.renderProof]; !stillRunning {
			continue
		}
		return candidate
	}
	return nil
}

// appliedPlansLocked is the set of plans the fleet reports applied. Caller
// holds mu.
func (c *Component) appliedPlansLocked() map[string]struct{} {
	applied := make(map[string]struct{}, len(c.appliedByPod))
	for _, renderProof := range c.appliedByPod {
		applied[renderProof] = struct{}{}
	}
	return applied
}

// trimSupersededLocked caps the retained superseded renders, dropping only
// ones no pod reports applied.
//
// It only acts above the cap, and a render some pod still runs is never
// evicted — the fleet holds at most one distinct plan per pod, and departed
// pods are pruned, so the still-running overshoot is bounded by fleet size.
// A render nobody has reported yet is not one nobody runs — the apply that
// would report it may still be in flight — so evicting on that basis would
// throw away exactly the render the straggler check exists for. Caller
// holds mu.
func (c *Component) trimSupersededLocked() {
	excess := len(c.superseded) - maxRetainedRenders
	if excess <= 0 {
		return
	}
	applied := c.appliedPlansLocked()
	kept := make([]*render, 0, maxRetainedRenders)
	for _, candidate := range c.superseded {
		if _, stillRunning := applied[candidate.renderProof]; !stillRunning && excess > 0 {
			excess--
			continue
		}
		kept = append(kept, candidate)
	}
	c.superseded = kept
}

// check runs one verdict and publishes it. It reports whether a verdict was
// reached at all: a check that could not run is not one, and its render stays
// unchecked so the worker can come back to it.
func (c *Component) check(ctx context.Context, job *render) bool {
	start := time.Now()
	answer, remembered := c.rememberedVerdict(job)
	if !remembered {
		checkCtx, cancel := context.WithTimeout(ctx, c.checkTimeout)
		err := c.checkRendered(checkCtx, job)
		cancel()
		// A cancelled check is not a verdict: the term is ending, and
		// publishing a failure here would pin the next leader on nothing.
		if ctx.Err() != nil {
			return false
		}
		answer = verdict{ok: err == nil, refused: errors.Is(err, dataplane.ErrHAProxyRefused)}
		if err != nil {
			answer.message = dataplane.SimplifyValidationError(err)
		}
	}
	durationMs := time.Since(start).Milliseconds()
	judged := answer.ok || answer.refused

	c.mu.Lock()
	// Only HAProxy's own answer settles a plan. A check that could not run
	// leaves it unchecked, so the retry re-runs this render rather than
	// leaving it judged by an unwritable temp directory forever.
	job.checked = judged
	if judged && !remembered {
		c.rememberVerdictLocked(job, answer)
	}
	// A newer render may arrive while haproxy -c is running. The verdict may
	// move the latch only if it still speaks for the newest render: the same
	// occurrence, or a later occurrence of the same content, which a reconcile
	// loop re-rendering a refused plan produces once per pass.
	newest := c.newest != nil &&
		(c.newest.renderProof == job.renderProof || verdictKey(c.newest) == verdictKey(job))
	pinned := c.settleLocked(answer.ok, newest)
	c.mu.Unlock()
	if newest {
		c.setPinnedGauge(pinned)
	}

	switch {
	case answer.ok:
		c.logger.Debug("Render gate passed", "plan", job.planID, "newest", newest,
			"remembered", remembered, "duration_ms", durationMs)
	case answer.refused:
		if c.metrics != nil && !remembered {
			c.metrics.RecordConfigRejected(validatorLabel)
		}
		c.logger.Error("HAProxy refused the rendered configuration",
			"plan", job.planID, "newest", newest, "remembered", remembered,
			"error", answer.message, "correlation_id", job.correlationID)
	default:
		c.logger.Error("Render gate could not run; holding renders until a check succeeds",
			"plan", job.planID, "newest", newest, "error", answer.message, "correlation_id", job.correlationID)
	}

	event, eventErr := events.NewRenderGateCompletedEventWithCycle(
		job.occurrence, answer.ok, answer.refused, newest, answer.message, pinned, durationMs,
		events.WithCorrelation(job.correlationID, job.causationID),
	)
	if eventErr != nil {
		c.logger.Error("Render gate could not publish an authenticated cycle verdict", "error", eventErr)
		return false
	}
	c.eventBus.Publish(event)
	return judged
}

// verdictKey identifies a render's content: the plan and the checksum over
// everything HAProxy reads, so two occurrences with the same key get the same
// answer from `haproxy -c`.
func verdictKey(job *render) string {
	return job.planID + "/" + job.checksum
}

// rememberedVerdict returns the answer kept for the job's content. A hit
// moves the content to the back of the eviction order, so a plan the
// reconcile loop keeps re-rendering outlives contents seen once.
func (c *Component) rememberedVerdict(job *render) (verdict, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := verdictKey(job)
	answer, found := c.verdicts[key]
	if found {
		c.touchVerdictLocked(key)
	}
	return answer, found
}

// rememberVerdictLocked keeps HAProxy's answer for the job's content, evicting
// the least recently used above the cap. Caller holds mu.
func (c *Component) rememberVerdictLocked(job *render, answer verdict) {
	key := verdictKey(job)
	if _, exists := c.verdicts[key]; exists {
		c.touchVerdictLocked(key)
	} else {
		c.verdictOrder = append(c.verdictOrder, key)
	}
	c.verdicts[key] = answer
	for len(c.verdictOrder) > maxRememberedVerdicts {
		delete(c.verdicts, c.verdictOrder[0])
		c.verdictOrder = c.verdictOrder[1:]
	}
}

func (c *Component) touchVerdictLocked(key string) {
	index := slices.Index(c.verdictOrder, key)
	if index < 0 || index == len(c.verdictOrder)-1 {
		return
	}
	c.verdictOrder = append(slices.Delete(c.verdictOrder, index, index+1), key)
}

func (c *Component) checkRendered(ctx context.Context, job *render) error {
	checksum, err := job.output.ContentChecksum()
	if err != nil {
		return err
	}
	if checker, ok := c.checker.(OutputChecker); ok {
		return checker.CheckOutput(ctx, job.output, checksum)
	}
	config, err := job.output.Config()
	if err != nil {
		return err
	}
	artifacts, err := job.output.ArtifactSnapshot()
	if err != nil {
		return err
	}
	if checker, ok := c.checker.(SnapshotChecker); ok {
		return checker.CheckSnapshot(ctx, config, artifacts, checksum)
	}
	auxFiles, err := dataplane.MaterializeAuxiliaryFileSnapshot(artifacts)
	if err != nil {
		return err
	}
	return c.checker.Check(ctx, config, auxFiles, checksum)
}

// settleLocked moves the latch and reports whether the fleet is now pinned.
// Caller holds mu.
//
// Only a verdict on the newest render moves it. A superseded plan a pod still
// runs is checked for the revert's sake, but the fleet has already moved past
// it: letting its verdict reopen the gate would dispatch renders nothing
// judged, and letting it close the gate would pin on a plan nobody is
// converging on.
//
// A pass always returns to OPTIMISTIC. Any failure — HAProxy's verdict or a
// gate that could not run — holds the next render, because dispatching a config
// nothing has judged is what the latch exists to prevent. Pinned is the second
// consecutive failure: a render the gate was already holding was refused, so
// nothing new reaches the pods until the operator's input changes.
func (c *Component) settleLocked(ok, newest bool) bool {
	if !newest {
		return c.pessimistic
	}
	pinned := !ok && c.pessimistic
	c.pessimistic = !ok
	return pinned
}

// ServiceChecker adapts a validation.ValidationService to Checker.
type ServiceChecker struct {
	Service *validation.ValidationService
}

// Check runs the service's `haproxy -c` and returns its error unchanged, so the
// caller can tell HAProxy's verdict from a check that could not run.
func (s ServiceChecker) Check(ctx context.Context, config string, auxFiles *dataplane.AuxiliaryFiles, checksum string) error {
	result := s.Service.ValidateWithChecksum(ctx, config, auxFiles, checksum)
	if result.Valid {
		return nil
	}
	return result.Error
}

// CheckSnapshot validates authenticated immutable auxiliary output.
func (s ServiceChecker) CheckSnapshot(
	ctx context.Context,
	config string,
	snapshot *renderartifact.Snapshot,
	checksum string,
) error {
	result := s.Service.ValidateSnapshotWithChecksum(ctx, config, snapshot, checksum)
	if result.Valid {
		return nil
	}
	return result.Error
}

// CheckOutput validates a complete immutable render output without splitting its identity.
func (s ServiceChecker) CheckOutput(
	ctx context.Context,
	snapshot *renderoutput.Snapshot,
	checksum string,
) error {
	result := s.Service.ValidateOutputSnapshotWithChecksum(ctx, snapshot, checksum)
	if result.Valid {
		return nil
	}
	return result.Error
}
