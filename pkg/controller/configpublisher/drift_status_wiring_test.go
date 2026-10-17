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

package configpublisher

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	coordinationv1 "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/utils/ptr"

	v1alpha1 "github.com/haproxy-haptic/HAPTIC/pkg/apis/haproxytemplate/v1alpha1"
	"github.com/haproxy-haptic/HAPTIC/pkg/controller/deployer"
	"github.com/haproxy-haptic/HAPTIC/pkg/controller/events"
	leaderelectionctrl "github.com/haproxy-haptic/HAPTIC/pkg/controller/leaderelection"
	"github.com/haproxy-haptic/HAPTIC/pkg/controller/metrics"
	"github.com/haproxy-haptic/HAPTIC/pkg/controller/rendercycle"
	"github.com/haproxy-haptic/HAPTIC/pkg/controller/testutil"
	coreconfig "github.com/haproxy-haptic/HAPTIC/pkg/core/config"
	"github.com/haproxy-haptic/HAPTIC/pkg/dataplane"
	"github.com/haproxy-haptic/HAPTIC/pkg/dataplane/agent/agenttest"
	"github.com/haproxy-haptic/HAPTIC/pkg/dataplane/auxiliaryfiles"
	"github.com/haproxy-haptic/HAPTIC/pkg/dataplane/renderoutput"
	"github.com/haproxy-haptic/HAPTIC/pkg/dataplane/renderplan"
	busevents "github.com/haproxy-haptic/HAPTIC/pkg/events"
	"github.com/haproxy-haptic/HAPTIC/pkg/generated/clientset/versioned/fake"
	"github.com/haproxy-haptic/HAPTIC/pkg/k8s/configpublisher"
)

const (
	driftNamespace = "haptic"
	driftLease     = "haptic-leader"
	driftTimeout   = 10 * time.Second
	driftPoll      = 10 * time.Millisecond
)

var driftObjectKinds = []string{"haproxycfgs", "haproxymapfiles", "haproxygeneralfiles", "haproxycrtlistfiles"}

type acceptAllOutputs struct{}

func (acceptAllOutputs) CheckOutput(context.Context, *renderoutput.Snapshot, string) error {
	return nil
}

// statusRequestLog counts per-pod status applies by resource and field manager
// and records the counts at every HAProxyCfg GET. Before and after #376, a
// drift status update without listers GETs the HAProxyCfg once and applies
// auxiliary entries only after that GET; nothing else GETs it during drift
// rounds.
type statusRequestLog struct {
	mu      sync.Mutex
	applies map[string]int
	atGet   []map[string]int
}

func applyKey(resource, fieldManager string) string { return resource + " " + fieldManager }

func installStatusRequestLog(c *fake.Clientset) *statusRequestLog {
	log := &statusRequestLog{applies: map[string]int{}}
	c.PrependReactor("*", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		log.mu.Lock()
		defer log.mu.Unlock()
		resource := action.GetResource().Resource
		switch {
		case action.GetVerb() == "patch" && action.GetSubresource() == "status" &&
			action.(k8stesting.PatchAction).GetPatchType() == types.ApplyPatchType:
			log.applies[applyKey(resource, action.(k8stesting.PatchActionImpl).PatchOptions.FieldManager)]++
		case action.GetVerb() == "get" && resource == "haproxycfgs":
			log.atGet = append(log.atGet, maps.Clone(log.applies))
		}
		return false, nil, nil
	})
	return log
}

func (l *statusRequestLog) gets() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.atGet)
}

// between returns the applies issued between two GETs for the given pods.
func (l *statusRequestLog) between(from, to int, pods ...dataplane.Endpoint) map[string]int {
	l.mu.Lock()
	defer l.mu.Unlock()
	issued := map[string]int{}
	for _, kind := range driftObjectKinds {
		for i := range pods {
			key := applyKey(kind, "haptic-pod-status-"+pods[i].PodName)
			issued[kind] += l.atGet[to][key] - l.atGet[from][key]
		}
	}
	return issued
}

func applies(cfg, mapFiles, generalFiles, crtListFiles int) map[string]int {
	return map[string]int{
		"haproxycfgs": cfg, "haproxymapfiles": mapFiles,
		"haproxygeneralfiles": generalFiles, "haproxycrtlistfiles": crtListFiles,
	}
}

