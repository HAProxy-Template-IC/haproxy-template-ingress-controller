// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package commentator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ctlevents "gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	k8stypes "gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

// resourceInsight produces operator-facing log messages for K8s
// resource-watcher events. Three event types, four distinct message
// shapes, no direct test coverage. The most load-bearing branch is
// the IsInitialSync split on ResourceIndexUpdatedEvent:
//
//   * IsInitialSync=true  → "Resource index loading: ..." prefix +
//     `initial_sync=true` attribute. Operators filter startup logs
//     by this prefix to suppress the noisy bulk-load phase.
//
//   * IsInitialSync=false → "Resource index updated: ..." prefix +
//     `initial_sync=false` attribute. These are real-time changes
//     that may signal an incident; operators page on volume of
//     these.
//
// A regression that flipped or merged the two prefixes would either:
//   - flood incident dashboards with bulk-load noise (false alarms
//     during pod restarts), OR
//   - hide real-time change spikes inside startup logs (silent
//     incident).
//
// Tests pin both branches plus the IndexSynchronized aggregate
// counter (which is operator-facing as the "ready" signal —
// dashboards alert when this never fires).

func TestResourceInsight_ResourceIndexUpdated_InitialSyncPrefix(t *testing.T) {
	tests := []struct {
		name              string
		isInitialSync     bool
		wantPrefix        string
		notWantPrefix     string
		wantSyncAttrValue any
	}{
		{
			name:              "initial sync uses 'loading' prefix and sets initial_sync=true",
			isInitialSync:     true,
			wantPrefix:        "Resource index loading:",
			notWantPrefix:     "Resource index updated:",
			wantSyncAttrValue: true,
		},
		{
			name:              "real-time update uses 'updated' prefix and sets initial_sync=false",
			isInitialSync:     false,
			wantPrefix:        "Resource index updated:",
			notWantPrefix:     "Resource index loading:",
			wantSyncAttrValue: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ec := riECommentator()
			evt := ctlevents.NewResourceIndexUpdatedEvent("ingresses",
				k8stypes.ChangeStats{
					Created:       3,
					Modified:      1,
					Deleted:       2,
					IsInitialSync: tt.isInitialSync,
				})

			insight, attrs := ec.resourceInsight(evt, nil)

			assert.True(t, hasPrefix(insight, tt.wantPrefix),
				"insight must start with %q so operators filter the right log "+
					"stream; a regression that swapped the prefix would either "+
					"flood incident dashboards with bulk-load noise OR hide "+
					"real-time changes in startup logs",
				tt.wantPrefix)
			assert.False(t, hasPrefix(insight, tt.notWantPrefix),
				"insight must NOT use the OTHER prefix %q — strict separation "+
					"is what lets operators reliably filter by activity phase",
				tt.notWantPrefix)

			// Counts must always appear so operators can grep for spike volume.
			assert.Contains(t, insight, "created=3",
				"created count must surface in the message text")
			assert.Contains(t, insight, "modified=1",
				"modified count must surface in the message text")
			assert.Contains(t, insight, "deleted=2",
				"deleted count must surface in the message text")

			// Structured initial_sync attribute must match the branch.
			syncAttr := riAttr(attrs, "initial_sync")
			require.NotNil(t, syncAttr,
				"every ResourceIndexUpdatedEvent insight must expose initial_sync "+
					"in structured attrs so dashboards can group/filter by phase")
			assert.Equal(t, tt.wantSyncAttrValue, syncAttr,
				"initial_sync attr must match the branch — a regression where "+
					"text and attribute disagreed would break log↔metrics correlation")
		})
	}
}

