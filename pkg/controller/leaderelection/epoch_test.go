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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/utils/ptr"
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

// A hand-edited annotation stands for a counter nobody can read, and the next
// epoch must not be lower than one already stamped on a pod. Restarting at 1 is
// exactly that, so the claim fails and the term never dispatches.
func TestLeaseEpoch_BumpRefusesAnUnreadableValue(t *testing.T) {
	clientset := fake.NewSimpleClientset(lease(map[string]string{EpochAnnotation: "not-a-number"}))
	epoch := NewLeaseEpoch(clientset, "haptic", "haptic-controller", "pod-c", nil)

	err := epoch.Bump(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not an epoch")
	assert.Equal(t, uint64(0), epoch.LeaderEpoch(), "an unclaimed epoch must stay zero")
	assert.Equal(t, "not-a-number", epochOf(t, clientset), "a value it cannot read is not one it may overwrite")
}

// A Lease deleted and recreated (the routine way to force a re-election) comes
// back without the annotation, so the new term claims an epoch the running pods
// already outrank. The pod's own epoch is the floor the counter is lifted past.
func TestLeaseEpoch_ReclaimLiftsItPastTheFleet(t *testing.T) {
	held := lease(nil)
	held.Spec.HolderIdentity = ptr.To("pod-a")
	clientset := fake.NewSimpleClientset(held)
	epoch := NewLeaseEpoch(clientset, "haptic", "haptic-controller", "pod-a", nil)
	require.NoError(t, epoch.Bump(context.Background()))
	require.Equal(t, uint64(1), epoch.LeaderEpoch())

	claimed, err := epoch.Reclaim(context.Background(), 12)

	require.NoError(t, err)
	assert.Equal(t, uint64(13), claimed)
	assert.Equal(t, uint64(13), epoch.LeaderEpoch())
	assert.Equal(t, "13", epochOf(t, clientset))
}

// A pod that already answered the reclaim raises the counter for every other
// pod of the same deployment: they must not each write the Lease again.
func TestLeaseEpoch_ReclaimIsANoopOnceTheEpochOutranksTheFloor(t *testing.T) {
	held := lease(map[string]string{EpochAnnotation: "13"})
	held.Spec.HolderIdentity = ptr.To("pod-a")
	clientset := fake.NewSimpleClientset(held)
	epoch := NewLeaseEpoch(clientset, "haptic", "haptic-controller", "pod-a", nil)
	require.NoError(t, epoch.Bump(context.Background()))

	updates := 0
	clientset.PrependReactor("update", "leases", func(k8stesting.Action) (bool, runtime.Object, error) {
		updates++
		return false, nil, nil
	})

	claimed, err := epoch.Reclaim(context.Background(), 12)

	require.NoError(t, err)
	assert.Equal(t, uint64(14), claimed, "the epoch this term already claimed")
	assert.Zero(t, updates)
}

// A Lease another replica holds is the one case where the pod is right: a newer
// leader really owns the fleet, and this controller must stop writing.
func TestLeaseEpoch_ReclaimRefusesAForeignHolder(t *testing.T) {
	held := lease(map[string]string{EpochAnnotation: "1"})
	held.Spec.HolderIdentity = ptr.To("pod-z")
	clientset := fake.NewSimpleClientset(held)
	epoch := NewLeaseEpoch(clientset, "haptic", "haptic-controller", "pod-a", nil)

	_, err := epoch.Reclaim(context.Background(), 12)

	require.ErrorIs(t, err, ErrForeignLeader)
	assert.Equal(t, "1", epochOf(t, clientset))
}

// The Lease still names this holder, but at an epoch it never claimed: a rival
// bumped it, so the counter did not regress and there is nothing to reclaim.
func TestLeaseEpoch_ReclaimRefusesAnEpochItNeverClaimed(t *testing.T) {
	held := lease(map[string]string{EpochAnnotation: "1"})
	held.Spec.HolderIdentity = ptr.To("pod-a")
	clientset := fake.NewSimpleClientset(held)
	epoch := NewLeaseEpoch(clientset, "haptic", "haptic-controller", "pod-a", nil)
	require.NoError(t, epoch.Bump(context.Background()))

	stored, err := clientset.CoordinationV1().Leases("haptic").Get(context.Background(), "haptic-controller", metav1.GetOptions{})
	require.NoError(t, err)
	stored.Annotations[EpochAnnotation] = "40"
	_, err = clientset.CoordinationV1().Leases("haptic").Update(context.Background(), stored, metav1.UpdateOptions{})
	require.NoError(t, err)

	_, err = epoch.Reclaim(context.Background(), 42)

	require.ErrorIs(t, err, ErrForeignLeader)
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

	_, err := epoch.Reclaim(context.Background(), 5)
	require.ErrorIs(t, err, ErrForeignLeader, "nothing to reclaim without a lease to reclaim it on")
}
