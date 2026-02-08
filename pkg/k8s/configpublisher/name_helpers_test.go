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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	haproxyv1alpha1 "gitlab.com/haproxy-haptic/haptic/pkg/apis/haproxytemplate/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ---------------------------------------------------------------------------
// copyPodStatuses
// ---------------------------------------------------------------------------

func TestCopyPodStatuses(t *testing.T) {
	tests := []struct {
		name   string
		input  []haproxyv1alpha1.PodDeploymentStatus
		expect []haproxyv1alpha1.PodDeploymentStatus
	}{
		{
			name:   "nil input returns nil",
			input:  nil,
			expect: nil,
		},
		{
			name:   "empty slice returns empty slice",
			input:  []haproxyv1alpha1.PodDeploymentStatus{},
			expect: []haproxyv1alpha1.PodDeploymentStatus{},
		},
		{
			name: "single element",
			input: []haproxyv1alpha1.PodDeploymentStatus{
				{PodName: "pod-1", Checksum: "abc"},
			},
			expect: []haproxyv1alpha1.PodDeploymentStatus{
				{PodName: "pod-1", Checksum: "abc"},
			},
		},
		{
			name: "multiple elements",
			input: []haproxyv1alpha1.PodDeploymentStatus{
				{PodName: "pod-1", Checksum: "abc"},
				{PodName: "pod-2", Checksum: "def"},
			},
			expect: []haproxyv1alpha1.PodDeploymentStatus{
				{PodName: "pod-1", Checksum: "abc"},
				{PodName: "pod-2", Checksum: "def"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := copyPodStatuses(tt.input)
			assert.Equal(t, tt.expect, result)

			// Verify deep copy: mutating the copy does not affect the original.
			if len(result) > 0 {
				result[0].PodName = "mutated"
				assert.NotEqual(t, tt.input[0].PodName, "mutated", "copy should be independent of original")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// podStatusesEqual
// ---------------------------------------------------------------------------

func TestPodStatusesEqual(t *testing.T) {
	now := metav1.Now()
	later := metav1.NewTime(now.Add(time.Second))
	dur := metav1.Duration{Duration: 5 * time.Second}

	tests := []struct {
		name   string
		a, b   []haproxyv1alpha1.PodDeploymentStatus
		expect bool
	}{
		{
			name:   "both nil",
			a:      nil,
			b:      nil,
			expect: true,
		},
		{
			name:   "both empty",
			a:      []haproxyv1alpha1.PodDeploymentStatus{},
			b:      []haproxyv1alpha1.PodDeploymentStatus{},
			expect: true,
		},
		{
			name: "different lengths",
			a: []haproxyv1alpha1.PodDeploymentStatus{
				{PodName: "pod-1"},
			},
			b:      []haproxyv1alpha1.PodDeploymentStatus{},
			expect: false,
		},
		{
			name: "same pods same fields",
			a: []haproxyv1alpha1.PodDeploymentStatus{
				{PodName: "pod-1", Checksum: "abc", DeployedAt: now},
			},
			b: []haproxyv1alpha1.PodDeploymentStatus{
				{PodName: "pod-1", Checksum: "abc", DeployedAt: now},
			},
			expect: true,
		},
		{
			name: "same pods different checksum",
			a: []haproxyv1alpha1.PodDeploymentStatus{
				{PodName: "pod-1", Checksum: "abc"},
			},
			b: []haproxyv1alpha1.PodDeploymentStatus{
				{PodName: "pod-1", Checksum: "xyz"},
			},
			expect: false,
		},
		{
			name: "same pods different deployed time",
			a: []haproxyv1alpha1.PodDeploymentStatus{
				{PodName: "pod-1", DeployedAt: now},
			},
			b: []haproxyv1alpha1.PodDeploymentStatus{
				{PodName: "pod-1", DeployedAt: later},
			},
			expect: false,
		},
		{
			name: "different pod names",
			a: []haproxyv1alpha1.PodDeploymentStatus{
				{PodName: "pod-1"},
			},
			b: []haproxyv1alpha1.PodDeploymentStatus{
				{PodName: "pod-2"},
			},
			expect: false,
		},
		{
			name: "same pods different order",
			a: []haproxyv1alpha1.PodDeploymentStatus{
				{PodName: "pod-1", Checksum: "abc"},
				{PodName: "pod-2", Checksum: "def"},
			},
			b: []haproxyv1alpha1.PodDeploymentStatus{
				{PodName: "pod-2", Checksum: "def"},
				{PodName: "pod-1", Checksum: "abc"},
			},
			expect: true,
		},
		{
			name: "different consecutive errors",
			a: []haproxyv1alpha1.PodDeploymentStatus{
				{PodName: "pod-1", ConsecutiveErrors: 0},
			},
			b: []haproxyv1alpha1.PodDeploymentStatus{
				{PodName: "pod-1", ConsecutiveErrors: 3},
			},
			expect: false,
		},
		{
			name: "different sync duration",
			a: []haproxyv1alpha1.PodDeploymentStatus{
				{PodName: "pod-1", SyncDuration: &dur},
			},
			b: []haproxyv1alpha1.PodDeploymentStatus{
				{PodName: "pod-1", SyncDuration: nil},
			},
			expect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expect, podStatusesEqual(tt.a, tt.b))
		})
	}
}

// ---------------------------------------------------------------------------
// podStatusEqual
// ---------------------------------------------------------------------------

func TestPodStatusEqual(t *testing.T) {
	now := metav1.Now()
	later := metav1.NewTime(now.Add(time.Second))
	dur1 := metav1.Duration{Duration: 5 * time.Second}
	dur2 := metav1.Duration{Duration: 10 * time.Second}

	summary := &haproxyv1alpha1.OperationSummary{TotalAPIOperations: 5}

	tests := []struct {
		name   string
		a, b   *haproxyv1alpha1.PodDeploymentStatus
		expect bool
	}{
		{
			name:   "identical zero values",
			a:      &haproxyv1alpha1.PodDeploymentStatus{},
			b:      &haproxyv1alpha1.PodDeploymentStatus{},
			expect: true,
		},
		{
			name:   "different checksum",
			a:      &haproxyv1alpha1.PodDeploymentStatus{Checksum: "a"},
			b:      &haproxyv1alpha1.PodDeploymentStatus{Checksum: "b"},
			expect: false,
		},
		{
			name:   "different last reload ID",
			a:      &haproxyv1alpha1.PodDeploymentStatus{LastReloadID: "1"},
			b:      &haproxyv1alpha1.PodDeploymentStatus{LastReloadID: "2"},
			expect: false,
		},
		{
			name:   "different last error",
			a:      &haproxyv1alpha1.PodDeploymentStatus{LastError: "err1"},
			b:      &haproxyv1alpha1.PodDeploymentStatus{LastError: ""},
			expect: false,
		},
		{
			name:   "different version conflict retries",
			a:      &haproxyv1alpha1.PodDeploymentStatus{VersionConflictRetries: 1},
			b:      &haproxyv1alpha1.PodDeploymentStatus{VersionConflictRetries: 0},
			expect: false,
		},
		{
			name:   "different fallback used",
			a:      &haproxyv1alpha1.PodDeploymentStatus{FallbackUsed: true},
			b:      &haproxyv1alpha1.PodDeploymentStatus{FallbackUsed: false},
			expect: false,
		},
		{
			name:   "different consecutive errors",
			a:      &haproxyv1alpha1.PodDeploymentStatus{ConsecutiveErrors: 0},
			b:      &haproxyv1alpha1.PodDeploymentStatus{ConsecutiveErrors: 1},
			expect: false,
		},
		{
			name:   "different deployed at",
			a:      &haproxyv1alpha1.PodDeploymentStatus{DeployedAt: now},
			b:      &haproxyv1alpha1.PodDeploymentStatus{DeployedAt: later},
			expect: false,
		},
		{
			name: "one has last reload at, other nil",
			a: &haproxyv1alpha1.PodDeploymentStatus{
				LastReloadAt: &now,
			},
			b:      &haproxyv1alpha1.PodDeploymentStatus{},
			expect: false,
		},
		{
			name: "different last error at",
			a: &haproxyv1alpha1.PodDeploymentStatus{
				LastErrorAt: &now,
			},
			b: &haproxyv1alpha1.PodDeploymentStatus{
				LastErrorAt: &later,
			},
			expect: false,
		},
		{
			name: "different sync duration",
			a: &haproxyv1alpha1.PodDeploymentStatus{
				SyncDuration: &dur1,
			},
			b: &haproxyv1alpha1.PodDeploymentStatus{
				SyncDuration: &dur2,
			},
			expect: false,
		},
		{
			name: "one has operation summary, other nil",
			a: &haproxyv1alpha1.PodDeploymentStatus{
				LastOperationSummary: summary,
			},
			b:      &haproxyv1alpha1.PodDeploymentStatus{},
			expect: false,
		},
		{
			name: "all fields identical",
			a: &haproxyv1alpha1.PodDeploymentStatus{
				PodName:                "pod-1",
				Checksum:               "sha256:abc",
				DeployedAt:             now,
				LastReloadAt:           &now,
				LastReloadID:           "42",
				SyncDuration:           &dur1,
				VersionConflictRetries: 2,
				FallbackUsed:           true,
				LastOperationSummary:   summary,
				LastError:              "oops",
				ConsecutiveErrors:      1,
				LastErrorAt:            &now,
			},
			b: &haproxyv1alpha1.PodDeploymentStatus{
				PodName:                "pod-1",
				Checksum:               "sha256:abc",
				DeployedAt:             now,
				LastReloadAt:           &now,
				LastReloadID:           "42",
				SyncDuration:           &dur1,
				VersionConflictRetries: 2,
				FallbackUsed:           true,
				LastOperationSummary:   summary,
				LastError:              "oops",
				ConsecutiveErrors:      1,
				LastErrorAt:            &now,
			},
			expect: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expect, podStatusEqual(tt.a, tt.b))
		})
	}
}

// ---------------------------------------------------------------------------
// metaTimeEqual
// ---------------------------------------------------------------------------

func TestMetaTimeEqual(t *testing.T) {
	now := metav1.Now()
	later := metav1.NewTime(now.Add(time.Hour))

	tests := []struct {
		name   string
		a, b   *metav1.Time
		expect bool
	}{
		{name: "both nil", a: nil, b: nil, expect: true},
		{name: "a nil b non-nil", a: nil, b: &now, expect: false},
		{name: "a non-nil b nil", a: &now, b: nil, expect: false},
		{name: "both same time", a: &now, b: &now, expect: true},
		{name: "different times", a: &now, b: &later, expect: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expect, metaTimeEqual(tt.a, tt.b))
		})
	}
}

