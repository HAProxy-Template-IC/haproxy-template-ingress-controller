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
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
)

// handleConfigValidated has THREE branches; the existing
// component_test.go integration tests exercise the happy path but
// the two defensive branches — both essential safeguards against
// publishing bugs upstream — are uncovered. This file pins them:
//
//  1. nil TemplateConfig → MUST log a warning and leave cached
//     state untouched. The TemplateConfig field is `any`-typed to
//     break a circular dependency; if a publisher upstream forgets
//     to populate it (or zeroes it out for some reason), the
//     receiving Component must NOT cache nil and silently propagate
//     it to publishWork — that would nil-deref later, deep in the
//     publish path, far from the original bug site.
//
//  2. wrong-type TemplateConfig → MUST log a warning and leave
//     cached state untouched. The `any` type means a future event
//     producer could accidentally pass the parsed config (which
//     belongs in event.Config, not event.TemplateConfig). Without
//     this guard, the type assertion later in the publish path
//     would panic; the warning here lets us spot the producer bug
//     in operator logs instead.
//
// Both contracts protect the stored state's `hasTemplateConfig`
// invariant: once true, c.templateConfig MUST be a usable
// *v1alpha1.HAProxyTemplateConfig pointer. The downstream publish
// path takes that as a precondition.

// publisherForConfigValidatedTest builds a Component populated only
// with the fields handleConfigValidated touches: logger, mu,
// templateConfig, hasTemplateConfig. No publisher / event bus
// needed — defensive branches return early before any of that.
func publisherForConfigValidatedTest() *Component {
	return &Component{
		logger: testutil.NewTestLogger(),
	}
}

func TestHandleConfigValidated_NilTemplateConfigPreservesState(t *testing.T) {
	c := publisherForConfigValidatedTest()
	// Pre-seed the cache so we can verify it's NOT touched on the
	// nil branch. A regression that overwrote the cache with the
	// nil event payload would silently destroy a previously valid
	// config.
	preexisting := &v1alpha1.HAProxyTemplateConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "preexisting", Namespace: "haptic"},
	}
	c.templateConfig = preexisting
	c.hasTemplateConfig = true

	// Build the event with TemplateConfig=nil.
	evt := events.NewConfigValidatedEvent(nil, nil, "v42", "secret-v1")

	require.NotPanics(t, func() { c.handleConfigValidated(evt) },
		"nil TemplateConfig must NOT panic — it must log and return early")

	c.mu.RLock()
	defer c.mu.RUnlock()
	assert.True(t, c.hasTemplateConfig,
		"hasTemplateConfig MUST stay true on the nil-guard branch — "+
			"a regression that flipped it to false (or replaced the cache "+
			"with the nil payload) would silently invalidate a previously "+
			"valid config and break the next publish")
	assert.Same(t, preexisting, c.templateConfig,
		"templateConfig MUST point to the SAME prior cached value — "+
			"nil-guard must be a no-op on state, not a destructive 'last "+
			"write wins' overwrite")
}

func TestHandleConfigValidated_WrongTypeTemplateConfigPreservesState(t *testing.T) {
	c := publisherForConfigValidatedTest()
	preexisting := &v1alpha1.HAProxyTemplateConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "preexisting", Namespace: "haptic"},
	}
	c.templateConfig = preexisting
	c.hasTemplateConfig = true

	// Wrong type — TemplateConfig is `any` so the constructor
	// accepts anything; the receiver's type-assertion guard is the
	// only line of defense.
	evt := events.NewConfigValidatedEvent(nil, "not a HAProxyTemplateConfig at all", "v42", "secret-v1")

	require.NotPanics(t, func() { c.handleConfigValidated(evt) },
		"wrong-type TemplateConfig must NOT panic — the type assertion "+
			"with comma-ok must catch the type mismatch and log instead")

	c.mu.RLock()
	defer c.mu.RUnlock()
	assert.True(t, c.hasTemplateConfig,
		"hasTemplateConfig MUST stay true on the wrong-type-guard branch")
	assert.Same(t, preexisting, c.templateConfig,
		"templateConfig MUST point to the SAME prior cached value — "+
			"a regression that stored the wrong-typed value would set up "+
			"a downstream type-assertion panic in the publish path, far "+
			"from the original event-producer bug")
}

func TestHandleConfigValidated_ValidTemplateConfigCachesIt(t *testing.T) {
	// Happy path: well-typed TemplateConfig must be cached AND
	// hasTemplateConfig flipped to true. This is also covered by the
	// component_test.go integration test, but pinning it here in the
	// same table makes the *contract* of this function self-contained:
	// the trio (nil → no-op, wrong-type → no-op, valid → cache) is
	// the function's full observable behaviour.
	c := publisherForConfigValidatedTest()
	require.False(t, c.hasTemplateConfig,
		"baseline: hasTemplateConfig must start false for the assertion to be meaningful")

	cfg := &v1alpha1.HAProxyTemplateConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "valid", Namespace: "haptic"},
	}
	evt := events.NewConfigValidatedEvent(nil, cfg, "v42", "secret-v1")

	c.handleConfigValidated(evt)

	c.mu.RLock()
	defer c.mu.RUnlock()
	assert.True(t, c.hasTemplateConfig,
		"hasTemplateConfig MUST be set true after caching a valid config")
	assert.Same(t, cfg, c.templateConfig,
		"the cached templateConfig MUST be the same pointer as the event's "+
			"TemplateConfig — a regression that copied or wrapped it would "+
			"break downstream publishers that rely on metadata identity")
}
