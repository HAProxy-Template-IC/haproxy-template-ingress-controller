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
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
)

// handlePodsDiscovered (configpublisher's, distinct from the
// scheduler's same-named handler) reconciles status against running
// HAProxy pods, cleaning up stale entries from terminated pods.
// This file pins the load-bearing pre-flight guards. The happy
// path requires a real K8s publisher and is covered by integration
// tests, but the two startup-race guards can fire BEFORE the
// publisher is even configured and would nil-deref or hit
// ReconcileDeployedToPods with an empty namespace on every
// pod-discovery event:
//
//  1. !hasTemplateConfig → MUST early-return without touching the
//     publisher. Pod discovery happens during startup, often before
//     the first ConfigValidatedEvent caches the templateConfig.
//     Without this guard the function would dereference a nil
//     templateConfig and crash.
//
//  2. templateConfig.Namespace == "" → MUST early-return. A
//     malformed CRD (or one whose namespace was stripped during a
//     copy) would propagate "" into ReconcileDeployedToPods, which
//     would either operate cluster-wide (deleting status entries
//     for unrelated runtime configs!) or return an opaque API
//     error.

// podsDiscoveredComponent constructs a Component populated only
// with the fields handlePodsDiscovered touches. publisher is
// intentionally nil — both tested branches MUST NOT reach it.
func podsDiscoveredComponent() *Component {
	return &Component{
		logger: testutil.NewTestLogger(),
	}
}

func TestHandlePodsDiscovered_SkipsWhenNoTemplateConfig(t *testing.T) {
	c := podsDiscoveredComponent()
	require.False(t, c.hasTemplateConfig,
		"baseline: hasTemplateConfig must start false for the assertion to be meaningful")

	evt := events.NewHAProxyPodsDiscoveredEvent(
		[]dataplane.Endpoint{
			{PodName: "haproxy-pod-1", PodNamespace: "haptic"},
		},
		1,
	)

	require.NotPanics(t, func() { c.handlePodsDiscovered(evt) },
		"handlePodsDiscovered MUST NOT touch the publisher when no "+
			"templateConfig is cached — pod discovery often arrives during "+
			"startup BEFORE the first ConfigValidatedEvent populates the "+
			"templateConfig; without this guard the function would crash "+
			"every controller restart that happens to receive pod events "+
			"before config events")
}

func TestHandlePodsDiscovered_SkipsWhenNamespaceIsEmpty(t *testing.T) {
	c := podsDiscoveredComponent()
	// hasTemplateConfig=true with an empty Namespace simulates a
	// malformed CRD or a defensive copy that lost the namespace.
	c.templateConfig = &v1alpha1.HAProxyTemplateConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "tmpl",
			Namespace: "", // ← the guard
		},
	}
	c.hasTemplateConfig = true

	evt := events.NewHAProxyPodsDiscoveredEvent(
		[]dataplane.Endpoint{
			{PodName: "haproxy-pod-1", PodNamespace: "haptic"},
		},
		1,
	)

	require.NotPanics(t, func() { c.handlePodsDiscovered(evt) },
		"empty templateConfig.Namespace MUST early-return — the namespace "+
			"is passed to publisher.ReconcileDeployedToPods, which uses it "+
			"to scope its status-cleanup queries. A regression that allowed "+
			"\"\" through would either operate CLUSTER-WIDE (deleting status "+
			"entries for unrelated runtime configs in other namespaces) or "+
			"return an opaque API error on every reconciliation")
}