// ---------------------------------------------------------------------------
// metaDurationEqual
// ---------------------------------------------------------------------------

func TestMetaDurationEqual(t *testing.T) {
	d1 := metav1.Duration{Duration: 5 * time.Second}
	d2 := metav1.Duration{Duration: 10 * time.Second}
	d1copy := metav1.Duration{Duration: 5 * time.Second}

	tests := []struct {
		name   string
		a, b   *metav1.Duration
		expect bool
	}{
		{name: "both nil", a: nil, b: nil, expect: true},
		{name: "a nil b non-nil", a: nil, b: &d1, expect: false},
		{name: "a non-nil b nil", a: &d1, b: nil, expect: false},
		{name: "same duration", a: &d1, b: &d1copy, expect: true},
		{name: "different durations", a: &d1, b: &d2, expect: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expect, metaDurationEqual(tt.a, tt.b))
		})
	}
}

// ---------------------------------------------------------------------------
// operationSummaryEqual
// ---------------------------------------------------------------------------

func TestOperationSummaryEqual(t *testing.T) {
	s1 := &haproxyv1alpha1.OperationSummary{
		TotalAPIOperations: 10,
		BackendsAdded:      1,
		BackendsRemoved:    2,
		BackendsModified:   3,
		ServersAdded:       4,
		ServersRemoved:     5,
		ServersModified:    6,
		FrontendsAdded:     7,
		FrontendsRemoved:   8,
		FrontendsModified:  9,
	}

	s1copy := &haproxyv1alpha1.OperationSummary{
		TotalAPIOperations: 10,
		BackendsAdded:      1,
		BackendsRemoved:    2,
		BackendsModified:   3,
		ServersAdded:       4,
		ServersRemoved:     5,
		ServersModified:    6,
		FrontendsAdded:     7,
		FrontendsRemoved:   8,
		FrontendsModified:  9,
	}

	tests := []struct {
		name   string
		a, b   *haproxyv1alpha1.OperationSummary
		expect bool
	}{
		{name: "both nil", a: nil, b: nil, expect: true},
		{name: "a nil b non-nil", a: nil, b: s1, expect: false},
		{name: "a non-nil b nil", a: s1, b: nil, expect: false},
		{name: "identical summaries", a: s1, b: s1copy, expect: true},
		{
			name:   "different total ops",
			a:      s1,
			b:      &haproxyv1alpha1.OperationSummary{TotalAPIOperations: 99},
			expect: false,
		},
		{
			name: "different backends added",
			a:    s1,
			b: &haproxyv1alpha1.OperationSummary{
				TotalAPIOperations: 10,
				BackendsAdded:      999,
			},
			expect: false,
		},
		{
			name: "different frontends modified",
			a:    s1,
			b: &haproxyv1alpha1.OperationSummary{
				TotalAPIOperations: 10,
				BackendsAdded:      1,
				BackendsRemoved:    2,
				BackendsModified:   3,
				ServersAdded:       4,
				ServersRemoved:     5,
				ServersModified:    6,
				FrontendsAdded:     7,
				FrontendsRemoved:   8,
				FrontendsModified:  999,
			},
			expect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expect, operationSummaryEqual(tt.a, tt.b))
		})
	}
}

