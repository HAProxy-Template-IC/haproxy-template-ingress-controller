// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package configpublisher

import (
	"log/slog"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

// deployedQueueComponent builds a Component with just the deployed-publish
// queue wired, which is all pruneSupersededDeployedLocked touches.
func deployedQueueComponent() *Component {
	return &Component{
		logger:                slog.New(slog.NewTextHandler(&syncBuffer{}, nil)),
		deployedTrigger:       make(chan struct{}, 1),
		deployedChecksumByPod: make(map[podAuthorityKey]string),
	}
}

func deployedItem(checksum string) *publishWorkItem {
	return &publishWorkItem{
		correlationID: "deployed:" + checksum,
		entry:         &renderedConfigEntry{contentChecksum: checksum, config: "cfg-" + checksum},
		deployDriven:  true,
	}
}

func queuedChecksums(c *Component) []string {
	c.deployedPendingMu.Lock()
	defer c.deployedPendingMu.Unlock()
	out := make([]string, 0, len(c.deployedPending))
	for _, w := range c.deployedPending {
		out = append(out, w.entry.contentChecksum)
	}
	return out
}

// A checksum a pod still reports is never pruned — that is the guarantee the
// queue exists for. Only the ones the whole fleet moved past are dropped.
func TestPruneSupersededDeployed_KeepsEveryChecksumAPodStillReports(t *testing.T) {
	c := deployedQueueComponent()

	for _, checksum := range []string{"aaa", "bbb", "ccc", "ddd"} {
		c.enqueueDeployed(deployedItem(checksum))
	}
	assert.Equal(t, []string{"aaa", "bbb", "ccc", "ddd"}, queuedChecksums(c),
		"nothing may be pruned before any pod has reported")

	// One pod lags on "aaa" while another has reached "ddd". Only the two
	// checksums no pod is at may go.
	c.recordPodChecksum("haptic", "haproxy-1", "aaa")
	c.recordPodChecksum("haptic", "haproxy-2", "ddd")
	c.enqueueDeployed(deployedItem("eee"))

	assert.Equal(t, []string{"aaa", "ddd", "eee"}, queuedChecksums(c),
		"a checksum a pod still reports must survive; only superseded ones are dropped")
}

// Entries newer than the newest reported checksum are always kept, so a deploy
// whose ConfigAppliedToPodEvent has not arrived yet can never be pruned.
func TestPruneSupersededDeployed_KeepsEntriesNewerThanTheNewestReported(t *testing.T) {
	c := deployedQueueComponent()

	for _, checksum := range []string{"aaa", "bbb", "ccc", "ddd"} {
		c.enqueueDeployed(deployedItem(checksum))
	}
	// The fleet is only as far as "bbb"; "ccc" and "ddd" are in flight.
	c.recordPodChecksum("haptic", "haproxy-1", "bbb")
	c.enqueueDeployed(deployedItem("eee"))

	assert.Equal(t, []string{"bbb", "ccc", "ddd", "eee"}, queuedChecksums(c),
		"only entries older than the newest reported checksum may be pruned")
}

// With no pod report at all, the queue is left exactly as it was — the prune
// can never be the thing that drops a checksum it cannot prove superseded.
func TestPruneSupersededDeployed_NoReportsPrunesNothing(t *testing.T) {
	c := deployedQueueComponent()

	for i := range 20 {
		c.enqueueDeployed(deployedItem("sum" + strconv.Itoa(i)))
	}

	assert.Len(t, queuedChecksums(c), 20,
		"without pod reports there is no evidence of supersession, so nothing may be dropped")
}

// The queue is bounded by the number of distinct checksums live across the
// fleet, not by the deploy rate. This is the leak fix: the runtime-raw lane
// skips minDeploymentInterval, so deploys arrive far faster than the
// one-per-configPublishInterval drain.
func TestPruneSupersededDeployed_BoundedByFleetNotDeployRate(t *testing.T) {
	c := deployedQueueComponent()

	// 500 deploys, with the single-pod fleet tracking each one as it lands.
	for i := range 500 {
		checksum := "sum" + strconv.Itoa(i)
		c.enqueueDeployed(deployedItem(checksum))
		c.recordPodChecksum("haptic", "haproxy-1", checksum)
	}
	// One more so the last reported checksum is no longer the newest entry.
	c.enqueueDeployed(deployedItem("final"))

	queued := queuedChecksums(c)
	assert.Equal(t, []string{"sum499", "final"}, queued,
		"a one-pod fleet must leave at most its live checksum plus in-flight entries")
}

// A departed pod must not pin its checksum in the queue forever.
func TestPruneSupersededDeployed_ForgetsTerminatedPods(t *testing.T) {
	c := deployedQueueComponent()

	c.enqueueDeployed(deployedItem("aaa"))
	c.enqueueDeployed(deployedItem("bbb"))
	c.recordPodChecksum("haptic", "haproxy-1", "aaa")
	c.recordPodChecksum("haptic", "haproxy-2", "bbb")
	assert.Equal(t, []string{"aaa", "bbb"}, queuedChecksums(c))

	c.forgetPodChecksum("haptic", "haproxy-1")
	c.enqueueDeployed(deployedItem("ccc"))

	assert.Equal(t, []string{"bbb", "ccc"}, queuedChecksums(c),
		"a terminated pod's checksum must stop holding its queue entry")
}

// Pruning must release the dropped entries' rendered bytes, not just reslice
// past them — the whole point is that each entry holds a full config.
func TestPruneSupersededDeployed_ReleasesDroppedEntries(t *testing.T) {
	c := deployedQueueComponent()

	for _, checksum := range []string{"aaa", "bbb", "ccc"} {
		c.enqueueDeployed(deployedItem(checksum))
	}
	c.recordPodChecksum("haptic", "haproxy-1", "ccc")
	c.enqueueDeployed(deployedItem("ddd"))

	c.deployedPendingMu.Lock()
	defer c.deployedPendingMu.Unlock()
	tail := c.deployedPending[len(c.deployedPending):cap(c.deployedPending)]
	for i, w := range tail {
		assert.Nil(t, w, "dropped entry %d still referenced from the queue's backing array", i)
	}
}
