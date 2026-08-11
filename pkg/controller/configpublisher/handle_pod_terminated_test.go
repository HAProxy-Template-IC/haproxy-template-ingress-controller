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

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
)

// handlePodTerminated cleans up status references when a HAProxy
// pod terminates. It shares the SAME startup-race guard pattern as
// handlePodsDiscovered (no template config / empty namespace), but
// distinct event type, distinct publisher method
// (CleanupPodReferences vs ReconcileDeployedToPods), and distinct
// failure-mode contract (CleanupPodReferences errors are logged
// but non-blocking; the function returns silently on cleanup failure).
//
// Two contracts pinned for the startup-race window:
//
//  1. !hasTemplateConfig → MUST early-return without touching the
//     publisher. Pod terminations can arrive during startup BEFORE
//     the first ConfigValidatedEvent populates templateConfig
//     (e.g. controller restart while pods are churning). Without
//     this guard the function would dereference nil.
//
//  2. templateConfig.Namespace == "" → MUST early-return. The
//     cleanup request includes the namespace; a missing namespace
//     would either cluster-wide-cleanup unrelated runtime configs
//     (deleting arbitrary status entries!) or surface as a confusing
//     API error.

// podTerminatedComponent constructs a Component populated only
// with the fields handlePodTerminated touches. publisher is nil —
// both tested branches MUST NOT reach it.
func podTerminatedComponent() *Component {
	return &Component{
		logger: testutil.NewTestLogger(),
	}
}

func TestHandlePodTerminated_SkipsWhenNoTemplateConfig(t *testing.T) {
	c := podTerminatedComponent()
	require.False(t, c.hasTemplateConfig,
		"baseline: hasTemplateConfig must start false")

	evt := events.NewHAProxyPodTerminatedEvent("haproxy-pod-1", "haptic", "")

	require.NotPanics(t, func() { c.handlePodTerminated(t.Context(), evt) },
		"handlePodTerminated MUST NOT touch the publisher when no "+
			"templateConfig is cached — pod terminations can arrive during "+
			"controller restart BEFORE the first ConfigValidatedEvent "+
			"populates templateConfig; without this guard the function "+
			"would dereference nil on every restart that received a "+
			"termination event before the config event")
}

func TestHandlePodTerminated_SkipsWhenNamespaceIsEmpty(t *testing.T) {
	c := podTerminatedComponent()
	c.templateConfig = &v1alpha1.HAProxyTemplateConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "tmpl",
			Namespace: "", // ← the guard
		},
	}
	c.hasTemplateConfig = true

	evt := events.NewHAProxyPodTerminatedEvent("haproxy-pod-1", "haptic", "")

	require.NotPanics(t, func() { c.handlePodTerminated(t.Context(), evt) },
		"empty templateConfig.Namespace MUST early-return — the namespace "+
			"is passed into the PodCleanupRequest, which scopes the "+
			"status-cleanup to a specific runtime config namespace. A "+
			"regression that allowed \"\" through would either operate "+
			"CLUSTER-WIDE (deleting arbitrary status entries for runtime "+
			"configs in OTHER namespaces) or surface as an opaque "+
			"namespace-not-found API error on every termination")
}
