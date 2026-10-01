// Copyright 2026 Philipp Hossner
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"gitlab.com/haproxy-haptic/haptic/tests/e2e/httpclient"
)

func TestHapticVarnishCacheTimeoutUpdateIsReloadFree(t *testing.T) {
	RequireCacheProfile(t)
	const host = "cache-timeout-update.localdev.me"
	feature := features.New("Cache dispatcher follows application timeout updates without reloading").
		Assess("a longer route timeout updates the live dispatcher budget", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			t.Helper()
			client, err := cfg.NewClient()
			require.NoError(t, err)
			cs, err := newClientsetForE2E(client.RESTConfig())
			require.NoError(t, err)
			ns := NamespaceForTest(ctx, t, client)
			DumpLogsOnFailure(t, ns)
			backend := NewEchoServerBackend(ctx, t, client, ns)
			NewIngress(ctx, t, client, ns, &IngressSpec{
				Name: "cache-timeout", Host: host, BackendService: backend.Service, BackendPort: backend.Port,
				Annotations: map[string]string{
					"haproxy-haptic.org/cache-enable":   "true",
					"haproxy-haptic.org/timeout-server": "2h",
				},
			})
			httpclient.New(t).GET(host, "/").ExpectStatus(t, 200)
			waitFleetQuiescent(ctx, t, client, cs)
			before := captureReloadFingerprint(ctx, t, cs)
			beforeBudget, err := strconv.Atoi(mapEntriesFrom(showMap(ctx, t, cs, "maps/cache-dispatch-timeout.map"))["budget"])
			require.NoError(t, err)
			require.Greater(t, beforeBudget, 7200000)
			require.NoError(t, retry.RetryOnConflict(retry.DefaultRetry, func() error {
				ingress := &networkingv1.Ingress{}
				if err := client.Resources(ns).Get(ctx, "cache-timeout", ns, ingress); err != nil {
					return err
				}
				ingress.Annotations["haproxy-haptic.org/timeout-server"] = "4h"
				return client.Resources(ns).Update(ctx, ingress)
			}))
			reloadFreeReaction(ctx, t, cs, "dispatcher timeout increased", func(ctx context.Context) (bool, error) {
				budget, err := strconv.Atoi(mapEntriesFrom(showMap(ctx, t, cs, "maps/cache-dispatch-timeout.map"))["budget"])
				return budget > beforeBudget, err
			})
			waitFleetQuiescent(ctx, t, client, cs)
			httpclient.New(t).GET(host, "/").ExpectStatus(t, 200)
			assertReloadFree(t, before, captureReloadFingerprint(ctx, t, cs), "cache route timeout 2h to 4h")
			return ctx
		}).Feature()
	testEnv.Test(t, feature)
}
