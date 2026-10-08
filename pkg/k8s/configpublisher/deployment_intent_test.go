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
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/utils/ptr"
)

func intentAuthority() DeploymentAuthority {
	return DeploymentAuthority{Namespace: "default", Name: "fleet", UID: "template-uid", Epoch: 1, Claim: "old-claim", LeaseName: "leader", Identity: "old-leader"}
}

func setIntentLeader(t *testing.T, ctx context.Context, client *fake.Clientset, authority *DeploymentAuthority) {
	t.Helper()
	leases := client.CoordinationV1().Leases(authority.Namespace)
	lease, err := leases.Get(ctx, authority.LeaseName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		lease, err = leases.Create(ctx, &coordinationv1.Lease{ObjectMeta: metav1.ObjectMeta{Name: authority.LeaseName}}, metav1.CreateOptions{})
	}
	require.NoError(t, err)
	if lease.Annotations == nil {
		lease.Annotations = map[string]string{}
	}
	lease.Annotations["haptic.io/leader-epoch"] = strconv.FormatUint(authority.Epoch, 10)
	lease.Spec.HolderIdentity = ptr.To(authority.Identity)
	_, err = leases.Update(ctx, lease, metav1.UpdateOptions{})
	require.NoError(t, err)
}

func TestDeploymentIntentPrecedesAcknowledgementAcrossFailover(t *testing.T) {
	ctx, client, _, publisher := newTestPublisher(t)
	authority := intentAuthority()
	setIntentLeader(t, ctx, client, &authority)
	require.NoError(t, publisher.ClaimDeploymentAuthority(ctx, &authority))
	require.NoError(t, publisher.RecordDeploymentIntent(ctx, &authority, "plan-a", "checksum-a"))
	require.NoError(t, publisher.RecordDeploymentIntent(ctx, &authority, "plan-b", "checksum-b"))
	fresh := authority
	fresh.Epoch, fresh.Claim, fresh.Identity = 2, "new-claim", "new-leader"
	setIntentLeader(t, ctx, client, &fresh)
	require.NoError(t, publisher.ClaimDeploymentAuthority(ctx, &fresh))
	require.ErrorContains(t, publisher.CheckDeploymentIntent(ctx, &fresh, "plan-a", "checksum-a"), "newer deployment intent")
	require.NoError(t, publisher.CheckDeploymentIntent(ctx, &fresh, "plan-b", "checksum-b"))
	require.ErrorContains(t, publisher.RecordDeploymentIntent(ctx, &authority, "plan-a", "checksum-a"), "newer controller term")
	require.ErrorContains(t, publisher.ClaimDeploymentAuthority(ctx, &authority), "newer controller term")
	require.NoError(t, publisher.CheckDeploymentIntent(ctx, &fresh, "plan-b", "checksum-b"))
}

func TestTakeoverFencesADelayedDeploymentIntent(t *testing.T) {
	ctx, client, _, publisher := newTestPublisher(t)
	authority := intentAuthority()
	setIntentLeader(t, ctx, client, &authority)
	require.NoError(t, publisher.ClaimDeploymentAuthority(ctx, &authority))
	require.NoError(t, publisher.RecordDeploymentIntent(ctx, &authority, "plan-a", "checksum-a"))
	fresh := authority
	fresh.Epoch, fresh.Claim, fresh.Identity = 2, "new-claim", "new-leader"
	setIntentLeader(t, ctx, client, &fresh)
	require.NoError(t, publisher.ClaimDeploymentAuthority(ctx, &fresh))
	require.Error(t, publisher.RecordDeploymentIntent(ctx, &authority, "plan-b", "checksum-b"))
	require.NoError(t, publisher.CheckDeploymentIntent(ctx, &fresh, "plan-a", "checksum-a"))
}

