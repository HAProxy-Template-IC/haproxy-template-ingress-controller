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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	haproxyv1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation"
	k8stesting "k8s.io/client-go/testing"
)

func TestPublishConfig_ReusesUnchangedChildren(t *testing.T) {
	ctx, core, client, publisher := newTestPublisher(t)
	req := mapFilesRequest("first", 2)
	req.AuxiliaryFiles.SSLCertificates = []auxiliaryfiles.SSLCertificate{{Path: "cert.pem", Content: "cert"}}
	req.AuxiliaryFiles.SSLCaFiles = []auxiliaryfiles.SSLCaFile{{Path: "ca.pem", Content: "ca"}}
	req.AuxiliaryFiles.GeneralFiles = []auxiliaryfiles.GeneralFile{{Filename: "error.http", Content: "error"}}
	req.AuxiliaryFiles.CRTListFiles = []auxiliaryfiles.CRTListFile{{Path: "list.txt", Content: "cert.pem"}}
	first, err := publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)
	client.ClearActions()
	core.ClearActions()
	req.AuxiliaryFiles.MapFiles[0].Content = "changed"
	second, err := publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)
	assert.NotEqual(t, first.MapFileNames[0], second.MapFileNames[0])
	assert.Equal(t, first.MapFileNames[1:], second.MapFileNames[1:])
	assert.Equal(t, first.SecretNames, second.SecretNames)
	assert.Equal(t, first.SSLCaFileNames, second.SSLCaFileNames)
	assert.Equal(t, first.GeneralFileNames, second.GeneralFileNames)
	assert.Equal(t, first.CRTListFileNames, second.CRTListFileNames)
	mutations := map[string]int{}
	for _, action := range append(client.Actions(), core.Actions()...) {
		if action.GetResource().Resource == "haproxycfgs" || action.GetVerb() == "get" || action.GetVerb() == "list" {
			continue
		}
		mutations[action.GetVerb()+" "+action.GetResource().Resource]++
	}
	assert.Equal(t, map[string]int{"create haproxymapfiles": 1, "delete haproxymapfiles": 1}, mutations)
}

func TestPublishConfig_ReclaimsReturningContent(t *testing.T) {
	ctx, _, client, publisher := newTestPublisher(t)
	req := mapFilesRequest("first", 1)
	first, err := publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)
	maps := client.HaproxyTemplateICV1alpha1().HAProxyMapFiles("default")
	initial, err := maps.Get(ctx, first.MapFileNames[0], metav1.GetOptions{})
	require.NoError(t, err)
	client.PrependReactor("delete", "haproxymapfiles", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return action.(k8stesting.DeleteAction).GetName() == initial.Name, nil, nil
	})
	req.AuxiliaryFiles.MapFiles[0].Content = "second"
	_, err = publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)
	req.AuxiliaryFiles.MapFiles[0].Content = "first"
	last, err := publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)
	assert.Equal(t, first.MapFileNames, last.MapFileNames)
	returned, err := maps.Get(ctx, initial.Name, metav1.GetOptions{})
	require.NoError(t, err)
	assert.NotEmpty(t, initial.Annotations[auxiliaryClaimAnnotationKey])
	assert.NotEqual(t, initial.Annotations[auxiliaryClaimAnnotationKey], returned.Annotations[auxiliaryClaimAnnotationKey])
}

func TestPublishConfig_StaleCleanupCannotAdoptNewClaim(t *testing.T) {
	ctx, _, client, publisher := newTestPublisher(t)
	req := mapFilesRequest("first", 1)
	first, err := publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)
	resource := haproxyv1alpha1.SchemeGroupVersion.WithResource("haproxymapfiles")
	attempts := 0
	client.PrependReactor("delete", resource.Resource, func(k8stesting.Action) (bool, runtime.Object, error) {
		attempts++
		obj, getErr := client.Tracker().Get(resource, "default", first.MapFileNames[0])
		require.NoError(t, getErr)
		file := obj.(*haproxyv1alpha1.HAProxyMapFile)
		file.Annotations[auxiliaryClaimAnnotationKey] = "new-publication"
		require.NoError(t, client.Tracker().Update(resource, file, "default"))
		return true, nil, apierrors.NewConflict(resource.GroupResource(), file.Name, errors.New("child reclaimed"))
	})
	req.AuxiliaryFiles.MapFiles[0].Content = "second"
	_, err = publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)
	assert.Equal(t, 1, attempts)
	_, err = client.Tracker().Get(resource, "default", first.MapFileNames[0])
	require.NoError(t, err)
}

