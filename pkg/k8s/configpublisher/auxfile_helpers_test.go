// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package configpublisher

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	haproxyv1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// ---------------------------------------------------------------------------
// mutateAuxFilePodStatus
// ---------------------------------------------------------------------------

func TestMutateAuxFilePodStatus(t *testing.T) {
	t.Run("applies mutation and writes back", func(t *testing.T) {
		var appliedPods []haproxyv1alpha1.PodDeploymentStatus
		handle := &auxFileHandle{
			pods:     []haproxyv1alpha1.PodDeploymentStatus{{PodName: "pod-1"}},
			checksum: "sha256:abc",
			applyStatus: func(pods []haproxyv1alpha1.PodDeploymentStatus) error {
				appliedPods = pods
				return nil
			},
		}

		err := mutateAuxFilePodStatus(
			func() (*auxFileHandle, error) { return handle, nil },
			func(pods []haproxyv1alpha1.PodDeploymentStatus) ([]haproxyv1alpha1.PodDeploymentStatus, bool) {
				return append(pods, haproxyv1alpha1.PodDeploymentStatus{PodName: "pod-2"}), true
			},
		)

		require.NoError(t, err)
		require.Len(t, appliedPods, 2)
		assert.Equal(t, "pod-1", appliedPods[0].PodName)
		assert.Equal(t, "pod-2", appliedPods[1].PodName)
	})

	t.Run("skips write when mutation reports no change", func(t *testing.T) {
		writeCalled := false
		handle := &auxFileHandle{
			pods:     []haproxyv1alpha1.PodDeploymentStatus{{PodName: "pod-1"}},
			checksum: "sha256:abc",
			applyStatus: func(_ []haproxyv1alpha1.PodDeploymentStatus) error {
				writeCalled = true
				return nil
			},
		}

		err := mutateAuxFilePodStatus(
			func() (*auxFileHandle, error) { return handle, nil },
			func(pods []haproxyv1alpha1.PodDeploymentStatus) ([]haproxyv1alpha1.PodDeploymentStatus, bool) {
				return pods, false // no change
			},
		)

		require.NoError(t, err)
		assert.False(t, writeCalled, "applyStatus should not be called when no change")
	})

	t.Run("returns nil when resource not found", func(t *testing.T) {
		notFoundErr := apierrors.NewNotFound(
			schema.GroupResource{Group: "haproxytemplate.io", Resource: "haproxymapfiles"},
			"missing-map",
		)

		err := mutateAuxFilePodStatus(
			func() (*auxFileHandle, error) { return nil, notFoundErr },
			func(pods []haproxyv1alpha1.PodDeploymentStatus) ([]haproxyv1alpha1.PodDeploymentStatus, bool) {
				t.Fatal("mutation should not be called when resource is not found")
				return pods, false
			},
		)

		assert.NoError(t, err)
	})

	t.Run("propagates non-NotFound errors", func(t *testing.T) {
		err := mutateAuxFilePodStatus(
			func() (*auxFileHandle, error) { return nil, errors.New("connection refused") },
			func(pods []haproxyv1alpha1.PodDeploymentStatus) ([]haproxyv1alpha1.PodDeploymentStatus, bool) {
				t.Fatal("mutation should not be called on error")
				return pods, false
			},
		)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "connection refused")
	})

	t.Run("propagates applyStatus errors", func(t *testing.T) {
		handle := &auxFileHandle{
			pods:     []haproxyv1alpha1.PodDeploymentStatus{{PodName: "pod-1"}},
			checksum: "sha256:abc",
			applyStatus: func(_ []haproxyv1alpha1.PodDeploymentStatus) error {
				return errors.New("API server unavailable")
			},
		}

		err := mutateAuxFilePodStatus(
			func() (*auxFileHandle, error) { return handle, nil },
			func(pods []haproxyv1alpha1.PodDeploymentStatus) ([]haproxyv1alpha1.PodDeploymentStatus, bool) {
				return pods, true
			},
		)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "API server unavailable")
	})
}

// ---------------------------------------------------------------------------
// filterRunningPods
// ---------------------------------------------------------------------------

