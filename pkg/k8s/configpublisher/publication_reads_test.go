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
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8stesting "k8s.io/client-go/testing"

	v1 "github.com/haproxy-haptic/HAPTIC/pkg/apis/haproxytemplate/v1alpha1"
	"github.com/haproxy-haptic/HAPTIC/pkg/dataplane/auxiliaryfiles"
)

var (
	auxiliaryChildResources = map[string]bool{"haproxymapfiles": true, "haproxygeneralfiles": true, "haproxycrtlistfiles": true, "secrets": true}
	mapFileResource         = schema.GroupVersionResource{Group: "haproxy-haptic.org", Version: "v1alpha1", Resource: "haproxymapfiles"}
)

// Issue #356: with unchanged auxiliary content, a publication cycle (the
// ordinary publication, then its checkpoint, as attemptPublication runs them)
// reads children with LISTs per kind and never GETs them one by one.
func TestPublicationCycleReadsAuxiliaryChildrenPerKind(t *testing.T) {
	const cycles, extraMaps = 6, 24
	ctx, kube, crd, publisher := newTestPublisher(t)
	publisher.SetRepublishInterval(time.Hour)
	requests := map[string]int{}
	for cycle := range cycles {
		req := retainedPublishRequestWithMaps(t, fmt.Sprintf("global\n  maxconn %d\n", 1000+cycle), extraMaps)
		crd.ClearActions()
		kube.ClearActions()
		_, err := publisher.PublishConfig(ctx, &req)
		require.NoError(t, err)
		require.NoError(t, publisher.PublishRetained(ctx, &req))
		if cycle == 0 {
			continue
		}
		for _, action := range append(crd.Actions(), kube.Actions()...) {
			if auxiliaryChildResources[action.GetResource().Resource] {
				requests[action.GetVerb()+" "+action.GetResource().Resource]++
			}
		}
	}

	steady := cycles - 1
	assert.Equal(t, map[string]int{
		"list haproxymapfiles":     3 * steady,
		"list haproxygeneralfiles": 3 * steady,
		"list haproxycrtlistfiles": 3 * steady,
		"list secrets":             3 * steady,
	}, requests, "per cycle: one ordinary LIST, one checkpoint publication LIST and one verification LIST per kind")
	loaded, err := publisher.LoadRetained(ctx, "default", "test-config", "test-uid-123")
	require.NoError(t, err)
	require.Len(t, loaded, 2)
}

func mapFilePublishRequest(content ...string) PublishRequest {
	req := basePublishRequest()
	req.AuxiliaryFiles = &AuxiliaryFiles{}
	for i, entries := range content {
		req.AuxiliaryFiles.MapFiles = append(req.AuxiliaryFiles.MapFiles,
			auxiliaryfiles.MapFile{Path: fmt.Sprintf("/maps/%d.map", i), Content: entries})
	}
	return req
}

// afterMapList runs mutate once, after the next map-file LIST has been answered.
func afterMapList(t *testing.T, crd interface {
	PrependReactor(string, string, k8stesting.ReactionFunc)
	Tracker() k8stesting.ObjectTracker
}, mutate func()) {
	t.Helper()
	done := false
	crd.PrependReactor("list", "haproxymapfiles", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if done {
			return false, nil, nil
		}
		done = true
		handled, listed, err := k8stesting.ObjectReaction(crd.Tracker())(action)
		mutate()
		return handled, listed, err
	})
}

func TestPublicationRecreatesChildDeletedAfterListing(t *testing.T) {
	ctx, _, crd, publisher := newTestPublisher(t)
	req := mapFilePublishRequest("a backend\n")
	result, err := publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)
	name := result.MapFileNames[0]
	afterMapList(t, crd, func() {
		require.NoError(t, crd.Tracker().Delete(mapFileResource, "default", name))
	})

	_, err = publisher.PublishConfig(ctx, &req)
	require.NoError(t, err, "a deletion after the listing is the same race as one after a GET")
	_, err = publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)
	_, err = crd.HaproxyTemplateICV1alpha1().HAProxyMapFiles("default").Get(ctx, name, metav1.GetOptions{})
	require.NoError(t, err, "the next publication lists the child as absent and creates it")
}

