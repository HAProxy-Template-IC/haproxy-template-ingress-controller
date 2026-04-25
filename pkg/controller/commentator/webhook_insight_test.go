// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package commentator

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ctlevents "gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
)

// webhookInsight produces operator-facing log messages for the four
// webhook validation event types. The TRUNCATION contract on
// WebhookValidationDeniedEvent.Reason is particularly load-bearing:
//
//   * The log message is truncated to maxErrorPreviewLength (80) with
//     a "..." suffix indicating truncation. Without this cap, a
//     malicious or just verbose deny reason (e.g. multi-page YAML
//     diffs) would spam logs and hide other entries.
//
//   * The structured `reason` attribute MUST keep the full untruncated
//     reason. Operators need the full reason for triage; truncating
//     the attr too would lose the context permanently.
//
//   * The "..." suffix in the log message is a load-bearing signal
//     that the message is truncated — operators look for it to know
//     they should consult the structured attr or replay the event for
//     the full text. A regression that dropped the "..." would
//     silently mislead operators into thinking the visible reason is
//     complete.
//
// Plus per-event-type formatting tests for the other three webhook
// events (request, allowed, error) — these flow into the same log
// stream operators triage by, so format regressions break grep
// patterns and structured-attr correlation.

// whECommentator returns a minimal EventCommentator with just the
// ringBuffer field. webhookInsight is a pure formatter that doesn't
// touch eventBus. The wh-prefix avoids future collision with
// emptyECommentator from sibling test files when this branch lands
// alongside others.
func whECommentator() *EventCommentator {
	return &EventCommentator{ringBuffer: NewRingBuffer(8)}
}

func TestWebhookInsight_DeniedEvent_TruncatesLogPreservesAttr(t *testing.T) {
	ec := whECommentator()

	// Build a reason long enough to trigger truncation. Choose 200
	// chars (well over maxErrorPreviewLength=80) with a recognizable
	// suffix so we can verify the LOG message was truncated and the
	// ATTR retained the full text.
	const fullReason = "validation failed: detailed schema error at .spec.haproxyConfig.template " +
		"line 42: unknown filter 'foobaz'; did you mean 'foobar'? See docs at " +
		"https://example.com/docs/filters for the full filter catalog."
	require.Greater(t, len(fullReason), maxErrorPreviewLength,
		"sanity: test fixture must be longer than the truncation threshold")

	evt := ctlevents.NewWebhookValidationDeniedEvent(
		"req-uid-123", "Ingress", "my-ingress", "default", fullReason)

	insight, attrs := ec.webhookInsight(evt, nil)

	// (1) Log message truncation: the visible reason must be
	// shorter than the full reason AND end with "..." so operators
	// see they need the structured attr for the full text.
	assert.Less(t, len(insight), len(fullReason)+100, // +100 for prefix overhead
		"log message must be truncated when reason exceeds %d chars; "+
			"a regression that dropped the cap would let multi-page deny "+
			"reasons spam logs and hide other entries",
		maxErrorPreviewLength)
	assert.True(t, strings.Contains(insight, "..."),
		"truncated message MUST contain '...' suffix as the load-bearing "+
			"signal that the visible text is incomplete; without it operators "+
			"would think the visible reason is the full one and miss critical "+
			"context")
	assert.NotContains(t, insight, "filter catalog",
		"the tail of the long reason must NOT appear in the truncated log "+
			"message — proves truncation actually fired (rather than just "+
			"appending '...' to the full text, which would defeat the cap)")

	// (2) Structured attribute completeness: reason attr must be
	// the FULL untruncated text. This is what operators dig into for
	// triage; truncating it would lose the context permanently.
	reasonAttr := findAttr(attrs, "reason")
	require.NotNil(t, reasonAttr,
		"reason attribute must always be present in structured args")
	assert.Equal(t, fullReason, reasonAttr,
		"the structured 'reason' attribute MUST contain the full untruncated "+
			"reason — operators need this for triage. A regression that also "+
			"truncated the attr would lose the deny context permanently")

	// (3) Other identifying attrs must be present for log filtering.
	for _, key := range []string{"request_uid", "kind", "name", "namespace"} {
		assert.NotNil(t, findAttr(attrs, key),
			"DeniedEvent must expose %q attr for log filtering / metrics", key)
	}
}