func TestDeploymentIntentRechecksAuthorityAfterConflict(t *testing.T) {
	ctx, client, _, publisher := newTestPublisher(t)
	authority := intentAuthority()
	setIntentLeader(t, ctx, client, &authority)
	require.NoError(t, publisher.ClaimDeploymentAuthority(ctx, &authority))
	require.NoError(t, publisher.RecordDeploymentIntent(ctx, &authority, "plan-a", "checksum-a"))
	raced := false
	client.PrependReactor("update", "leases", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if raced {
			return false, nil, nil
		}
		raced = true
		lease := action.(k8stesting.UpdateAction).GetObject().(*coordinationv1.Lease).DeepCopy()
		previous, err := client.Tracker().Get(coordinationv1.SchemeGroupVersion.WithResource("leases"), authority.Namespace, authority.LeaseName)
		require.NoError(t, err)
		lease.Annotations = previous.(*coordinationv1.Lease).Annotations
		lease.Spec.HolderIdentity = ptr.To("new-leader")
		lease.Annotations["haptic.io/leader-epoch"] = "2"
		require.NoError(t, client.Tracker().Update(coordinationv1.SchemeGroupVersion.WithResource("leases"), lease, authority.Namespace))
		return true, nil, apierrors.NewConflict(schema.GroupResource{Group: "coordination.k8s.io", Resource: "leases"}, authority.LeaseName, errors.New("leadership changed"))
	})
	require.ErrorContains(t, publisher.RecordDeploymentIntent(ctx, &authority, "plan-b", "checksum-b"), "newer controller term")
	fresh := authority
	fresh.Epoch, fresh.Claim, fresh.Identity = 2, "new-claim", "new-leader"
	require.NoError(t, publisher.ClaimDeploymentAuthority(ctx, &fresh))
	require.NoError(t, publisher.CheckDeploymentIntent(ctx, &fresh, "plan-a", "checksum-a"))
}

func TestStandaloneDeploymentIntentSurvivesRestart(t *testing.T) {
	ctx, _, _, publisher := newTestPublisher(t)
	authority := intentAuthority()
	authority.Standalone = true
	require.NoError(t, publisher.ClaimDeploymentAuthority(ctx, &authority))
	require.NoError(t, publisher.RecordDeploymentIntent(ctx, &authority, "plan-b", "checksum-b"))
	fresh := authority
	fresh.Claim = "restarted"
	require.NoError(t, publisher.ClaimDeploymentAuthority(ctx, &fresh))
	require.NoError(t, publisher.CheckDeploymentIntent(ctx, &fresh, "plan-b", "checksum-b"))
	require.Error(t, publisher.CheckDeploymentIntent(ctx, &fresh, "plan-a", "checksum-a"))
	require.Error(t, publisher.RecordDeploymentIntent(ctx, &authority, "plan-a", "checksum-a"))
}

func TestDeploymentIntentRefusesUnknownOrForeignState(t *testing.T) {
	for _, tc := range []struct{ name, annotation string }{
		{"absent", ""}, {"corrupt", "{"}, {"foreign", `{"uid":"foreign","claim":"old-claim","planID":"plan-a","checksum":"checksum-a"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, client, _, publisher := newTestPublisher(t)
			authority := intentAuthority()
			setIntentLeader(t, ctx, client, &authority)
			lease, err := client.CoordinationV1().Leases(authority.Namespace).Get(ctx, authority.LeaseName, metav1.GetOptions{})
			require.NoError(t, err)
			lease.Annotations[deploymentIntentAnnotation] = tc.annotation
			_, err = client.CoordinationV1().Leases(authority.Namespace).Update(ctx, lease, metav1.UpdateOptions{})
			require.NoError(t, err)
			require.Error(t, publisher.CheckDeploymentIntent(ctx, &authority, "plan-a", "checksum-a"))
		})
	}
}

func TestNewTemplateUIDCannotInheritPreviousDeployment(t *testing.T) {
	ctx, client, _, publisher := newTestPublisher(t)
	authority := intentAuthority()
	setIntentLeader(t, ctx, client, &authority)
	require.NoError(t, publisher.ClaimDeploymentAuthority(ctx, &authority))
	require.NoError(t, publisher.RecordDeploymentIntent(ctx, &authority, "previous-plan", "previous-checksum"))
	fresh := authority
	fresh.Epoch, fresh.Claim, fresh.UID = 2, "fresh-claim", "recreated-template"
	setIntentLeader(t, ctx, client, &fresh)
	require.NoError(t, publisher.ClaimDeploymentAuthority(ctx, &fresh))
	require.Error(t, publisher.CheckDeploymentIntent(ctx, &fresh, "previous-plan", "previous-checksum"))
	require.NoError(t, publisher.RecordDeploymentIntent(ctx, &fresh, "new-plan", "new-checksum"))
	require.NoError(t, publisher.CheckDeploymentIntent(ctx, &fresh, "new-plan", "new-checksum"))
}
