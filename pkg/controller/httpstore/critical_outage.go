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

package httpstore

import (
	"context"
	"errors"
	"maps"
	"sync"
	"time"

	"github.com/haproxy-haptic/HAPTIC/pkg/controller/events"
	purehttpstore "github.com/haproxy-haptic/HAPTIC/pkg/httpstore"
)

// DefaultOutageRetryInterval paces refetching an unavailable critical source
// that declares no refresh interval.
const DefaultOutageRetryInterval = 30 * time.Second

// criticalOutages tracks critical sources without accepted content whose
// fetch failed, and the reconcile renders that would only repeat that failure.
// See ADR-0030.
type criticalOutages struct {
	mu         sync.Mutex
	sources    map[string]*criticalOutage
	recovered  map[string]retryInput
	generation uint64
	// epoch advances whenever a held render could end differently.
	epoch   uint64
	hold    *renderHold
	stopped bool
	probes  sync.WaitGroup
}

type criticalOutage struct {
	source     *purehttpstore.StagedSource
	err        error
	generation uint64
	timer      *time.Timer
}

type renderHold struct {
	err   error
	epoch uint64
	// deferred records a render skipped since the hold began.
	deferred bool
}

func newCriticalOutages() *criticalOutages {
	return &criticalOutages{
		sources:   make(map[string]*criticalOutage),
		recovered: make(map[string]retryInput),
	}
}

func outageRetryInterval(source *purehttpstore.StagedSource) time.Duration {
	if interval := source.RefreshInterval(); interval > 0 {
		return interval
	}
	return DefaultOutageRetryInterval
}

// BeginReconcileRender returns the failure a reconcile render would only
// repeat, or the epoch to hold at if this render fails on unavailable content.
func (c *Component) BeginReconcileRender() (uint64, error) {
	o := c.outages
	o.mu.Lock()
	defer o.mu.Unlock()
	if hold := o.hold; hold != nil {
		if hold.epoch == o.epoch {
			hold.deferred = true
			return 0, hold.err
		}
		o.hold = nil
	}
	return o.epoch, nil
}

// HoldReconcileRenders pauses reconcile renders after one, begun at epoch,
// failed because a critical source is unavailable. Renders resume when the
// source answers, or after its next failed refetch if a render was skipped.
func (c *Component) HoldReconcileRenders(epoch uint64, err error) {
	if !errors.Is(err, purehttpstore.ErrCriticalFetch) {
		return
	}
	o := c.outages
	o.mu.Lock()
	if o.stopped || epoch != o.epoch || len(o.sources) == 0 {
		o.mu.Unlock()
		return
	}
	first := o.hold == nil
	o.hold = &renderHold{err: err, epoch: epoch}
	o.mu.Unlock()
	if first {
		c.logger.Info("Rendering paused until the critical http.Fetch source answers; it is refetched on its own schedule", "error", err)
	}
}

// ReconcileRenderCompleted ends every outage: a completed render needed none
// of those sources, since an unavailable critical source fails its render.
func (c *Component) ReconcileRenderCompleted() {
	o := c.outages
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.sources) == 0 {
		return
	}
	for url := range o.sources {
		o.stopLocked(url)
	}
	o.epoch++
	o.hold = nil
}

// observeInitialFetch records whether an unaccepted critical source answered.
func (c *Component) observeInitialFetch(ctx context.Context, source *purehttpstore.StagedSource, err error) {
	if ctx.Err() != nil || !source.Critical() {
		return
	}
	switch {
	case err == nil:
		c.endOutage(source)
	case errors.Is(err, purehttpstore.ErrCriticalFetch):
		c.recordOutage(source, err)
	}
}

func (c *Component) recordOutage(source *purehttpstore.StagedSource, err error) {
	o := c.outages
	url := source.URL()
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.stopped {
		return
	}
	delete(o.recovered, url)
	if outage := o.sources[url]; outage != nil && outage.source.Descriptor() == source.Descriptor() {
		outage.err = err
		return
	}
	o.stopLocked(url)
	o.generation++
	outage := &criticalOutage{source: source, err: err, generation: o.generation}
	o.sources[url] = outage
	c.armProbeLocked(outage)
	c.logger.Warn("Critical http.Fetch source is unavailable. Renders that need it fail until it answers. Fix the source.",
		"url", purehttpstore.RedactURL(url), "retry_interval", outageRetryInterval(source).String(), "error", err)
}

func (c *Component) endOutage(source *purehttpstore.StagedSource) {
	o := c.outages
	o.mu.Lock()
	defer o.mu.Unlock()
	if outage := o.sources[source.URL()]; outage != nil && outage.source.Descriptor() == source.Descriptor() {
		o.stopLocked(source.URL())
		o.epoch++
	}
}