func TestWebhookInsight_DeniedEvent_ShortReasonNotTruncated(t *testing.T) {
	// Pin the inverse contract: a reason BELOW the threshold must
	// pass through verbatim with NO "..." suffix (so the absence of
	// "..." reliably signals "complete reason visible").
	ec := whECommentator()
	const shortReason = "validation failed: bad request"
	require.Less(t, len(shortReason), maxErrorPreviewLength,
		"sanity: test fixture must be UNDER the truncation threshold")

	evt := ctlevents.NewWebhookValidationDeniedEvent(
		"req-uid-456", "Service", "svc", "default", shortReason)

	insight, _ := ec.webhookInsight(evt, nil)

	assert.Contains(t, insight, shortReason,
		"a short reason must appear verbatim in the log message")
	assert.NotContains(t, insight, "...",
		"a short (under-threshold) reason MUST NOT carry the truncation "+
			"marker — operators rely on the absence of '...' as a signal "+
			"that the visible reason is complete; a regression that always "+
			"appended '...' would force operators to dig into structured "+
			"attrs for every deny, defeating the log-readability optimization")
}

func TestWebhookInsight_RequestEvent_FormatsWithIdentifiers(t *testing.T) {
	ec := whECommentator()
	evt := ctlevents.NewWebhookValidationRequestEvent(
		"req-1", "Ingress", "my-ingress", "default", "CREATE")

	insight, attrs := ec.webhookInsight(evt, nil)

	// Operator-facing message must include operation + kind + ns/name
	assert.Contains(t, insight, "Webhook validation request:",
		"message must start with the documented prefix for log scrapers")
	assert.Contains(t, insight, "CREATE",
		"operation must surface so operators see what kind of admission is happening")
	assert.Contains(t, insight, "Ingress",
		"resource kind must appear")
	assert.Contains(t, insight, "default/my-ingress",
		"namespaced name must appear in canonical 'namespace/name' form")

	// All identifying attrs present for structured filtering.
	for _, key := range []string{"request_uid", "kind", "name", "namespace", "operation"} {
		assert.NotNil(t, findAttr(attrs, key),
			"RequestEvent must expose %q attr for log filtering", key)
	}
}

func TestWebhookInsight_AllowedEvent_NoSensitiveData(t *testing.T) {
	ec := whECommentator()
	evt := ctlevents.NewWebhookValidationAllowedEvent(
		"req-2", "Ingress", "my-ingress", "default")

	insight, _ := ec.webhookInsight(evt, nil)

	assert.Contains(t, insight, "Webhook validation allowed:",
		"message must start with the documented prefix")
	assert.Contains(t, insight, "default/my-ingress",
		"namespaced name must appear so operators can correlate "+
			"allows with subsequent reconciliation activity")
}

func TestWebhookInsight_ErrorEvent_SurfacesError(t *testing.T) {
	ec := whECommentator()
	evt := ctlevents.NewWebhookValidationErrorEvent(
		"req-3", "Ingress", "validator timeout after 10s")

	insight, attrs := ec.webhookInsight(evt, nil)

	assert.Contains(t, insight, "Webhook validation error",
		"message must start with the documented prefix for error events")
	assert.Contains(t, insight, "validator timeout after 10s",
		"the inner error must surface in the operator-facing message — "+
			"a regression that dropped it would force operators to dig into "+
			"structured attrs for every webhook error")
	assert.Equal(t, "validator timeout after 10s", findAttr(attrs, "error"),
		"the structured error attribute must be the full inner error verbatim")
}

func TestWebhookInsight_UnknownEventTypeReturnsEmpty(t *testing.T) {
	// The default arm returns an empty insight and the attrs unchanged.
	// This is what allows the higher-level dispatcher to "skip" events
	// the webhook insight doesn't handle; a regression that returned a
	// non-empty insight here would emit garbage log lines.
	ec := whECommentator()
	// TemplateRenderedEvent is handled by templateInsight, not
	// webhookInsight — it's "unknown" to this function.
	other := ctlevents.NewTemplateRenderedEvent("", nil, nil, 0, 1, "", "", true)

	insight, attrs := ec.webhookInsight(other, []any{"existing", "attr"})

	assert.Empty(t, insight,
		"unhandled event types must produce an EMPTY insight — the "+
			"dispatcher uses this as the signal to skip; a non-empty insight "+
			"would emit garbage log lines for events this function doesn't own")
	assert.Equal(t, []any{"existing", "attr"}, attrs,
		"the attrs slice must be returned UNCHANGED on the default arm — "+
			"a regression that mutated it would corrupt the dispatcher's "+
			"running attribute accumulator")
}

// findAttr walks a slog-style key/value attribute slice looking for
// the value of the named key. Returns nil if not found.
func findAttr(attrs []any, key string) any {
	for i := 0; i+1 < len(attrs); i += 2 {
		k, ok := attrs[i].(string)
		if !ok {
			continue
		}
		if k == key {
			return attrs[i+1]
		}
	}
	return nil
}
