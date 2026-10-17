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
	"bytes"
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/haproxy-haptic/HAPTIC/pkg/controller/events"
	"github.com/haproxy-haptic/HAPTIC/pkg/controller/testutil"
	"github.com/haproxy-haptic/HAPTIC/pkg/controller/throttle"
	"github.com/haproxy-haptic/HAPTIC/pkg/dataplane/auxiliaryfiles"
	"github.com/haproxy-haptic/HAPTIC/pkg/generated/clientset/versioned/fake"
	configpublisher "github.com/haproxy-haptic/HAPTIC/pkg/k8s/configpublisher"
)

// holdAuxiliaryReferences refuses a publication's status.auxiliaryFiles patch
// until release is called, leaving the HAProxyCfg written without them.
func holdAuxiliaryReferences(c *fake.Clientset) (release func()) {
	var held atomic.Bool
	held.Store(true)
	c.PrependReactor("patch", "haproxycfgs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		patch := action.(k8stesting.PatchAction)
		if !held.Load() || patch.GetPatchType() != types.JSONPatchType ||
			!bytes.Contains(patch.GetPatch(), []byte(`"/status/auxiliaryFiles"`)) {
			return false, nil, nil
		}
		return true, nil, apierrors.NewServiceUnavailable("auxiliary references held")
	})
	return func() { held.Store(false) }
}

// TestProcessStatusWork_RequeuesUntilRuntimeConfigPublished is the regression
// test for the startup race where the first deployment to the HAProxy pods
// completes before the initial HAProxyCfg publish lands. The per-pod status
// SSA then hits NotFound; the old code swallowed it as success and — because
// subsequent no-op deploys dedupe on the unchanged checksum — the pod's
// deployedToPods entry was lost until the next config change or drift check.
// Observed as the e2e initial-sync waiter timing out with 1/2 pods reported.
//
// The fix requeues the update and retries after statusWorkRetryDelay; this
// test drives the requeue + eventual success cycle synchronously.
func TestProcessStatusWork_RequeuesUntilRuntimeConfigPublished(t *testing.T) {
	ctx := context.Background()
	crdClient := fake.NewSimpleClientset()
	installSSAListMapMergeReactor(crdClient)
	publisher := configpublisher.NewWithListers(k8sfake.NewClientset(), crdClient, nil, testutil.NewTestLogger())

	c := &Component{
		logger:            testutil.NewTestLogger(),
		publisher:         publisher,
		statusWorkPending: make(map[string]*statusWorkItem),
		statusWorkTrigger: make(chan struct{}, 1),
		statusThrottle:    throttle.New(0),
	}

	event := &events.ConfigAppliedToPodEvent{
		RuntimeConfigName:      "test-config-haproxycfg",
		RuntimeConfigNamespace: "default",
		PodName:                "haproxy-1",
		PodNamespace:           "default",
		Checksum:               "abc123",
	}
	work := &statusWorkItem{event: event}

	// HAProxyCfg not published yet: the update must be requeued, not lost.
	c.processStatusWork(ctx, work)

	key := statusWorkKey(event)
	c.statusWorkPendingMu.Lock()
	requeued, pending := c.statusWorkPending[key]
	c.statusWorkPendingMu.Unlock()
	require.True(t, pending, "status update must be requeued while HAProxyCfg is not published")
	assert.Equal(t, 1, requeued.retries)

	// A newer event arriving meanwhile must not be clobbered by a second requeue.
	newer := &statusWorkItem{event: event}
	c.statusWorkPendingMu.Lock()
	c.statusWorkPending[key] = newer
	c.statusWorkPendingMu.Unlock()
	c.requeueStatusWork(ctx, work)
	c.statusWorkPendingMu.Lock()
	kept := c.statusWorkPending[key]
	c.statusWorkPendingMu.Unlock()
	assert.Same(t, newer, kept, "requeue must not overwrite a newer pending update")

	// The publication writes the HAProxyCfg before its auxiliary references:
	// the update is still requeued, or the pod's map file entry is lost.
	release := holdAuxiliaryReferences(crdClient)
	request := &configpublisher.PublishRequest{
		TemplateConfigName: "test-config", TemplateConfigNamespace: "default", TemplateConfigUID: "test-uid",
		Config: "global\n", ConfigPath: "/etc/haproxy/haproxy.cfg", Checksum: "abc123",
		AuxiliaryFiles: &configpublisher.AuxiliaryFiles{
			MapFiles: []auxiliaryfiles.MapFile{{Path: "/etc/haproxy/maps/host.map", Content: "example.com be_a\n"}},
		},
	}
	_, err := publisher.PublishConfig(ctx, request)
	require.Error(t, err)

	c.processAllPendingStatusWork(ctx)

	c.statusWorkPendingMu.Lock()
	_, pending = c.statusWorkPending[key]
	c.statusWorkPendingMu.Unlock()
	require.True(t, pending, "status update must be requeued while the HAProxyCfg has no auxiliary references")

	// Once the publication completes, the entry lands on every object.
	release()
	_, err = publisher.PublishConfig(ctx, request)
	require.NoError(t, err)

	c.processAllPendingStatusWork(ctx)

	cfg, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyCfgs("default").
		Get(ctx, "test-config-haproxycfg", metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, cfg.Status.DeployedToPods, 1)
	assert.Equal(t, "haproxy-1", cfg.Status.DeployedToPods[0].PodName)
	assert.Equal(t, "abc123", cfg.Status.DeployedToPods[0].Checksum)
	require.Len(t, cfg.Status.AuxiliaryFiles.MapFiles, 1)
	mapFile, err := crdClient.HaproxyTemplateICV1alpha1().HAProxyMapFiles("default").
		Get(ctx, cfg.Status.AuxiliaryFiles.MapFiles[0].Name, metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, mapFile.Status.DeployedToPods, 1)
	assert.Equal(t, "haproxy-1", mapFile.Status.DeployedToPods[0].PodName)

	c.statusWorkPendingMu.Lock()
	remaining := len(c.statusWorkPending)
	c.statusWorkPendingMu.Unlock()
	assert.Zero(t, remaining, "pending map must be drained after a successful apply")
}

// TestRequeueStatusWork_DropsAfterMaxRetries verifies the retry cap: an item
// whose HAProxyCfg never appears is eventually dropped instead of requeueing
// forever.
func TestRequeueStatusWork_DropsAfterMaxRetries(t *testing.T) {
	c := &Component{
		logger:            testutil.NewTestLogger(),
		statusWorkPending: make(map[string]*statusWorkItem),
		statusWorkTrigger: make(chan struct{}, 1),
	}

	event := &events.ConfigAppliedToPodEvent{
		RuntimeConfigName:      "gone-haproxycfg",
		RuntimeConfigNamespace: "default",
		PodName:                "haproxy-1",
	}
	work := &statusWorkItem{event: event, retries: statusWorkMaxRetries}

	c.requeueStatusWork(t.Context(), work)

	c.statusWorkPendingMu.Lock()
	_, pending := c.statusWorkPending[statusWorkKey(event)]
	c.statusWorkPendingMu.Unlock()
	assert.False(t, pending, "item at max retries must be dropped, not requeued")
}
