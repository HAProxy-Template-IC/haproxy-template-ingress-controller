// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package configpublisher

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
)

// minimalComponent returns a Component with just the fields
// skipIfAlreadyPublished and discardCachedConfig need. Avoids
// pulling in the full New() constructor's K8s clients and event
// bus — neither of which is exercised on the dedup path.
func minimalComponent(lastChecksum string) *Component {
	component := &Component{
		logger:                testutil.NewTestLogger(),
		lastPublishedChecksum: lastChecksum,
		renderedConfigs:       make(map[string]*renderedConfigEntry),
	}
	if lastChecksum != "" {
		component.lastPublishedEntry = &renderedConfigEntry{
			config: "content:" + lastChecksum, contentChecksum: lastChecksum,
		}
	}
	return component
}

// workWith builds a minimal *publishWorkItem carrying the supplied
// checksum. The work entry is pre-cached in renderedConfigs so the
// dedup-side-effect (cache drop) is observable.
func workWith(c *Component, correlationID, checksum string) *publishWorkItem {
	entry := &renderedConfigEntry{config: "content:" + checksum, contentChecksum: checksum}
	c.renderedConfigs[correlationID] = entry
	return &publishWorkItem{
		correlationID: correlationID,
		entry:         entry,
	}
}

func TestComponent_SkipIfAlreadyPublishedWithoutPriorContentNeverDedups(t *testing.T) {
	tests := []struct {
		name         string
		lastChecksum string
	}{
		{name: "fresh component (lastPublishedChecksum is empty)", lastChecksum: ""},
		{name: "component with prior publish (lastPublishedChecksum non-empty)", lastChecksum: "previous"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := minimalComponent(tt.lastChecksum)
			work := workWith(c, "corr-1", "")

			skip := c.skipIfAlreadyPublished(work, "test-msg")

			assert.False(t, skip)
			// Cache must NOT be cleared on a non-dedup result.
			assert.Contains(t, c.renderedConfigs, "corr-1",
				"on non-dedup the renderedConfigs cache must be left intact "+
					"so the subsequent publish path can still consume it")
		})
	}
}

func TestComponent_SkipIfAlreadyPublished_DifferentContentDoesNotDedup(t *testing.T) {
	c := minimalComponent("checksum-A")
	// Use a distinct correlation ID per test so the workWith helper's
	// correlationID parameter actually varies across call sites.
	work := workWith(c, "corr-different-checksum", "checksum-B")

	skip := c.skipIfAlreadyPublished(work, "test-msg")

	assert.False(t, skip)
	assert.Contains(t, c.renderedConfigs, "corr-different-checksum",
		"non-dedup must leave the cache intact for the publish path")
}

func TestComponent_SkipIfAlreadyPublished_ExactContentDedupsAndDropsCache(t *testing.T) {
	const checksum = "duplicate-content-checksum"
	c := minimalComponent(checksum)
	work := workWith(c, "corr-matching-checksum", checksum)

	require.Contains(t, c.renderedConfigs, "corr-matching-checksum",
		"sanity: the cache must contain the entry before the dedup call")

	skip := c.skipIfAlreadyPublished(work, "test-msg")

	assert.True(t, skip)

	// SIDE EFFECT: the cached entry MUST be dropped on dedup. This
	// is the contract that keeps renderedConfigs from leaking
	// memory proportional to the dedup hit rate.
	assert.NotContains(t, c.renderedConfigs, "corr-matching-checksum",
		"on dedup the cached renderedConfig entry MUST be dropped — without "+
			"this the cache leaks memory proportional to the dedup hit rate, "+
			"which is exactly the steady-state scenario the dedup is designed for")
}

func TestComponent_SkipIfAlreadyPublishedNeverTrustsChecksumAsPositiveProof(t *testing.T) {
	const checksum = "poisoned-shared-checksum"
	c := minimalComponent(checksum)
	work := workWith(c, "changed", checksum)
	work.entry.config = "different bytes"

	assert.False(t, c.skipIfAlreadyPublished(work, "test-msg"))
	assert.Contains(t, c.renderedConfigs, "changed")
}

func TestComponent_SkipIfAlreadyPublished_CacheDropOnlyTouchesMatchingCorrelation(t *testing.T) {
	// The cache drop on dedup must be SCOPED to the work item's
	// correlation ID — dropping unrelated entries would orphan
	// in-flight work for other correlation IDs.
	const checksum = "shared-checksum"
	c := minimalComponent(checksum)

	work := workWith(c, "corr-1", checksum)
	// Add a second cached entry that is unrelated.
	c.renderedConfigs["corr-2-unrelated"] = &renderedConfigEntry{
		contentChecksum: "different-checksum-other-correlation",
	}

	skip := c.skipIfAlreadyPublished(work, "test-msg")

	require.True(t, skip)
	assert.NotContains(t, c.renderedConfigs, "corr-1",
		"the matching correlation's entry must be dropped")
	assert.Contains(t, c.renderedConfigs, "corr-2-unrelated",
		"the dedup cache drop must be scoped to the dedup'd correlation; "+
			"a regression that cleared the entire renderedConfigs map would "+
			"orphan in-flight work for other reconciliation cycles, causing "+
			"those publishes to silently lose their rendered output")
}