func TestFilterRunningPods(t *testing.T) {
	t.Run("keeps only running pods", func(t *testing.T) {
		runningSet := map[string]struct{}{
			"pod-1": {},
			"pod-3": {},
		}

		var loggedRemoved []string
		mutate := filterRunningPods(runningSet, func(removed []string) {
			loggedRemoved = removed
		})

		pods := []haproxyv1alpha1.PodDeploymentStatus{
			{PodName: "pod-1"},
			{PodName: "pod-2"},
			{PodName: "pod-3"},
			{PodName: "pod-4"},
		}

		result, changed := mutate(pods)
		assert.True(t, changed)
		require.Len(t, result, 2)
		assert.Equal(t, "pod-1", result[0].PodName)
		assert.Equal(t, "pod-3", result[1].PodName)

		assert.ElementsMatch(t, []string{"pod-2", "pod-4"}, loggedRemoved)
	})

	t.Run("returns unchanged when all pods are running", func(t *testing.T) {
		runningSet := map[string]struct{}{
			"pod-1": {},
			"pod-2": {},
		}

		logCalled := false
		mutate := filterRunningPods(runningSet, func(_ []string) {
			logCalled = true
		})

		pods := []haproxyv1alpha1.PodDeploymentStatus{
			{PodName: "pod-1"},
			{PodName: "pod-2"},
		}

		result, changed := mutate(pods)
		assert.False(t, changed)
		assert.Len(t, result, 2)
		assert.False(t, logCalled, "logRemoved should not be called when nothing is removed")
	})

	t.Run("removes all pods when none are running", func(t *testing.T) {
		runningSet := map[string]struct{}{}

		mutate := filterRunningPods(runningSet, nil)

		pods := []haproxyv1alpha1.PodDeploymentStatus{
			{PodName: "pod-1"},
			{PodName: "pod-2"},
		}

		result, changed := mutate(pods)
		assert.True(t, changed)
		assert.Empty(t, result)
	})

	t.Run("empty pods input returns unchanged", func(t *testing.T) {
		runningSet := map[string]struct{}{"pod-1": {}}
		mutate := filterRunningPods(runningSet, nil)

		result, changed := mutate(nil)
		assert.False(t, changed)
		assert.Nil(t, result)
	})

	t.Run("nil logRemoved callback does not panic", func(t *testing.T) {
		runningSet := map[string]struct{}{}
		mutate := filterRunningPods(runningSet, nil)

		pods := []haproxyv1alpha1.PodDeploymentStatus{{PodName: "pod-1"}}

		assert.NotPanics(t, func() {
			result, changed := mutate(pods)
			assert.True(t, changed)
			assert.Empty(t, result)
		})
	})
}

// ---------------------------------------------------------------------------
// removePodMutation
// ---------------------------------------------------------------------------

func TestRemovePodMutation(t *testing.T) {
	t.Run("removes existing pod", func(t *testing.T) {
		mutate := removePodMutation("pod-2")
		pods := []haproxyv1alpha1.PodDeploymentStatus{
			{PodName: "pod-1"},
			{PodName: "pod-2"},
			{PodName: "pod-3"},
		}

		result, changed := mutate(pods)
		assert.True(t, changed)
		require.Len(t, result, 2)
		assert.Equal(t, "pod-1", result[0].PodName)
		assert.Equal(t, "pod-3", result[1].PodName)
	})

	t.Run("reports no change when pod not found", func(t *testing.T) {
		mutate := removePodMutation("pod-99")
		pods := []haproxyv1alpha1.PodDeploymentStatus{
			{PodName: "pod-1"},
		}

		result, changed := mutate(pods)
		assert.False(t, changed)
		assert.Len(t, result, 1)
	})

	t.Run("handles empty pod list", func(t *testing.T) {
		mutate := removePodMutation("pod-1")
		result, changed := mutate(nil)
		assert.False(t, changed)
		assert.Empty(t, result)
	})
}

// ---------------------------------------------------------------------------
// updateAuxFileDeploymentStatus
// ---------------------------------------------------------------------------

