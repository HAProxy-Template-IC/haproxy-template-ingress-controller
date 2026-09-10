// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package statusapplier

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func deltaPatch(name, resourceVersion, owner string) templating.StatusPatch {
	return templating.StatusPatch{
		Namespace: "default", Name: name, APIVersion: "networking.k8s.io/v1", Kind: "Ingress",
		UID: "uid-" + name, ResourceVersion: resourceVersion,
		Variants: map[string]map[string]any{"rendered": {"owner": owner}},
	}
}

func patchedNames(t *testing.T, action k8stesting.Action) string {
	t.Helper()
	patchAction, ok := action.(k8stesting.PatchAction)
	require.True(t, ok)
	return patchAction.GetName()
}

// TestSnapshotsApplyOnlyWhatChangedSinceTheLastOne pins the applier to the
// delta between consecutive snapshots of a phase: an unchanged patch is not
// materialized or sent again, a changed or new one is, and a patch that failed
// to apply is retried with the next snapshot even when it did not change.
func TestSnapshotsApplyOnlyWhatChangedSinceTheLastOne(t *testing.T) {
	bus := testutil.NewTestBus()
	fakeClient := newFakeDynamicClientWithPatchSuccess()
	var sentMu sync.Mutex
	var sent []string
	var failBeta atomic.Bool
	fakeClient.PrependReactor("patch", "ingresses", func(action k8stesting.Action) (bool, runtime.Object, error) {
		name := patchedNames(t, action)
		sentMu.Lock()
		sent = append(sent, name)
		sentMu.Unlock()
		if name == "beta" && failBeta.Load() {
			return true, nil, errors.New("apiserver unavailable")
		}
		return false, nil, nil
	})
	comp := newTestComponent(bus, fakeClient, newTestResolver())
	eventChan := bus.Subscribe("test", 50)
	bus.Start()
	setLeader(comp)
	ctx := context.Background()

	apply := func(t *testing.T, patches ...templating.StatusPatch) *events.StatusUpdateCompletedEvent {
		t.Helper()
		sentMu.Lock()
		sent = nil
		sentMu.Unlock()
		snapshot := newTestStatusPatchSnapshotFromPatches(t, patches)
		comp.applyStatusPatchSet(ctx, nil, snapshot, events.StatusPatchPhaseRendered)
		return testutil.WaitForEvent[*events.StatusUpdateCompletedEvent](t, eventChan, testutil.EventTimeout)
	}

	first := apply(t, deltaPatch("alpha", "1", "a"), deltaPatch("beta", "1", "b"))
	assert.Equal(t, 2, first.AppliedCount, "the first snapshot of a term applies everything")
	assert.ElementsMatch(t, []string{"alpha", "beta"}, sent)

	second := apply(t, deltaPatch("alpha", "1", "a"), deltaPatch("beta", "1", "b2"), deltaPatch("gamma", "1", "g"))
	assert.Equal(t, 2, second.AppliedCount, "only the changed and the new patch are applied")
	assert.ElementsMatch(t, []string{"beta", "gamma"}, sent, "the unchanged patch is not even sent")

	failBeta.Store(true)
	third := apply(t, deltaPatch("alpha", "1", "a"), deltaPatch("beta", "2", "b3"), deltaPatch("gamma", "1", "g"))
	assert.Equal(t, 0, third.AppliedCount)
	assert.ElementsMatch(t, []string{"beta"}, sent)

	failBeta.Store(false)
	fourth := apply(t, deltaPatch("alpha", "1", "a"), deltaPatch("beta", "2", "b3"), deltaPatch("gamma", "1", "g"))
	assert.Equal(t, 1, fourth.AppliedCount, "the failed patch is retried although nothing changed")
	assert.ElementsMatch(t, []string{"beta"}, sent)

	fifth := apply(t, deltaPatch("alpha", "1", "a"), deltaPatch("beta", "2", "b3"), deltaPatch("gamma", "1", "g"))
	assert.Equal(t, 0, fifth.AppliedCount)
	assert.Empty(t, sent, "a snapshot with nothing new sends nothing")

	comp.handleBecameLeader(ctx)
	sixth := apply(t, deltaPatch("alpha", "1", "a"), deltaPatch("beta", "2", "b3"), deltaPatch("gamma", "1", "g"))
	assert.Equal(t, 3, sixth.AppliedCount, "a new term starts from the whole snapshot")
}

func TestRetriesUseOnlyTheCurrentSnapshot(t *testing.T) {
	failed := deltaPatch("beta", "1", "old")
	otherPhase := deltaPatch("beta", "1", "deployed")
	otherPhase.Variants = map[string]map[string]any{"deployed": {"owner": "deployed"}}
	stable := deltaPatch("alpha", "1", "stable")
	tests := []struct {
		name    string
		current []templating.StatusPatch
		want    []string
	}{
		{"removed target", []templating.StatusPatch{stable}, nil},
		{"removed phase", []templating.StatusPatch{stable, otherPhase}, nil},
		{"empty snapshot", nil, nil},
		{"unchanged failed target", []templating.StatusPatch{stable, failed}, []string{"beta"}},
		{"new failed target revision", []templating.StatusPatch{stable, deltaPatch("beta", "2", "new")}, []string{"beta"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bus := testutil.NewTestBus()
			client := newFakeDynamicClientWithPatchSuccess()
			comp := newTestComponent(bus, client, newTestResolver())
			setLeader(comp)
			previous := newTestStatusPatchSnapshotFromPatches(t, []templating.StatusPatch{stable, failed})
			comp.rememberAppliedPhase(events.StatusPatchPhaseRendered, previous, []templating.StatusPatch{failed})
			current := newTestStatusPatchSnapshotFromPatches(t, test.current)
			comp.applyStatusPatchSet(context.Background(), nil, current, events.StatusPatchPhaseRendered)
			var sent []string
			for _, action := range client.Actions() {
				if action.GetVerb() == "patch" {
					sent = append(sent, patchedNames(t, action))
				}
			}
			assert.ElementsMatch(t, test.want, sent)
			snapshot, retries := comp.takeAppliedPhase(events.StatusPatchPhaseRendered)
			assert.Same(t, current, snapshot)
			assert.Empty(t, retries)
		})
	}
}