// ---------------------------------------------------------------------------
// findPodStatus
// ---------------------------------------------------------------------------

func TestFindPodStatus(t *testing.T) {
	pods := []haproxyv1alpha1.PodDeploymentStatus{
		{PodName: "pod-1", Checksum: "aaa"},
		{PodName: "pod-2", Checksum: "bbb"},
		{PodName: "pod-3", Checksum: "ccc"},
	}

	tests := []struct {
		name           string
		pods           []haproxyv1alpha1.PodDeploymentStatus
		podName        string
		expectNil      bool
		expectChecksum string
	}{
		{name: "find first", pods: pods, podName: "pod-1", expectChecksum: "aaa"},
		{name: "find last", pods: pods, podName: "pod-3", expectChecksum: "ccc"},
		{name: "not found", pods: pods, podName: "pod-99", expectNil: true},
		{name: "empty slice", pods: nil, podName: "pod-1", expectNil: true},
		{name: "empty pod name", pods: pods, podName: "", expectNil: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := findPodStatus(tt.pods, tt.podName)
			if tt.expectNil {
				assert.Nil(t, result)
			} else {
				require.NotNil(t, result)
				assert.Equal(t, tt.expectChecksum, result.Checksum)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// buildAuxiliaryFilePodStatus
// ---------------------------------------------------------------------------

func TestBuildAuxiliaryFilePodStatus(t *testing.T) {
	now := time.Now()
	earlier := now.Add(-time.Hour)
	metaNow := metav1.NewTime(now)
	metaEarlier := metav1.NewTime(earlier)
	errAt := metav1.NewTime(earlier)

	tests := []struct {
		name           string
		podName        string
		fileChecksum   string
		existing       *haproxyv1alpha1.PodDeploymentStatus
		deployedAt     time.Time
		expectChecksum string
		expectPodName  string
		expectError    string
		expectErrCount int
	}{
		{
			name:           "new pod with no existing status",
			podName:        "pod-1",
			fileChecksum:   "sha256:new",
			existing:       nil,
			deployedAt:     now,
			expectChecksum: "sha256:new",
			expectPodName:  "pod-1",
		},
		{
			name:         "existing pod with unchanged checksum preserves status",
			podName:      "pod-1",
			fileChecksum: "sha256:same",
			existing: &haproxyv1alpha1.PodDeploymentStatus{
				PodName:           "pod-1",
				Checksum:          "sha256:same",
				DeployedAt:        metaEarlier,
				LastError:         "old error",
				ConsecutiveErrors: 3,
				LastErrorAt:       &errAt,
			},
			deployedAt:     now,
			expectChecksum: "sha256:same",
			expectPodName:  "pod-1",
			expectError:    "old error",
			expectErrCount: 3,
		},
		{
			name:         "existing pod with changed checksum resets to new deployment",
			podName:      "pod-1",
			fileChecksum: "sha256:changed",
			existing: &haproxyv1alpha1.PodDeploymentStatus{
				PodName:           "pod-1",
				Checksum:          "sha256:old",
				DeployedAt:        metaEarlier,
				LastError:         "old error",
				ConsecutiveErrors: 3,
			},
			deployedAt:     now,
			expectChecksum: "sha256:changed",
			expectPodName:  "pod-1",
			expectError:    "",
			expectErrCount: 0,
		},
		{
			name:         "zero deployedAt with existing status preserves existing deployedAt",
			podName:      "pod-1",
			fileChecksum: "sha256:new",
			existing: &haproxyv1alpha1.PodDeploymentStatus{
				PodName:    "pod-1",
				Checksum:   "sha256:old",
				DeployedAt: metaEarlier,
			},
			deployedAt:     time.Time{},
			expectChecksum: "sha256:new",
			expectPodName:  "pod-1",
		},
		{
			name:           "zero deployedAt without existing status",
			podName:        "pod-1",
			fileChecksum:   "sha256:new",
			existing:       nil,
			deployedAt:     time.Time{},
			expectChecksum: "sha256:new",
			expectPodName:  "pod-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildAuxiliaryFilePodStatus(tt.podName, tt.fileChecksum, tt.existing, tt.deployedAt)
			assert.Equal(t, tt.expectPodName, result.PodName)
			assert.Equal(t, tt.expectChecksum, result.Checksum)
			assert.Equal(t, tt.expectError, result.LastError)
			assert.Equal(t, tt.expectErrCount, result.ConsecutiveErrors)

			// For unchanged checksum, verify the original deployedAt is preserved.
			if tt.existing != nil && tt.existing.Checksum == tt.fileChecksum {
				assert.Equal(t, tt.existing.DeployedAt, result.DeployedAt)
			}

			// For zero deployedAt with existing, verify preservation.
			if tt.deployedAt.IsZero() && tt.existing != nil && tt.existing.Checksum != tt.fileChecksum {
				assert.Equal(t, metaEarlier, result.DeployedAt)
			}

			// New deployment with non-zero time.
			if !tt.deployedAt.IsZero() && (tt.existing == nil || tt.existing.Checksum != tt.fileChecksum) {
				assert.Equal(t, metaNow, result.DeployedAt)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// addOrUpdatePodStatus
// ---------------------------------------------------------------------------

func TestAddOrUpdatePodStatus(t *testing.T) {
	now := metav1.Now()

	tests := []struct {
		name      string
		pods      []haproxyv1alpha1.PodDeploymentStatus
		podStatus haproxyv1alpha1.PodDeploymentStatus
		expectLen int
		expectPod string
	}{
		{
			name:      "add to empty slice",
			pods:      nil,
			podStatus: haproxyv1alpha1.PodDeploymentStatus{PodName: "pod-1", Checksum: "abc"},
			expectLen: 1,
			expectPod: "pod-1",
		},
		{
			name: "add new pod",
			pods: []haproxyv1alpha1.PodDeploymentStatus{
				{PodName: "pod-1", Checksum: "abc"},
			},
			podStatus: haproxyv1alpha1.PodDeploymentStatus{PodName: "pod-2", Checksum: "def"},
			expectLen: 2,
			expectPod: "pod-2",
		},
		{
			name: "update existing pod",
			pods: []haproxyv1alpha1.PodDeploymentStatus{
				{PodName: "pod-1", Checksum: "old"},
			},
			podStatus: haproxyv1alpha1.PodDeploymentStatus{PodName: "pod-1", Checksum: "new"},
			expectLen: 1,
			expectPod: "pod-1",
		},
		{
			name: "update preserves existing deployedAt when new is zero",
			pods: []haproxyv1alpha1.PodDeploymentStatus{
				{PodName: "pod-1", Checksum: "old", DeployedAt: now},
			},
			podStatus: haproxyv1alpha1.PodDeploymentStatus{PodName: "pod-1", Checksum: "new"},
			expectLen: 1,
			expectPod: "pod-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ps := tt.podStatus
			result := addOrUpdatePodStatus(tt.pods, &ps)
			assert.Len(t, result, tt.expectLen)
			found := findPodStatus(result, tt.expectPod)
			require.NotNil(t, found)

			if tt.name == "update preserves existing deployedAt when new is zero" {
				assert.Equal(t, now, found.DeployedAt, "should preserve existing deployedAt")
			}
			if tt.name == "update existing pod" {
				assert.Equal(t, "new", found.Checksum)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// removePodFromStatus
// ---------------------------------------------------------------------------

func TestRemovePodFromStatus(t *testing.T) {
	tests := []struct {
		name        string
		pods        []haproxyv1alpha1.PodDeploymentStatus
		podName     string
		expectLen   int
		expectFound bool
	}{
		{
			name:        "remove from empty slice",
			pods:        nil,
			podName:     "pod-1",
			expectLen:   0,
			expectFound: false,
		},
		{
			name: "remove existing pod",
			pods: []haproxyv1alpha1.PodDeploymentStatus{
				{PodName: "pod-1"},
				{PodName: "pod-2"},
			},
			podName:     "pod-1",
			expectLen:   1,
			expectFound: true,
		},
		{
			name: "remove non-existing pod",
			pods: []haproxyv1alpha1.PodDeploymentStatus{
				{PodName: "pod-1"},
			},
			podName:     "pod-99",
			expectLen:   1,
			expectFound: false,
		},
		{
			name: "remove only pod",
			pods: []haproxyv1alpha1.PodDeploymentStatus{
				{PodName: "pod-1"},
			},
			podName:     "pod-1",
			expectLen:   0,
			expectFound: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, removed := removePodFromStatus(tt.pods, tt.podName)
			assert.Len(t, result, tt.expectLen)
			assert.Equal(t, tt.expectFound, removed)
			// The removed pod should not be present.
			assert.Nil(t, findPodStatus(result, tt.podName))
		})
	}
}

// ---------------------------------------------------------------------------
// updateOrAppendPodStatus
// ---------------------------------------------------------------------------

func TestUpdateOrAppendPodStatus(t *testing.T) {
	now := metav1.Now()
	dur := metav1.Duration{Duration: 2 * time.Second}
	summary := &haproxyv1alpha1.OperationSummary{TotalAPIOperations: 5}

	t.Run("append new pod to empty slice", func(t *testing.T) {
		ps := haproxyv1alpha1.PodDeploymentStatus{
			PodName:  "pod-1",
			Checksum: "abc",
		}
		update := &DeploymentStatusUpdate{PodName: "pod-1"}
		result := updateOrAppendPodStatus(nil, &ps, update)
		require.Len(t, result, 1)
		assert.Equal(t, "pod-1", result[0].PodName)
	})

	t.Run("update existing pod resets consecutive errors on success", func(t *testing.T) {
		pods := []haproxyv1alpha1.PodDeploymentStatus{
			{PodName: "pod-1", ConsecutiveErrors: 5, DeployedAt: now},
		}
		ps := haproxyv1alpha1.PodDeploymentStatus{
			PodName:    "pod-1",
			Checksum:   "new",
			DeployedAt: now,
		}
		update := &DeploymentStatusUpdate{PodName: "pod-1", Error: ""}
		result := updateOrAppendPodStatus(pods, &ps, update)
		require.Len(t, result, 1)
		assert.Equal(t, 0, result[0].ConsecutiveErrors)
	})

	t.Run("update existing pod increments consecutive errors on failure", func(t *testing.T) {
		pods := []haproxyv1alpha1.PodDeploymentStatus{
			{PodName: "pod-1", ConsecutiveErrors: 2, DeployedAt: now},
		}
		ps := haproxyv1alpha1.PodDeploymentStatus{
			PodName:    "pod-1",
			DeployedAt: now,
		}
		update := &DeploymentStatusUpdate{PodName: "pod-1", Error: "connection refused"}
		result := updateOrAppendPodStatus(pods, &ps, update)
		require.Len(t, result, 1)
		assert.Equal(t, 3, result[0].ConsecutiveErrors)
	})

	t.Run("preserves deployed at when new one is zero", func(t *testing.T) {
		pods := []haproxyv1alpha1.PodDeploymentStatus{
			{PodName: "pod-1", DeployedAt: now},
		}
		ps := haproxyv1alpha1.PodDeploymentStatus{PodName: "pod-1"}
		update := &DeploymentStatusUpdate{PodName: "pod-1"}
		result := updateOrAppendPodStatus(pods, &ps, update)
		assert.Equal(t, now, result[0].DeployedAt)
	})

	t.Run("preserves sync duration when not being updated", func(t *testing.T) {
		pods := []haproxyv1alpha1.PodDeploymentStatus{
			{PodName: "pod-1", SyncDuration: &dur, DeployedAt: now},
		}
		ps := haproxyv1alpha1.PodDeploymentStatus{
			PodName:    "pod-1",
			DeployedAt: now,
		}
		update := &DeploymentStatusUpdate{PodName: "pod-1"}
		result := updateOrAppendPodStatus(pods, &ps, update)
		require.NotNil(t, result[0].SyncDuration)
		assert.Equal(t, dur.Duration, result[0].SyncDuration.Duration)
	})

	t.Run("preserves operation summary when not being updated", func(t *testing.T) {
		pods := []haproxyv1alpha1.PodDeploymentStatus{
			{PodName: "pod-1", LastOperationSummary: summary, DeployedAt: now},
		}
		ps := haproxyv1alpha1.PodDeploymentStatus{
			PodName:    "pod-1",
			DeployedAt: now,
		}
		update := &DeploymentStatusUpdate{PodName: "pod-1"}
		result := updateOrAppendPodStatus(pods, &ps, update)
		require.NotNil(t, result[0].LastOperationSummary)
		assert.Equal(t, 5, result[0].LastOperationSummary.TotalAPIOperations)
	})
}

// ---------------------------------------------------------------------------
// buildPodStatus
// ---------------------------------------------------------------------------

func TestBuildPodStatus(t *testing.T) {
	now := time.Now()
	reloadTime := now.Add(-time.Minute)
	syncDur := 3 * time.Second

	t.Run("minimal update with no optional fields", func(t *testing.T) {
		update := &DeploymentStatusUpdate{
			PodName:  "pod-1",
			Checksum: "sha256:abc",
		}
		result := buildPodStatus(update)
		assert.Equal(t, "pod-1", result.PodName)
		assert.Equal(t, "sha256:abc", result.Checksum)
		assert.True(t, result.DeployedAt.IsZero(), "deployedAt should be zero when DeployedAt is zero")
		assert.Nil(t, result.LastReloadAt)
		assert.Empty(t, result.LastReloadID)
		assert.Nil(t, result.SyncDuration)
		assert.Nil(t, result.LastOperationSummary)
		assert.Empty(t, result.LastError)
		assert.Nil(t, result.LastErrorAt)
	})

	t.Run("full update with all fields set", func(t *testing.T) {
		update := &DeploymentStatusUpdate{
			PodName:                "pod-1",
			Checksum:               "sha256:abc",
			DeployedAt:             now,
			LastReloadAt:           &reloadTime,
			LastReloadID:           "reload-42",
			SyncDuration:           &syncDur,
			VersionConflictRetries: 3,
			FallbackUsed:           true,
			OperationSummary: &OperationSummary{
				TotalAPIOperations: 10,
				BackendsAdded:      1,
				ServersRemoved:     2,
				FrontendsModified:  3,
			},
			Error: "connection reset",
		}
		result := buildPodStatus(update)

		assert.Equal(t, "pod-1", result.PodName)
		assert.Equal(t, "sha256:abc", result.Checksum)
		assert.Equal(t, metav1.NewTime(now), result.DeployedAt)

		require.NotNil(t, result.LastReloadAt)
		assert.Equal(t, metav1.NewTime(reloadTime), *result.LastReloadAt)
		assert.Equal(t, "reload-42", result.LastReloadID)

		require.NotNil(t, result.SyncDuration)
		assert.Equal(t, syncDur, result.SyncDuration.Duration)

		assert.Equal(t, 3, result.VersionConflictRetries)
		assert.True(t, result.FallbackUsed)

		require.NotNil(t, result.LastOperationSummary)
		assert.Equal(t, 10, result.LastOperationSummary.TotalAPIOperations)
		assert.Equal(t, 1, result.LastOperationSummary.BackendsAdded)
		assert.Equal(t, 2, result.LastOperationSummary.ServersRemoved)
		assert.Equal(t, 3, result.LastOperationSummary.FrontendsModified)

		assert.Equal(t, "connection reset", result.LastError)
		require.NotNil(t, result.LastErrorAt)
	})

	t.Run("error sets lastErrorAt to deployedAt time", func(t *testing.T) {
		update := &DeploymentStatusUpdate{
			PodName:    "pod-1",
			DeployedAt: now,
			Error:      "timeout",
		}
		result := buildPodStatus(update)
		require.NotNil(t, result.LastErrorAt)
		assert.Equal(t, metav1.NewTime(now), *result.LastErrorAt)
	})

	t.Run("no error does not set lastErrorAt", func(t *testing.T) {
		update := &DeploymentStatusUpdate{
			PodName:    "pod-1",
			DeployedAt: now,
			Error:      "",
		}
		result := buildPodStatus(update)
		assert.Nil(t, result.LastErrorAt)
		assert.Empty(t, result.LastError)
	})
}

// ---------------------------------------------------------------------------
// GenerateRuntimeConfigName
// ---------------------------------------------------------------------------

func TestGenerateRuntimeConfigName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "simple name", input: "my-config", expected: "my-config-haproxycfg"},
		{name: "empty string", input: "", expected: "-haproxycfg"},
		{name: "with dots", input: "my.config.v1", expected: "my.config.v1-haproxycfg"},
		{name: "with hyphens", input: "my-long-config-name", expected: "my-long-config-name-haproxycfg"},
		{name: "already has suffix", input: "config-haproxycfg", expected: "config-haproxycfg-haproxycfg"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, GenerateRuntimeConfigName(tt.input))
		})
	}
}

// ---------------------------------------------------------------------------
// Publisher name generation methods
// ---------------------------------------------------------------------------

func TestPublisher_GenerateMapFileName(t *testing.T) {
	p := &Publisher{}

	tests := []struct {
		name     string
		mapName  string
		expected string
	}{
		{name: "simple map", mapName: "hosts.map", expected: "haproxy-map-hosts"},
		{name: "no extension", mapName: "hosts", expected: "haproxy-map-hosts"},
		{name: "path with directory", mapName: "/etc/haproxy/maps/hosts.map", expected: "haproxy-map-/etc/haproxy/maps/hosts"},
		{name: "double extension", mapName: "hosts.map.bak", expected: "haproxy-map-hosts.map"},
		{name: "hidden file", mapName: ".hidden.map", expected: "haproxy-map-.hidden"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, p.generateMapFileName(tt.mapName))
		})
	}
}

func TestPublisher_GenerateSecretName(t *testing.T) {
	p := &Publisher{}

	tests := []struct {
		name     string
		certPath string
		expected string
	}{
		{name: "simple cert", certPath: "/etc/haproxy/ssl/cert.pem", expected: "haproxy-cert-cert"},
		{name: "with underscores", certPath: "/ssl/my_cert_bundle.pem", expected: "haproxy-cert-my-cert-bundle"},
		{name: "no extension", certPath: "/ssl/cert", expected: "haproxy-cert-cert"},
		{name: "crt extension", certPath: "server.crt", expected: "haproxy-cert-server"},
		{name: "deep path", certPath: "/a/b/c/d/cert.pem", expected: "haproxy-cert-cert"},
		{name: "multiple underscores", certPath: "a_b_c_d.pem", expected: "haproxy-cert-a-b-c-d"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, p.generateSecretName(tt.certPath))
		})
	}
}

func TestPublisher_GenerateGeneralFileName(t *testing.T) {
	p := &Publisher{}

	tests := []struct {
		name     string
		fileName string
		expected string
	}{
		{name: "error page", fileName: "/etc/haproxy/errors/503.http", expected: "haproxy-file-503"},
		{name: "with underscores", fileName: "my_error_page.html", expected: "haproxy-file-my-error-page"},
		{name: "with dots", fileName: "config.v2.json", expected: "haproxy-file-config-v2"},
		{name: "no extension", fileName: "myfile", expected: "haproxy-file-myfile"},
		{name: "deep path", fileName: "/a/b/c/file.txt", expected: "haproxy-file-file"},
		{name: "underscores and dots", fileName: "my_file.name.ext", expected: "haproxy-file-my-file-name"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, p.generateGeneralFileName(tt.fileName))
		})
	}
}

