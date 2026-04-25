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

	"github.com/stretchr/testify/assert"

	haproxyv1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"
)

// removePodFromList and filterStalePods are pure helpers on Publisher (they
// don't use any Publisher state) that gate two cleanup paths:
//   - removePodFromList strips a single terminated pod from a HAProxyCfg
//     status during cleanupRuntimeConfigPodReference;
//   - filterStalePods partitions DeployedToPods into still-running vs stale
//     pods during ReconcileDeployedToPods.
//
// Both are pinned with table-driven tests so future refactors can't silently
// keep stale entries or drop running ones.

func podStatus(name string) haproxyv1alpha1.PodDeploymentStatus {
	return haproxyv1alpha1.PodDeploymentStatus{PodName: name}
}

func TestPublisher_RemovePodFromList(t *testing.T) {
	p := &Publisher{}

	tests := []struct {
		name        string
		pods        []haproxyv1alpha1.PodDeploymentStatus
		cleanup     *PodCleanupRequest
		wantPods    []haproxyv1alpha1.PodDeploymentStatus
		wantRemoved bool
	}{
		{
			name:        "removes the named pod and reports removed=true",
			pods:        []haproxyv1alpha1.PodDeploymentStatus{podStatus("a"), podStatus("b"), podStatus("c")},
			cleanup:     &PodCleanupRequest{PodName: "b"},
			wantPods:    []haproxyv1alpha1.PodDeploymentStatus{podStatus("a"), podStatus("c")},
			wantRemoved: true,
		},
		{
			name:        "unknown pod leaves list intact and reports removed=false",
			pods:        []haproxyv1alpha1.PodDeploymentStatus{podStatus("a"), podStatus("b")},
			cleanup:     &PodCleanupRequest{PodName: "missing"},
			wantPods:    []haproxyv1alpha1.PodDeploymentStatus{podStatus("a"), podStatus("b")},
			wantRemoved: false,
		},
		{
			name:        "removes ALL duplicates of the named pod (every match is dropped)",
			pods:        []haproxyv1alpha1.PodDeploymentStatus{podStatus("a"), podStatus("dup"), podStatus("b"), podStatus("dup")},
			cleanup:     &PodCleanupRequest{PodName: "dup"},
			wantPods:    []haproxyv1alpha1.PodDeploymentStatus{podStatus("a"), podStatus("b")},
			wantRemoved: true,
		},
		{
			name:        "empty input returns empty list and removed=false",
			pods:        nil,
			cleanup:     &PodCleanupRequest{PodName: "any"},
			wantPods:    []haproxyv1alpha1.PodDeploymentStatus{},
			wantRemoved: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPods, gotRemoved := p.removePodFromList(tt.pods, tt.cleanup)
			assert.Equal(t, tt.wantPods, gotPods)
			assert.Equal(t, tt.wantRemoved, gotRemoved)
		})
	}
}

func TestPublisher_FilterStalePods(t *testing.T) {
	p := &Publisher{}

	tests := []struct {
		name            string
		deployed        []haproxyv1alpha1.PodDeploymentStatus
		runningSet      map[string]struct{}
		wantStale       []string
		wantRunningPods []haproxyv1alpha1.PodDeploymentStatus
	}{
		{
			name:            "all deployed pods running yields empty stale set",
			deployed:        []haproxyv1alpha1.PodDeploymentStatus{podStatus("a"), podStatus("b")},
			runningSet:      map[string]struct{}{"a": {}, "b": {}},
			wantStale:       nil,
			wantRunningPods: []haproxyv1alpha1.PodDeploymentStatus{podStatus("a"), podStatus("b")},
		},
		{
			name:            "missing pods are flagged stale and dropped from running list",
			deployed:        []haproxyv1alpha1.PodDeploymentStatus{podStatus("a"), podStatus("gone"), podStatus("c")},
			runningSet:      map[string]struct{}{"a": {}, "c": {}},
			wantStale:       []string{"gone"},
			wantRunningPods: []haproxyv1alpha1.PodDeploymentStatus{podStatus("a"), podStatus("c")},
		},
		{
			name:            "every deployed pod missing from running set",
			deployed:        []haproxyv1alpha1.PodDeploymentStatus{podStatus("a"), podStatus("b")},
			runningSet:      map[string]struct{}{},
			wantStale:       []string{"a", "b"},
			wantRunningPods: []haproxyv1alpha1.PodDeploymentStatus{},
		},
		{
			name:            "empty deployed list yields empty results",
			deployed:        nil,
			runningSet:      map[string]struct{}{"a": {}},
			wantStale:       nil,
			wantRunningPods: []haproxyv1alpha1.PodDeploymentStatus{},
		},
		{
			name:            "preserves order from the deployed slice",
			deployed:        []haproxyv1alpha1.PodDeploymentStatus{podStatus("c"), podStatus("a"), podStatus("b")},
			runningSet:      map[string]struct{}{"a": {}, "b": {}, "c": {}},
			wantStale:       nil,
			wantRunningPods: []haproxyv1alpha1.PodDeploymentStatus{podStatus("c"), podStatus("a"), podStatus("b")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStale, gotRunning := p.filterStalePods(tt.deployed, tt.runningSet)
			assert.ElementsMatch(t, tt.wantStale, gotStale, "stale pods (order-insensitive)")
			assert.Equal(t, tt.wantRunningPods, gotRunning, "running pods preserve deployed-list order")
		})
	}
}