func TestPublicationRetriesChildChangedAfterListing(t *testing.T) {
	ctx, _, crd, publisher := newTestPublisher(t)
	req := mapFilePublishRequest("a backend\n")
	result, err := publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)
	name := result.MapFileNames[0]
	maps := crd.HaproxyTemplateICV1alpha1().HAProxyMapFiles("default")
	tamper := func(entries, version string) {
		obj, err := crd.Tracker().Get(mapFileResource, "default", name)
		require.NoError(t, err)
		file := obj.(*v1.HAProxyMapFile)
		file.Spec.Entries, file.ResourceVersion = entries, version
		require.NoError(t, crd.Tracker().Update(mapFileResource, file, "default"))
	}
	tamper("tampered\n", "1")
	afterMapList(t, crd, func() { tamper("tampered again\n", "2") })
	crd.PrependReactor("update", "haproxymapfiles", func(action k8stesting.Action) (bool, runtime.Object, error) {
		written := action.(k8stesting.UpdateAction).GetObject().(*v1.HAProxyMapFile)
		current, err := crd.Tracker().Get(mapFileResource, "default", name)
		require.NoError(t, err)
		if written.ResourceVersion != current.(*v1.HAProxyMapFile).ResourceVersion {
			return true, nil, apierrors.NewConflict(mapFileResource.GroupResource(), name, errors.New("changed after list"))
		}
		return false, nil, nil
	})
	crd.ClearActions()

	_, err = publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)
	var verbs []string
	for _, action := range crd.Actions() {
		if action.GetResource().Resource == "haproxymapfiles" {
			verbs = append(verbs, action.GetVerb())
		}
	}
	assert.Equal(t, []string{"list", "update", "get", "update"}, verbs, "a stale listed copy is refused and the retry reads live")
	repaired, err := maps.Get(ctx, name, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "a backend\n", repaired.Spec.Entries)
}

func TestPublicationStatusOnlyChangeNeedsNoChildRead(t *testing.T) {
	ctx, _, crd, publisher := newTestPublisher(t)
	req := mapFilePublishRequest("a backend\n", "b backend\n")
	result, err := publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)
	maps := crd.HaproxyTemplateICV1alpha1().HAProxyMapFiles("default")
	for _, name := range result.MapFileNames {
		file, err := maps.Get(ctx, name, metav1.GetOptions{})
		require.NoError(t, err)
		file.Status.DeployedToPods = []v1.PodDeploymentStatus{{PodName: "haproxy-0", Checksum: file.Spec.Checksum}}
		_, err = maps.UpdateStatus(ctx, file, metav1.UpdateOptions{})
		require.NoError(t, err)
	}
	crd.ClearActions()

	req.Checksum = "new-config"
	_, err = publisher.PublishConfig(ctx, &req)
	require.NoError(t, err)
	var verbs []string
	for _, action := range crd.Actions() {
		if action.GetResource().Resource == "haproxymapfiles" {
			verbs = append(verbs, action.GetVerb())
		}
	}
	assert.Equal(t, []string{"list"}, verbs)
	for _, name := range result.MapFileNames {
		file, err := maps.Get(ctx, name, metav1.GetOptions{})
		require.NoError(t, err)
		assert.Len(t, file.Status.DeployedToPods, 1)
	}
}

func TestPublicationPrunesChildCreatedAfterListingNextTime(t *testing.T) {
	ctx, _, crd, publisher := newTestPublisher(t)
	stale := mapFilePublishRequest("a backend\n", "b backend\n")
	staleResult, err := publisher.PublishConfig(ctx, &stale)
	require.NoError(t, err)
	maps := crd.HaproxyTemplateICV1alpha1().HAProxyMapFiles("default")
	late, err := maps.Get(ctx, staleResult.MapFileNames[1], metav1.GetOptions{})
	require.NoError(t, err)
	require.NoError(t, maps.Delete(ctx, late.Name, metav1.DeleteOptions{}))
	late.ResourceVersion = ""

	current := mapFilePublishRequest("a backend\n")
	current.Checksum = "current"
	afterMapList(t, crd, func() {
		require.NoError(t, crd.Tracker().Create(mapFileResource, late, "default"))
	})
	_, err = publisher.PublishConfig(ctx, &current)
	require.NoError(t, err)
	_, err = maps.Get(ctx, late.Name, metav1.GetOptions{})
	require.NoError(t, err, "a child created after the listing is not judged by this publication")

	_, err = publisher.PublishConfig(ctx, &current)
	require.NoError(t, err)
	_, err = maps.Get(ctx, late.Name, metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err), "the next publication prunes it")
}