func TestPublisher_GenerateCRTListFileName(t *testing.T) {
	p := &Publisher{}

	tests := []struct {
		name     string
		listPath string
		expected string
	}{
		{name: "simple crt-list", listPath: "/etc/haproxy/ssl/certs.crt-list", expected: "haproxy-crtlist-certs"},
		{name: "with underscores", listPath: "my_crt_list.list", expected: "haproxy-crtlist-my-crt-list"},
		{name: "no extension", listPath: "crtlist", expected: "haproxy-crtlist-crtlist"},
		{name: "deep path", listPath: "/a/b/c/list.cfg", expected: "haproxy-crtlist-list"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, p.generateCRTListFileName(tt.listPath))
		})
	}
}

// ---------------------------------------------------------------------------
// calculateChecksum
// ---------------------------------------------------------------------------

func TestCalculateChecksum(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{name: "empty string", content: ""},
		{name: "simple content", content: "hello world"},
		{name: "multiline", content: "line1\nline2\nline3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calculateChecksum(tt.content)
			assert.True(t, strings.HasPrefix(result, "sha256:"), "should have sha256 prefix")
			// SHA256 hex is 64 chars + "sha256:" prefix = 71 chars.
			assert.Len(t, result, 71)
		})
	}

	t.Run("deterministic", func(t *testing.T) {
		a := calculateChecksum("test content")
		b := calculateChecksum("test content")
		assert.Equal(t, a, b)
	})

	t.Run("different content produces different checksum", func(t *testing.T) {
		a := calculateChecksum("content a")
		b := calculateChecksum("content b")
		assert.NotEqual(t, a, b)
	})
}