func TestPublishConfig_TerminatingChildCannotCommit(t *testing.T) {
	ctx, _, client, publisher := newTestPublisher(t)
	req := mapFilesRequest("first", 1)
	first, err := publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)
	maps := client.HaproxyTemplateICV1alpha1().HAProxyMapFiles("default")
	file, err := maps.Get(ctx, first.MapFileNames[0], metav1.GetOptions{})
	require.NoError(t, err)
	file.DeletionTimestamp = new(metav1.Now())
	_, err = maps.Update(ctx, file, metav1.UpdateOptions{})
	require.NoError(t, err)
	_, err = publisher.PublishConfig(ctx, &req)
	require.ErrorContains(t, err, "is terminating")
}

func TestPublishConfig_ParentRecreationIsolatesChildren(t *testing.T) {
	ctx, _, client, publisher := newTestPublisher(t)
	req := mapFilesRequest("same-content", 1)
	first, err := publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)
	configs := client.HaproxyTemplateICV1alpha1().HAProxyCfgs("default")
	parent, err := configs.Get(ctx, first.RuntimeConfigName, metav1.GetOptions{})
	require.NoError(t, err)
	oldUID := parent.UID
	require.NoError(t, configs.Delete(ctx, parent.Name, metav1.DeleteOptions{}))
	parent.UID = "new-parent"
	parent.ResourceVersion = ""
	parent.Status.AuxiliaryFiles = nil
	_, err = configs.Create(ctx, parent, metav1.CreateOptions{})
	require.NoError(t, err)
	second, err := publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)
	assert.NotEqual(t, first.MapFileNames, second.MapFileNames)
	old, err := client.HaproxyTemplateICV1alpha1().HAProxyMapFiles("default").Get(ctx, first.MapFileNames[0], metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, oldUID, old.OwnerReferences[0].UID)
}

func TestPublishConfig_ContentNamesRemainValidAndDistinct(t *testing.T) {
	ctx, _, _, publisher := newTestPublisher(t)
	req := basePublishRequest()
	req.NameSuffix = "-invalid"
	req.AuxiliaryFiles = &AuxiliaryFiles{GeneralFiles: []auxiliaryfiles.GeneralFile{
		{Filename: "error.http", Content: "first"},
		{Filename: "error.txt", Content: "second"},
		{Filename: strings.Repeat("A", 300) + ".txt", Content: "long"},
	}}
	result, err := publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)
	require.Len(t, result.GeneralFileNames, 3)
	assert.NotEqual(t, result.GeneralFileNames[0], result.GeneralFileNames[1])
	for _, name := range result.GeneralFileNames {
		assert.Empty(t, validation.IsDNS1123Subdomain(name), name)
		assert.LessOrEqual(t, len(name), validation.DNS1123SubdomainMaxLength)
		assert.True(t, strings.HasSuffix(name, "-invalid"))
	}
}

func TestPublishConfig_RecreatesChildDeletedBeforeClaim(t *testing.T) {
	ctx, _, client, publisher := newTestPublisher(t)
	req := mapFilesRequest("first", 1)
	first, err := publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)
	resource := haproxyv1alpha1.SchemeGroupVersion.WithResource("haproxymapfiles")
	client.PrependReactor("delete", resource.Resource, func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, nil
	})
	empty := basePublishRequest()
	_, err = publisher.PublishConfig(ctx, &empty)
	require.NoError(t, err)
	deleted := false
	client.PrependReactor("update", resource.Resource, func(k8stesting.Action) (bool, runtime.Object, error) {
		if deleted {
			return false, nil, nil
		}
		deleted = true
		require.NoError(t, client.Tracker().Delete(resource, "default", first.MapFileNames[0]))
		return true, nil, apierrors.NewNotFound(resource.GroupResource(), first.MapFileNames[0])
	})
	last, err := publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)
	assert.True(t, deleted)
	assert.Equal(t, first.MapFileNames, last.MapFileNames)
	file, err := client.HaproxyTemplateICV1alpha1().HAProxyMapFiles("default").Get(ctx, last.MapFileNames[0], metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "first", file.Spec.Entries)
}
