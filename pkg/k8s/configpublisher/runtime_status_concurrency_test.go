// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package configpublisher

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8stesting "k8s.io/client-go/testing"

	haproxyv1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/generated/clientset/versioned/fake"
)

func TestRuntimeConfigStatusConcurrentWriters(t *testing.T) {
	tests := []struct {
		name    string
		change  func(*haproxyv1alpha1.HAProxyCfg)
		wantErr bool
	}{
		{name: "pod status", change: func(cfg *haproxyv1alpha1.HAProxyCfg) {
			cfg.Status.DeployedToPods = []haproxyv1alpha1.PodDeploymentStatus{{PodName: "haproxy-0", Checksum: "deployed"}}
			cfg.Status.ObservedGeneration = 42
		}},
		{name: "replacement", wantErr: true, change: func(cfg *haproxyv1alpha1.HAProxyCfg) { cfg.UID = "replacement" }},
		{name: "new source", wantErr: true, change: func(cfg *haproxyv1alpha1.HAProxyCfg) { cfg.Spec.Content = "global\n  maxconn 42\n" }},
		{name: "new set", wantErr: true, change: func(cfg *haproxyv1alpha1.HAProxyCfg) {
			cfg.Annotations[AuxiliarySetIDAnnotationKey] = "sha256:new-set"
		}},
		{name: "new owner", wantErr: true, change: func(cfg *haproxyv1alpha1.HAProxyCfg) {
			cfg.OwnerReferences[0].UID = "replacement-template"
		}},
	}
	for _, tt := range tests {
		for _, beforeRead := range []bool{true, false} {
			phase := "during write"
			if beforeRead {
				phase = "before read"
			}
			t.Run(tt.name+"/"+phase, func(t *testing.T) {
				ctx, _, client, publisher := newTestPublisher(t)
				cfg := runtimeStatusFixture(t, publisher)
				_, err := client.HaproxyTemplateICV1alpha1().HAProxyCfgs(cfg.Namespace).Create(ctx, cfg, metav1.CreateOptions{})
				require.NoError(t, err)
				installRuntimeStatusMutation(t, client, beforeRead, tt.change)
				result := &PublishResult{MapFileNames: []string{"new-map"}}
				err = publisher.updateRuntimeConfigStatus(ctx, cfg, result)
				if tt.wantErr {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
				current, err := client.HaproxyTemplateICV1alpha1().HAProxyCfgs(cfg.Namespace).Get(ctx, cfg.Name, metav1.GetOptions{})
				require.NoError(t, err)
				want := cfg.DeepCopy()
				tt.change(want)
				if !tt.wantErr {
					want.Status.AuxiliaryFiles = buildAuxiliaryFileReferences(cfg.Namespace, result, cfg.Annotations[AuxiliarySetIDAnnotationKey])
				}
				assert.Equal(t, want.Status, current.Status)
			})
		}
	}
}

func runtimeStatusFixture(t *testing.T, publisher *Publisher) *haproxyv1alpha1.HAProxyCfg {
	t.Helper()
	req := basePublishRequest()
	canonical, err := canonicalizePublishRequest(&req)
	require.NoError(t, err)
	cfg := publisher.buildRuntimeConfig("test-config-haproxycfg", canonical)
	cfg.UID = "runtime-uid"
	cfg.ResourceVersion = "1"
	cfg.Status.AuxiliaryFiles = &haproxyv1alpha1.AuxiliaryFileReferences{SetID: "sha256:previous"}
	return cfg
}

func TestRuntimeConfigStatusUnchangedReferencesRequirePublicationIdentity(t *testing.T) {
	ctx, _, client, publisher := newTestPublisher(t)
	cfg := runtimeStatusFixture(t, publisher)
	result := &PublishResult{MapFileNames: []string{"same-map"}}
	cfg.Status.AuxiliaryFiles = buildAuxiliaryFileReferences(cfg.Namespace, result, cfg.Annotations[AuxiliarySetIDAnnotationKey])
	_, err := client.HaproxyTemplateICV1alpha1().HAProxyCfgs(cfg.Namespace).Create(ctx, cfg, metav1.CreateOptions{})
	require.NoError(t, err)
	installRuntimeStatusMutation(t, client, true, func(current *haproxyv1alpha1.HAProxyCfg) {
		current.UID = "replacement"
	})
	require.Error(t, publisher.updateRuntimeConfigStatus(ctx, cfg, result))
	require.Error(t, publisher.ensurePublicationCurrent(ctx, cfg, cfg.Status.AuxiliaryFiles))
}

func TestRuntimeConfigStatusRequiresUID(t *testing.T) {
	ctx, _, client, publisher := newTestPublisher(t)
	cfg := runtimeStatusFixture(t, publisher)
	cfg.UID = ""
	require.ErrorContains(t, publisher.patchRuntimeConfigStatusField(ctx, cfg, cfg, "validationError", "rejected"), "no UID")
	assert.Empty(t, client.Actions())
}

func installRuntimeStatusMutation(t *testing.T, client *fake.Clientset, beforeRead bool, change func(*haproxyv1alpha1.HAProxyCfg)) {
	t.Helper()
	mutated := false
	client.PrependReactor("*", "haproxycfgs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		isRead := action.GetVerb() == "get"
		isWrite := action.GetSubresource() == statusSubresource && (action.GetVerb() == "update" || action.GetVerb() == "patch")
		shouldMutate := beforeRead && isRead || !beforeRead && isWrite
		if mutated || !shouldMutate {
			return false, nil, nil
		}
		mutated = true
		gvr := schema.GroupVersionResource{Group: "haproxy-haptic.org", Version: "v1alpha1", Resource: "haproxycfgs"}
		obj, err := client.Tracker().Get(gvr, "default", "test-config-haproxycfg")
		require.NoError(t, err)
		cfg := obj.(*haproxyv1alpha1.HAProxyCfg)
		change(cfg)
		cfg.ResourceVersion = "2"
		require.NoError(t, client.Tracker().Update(gvr, cfg, cfg.Namespace))
		if update, ok := action.(k8stesting.UpdateAction); ok {
			requested := update.GetObject().(*haproxyv1alpha1.HAProxyCfg)
			if requested.ResourceVersion != cfg.ResourceVersion {
				return true, nil, apierrors.NewConflict(gvr.GroupResource(), cfg.Name, errors.New("status changed after read"))
			}
		}
		return false, nil, nil
	})
}