// driftWiring runs the production deployer, config publisher component and
// pure publisher against fake API servers and fake HAProxy agents.
type driftWiring struct {
	crd        *fake.Clientset
	kube       *k8sfake.Clientset
	bus        *busevents.EventBus
	completed  <-chan busevents.Event
	published  <-chan busevents.Event
	occurrence *rendercycle.Occurrence
	pods       []dataplane.Endpoint
	log        *statusRequestLog
	template   *v1alpha1.HAProxyTemplateConfig
	stopTerm   context.CancelFunc
	termDone   chan struct{}
}

// newDriftWiring starts the wiring with two HAProxy pods.
func newDriftWiring(t *testing.T) *driftWiring {
	t.Helper()
	logger := testutil.NewTestLogger()
	kube := k8sfake.NewClientset(&coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{Name: driftLease, Namespace: driftNamespace},
		Spec:       coordinationv1.LeaseSpec{HolderIdentity: ptr.To("leader-a")},
	})
	epoch := leaderelectionctrl.NewLeaseEpoch(kube, driftNamespace, driftLease, "leader-a", logger)
	require.NoError(t, epoch.Bump(t.Context()))
	crd := fake.NewSimpleClientset()
	installSSAListMapMergeReactor(crd)

	w := &driftWiring{
		crd: crd, kube: kube, bus: testutil.NewTestBus(), log: installStatusRequestLog(crd),
		template: &v1alpha1.HAProxyTemplateConfig{ObjectMeta: metav1.ObjectMeta{
			Name: "haptic", Namespace: driftNamespace, UID: "template-uid",
		}},
	}
	w.completed = w.bus.SubscribeTypes("drift-wiring-test", 100, events.EventTypeDeploymentCompleted)
	w.published = w.bus.SubscribeTypes("drift-wiring-published", 10, events.EventTypeConfigPublished)
	w.bus.Start()

	domainMetrics := metrics.NewMetrics(prometheus.NewRegistry())
	publisher := configpublisher.NewWithListers(kube, crd, nil, logger)
	stack := deployer.NewDeployStack(w.bus, &coreconfig.Config{}, logger, domainMetrics, nil,
		leaderelectionctrl.NewTerm(epoch, func(string) {}), nil)
	stack.EnableRetainedRecovery(publisher, acceptAllOutputs{}, w.template, domainMetrics, driftLease)
	ctx, cancel := context.WithCancel(context.Background())
	deployerDone := make(chan struct{})
	go func() {
		defer close(deployerDone)
		_ = stack.Deployer.Start(ctx)
	}()
	t.Cleanup(func() {
		w.stop()
		cancel()
		<-deployerDone
	})
	waitReady(t, stack.Deployer.SubscriptionReady())
	w.startTerm(t, publisher)

	occurrence, err := rendercycle.NewOccurrence(auxiliarySnapshot(t))
	require.NoError(t, err)
	w.occurrence = occurrence
	w.addPod(t)
	w.addPod(t)
	return w
}

