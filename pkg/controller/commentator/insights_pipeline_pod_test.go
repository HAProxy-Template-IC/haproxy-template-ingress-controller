// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package commentator

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	busevents "gitlab.com/haproxy-haptic/haptic/pkg/events"
)

// podInsight has a non-obvious correlation branch for
// HAProxyPodsDiscoveredEvent: when more than one discovery has been
// seen inside the discoveryLookbackWindow, the insight string is
// suffixed with " (pods changed)" to signal a transition to operators
// reading logs.
//
// The existing TestEventCommentator_GenerateInsight_HAProxyPodEvents
// test calls generateInsight directly on a fresh commentator with an
// empty ring buffer — it only ever exercises the "no change suffix"
// branch. The "(pods changed)" branch (which fires once two
// discoveries land within the lookback window) had no direct coverage.
//
// The branch matters because:
//
//   - The suffix is the ONLY signal in the log that the pod set
//     actually changed (versus a redundant re-discovery from the
//     poller). A regression that dropped the suffix would silently
//     hide pod scaling / rolling-update events from operators.
//   - The threshold is ">1", not ">0". This is load-bearing: the
//     CURRENT discovery counts toward the buffer (processEvent adds
//     it before logWithInsight runs), so a refactor that flipped to
//     ">0" would suffix EVERY discovery — including the very first
//     one — making the signal useless.
//
// Pin both branches by going through processEvent end-to-end so the
// ring-buffer-add ordering matches production.
func TestPodInsight_DiscoveredEvent_PodsChangedSuffix(t *testing.T) {
	t.Run("first discovery has NO '(pods changed)' suffix", func(t *testing.T) {
		bus := busevents.NewEventBus(100)
		ec := NewEventCommentator(bus, slog.Default(), 100)

		// Single discovery → ring buffer holds 1 entry → no suffix.
		event := events.NewHAProxyPodsDiscoveredEvent(
			[]dataplane.Endpoint{{URL: "http://pod1:5555"}}, 1,
		)
		ec.processEvent(event)

		insight, _ := ec.generateInsight(event)
		assert.NotContains(t, insight, "(pods changed)",
			"the very first discovery must NOT carry the '(pods changed)' suffix; "+
				"otherwise every controller startup would log a misleading change signal")
		assert.Contains(t, insight, "1 instances",
			"insight must include the pod count regardless of change-suffix branch")
	})

	t.Run("second discovery within lookback window adds '(pods changed)' suffix", func(t *testing.T) {
		bus := busevents.NewEventBus(100)
		ec := NewEventCommentator(bus, slog.Default(), 100)

		// First discovery — adds to ring buffer.
		first := events.NewHAProxyPodsDiscoveredEvent(
			[]dataplane.Endpoint{{URL: "http://pod1:5555"}}, 1,
		)
		ec.processEvent(first)

		// Second discovery — buffer now has 2 entries when generateInsight
		// runs, triggering the change-suffix branch.
		second := events.NewHAProxyPodsDiscoveredEvent(
			[]dataplane.Endpoint{
				{URL: "http://pod1:5555"},
				{URL: "http://pod2:5555"},
			}, 2,
		)
		ec.processEvent(second)

		insight, attrs := ec.generateInsight(second)
		assert.Contains(t, insight, "(pods changed)",
			"a second discovery within the lookback window must trigger the '(pods changed)' suffix; "+
				"this is the ONLY log signal that distinguishes a real pod-set change from a redundant poll")
		assert.Contains(t, insight, "2 instances")
		assertContainsAttr(t, attrs, "count", 2)
	})

	t.Run("subsequent discoveries continue to carry the suffix", func(t *testing.T) {
		// Pin that the suffix is sticky once the buffer has two
		// entries — a refactor that "compared only the last two" would
		// stop suffixing after the third discovery, hiding ongoing
		// pod churn.
		bus := busevents.NewEventBus(100)
		ec := NewEventCommentator(bus, slog.Default(), 100)

		for i := 1; i <= 3; i++ {
			ec.processEvent(events.NewHAProxyPodsDiscoveredEvent(
				[]dataplane.Endpoint{{URL: "http://pod:5555"}}, i,
			))
		}

		// Generate insight for a fourth discovery (buffer holds 4).
		fourth := events.NewHAProxyPodsDiscoveredEvent(
			[]dataplane.Endpoint{{URL: "http://pod:5555"}}, 4,
		)
		ec.processEvent(fourth)

		insight, _ := ec.generateInsight(fourth)
		require.Contains(t, insight, "(pods changed)",
			"subsequent discoveries within the lookback window must keep the suffix")
	})
}

// HAProxyPodTerminatedEvent has no correlation logic — it always
// emits the same "HAProxy pod terminated: {ns}/{name}" message. Pin
// the format so a refactor can't reorder namespace/name (which would
// break log scrapers parsing the standard "namespace/name" prefix).
func TestPodInsight_TerminatedEvent_FormatStability(t *testing.T) {
	bus := busevents.NewEventBus(100)
	ec := NewEventCommentator(bus, slog.Default(), 100)

	event := events.NewHAProxyPodTerminatedEvent("haproxy-abc", "haptic-system", "")

	insight, attrs := ec.generateInsight(event)

	// Format must be "namespace/name" — log scrapers and downstream
	// alert templates parse this exact shape. A refactor that swapped
	// to "name in namespace" would break the parsers without any
	// test failure inside the commentator package.
	assert.Contains(t, insight, "haptic-system/haproxy-abc",
		"terminated insight must use 'namespace/name' format; reordering would break log scrapers")
	assertContainsAttr(t, attrs, "pod_name", "haproxy-abc")
	assertContainsAttr(t, attrs, "pod_namespace", "haptic-system")
}

// HAProxyPodRejectedEvent also has no correlation logic — pin the
// "HAProxy pod rejected: {pod} (reason: {reason})" format. The reason
// label flows through to haptic_haproxy_pods_rejected_total{reason}
// so log queries and Prometheus alerts share the same enum values
// (version_mismatch_older / _newer / version_check_failed). A refactor
// that capitalised or rewrote the reason string would silently break
// log-to-metric correlation.
func TestPodInsight_RejectedEvent_FormatStability(t *testing.T) {
	bus := busevents.NewEventBus(100)
	ec := NewEventCommentator(bus, slog.Default(), 100)

	event := events.NewHAProxyPodRejectedEvent("haproxy-xyz", "version_mismatch_older")

	insight, attrs := ec.generateInsight(event)

	assert.Contains(t, insight, "haproxy-xyz",
		"rejected insight must include the pod name verbatim for ELK correlation")
	assert.Contains(t, insight, "version_mismatch_older",
		"reason must round-trip verbatim — alert templates and dashboards key on the exact enum value")
	assertContainsAttr(t, attrs, "pod_name", "haproxy-xyz")
	assertContainsAttr(t, attrs, "reason", "version_mismatch_older")
}