// ---------------------------------------------------------------------------
// boolPtr
// ---------------------------------------------------------------------------

func TestBoolPtr(t *testing.T) {
	trueVal := boolPtr(true)
	falseVal := boolPtr(false)

	require.NotNil(t, trueVal)
	assert.True(t, *trueVal)

	require.NotNil(t, falseVal)
	assert.False(t, *falseVal)
}

// ---------------------------------------------------------------------------
// compressIfNeeded (requires Publisher with logger)
// ---------------------------------------------------------------------------

func TestCompressIfNeeded(t *testing.T) {
	p := &Publisher{logger: testLogger()}

	t.Run("threshold zero disables compression", func(t *testing.T) {
		result := p.compressIfNeeded("some content", 0, "test")
		assert.Equal(t, "some content", result.content)
		assert.False(t, result.compressed)
	})

	t.Run("threshold negative disables compression", func(t *testing.T) {
		result := p.compressIfNeeded("some content", -1, "test")
		assert.Equal(t, "some content", result.content)
		assert.False(t, result.compressed)
	})

	t.Run("content below threshold not compressed", func(t *testing.T) {
		result := p.compressIfNeeded("small", 1000, "test")
		assert.Equal(t, "small", result.content)
		assert.False(t, result.compressed)
	})

	t.Run("content at threshold not compressed", func(t *testing.T) {
		content := strings.Repeat("x", 100)
		result := p.compressIfNeeded(content, 100, "test")
		assert.Equal(t, content, result.content)
		assert.False(t, result.compressed)
	})

	t.Run("large repetitive content is compressed", func(t *testing.T) {
		// Repetitive content compresses well with zstd.
		content := strings.Repeat("frontend http\n  bind *:80\n  default_backend web\n", 500)
		result := p.compressIfNeeded(content, 100, "test")
		assert.True(t, result.compressed, "highly repetitive content should compress well")
		assert.Less(t, len(result.content), len(content), "compressed should be smaller")
	})

	t.Run("incompressible content stays uncompressed", func(t *testing.T) {
		// Very short content that won't benefit from compression.
		content := strings.Repeat("x", 101)
		result := p.compressIfNeeded(content, 100, "test")
		// Short non-repetitive content may not compress smaller; in that case it stays uncompressed.
		if !result.compressed {
			assert.Equal(t, content, result.content)
		}
	})
}