// auxiliarySnapshot is a render with one map, one general and one crt-list file.
func auxiliarySnapshot(t *testing.T) *rendercycle.Snapshot {
	t.Helper()
	const config = "global\n  maxconn 100\n"
	files := []struct{ kind, path, content string }{
		{renderplan.FileKindMap, "maps/host.map", "example.com be_a\n"},
		{renderplan.FileKindGeneral, "general/503.http", "HTTP/1.0 503\r\n\r\n"},
		{renderplan.FileKindCRTList, "ssl/crt-list.txt", "ssl/x.pem example.com\n"},
	}
	plan := &renderplan.Plan{
		SchemaVersion: renderplan.SchemaVersion,
		Sections: []renderplan.Section{{
			Kind: renderplan.SectionKindCore, Name: "core#0", Text: config,
			TextKnown: true, TextDigest: renderplan.DigestString(config), Length: len(config),
		}},
		Maps: map[string]renderplan.Map{files[0].path: {
			Path: files[0].path, Ordered: true, Entries: renderplan.ParseMapEntries(files[0].content),
		}},
		Files: []renderplan.File{{
			Path: renderplan.ConfigFilePath, Kind: renderplan.FileKindConfig,
			ReloadOnChange: true, Content: config, ContentKnown: true,
			Digest: renderplan.DigestString(config), Size: int64(len(config)),
		}},
	}
	for _, file := range files {
		plan.Files = append(plan.Files, renderplan.File{
			Path: file.path, Kind: file.kind, ReloadOnChange: file.kind == renderplan.FileKindGeneral,
			Content: file.content, ContentKnown: true,
			Digest: renderplan.DigestString(file.content), Size: int64(len(file.content)),
		})
	}
	plan.ComputeID()
	fixture := testutil.NewRenderCycleFixture(t)
	artifacts := fixture.Artifacts(t, &dataplane.AuxiliaryFiles{
		MapFiles:     []auxiliaryfiles.MapFile{{Path: files[0].path, Content: files[0].content}},
		GeneralFiles: []auxiliaryfiles.GeneralFile{{Filename: "503.http", Path: files[1].path, Content: files[1].content}},
		CRTListFiles: []auxiliaryfiles.CRTListFile{{Path: files[2].path, Content: files[2].content}},
	}, nil)
	return fixture.SnapshotWithEffects(t, config, plan, artifacts, nil, nil, nil, nil)
}

// startTerm starts a leadership term of the config publisher component.
func (w *driftWiring) startTerm(t *testing.T, publisher *configpublisher.Publisher) {
	t.Helper()
	component := New(publisher, w.bus, testutil.NewTestLogger(),
		WithPublishInterval(0), WithTemplateConfig(w.template))
	ctx, cancel := context.WithCancel(context.Background())
	w.stopTerm, w.termDone = cancel, make(chan struct{})
	go func(done chan struct{}) {
		defer close(done)
		_ = component.Start(ctx)
	}(w.termDone)
	waitReady(t, component.SubscriptionReady())
}

func waitReady(t *testing.T, ready <-chan struct{}) {
	t.Helper()
	select {
	case <-ready:
	case <-time.After(driftTimeout):
		t.Fatal("component did not subscribe")
	}
}

// stop ends the term once its status workers have finished.
func (w *driftWiring) stop() {
	if w.stopTerm != nil {
		w.stopTerm()
		<-w.termDone
		w.stopTerm = nil
	}
}

func (w *driftWiring) addPod(t *testing.T) {
	t.Helper()
	agent := agenttest.New(t)
	index := len(w.pods)
	w.pods = append(w.pods, dataplane.Endpoint{
		URL: agent.URL(), Username: agent.Username(), Password: agent.Password(),
		PodName: fmt.Sprintf("haproxy-%d", index), PodNamespace: driftNamespace,
		PodUID: fmt.Sprintf("haproxy-%d-uid", index), PodRuntimeID: fmt.Sprintf("runtime-%d", index),
	})
}

func (w *driftWiring) deployTo(t *testing.T, reason string) {
	t.Helper()
	scheduled, err := events.NewDeploymentScheduledEventWithCycle(w.occurrence, w.pods,
		configpublisher.GenerateRuntimeConfigName(w.template.Name), driftNamespace, reason, false)
	require.NoError(t, err)
	w.bus.Publish(scheduled)
	result := testutil.WaitForEvent[*events.DeploymentCompletedEvent](t, w.completed, driftTimeout)
	require.Equal(t, len(w.pods), result.Succeeded)
}

// driftRound runs one drift-prevention round, waits until every pod's status
// update for it has started, and returns the index of the round's last GET.
//
// A pod's updates run one at a time, so the applies between the last GETs of
// rounds a and b include every apply of rounds a+1 to b-1 and at most those of
// rounds a and b: measurements are bracketed by idle rounds.
func (w *driftWiring) driftRound(t *testing.T) int {
	t.Helper()
	want := w.log.gets() + len(w.pods)
	w.deployTo(t, events.TriggerReasonDriftPrevention)
	require.Eventually(t, func() bool { return w.log.gets() >= want }, driftTimeout, driftPoll,
		"every pod's drift status update must run")
	require.Equal(t, want, w.log.gets(), "only drift status updates may read the HAProxyCfg")
	return want - 1
}

