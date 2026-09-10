//go:build e2e

// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package e2e

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	v1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
	hapticclient "gitlab.com/haproxy-haptic/haptic/pkg/generated/clientset/versioned"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/configpublisher"
)

func TestPublicationStatusConcurrentWriters(t *testing.T) {
	feature := features.New("publication status field isolation").Assess("atomic publication with concurrent API writes",
		func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			t.Helper()
			for _, test := range []struct {
				name            string
				validationError string
				supersede       bool
				initialization  bool
			}{
				{name: "auxiliary-references"},
				{name: "status-initialization", initialization: true},
				{name: "validation-error", validationError: "synthetic rejection"},
				{name: "superseded-source", supersede: true},
			} {
				t.Run(test.name, func(t *testing.T) {
					assertPublicationStatusRace(ctx, t, cfg, test.validationError, test.supersede, test.initialization)
				})
			}
			return ctx
		})
	testEnv.Test(t, feature.Feature())
}

func assertPublicationStatusRace(ctx context.Context, t *testing.T, cfg *envconf.Config, validationError string, supersede, initialization bool) {
	t.Helper()
	client, namespace := hapticClientAndNamespace(ctx, t, cfg)
	parent, err := client.HaproxyTemplateICV1alpha1().HAProxyTemplateConfigs(namespace).
		Create(ctx, minimalValidHAProxyTemplateConfig(namespace, "publication"), metav1.CreateOptions{})
	require.NoError(t, err)
	request := &configpublisher.PublishRequest{
		TemplateConfigName: parent.Name, TemplateConfigNamespace: namespace, TemplateConfigUID: parent.UID,
		Config: parent.Spec.HAProxyConfig.Template, ConfigPath: "/etc/haproxy/haproxy.cfg", Checksum: "synthetic-checksum",
		ValidationError: validationError,
		AuxiliaryFiles:  &configpublisher.AuxiliaryFiles{MapFiles: []auxiliaryfiles.MapFile{{Path: "maps/test.map", Content: "key backend\n"}}},
	}
	if validationError != "" {
		request.NameSuffix = "-invalid"
	}
	name := "publication-haproxycfg" + request.NameSuffix
	interleaver := &publicationStatusInterleaver{
		client: client, namespace: namespace, name: name, supersede: supersede, initialization: initialization,
	}
	wrapped := rest.CopyConfig(cfg.Client().RESTConfig())
	wrapped.Wrap(func(base http.RoundTripper) http.RoundTripper {
		interleaver.base = base
		return interleaver
	})
	publicationClient, err := hapticclient.NewForConfig(wrapped)
	require.NoError(t, err)
	kubeClient, err := newClientsetForE2E(cfg.Client().RESTConfig())
	require.NoError(t, err)
	publisher := configpublisher.NewWithListers(kubeClient, publicationClient, nil, slog.Default())
	result, publishErr := publisher.PublishConfig(ctx, request)
	require.True(t, interleaver.called)
	current, err := client.HaproxyTemplateICV1alpha1().HAProxyCfgs(namespace).Get(ctx, name, metav1.GetOptions{})
	require.NoError(t, err)
	if supersede {
		require.Error(t, publishErr)
		assert.Nil(t, current.Status.AuxiliaryFiles)
		assert.Contains(t, current.Spec.Content, "# newer source")
		return
	}
	require.NoError(t, publishErr)
	require.NotNil(t, current.Status.AuxiliaryFiles)
	require.Len(t, current.Status.AuxiliaryFiles.MapFiles, 1)
	assert.Equal(t, result.MapFileNames[0], current.Status.AuxiliaryFiles.MapFiles[0].Name)
	assert.Equal(t, validationError, current.Status.ValidationError)
	assert.Equal(t, concurrentPublicationPodStatus(), current.Status.DeployedToPods)
	assert.EqualValues(t, 42, current.Status.ObservedGeneration)
}

type publicationStatusInterleaver struct {
	base           http.RoundTripper
	client         hapticclient.Interface
	namespace      string
	name           string
	supersede      bool
	initialization bool
	once           sync.Once
	called         bool
	err            error
}

func (i *publicationStatusInterleaver) RoundTrip(request *http.Request) (*http.Response, error) {
	patchType := request.Header.Get("Content-Type")
	statusWrite := request.Method == http.MethodPut || patchType == string(types.JSONPatchType) ||
		i.initialization && patchType == string(types.MergePatchType)
	if statusWrite && strings.HasSuffix(request.URL.Path, "/haproxycfgs/"+i.name+"/status") {
		i.once.Do(func() {
			i.called = true
			i.err = i.mutate(request.Context())
		})
		if i.err != nil {
			return nil, i.err
		}
	}
	return i.base.RoundTrip(request)
}

func (i *publicationStatusInterleaver) mutate(ctx context.Context) error {
	client := i.client.HaproxyTemplateICV1alpha1().HAProxyCfgs(i.namespace)
	current, err := client.Get(ctx, i.name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if i.supersede {
		current.Spec.Content += "\n# newer source\n"
		_, err = client.Update(ctx, current, metav1.UpdateOptions{})
		return err
	}
	current.Status.DeployedToPods = concurrentPublicationPodStatus()
	current.Status.ObservedGeneration = 42
	_, err = client.UpdateStatus(ctx, current, metav1.UpdateOptions{})
	return err
}

func concurrentPublicationPodStatus() []v1alpha1.PodDeploymentStatus {
	return []v1alpha1.PodDeploymentStatus{
		{PodName: "haproxy-first", PodUID: "first-uid", Checksum: "first-checksum"},
		{PodName: "haproxy-second", PodUID: "second-uid", Checksum: "second-checksum"},
	}
}
