// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package configpublisher

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/throttle"
)

// syncBuffer is an io.Writer safe for the worker goroutine to write while the
// test reads.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// publishWorker drains deploy-driven work ahead of validation-driven work.
//
// The two used to be equal cases of one select, and a select picks uniformly at
// random among ready cases — so with validation publishes arriving
// continuously, a deployed item could sit unread. Measured on a real run: 5.2s,
// with three validation publishes going out ahead of it.
//
// That window is not cosmetic. `status.deployedToPods[].checksum` is written by
// an independent path, so for as long as the deployed item waits, the CR
// advertises a pod running a config whose content `spec` has not published — a
// checksum no reader can resolve. That is what strands `waitForControllerDeployed`
// in the e2e suite, and what a GitOps reader would see too.
func TestPublishWorker_DrainsDeployedWorkFirst(t *testing.T) {
	logBuf := &syncBuffer{}

	// Every item carries the same content checksum as lastPublishedChecksum, so
	// skipIfAlreadyPublished short-circuits each one *after* processPublishWork
	// logs "Processing publish work" — the worker loop is exercised end to end
	// while the (nil) publisher is never reached. The log order is therefore the
	// order the worker took items off the channels.
	const published = "same-content"
	c := &Component{
		logger: slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		})),
		renderedConfigs:       make(map[string]*renderedConfigEntry),
		publishWork:           make(chan *publishWorkItem, 4),
		deployedTrigger:       make(chan struct{}, 1),
		lastPublishedChecksum: published,
		publishThrottle:       throttle.New(time.Hour),
	}

	item := func(id string, deployDriven bool) *publishWorkItem {
		return &publishWorkItem{
			correlationID:  id,
			templateConfig: &v1alpha1.HAProxyTemplateConfig{},
			entry:          &renderedConfigEntry{contentChecksum: published},
			deployDriven:   deployDriven,
		}
	}

	// Both channels ready before the worker starts. Under a fair select the
	// validation items would win roughly half the time.
	c.publishWork <- item("validation-1", false)
	c.publishWork <- item("validation-2", false)
	c.enqueueDeployed(item("deployed:abc", true))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		c.publishWorker(ctx)
		close(done)
	}()
	defer func() {
		cancel()
		c.publishThrottle.Stop()
		<-done
	}()

	require.Eventually(t, func() bool {
		return len(processedOrder(logBuf.String())) >= 3
	}, 5*time.Second, 5*time.Millisecond, "publishWorker did not process all queued work")

	order := processedOrder(logBuf.String())
	assert.Equal(t, "deployed:abc", order[0],
		"the deployed checksum must be published before validation work, since "+
			"status.deployedToPods already advertises it")
}

// processedOrder extracts the correlation IDs of "Processing publish work"
// records, in the order the worker emitted them.
func processedOrder(logText string) []string {
	var ids []string
	for line := range strings.SplitSeq(logText, "\n") {
		if !strings.Contains(line, `msg="Processing publish work"`) {
			continue
		}
		_, rest, found := strings.Cut(line, "correlation_id=")
		if !found {
			continue
		}
		id, _, _ := strings.Cut(rest, " ")
		ids = append(ids, strings.Trim(id, `"`))
	}
	return ids
}