func TestResourceInsight_ResourceSyncCompleteFormatsCount(t *testing.T) {
	ec := riECommentator()
	evt := ctlevents.NewResourceSyncCompleteEvent("services", 17)

	insight, attrs := ec.resourceInsight(evt, nil)

	assert.Contains(t, insight, "Initial sync complete for services",
		"insight must include the resource type so operators see WHICH "+
			"watcher just finished its initial sync")
	assert.Contains(t, insight, "(17 resources)",
		"the initial count must be in the message — operators use it to "+
			"verify the watcher saw expected cluster state at startup")
	assert.Equal(t, "services", riAttr(attrs, "resource_type"))
	assert.Equal(t, 17, riAttr(attrs, "initial_count"))
}

func TestResourceInsight_IndexSynchronized_AggregatesCountsAcrossTypes(t *testing.T) {
	// IndexSynchronized is the "ready" signal — dashboards alert
	// when this never fires after startup. The message must include
	// total resource count + type count. A regression in the
	// aggregation loop would silently understate cluster scope.
	ec := riECommentator()
	counts := map[string]int{
		"ingresses": 5,
		"services":  10,
		"endpoints": 25, // total = 40, types = 3
	}
	evt := ctlevents.NewIndexSynchronizedEvent(counts)

	insight, attrs := ec.resourceInsight(evt, nil)

	assert.Contains(t, insight, "All resource indexes synchronized",
		"insight must use the documented 'ready' phrase so dashboards can "+
			"detect controller startup completion via log scrape")
	assert.Contains(t, insight, "40 resources",
		"total resource count must appear (sum across all types) — "+
			"a regression in the aggregation loop (e.g. += instead of =) would "+
			"silently understate cluster scope")
	assert.Contains(t, insight, "3 types",
		"distinct type count must appear so operators see watcher coverage")

	assert.Equal(t, 3, riAttr(attrs, "resource_types"),
		"resource_types attr must match the type count in the message")
	assert.Equal(t, 40, riAttr(attrs, "total_resources"),
		"total_resources attr must match the count in the message — "+
			"text/attr drift would break log↔metrics correlation")
}

func TestResourceInsight_IndexSynchronized_EmptyCountsReportsZero(t *testing.T) {
	// Edge case: no resources at all (e.g. cluster with no watched
	// resource types matching). The message must still fire (operators
	// rely on it as the readiness signal) and report 0/0.
	ec := riECommentator()
	evt := ctlevents.NewIndexSynchronizedEvent(map[string]int{})

	insight, _ := ec.resourceInsight(evt, nil)

	require.NotEmpty(t, insight,
		"IndexSynchronized must produce an insight even when no resources "+
			"exist — the message itself is the readiness signal")
	assert.Contains(t, insight, "0 resources")
	assert.Contains(t, insight, "0 types")
}

func TestResourceInsight_UnknownEventTypeReturnsEmpty(t *testing.T) {
	// Default arm: events this insight doesn't own must produce
	// empty output so the dispatcher can skip cleanly. A non-empty
	// default would emit garbage log lines for every other event
	// type.
	ec := riECommentator()
	other := ctlevents.NewBecameLeaderEvent("test-pod") // not a resource event

	insight, attrs := ec.resourceInsight(other, []any{"existing", "value"})

	assert.Empty(t, insight,
		"unhandled event types must produce empty insight — the dispatcher "+
			"uses this as the skip signal")
	assert.Equal(t, []any{"existing", "value"}, attrs,
		"the attrs slice must pass through UNCHANGED on the default arm")
}

// riECommentator returns a minimal EventCommentator with just the
// ringBuffer field populated. resourceInsight is a pure formatter
// that doesn't touch eventBus. The ri-prefix avoids collision with
// sibling test files.
func riECommentator() *EventCommentator {
	return &EventCommentator{ringBuffer: NewRingBuffer(8)}
}

// riAttr walks a slog-style key/value attribute slice for the value
// of the named key. Returns nil if not found.
func riAttr(attrs []any, key string) any {
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

// hasPrefix is a tiny strings.HasPrefix wrapper without importing
// strings into the test header (purely to keep the import block
// small for readability of the contract documentation above).
func hasPrefix(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	return s[:len(prefix)] == prefix
}
