// Copyright 2026 Philipp Hossner
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

package leaderelection

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func lease(annotations map[string]string) *coordinationv1.Lease {
	return &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "haptic-controller",
			Namespace:   "haptic",
			Annotations: annotations,
		},
	}
}

func epochOf(t *testing.T, clientset *fake.Clientset) string {
	t.Helper()
	stored, err := clientset.CoordinationV1().Leases("haptic").Get(context.Background(), "haptic-controller", metav1.GetOptions{})
	require.NoError(t, err)
	return stored.Annotations[EpochAnnotation]
}

func TestLeaseEpoch_BumpClaimsTheNextEpoch(t *testing.T) {
	clientset := fake.NewSimpleClientset(lease(nil))
	epoch := NewLeaseEpoch(clientset, "haptic", "haptic-controller", "pod-a", nil)

	require.Equal(t, uint64(0), epoch.LeaderEpoch())
	require.NoError(t, epoch.Bump(context.Background()))

	assert.Equal(t, uint64(1), epoch.LeaderEpoch())
	assert.Equal(t, "1", epochOf(t, clientset))
	assert.Equal(t, "pod-a", epoch.Identity())
}

func TestLeaseEpoch_BumpContinuesFromTheStoredValue(t *testing.T) {
	clientset := fake.NewSimpleClientset(lease(map[string]string{EpochAnnotation: "7"}))
	epoch := NewLeaseEpoch(clientset, "haptic", "haptic-controller", "pod-b", nil)

	require.NoError(t, epoch.Bump(context.Background()))

	assert.Equal(t, uint64(8), epoch.LeaderEpoch(), "a new term must outrank every term before it")
	assert.Equal(t, "8", epochOf(t, clientset))
}

// A hand-edited annotation must not lower the next epoch below one already
// stamped on a pod: unreadable reads as none, and the counter restarts at 1.
func TestLeaseEpoch_BumpTreatsAnUnreadableValueAsNone(t *testing.T) {
	clientset := fake.NewSimpleClientset(lease(map[string]string{EpochAnnotation: "not-a-number"}))
	epoch := NewLeaseEpoch(clientset, "haptic", "haptic-controller", "pod-c", nil)

	require.NoError(t, epoch.Bump(context.Background()))

	assert.Equal(t, uint64(1), epoch.LeaderEpoch())
}

func TestLeaseEpoch_BumpReportsAMissingLease(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	epoch := NewLeaseEpoch(clientset, "haptic", "haptic-controller", "pod-d", nil)

	require.Error(t, epoch.Bump(context.Background()))
	assert.Equal(t, uint64(0), epoch.LeaderEpoch(), "an unclaimed epoch must stay zero, which every pod outranks")
}

func TestLeaseEpoch_NilIsUsable(t *testing.T) {
	var epoch *LeaseEpoch

	require.NoError(t, epoch.Bump(context.Background()))
	assert.Equal(t, uint64(0), epoch.LeaderEpoch())
	assert.Empty(t, epoch.Identity())
}