// Every distinct deployed checksum stays queued, even when a newer one arrives
// behind it.
//
// The old size-1 channel coalesced latest-wins, which is right for validation
// publishes and wrong here: `status.deployedToPods[].checksum` is written by an
// independent path, so a dropped deployed checksum leaves the CR advertising a
// config `spec.content` never carried — a checksum no reader and no watcher can
// resolve. Measured on a real run: 1 checksum in 31 was dropped, which is what
// strands `waitForControllerDeployed`.
func TestEnqueueDeployed_KeepsEveryDistinctChecksum(t *testing.T) {
	c := &Component{
		logger:          slog.New(slog.NewTextHandler(&syncBuffer{}, nil)),
		deployedTrigger: make(chan struct{}, 1),
	}

	deployed := func(checksum string) *publishWorkItem {
		return &publishWorkItem{
			correlationID: "deployed:" + checksum,
			entry:         &renderedConfigEntry{contentChecksum: checksum},
			deployDriven:  true,
		}
	}

	queued := func() []string {
		c.deployedPendingMu.Lock()
		defer c.deployedPendingMu.Unlock()
		out := make([]string, 0, len(c.deployedPending))
		for _, w := range c.deployedPending {
			out = append(out, w.entry.contentChecksum)
		}
		return out
	}

	// Three distinct deployed checksums back to back — the shape that
	// previously dropped the first two.
	c.enqueueDeployed(deployed("aaa"))
	c.enqueueDeployed(deployed("bbb"))
	c.enqueueDeployed(deployed("ccc"))
	assert.Equal(t, []string{"aaa", "bbb", "ccc"}, queued(),
		"no distinct deployed checksum may be displaced by a newer one")

	// A repeat replaces in place: repeats still collapse, only distinct
	// checksums are kept, and the arrival order is preserved.
	c.enqueueDeployed(deployed("bbb"))
	assert.Equal(t, []string{"aaa", "bbb", "ccc"}, queued(),
		"a repeat of a queued checksum must not grow or reorder the queue")

	// takeDeployed pops oldest-first and empties.
	assert.Equal(t, "aaa", c.takeDeployed().entry.contentChecksum)
	assert.Equal(t, "bbb", c.takeDeployed().entry.contentChecksum)
	assert.Equal(t, "ccc", c.takeDeployed().entry.contentChecksum)
	assert.Nil(t, c.takeDeployed(), "an empty queue yields nil, not a panic")
}

// A deployed render that arrives while the throttle gate is CLOSED must go back
// on the queue, not into a one-slot buffer.
//
// This is the path the direct-queue test above does not reach, and the one that
// silently undid the guarantee: draining a burst closes the gate on the first
// publish, so with a single slot every remaining deployed checksum overwrote it
// and only the newest survived — reintroducing the drop the queue exists to
// prevent.
func TestProcessPublishWork_ThrottledDeployedWorkStaysQueued(t *testing.T) {
	c := &Component{
		logger:          slog.New(slog.NewTextHandler(&syncBuffer{}, nil)),
		renderedConfigs: make(map[string]*renderedConfigEntry),
		deployedTrigger: make(chan struct{}, 1),
		// A throttle that has already fired is in its refractory period, so
		// Available() is false and processPublishWork takes the buffering path.
		publishThrottle:       throttle.New(time.Hour),
		lastPublishedChecksum: "none",
	}
	defer c.publishThrottle.Stop()
	c.publishThrottle.MarkFired()
	require.False(t, c.publishThrottle.Available(), "premise: the gate must be closed")

	deployed := func(checksum string) *publishWorkItem {
		return &publishWorkItem{
			correlationID:  "deployed:" + checksum,
			templateConfig: &v1alpha1.HAProxyTemplateConfig{},
			entry:          &renderedConfigEntry{contentChecksum: checksum},
			deployDriven:   true,
		}
	}

	// Three distinct deployed checksums hit the closed gate back to back.
	c.processPublishWork(context.Background(), deployed("aaa"))
	c.processPublishWork(context.Background(), deployed("bbb"))
	c.processPublishWork(context.Background(), deployed("ccc"))

	c.deployedPendingMu.Lock()
	queued := make([]string, 0, len(c.deployedPending))
	for _, w := range c.deployedPending {
		queued = append(queued, w.entry.contentChecksum)
	}
	c.deployedPendingMu.Unlock()

	assert.ElementsMatch(t, []string{"aaa", "bbb", "ccc"}, queued,
		"a closed throttle gate must not collapse distinct deployed checksums")
}

// A leadership loss drops queued deployed work.
//
// The Component outlives a leadership transition, so anything queued under the
// previous term would otherwise publish after the term ended — writing a spec
// the new leader never deployed. The one-slot buffer this queue replaced was
// cleared here for exactly that reason.
func TestHandleLostLeadership_ClearsDeployedQueue(t *testing.T) {
	c := &Component{
		logger:          slog.New(slog.NewTextHandler(&syncBuffer{}, nil)),
		renderedConfigs: make(map[string]*renderedConfigEntry),
		deployedTrigger: make(chan struct{}, 1),
	}
	c.enqueueDeployed(&publishWorkItem{
		correlationID: "deployed:stale",
		entry:         &renderedConfigEntry{contentChecksum: "stale"},
		deployDriven:  true,
	})
	require.Equal(t, 1, c.deployedQueueDepth(), "premise: something is queued")

	c.handleLostLeadership(nil)

	assert.Equal(t, 0, c.deployedQueueDepth(),
		"queued deployed work must not survive a leadership transition")
}
