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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
)

// lookupCachedConfig is the correlation-ID-keyed lookup that BOTH
// handleValidationCompleted and handleValidationFailed funnel
// through before any K8s publish work is queued. It guards three
// load-bearing preconditions; only the happy path was indirectly
// exercised by integration tests, so the three negative branches
// were uncovered.
//
// Contracts pinned:
//
//  1. Empty correlation ID → returns ok=false. The correlation ID
//     is the only key tying TemplateRenderedEvent → ValidationXEvent
//     across the renderer→validator hop. Without this guard, an
//     event missing its correlation ID would silently match the
//     empty-string key in renderedConfigs (ok in Go map lookup if
//     such a key exists) — publishing the WRONG config.
//
//  2. !hasTemplateConfig → returns ok=false. The template config
//     arrives via ConfigValidatedEvent BEFORE any
//     ValidationCompleted/Failed events. A regression that skipped
//     this guard would publish with a nil *HAProxyTemplateConfig,
//     nil-deref'ing in buildPublishRequest (which reads .Name /
//     .Namespace).
//
//  3. !hasRenderedConfig (correlation ID not in renderedConfigs)
//     → returns ok=false. The rendered config arrives via
//     TemplateRenderedEvent. If the validator publishes a result
//     for a correlation ID we've never rendered (e.g. event order
//     inversion across components), we MUST NOT proceed — without
//     the guard the publisher would queue work with a nil entry,
//     causing a nil-deref reading entry.config / entry.contentChecksum
//     in the publish path.
//
//  4. Happy path with both pieces present → returns the cached
//     pointers AND ok=true. Pointer identity matters: callers
//     pass these into the work item that the worker eventually
//     hands to buildPublishRequest, which assumes the pointers
//     are the exact ones cached at event time (no copies).

// lookupComponent constructs a Component populated only with the
// fields lookupCachedConfig touches.
func lookupComponent() *Component {
	return &Component{
		logger:          testutil.NewTestLogger(),
		renderedConfigs: make(map[string]*renderedConfigEntry),
	}
}

func TestLookupCachedConfig_EmptyCorrelationIDReturnsNotOk(t *testing.T) {
	c := lookupComponent()
	// Pre-seed with an empty-string key just to make sure a
	// regression that did `c.renderedConfigs[""]` wouldn't
	// accidentally find SOMETHING and pass the test.
	c.renderedConfigs[""] = &renderedConfigEntry{config: "should-not-be-returned"}
	c.templateConfig = &v1alpha1.HAProxyTemplateConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "tmpl", Namespace: "haptic"},
	}
	c.hasTemplateConfig = true

	tc, entry, ok := c.lookupCachedConfig("evt-1", "", "TestEvent", "test action")

	assert.False(t, ok,
		"empty correlation ID MUST return ok=false — without this guard, "+
			"an event missing correlation would match the empty-string "+
			"key in renderedConfigs (Go map lookup succeeds for empty "+
			"string keys) and silently publish the WRONG config")
	assert.Nil(t, tc, "wrapped TemplateConfig MUST be nil on failed lookup")
	assert.Nil(t, entry, "wrapped renderedConfigEntry MUST be nil on failed lookup")
}

func TestLookupCachedConfig_MissingTemplateConfigReturnsNotOk(t *testing.T) {
	c := lookupComponent()
	// Pre-seed the rendered config so the test isolates the
	// hasTemplateConfig guard from the hasRenderedConfig guard.
	c.renderedConfigs["corr-1"] = &renderedConfigEntry{config: "rendered"}
	// hasTemplateConfig left false (default zero value).

	tc, entry, ok := c.lookupCachedConfig("evt-1", "corr-1", "TestEvent", "test action")

	assert.False(t, ok,
		"!hasTemplateConfig MUST return ok=false — without this guard, "+
			"the publisher would queue work with a nil *HAProxyTemplateConfig "+
			"and nil-deref in buildPublishRequest reading .Name/.Namespace")
	assert.Nil(t, tc)
	assert.Nil(t, entry)
}

func TestLookupCachedConfig_MissingRenderedConfigReturnsNotOk(t *testing.T) {
	c := lookupComponent()
	// Pre-seed template config so we isolate the missing-rendered
	// branch.
	c.templateConfig = &v1alpha1.HAProxyTemplateConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "tmpl", Namespace: "haptic"},
	}
	c.hasTemplateConfig = true
	// renderedConfigs left empty.

	tc, entry, ok := c.lookupCachedConfig("evt-1", "corr-never-rendered", "TestEvent", "test action")

	assert.False(t, ok,
		"!hasRenderedConfig MUST return ok=false — without this guard, "+
			"a validator-result event for a correlation ID we've never "+
			"rendered (event order inversion across components) would "+
			"queue work with a nil entry, causing nil-deref reading "+
			"entry.config / entry.contentChecksum in the publish path")
	assert.Nil(t, tc)
	assert.Nil(t, entry)
}

func TestLookupCachedConfig_HappyPathReturnsCachedPointers(t *testing.T) {
	c := lookupComponent()
	cachedTemplate := &v1alpha1.HAProxyTemplateConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "happy-tmpl", Namespace: "haptic"},
	}
	cachedEntry := &renderedConfigEntry{
		config:          "rendered-config-bytes",
		contentChecksum: "checksum-abc",
	}
	c.templateConfig = cachedTemplate
	c.hasTemplateConfig = true
	c.renderedConfigs["corr-happy"] = cachedEntry

	tc, entry, ok := c.lookupCachedConfig("evt-1", "corr-happy", "TestEvent", "test action")

	require.True(t, ok,
		"with both pieces present the lookup MUST succeed — this is the "+
			"normal path the publisher hits on every successful render+"+
			"validate cycle")
	assert.Same(t, cachedTemplate, tc,
		"the returned *HAProxyTemplateConfig MUST be the exact pointer "+
			"that was cached — buildPublishRequest reads identity-tied "+
			"metadata (Name/Namespace/UID), so a regression that returned "+
			"a copy would silently break ownership tracking on the published CRD")
	assert.Same(t, cachedEntry, entry,
		"the returned *renderedConfigEntry MUST be the exact pointer "+
			"that was cached — the publish path reads its config and "+
			"contentChecksum fields and any divergence from the cached "+
			"entry would break the deduplication checksum chain")
}