func (c *Component) armProbeLocked(outage *criticalOutage) {
	url, generation := outage.source.URL(), outage.generation
	c.outages.probes.Add(1)
	outage.timer = time.AfterFunc(outageRetryInterval(outage.source), func() {
		defer c.outages.probes.Done()
		c.probeOutage(url, generation)
	})
}

func (o *criticalOutages) stopLocked(url string) {
	outage := o.sources[url]
	if outage == nil {
		return
	}
	if outage.timer != nil && outage.timer.Stop() {
		o.probes.Done()
	}
	delete(o.sources, url)
}

// probeOutage refetches the source outside any render.
func (c *Component) probeOutage(url string, generation uint64) {
	c.mu.Lock()
	ctx := c.ctx
	c.mu.Unlock()
	o := c.outages
	o.mu.Lock()
	outage := o.sources[url]
	if ctx == nil || outage == nil || outage.generation != generation {
		o.mu.Unlock()
		return
	}
	source := outage.source
	o.mu.Unlock()

	restaged, err := c.store.RestageSource(source)
	var recovered retryInput
	fetched := false
	if err == nil {
		recovered, fetched, err = c.prepareRecoveredInput(ctx, restaged)
	}
	if ctx.Err() != nil {
		return
	}

	o.mu.Lock()
	outage = o.sources[url]
	if o.stopped || outage == nil || outage.generation != generation {
		o.mu.Unlock()
		return
	}
	if err == nil {
		delete(o.sources, url)
		if fetched {
			o.recovered[url] = recovered
		}
		o.epoch++
		o.hold = nil
		o.mu.Unlock()
		c.logger.Info("Critical http.Fetch source answered again; rendering resumes", "url", purehttpstore.RedactURL(url))
		c.eventBus.Publish(events.NewReconciliationTriggeredEvent("critical_http_content_available", true, events.WithNewCorrelation()))
		return
	}
	recheck := false
	if errors.Is(err, purehttpstore.ErrCriticalFetch) {
		outage.err = err
		// A skipped render may have carried inputs that no longer need the source.
		if o.hold != nil && o.hold.deferred {
			o.epoch++
			recheck = true
		}
	}
	c.armProbeLocked(outage)
	o.mu.Unlock()
	c.logger.Debug("Critical http.Fetch source is still unavailable", "url", purehttpstore.RedactURL(url), "error", err)
	if recheck {
		c.eventBus.Publish(events.NewReconciliationTriggeredEvent("critical_http_content_recheck", true, events.WithNewCorrelation()))
	}
}

// prepareRecoveredInput fetches the source unless it already has accepted
// content, which needs no candidate.
func (c *Component) prepareRecoveredInput(ctx context.Context, source *purehttpstore.StagedSource) (retryInput, bool, error) {
	accepted, _, err := c.store.AcceptedStagedSnapshot(source)
	if err != nil || accepted.Found {
		return retryInput{}, false, err
	}
	snapshot, candidate, err := c.store.PrepareStagedSnapshot(ctx, source)
	if err != nil {
		return retryInput{}, false, err
	}
	return retryInput{source: source, result: inputFetchResult{snapshot: snapshot}, candidate: candidate}, true, nil
}

// criticalOutageFailure returns the recorded failure of an unaccepted source.
func (c *Component) criticalOutageFailure(source *purehttpstore.StagedSource) error {
	o := c.outages
	o.mu.Lock()
	defer o.mu.Unlock()
	outage := o.sources[source.URL()]
	if outage == nil || outage.source.Descriptor() != source.Descriptor() {
		return nil
	}
	return outage.err
}

// withRecoveredInputs lets an accepting render adopt content a refetch
// recovered instead of fetching it again; the seed's own inputs win.
func (c *Component) withRecoveredInputs(seed *InputRetrySeed) *InputRetrySeed {
	o := c.outages
	o.mu.Lock()
	recovered := maps.Clone(o.recovered)
	o.mu.Unlock()
	for url := range recovered {
		if source := recovered[url].source; !c.verifyStagedSource(source) {
			delete(recovered, url)
			c.forgetRecovered(url, source)
		}
	}
	if len(recovered) == 0 {
		return seed
	}
	if seed != nil {
		maps.Copy(recovered, seed.inputs)
	}
	return &InputRetrySeed{inputs: recovered}
}

func (c *Component) forgetRecovered(url string, source *purehttpstore.StagedSource) {
	o := c.outages
	o.mu.Lock()
	defer o.mu.Unlock()
	if input, exists := o.recovered[url]; exists && input.source == source {
		delete(o.recovered, url)
	}
}

func (c *Component) stopOutages() {
	o := c.outages
	o.mu.Lock()
	o.stopped = true
	for url := range o.sources {
		o.stopLocked(url)
	}
	clear(o.recovered)
	o.hold = nil
	o.mu.Unlock()
	o.probes.Wait()
}