func TestUpdateAuxFileDeploymentStatus(t *testing.T) {
	now := metav1.Now()

	t.Run("skips update when cached read shows no change", func(t *testing.T) {
		handleCalled := false
		podStatus := &haproxyv1alpha1.PodDeploymentStatus{
			PodName:    "pod-1",
			Checksum:   "sha256:abc",
			DeployedAt: now,
		}

		err := updateAuxFileDeploymentStatus(
			podStatus,
			func() *cachedAuxFileStatus {
				return &cachedAuxFileStatus{
					pods: []haproxyv1alpha1.PodDeploymentStatus{
						{PodName: "pod-1", Checksum: "sha256:abc", DeployedAt: now},
					},
					checksum: "sha256:abc",
				}
			},
			func() (*auxFileHandle, error) {
				handleCalled = true
				return nil, errors.New("should not be called")
			},
			"mapfile",
		)

		require.NoError(t, err)
		assert.False(t, handleCalled, "getHandle should not be called when cache shows no change")
	})

	t.Run("proceeds to API when cache shows change needed", func(t *testing.T) {
		var appliedPods []haproxyv1alpha1.PodDeploymentStatus
		podStatus := &haproxyv1alpha1.PodDeploymentStatus{
			PodName:    "pod-new",
			Checksum:   "sha256:abc",
			DeployedAt: now,
		}

		err := updateAuxFileDeploymentStatus(
			podStatus,
			func() *cachedAuxFileStatus {
				return &cachedAuxFileStatus{
					pods:     nil, // no existing pods, so adding one is a change
					checksum: "sha256:abc",
				}
			},
			func() (*auxFileHandle, error) {
				return &auxFileHandle{
					pods:     nil,
					checksum: "sha256:abc",
					applyStatus: func(pods []haproxyv1alpha1.PodDeploymentStatus) error {
						appliedPods = pods
						return nil
					},
				}, nil
			},
			"mapfile",
		)

		require.NoError(t, err)
		require.Len(t, appliedPods, 1)
		assert.Equal(t, "pod-new", appliedPods[0].PodName)
	})

	t.Run("proceeds to API when no cache available", func(t *testing.T) {
		var appliedPods []haproxyv1alpha1.PodDeploymentStatus
		podStatus := &haproxyv1alpha1.PodDeploymentStatus{
			PodName:    "pod-1",
			Checksum:   "sha256:abc",
			DeployedAt: now,
		}

		err := updateAuxFileDeploymentStatus(
			podStatus,
			func() *cachedAuxFileStatus {
				return nil // no cache
			},
			func() (*auxFileHandle, error) {
				return &auxFileHandle{
					pods:     nil,
					checksum: "sha256:abc",
					applyStatus: func(pods []haproxyv1alpha1.PodDeploymentStatus) error {
						appliedPods = pods
						return nil
					},
				}, nil
			},
			"mapfile",
		)

		require.NoError(t, err)
		require.Len(t, appliedPods, 1)
	})

	t.Run("returns nil when resource not found during API read", func(t *testing.T) {
		notFoundErr := apierrors.NewNotFound(
			schema.GroupResource{Group: "haproxytemplate.io", Resource: "haproxymapfiles"},
			"missing",
		)
		podStatus := &haproxyv1alpha1.PodDeploymentStatus{
			PodName:    "pod-1",
			DeployedAt: now,
		}

		err := updateAuxFileDeploymentStatus(
			podStatus,
			func() *cachedAuxFileStatus { return nil },
			func() (*auxFileHandle, error) { return nil, notFoundErr },
			"mapfile",
		)

		assert.NoError(t, err)
	})

	t.Run("propagates API errors", func(t *testing.T) {
		podStatus := &haproxyv1alpha1.PodDeploymentStatus{
			PodName:    "pod-1",
			DeployedAt: now,
		}

		err := updateAuxFileDeploymentStatus(
			podStatus,
			func() *cachedAuxFileStatus { return nil },
			func() (*auxFileHandle, error) { return nil, errors.New("server error") },
			"mapfile",
		)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "server error")
	})

	t.Run("skips API write when fresh read shows no change", func(t *testing.T) {
		writeCalled := false
		podStatus := &haproxyv1alpha1.PodDeploymentStatus{
			PodName:    "pod-1",
			Checksum:   "sha256:abc",
			DeployedAt: now,
		}

		err := updateAuxFileDeploymentStatus(
			podStatus,
			func() *cachedAuxFileStatus { return nil },
			func() (*auxFileHandle, error) {
				return &auxFileHandle{
					pods: []haproxyv1alpha1.PodDeploymentStatus{
						{PodName: "pod-1", Checksum: "sha256:abc", DeployedAt: now},
					},
					checksum: "sha256:abc",
					applyStatus: func(_ []haproxyv1alpha1.PodDeploymentStatus) error {
						writeCalled = true
						return nil
					},
				}, nil
			},
			"mapfile",
		)

		require.NoError(t, err)
		assert.False(t, writeCalled, "write should be skipped when status is unchanged")
	})
}
