// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package configpublisher

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/throttle"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/generated/clientset/versioned/fake"
	configpublisherk8s "gitlab.com/haproxy-haptic/haptic/pkg/k8s/configpublisher"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

// The worker must not spin while a deployed item waits for the throttle gate.
//
// The top-of-loop drain exists so deploy work never queues behind validation
// work. But processPublishWork puts a deploy item straight back at the FRONT of
// the queue when the gate is closed, so draining unconditionally pops the same
// item again on the very next iteration — a loop with nothing to block on,
// pinning a core for the whole refractory window.
//
// It fires in the ordinary burst this queue was built for: the first item
// publishes and closes the gate, the rest arrive during refractory.
func TestPublishWorker_DoesNotSpinWhileThrottleGateIsClosed(t *testing.T) {
	logBuf := &syncBuffer{}

	c := &Component{
		logger: slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		})),
		renderedConfigs: make(map[string]*renderedConfigEntry),
		publishWork:     make(chan *publishWorkItem, 4),
		deployedTrigger: make(chan struct{}, 1),
		publishThrottle: throttle.New(time.Hour),
		// The shutdown flush publishes whatever is still queued, so the
		// publisher must be real enough to be called.
		publisher: configpublisherk8s.NewWithListers(
			k8sfake.NewClientset(), fake.NewSimpleClientset(), nil,
			slog.New(slog.NewTextHandler(logBuf, nil))),
		eventBus: busevents.NewEventBus(8),
	}

	// Close the gate, then queue deploy work behind it.
	c.publishThrottle.MarkFired()
	require.False(t, c.publishThrottle.Available(), "gate must be closed for this test to mean anything")

	c.enqueueDeployed(&publishWorkItem{
		correlationID:  "deployed:abc",
		templateConfig: &v1alpha1.HAProxyTemplateConfig{},
		entry:          &renderedConfigEntry{contentChecksum: "abc"},
		deployDriven:   true,
	})

	ctx, cancel := context.WithCancel(context.Background())
	go c.publishWorker(ctx)

	// Long enough that a spinning loop racks up thousands of iterations.
	time.Sleep(200 * time.Millisecond)

	// Assert BEFORE cancelling: the shutdown path flushes whatever is queued,
	// so cancelling first would empty the queue and hide the second assertion.
	//
	// Each spin iteration re-enters processPublishWork, which logs one buffering
	// record before requeueing. One or two is the honest ceiling for a worker
	// that blocks; the broken version produced these as fast as the CPU allowed.
	buffered := strings.Count(logBuf.String(), `msg="Throttling CRD publish, buffering for later"`)
	assert.LessOrEqual(t, buffered, 2,
		"publishWorker re-processed the same deploy item %d times in 200ms — it is "+
			"spinning on requeue instead of waiting for the throttle to reopen", buffered)

	// The item must still be queued, not dropped, and not published early.
	assert.Equal(t, 1, c.deployedQueueDepth(),
		"the deploy item must stay queued while the gate is closed")

	cancel()
}

// Declining to drain must not strand the item.
//
// enqueueDeployed only signals deployedTrigger; it never schedules a flush. So a
// worker that simply skips the drain under a closed gate would fall through to
// the select and wait on FiredCh that nobody armed — trading a busy-spin for a
// stall, which is worse: the CR keeps advertising a deployed checksum whose
// content never publishes.
func TestPublishWorker_QueuedDeployWorkStillFlushesWhenGateReopens(t *testing.T) {
	logBuf := &syncBuffer{}

	const published = "same-content"
	c := &Component{
		logger: slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		})),
		renderedConfigs: make(map[string]*renderedConfigEntry),
		publishWork:     make(chan *publishWorkItem, 4),
		deployedTrigger: make(chan struct{}, 1),
		// Same checksum as the item, so skipIfAlreadyPublished short-circuits
		// after "Processing publish work" is logged and the nil publisher is
		// never reached.
		lastPublishedChecksum: published,
		publishThrottle:       throttle.New(50 * time.Millisecond),
	}

	c.publishThrottle.MarkFired()
	require.False(t, c.publishThrottle.Available())

	c.enqueueDeployed(&publishWorkItem{
		correlationID:  "deployed:abc",
		templateConfig: &v1alpha1.HAProxyTemplateConfig{},
		entry:          &renderedConfigEntry{contentChecksum: published},
		deployDriven:   true,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.publishWorker(ctx)

	// The queue emptying is the behaviour that matters: the item left the queue
	// on the flush path rather than sitting there forever. (It leaves via
	// flushPendingPublish, which logs "Flushing throttled CRD publish" — not
	// processPublishWork's "Processing publish work" — so assert on the queue,
	// not on that log line.)
	require.Eventually(t, func() bool {
		return c.deployedQueueDepth() == 0
	}, 5*time.Second, 5*time.Millisecond,
		"the queued deploy item was never drained after the gate reopened — "+
			"skipping the top-of-loop drain stranded it instead of letting FiredCh "+
			"drive flushPendingPublish")

	assert.Contains(t, logBuf.String(), "deployed:abc",
		"the drained item must be the deploy-driven one that was queued")
}