// deployed deploys to every pod, waits for their entries and for the
// publication, whose retained checkpoint step reads HAProxyCfgs, and settles:
// the first drift round may record the drift pass's own apply mode on the
// HAProxyCfg. It returns the last GET of the idle round that follows.
func (w *driftWiring) deployed(t *testing.T) int {
	t.Helper()
	w.deployTo(t, "config_validation")
	w.waitStamped(t)
	testutil.WaitForEvent[*events.ConfigPublishedEvent](t, w.published, driftTimeout)
	w.driftRound(t)
	return w.driftRound(t)
}

func (w *driftWiring) objects(t *testing.T) []runtime.Object {
	t.Helper()
	client := w.crd.HaproxyTemplateICV1alpha1()
	cfg, err := client.HAProxyCfgs(driftNamespace).Get(t.Context(),
		configpublisher.GenerateRuntimeConfigName(w.template.Name), metav1.GetOptions{})
	if err != nil || cfg.Status.AuxiliaryFiles == nil {
		return nil
	}
	objects := []runtime.Object{cfg}
	refs := cfg.Status.AuxiliaryFiles
	for _, ref := range refs.MapFiles {
		if obj, err := client.HAProxyMapFiles(driftNamespace).Get(t.Context(), ref.Name, metav1.GetOptions{}); err == nil {
			objects = append(objects, obj)
		}
	}
	for _, ref := range refs.GeneralFiles {
		if obj, err := client.HAProxyGeneralFiles(driftNamespace).Get(t.Context(), ref.Name, metav1.GetOptions{}); err == nil {
			objects = append(objects, obj)
		}
	}
	for _, ref := range refs.CRTListFiles {
		if obj, err := client.HAProxyCRTListFiles(driftNamespace).Get(t.Context(), ref.Name, metav1.GetOptions{}); err == nil {
			objects = append(objects, obj)
		}
	}
	return objects
}

// waitStamped waits until the HAProxyCfg and its three auxiliary files carry
// an entry for every pod. Its own GETs come between rounds.
func (w *driftWiring) waitStamped(t *testing.T) {
	t.Helper()
	require.Eventually(t, func() bool {
		objects := w.objects(t)
		if len(objects) != len(driftObjectKinds) {
			return false
		}
		for _, obj := range objects {
			stamped := getDeployedToPods(obj)
			for i := range w.pods {
				if !slices.ContainsFunc(stamped, func(pod v1alpha1.PodDeploymentStatus) bool { return pod.PodName == w.pods[i].PodName }) {
					return false
				}
			}
		}
		return true
	}, driftTimeout, driftPoll, "every pod's entry must land on every object")
}

func (w *driftWiring) removeMapFileEntry(t *testing.T, podName string) {
	t.Helper()
	for _, obj := range w.objects(t) {
		mapFile, ok := obj.(*v1alpha1.HAProxyMapFile)
		if !ok {
			continue
		}
		mapFile.Status.DeployedToPods = slices.DeleteFunc(mapFile.Status.DeployedToPods,
			func(pod v1alpha1.PodDeploymentStatus) bool { return pod.PodName == podName })
		_, err := w.crd.HaproxyTemplateICV1alpha1().HAProxyMapFiles(driftNamespace).UpdateStatus(t.Context(), mapFile, metav1.UpdateOptions{})
		require.NoError(t, err)
		return
	}
	t.Fatal("no map file published")
}

