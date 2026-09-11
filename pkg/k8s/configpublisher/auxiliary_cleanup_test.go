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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8stesting "k8s.io/client-go/testing"

	haproxyv1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
)

func TestPublishConfig_CleanupRetriesConcurrentPodStatus(t *testing.T) {
	ctx, _, client, publisher := newTestPublisher(t)
	req := basePublishRequest()
	req.AuxiliaryFiles = &AuxiliaryFiles{MapFiles: []auxiliaryfiles.MapFile{{Path: "routes.map", Content: "old"}}}
	initial, err := publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)
	name := initial.MapFileNames[0]
	maps := client.HaproxyTemplateICV1alpha1().HAProxyMapFiles("default")
	file, err := maps.Get(ctx, name, metav1.GetOptions{})
	require.NoError(t, err)
	file.UID = "map-uid"
	file.ResourceVersion = "1"
	_, err = maps.Update(ctx, file, metav1.UpdateOptions{})
	require.NoError(t, err)

	attempts := 0
	client.PrependReactor("delete", "haproxymapfiles", func(action k8stesting.Action) (bool, runtime.Object, error) {
		attempts++
		deletion := action.(k8stesting.DeleteAction)
		gvr := schema.GroupVersionResource{Group: "haproxy-haptic.org", Version: "v1alpha1", Resource: "haproxymapfiles"}
		obj, getErr := client.Tracker().Get(gvr, "default", name)
		require.NoError(t, getErr)
		current := obj.(*haproxyv1alpha1.HAProxyMapFile)
		if attempts == 1 {
			current.Status.DeployedToPods = []haproxyv1alpha1.PodDeploymentStatus{{PodName: "haproxy-1"}}
			current.ResourceVersion = "2"
			require.NoError(t, client.Tracker().Update(gvr, current, "default"))
		}
		preconditions := deletion.GetDeleteOptions().Preconditions
		require.NotNil(t, preconditions)
		require.NotNil(t, preconditions.UID)
		require.NotNil(t, preconditions.ResourceVersion)
		assert.Equal(t, current.UID, *preconditions.UID)
		if *preconditions.ResourceVersion != current.ResourceVersion {
			return true, nil, apierrors.NewConflict(gvr.GroupResource(), name, errors.New("pod status changed after list"))
		}
		return false, nil, nil
	})

	req.AuxiliaryFiles = nil
	_, err = publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)
	assert.Equal(t, 2, attempts)
	_, err = maps.Get(ctx, name, metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err))
}

func TestPublishConfig_CleanupRefreshesEveryAuxiliaryKind(t *testing.T) {
	tests := []struct {
		resource string
		files    AuxiliaryFiles
		name     func(*PublishResult) string
	}{
		{resource: "secrets", files: AuxiliaryFiles{SSLCertificates: []auxiliaryfiles.SSLCertificate{{Path: "cert.pem", Content: "cert"}}},
			name: func(result *PublishResult) string { return result.SecretNames[0] }},
		{resource: "haproxygeneralfiles", files: AuxiliaryFiles{GeneralFiles: []auxiliaryfiles.GeneralFile{{Filename: "error.http", Content: "error"}}},
			name: func(result *PublishResult) string { return result.GeneralFileNames[0] }},
		{resource: "haproxycrtlistfiles", files: AuxiliaryFiles{CRTListFiles: []auxiliaryfiles.CRTListFile{{Path: "crt-list.txt", Content: "cert.pem"}}},
			name: func(result *PublishResult) string { return result.CRTListFileNames[0] }},
	}
	for i := range tests {
		tt := &tests[i]
		t.Run(tt.resource, func(t *testing.T) {
			ctx, coreClient, client, publisher := newTestPublisher(t)
			req := basePublishRequest()
			req.AuxiliaryFiles = &tt.files
			initial, err := publisher.PublishConfig(ctx, &req)
			require.NoError(t, err)
			name := tt.name(initial)
			tracker, fakeClient := client.Tracker(), &client.Fake
			gvr := schema.GroupVersionResource{Group: "haproxy-haptic.org", Version: "v1alpha1", Resource: tt.resource}
			if tt.resource == "secrets" {
				tracker, fakeClient = coreClient.Tracker(), &coreClient.Fake
				gvr.Group, gvr.Version = "", "v1"
			}
			obj, err := tracker.Get(gvr, "default", name)
			require.NoError(t, err)
			metadata, err := meta.Accessor(obj)
			require.NoError(t, err)
			metadata.SetUID("file-uid")
			metadata.SetResourceVersion("1")
			require.NoError(t, tracker.Update(gvr, obj, "default"))
			attempts := 0
			fakeClient.PrependReactor("delete", tt.resource, func(action k8stesting.Action) (bool, runtime.Object, error) {
				attempts++
				if attempts == 1 {
					metadata.SetResourceVersion("2")
					require.NoError(t, tracker.Update(gvr, obj, "default"))
				}
				options := action.(k8stesting.DeleteAction).GetDeleteOptions()
				require.NotNil(t, options.Preconditions)
				require.NotNil(t, options.Preconditions.UID)
				require.NotNil(t, options.Preconditions.ResourceVersion)
				assert.Equal(t, metadata.GetUID(), *options.Preconditions.UID)
				if *options.Preconditions.ResourceVersion != metadata.GetResourceVersion() {
					return true, nil, apierrors.NewConflict(gvr.GroupResource(), name, errors.New("concurrent update"))
				}
				return false, nil, nil
			})
			req.AuxiliaryFiles = nil
			_, err = publisher.PublishConfig(ctx, &req)
			require.NoError(t, err)
			assert.Equal(t, 2, attempts)
			_, err = tracker.Get(gvr, "default", name)
			assert.True(t, apierrors.IsNotFound(err))
		})
	}
}
