// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package controller

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	fakediscovery "k8s.io/client-go/discovery/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
)

// fakeDiscoveryWithErr wraps the fake discovery to inject per-groupVersion errors.
type fakeDiscoveryWithErr struct {
	discovery.DiscoveryInterface
	errs map[string]error
}

func (f *fakeDiscoveryWithErr) ServerResourcesForGroupVersion(gv string) (*metav1.APIResourceList, error) {
	if err, ok := f.errs[gv]; ok {
		return nil, err
	}
	return f.DiscoveryInterface.ServerResourcesForGroupVersion(gv)
}

// TestDiscoveryServedChecker_TransientVsNotFound pins the error
// discrimination (runtime-version-detection review finding): only an
// authoritative NotFound counts as unserved; any other discovery error is
// recorded as transient so the caller fails the resolution instead of
// silently stripping optional features on an apiserver blip.
func TestDiscoveryServedChecker_TransientVsNotFound(t *testing.T) {
	cs := kubefake.NewSimpleClientset()
	fd := cs.Discovery().(*fakediscovery.FakeDiscovery)
	fd.Resources = []*metav1.APIResourceList{{
		GroupVersion: "example.io/v1",
		APIResources: []metav1.APIResource{{Name: "widgets"}},
	}}

	notFound := apierrors.NewNotFound(schema.GroupResource{Group: "missing.io"}, "v1")
	d := &fakeDiscoveryWithErr{DiscoveryInterface: fd, errs: map[string]error{
		"missing.io/v1": notFound,
		"flaky.io/v1":   assert.AnError,
	}}

	checker := newDiscoveryServedChecker(d, slog.Default())

	assert.True(t, checker.IsServed("example.io/v1", "widgets"))
	assert.False(t, checker.IsServed("example.io/v1", "gadgets"))
	require.NoError(t, checker.TransientErr(), "served/unlisted answers are authoritative")

	assert.False(t, checker.IsServed("missing.io/v1", "widgets"))
	require.NoError(t, checker.TransientErr(), "NotFound is authoritative unserved, not transient")

	assert.False(t, checker.IsServed("flaky.io/v1", "widgets"))
	require.Error(t, checker.TransientErr(), "non-NotFound discovery errors must be recorded as transient")
}
