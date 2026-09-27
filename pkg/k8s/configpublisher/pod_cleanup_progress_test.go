// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package configpublisher

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"

	haproxyv1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	"gitlab.com/haproxy-haptic/haptic/pkg/generated/clientset/versioned/fake"
	listers "gitlab.com/haproxy-haptic/haptic/pkg/generated/listers/haproxytemplate/v1alpha1"
)

func cleanupProgressFixture(t *testing.T) (*fake.Clientset, *Publisher, *PublishResult) {
	t.Helper()
	ctx, _, client, publisher := newTestPublisher(t)
	req := basePublishRequest()
	req.AuxiliaryFiles = &AuxiliaryFiles{
		MapFiles:     []auxiliaryfiles.MapFile{{Path: "routes.map", Content: "host backend"}},
		GeneralFiles: []auxiliaryfiles.GeneralFile{{Filename: "error.http", Content: "error"}},
		CRTListFiles: []auxiliaryfiles.CRTListFile{{Path: "certificates.list", Content: "certificate.pem"}},
	}
	result, err := publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)
	for _, name := range []string{"departed", "running"} {
		require.NoError(t, publisher.UpdateDeploymentStatus(ctx, &DeploymentStatusUpdate{
			RuntimeConfigName: "test-config-haproxycfg", RuntimeConfigNamespace: "default", PodName: name, Checksum: "abc123",
		}))
	}
	return client, publisher, result
}

func TestPodCleanupRetainsProgressAfterAuxiliaryFailure(t *testing.T) {
	for _, resource := range []string{"haproxymapfiles", "haproxygeneralfiles", "haproxycrtlistfiles"} {
		for _, reconcile := range []bool{false, true} {
			name := resource + "/termination"
			if reconcile {
				name = resource + "/discovery"
			}
			t.Run(name, func(t *testing.T) {
				testCleanupProgressRetry(t, resource, reconcile)
			})
		}
	}
}

func TestPodReconciliationRepairsAuxiliaryStatusWithoutStaleParent(t *testing.T) {
	client, publisher, result := cleanupProgressFixture(t)
	configs := client.HaproxyTemplateICV1alpha1().HAProxyCfgs("default")
	cfg, err := configs.Get(t.Context(), "test-config-haproxycfg", metav1.GetOptions{})
	require.NoError(t, err)
	cfg.Status.DeployedToPods = []haproxyv1alpha1.PodDeploymentStatus{{PodName: "running"}}
	_, err = configs.UpdateStatus(t.Context(), cfg, metav1.UpdateOptions{})
	require.NoError(t, err)
	require.NoError(t, publisher.ReconcileDeployedToPods(t.Context(), "default", []PodIdentity{{PodName: "running"}}))
	assertCleanupProgress(t, t.Context(), client, result)
}

func assertCleanupProgress(t *testing.T, ctx context.Context, client *fake.Clientset, result *PublishResult) {
	t.Helper()
	api := client.HaproxyTemplateICV1alpha1()
	cfg, err := api.HAProxyCfgs("default").Get(ctx, "test-config-haproxycfg", metav1.GetOptions{})
	require.NoError(t, err)
	mapFile, err := api.HAProxyMapFiles("default").Get(ctx, result.MapFileNames[0], metav1.GetOptions{})
	require.NoError(t, err)
	general, err := api.HAProxyGeneralFiles("default").Get(ctx, result.GeneralFileNames[0], metav1.GetOptions{})
	require.NoError(t, err)
	crt, err := api.HAProxyCRTListFiles("default").Get(ctx, result.CRTListFileNames[0], metav1.GetOptions{})
	require.NoError(t, err)
	for _, pods := range [][]haproxyv1alpha1.PodDeploymentStatus{cfg.Status.DeployedToPods, mapFile.Status.DeployedToPods, general.Status.DeployedToPods, crt.Status.DeployedToPods} {
		require.Len(t, pods, 1)
		assert.Equal(t, "running", pods[0].PodName)
	}
}

func TestPodReconciliationSkipsCachedCurrentAuxiliaryStatus(t *testing.T) {
	client, publisher, result := cleanupProgressFixture(t)
	running := []PodIdentity{{PodName: "running"}}
	require.NoError(t, publisher.ReconcileDeployedToPods(t.Context(), "default", running))
	api := client.HaproxyTemplateICV1alpha1()
	mapFile, err := api.HAProxyMapFiles("default").Get(t.Context(), result.MapFileNames[0], metav1.GetOptions{})
	require.NoError(t, err)
	general, err := api.HAProxyGeneralFiles("default").Get(t.Context(), result.GeneralFileNames[0], metav1.GetOptions{})
	require.NoError(t, err)
	crt, err := api.HAProxyCRTListFiles("default").Get(t.Context(), result.CRTListFileNames[0], metav1.GetOptions{})
	require.NoError(t, err)
	index := func(obj runtime.Object) cache.Indexer {
		store := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
		require.NoError(t, store.Add(obj))
		return store
	}
	publisher.listers = &Listers{
		MapFiles:     listers.NewHAProxyMapFileLister(index(mapFile)),
		GeneralFiles: listers.NewHAProxyGeneralFileLister(index(general)),
		CRTListFiles: listers.NewHAProxyCRTListFileLister(index(crt)),
	}
	client.ClearActions()
	require.NoError(t, publisher.ReconcileDeployedToPods(t.Context(), "default", running))
	for _, action := range client.Actions() {
		assert.Equal(t, "haproxycfgs", action.GetResource().Resource)
	}
}

func testCleanupProgressRetry(t *testing.T, resource string, reconcile bool) {
	t.Helper()
	client, publisher, result := cleanupProgressFixture(t)
	var fail atomic.Bool
	fail.Store(true)
	client.PrependReactor("update", resource, func(k8stesting.Action) (bool, runtime.Object, error) {
		if fail.Swap(false) {
			return true, nil, errors.New("auxiliary status unavailable")
		}
		return false, nil, nil
	})
	cleanup := func() error {
		if reconcile {
			return publisher.ReconcileDeployedToPods(t.Context(), "default", []PodIdentity{{PodName: "running"}})
		}
		return publisher.CleanupPodReferences(t.Context(), &PodCleanupRequest{Namespace: "default", PodName: "departed"})
	}
	require.ErrorContains(t, cleanup(), "auxiliary status unavailable")
	cfg, err := client.HaproxyTemplateICV1alpha1().HAProxyCfgs("default").Get(t.Context(), "test-config-haproxycfg", metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, cfg.Status.DeployedToPods, 2)
	require.NoError(t, cleanup())
	assertCleanupProgress(t, t.Context(), client, result)
}
