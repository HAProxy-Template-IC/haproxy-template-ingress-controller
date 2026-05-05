// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/tests/testutil"
)

// TestSPOAHubReloadOnConfigPush exercises the end-to-end runtime config
// delivery path introduced by ADR-0007: HAPTIC's chart-library snippet
// renders /etc/haproxy/general/spoa-hub-config.toml, the dataplane API
// pushes it to each haproxy pod, and the spoa-hub sidecar's file-watch
// (haproxy-spoa-hub MR1) gracefully reloads on the change.
//
// The trigger is an Ingress with `nginx.ingress.kubernetes.io/modsecurity-snippet`.
// The chart-library inlines the annotation value into the rendered TOML's
// coraza directives field; that's a real config delta the controller
// must push and the hub must observe. We verify two facts after applying
// the Ingress:
//
//  1. The on-disk file at /etc/haproxy/general/spoa-hub-config.toml on
//     the haproxy pod contains the snippet's distinctive rule-id (proves
//     the controller re-rendered + dataplane pushed).
//  2. The spoa-hub container's recent log output contains a successful
//     reload event (proves the hub's file-watch + arc_swap-based reload
//     fired and didn't bail on parse/plugin-init failure).
//
// We do NOT couple to the hub's exact log format string — see
// spoaHubLogsShowSuccessfulReload's substring match for the contract.
func TestSPOAHubReloadOnConfigPush(t *testing.T) {
	feature := features.New("SPOA hub: graceful reload on controller config push").
		Setup(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			client, err := cfg.NewClient()
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			backend := NewEchoServerBackend(ctx, t, client, ns)

			// Distinctive id we'll grep for on the haproxy pod's filesystem.
			// "9001" is unlikely to collide with anything else in CI.
			const snippetMarker = "id:9001"
			snippetRule := `SecRule REQUEST_URI "@beginsWith /reload-canary" "id:9001,phase:1,deny,status:403"`

			haproxyPod := firstHAProxyPodName(ctx, t)

			NewIngress(ctx, t, client, ns, IngressSpec{
				Name:           "echo",
				Host:           "spoa-hub-reload.localdev.me",
				BackendService: backend.Service,
				BackendPort:    backend.Port,
				Annotations: map[string]string{
					"nginx.ingress.kubernetes.io/modsecurity-snippet": snippetRule,
				},
			})

			// (1) File on haproxy pod's filesystem reflects the snippet.
			fileCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
			defer cancel()
			if err := testutil.WaitForCondition(fileCtx, testutil.DefaultWaitConfig(), func(c context.Context) (bool, error) {
				content, err := readFileFromHAProxyPod(c, haproxyPod, "/etc/haproxy/general/spoa-hub-config.toml")
				if err != nil {
					return false, err
				}
				return strings.Contains(content, snippetMarker), nil
			}); err != nil {
				t.Fatalf("timed out waiting for snippet %q to land in /etc/haproxy/general/spoa-hub-config.toml on %s: %v", snippetMarker, haproxyPod, err)
			}

			// (2) Hub container logs a successful reload after the push.
			logCtx, cancel2 := context.WithTimeout(ctx, 60*time.Second)
			defer cancel2()
			if err := testutil.WaitForCondition(logCtx, testutil.DefaultWaitConfig(), func(c context.Context) (bool, error) {
				logs, err := readSPOAHubLogs(c, haproxyPod)
				if err != nil {
					return false, err
				}
				return spoaHubLogsShowSuccessfulReload(logs), nil
			}); err != nil {
				t.Fatalf("timed out waiting for spoa-hub successful-reload log on %s: %v", haproxyPod, err)
			}

			return ctx
		}).
		Assess("config push lands on haproxy pod and hub reloads", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			// Conditions verified in Setup; this Assess keeps the
			// feature wiring aligned with the rest of the suite.
			return ctx
		})

	testEnv.Test(t, feature.Feature())
}