// TestFakeRefusesStaleStatusUpdate: the wiring's read-modify-write of
// status.retainedConfigs keeps a pod entry applied between its read and its
// update only because that update conflicts, as on a real API server.
func TestFakeRefusesStaleStatusUpdate(t *testing.T) {
	crd := fake.NewSimpleClientset()
	installSSAListMapMergeReactor(crd)
	client := crd.HaproxyTemplateICV1alpha1().HAProxyCfgs(driftNamespace)
	_, err := client.Create(t.Context(), &v1alpha1.HAProxyCfg{
		ObjectMeta: metav1.ObjectMeta{Name: "cfg", Namespace: driftNamespace},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	read, err := client.Get(t.Context(), "cfg", metav1.GetOptions{})
	require.NoError(t, err)
	apply := fmt.Sprintf(`{"apiVersion":"haproxy-haptic.org/v1alpha1","kind":"HAProxyCfg",`+
		`"metadata":{"name":"cfg","namespace":%q},"status":{"deployedToPods":[{"podName":"haproxy-0"}]}}`, driftNamespace)
	_, err = client.Patch(t.Context(), "cfg", types.ApplyPatchType, []byte(apply),
		metav1.PatchOptions{FieldManager: "haptic-pod-status-haproxy-0"}, "status")
	require.NoError(t, err)

	read.Status.RetainedConfigs = []v1alpha1.RetainedConfigReference{{Name: "checkpoint"}}
	_, err = client.UpdateStatus(t.Context(), read, metav1.UpdateOptions{})
	require.True(t, apierrors.IsConflict(err), "a stale status update must conflict, got %v", err)
	current, err := client.Get(t.Context(), "cfg", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Len(t, current.Status.DeployedToPods, 1)
}

// TestIdleDriftRoundsApplyNoPodStatus is issue #376 through the production
// deployer and publisher: drift rounds over an unchanged fleet send no
// per-pod status applies.
func TestIdleDriftRoundsApplyNoPodStatus(t *testing.T) {
	w := newDriftWiring(t)
	start := w.deployed(t)

	end := start
	for range 5 {
		end = w.driftRound(t)
	}

	assert.Equal(t, applies(0, 0, 0, 0), w.log.between(start, end, w.pods...),
		"idle drift rounds must apply nothing (before #376: one apply per pod and object per round)")
}

// TestDriftRoundRestoresRemovedEntry: an entry another writer removed is
// re-applied in the next round, and only that one.
func TestDriftRoundRestoresRemovedEntry(t *testing.T) {
	w := newDriftWiring(t)
	start := w.deployed(t)

	w.removeMapFileEntry(t, "haproxy-0")
	w.driftRound(t)
	end := w.driftRound(t)

	assert.Equal(t, applies(0, 1, 0, 0), w.log.between(start, end, w.pods[0]), "the next round re-applies exactly the removed entry")
	assert.Equal(t, applies(0, 0, 0, 0), w.log.between(start, end, w.pods[1]))
	w.waitStamped(t)
}

// TestDriftRoundStampsNewPod: a pod that joins between rounds gets its entries
// in the next round while the other pods stay idle.
func TestDriftRoundStampsNewPod(t *testing.T) {
	w := newDriftWiring(t)
	start := w.deployed(t)

	w.addPod(t)
	w.driftRound(t)
	end := w.driftRound(t)

	assert.Equal(t, applies(0, 0, 0, 0), w.log.between(start, end, w.pods[:2]...), "the other pods stay idle")
	added := w.log.between(start, end, w.pods[2])
	assert.GreaterOrEqual(t, added["haproxycfgs"], 1, "the new pod gets its HAProxyCfg entry")
	delete(added, "haproxycfgs")
	assert.Equal(t, map[string]int{"haproxymapfiles": 1, "haproxygeneralfiles": 1, "haproxycrtlistfiles": 1}, added,
		"the new pod gets each auxiliary entry once")
	w.waitStamped(t)
}

// TestDriftRoundAfterLeaderChangeReassertsOnce: a new leader has no memory of
// what the old one applied. Its first round re-applies every entry, restoring
// one removed while no leader ran, and its next rounds are idle.
func TestDriftRoundAfterLeaderChangeReassertsOnce(t *testing.T) {
	w := newDriftWiring(t)
	w.deployed(t)

	w.stop()
	w.removeMapFileEntry(t, "haproxy-1")
	start := w.log.gets() - 1
	w.startTerm(t, configpublisher.NewWithListers(w.kube, w.crd, nil, testutil.NewTestLogger()))
	w.driftRound(t)
	idle := w.driftRound(t)

	for i := range w.pods {
		assert.Equal(t, applies(1, 1, 1, 1), w.log.between(start, idle, w.pods[i]),
			"the new leader re-asserts %s's entries once", w.pods[i].PodName)
	}
	w.waitStamped(t)

	end := idle
	for range 3 {
		end = w.driftRound(t)
	}
	assert.Equal(t, applies(0, 0, 0, 0), w.log.between(idle, end, w.pods...), "and then idles")
}
